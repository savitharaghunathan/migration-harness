package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/config"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-configure: "+err.Error())
		os.Exit(1)
	}
	if err := config.WriteGooseConfig(filepath.Join(homeDir, ".config", "goose")); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-configure: "+err.Error())
		os.Exit(1)
	}
}
