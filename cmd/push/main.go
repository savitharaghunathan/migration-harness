package main

import (
	"os"

	"github.com/konveyor/migration-harness/internal/cliutil"
	"github.com/konveyor/migration-harness/internal/git"
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
	message, files := parseArgs(args)

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
