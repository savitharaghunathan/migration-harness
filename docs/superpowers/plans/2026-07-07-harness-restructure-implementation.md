# Migration Harness Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bash CLI (`bin/migration-harness`, `lib/step-*.sh`) with Go binaries and SKILL.md files that run the same 5-step migration pipeline (detect → plan → execute → verify-fix) on the Konveyor Agentic Platform, per `docs/superpowers/specs/2026-07-02-harness-restructure-design.md`.

**Architecture:** A single Go module builds six binaries (`konveyor-harness` entrypoint, plus `konveyor-clone`/`konveyor-push`/`konveyor-configure`/`konveyor-detect`/`konveyor-results` utilities) sharing internal packages for git operations, config, phase tracking, session metadata, and an ACP client. The harness runs `konveyor-detect` directly (no LLM), then launches `goose serve`, drives it over ACP (event-stream-based, not polling), and the goose session runs pipeline skills (`plan`, `execute`, `verify` — verify includes the fix loop) ported from the existing `skill-bundle/`/`recipes/` content.

**Tech Stack:** Go (stdlib only — `os/exec`, `net/http`, `encoding/json`, `bufio` — no external dependencies for the POC), `git` CLI, `goose` CLI, SKILL.md (markdown with YAML frontmatter).

## Global Constraints

- Go module path: `github.com/konveyor/migration-harness`. Go version: 1.22+.
- No external Go dependencies for the POC — stdlib only (spec: "shell scripts wrapping git commands are sufficient" reasoning extends to "stdlib is sufficient" for Go).
- Git credentials arrive as `KONVEYOR_GIT_USERNAME` and `KONVEYOR_GIT_TOKEN` env vars (via `envFrom`/`secretRef` per PR #295), NOT a mounted credential file.
- Push-time re-authentication uses a `GIT_ASKPASS` helper script, since `konveyor-clone` strips credentials from the remote URL after cloning.
- Env var names: `KONVEYOR_PARAM_SOURCE_URL`, `KONVEYOR_PARAM_TARGET_BRANCH`, `KONVEYOR_INSTRUCTIONS` (deliberately NOT `KONVEYOR_PARAM_INSTRUCTIONS` — see spec), `KONVEYOR_SKILLS_DIR` (default `/opt/skills`).
- `instructions.md` is written to `/workspace/instructions.md` — outside `/workspace/repo/` — so it is never git-tracked or accidentally pushed.
- Pipeline phase names: `plan`, `execute`, `verify-fix` (NOT separate `verify` and `fix` phases — they were merged into one skill with an internal iteration loop, max 3 iterations).
- `goose serve` requires `GOOSE_SERVER__SECRET_KEY` (confirmed against goose's docs — `--dangerously-unauthenticated` is dev-only, not used here). Transport is Streamable HTTP and/or WebSocket — goose does NOT support SSE.
- The exact ACP wire protocol (method names, event shapes, terminal-event semantics) is UNVERIFIED per the spec's "Known Unknowns" section. This plan builds `internal/acp` against the best-documented assumption (newline-delimited JSON events over a streamed HTTP response) with an isolated, swappable interface, and ends with a manual verification task against a real `goose serve` instance — do not treat `internal/acp`'s current behavior as final until that task passes.
- Session handoff: `handoff.md` is written and pushed by the orchestration skill (inside the goose session), never by the harness. `session.json` is written and pushed by the harness, never by the skill.
- Existing bash implementation moves to `_legacy/` and is not deleted until the new code is proven — do not reference `_legacy/` from any new Go code or skill.

---

## File Structure

```
migration-harness/
  go.mod
  cmd/
    harness/main.go       # konveyor-harness entrypoint
    clone/main.go         # konveyor-clone
    push/main.go          # konveyor-push
    configure/main.go     # konveyor-configure
    detect/main.go        # konveyor-detect
    results/main.go       # konveyor-results
  internal/
    git/
      git.go              # Clone, CheckoutOrCreateBranch, Push, CredentialsFromEnv
      git_test.go
    config/
      config.go           # LoadParamsFromEnv, GenerateSecretKey, WriteGooseConfig
      config_test.go
    detect/
      detect.go           # Summarize (graph.json -> detect.json), DetectManifests
      detect_test.go
    phases/
      phases.go           # Phase, DefaultPipeline, WriteJSON, CheckCompletion
      phases_test.go
    session/
      session.go          # Session, Results structs + WriteTo
      session_test.go
    acp/
      client.go           # Client: WaitReady, NewSession, Prompt, Stream, UsageFromEvent
      client_test.go
  scripts/
    git-askpass.sh
  skills/
    orchestrator/SKILL.md
    plan/SKILL.md
    execute/SKILL.md
    verify/SKILL.md
  dockerfiles/
    agent-base.Dockerfile
    agent-base-goose.Dockerfile
  _legacy/
    (moved: bin/, lib/, recipes/, skill-bundle/, Dockerfile, docker-compose.yml, install.sh)
```

Each `internal/` package has one clear responsibility and no dependency on `cmd/`. `cmd/*/main.go` files are thin — they parse args/env and call `internal/` functions. This keeps the risky, hard-to-test part (`internal/acp`) isolated from the well-tested mechanical parts (`internal/git`, `internal/phases`, `internal/session`).

---

### Task 1: Move existing bash implementation to `_legacy/`

The current bash CLI and its skill/recipe content stay in the repo as reference but move out of the way so the new Go code and skills are unambiguous. This is a pure file move — no code changes yet.

**Files:**
- Move: `bin/` → `_legacy/bin/`
- Move: `lib/` → `_legacy/lib/`
- Move: `recipes/` → `_legacy/recipes/`
- Move: `skill-bundle/` → `_legacy/skill-bundle/`
- Move: `Dockerfile` → `_legacy/Dockerfile`
- Move: `docker-compose.yml` → `_legacy/docker-compose.yml`
- Move: `install.sh` → `_legacy/install.sh`

**Interfaces:** None — this task has no code interfaces, it only relocates files that later tasks must NOT reference.

- [ ] **Step 1: Create `_legacy/` and move the files with `git mv` (preserves history)**

```bash
mkdir -p _legacy
git mv bin _legacy/bin
git mv lib _legacy/lib
git mv recipes _legacy/recipes
git mv skill-bundle _legacy/skill-bundle
git mv Dockerfile _legacy/Dockerfile
git mv docker-compose.yml _legacy/docker-compose.yml
git mv install.sh _legacy/install.sh
```

- [ ] **Step 2: Verify the move — nothing should remain at the old paths**

Run: `ls bin lib recipes skill-bundle Dockerfile docker-compose.yml install.sh 2>&1`
Expected: `No such file or directory` for every one of them (all now live under `_legacy/`).

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore: move existing bash CLI implementation to _legacy/"
```

---

### Task 2: Go module scaffolding

Set up the Go module so subsequent tasks can `go build`/`go test` immediately.

**Files:**
- Create: `go.mod`

**Interfaces:**
- Produces: module path `github.com/konveyor/migration-harness`, used as the import prefix by every later task's Go files.

- [ ] **Step 1: Initialize the module**

```bash
go mod init github.com/konveyor/migration-harness
```

- [ ] **Step 2: Verify the go.mod contents**

Run: `cat go.mod`
Expected output (Go version may differ slightly based on installed toolchain, that's fine):

```
module github.com/konveyor/migration-harness

go 1.22
```

- [ ] **Step 3: Commit**

```bash
git add go.mod
git commit -m "chore: initialize Go module"
```

---

### Task 3: `internal/git` — Clone and branch checkout

Implements the clone half of `konveyor-clone`: clone with injected HTTPS credentials, then strip credentials from the remote and checkout/create the target branch. Tested against a local bare repo — no network or real GitHub access needed.

**Files:**
- Create: `internal/git/git.go`
- Test: `internal/git/git_test.go`

**Interfaces:**
- Produces:
  - `type Credentials struct { Username, Token string }`
  - `func CredentialsFromEnv() (Credentials, error)` — reads `KONVEYOR_GIT_USERNAME`/`KONVEYOR_GIT_TOKEN`
  - `func Clone(url, dest string, creds Credentials) error`
  - `func CheckoutOrCreateBranch(repoDir, branch string) error`
- Consumes: nothing (first package in the dependency graph).

- [ ] **Step 1: Write the failing tests**

Create `internal/git/git_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/git/... -v`
Expected: FAIL — build error, `package git` doesn't exist yet (no `git.go` file).

- [ ] **Step 3: Write the implementation**

Create `internal/git/git.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/git/... -v`
Expected: PASS for all 5 tests (`TestClone_ChecksOutFilesAndStripsCredentials`, `TestClone_InjectsCredentialsForHTTPS`, `TestClone_LeavesNonHTTPSURLsUnchanged`, `TestCheckoutOrCreateBranch_CreatesNewBranch`, `TestCheckoutOrCreateBranch_ChecksOutExistingRemoteBranch`).

- [ ] **Step 5: Commit**

```bash
git add internal/git/git.go internal/git/git_test.go
git commit -m "feat: add git clone and branch checkout with credential stripping"
```

---

### Task 4: `internal/git` — Push with GIT_ASKPASS re-authentication

Adds `Push`, which stages, commits, and pushes using a `GIT_ASKPASS` helper for re-authentication (since `Clone` already stripped credentials from the remote).

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

**Interfaces:**
- Consumes: `Credentials`, `setupSeededRemote`/`runGit` test helpers from Task 3.
- Produces: `func Push(repoDir string, files []string, message string, creds Credentials, askpassPath string) error`

- [ ] **Step 1: Write the failing test**

Append to `internal/git/git_test.go`:

```go
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

	runGit(t, dest, "-c", "user.email=test@test.com", "-c", "user.name=test", "config", "user.email", "test@test.com")
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/git/... -run TestPush -v`
Expected: FAIL — `undefined: Push`.

- [ ] **Step 3: Write the implementation**

Add to `internal/git/git.go`:

```go
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

	commit := exec.Command("git", "-C", repoDir, "commit", "-m", message)
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/git/... -v`
Expected: PASS for all tests including `TestPush_CommitsAndPushesChanges` and `TestPush_NoOpWhenNothingStaged`.

- [ ] **Step 5: Commit**

```bash
git add internal/git/git.go internal/git/git_test.go
git commit -m "feat: add git push with GIT_ASKPASS re-authentication"
```

---

### Task 5: GIT_ASKPASS helper script

The actual script `Push` points `GIT_ASKPASS` at. Git invokes this whenever it needs a username or password; it echoes the token for either prompt (works for token-based auth where any non-empty username is accepted).

**Files:**
- Create: `scripts/git-askpass.sh`

**Interfaces:** None — this is a standalone shell script invoked by `git`, not Go code.

- [ ] **Step 1: Create the script**

```bash
mkdir -p scripts
cat > scripts/git-askpass.sh <<'EOF'
#!/usr/bin/env sh
# GIT_ASKPASS helper for konveyor-push. Git calls this for both username
# and password prompts; returning KONVEYOR_GIT_TOKEN for either works for
# token-based auth (GitHub/GitLab personal access tokens accept the token
# as the password with any non-empty username).
echo "$KONVEYOR_GIT_TOKEN"
EOF
chmod +x scripts/git-askpass.sh
```

- [ ] **Step 2: Verify it's executable and echoes the token**

Run: `KONVEYOR_GIT_TOKEN=test-token ./scripts/git-askpass.sh`
Expected output: `test-token`

- [ ] **Step 3: Commit**

```bash
git add scripts/git-askpass.sh
git commit -m "feat: add GIT_ASKPASS helper script for push re-authentication"
```

---

### Task 6: `cmd/clone` — konveyor-clone binary

Thin CLI wrapper: parse args, load credentials, call `internal/git.Clone` + `CheckoutOrCreateBranch`.

**Files:**
- Create: `cmd/clone/main.go`

**Interfaces:**
- Consumes: `git.CredentialsFromEnv`, `git.Clone`, `git.CheckoutOrCreateBranch` from Task 3/4.
- Produces: the `konveyor-clone` binary, invoked as `konveyor-clone <url> <dest>`.

- [ ] **Step 1: Write the implementation**

There's no separate unit test for `main.go` itself — its logic is a thin pass-through already covered by `internal/git`'s tests. Verification is a manual build-and-run check in Step 2.

Create `cmd/clone/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/git"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: konveyor-clone <url> <dest>")
		os.Exit(1)
	}
	url, dest := os.Args[1], os.Args[2]

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-clone: "+err.Error())
		os.Exit(1)
	}
	if err := git.Clone(url, dest, creds); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-clone: "+err.Error())
		os.Exit(1)
	}

	if branch := os.Getenv("KONVEYOR_PARAM_TARGET_BRANCH"); branch != "" {
		if err := git.CheckoutOrCreateBranch(dest, branch); err != nil {
			fmt.Fprintln(os.Stderr, "konveyor-clone: "+err.Error())
			os.Exit(1)
		}
	}
}
```

- [ ] **Step 2: Build and manually verify against a local bare repo**

```bash
go build -o /tmp/konveyor-clone ./cmd/clone

# Set up a throwaway local bare repo to clone from.
rm -rf /tmp/testremote.git /tmp/testclone
git init --bare /tmp/testremote.git
git clone /tmp/testremote.git /tmp/seed
cd /tmp/seed && echo hi > f.txt && git add f.txt && git -c user.email=t@t.com -c user.name=t commit -m seed && git push origin HEAD:main
cd -

KONVEYOR_GIT_USERNAME=u KONVEYOR_GIT_TOKEN=t KONVEYOR_PARAM_TARGET_BRANCH=konveyor/test \
  /tmp/konveyor-clone file:///tmp/testremote.git /tmp/testclone
```

Expected: no errors, and `git -C /tmp/testclone rev-parse --abbrev-ref HEAD` prints `konveyor/test`.

- [ ] **Step 3: Commit**

```bash
git add cmd/clone/main.go
git commit -m "feat: add konveyor-clone binary"
```

---

### Task 7: `cmd/push` — konveyor-push binary

Thin CLI wrapper: parse `--message` and file args, call `internal/git.Push`.

**Files:**
- Create: `cmd/push/main.go`

**Interfaces:**
- Consumes: `git.CredentialsFromEnv`, `git.Push` from Task 3/4.
- Produces: the `konveyor-push` binary, invoked as `konveyor-push [--message <msg>] [files...]`.

- [ ] **Step 1: Write the implementation**

Create `cmd/push/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/git"
)

const askpassPath = "/usr/local/bin/git-askpass.sh"

func main() {
	message, files := parseArgs(os.Args[1:])

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
	repoDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
	if err := git.Push(repoDir, files, message, creds, askpassPath); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
}

func parseArgs(args []string) (message string, files []string) {
	message = "konveyor: update"
	for i := 0; i < len(args); i++ {
		if args[i] == "--message" && i+1 < len(args) {
			message = args[i+1]
			i++
			continue
		}
		files = append(files, args[i])
	}
	return message, files
}
```

- [ ] **Step 2: Write a unit test for the pure argument-parsing logic**

Create `cmd/push/main_test.go`:

```go
package main

import (
	"reflect"
	"testing"
)

func TestParseArgs_MessageAndFiles(t *testing.T) {
	message, files := parseArgs([]string{"--message", "hello world", "a.txt", "b.txt"})
	if message != "hello world" {
		t.Errorf("expected message %q, got %q", "hello world", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt", "b.txt"}) {
		t.Errorf("expected files [a.txt b.txt], got %v", files)
	}
}

func TestParseArgs_DefaultMessageWhenOmitted(t *testing.T) {
	message, files := parseArgs([]string{"a.txt"})
	if message != "konveyor: update" {
		t.Errorf("expected default message, got %q", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt"}) {
		t.Errorf("expected files [a.txt], got %v", files)
	}
}

func TestParseArgs_NoFilesMeansStageAll(t *testing.T) {
	message, files := parseArgs([]string{"--message", "commit everything"})
	if message != "commit everything" {
		t.Errorf("expected message %q, got %q", "commit everything", message)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}
```

- [ ] **Step 3: Run the test to verify it passes**

Run: `go test ./cmd/push/... -v`
Expected: PASS for all 3 tests.

- [ ] **Step 4: Commit**

```bash
git add cmd/push/main.go cmd/push/main_test.go
git commit -m "feat: add konveyor-push binary"
```

---

### Task 8: `internal/config` — params, secret key, goose config

Reads `KONVEYOR_PARAM_*`/`KONVEYOR_INSTRUCTIONS`/`KONVEYOR_SKILLS_DIR`, generates the per-run `GOOSE_SERVER__SECRET_KEY`, and writes goose's config file.

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Params struct { SourceURL, TargetBranch, Instructions, SkillsDir string }`
  - `func LoadParamsFromEnv() (Params, error)`
  - `func GenerateSecretKey() (string, error)`
  - `func WriteGooseConfig(configDir string) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParamsFromEnv_ReadsAllFields(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")
	t.Setenv("KONVEYOR_INSTRUCTIONS", "Migrate to Quarkus")
	t.Setenv("KONVEYOR_SKILLS_DIR", "/custom/skills")

	p, err := LoadParamsFromEnv()
	if err != nil {
		t.Fatalf("LoadParamsFromEnv failed: %v", err)
	}
	if p.SourceURL != "https://example.com/repo.git" {
		t.Errorf("unexpected SourceURL: %q", p.SourceURL)
	}
	if p.TargetBranch != "konveyor/migrate-1" {
		t.Errorf("unexpected TargetBranch: %q", p.TargetBranch)
	}
	if p.Instructions != "Migrate to Quarkus" {
		t.Errorf("unexpected Instructions: %q", p.Instructions)
	}
	if p.SkillsDir != "/custom/skills" {
		t.Errorf("unexpected SkillsDir: %q", p.SkillsDir)
	}
}

func TestLoadParamsFromEnv_DefaultsSkillsDir(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")
	t.Setenv("KONVEYOR_SKILLS_DIR", "")

	p, err := LoadParamsFromEnv()
	if err != nil {
		t.Fatalf("LoadParamsFromEnv failed: %v", err)
	}
	if p.SkillsDir != "/opt/skills" {
		t.Errorf("expected default /opt/skills, got %q", p.SkillsDir)
	}
}

func TestLoadParamsFromEnv_ErrorsWhenSourceURLMissing(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")

	_, err := LoadParamsFromEnv()
	if err == nil {
		t.Fatal("expected an error when KONVEYOR_PARAM_SOURCE_URL is missing")
	}
}

func TestLoadParamsFromEnv_ErrorsWhenTargetBranchMissing(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "")

	_, err := LoadParamsFromEnv()
	if err == nil {
		t.Fatal("expected an error when KONVEYOR_PARAM_TARGET_BRANCH is missing")
	}
}

func TestGenerateSecretKey_ProducesNonEmptyHexString(t *testing.T) {
	key, err := GenerateSecretKey()
	if err != nil {
		t.Fatalf("GenerateSecretKey failed: %v", err)
	}
	if len(key) != 64 { // 32 bytes hex-encoded = 64 chars
		t.Errorf("expected 64-char hex string, got length %d: %q", len(key), key)
	}
}

func TestGenerateSecretKey_ProducesDifferentKeysEachCall(t *testing.T) {
	key1, _ := GenerateSecretKey()
	key2, _ := GenerateSecretKey()
	if key1 == key2 {
		t.Error("expected two calls to produce different keys")
	}
}

func TestWriteGooseConfig_WritesProviderAndModel(t *testing.T) {
	t.Setenv("GOOSE_PROVIDER", "anthropic")
	t.Setenv("GOOSE_MODEL", "claude-sonnet-4-20250514")

	dir := t.TempDir()
	configDir := filepath.Join(dir, ".config", "goose")
	if err := WriteGooseConfig(configDir); err != nil {
		t.Fatalf("WriteGooseConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("expected config.yaml to exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "GOOSE_PROVIDER: anthropic") {
		t.Errorf("expected provider in config, got: %s", content)
	}
	if !strings.Contains(content, "GOOSE_MODEL: claude-sonnet-4-20250514") {
		t.Errorf("expected model in config, got: %s", content)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/... -v`
Expected: FAIL — `package config` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/config/config.go`:

```go
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Params holds the KONVEYOR_PARAM_* and related env vars the harness needs.
type Params struct {
	SourceURL    string
	TargetBranch string
	Instructions string
	SkillsDir    string
}

// LoadParamsFromEnv reads the harness's required configuration from the
// environment. Instructions comes from KONVEYOR_INSTRUCTIONS (not
// KONVEYOR_PARAM_INSTRUCTIONS) because PR #295 treats AgentRun's
// `instructions` field as distinct from declared `params`.
func LoadParamsFromEnv() (Params, error) {
	p := Params{
		SourceURL:    os.Getenv("KONVEYOR_PARAM_SOURCE_URL"),
		TargetBranch: os.Getenv("KONVEYOR_PARAM_TARGET_BRANCH"),
		Instructions: os.Getenv("KONVEYOR_INSTRUCTIONS"),
		SkillsDir:    os.Getenv("KONVEYOR_SKILLS_DIR"),
	}
	if p.SkillsDir == "" {
		p.SkillsDir = "/opt/skills"
	}
	if p.SourceURL == "" {
		return Params{}, fmt.Errorf("KONVEYOR_PARAM_SOURCE_URL is required")
	}
	if p.TargetBranch == "" {
		return Params{}, fmt.Errorf("KONVEYOR_PARAM_TARGET_BRANCH is required")
	}
	return p, nil
}

// GenerateSecretKey produces a random hex string for GOOSE_SERVER__SECRET_KEY.
// Generated fresh per run — local to this pod/process, never shared with
// the controller or UI (see spec's "Controller/UI auth to /acp" open item).
func GenerateSecretKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteGooseConfig writes goose's config.yaml from GOOSE_PROVIDER/GOOSE_MODEL
// env vars (passed through via envFrom from the LLMProvider Secret).
func WriteGooseConfig(configDir string) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	contents := fmt.Sprintf("GOOSE_PROVIDER: %s\nGOOSE_MODEL: %s\n",
		os.Getenv("GOOSE_PROVIDER"), os.Getenv("GOOSE_MODEL"))
	return os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(contents), 0644)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: PASS for all 7 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add config package for params, secret key, and goose config"
```

---

### Task 9: `cmd/configure` — konveyor-configure binary

**Files:**
- Create: `cmd/configure/main.go`

**Interfaces:**
- Consumes: `config.WriteGooseConfig` from Task 8.
- Produces: the `konveyor-configure` binary, invoked as `konveyor-configure` with no args.

- [ ] **Step 1: Write the implementation**

Create `cmd/configure/main.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/config"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-configure: "+err.Error())
		os.Exit(1)
	}
	if err := config.WriteGooseConfig(filepath.Join(homeDir, ".config", "goose")); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-configure: "+err.Error())
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Build and manually verify**

```bash
go build -o /tmp/konveyor-configure ./cmd/configure
GOOSE_PROVIDER=anthropic GOOSE_MODEL=claude-sonnet-4-20250514 HOME=/tmp/testhome /tmp/konveyor-configure
cat /tmp/testhome/.config/goose/config.yaml
```

Expected output:
```
GOOSE_PROVIDER: anthropic
GOOSE_MODEL: claude-sonnet-4-20250514
```

- [ ] **Step 3: Commit**

```bash
git add cmd/configure/main.go
git commit -m "feat: add konveyor-configure binary"
```

---

### Task 10: `internal/detect` — graph summarization

Pure logic that turns graphify's `graph.json` output into the `detect.json` summary (manifest flags, per-language file counts, graph stats). Testable with a fixture `graph.json` — no real `graphify` binary needed for this task.

**Files:**
- Create: `internal/detect/detect.go`
- Test: `internal/detect/detect_test.go`

**Interfaces:**
- Produces:
  - `type Summary struct { Repo string; Manifests Manifests; Files FileCounts; Graph GraphStats; GraphFile string }` (all fields JSON-tagged to match the spec's `detect.json` shape)
  - `func DetectManifests(repoDir string) Manifests`
  - `func Summarize(repoDir string, graphJSON []byte) (Summary, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/detect/detect_test.go`:

```go
package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectManifests_FindsPomXML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0644); err != nil {
		t.Fatal(err)
	}

	m := DetectManifests(dir)
	if !m.PomXML {
		t.Error("expected PomXML to be true")
	}
	if m.PackageJSON {
		t.Error("expected PackageJSON to be false")
	}
}

func TestSummarize_CountsFilesAndGraphStats(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0644); err != nil {
		t.Fatal(err)
	}

	graphJSON := []byte(`{
		"nodes": [
			{"source_file": "src/Foo.java", "degree": 5},
			{"source_file": "src/Bar.java", "degree": 25},
			{"source_file": "src/main.py", "degree": 1}
		],
		"links": [{}, {}],
		"communities": [{"id": 0}, {"id": 0}, {"id": 1}]
	}`)

	summary, err := Summarize(dir, graphJSON)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if !summary.Manifests.PomXML {
		t.Error("expected PomXML to be true")
	}
	if summary.Files.Java != 2 {
		t.Errorf("expected 2 java files, got %d", summary.Files.Java)
	}
	if summary.Files.Python != 1 {
		t.Errorf("expected 1 python file, got %d", summary.Files.Python)
	}
	if summary.Graph.Nodes != 3 {
		t.Errorf("expected 3 nodes, got %d", summary.Graph.Nodes)
	}
	if summary.Graph.Edges != 2 {
		t.Errorf("expected 2 edges, got %d", summary.Graph.Edges)
	}
	if summary.Graph.Communities != 2 {
		t.Errorf("expected 2 communities, got %d", summary.Graph.Communities)
	}
	if summary.Graph.GodNodes != 1 {
		t.Errorf("expected 1 god node (degree>20), got %d", summary.Graph.GodNodes)
	}
	if summary.GraphFile != "graph.json" {
		t.Errorf("expected GraphFile to be graph.json, got %q", summary.GraphFile)
	}
}

func TestSummarize_ErrorsOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	_, err := Summarize(dir, []byte("not json"))
	if err == nil {
		t.Fatal("expected an error for invalid graph.json")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/detect/... -v`
Expected: FAIL — `package detect` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/detect/detect.go`:

```go
package detect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manifests struct {
	PomXML          bool `json:"pom_xml"`
	PackageJSON     bool `json:"package_json"`
	PyprojectTOML   bool `json:"pyproject_toml"`
	RequirementsTXT bool `json:"requirements_txt"`
	SetupPY         bool `json:"setup_py"`
	GoMod           bool `json:"go_mod"`
	CargoTOML       bool `json:"cargo_toml"`
	Gemfile         bool `json:"gemfile"`
}

type FileCounts struct {
	Java       int `json:"java"`
	Python     int `json:"python"`
	JavaScript int `json:"javascript"`
	TypeScript int `json:"typescript"`
	Go         int `json:"go"`
	Rust       int `json:"rust"`
	CSharp     int `json:"csharp"`
	Ruby       int `json:"ruby"`
}

type GraphStats struct {
	Nodes       int `json:"nodes"`
	Edges       int `json:"edges"`
	Communities int `json:"communities"`
	GodNodes    int `json:"god_nodes"`
}

type Summary struct {
	Repo      string     `json:"repo"`
	Manifests Manifests  `json:"manifests"`
	Files     FileCounts `json:"files"`
	Graph     GraphStats `json:"graph"`
	GraphFile string     `json:"graph_file"`
}

// DetectManifests checks which build manifest files exist at repoDir's root.
func DetectManifests(repoDir string) Manifests {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(repoDir, name))
		return err == nil
	}
	return Manifests{
		PomXML:          exists("pom.xml"),
		PackageJSON:     exists("package.json"),
		PyprojectTOML:   exists("pyproject.toml"),
		RequirementsTXT: exists("requirements.txt"),
		SetupPY:         exists("setup.py"),
		GoMod:           exists("go.mod"),
		CargoTOML:       exists("Cargo.toml"),
		Gemfile:         exists("Gemfile"),
	}
}

type graphNode struct {
	SourceFile string `json:"source_file"`
	Degree     int    `json:"degree"`
}

type graphCommunity struct {
	ID int `json:"id"`
}

type graphDoc struct {
	Nodes       []graphNode       `json:"nodes"`
	Links       []json.RawMessage `json:"links"`
	Communities []graphCommunity  `json:"communities"`
}

// Summarize parses graphify's graph.json and produces the detect.json
// summary: manifest flags, per-language file counts, and graph stats.
// God nodes are nodes with degree > 20 (matches the existing bash heuristic).
func Summarize(repoDir string, graphJSON []byte) (Summary, error) {
	var g graphDoc
	if err := json.Unmarshal(graphJSON, &g); err != nil {
		return Summary{}, fmt.Errorf("parse graph.json: %w", err)
	}

	communitySet := map[int]bool{}
	for _, c := range g.Communities {
		communitySet[c.ID] = true
	}

	var files FileCounts
	var godNodes int
	for _, n := range g.Nodes {
		switch {
		case strings.HasSuffix(n.SourceFile, ".java"):
			files.Java++
		case strings.HasSuffix(n.SourceFile, ".py"):
			files.Python++
		case strings.HasSuffix(n.SourceFile, ".js"), strings.HasSuffix(n.SourceFile, ".jsx"):
			files.JavaScript++
		case strings.HasSuffix(n.SourceFile, ".ts"), strings.HasSuffix(n.SourceFile, ".tsx"):
			files.TypeScript++
		case strings.HasSuffix(n.SourceFile, ".go"):
			files.Go++
		case strings.HasSuffix(n.SourceFile, ".rs"):
			files.Rust++
		case strings.HasSuffix(n.SourceFile, ".cs"):
			files.CSharp++
		case strings.HasSuffix(n.SourceFile, ".rb"):
			files.Ruby++
		}
		if n.Degree > 20 {
			godNodes++
		}
	}

	return Summary{
		Repo:      repoDir,
		Manifests: DetectManifests(repoDir),
		Files:     files,
		Graph: GraphStats{
			Nodes:       len(g.Nodes),
			Edges:       len(g.Links),
			Communities: len(communitySet),
			GodNodes:    godNodes,
		},
		GraphFile: "graph.json",
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/detect/... -v`
Expected: PASS for all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/detect/detect.go internal/detect/detect_test.go
git commit -m "feat: add detect package for graph.json summarization"
```

---

### Task 11: `cmd/detect` — konveyor-detect binary

Runs `graphify` as a subprocess, then uses `internal/detect.Summarize` to write `detect.json` (and copies `graph.json` to the repo root).

**Files:**
- Create: `cmd/detect/main.go`

**Interfaces:**
- Consumes: `detect.Summarize` from Task 10.
- Produces: the `konveyor-detect` binary, invoked as `konveyor-detect <repo-path>`.

**Note:** this task's `main.go` shells out to the real `graphify` binary, which isn't available in a unit-test environment — its logic is a thin wrapper already covered by `internal/detect`'s tests. Verification is manual (Step 2), and requires `graphify` to be installed locally (`pip install graphifyy`) or can be skipped if unavailable — the important logic is already tested in Task 10.

- [ ] **Step 1: Write the implementation**

Create `cmd/detect/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/detect"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: konveyor-detect <repo-path>")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-detect: "+err.Error())
		os.Exit(1)
	}
}

func run(repoDir string) error {
	outDir := filepath.Join(repoDir, "graphify-out")
	cmd := exec.Command("graphify", repoDir, "--output", outDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run graphify: %w", err)
	}

	graphJSON, err := os.ReadFile(filepath.Join(outDir, "graph.json"))
	if err != nil {
		return fmt.Errorf("read graph.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "graph.json"), graphJSON, 0644); err != nil {
		return fmt.Errorf("copy graph.json to repo root: %w", err)
	}

	summary, err := detect.Summarize(repoDir, graphJSON)
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal detect.json: %w", err)
	}
	return os.WriteFile(filepath.Join(repoDir, "detect.json"), data, 0644)
}
```

- [ ] **Step 2: Build to confirm it compiles**

```bash
go build -o /tmp/konveyor-detect ./cmd/detect
```

Expected: builds with no errors. (Full manual run against a real repo requires `graphify` installed — skip if unavailable; the tested logic already lives in `internal/detect`.)

- [ ] **Step 3: Commit**

```bash
git add cmd/detect/main.go
git commit -m "feat: add konveyor-detect binary"
```

---

### Task 12: `internal/phases` — pipeline definition and completion tracking

Defines the fixed 3-phase pipeline (`plan`, `execute`, `verify-fix`), writes `phases.json`, and checks which phases actually completed by looking for their `expected_outputs` on disk.

**Files:**
- Create: `internal/phases/phases.go`
- Test: `internal/phases/phases_test.go`

**Interfaces:**
- Produces:
  - `type Phase struct { Name, Skill string; ExpectedOutputs []string; Description string }`
  - `func DefaultPipeline() []Phase`
  - `func WriteJSON(path string, phases []Phase) error`
  - `func CheckCompletion(repoDir string, phases []Phase) (completed, incomplete []string)`

- [ ] **Step 1: Write the failing tests**

Create `internal/phases/phases_test.go`:

```go
package phases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultPipeline_HasThreePhasesInOrder(t *testing.T) {
	pipeline := DefaultPipeline()
	if len(pipeline) != 3 {
		t.Fatalf("expected 3 phases, got %d", len(pipeline))
	}
	wantNames := []string{"plan", "execute", "verify-fix"}
	for i, want := range wantNames {
		if pipeline[i].Name != want {
			t.Errorf("phase %d: expected name %q, got %q", i, want, pipeline[i].Name)
		}
	}
}

func TestWriteJSON_ProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "phases.json")
	pipeline := DefaultPipeline()

	if err := WriteJSON(path, pipeline); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected phases.json to exist: %v", err)
	}

	var roundTripped []Phase
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("phases.json is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, pipeline) {
		t.Errorf("round-tripped phases don't match: got %+v, want %+v", roundTripped, pipeline)
	}
}

func TestCheckCompletion_ReportsCompletedAndIncomplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("plan"), 0644); err != nil {
		t.Fatal(err)
	}
	// execution-log.md and verify-report.md are NOT created — those phases
	// should be reported as incomplete.

	testPhases := []Phase{
		{Name: "plan", ExpectedOutputs: []string{"PLAN.md"}},
		{Name: "execute", ExpectedOutputs: []string{"execution-log.md"}},
		{Name: "verify-fix", ExpectedOutputs: []string{"verify-report.md"}},
	}

	completed, incomplete := CheckCompletion(dir, testPhases)

	if !reflect.DeepEqual(completed, []string{"plan"}) {
		t.Errorf("expected completed=[plan], got %v", completed)
	}
	if !reflect.DeepEqual(incomplete, []string{"execute", "verify-fix"}) {
		t.Errorf("expected incomplete=[execute verify-fix], got %v", incomplete)
	}
}

func TestCheckCompletion_RequiresAllExpectedOutputsForOnePhase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	// b.txt is missing — the phase requires both.

	testPhases := []Phase{
		{Name: "multi-output", ExpectedOutputs: []string{"a.txt", "b.txt"}},
	}

	completed, incomplete := CheckCompletion(dir, testPhases)
	if len(completed) != 0 {
		t.Errorf("expected no completed phases, got %v", completed)
	}
	if !reflect.DeepEqual(incomplete, []string{"multi-output"}) {
		t.Errorf("expected incomplete=[multi-output], got %v", incomplete)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/phases/... -v`
Expected: FAIL — `package phases` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/phases/phases.go`:

```go
package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Phase describes one entry in phases.json — a named unit of LLM work
// that the orchestrator skill loads and runs.
type Phase struct {
	Name            string   `json:"name"`
	Skill           string   `json:"skill"`
	ExpectedOutputs []string `json:"expected_outputs"`
	Description     string   `json:"description,omitempty"`
}

// DefaultPipeline is the fixed POC pipeline: plan, execute, verify-fix.
// verify-fix is one phase (not two) — the verify skill owns an internal
// build/fix/re-verify loop, max 3 iterations.
func DefaultPipeline() []Phase {
	return []Phase{
		{Name: "plan", Skill: "plan/SKILL.md", ExpectedOutputs: []string{"PLAN.md"}},
		{Name: "execute", Skill: "execute/SKILL.md", ExpectedOutputs: []string{"execution-log.md"}},
		{
			Name:            "verify-fix",
			Skill:           "verify/SKILL.md",
			ExpectedOutputs: []string{"verify-report.md"},
			Description:     "Verify build, then fix errors iteratively (max 3 iterations, internal to this skill).",
		},
	}
}

// WriteJSON writes the phase list to path as JSON, for the orchestrator
// skill to read.
func WriteJSON(path string, phases []Phase) error {
	data, err := json.MarshalIndent(phases, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal phases: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// CheckCompletion reports which phases completed (all ExpectedOutputs
// exist under repoDir) and which didn't. This is how the harness builds
// session.json's steps_completed/steps_failed — the ACP stream's terminal
// event only reports overall session status, not per-phase status.
func CheckCompletion(repoDir string, phases []Phase) (completed, incomplete []string) {
	for _, p := range phases {
		allExist := true
		for _, out := range p.ExpectedOutputs {
			if _, err := os.Stat(filepath.Join(repoDir, out)); err != nil {
				allExist = false
				break
			}
		}
		if allExist {
			completed = append(completed, p.Name)
		} else {
			incomplete = append(incomplete, p.Name)
		}
	}
	return completed, incomplete
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/phases/... -v`
Expected: PASS for all 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/phases/phases.go internal/phases/phases_test.go
git commit -m "feat: add phases package for pipeline definition and completion tracking"
```

---

### Task 13: `internal/session` — session.json and results.json

Structs and writers for the two JSON files the harness produces. Field names and shapes match the spec exactly (`models` list keyed by `role`, not a flat single model+usage pair).

**Files:**
- Create: `internal/session/session.go`
- Test: `internal/session/session_test.go`

**Interfaces:**
- Produces:
  - `type TokenUsage struct { InputTokens, OutputTokens int }`
  - `type ModelUsage struct { Role, Provider, Name string; TokenUsage TokenUsage }`
  - `type GitInfo struct { TargetBranch string; Commits int; LastCommitSHA string }`
  - `type Session struct { SessionID, Status string; StartedAt, CompletedAt time.Time; DurationSeconds int; Runtime string; Models []ModelUsage; StepsCompleted, StepsFailed []string; Git GitInfo }`
  - `func (s Session) WriteTo(path string) error`
  - `type Results struct { Status string; ExitCode, DurationSeconds int; Git GitInfo }`
  - `func (r Results) WriteTo(path string) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/session/session_test.go`:

```go
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSession_WriteTo_ProducesExpectedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	s := Session{
		SessionID:       "sess-1",
		Status:          "complete",
		StartedAt:       time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		CompletedAt:     time.Date(2026, 7, 1, 10, 45, 0, 0, time.UTC),
		DurationSeconds: 2700,
		Runtime:         "goose",
		Models: []ModelUsage{
			{
				Role:     "primary",
				Provider: "anthropic",
				Name:     "claude-sonnet-4-20250514",
				TokenUsage: TokenUsage{
					InputTokens:  125000,
					OutputTokens: 45000,
				},
			},
		},
		StepsCompleted: []string{"detect", "plan", "execute", "verify-fix"},
		StepsFailed:    []string{},
		Git: GitInfo{
			TargetBranch:  "konveyor/migrate-app-123",
			Commits:       12,
			LastCommitSHA: "abc1234",
		},
	}

	if err := s.WriteTo(path); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected session.json to exist: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("session.json is not valid JSON: %v", err)
	}

	if raw["session_id"] != "sess-1" {
		t.Errorf("expected session_id sess-1, got %v", raw["session_id"])
	}
	models, ok := raw["models"].([]interface{})
	if !ok || len(models) != 1 {
		t.Fatalf("expected models to be a list of length 1, got %v", raw["models"])
	}
	firstModel := models[0].(map[string]interface{})
	if firstModel["role"] != "primary" {
		t.Errorf("expected model role 'primary', got %v", firstModel["role"])
	}
	if _, hasStage := raw["stage"]; hasStage {
		t.Error("session.json must NOT have a top-level 'stage' field (dropped per spec)")
	}
}

func TestResults_WriteTo_ProducesExpectedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")

	r := Results{
		Status:          "succeeded",
		ExitCode:        0,
		DurationSeconds: 2700,
		Git: GitInfo{
			TargetBranch:  "konveyor/migrate-app-123",
			Commits:       12,
			LastCommitSHA: "abc1234",
		},
	}

	if err := r.WriteTo(path); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected results.json to exist: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("results.json is not valid JSON: %v", err)
	}
	gitField, ok := raw["git"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected git field to be an object, got %v", raw["git"])
	}
	// results.json's git object must use the SAME field names as
	// session.json's (target_branch, commits) — this was a documented
	// regression fix in the spec review.
	if gitField["target_branch"] != "konveyor/migrate-app-123" {
		t.Errorf("expected target_branch field, got %v", gitField)
	}
	if gitField["commits"] != float64(12) {
		t.Errorf("expected commits field, got %v", gitField)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/session/... -v`
Expected: FAIL — `package session` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/session/session.go`:

```go
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ModelUsage struct {
	Role       string     `json:"role"`
	Provider   string     `json:"provider"`
	Name       string     `json:"name"`
	TokenUsage TokenUsage `json:"token_usage"`
}

// GitInfo is shared verbatim between session.json and results.json —
// keeping one shared type prevents the two files' git objects from
// drifting to different field names (a bug caught in spec review).
type GitInfo struct {
	TargetBranch  string `json:"target_branch"`
	Commits       int    `json:"commits"`
	LastCommitSHA string `json:"last_commit_sha"`
}

// Session is written by the harness to .konveyor/session.json and pushed
// to git. It intentionally has no "stage" field — this document has no
// defined stage vocabulary yet (that belongs to a future AgentPlaybook
// design). models is a list keyed by role (not a flat single model) to
// match PR #295's primary/efficient/planner convention.
type Session struct {
	SessionID       string       `json:"session_id"`
	Status          string       `json:"status"`
	StartedAt       time.Time    `json:"started_at"`
	CompletedAt     time.Time    `json:"completed_at"`
	DurationSeconds int          `json:"duration_seconds"`
	Runtime         string       `json:"runtime"`
	Models          []ModelUsage `json:"models"`
	StepsCompleted  []string     `json:"steps_completed"`
	StepsFailed     []string     `json:"steps_failed"`
	Git             GitInfo      `json:"git"`
}

func (s Session) WriteTo(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Results is written by the harness to the pod-local /.konveyor/results.json
// (NOT committed to git). Read by the controller after pod completion.
type Results struct {
	Status          string  `json:"status"`
	ExitCode        int     `json:"exit_code"`
	DurationSeconds int     `json:"duration_seconds"`
	Git             GitInfo `json:"git"`
}

func (r Results) WriteTo(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/session/... -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/session/session.go internal/session/session_test.go
git commit -m "feat: add session package for session.json and results.json"
```

---

### Task 14: `cmd/results` — konveyor-results binary

**Files:**
- Create: `cmd/results/main.go`

**Interfaces:**
- Consumes: `session.Results`, `session.Results.WriteTo` from Task 13.
- Produces: the `konveyor-results` binary, invoked as `konveyor-results --exit-code <N>`.

**Note:** `cmd/harness` (Task 16) writes `/.konveyor/results.json` directly via the `internal/session` package rather than shelling out to this binary — that's simpler for the one caller that needs it. This binary still exists on PATH per the spec's utility listing, for manual/advanced use (e.g. `migration-harness step` style invocations, matching the existing bash CLI's `step` subcommand pattern).

- [ ] **Step 1: Write the implementation**

Create `cmd/results/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/session"
)

func main() {
	exitCode := flag.Int("exit-code", 0, "exit code of the goose session")
	flag.Parse()

	status := "succeeded"
	if *exitCode != 0 {
		status = "failed"
	}
	res := session.Results{
		Status:   status,
		ExitCode: *exitCode,
	}
	if err := res.WriteTo("/.konveyor/results.json"); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-results: "+err.Error())
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Build and manually verify**

```bash
go build -o /tmp/konveyor-results ./cmd/results
mkdir -p /tmp/konveyor-results-test
cd /tmp/konveyor-results-test
mkdir -p .konveyor
sudo mkdir -p /.konveyor 2>/dev/null || mkdir -p /tmp/fake-root/.konveyor
```

Since `/.konveyor/` is a real absolute path that may require permissions locally, the simplest verification is a build check plus reading the source — the logic is a two-line pass-through of already-tested `internal/session` code:

```bash
go build -o /tmp/konveyor-results ./cmd/results
```

Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/results/main.go
git commit -m "feat: add konveyor-results binary"
```

---

### Task 15: `internal/acp` — ACP client (event-stream driven)

The riskiest part of the system (see spec's "Known Unknowns — Requires Prototyping"). Implements `NewSession`, `Prompt` (fire-and-forget), and `Stream` (consumes newline-delimited JSON events until a terminal event), tested against a local `httptest` fake server that speaks the assumed protocol shape. **This assumed shape is unverified against real goose** — Task 22 is the manual verification step against a real `goose serve` instance.

**Files:**
- Create: `internal/acp/client.go`
- Test: `internal/acp/client_test.go`

**Interfaces:**
- Produces:
  - `type Event struct { Type string; Data json.RawMessage }`
  - `func New(baseURL string) *Client`
  - `func (c *Client) WaitReady(timeout time.Duration) error`
  - `func (c *Client) NewSession() (string, error)`
  - `func (c *Client) Prompt(sessionID, message string) error`
  - `func (c *Client) Stream(ctx context.Context, sessionID string) (<-chan Event, error)`
  - `func IsTerminal(e Event) bool`
  - `func UsageFromEvent(e Event) (input, output int)`

- [ ] **Step 1: Write the failing tests**

Create `internal/acp/client_test.go`:

```go
package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_WaitReady_SucceedsOnceServerResponds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	if err := client.WaitReady(2 * time.Second); err != nil {
		t.Fatalf("WaitReady failed: %v", err)
	}
}

func TestClient_WaitReady_TimesOutIfServerNeverResponds(t *testing.T) {
	client := New("http://127.0.0.1:1") // nothing listens here
	err := client.WaitReady(300 * time.Millisecond)
	if err == nil {
		t.Fatal("expected WaitReady to time out and return an error")
	}
}

func TestClient_NewSessionAndPrompt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		switch req.Method {
		case "session/new":
			json.NewEncoder(w).Encode(newSessionResult{SessionID: "sess-1"})
		case "session/prompt":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	sessionID, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("expected session id 'sess-1', got %q", sessionID)
	}
	if err := client.Prompt(sessionID, "hello"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
}

func TestClient_NewSession_ErrorsOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL)
	if _, err := client.NewSession(); err == nil {
		t.Fatal("expected an error on 500 response")
	}
}

func TestClient_Stream_StopsAtTerminalEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		events := []string{
			`{"type":"usage","data":{"input_tokens":100,"output_tokens":20}}`,
			`{"type":"usage","data":{"input_tokens":50,"output_tokens":10}}`,
			`{"type":"complete"}`,
		}
		for _, e := range events {
			fmt.Fprintln(w, e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := client.Stream(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var seen []Event
	for e := range events {
		seen = append(seen, e)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 events, got %d", len(seen))
	}
	if !IsTerminal(seen[len(seen)-1]) {
		t.Fatalf("expected last event to be terminal, got %+v", seen[len(seen)-1])
	}
	if IsTerminal(seen[0]) {
		t.Fatalf("expected first event to NOT be terminal, got %+v", seen[0])
	}
}

func TestClient_Stream_ClosesChannelWhenContextCancelled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/acp/sessions/sess-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		fmt.Fprintln(w, `{"type":"usage","data":{"input_tokens":1,"output_tokens":1}}`)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done() // hang until the client disconnects
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithCancel(context.Background())

	events, err := client.Stream(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	<-events // consume the one event
	cancel()

	// The channel must close (not hang) once the context is cancelled.
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected channel to be closed after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close after cancellation")
	}
}

func TestUsageFromEvent_ExtractsTokenCounts(t *testing.T) {
	e := Event{Type: "usage", Data: json.RawMessage(`{"input_tokens":100,"output_tokens":20}`)}
	input, output := UsageFromEvent(e)
	if input != 100 || output != 20 {
		t.Errorf("expected (100, 20), got (%d, %d)", input, output)
	}
}

func TestUsageFromEvent_ReturnsZeroForNonUsageEvent(t *testing.T) {
	e := Event{Type: "complete"}
	input, output := UsageFromEvent(e)
	if input != 0 || output != 0 {
		t.Errorf("expected (0, 0) for non-usage event, got (%d, %d)", input, output)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		eventType string
		want      bool
	}{
		{"complete", true},
		{"failed", true},
		{"usage", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsTerminal(Event{Type: c.eventType}); got != c.want {
			t.Errorf("IsTerminal(%q) = %v, want %v", c.eventType, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/acp/... -v`
Expected: FAIL — `package acp` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/acp/client.go`:

```go
// Package acp implements a client for goose's ACP (Agent Client Protocol)
// endpoint. The exact wire protocol (method names, event shapes, terminal
// semantics) is UNVERIFIED against goose's real implementation — see the
// design spec's "Known Unknowns — Requires Prototyping" section. This
// client is built against the best-documented assumption (JSON-RPC-style
// POST calls, newline-delimited JSON events over a streamed HTTP GET) and
// is deliberately isolated behind this package so the assumption can be
// swapped out without touching cmd/harness.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event is one message from a session's event stream.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// IsTerminal reports whether e signals the end of a session.
func IsTerminal(e Event) bool {
	return e.Type == "complete" || e.Type == "failed"
}

type usageData struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// UsageFromEvent extracts token counts from a "usage" event. Returns
// (0, 0) for any other event type or malformed data.
func UsageFromEvent(e Event) (input, output int) {
	if e.Type != "usage" {
		return 0, 0
	}
	var u usageData
	if err := json.Unmarshal(e.Data, &u); err != nil {
		return 0, 0
	}
	return u.InputTokens, u.OutputTokens
}

// Client drives a goose serve instance's ACP endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// WaitReady polls the base ACP endpoint until it responds or timeout elapses.
func (c *Client) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := c.http.Get(c.baseURL + "/acp")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("acp endpoint not ready after %s: %w", timeout, lastErr)
}

type rpcRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type newSessionResult struct {
	SessionID string `json:"session_id"`
}

// NewSession creates a new goose session and returns its ID.
func (c *Client) NewSession() (string, error) {
	body, err := json.Marshal(rpcRequest{Method: "session/new"})
	if err != nil {
		return "", fmt.Errorf("marshal session/new request: %w", err)
	}
	resp, err := c.http.Post(c.baseURL+"/acp", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("session/new request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session/new returned status %d", resp.StatusCode)
	}
	var result newSessionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session/new response: %w", err)
	}
	return result.SessionID, nil
}

type promptParams struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// Prompt sends the initial message to a session. This is fire-and-forget —
// it acks receipt and does not block for the full pipeline duration.
// Callers must consume Stream to know when the session actually finishes.
func (c *Client) Prompt(sessionID, message string) error {
	body, err := json.Marshal(rpcRequest{
		Method: "session/prompt",
		Params: promptParams{SessionID: sessionID, Message: message},
	})
	if err != nil {
		return fmt.Errorf("marshal session/prompt request: %w", err)
	}
	resp, err := c.http.Post(c.baseURL+"/acp", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("session/prompt request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("session/prompt returned status %d", resp.StatusCode)
	}
	return nil
}

// Stream opens the session's event stream and returns a channel of events.
// The channel closes when a terminal event arrives, the stream ends, or
// ctx is cancelled — whichever happens first. Malformed lines are skipped
// rather than crashing the reader.
func (c *Client) Stream(ctx context.Context, sessionID string) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/acp/sessions/"+sessionID+"/events", nil)
	if err != nil {
		return nil, fmt.Errorf("build stream request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("event stream returned status %d", resp.StatusCode)
	}

	events := make(chan Event)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var e Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}
			select {
			case events <- e:
			case <-ctx.Done():
				return
			}
			if IsTerminal(e) {
				return
			}
		}
	}()
	return events, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/acp/... -v`
Expected: PASS for all 10 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/acp/client.go internal/acp/client_test.go
git commit -m "feat: add ACP client (event-stream driven, unverified against real goose)"
```

---

### Task 16: `cmd/harness` — konveyor-harness entrypoint

Ties everything together: the full lifecycle from the spec's "Harness Lifecycle" section. This is integration code — most of its logic is already unit-tested via the packages it calls. The one pure-logic piece (`buildPromptMessage`) gets its own unit test; the rest is verified manually in Task 22.

**Files:**
- Create: `cmd/harness/main.go`
- Test: `cmd/harness/main_test.go`

**Interfaces:**
- Consumes: everything from Tasks 3, 4, 8, 10 (used internally by cmd/detect, not directly), 12, 13, 15.
- Produces: the `konveyor-harness` binary, invoked as `konveyor-harness run`.

- [ ] **Step 1: Write the failing test for the one pure-logic function**

Create `cmd/harness/main_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestBuildPromptMessage_IncludesSkillsDirInstructionsAndPhasesPath(t *testing.T) {
	msg := buildPromptMessage("/opt/skills", "/workspace/instructions.md", "/workspace/repo/phases.json")

	if !strings.Contains(msg, "/opt/skills/orchestrator/SKILL.md") {
		t.Errorf("expected message to reference orchestrator skill path, got: %s", msg)
	}
	if !strings.Contains(msg, "/workspace/instructions.md") {
		t.Errorf("expected message to reference instructions path, got: %s", msg)
	}
	if !strings.Contains(msg, "/workspace/repo/phases.json") {
		t.Errorf("expected message to reference phases.json path, got: %s", msg)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/harness/... -v`
Expected: FAIL — `undefined: buildPromptMessage` (no `main.go` yet).

- [ ] **Step 3: Write the implementation**

Create `cmd/harness/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	if err := stopGoose(gooseCmd); err != nil {
		return fmt.Errorf("stop goose: %w", err)
	}
	gooseStopped = true

	completedSteps, failedSteps := phases.CheckCompletion(repoDir, pipeline)
	completedSteps = append([]string{"detect"}, completedSteps...)

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
		Git: session.GitInfo{
			TargetBranch: params.TargetBranch,
		},
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
	if finalStatus != "complete" {
		exitCode = 1
	}
	res := session.Results{
		Status:          finalStatus,
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
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/harness/... -v`
Expected: PASS for `TestBuildPromptMessage_IncludesSkillsDirInstructionsAndPhasesPath`.

- [ ] **Step 5: Build the full binary to confirm everything compiles together**

Run: `go build -o /tmp/konveyor-harness ./cmd/harness`
Expected: builds with no errors — this is the integration check that all packages wire together correctly. Actually running it requires a real workspace, git credentials, and a real `goose` binary, which is covered by the manual runbook in Task 22.

- [ ] **Step 6: Commit**

```bash
git add cmd/harness/main.go cmd/harness/main_test.go
git commit -m "feat: add konveyor-harness entrypoint"
```

---

### Task 17: Build all binaries together and run the full test suite

A checkpoint task: confirm the whole module builds and every test passes before moving to skills/Dockerfile work.

**Files:** none created — verification only.

**Interfaces:** none.

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... -v`
Expected: PASS for every package (`internal/git`, `internal/config`, `internal/detect`, `internal/phases`, `internal/session`, `internal/acp`, `cmd/push`, `cmd/harness`).

- [ ] **Step 2: Build every binary**

```bash
mkdir -p bin
go build -o bin/konveyor-harness ./cmd/harness
go build -o bin/konveyor-clone ./cmd/clone
go build -o bin/konveyor-push ./cmd/push
go build -o bin/konveyor-configure ./cmd/configure
go build -o bin/konveyor-detect ./cmd/detect
go build -o bin/konveyor-results ./cmd/results
ls bin/
```

Expected: all 6 binaries listed with no build errors.

- [ ] **Step 3: Run `go vet` to catch obvious mistakes**

Run: `go vet ./...`
Expected: no output (no issues found).

- [ ] **Step 4: Commit the bin/ gitignore (binaries themselves shouldn't be committed)**

```bash
cat > .gitignore <<'EOF'
/bin/
EOF
git add .gitignore
git commit -m "chore: ignore built binaries in bin/"
```

---

### Task 18: Port the orchestrator skill

Ports `_legacy/meta-skill/orchestrate.md` into the new `skills/orchestrator/SKILL.md`, updated to read `phases.json` (already generated by the harness) and call `konveyor-push` at phase boundaries — matching the spec's "Push Model" table.

**Files:**
- Create: `skills/orchestrator/SKILL.md`

**Interfaces:**
- Consumes: `phases.json` (produced by `internal/phases.WriteJSON` in Task 16's harness flow), `instructions.md` (written by the harness), the `konveyor-push` binary on PATH.
- Produces: `.konveyor/handoff.md`, written and pushed by this skill before the session ends (per spec — never by the harness).

- [ ] **Step 1: Write the skill**

Create `skills/orchestrator/SKILL.md`:

```markdown
---
name: orchestrator
description: Reads phases.json and executes each phase's skill in strict order. Baked into the image — the brain of the migration pipeline. Triggered by the harness's initial session/prompt message.
type: skill
---

# Orchestrator

You are the phase execution engine for a code migration. Your job is to
execute the phases defined in `phases.json` by loading and following each
phase's skill instructions, in order.

## What You Do

1. Read `phases.json` from the path given in your initial prompt.
2. For each phase, in order: load the named skill file, follow its
   instructions completely, then verify its `expected_outputs` exist.
3. If a phase's `expected_outputs` are missing after running it, stop —
   do not proceed to the next phase.
4. Before your session ends (whether all phases succeeded or one failed),
   write and push `.konveyor/handoff.md` yourself — the harness does not
   write this file.

## Step 1: Read phases.json

Your initial prompt tells you where `phases.json` and `instructions.md`
live. Read both:

```bash
cat /workspace/repo/phases.json
cat /workspace/instructions.md
```

`instructions.md` is the user's migration request (e.g. "Migrate this
application from Java EE 7 to Quarkus 3.x"). Keep it in mind throughout —
each phase skill uses it to know what transformation to perform.

## Step 2: Execute each phase in order

For each phase object in the `phases.json` array:

1. Log: `Orchestrator: starting phase {name}`
2. Read the skill file at `{KONVEYOR_SKILLS_DIR}/{phase.skill}` (the path
   in `phases.json` is relative to your skills directory — check the
   `KONVEYOR_SKILLS_DIR` env var, defaulting to `/opt/skills` if unset).
3. Follow that skill's instructions completely. It will read/write files
   under `/workspace/repo/` and may call `konveyor-push` itself at its own
   checkpoints (see each skill for its own push instructions — you do not
   need to push on the phase's behalf, only verify outputs after).
4. After the skill's instructions complete, check that every path in
   `phase.expected_outputs` exists under `/workspace/repo/`:

```bash
for output in <expected_outputs from this phase>; do
  test -f "/workspace/repo/$output" && echo "OK: $output" || echo "MISSING: $output"
done
```

5. If any output is missing: log the failure, stop processing further
   phases, and go to Step 3 (write handoff.md) with status `failed`.
6. If all outputs exist: log success and continue to the next phase.

## Step 3: Write and push handoff.md

Before your session ends — this is your responsibility, not the harness's:

```bash
cat > /workspace/repo/.konveyor/handoff.md <<'EOF'
# Stage Handoff

## Status
<complete if all phases succeeded, failed if one didn't>

## What Was Done
<summarize what each phase actually did — plan produced N steps,
execute migrated N files, verify-fix result>

## What Needs to Happen Next
<remaining work, or "none — migration complete" if status is complete>

## Key Findings
<architectural observations, migration patterns that worked or failed,
warnings for anyone reviewing this branch>
EOF

konveyor-push --message "konveyor: stage handoff" .konveyor/handoff.md
```

Write real content into each section based on what actually happened
during this session — do not leave the placeholder text above.

## Rules

- Execute phases in the exact order they appear in `phases.json`. Never
  skip, reorder, or run them in parallel.
- Do only what each phase's skill instructs. Don't add extra steps or
  "improve" things beyond what the skill asks for.
- Stop immediately if a phase's expected_outputs are missing — do not
  attempt the next phase.
- Always write and push `.konveyor/handoff.md` before your session ends,
  even if a phase failed.
- You do not manage git credentials — call `konveyor-push` as a CLI tool
  when a skill instructs you to; it handles credentials itself.
```

- [ ] **Step 2: Verify the frontmatter is valid YAML**

Run: `python3 -c "import yaml, sys; content = open('skills/orchestrator/SKILL.md').read(); frontmatter = content.split('---')[1]; yaml.safe_load(frontmatter); print('OK')"`
Expected output: `OK`

(If `python3`/`pyyaml` isn't available locally, a manual visual check of the `---`-delimited frontmatter block is an acceptable substitute — it's 3 simple key-value lines.)

- [ ] **Step 3: Commit**

```bash
git add skills/orchestrator/SKILL.md
git commit -m "feat: port orchestrator skill from _legacy/meta-skill"
```

---

### Task 19: Port the plan skill

Ports `_legacy/skill-bundle/goose-migration/skills/migration-plan/SKILL.md` into `skills/plan/SKILL.md`, with paths updated to the new layout and a `konveyor-push` call added after writing `PLAN.md`.

**Files:**
- Create: `skills/plan/SKILL.md`

**Interfaces:**
- Consumes: `detect.json`, `graph.json` (already on disk and pushed by the time this phase runs), rule skills mounted under `KONVEYOR_SKILLS_DIR` (e.g. `javaee-quarkus/`).
- Produces: `PLAN.md` at the repo root (this phase's `expected_outputs` entry).

- [ ] **Step 1: Write the skill**

Create `skills/plan/SKILL.md`:

```markdown
---
name: plan
description: Reads detect.json and graph.json, selects a matching migration rule skill if one is mounted, and writes PLAN.md with a specific, ordered migration plan for this project. Does not modify any source files.
type: skill
---

# Plan

Read the project's structure, pick the right migration pattern, and write
`PLAN.md`. This phase does not touch source files — planning only.

## Phase 1 — Read what's already known

`detect.json` and `graph.json` were produced by the harness's detect step
(no LLM was involved) and already exist at the repo root:

```bash
cat /workspace/repo/detect.json
```

`graph.json` is the full code graph — read it selectively (it can be
large), focusing on `nodes`, `communities`, and any node with
`degree > 20` (flagged as `god_nodes` in `detect.json` — these are
high-risk, central files).

## Phase 2 — Select a migration reference, if one is mounted

Check `{KONVEYOR_SKILLS_DIR}/` (default `/opt/skills`) for a rule skill
matching this project's stack — for example `javaee-quarkus/SKILL.md` for
a Maven project with `javax.ejb`/`javax.jms` usage, or
`python2-to-python3/SKILL.md` for a Python 2 codebase. Match based on
`detect.json`'s `manifests` block:

- `pom_xml: true` → check for a Java rule skill
- `requirements_txt` or `setup_py: true` → check for a Python rule skill

If a matching rule skill is mounted, read it — it contains the migration
order, transformation patterns, and files to delete/create. If none
matches, proceed with generic judgment based on `instructions.md` and the
graph structure alone.

## Phase 3 — Read a few source files, selectively

Read the build manifest (`pom.xml`, `package.json`, etc. — always).
Beyond that, only read files the graph flags as complex (god nodes, or
files matching a rule skill's "complex pattern" list). Aim for 5-10 total
file reads across this phase — the graph and rule skill should cover
everything else.

## Phase 4 — Write PLAN.md

Write `/workspace/repo/PLAN.md` with this structure:

```markdown
# PLAN.md

## Goal
<restate the migration goal from instructions.md in one sentence>
- Reference used: <rule skill name, or "none">

## Project Summary
- Type: <Maven/Node/Python/etc, from detect.json manifests>
- Files affected: <N>
- Estimated complexity: <Low/Medium/High>

## Steps

### Step 1: <title>
- File: <exact path from repo root>
- Action: <CREATE | MODIFY | DELETE>
- What to do: <specific instructions>
- Depends on: <step numbers, or "none">

### Step 2: <title>
...

## Verification
<exact build/test command(s) to run, e.g. `mvn clean compile`>

## Notes
<gotchas, decisions made>
```

Rules for steps: one file per step, exact paths (not placeholders),
dependency order (steps others depend on come first), build config before
source before tests before deletions.

## Phase 5 — Push and finish

```bash
konveyor-push --message "konveyor: plan phase" PLAN.md
```

Report back: how many steps, which reference (if any) you used, and a
one-line summary of the plan.
```

- [ ] **Step 2: Verify the frontmatter is valid YAML**

Run: `python3 -c "import yaml; content = open('skills/plan/SKILL.md').read(); yaml.safe_load(content.split('---')[1]); print('OK')"`
Expected output: `OK`

- [ ] **Step 3: Commit**

```bash
git add skills/plan/SKILL.md
git commit -m "feat: port plan skill from _legacy/skill-bundle"
```

---

### Task 20: Port the execute skill

Ports `_legacy/recipes/execute.yaml`'s instructions into `skills/execute/SKILL.md` (converted from goose recipe YAML format to SKILL.md markdown), with a `konveyor-push` call after each migrated file.

**Files:**
- Create: `skills/execute/SKILL.md`

**Interfaces:**
- Consumes: `PLAN.md` (produced by the plan phase).
- Produces: `execution-log.md` at the repo root (this phase's `expected_outputs` entry), plus the actual migrated source files.

- [ ] **Step 1: Write the skill**

Create `skills/execute/SKILL.md`:

```markdown
---
name: execute
description: Reads PLAN.md and executes each step in order, transforming one file at a time. Pushes after each file. Writes execution-log.md with lessons learned.
type: skill
---

# Execute

Execute the approved plan from `PLAN.md`, one step at a time, in the
order the steps appear.

## Startup

Read `/workspace/repo/PLAN.md` in full — the Goal, Project Summary, and
every Step. This is your only planning context; don't re-derive anything
already decided during the plan phase.

## Per-Step Execution Loop

For each step in `PLAN.md`, in order:

1. Read the current content of the step's `File` (if it exists — CREATE
   steps won't have existing content).
2. Apply the transformation described in "What to do".
3. Write/edit/delete the file per the step's `Action`.
4. Append a line to `/workspace/repo/execution-log.md` (create it on the
   first step):

```markdown
## Step N: <title>
- Status: ok | failed
- Files touched: <list>
- Lesson: <anything genuinely useful for the verify-fix phase, or omit>
```

5. Push this step's changes:

```bash
konveyor-push --message "konveyor: migrate <filename>" <files touched in this step>
```

6. Move to the next step.

## Rules

- Execute steps in the exact order `PLAN.md` lists them — a step's
  "Depends on" field means those steps must already be done.
- Do not compile, test, or verify — that's the next phase's job.
- Do not touch files outside the current step's scope.
- If a step is unclear or fails, log it in `execution-log.md` under that
  step with `Status: failed` and continue to the next step rather than
  stopping the whole phase — the verify-fix phase will surface remaining
  problems via the build.

## Completion

Once every step in `PLAN.md` has an entry in `execution-log.md`, do a
final push if anything is unpushed:

```bash
konveyor-push --message "konveyor: execute phase complete" execution-log.md
```

Report back: how many steps succeeded vs failed, and any lessons worth
flagging for the verify-fix phase.
```

- [ ] **Step 2: Verify the frontmatter is valid YAML**

Run: `python3 -c "import yaml; content = open('skills/execute/SKILL.md').read(); yaml.safe_load(content.split('---')[1]); print('OK')"`
Expected output: `OK`

- [ ] **Step 3: Commit**

```bash
git add skills/execute/SKILL.md
git commit -m "feat: port execute skill from _legacy/recipes/execute.yaml"
```

---

### Task 21: Port the verify skill (merged with fix)

Ports `_legacy/recipes/verify.yaml` and `_legacy/recipes/fix.yaml` into a single `skills/verify/SKILL.md`, with the fix loop as an internal iteration (max 3 attempts) rather than a separate phase — matching the spec's `verify-fix` phase design.

**Files:**
- Create: `skills/verify/SKILL.md`

**Interfaces:**
- Consumes: `PLAN.md`'s Verification section, `execution-log.md` (for context on what execute attempted).
- Produces: `verify-report.md` at the repo root (this phase's `expected_outputs` entry).

- [ ] **Step 1: Write the skill**

Create `skills/verify/SKILL.md`:

```markdown
---
name: verify
description: Runs the build/test commands from PLAN.md's Verification section. If they fail, fixes errors one at a time and re-verifies, up to 3 iterations. Writes verify-report.md with the final state.
type: skill
---

# Verify (includes Fix)

Verify the migrated codebase builds, and fix build errors if it doesn't —
up to 3 iterations. This is one phase, not two: don't treat verify and
fix as separate steps requiring separate phases.json entries.

## Phase 1 — Read the verification command

Read `/workspace/repo/PLAN.md`'s `## Verification` section — it has the
exact build/test command(s) to run (e.g. `mvn clean compile`,
`npm test`). Ignore the rest of `PLAN.md` for now.

## Phase 2 — Run verification

Run the command(s) from Phase 1. Capture:
- Build status (success/failure)
- Compilation/build errors (file, line, message) if it failed

## Phase 3 — Fix loop (only if Phase 2 failed)

Repeat up to 3 times:

1. Read `/workspace/repo/execution-log.md` for context on what the
   execute phase attempted for the file(s) with errors.
2. Pick the first remaining error. Make the minimal edit needed to fix
   it — only touch the file the error points at (or the build manifest,
   if it's a missing dependency). Do not refactor working code.
3. Re-run the verification command from Phase 1.
4. If it now passes, stop the loop. If it still fails, go to step 1 with
   the next error, up to 3 total iterations.

If still failing after 3 iterations, stop and report the remaining
errors — do not keep retrying beyond 3.

## Phase 4 — Write verify-report.md and push

```markdown
# Verify Report

## Build Status
<success | failure>

## Fix Iterations
<0-3>

## Fixes Applied
<description of what was fixed each iteration, or "none" if it passed
on the first try>

## Remaining Errors
<list, or "none">
```

```bash
konveyor-push --message "konveyor: verify-fix phase" verify-report.md
```

Also push any files changed during the fix loop, if not already pushed:

```bash
konveyor-push --message "konveyor: verify-fix phase" <files fixed during the loop>
```

## Rules

- Focus on build/compile errors first — test failures are acceptable to
  leave as remaining errors within the 3-iteration budget.
- Minimal, targeted fixes only. No refactoring.
- Maximum 3 fix iterations. Report and stop after that, even if errors
  remain.
```

- [ ] **Step 2: Verify the frontmatter is valid YAML**

Run: `python3 -c "import yaml; content = open('skills/verify/SKILL.md').read(); yaml.safe_load(content.split('---')[1]); print('OK')"`
Expected output: `OK`

- [ ] **Step 3: Commit**

```bash
git add skills/verify/SKILL.md
git commit -m "feat: port verify skill, merging verify+fix into one iterative phase"
```

---

### Task 22: Dockerfile — agent-base and agent-base-goose

Builds the two-layer image per the spec: `agent-base` (no entrypoint, no runtime) and `agent-base-goose` (adds goose, sets the entrypoint).

**Files:**
- Create: `dockerfiles/agent-base.Dockerfile`
- Create: `dockerfiles/agent-base-goose.Dockerfile`

**Interfaces:**
- Consumes: the 6 built binaries (Task 17), `scripts/git-askpass.sh` (Task 5), `skills/` (Tasks 18-21).

- [ ] **Step 1: Write agent-base.Dockerfile**

Create `dockerfiles/agent-base.Dockerfile`:

```dockerfile
# agent-base — building block, NO entrypoint, NO runtime.
# The entrypoint lives on agent-base-goose (or future agent-base-<runtime>
# images) because konveyor-harness requires a runtime to launch.
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git jq curl python3 python3-pip && microdnf clean all

# graphify (required by konveyor-detect)
RUN pip3 install --no-cache-dir graphifyy==0.7.17

# skillctl (skill discovery, from the skillimage project)
RUN curl -fsSL <skillctl-release-url> -o /usr/local/bin/skillctl \
    && chmod +x /usr/local/bin/skillctl

# Go binaries (built in CI via `go build ./cmd/...`, copied in from bin/)
COPY bin/konveyor-clone      /usr/local/bin/
COPY bin/konveyor-push       /usr/local/bin/
COPY bin/konveyor-configure  /usr/local/bin/
COPY bin/konveyor-detect     /usr/local/bin/
COPY bin/konveyor-results    /usr/local/bin/
COPY bin/konveyor-harness    /usr/local/bin/

# GIT_ASKPASS helper for konveyor-push re-authentication
COPY scripts/git-askpass.sh /usr/local/bin/git-askpass.sh
RUN chmod +x /usr/local/bin/git-askpass.sh

# Pipeline skills — baked in (POC deviation from PR #296's "never baked
# in" model, see design spec's Key Decisions and Open Questions)
COPY skills/ /opt/skills/

# Writable dirs for non-root (OpenShift restricted SCC compatible)
RUN mkdir -p /workspace /.konveyor \
    && chmod 775 /workspace /.konveyor \
    && chgrp -R 0 /workspace /.konveyor \
    && chmod -R g=u /workspace /.konveyor

WORKDIR /workspace
# No ENTRYPOINT here — this image has no runtime to launch.
```

- [ ] **Step 2: Write agent-base-goose.Dockerfile**

Create `dockerfiles/agent-base-goose.Dockerfile`:

```dockerfile
# agent-base-goose — extends agent-base with the goose runtime and sets
# the entrypoint. This is the image the POC actually deploys.
FROM localhost/agent-base:latest

RUN microdnf install -y bzip2 && microdnf clean all \
    && ARCH=$(uname -m) \
    && if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH="x86_64-unknown-linux-gnu"; \
       elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH="aarch64-unknown-linux-gnu"; \
       else echo "Unsupported architecture: $ARCH" && exit 1; fi \
    && curl -fsSL "https://github.com/block/goose/releases/download/stable/goose-${GOOSE_ARCH}.tar.bz2" -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /usr/local/bin \
    && chmod +x /usr/local/bin/goose \
    && rm -f /tmp/goose.tar.bz2

ENTRYPOINT ["konveyor-harness"]
CMD ["run"]
```

- [ ] **Step 3: Build agent-base locally to confirm it builds**

```bash
mkdir -p bin
go build -o bin/konveyor-harness ./cmd/harness
go build -o bin/konveyor-clone ./cmd/clone
go build -o bin/konveyor-push ./cmd/push
go build -o bin/konveyor-configure ./cmd/configure
go build -o bin/konveyor-detect ./cmd/detect
go build -o bin/konveyor-results ./cmd/results

docker build -f dockerfiles/agent-base.Dockerfile -t agent-base:latest .
```

Expected: image builds successfully. (Replace `<skillctl-release-url>` with the actual skillctl release URL before running this for real — it's a placeholder pending that project's release artifact location, same as the design spec left it.)

- [ ] **Step 4: Build agent-base-goose on top of it**

```bash
docker build -f dockerfiles/agent-base-goose.Dockerfile -t agent-base-goose:latest .
docker run --rm agent-base-goose:latest --help 2>&1 || true
```

Expected: image builds successfully; running it without required env vars fails with a clear "KONVEYOR_PARAM_SOURCE_URL is required" style error (not a crash) — this confirms the entrypoint and config validation wire together correctly end to end.

- [ ] **Step 5: Commit**

```bash
git add dockerfiles/agent-base.Dockerfile dockerfiles/agent-base-goose.Dockerfile
git commit -m "feat: add agent-base and agent-base-goose Dockerfiles"
```

---

### Task 23: Manual verification runbook for the ACP mechanism

The spec explicitly flags `internal/acp`'s design as unverified against real goose behavior. This task is a documented, human-run verification procedure — not automatable — that confirms (or corrects) the assumptions baked into Task 15 before this harness is used for a real migration.

**Files:**
- Create: `docs/superpowers/plans/acp-verification-runbook.md`

**Interfaces:** none — this is a runbook, not code.

- [ ] **Step 1: Write the runbook**

Create `docs/superpowers/plans/acp-verification-runbook.md`:

```markdown
# ACP Mechanism Verification Runbook

Run this manually against a real `goose serve` instance before relying on
`internal/acp` for a production migration. This confirms or corrects the
assumptions documented in the design spec's "Known Unknowns" section.

## Prerequisites

- `goose` installed and configured with a working LLM provider.
- `GOOSE_SERVER__SECRET_KEY` set to any value.

## Procedure

1. Start goose serve and inspect its actual endpoints:

   ```bash
   GOOSE_SERVER__SECRET_KEY=test-key goose serve --port 4000 &
   curl -v http://localhost:4000/acp
   ```

   Record: does `/acp` respond at all without auth, or does it require
   the secret key in a header/query param immediately? Compare against
   `internal/acp.Client.WaitReady`'s bare GET request.

2. Attempt to create a session. Try the assumed shape first:

   ```bash
   curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/new"}'
   ```

   Record the actual response shape. Compare against
   `internal/acp.newSessionResult{SessionID string}`. If the real method
   name or response field differs, update `internal/acp/client.go`'s
   `NewSession` accordingly and add a regression test to
   `internal/acp/client_test.go` documenting the real shape.

3. Attempt to prompt the session and observe whether it blocks:

   ```bash
   time curl -v -X POST http://localhost:4000/acp \
     -H "Content-Type: application/json" \
     -d '{"method":"session/prompt","params":{"session_id":"<id-from-step-2>","message":"say hello"}}'
   ```

   Record: does this return immediately (fire-and-forget, as assumed) or
   does it block until the agent's turn completes? This determines
   whether `internal/acp.Client.Prompt`'s current fire-and-forget
   assumption is correct or needs to change to a blocking call (which
   would also mean `Stream` becomes unnecessary for detecting
   completion).

4. Attempt to open the event stream:

   ```bash
   curl -N http://localhost:4000/acp/sessions/<id>/events
   ```

   Record: is this even a real endpoint? If not, find the real mechanism
   for observing session progress (check goose's `--help`, its source, or
   the ACP spec's deeper pages beyond the overview at
   agentclientprotocol.com). Record the actual event format (newline-JSON,
   SSE `data:` lines, WebSocket frames) and whether a terminal event
   exists and what it's called.

5. Check whether token usage appears anywhere in the above — in a stream
   event, in the session/prompt response, or nowhere (requiring a
   different source like goose's own logs).

6. Update `internal/acp/client.go` and its tests to match what was
   actually observed. Update the design spec's "Known Unknowns" section
   to mark resolved items and remove the "unverified" caveats language
   for anything confirmed here.

## Outcome

This task is complete when `internal/acp`'s tests reflect real, observed
goose behavior rather than the spec's inferred assumption, OR when a
decision is made (and documented) to switch to `goose acp` (stdio) for
driving instead — see the design spec's discussion of that alternative.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/plans/acp-verification-runbook.md
git commit -m "docs: add manual verification runbook for the ACP mechanism"
```

---

## Self-Review

**Spec coverage:**
- Architecture (Go entrypoint + utilities + skills) → Tasks 1-17, 22.
- `konveyor-clone`, `konveyor-push`, `konveyor-configure`, `konveyor-detect`, `konveyor-results` → Tasks 6, 7, 9, 11, 14.
- `konveyor-harness` lifecycle → Task 16.
- ACP event-stream mechanism + Known Unknowns → Task 15 (implementation) + Task 23 (verification runbook).
- Skills (orchestrator, plan, execute, verify-fix) → Tasks 18-21.
- Session handoff (`handoff.md` by skill, `session.json`/`results.json` by harness) → Task 13 (structs) + Task 18 (skill writes handoff.md) + Task 16 (harness writes session.json/results.json).
- Push model (harness-driven + skill-driven, distinct commit messages) → Task 4 (Push), Task 16 (harness calls), Tasks 18-21 (skill calls).
- Credential model (`KONVEYOR_GIT_USERNAME`/`TOKEN`, `GIT_ASKPASS`) → Tasks 3-5.
- `KONVEYOR_INSTRUCTIONS` distinct from `KONVEYOR_PARAM_*` → Task 8.
- `instructions.md` outside `/workspace/repo/` → Task 16.
- Image hierarchy (`agent-base` no entrypoint, `agent-base-goose` has it) → Task 22.
- `_legacy/` move → Task 1.
- CRD mapping section of the spec is documentation-only (no code to implement) — not a task; already captured in the spec itself for the controller side, which is a separate repo (`konveyor/agentic-controller`, per the spec's "What Lives Where" table).

**Placeholder scan:** No "TBD"/"TODO" in any task's code. The `<goose-release-url>` and `<skillctl-release-url>` placeholders in Dockerfiles are pre-existing placeholders from the design spec itself (real release URLs, not vague instructions) — flagged in Task 22 Step 3 as needing the real skillctl URL before a real build.

**Type consistency:** Verified `git.Credentials`, `phases.Phase`, `session.Session`/`session.Results`/`session.GitInfo`, `acp.Event`/`acp.Client` are used with identical field names and types everywhere they cross task boundaries (Task 16's `cmd/harness/main.go` is the integration point referencing all of them — cross-checked against each producing task's Interfaces block).

---

Plan complete and saved to `docs/superpowers/plans/2026-07-07-harness-restructure-implementation.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
