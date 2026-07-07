package git

import (
	"os"
	"os/exec"
	"path/filepath"
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
