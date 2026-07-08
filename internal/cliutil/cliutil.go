// Package cliutil provides small helpers shared by every cmd/ binary's
// main(), keeping error-reporting and exit-code conventions consistent
// across the harness's binaries.
package cliutil

import (
	"fmt"
	"os"
)

// Fatal prints "<prog>: <err>" to stderr and exits with status 1. Every
// cmd/*/main.go uses this for the "an operation failed" case, so the
// exact prefix/format stays identical across binaries.
func Fatal(prog string, err error) {
	fmt.Fprintln(os.Stderr, prog+": "+err.Error())
	os.Exit(1)
}

// Usage prints a usage message verbatim to stderr and exits with status
// 1. Callers pass the full message (e.g. "usage: konveyor-clone <url>
// <dest>"), since the wording varies per binary.
func Usage(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
