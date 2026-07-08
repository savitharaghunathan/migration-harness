package main

import (
	"os"
	"strings"
	"testing"
)

// TestRun_ErrorsWhenGraphifyUnavailable exercises the error-path wiring in
// run() when the graphify binary cannot be found on PATH. It forces PATH to
// a directory known not to contain graphify so the test is deterministic
// regardless of whether graphify happens to be installed on the host
// running the test.
func TestRun_ErrorsWhenGraphifyUnavailable(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", origPath)
	})
	if err := os.Setenv("PATH", t.TempDir()); err != nil {
		t.Fatalf("failed to set PATH: %v", err)
	}

	repoDir := t.TempDir()

	err := run(repoDir)
	if err == nil {
		t.Fatal("expected run() to return an error when graphify is not on PATH")
	}
	if !strings.Contains(err.Error(), "run graphify") {
		t.Errorf("expected error to wrap %q, got: %v", "run graphify", err)
	}
}
