package session

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ModelUsage struct {
	Role       string     `json:"role"`
	Provider   string     `json:"provider"`
	Name       string     `json:"name"`
	TokenUsage TokenUsage `json:"token_usage"`
}

// GitInfo is shared verbatim between session.json and results.json —
// keeping one shared type prevents the two files' git objects from
// drifting to different field names (a bug caught in spec review).
type GitInfo struct {
	TargetBranch  string `json:"target_branch"`
	Commits       int    `json:"commits"`
	LastCommitSHA string `json:"last_commit_sha"`
}

// Session is written by the harness to .konveyor/session.json and pushed
// to git. It has no "stage" field — no stage vocabulary is defined yet.
// Models is a list keyed by role (not a flat single model) so multi-model
// runs, where different pipeline phases use different models, can be
// represented with separate token usage per role.
type Session struct {
	SessionID       string       `json:"session_id"`
	Status          string       `json:"status"`
	StartedAt       time.Time    `json:"started_at"`
	CompletedAt     time.Time    `json:"completed_at"`
	DurationSeconds int          `json:"duration_seconds"`
	Runtime         string       `json:"runtime"`
	Models          []ModelUsage `json:"models"`
	StepsCompleted  []string     `json:"steps_completed"`
	StepsFailed     []string     `json:"steps_failed"`
	Git             GitInfo      `json:"git"`
}

func (s Session) WriteTo(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Results is written by the harness to the pod-local /.konveyor/results.json
// (NOT committed to git). Read by the controller after pod completion.
type Results struct {
	Status          string  `json:"status"`
	ExitCode        int     `json:"exit_code"`
	DurationSeconds int     `json:"duration_seconds"`
	Git             GitInfo `json:"git"`
}

func (r Results) WriteTo(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
