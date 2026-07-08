package main

import (
	"os"
	"path/filepath"

	"github.com/hhpatel14/migration-harness/internal/cliutil"
	"github.com/hhpatel14/migration-harness/internal/config"
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
