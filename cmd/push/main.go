package main

import (
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/git"
)

func main() {
	message, files := parseArgs(os.Args[1:])

	creds, err := git.CredentialsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
	repoDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
	if err := git.Push(repoDir, files, message, creds, git.DefaultAskpassPath); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-push: "+err.Error())
		os.Exit(1)
	}
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
