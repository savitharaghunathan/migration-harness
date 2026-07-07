package git

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
)

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

// injectCredentials adds basic-auth credentials to an https:// URL. Other
// schemes (file://, git://, ssh://) pass through unchanged — this lets
// local/test clones work without credentials.
func injectCredentials(rawURL string, creds Credentials) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return rawURL, nil
	}
	u.User = url.UserPassword(creds.Username, creds.Token)
	return u.String(), nil
}

// Clone clones url into dest, then strips any injected credentials from
// the remote so the agent's own git operations can't push directly.
func Clone(rawURL, dest string, creds Credentials) error {
	authURL, err := injectCredentials(rawURL, creds)
	if err != nil {
		return err
	}

	cmd := exec.Command("git", "clone", authURL, dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	setURL := exec.Command("git", "-C", dest, "remote", "set-url", "origin", rawURL)
	setURL.Stdout = os.Stdout
	setURL.Stderr = os.Stderr
	if err := setURL.Run(); err != nil {
		return fmt.Errorf("strip credentials from remote: %w", err)
	}
	return nil
}

// CheckoutOrCreateBranch checks out branch if it exists on origin,
// otherwise creates it locally from the current HEAD.
func CheckoutOrCreateBranch(repoDir, branch string) error {
	check := exec.Command("git", "-C", repoDir, "ls-remote", "--exit-code", "--heads", "origin", branch)
	if err := check.Run(); err == nil {
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

	create := exec.Command("git", "-C", repoDir, "checkout", "-b", branch)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}
