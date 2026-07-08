package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSession_WriteTo_ProducesExpectedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	s := Session{
		SessionID:       "sess-1",
		Status:          "complete",
		StartedAt:       time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		CompletedAt:     time.Date(2026, 7, 1, 10, 45, 0, 0, time.UTC),
		DurationSeconds: 2700,
		Runtime:         "goose",
		Models: []ModelUsage{
			{
				Role:     "primary",
				Provider: "anthropic",
				Name:     "claude-sonnet-4-20250514",
				TokenUsage: TokenUsage{
					InputTokens:  125000,
					OutputTokens: 45000,
				},
			},
		},
		StepsCompleted: []string{"detect", "plan", "execute", "verify-fix"},
		StepsFailed:    []string{},
		Git: GitInfo{
			TargetBranch:  "konveyor/migrate-app-123",
			Commits:       12,
			LastCommitSHA: "abc1234",
		},
	}

	if err := s.WriteTo(path); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected session.json to exist: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("session.json is not valid JSON: %v", err)
	}

	if raw["session_id"] != "sess-1" {
		t.Errorf("expected session_id sess-1, got %v", raw["session_id"])
	}
	models, ok := raw["models"].([]interface{})
	if !ok || len(models) != 1 {
		t.Fatalf("expected models to be a list of length 1, got %v", raw["models"])
	}
	firstModel := models[0].(map[string]interface{})
	if firstModel["role"] != "primary" {
		t.Errorf("expected model role 'primary', got %v", firstModel["role"])
	}
	if _, hasStage := raw["stage"]; hasStage {
		t.Error("session.json must NOT have a top-level 'stage' field (dropped per spec)")
	}

	gitField, ok := raw["git"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected git field to be an object, got %v", raw["git"])
	}
	if gitField["target_branch"] != "konveyor/migrate-app-123" {
		t.Errorf("expected target_branch field, got %v", gitField)
	}
	if gitField["commits"] != float64(12) {
		t.Errorf("expected commits field, got %v", gitField)
	}
	if gitField["last_commit_sha"] != "abc1234" {
		t.Errorf("expected last_commit_sha field, got %v", gitField)
	}
}

func TestResults_WriteTo_ProducesExpectedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")

	r := Results{
		Status:          "succeeded",
		ExitCode:        0,
		DurationSeconds: 2700,
		Git: GitInfo{
			TargetBranch:  "konveyor/migrate-app-123",
			Commits:       12,
			LastCommitSHA: "abc1234",
		},
	}

	if err := r.WriteTo(path); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected results.json to exist: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("results.json is not valid JSON: %v", err)
	}
	gitField, ok := raw["git"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected git field to be an object, got %v", raw["git"])
	}
	// results.json's git object must use the SAME field names as
	// session.json's (target_branch, commits) — this was a documented
	// regression fix in the spec review.
	if gitField["target_branch"] != "konveyor/migrate-app-123" {
		t.Errorf("expected target_branch field, got %v", gitField)
	}
	if gitField["commits"] != float64(12) {
		t.Errorf("expected commits field, got %v", gitField)
	}
}
