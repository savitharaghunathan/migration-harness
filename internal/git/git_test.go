package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// unsetEnv removes an environment variable for the duration of the test,
// restoring its previous value (or absence) on cleanup. This differs from
// t.Setenv(key, ""), which sets the variable to an explicit empty string —
// git treats that as "identity is the empty string" rather than "no
// identity configured", so it does NOT fall back to `-c user.name=...`/
// config the way a genuinely unset variable does.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	if old, ok := os.LookupEnv(key); ok {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() { os.Setenv(key, old) })
	}
}

// setupSeededRemote creates a bare git repo with one commit on branch
// "main" and returns its filesystem path (usable as a file:// URL).
func setupSeededRemote(t *testing.T) string {
	t.Helper()
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v\n%s", err, out)
	}

	seed := t.TempDir()
	if out, err := exec.Command("git", "clone", remoteDir, seed).CombinedOutput(); err != nil {
		t.Fatalf("seed clone failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "-c", "user.email=test@test.com", "-c", "user.name=test", "commit", "-m", "seed")
	runGit(t, seed, "push", "origin", "HEAD:main")
	runGit(t, remoteDir, "symbolic-ref", "HEAD", "refs/heads/main")

	return remoteDir
}

func TestClone_ChecksOutFilesAndStripsCredentials(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")

	// file:// URLs bypass credential injection (only https:// gets creds),
	// so this also verifies non-https URLs pass through untouched.
	err := Clone("file://"+remoteDir, dest, Credentials{Username: "u", Token: "t"})
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Fatalf("expected README.md in clone, got: %v", err)
	}

	out, err := exec.Command("git", "-C", dest, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get-url failed: %v", err)
	}
	got := string(out)
	want := "file://" + remoteDir + "\n"
	if got != want {
		t.Fatalf("expected remote url %q, got %q", want, got)
	}
}

func TestClone_InjectsCredentialsForHTTPS(t *testing.T) {
	url, err := injectCredentials("https://example.com/org/repo.git", Credentials{Username: "myuser", Token: "mytoken"})
	if err != nil {
		t.Fatalf("injectCredentials failed: %v", err)
	}
	want := "https://myuser:mytoken@example.com/org/repo.git"
	if url != want {
		t.Fatalf("expected %q, got %q", want, url)
	}
}

func TestClone_LeavesNonHTTPSURLsUnchanged(t *testing.T) {
	url, err := injectCredentials("file:///tmp/repo.git", Credentials{Username: "u", Token: "t"})
	if err != nil {
		t.Fatalf("injectCredentials failed: %v", err)
	}
	if url != "file:///tmp/repo.git" {
		t.Fatalf("expected unchanged url, got %q", url)
	}
}

func TestCheckoutOrCreateBranch_CreatesNewBranch(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if err := CheckoutOrCreateBranch(dest, "konveyor/new-branch"); err != nil {
		t.Fatalf("CheckoutOrCreateBranch failed: %v", err)
	}

	out, err := exec.Command("git", "-C", dest, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse failed: %v", err)
	}
	got := string(out)
	if got != "konveyor/new-branch\n" {
		t.Fatalf("expected branch konveyor/new-branch, got %q", got)
	}
}

func TestCheckoutOrCreateBranch_ChecksOutExistingRemoteBranch(t *testing.T) {
	remoteDir := setupSeededRemote(t)

	// Create the branch on the remote first, from a second working copy.
	other := filepath.Join(t.TempDir(), "other")
	if err := Clone("file://"+remoteDir, other, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	runGit(t, other, "checkout", "-b", "konveyor/existing")
	runGit(t, other, "push", "origin", "konveyor/existing")

	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if err := CheckoutOrCreateBranch(dest, "konveyor/existing"); err != nil {
		t.Fatalf("CheckoutOrCreateBranch failed: %v", err)
	}

	out, err := exec.Command("git", "-C", dest, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse failed: %v", err)
	}
	if string(out) != "konveyor/existing\n" {
		t.Fatalf("expected branch konveyor/existing, got %q", out)
	}
}

func TestCredentialsFromEnv_HappyPath(t *testing.T) {
	t.Setenv("KONVEYOR_GIT_USERNAME", "testuser")
	t.Setenv("KONVEYOR_GIT_TOKEN", "testtoken")

	creds, err := CredentialsFromEnv()
	if err != nil {
		t.Fatalf("CredentialsFromEnv failed: %v", err)
	}

	if creds.Username != "testuser" {
		t.Fatalf("expected username testuser, got %q", creds.Username)
	}
	if creds.Token != "testtoken" {
		t.Fatalf("expected token testtoken, got %q", creds.Token)
	}
}

func TestCredentialsFromEnv_MissingUsername(t *testing.T) {
	t.Setenv("KONVEYOR_GIT_USERNAME", "")
	t.Setenv("KONVEYOR_GIT_TOKEN", "testtoken")

	_, err := CredentialsFromEnv()
	if err == nil {
		t.Fatalf("expected error when username is empty, got nil")
	}
}

func TestCredentialsFromEnv_MissingToken(t *testing.T) {
	t.Setenv("KONVEYOR_GIT_USERNAME", "testuser")
	t.Setenv("KONVEYOR_GIT_TOKEN", "")

	_, err := CredentialsFromEnv()
	if err == nil {
		t.Fatalf("expected error when token is empty, got nil")
	}
}

func writeAskpassScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "askpass.sh")
	script := "#!/bin/sh\necho \"$KONVEYOR_GIT_TOKEN\"\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPush_CommitsAndPushesChanges(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	if err := CheckoutOrCreateBranch(dest, "main"); err != nil {
		t.Fatalf("CheckoutOrCreateBranch failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dest, "NEW.md"), []byte("new file"), 0644); err != nil {
		t.Fatal(err)
	}

	// Strip any inherited git identity (env vars and global/user config) so
	// this test only passes because Push's own -c flags supply an identity —
	// not because the host machine happens to have a resolvable git user.
	unsetEnv(t, "GIT_AUTHOR_NAME")
	unsetEnv(t, "GIT_AUTHOR_EMAIL")
	unsetEnv(t, "GIT_COMMITTER_NAME")
	unsetEnv(t, "GIT_COMMITTER_EMAIL")
	t.Setenv("HOME", t.TempDir())

	askpass := writeAskpassScript(t)
	err := Push(dest, []string{"NEW.md"}, "add NEW.md", Credentials{Token: "unused-for-file-url"}, askpass)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Verify the remote actually received the commit by cloning fresh.
	verify := filepath.Join(t.TempDir(), "verify")
	if err := Clone("file://"+remoteDir, verify, Credentials{}); err != nil {
		t.Fatalf("verify clone failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(verify, "NEW.md")); err != nil {
		t.Fatalf("expected NEW.md to be pushed, got: %v", err)
	}
}

func TestCommitCount_ReturnsOneForFreshlySeededRepo(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	count, err := CommitCount(dest)
	if err != nil {
		t.Fatalf("CommitCount failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected commit count 1, got %d", count)
	}
}

func TestCommitCount_ReturnsTwoAfterAdditionalCommit(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dest, "SECOND.md"), []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dest, "add", "SECOND.md")
	runGit(t, dest, "-c", "user.email=test@test.com", "-c", "user.name=test", "commit", "-m", "second commit")

	count, err := CommitCount(dest)
	if err != nil {
		t.Fatalf("CommitCount failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected commit count 2, got %d", count)
	}
}

func TestHeadSHA_ReturnsCurrentCommitSHA(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	sha, err := HeadSHA(dest)
	if err != nil {
		t.Fatalf("HeadSHA failed: %v", err)
	}
	if len(sha) != 40 {
		t.Fatalf("expected 40-character SHA, got %q (len %d)", sha, len(sha))
	}
	for _, c := range sha {
		if !strings.Contains("0123456789abcdef", string(c)) {
			t.Fatalf("expected hex SHA, got %q", sha)
		}
	}

	want, err := exec.Command("git", "-C", dest, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse failed: %v", err)
	}
	if sha != strings.TrimSpace(string(want)) {
		t.Fatalf("expected sha %q, got %q", strings.TrimSpace(string(want)), sha)
	}
}

func TestPush_NoOpWhenNothingStaged(t *testing.T) {
	remoteDir := setupSeededRemote(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone("file://"+remoteDir, dest, Credentials{}); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	askpass := writeAskpassScript(t)
	// No file changes were made — Push must not error even though there's
	// nothing to commit.
	if err := Push(dest, nil, "no-op", Credentials{}, askpass); err != nil {
		t.Fatalf("expected no-op Push to succeed, got: %v", err)
	}
}
