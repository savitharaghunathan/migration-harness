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
