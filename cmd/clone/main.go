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
