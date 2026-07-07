package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Phase describes one entry in phases.json — a named unit of LLM work
// that the orchestrator skill loads and runs.
type Phase struct {
	Name            string   `json:"name"`
	Skill           string   `json:"skill"`
	ExpectedOutputs []string `json:"expected_outputs"`
	Description     string   `json:"description,omitempty"`
}

// DefaultPipeline is the fixed POC pipeline: plan, execute, verify-fix.
// verify-fix is one phase (not two) — the verify skill owns an internal
// build/fix/re-verify loop, max 3 iterations.
func DefaultPipeline() []Phase {
	return []Phase{
		{Name: "plan", Skill: "plan/SKILL.md", ExpectedOutputs: []string{"PLAN.md"}},
		{Name: "execute", Skill: "execute/SKILL.md", ExpectedOutputs: []string{"execution-log.md"}},
		{
			Name:            "verify-fix",
			Skill:           "verify/SKILL.md",
			ExpectedOutputs: []string{"verify-report.md"},
			Description:     "Verify build, then fix errors iteratively (max 3 iterations, internal to this skill).",
		},
	}
}

// WriteJSON writes the phase list to path as JSON, for the orchestrator
// skill to read.
func WriteJSON(path string, phases []Phase) error {
	data, err := json.MarshalIndent(phases, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal phases: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// CheckCompletion reports which phases completed (all ExpectedOutputs
// exist under repoDir) and which didn't. This is how the harness builds
// session.json's steps_completed/steps_failed — the ACP stream's terminal
// event only reports overall session status, not per-phase status.
func CheckCompletion(repoDir string, phases []Phase) (completed, incomplete []string) {
	for _, p := range phases {
		allExist := true
		for _, out := range p.ExpectedOutputs {
			if _, err := os.Stat(filepath.Join(repoDir, out)); err != nil {
				allExist = false
				break
			}
		}
		if allExist {
			completed = append(completed, p.Name)
		} else {
			incomplete = append(incomplete, p.Name)
		}
	}
	return completed, incomplete
}
