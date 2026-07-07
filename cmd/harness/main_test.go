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
