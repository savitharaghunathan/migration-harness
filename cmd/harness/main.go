package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/config"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/phases"
	"github.com/konveyor/migration-harness/internal/session"
)

const (
	repoDir     = "/workspace/repo"
	askpassPath = "/usr/local/bin/git-askpass.sh"
	acpBaseURL  = "http://localhost:4000"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: konveyor-harness run")
		os.Exit(1)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-harness: "+err.Error())
		os.Exit(1)
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

	if err := git.Clone(params.SourceURL, repoDir, creds); err != nil {
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
	secretKey, err := config.GenerateSecretKey()
	if err != nil {
		return fmt.Errorf("generate secret key: %w", err)
	}

	if err := runDetect(repoDir); err != nil {
		return fmt.Errorf("detect: %w", err)
	}
	if err := git.Push(repoDir, []string{"detect.json", "graph.json"}, "konveyor: detect phase", creds, askpassPath); err != nil {
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

	client := acp.New(acpBaseURL)
	if err := client.WaitReady(30 * time.Second); err != nil {
		return fmt.Errorf("wait for acp: %w", err)
	}
	sessionID, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	events, err := client.Stream(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("open event stream: %w", err)
	}

	message := buildPromptMessage(params.SkillsDir, instructionsPath, phasesPath)
	if err := client.Prompt(sessionID, message); err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}

	var inputTokens, outputTokens int
	finalStatus := "failed"
	for e := range events {
		in, out := acp.UsageFromEvent(e)
		inputTokens += in
		outputTokens += out
		if e.Type == "complete" {
			finalStatus = "complete"
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
	if err := git.Push(repoDir, []string{".konveyor/session.json"}, "konveyor: session metadata", creds, askpassPath); err != nil {
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

	return nil
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
	cmd.Env = append(os.Environ(), "GOOSE_SERVER__SECRET_KEY="+secretKey)
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
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return cmd.Process.Kill()
	}
}
