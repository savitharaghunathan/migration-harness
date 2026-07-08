package main

import (
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/cliutil"
	"github.com/konveyor/migration-harness/internal/git"
)

func main() {
	if len(os.Args) != 3 {
		cliutil.Usage("usage: konveyor-clone <url> <dest>")
	}
	if err := run(os.Args[1:]); err != nil {
		cliutil.Fatal("konveyor-clone", err)
	}
}

// run performs the clone: it validates args, reads git credentials from
// the environment, clones url into dest, and (if
// KONVEYOR_PARAM_TARGET_BRANCH is set) checks out or creates that
// branch. Kept separate from main so it can be exercised directly by
// tests without going through os.Exit.
func run(args []string) error {
	url, dest, err := parseArgs(args)
	if err != nil {
		return err
	}

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		return err
	}
	if err := git.Clone(url, dest, creds, git.DefaultAskpassPath); err != nil {
		return err
	}

	if branch := os.Getenv("KONVEYOR_PARAM_TARGET_BRANCH"); branch != "" {
		if err := git.CheckoutOrCreateBranch(dest, branch); err != nil {
			return err
		}
	}
	return nil
}

// parseArgs validates and extracts the positional url/dest arguments.
// It expects exactly two positional args: url, dest.
func parseArgs(args []string) (url, dest string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("expected exactly 2 args (url, dest), got %d", len(args))
	}
	return args[0], args[1], nil
}
