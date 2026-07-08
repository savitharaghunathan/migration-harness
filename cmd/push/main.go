package main

import (
	"flag"
	"io"
	"os"

	"github.com/hhpatel14/migration-harness/internal/cliutil"
	"github.com/hhpatel14/migration-harness/internal/git"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		cliutil.Fatal("konveyor-push", err)
	}
}

// run performs the push: it parses the commit message/files from args,
// reads git credentials from the environment, and pushes from the
// current working directory. Kept separate from main so it can be
// exercised directly by tests without going through os.Exit.
func run(args []string) error {
	message, files, err := parseArgs(args)
	if err != nil {
		return err
	}

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		return err
	}
	repoDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := git.Push(repoDir, files, message, creds, git.DefaultAskpassPath); err != nil {
		return err
	}
	return nil
}

// parseArgs parses konveyor-push's arguments using the standard flag
// package, matching the convention established by konveyor-results.
// A dedicated FlagSet (rather than the global flag.CommandLine) is used
// since this function takes an explicit args slice instead of reading
// os.Args, and flag.ContinueOnError lets callers handle parse errors
// instead of the flag package printing usage and calling os.Exit.
func parseArgs(args []string) (message string, files []string, err error) {
	fs := flag.NewFlagSet("konveyor-push", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // run() reports errors itself; suppress flag's own output
	msg := fs.String("message", "konveyor: update", "commit message")
	if err := fs.Parse(args); err != nil {
		return "", nil, err
	}
	return *msg, fs.Args(), nil
}
