package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hhpatel14/migration-harness/internal/acp"
	"github.com/hhpatel14/migration-harness/internal/cliutil"
	"github.com/hhpatel14/migration-harness/internal/config"
	"github.com/hhpatel14/migration-harness/internal/git"
	"github.com/hhpatel14/migration-harness/internal/phases"
	"github.com/hhpatel14/migration-harness/internal/session"
)

const (
	repoDir    = "/workspace/repo"
	acpBaseURL = "http://localhost:4000"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		cliutil.Usage("usage: konveyor-harness run")
	}
	if err := run(); err != nil {
		cliutil.Fatal("konveyor-harness", err)
	}
}

func run() error {
	startedAt := time.Now()

	params, err := config.LoadParamsFromEnv()
	if err != nil {
		return fmt.Errorf("load params: %w", err)
	}
	creds, err := git.CredentialsFromEnv()
	if err != nil {
		return fmt.Errorf("load git credentials: %w", err)
	}

	if err := git.Clone(params.SourceURL, repoDir, creds, git.DefaultAskpassPath); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	if err := git.CheckoutOrCreateBranch(repoDir, params.TargetBranch); err != nil {
		return fmt.Errorf("checkout branch: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	if err := config.WriteGooseConfig(filepath.Join(homeDir, ".config", "goose")); err != nil {
		return fmt.Errorf("write goose config: %w", err)
	}
	secretKey, err := config.SecretKey()
	if err != nil {
		return fmt.Errorf("generate secret key: %w", err)
	}

	// launchGoose only depends on the goose config and secret key written
	// above, not on detect/push completing. Its process startup is
	// kicked off here (exec.Cmd.Start is non-blocking) so that goose boots
	// concurrently with runDetect + the detect-artifacts push below,
	// overlapping goose's own startup latency with that work instead of
	// paying for it serially. We still block on acp.WaitReady afterward,
	// but by then goose has had the whole detect+push duration to come up,
	// so the wait is typically near-instant.
	//
	// The gooseCmd/gooseStopped bookkeeping and cleanup defer are set up
	// immediately once the process is started — before we know whether
	// detect/push will succeed — so that a failure in that chain still
	// stops the already-running goose process rather than leaking it.
	gooseCmd, err := launchGoose(secretKey)
	if err != nil {
		return fmt.Errorf("launch goose: %w", err)
	}
	gooseStopped := false
	defer func() {
		if !gooseStopped && gooseCmd.Process != nil {
			gooseCmd.Process.Kill()
		}
	}()

	if err := runDetect(repoDir); err != nil {
		return fmt.Errorf("detect: %w", err)
	}
	if err := git.Push(repoDir, []string{"detect.json", "graph.json"}, "konveyor: detect phase", creds, git.DefaultAskpassPath); err != nil {
		return fmt.Errorf("push detect artifacts: %w", err)
	}

	instructionsPath := "/workspace/instructions.md"
	if err := os.WriteFile(instructionsPath, []byte(params.Instructions), 0644); err != nil {
		return fmt.Errorf("write instructions.md: %w", err)
	}

	pipeline := phases.DefaultPipeline()
	phasesPath := filepath.Join(repoDir, "phases.json")
	if err := phases.WriteJSON(phasesPath, pipeline); err != nil {
		return fmt.Errorf("write phases.json: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()

	if err := acp.WaitReady(ctx, acpBaseURL, 30*time.Second); err != nil {
		return fmt.Errorf("wait for acp: %w", err)
	}

	client, err := acp.Connect(ctx, acpBaseURL)
	if err != nil {
		return fmt.Errorf("connect to acp: %w", err)
	}
	defer client.Close(ctx)

	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("acp initialize: %w", err)
	}

	sessionID, err := client.NewSession(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}

	message := buildPromptMessage(params.SkillsDir, instructionsPath, phasesPath)
	promptResult, promptErr := client.Prompt(ctx, sessionID, message)

	var inputTokens, outputTokens int
	finalStatus := "failed"
	failureDetail := ""
	if promptErr != nil {
		failureDetail = promptErr.Error()
		fmt.Fprintf(os.Stderr, "konveyor-harness: warning: session/prompt failed: %v\n", promptErr)
	} else {
		inputTokens = promptResult.Usage.InputTokens
		outputTokens = promptResult.Usage.OutputTokens
		if promptResult.StopReason == "end_turn" {
			finalStatus = "complete"
		} else {
			failureDetail = fmt.Sprintf("stopReason=%q", promptResult.StopReason)
			fmt.Fprintf(os.Stderr, "konveyor-harness: warning: session ended with stopReason=%q\n", promptResult.StopReason)
		}
	}

	if stopErr := stopGoose(gooseCmd); stopErr != nil {
		fmt.Fprintf(os.Stderr, "konveyor-harness: warning: failed to stop goose serve cleanly: %v\n", stopErr)
	}
	gooseStopped = true

	completedSteps, failedSteps := phases.CheckCompletion(repoDir, pipeline)
	completedSteps = append([]string{"detect"}, completedSteps...)

	gitInfo := session.GitInfo{
		TargetBranch: params.TargetBranch,
	}
	if commits, err := git.CommitCount(repoDir); err != nil {
		fmt.Fprintf(os.Stderr, "konveyor-harness: warning: failed to get commit count: %v\n", err)
	} else {
		gitInfo.Commits = commits
	}
	if sha, err := git.HeadSHA(repoDir); err != nil {
		fmt.Fprintf(os.Stderr, "konveyor-harness: warning: failed to get HEAD sha: %v\n", err)
	} else {
		gitInfo.LastCommitSHA = sha
	}

	durationSeconds := int(time.Since(startedAt).Seconds())
	sess := session.Session{
		SessionID:       sessionID,
		Status:          finalStatus,
		StartedAt:       startedAt,
		CompletedAt:     time.Now(),
		DurationSeconds: durationSeconds,
		Runtime:         "goose",
		Models: []session.ModelUsage{{
			Role:     "primary",
			Provider: os.Getenv("GOOSE_PROVIDER"),
			Name:     os.Getenv("GOOSE_MODEL"),
			TokenUsage: session.TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			},
		}},
		StepsCompleted: completedSteps,
		StepsFailed:    failedSteps,
		Git:            gitInfo,
	}
	sessionPath := filepath.Join(repoDir, ".konveyor", "session.json")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0755); err != nil {
		return fmt.Errorf("create .konveyor dir: %w", err)
	}
	if err := sess.WriteTo(sessionPath); err != nil {
		return fmt.Errorf("write session.json: %w", err)
	}
	if err := git.Push(repoDir, []string{".konveyor/session.json"}, "konveyor: session metadata", creds, git.DefaultAskpassPath); err != nil {
		return fmt.Errorf("push session.json: %w", err)
	}

	exitCode := 0
	resultsStatus := "succeeded"
	if finalStatus != "complete" {
		exitCode = 1
		resultsStatus = "failed"
	}
	res := session.Results{
		Status:          resultsStatus,
		ExitCode:        exitCode,
		DurationSeconds: durationSeconds,
		Git:             sess.Git,
	}
	if err := os.MkdirAll("/.konveyor", 0755); err != nil {
		return fmt.Errorf("create /.konveyor: %w", err)
	}
	if err := res.WriteTo("/.konveyor/results.json"); err != nil {
		return fmt.Errorf("write results.json: %w", err)
	}

	return finalRunError(finalStatus, failureDetail)
}

// finalRunError reports whether the migration session itself succeeded,
// independent of whether all the harness's own bookkeeping (writing
// session.json/results.json, pushing metadata, etc.) succeeded. run()
// must propagate this as a non-nil error when the session did not
// complete, so that main() exits non-zero instead of masking a failed
// migration as a successful process exit. detail, when non-empty, carries
// the specific reason (a stopReason or the session/prompt error) for
// better diagnostics.
func finalRunError(status, detail string) error {
	if status == "complete" {
		return nil
	}
	if detail != "" {
		return fmt.Errorf("migration session did not complete successfully (status=%q): %s", status, detail)
	}
	return fmt.Errorf("migration session did not complete successfully (status=%q)", status)
}

// buildPromptMessage is the initial message sent via session/prompt,
// telling the orchestrator skill what to load and where to find its inputs.
func buildPromptMessage(skillsDir, instructionsPath, phasesPath string) string {
	return fmt.Sprintf(
		"Load skill %s/orchestrator/SKILL.md. Instructions: see %s. phases.json is at %s.",
		skillsDir, instructionsPath, phasesPath,
	)
}

func runDetect(repoDir string) error {
	cmd := exec.Command("konveyor-detect", repoDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func launchGoose(secretKey string) (*exec.Cmd, error) {
	cmd := exec.Command("goose", "serve", "--port", "4000")
	// The agent (goose) must never receive git push credentials directly,
	// even though it inherits most of the harness's environment for other
	// purposes (LLM provider credentials, PATH, HOME, etc.) — see the
	// design spec's credential handling section. internal/git owns the
	// names of the credential env vars, so it also owns how to filter
	// them out.
	cmd.Env = append(git.FilterCredentials(os.Environ()), "GOOSE_SERVER__SECRET_KEY="+secretKey)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start goose serve: %w", err)
	}
	return cmd, nil
}

// stopGoose sends an interrupt and waits up to 10s before force-killing.
// The container's lifetime is the harness's lifetime, so goose serve must
// be stopped explicitly rather than left to be killed by the container
// runtime tearing down the pod.
func stopGoose(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		if waitErr != nil {
			fmt.Fprintf(os.Stderr, "konveyor-harness: warning: goose serve exited with error during shutdown: %v\n", waitErr)
		}
		return nil
	case <-time.After(10 * time.Second):
		return cmd.Process.Kill()
	}
}
