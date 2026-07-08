package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/cliutil"
	"github.com/konveyor/migration-harness/internal/detect"
	"github.com/konveyor/migration-harness/internal/jsonfile"
)

func main() {
	if len(os.Args) != 2 {
		cliutil.Usage("usage: konveyor-detect <repo-path>")
	}
	if err := run(os.Args[1]); err != nil {
		cliutil.Fatal("konveyor-detect", err)
	}
}

func run(repoDir string) error {
	outDir := filepath.Join(repoDir, "graphify-out")
	cmd := exec.Command("graphify", repoDir, "--output", outDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run graphify: %w", err)
	}

	graphJSON, err := os.ReadFile(filepath.Join(outDir, "graph.json"))
	if err != nil {
		return fmt.Errorf("read graph.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "graph.json"), graphJSON, 0644); err != nil {
		return fmt.Errorf("copy graph.json to repo root: %w", err)
	}

	summary, err := detect.Summarize(repoDir, graphJSON)
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	if err := jsonfile.Write(filepath.Join(repoDir, "detect.json"), summary); err != nil {
		return fmt.Errorf("write detect.json: %w", err)
	}
	return nil
}
