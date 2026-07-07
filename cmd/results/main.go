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

	status := "succeeded"
	if *exitCode != 0 {
		status = "failed"
	}
	res := session.Results{
		Status:   status,
		ExitCode: *exitCode,
	}
	if err := res.WriteTo("/.konveyor/results.json"); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-results: "+err.Error())
		os.Exit(1)
	}
}
