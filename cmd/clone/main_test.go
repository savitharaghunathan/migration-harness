package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		wantURL string
		wantDst string
	}{
		{name: "zero args", args: []string{}, wantErr: true},
		{name: "one arg", args: []string{"https://example.com/repo.git"}, wantErr: true},
		{name: "two args", args: []string{"https://example.com/repo.git", "/tmp/dest"}, wantErr: false, wantURL: "https://example.com/repo.git", wantDst: "/tmp/dest"},
		{name: "three args", args: []string{"a", "b", "c"}, wantErr: true},
		{name: "four args", args: []string{"a", "b", "c", "d"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, dest, err := parseArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for args %v, got none", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for args %v: %v", tc.args, err)
			}
			if url != tc.wantURL {
				t.Errorf("expected url %q, got %q", tc.wantURL, url)
			}
			if dest != tc.wantDst {
				t.Errorf("expected dest %q, got %q", tc.wantDst, dest)
			}
		})
	}
}

// TestRun_ReturnsErrorForInvalidArgCount exercises run()'s argument
// validation without touching git or the network: an invalid arg count
// must fail in parseArgs before run() ever calls out to git.
func TestRun_ReturnsErrorForInvalidArgCount(t *testing.T) {
	err := run([]string{"only-one-arg"})
	if err == nil {
		t.Fatal("expected an error for an invalid arg count, got none")
	}
	if !strings.Contains(err.Error(), "expected exactly 2 args") {
		t.Errorf("expected error to mention arg count, got: %v", err)
	}
}

// TestRun_ReturnsErrorWhenCredentialsMissing exercises run()'s next
// validation step deterministically: with valid args but no git
// credentials in the environment, run() must fail before attempting any
// actual clone (no network access required for this test).
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

	err := run([]string{"https://example.com/repo.git", t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when git credentials are missing, got none")
	}
	if !strings.Contains(err.Error(), "KONVEYOR_GIT_USERNAME") {
		t.Errorf("expected error to mention missing credentials, got: %v", err)
	}
}
