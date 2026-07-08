package main

import (
	"testing"

	"github.com/hhpatel14/migration-harness/internal/session"
)

func TestStatusForExitCode(t *testing.T) {
	cases := []struct {
		name string
		code int
		want string
	}{
		{name: "zero is succeeded", code: 0, want: "succeeded"},
		{name: "one is failed", code: 1, want: "failed"},
		{name: "negative one is failed", code: -1, want: "failed"},
		{name: "large positive code is failed", code: 137, want: "failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusForExitCode(tc.code)
			if got != tc.want {
				t.Errorf("statusForExitCode(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

func TestBuildResults_WithGitInfoSupplied(t *testing.T) {
	got := buildResults(0, 0, "konveyor/migrate-app-123", 12, "abc1234")

	want := session.Results{
		Status:   "succeeded",
		ExitCode: 0,
		Git: session.GitInfo{
			TargetBranch:  "konveyor/migrate-app-123",
			Commits:       12,
			LastCommitSHA: "abc1234",
		},
	}

	if got != want {
		t.Errorf("buildResults() = %+v, want %+v", got, want)
	}
}

func TestBuildResults_WithoutGitInfoDefaultsToZeroValue(t *testing.T) {
	// Mirrors invoking the binary with just --exit-code, as before these
	// flags existed: Git should remain entirely zero-valued.
	got := buildResults(1, 0, "", 0, "")

	want := session.Results{
		Status:   "failed",
		ExitCode: 1,
		Git:      session.GitInfo{},
	}

	if got != want {
		t.Errorf("buildResults() = %+v, want %+v", got, want)
	}

	if got.Git.TargetBranch != "" || got.Git.Commits != 0 || got.Git.LastCommitSHA != "" {
		t.Errorf("expected zero-valued Git fields when not supplied, got %+v", got.Git)
	}
}

func TestBuildResults_DurationPopulated(t *testing.T) {
	got := buildResults(0, 2700, "", 0, "")

	want := session.Results{
		Status:          "succeeded",
		ExitCode:        0,
		DurationSeconds: 2700,
		Git:             session.GitInfo{},
	}

	if got != want {
		t.Errorf("buildResults() = %+v, want %+v", got, want)
	}
}
