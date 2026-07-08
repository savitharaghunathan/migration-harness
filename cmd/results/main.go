package main

import (
	"flag"

	"github.com/hhpatel14/migration-harness/internal/cliutil"
	"github.com/hhpatel14/migration-harness/internal/session"
)

func main() {
	exitCode := flag.Int("exit-code", 0, "exit code of the goose session")
	targetBranch := flag.String("target-branch", "", "git target branch (optional)")
	commits := flag.Int("commits", 0, "commit count (optional)")
	lastCommitSHA := flag.String("last-commit-sha", "", "last commit SHA (optional)")
	flag.Parse()

	res := buildResults(*exitCode, *targetBranch, *commits, *lastCommitSHA)
	if err := res.WriteTo("/.konveyor/results.json"); err != nil {
		cliutil.Fatal("konveyor-results", err)
	}
}

// buildResults constructs a session.Results from the parsed flag values.
// targetBranch, commits, and lastCommitSHA are optional: when the caller
// invokes konveyor-results standalone without git info, they default to
// their zero values and Results.Git is left zero-valued, matching the
// binary's original behavior.
func buildResults(exitCode int, targetBranch string, commits int, lastCommitSHA string) session.Results {
	return session.Results{
		Status:   statusForExitCode(exitCode),
		ExitCode: exitCode,
		Git: session.GitInfo{
			TargetBranch:  targetBranch,
			Commits:       commits,
			LastCommitSHA: lastCommitSHA,
		},
	}
}

// statusForExitCode maps a goose session's exit code to a results status.
func statusForExitCode(code int) string {
	if code != 0 {
		return "failed"
	}
	return "succeeded"
}
