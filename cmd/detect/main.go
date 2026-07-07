package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/konveyor/migration-harness/internal/detect"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: konveyor-detect <repo-path>")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "konveyor-detect: "+err.Error())
		os.Exit(1)
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

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal detect.json: %w", err)
	}
	return os.WriteFile(filepath.Join(repoDir, "detect.json"), data, 0644)
}
