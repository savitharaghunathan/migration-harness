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

func TestFilteredEnviron_RemovesGitCredentialsOnly(t *testing.T) {
	t.Setenv("KONVEYOR_GIT_USERNAME", "some-user")
	t.Setenv("KONVEYOR_GIT_TOKEN", "super-secret-token")
	t.Setenv("GOOSE_PROVIDER", "anthropic")

	env := filteredEnviron()

	for _, kv := range env {
		if strings.HasPrefix(kv, "KONVEYOR_GIT_") {
			t.Errorf("expected filtered environment to omit KONVEYOR_GIT_* vars, found: %s", kv)
		}
	}

	found := false
	for _, kv := range env {
		if kv == "GOOSE_PROVIDER=anthropic" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected filtered environment to retain unrelated vars, GOOSE_PROVIDER=anthropic not found in: %v", env)
	}
}
