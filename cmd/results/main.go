package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/konveyor/migration-harness/internal/session"
)

func main() {
	exitCode := flag.Int("exit-code", 0, "exit code of the goose session")
	flag.Parse()

	res := session.Results{
		Status:   statusForExitCode(*exitCode),
		ExitCode: *exitCode,
	}
	if err := res.WriteTo("/.konveyor/results.json"); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-results: "+err.Error())
		os.Exit(1)
	}
}

// statusForExitCode maps a goose session's exit code to a results status.
func statusForExitCode(code int) string {
	if code != 0 {
		return "failed"
	}
	return "succeeded"
}
