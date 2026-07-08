package main

import (
	"strings"
	"testing"
)

func TestBuildPromptMessage_IncludesSkillsDirInstructionsAndPhasesPath(t *testing.T) {
	msg := buildPromptMessage("/opt/skills", "/workspace/instructions.md", "/workspace/repo/phases.json")

	if !strings.Contains(msg, "/opt/skills/orchestrator/SKILL.md") {
		t.Errorf("expected message to reference orchestrator skill path, got: %s", msg)
	}
	if !strings.Contains(msg, "/workspace/instructions.md") {
		t.Errorf("expected message to reference instructions path, got: %s", msg)
	}
	if !strings.Contains(msg, "/workspace/repo/phases.json") {
		t.Errorf("expected message to reference phases.json path, got: %s", msg)
	}
}

func TestFinalRunError_ReturnsNilWhenComplete(t *testing.T) {
	if err := finalRunError("complete", ""); err != nil {
		t.Fatalf("expected nil error for status=complete, got: %v", err)
	}
}

func TestFinalRunError_ReturnsErrorWhenNotComplete(t *testing.T) {
	err := finalRunError("failed", "")
	if err == nil {
		t.Fatal("expected non-nil error for status=failed, got nil")
	}
	if !strings.Contains(err.Error(), `status="failed"`) {
		t.Errorf("expected error to mention status, got: %v", err)
	}
}

func TestFinalRunError_IncludesDetailWhenProvided(t *testing.T) {
	err := finalRunError("failed", `stopReason="max_tokens"`)
	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), `stopReason="max_tokens"`) {
		t.Errorf("expected error to include failure detail, got: %v", err)
	}
}

func TestGooseEnv_FiltersCredentialsAndInjectsSecretKey(t *testing.T) {
	t.Setenv("KONVEYOR_GIT_USERNAME", "should-be-stripped")
	t.Setenv("KONVEYOR_GIT_TOKEN", "should-be-stripped")
	t.Setenv("GOOSE_PROVIDER", "anthropic")

	env := gooseEnv("test-secret-key")

	for _, kv := range env {
		if strings.HasPrefix(kv, "KONVEYOR_GIT_") {
			t.Errorf("credential var leaked into goose env: %s", kv)
		}
	}
	if !containsEnv(env, "GOOSE_SERVER__SECRET_KEY=test-secret-key") {
		t.Error("expected GOOSE_SERVER__SECRET_KEY to be set")
	}
	if !containsEnv(env, "GOOSE_PROVIDER=anthropic") {
		t.Error("expected unrelated env var to survive filtering")
	}
}

func containsEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
