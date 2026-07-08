package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs_MessageAndFiles(t *testing.T) {
	message, files, err := parseArgs([]string{"--message", "hello world", "a.txt", "b.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if message != "hello world" {
		t.Errorf("expected message %q, got %q", "hello world", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt", "b.txt"}) {
		t.Errorf("expected files [a.txt b.txt], got %v", files)
	}
}

func TestParseArgs_DefaultMessageWhenOmitted(t *testing.T) {
	message, files, err := parseArgs([]string{"a.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if message != "konveyor: update" {
		t.Errorf("expected default message, got %q", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt"}) {
		t.Errorf("expected files [a.txt], got %v", files)
	}
}

func TestParseArgs_NoFilesMeansStageAll(t *testing.T) {
	message, files, err := parseArgs([]string{"--message", "commit everything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if message != "commit everything" {
		t.Errorf("expected message %q, got %q", "commit everything", message)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}

func TestParseArgs_TrailingMessageFlagReturnsError(t *testing.T) {
	// With the standard flag package, flag parsing stops consuming flags
	// at the first non-flag argument, so "--message" must appear before
	// any positional file args to be recognized as a flag at all (this
	// matches how konveyor-push is invoked in practice: --message first,
	// then files). A bare trailing "--message" with no value exercises
	// the value-less-flag error path.
	_, _, err := parseArgs([]string{"--message"})
	if err == nil {
		t.Fatal("expected error for trailing --message without a value")
	}
}

// TestRun_ReturnsErrorWhenCredentialsMissing exercises run()'s
// validation deterministically: with git credentials absent from the
// environment, run() must fail before ever touching a git repo or the
// network.
func TestRun_ReturnsErrorWhenCredentialsMissing(t *testing.T) {
	origUser, hadUser := os.LookupEnv("KONVEYOR_GIT_USERNAME")
	origToken, hadToken := os.LookupEnv("KONVEYOR_GIT_TOKEN")
	t.Cleanup(func() {
		if hadUser {
			os.Setenv("KONVEYOR_GIT_USERNAME", origUser)
		} else {
			os.Unsetenv("KONVEYOR_GIT_USERNAME")
		}
		if hadToken {
			os.Setenv("KONVEYOR_GIT_TOKEN", origToken)
		} else {
			os.Unsetenv("KONVEYOR_GIT_TOKEN")
		}
	})
	os.Unsetenv("KONVEYOR_GIT_USERNAME")
	os.Unsetenv("KONVEYOR_GIT_TOKEN")

	err := run([]string{"a.txt"})
	if err == nil {
		t.Fatal("expected an error when git credentials are missing, got none")
	}
	if !strings.Contains(err.Error(), "KONVEYOR_GIT_USERNAME") {
		t.Errorf("expected error to mention missing credentials, got: %v", err)
	}
}
