package main

import (
	"os"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/cliutil"
	"github.com/konveyor/migration-harness/internal/config"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		cliutil.Fatal("konveyor-configure", err)
	}
	if err := config.WriteGooseConfig(filepath.Join(homeDir, ".config", "goose")); err != nil {
		cliutil.Fatal("konveyor-configure", err)
	}
}
