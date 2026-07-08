package main

import (
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/git"
)

const askpassPath = "/usr/local/bin/git-askpass.sh"

func main() {
	url, dest, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: konveyor-clone <url> <dest>")
		os.Exit(1)
	}

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-clone: "+err.Error())
		os.Exit(1)
	}
	if err := git.Clone(url, dest, creds, askpassPath); err != nil {
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

// parseArgs validates and extracts the positional url/dest arguments.
// It expects exactly two positional args: url, dest.
func parseArgs(args []string) (url, dest string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("expected exactly 2 args (url, dest), got %d", len(args))
	}
	return args[0], args[1], nil
}
