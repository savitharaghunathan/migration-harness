package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultAskpassPath is where the GIT_ASKPASS helper script is installed
// in the container image (see dockerfiles/agent-base.Dockerfile).
const DefaultAskpassPath = "/usr/local/bin/git-askpass.sh"

// Credentials holds git push/clone credentials read from the environment.
type Credentials struct {
	Username string
	Token    string
}

// CredentialsFromEnv reads KONVEYOR_GIT_USERNAME and KONVEYOR_GIT_TOKEN.
// These are injected by the controller via envFrom/secretRef (PR #295),
// not a mounted credential file.
func CredentialsFromEnv() (Credentials, error) {
	username := os.Getenv("KONVEYOR_GIT_USERNAME")
	token := os.Getenv("KONVEYOR_GIT_TOKEN")
	if username == "" || token == "" {
		return Credentials{}, fmt.Errorf("KONVEYOR_GIT_USERNAME and KONVEYOR_GIT_TOKEN must both be set")
	}
	return Credentials{Username: username, Token: token}, nil
}

// Clone clones rawURL into dest. Credentials are never embedded in the
// URL — when askpassPath is non-empty, git is configured to invoke it via
// GIT_ASKPASS whenever it needs a credential, so the token only ever
// travels through an env var to that helper script, never through argv or
// a URL (and therefore never through any stderr git might emit). Pass an
// empty askpassPath for URLs that need no auth (e.g. local file:// clones
// in tests) — GIT_ASKPASS is then left unset and simply never invoked.
func Clone(rawURL, dest string, creds Credentials, askpassPath string) error {
	cmd := exec.Command("git", "clone", rawURL, dest)
	if askpassPath != "" {
		cmd.Env = append(os.Environ(),
			"GIT_ASKPASS="+askpassPath,
			"KONVEYOR_GIT_USERNAME="+creds.Username,
			"KONVEYOR_GIT_TOKEN="+creds.Token,
		)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

// CheckoutOrCreateBranch checks out branch if it exists on origin,
// otherwise creates it locally from the current HEAD.
func CheckoutOrCreateBranch(repoDir, branch string) error {
	check := exec.Command("git", "-C", repoDir, "ls-remote", "--exit-code", "--heads", "origin", branch)
	var stderr bytes.Buffer
	check.Stderr = &stderr
	err := check.Run()
	if err == nil {
		fetch := exec.Command("git", "-C", repoDir, "fetch", "origin", branch)
		fetch.Stdout = os.Stdout
		fetch.Stderr = os.Stderr
		if err := fetch.Run(); err != nil {
			return fmt.Errorf("fetch existing branch: %w", err)
		}
		checkout := exec.Command("git", "-C", repoDir, "checkout", branch)
		checkout.Stdout = os.Stdout
		checkout.Stderr = os.Stderr
		if err := checkout.Run(); err != nil {
			return fmt.Errorf("checkout existing branch: %w", err)
		}
		return nil
	}

	// git ls-remote --exit-code returns exit code 2 specifically when no
	// matching refs are found. Any other non-zero exit code (network
	// failure, auth failure, bad remote, etc.) is a genuine failure that
	// must not be silently treated as "branch doesn't exist".
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		return fmt.Errorf("check remote branch existence: %w (stderr: %s)", err, stderr.String())
	}

	create := exec.Command("git", "-C", repoDir, "checkout", "-b", branch)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

// Push stages files (or all changes if files is empty), commits, and
// pushes to the current branch. If nothing is staged, it's a no-op —
// callers may call Push speculatively without checking for changes first.
func Push(repoDir string, files []string, message string, creds Credentials, askpassPath string) error {
	var addArgs []string
	if len(files) == 0 {
		addArgs = []string{"-C", repoDir, "add", "-A"}
	} else {
		addArgs = append([]string{"-C", repoDir, "add"}, files...)
	}
	add := exec.Command("git", addArgs...)
	add.Stdout = os.Stdout
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	diff := exec.Command("git", "-C", repoDir, "diff", "--cached", "--quiet")
	if err := diff.Run(); err == nil {
		return nil // nothing staged
	}

	commit := exec.Command("git", "-C", repoDir, "-c", "user.name=konveyor-harness", "-c", "user.email=konveyor-harness@noreply.local", "commit", "-m", message)
	commit.Stdout = os.Stdout
	commit.Stderr = os.Stderr
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	push := exec.Command("git", "-C", repoDir, "push", "origin", "HEAD")
	push.Env = append(os.Environ(),
		"GIT_ASKPASS="+askpassPath,
		"KONVEYOR_GIT_USERNAME="+creds.Username,
		"KONVEYOR_GIT_TOKEN="+creds.Token,
	)
	push.Stdout = os.Stdout
	push.Stderr = os.Stderr
	if err := push.Run(); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

// CommitCount returns the number of commits reachable from HEAD in repoDir.
func CommitCount(repoDir string) (int, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse commit count: %w", err)
	}
	return count, nil
}

// HeadSHA returns the current HEAD commit SHA in repoDir.
func HeadSHA(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
