package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParamsFromEnv_ReadsAllFields(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")
	t.Setenv("KONVEYOR_INSTRUCTIONS", "Migrate to Quarkus")
	t.Setenv("KONVEYOR_SKILLS_DIR", "/custom/skills")

	p, err := LoadParamsFromEnv()
	if err != nil {
		t.Fatalf("LoadParamsFromEnv failed: %v", err)
	}
	if p.SourceURL != "https://example.com/repo.git" {
		t.Errorf("unexpected SourceURL: %q", p.SourceURL)
	}
	if p.TargetBranch != "konveyor/migrate-1" {
		t.Errorf("unexpected TargetBranch: %q", p.TargetBranch)
	}
	if p.Instructions != "Migrate to Quarkus" {
		t.Errorf("unexpected Instructions: %q", p.Instructions)
	}
	if p.SkillsDir != "/custom/skills" {
		t.Errorf("unexpected SkillsDir: %q", p.SkillsDir)
	}
}

func TestLoadParamsFromEnv_DefaultsSkillsDir(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")
	t.Setenv("KONVEYOR_SKILLS_DIR", "")

	p, err := LoadParamsFromEnv()
	if err != nil {
		t.Fatalf("LoadParamsFromEnv failed: %v", err)
	}
	if p.SkillsDir != "/opt/skills" {
		t.Errorf("expected default /opt/skills, got %q", p.SkillsDir)
	}
}

func TestLoadParamsFromEnv_ErrorsWhenSourceURLMissing(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "konveyor/migrate-1")

	_, err := LoadParamsFromEnv()
	if err == nil {
		t.Fatal("expected an error when KONVEYOR_PARAM_SOURCE_URL is missing")
	}
}

func TestLoadParamsFromEnv_ErrorsWhenTargetBranchMissing(t *testing.T) {
	t.Setenv("KONVEYOR_PARAM_SOURCE_URL", "https://example.com/repo.git")
	t.Setenv("KONVEYOR_PARAM_TARGET_BRANCH", "")

	_, err := LoadParamsFromEnv()
	if err == nil {
		t.Fatal("expected an error when KONVEYOR_PARAM_TARGET_BRANCH is missing")
	}
}

func TestGenerateSecretKey_ProducesNonEmptyHexString(t *testing.T) {
	key, err := GenerateSecretKey()
	if err != nil {
		t.Fatalf("GenerateSecretKey failed: %v", err)
	}
	if len(key) != 64 { // 32 bytes hex-encoded = 64 chars
		t.Errorf("expected 64-char hex string, got length %d: %q", len(key), key)
	}
}

func TestGenerateSecretKey_ProducesDifferentKeysEachCall(t *testing.T) {
	key1, _ := GenerateSecretKey()
	key2, _ := GenerateSecretKey()
	if key1 == key2 {
		t.Error("expected two calls to produce different keys")
	}
}

func TestWriteGooseConfig_WritesProviderAndModel(t *testing.T) {
	t.Setenv("GOOSE_PROVIDER", "anthropic")
	t.Setenv("GOOSE_MODEL", "claude-sonnet-4-20250514")

	dir := t.TempDir()
	configDir := filepath.Join(dir, ".config", "goose")
	if err := WriteGooseConfig(configDir); err != nil {
		t.Fatalf("WriteGooseConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("expected config.yaml to exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "GOOSE_PROVIDER: anthropic") {
		t.Errorf("expected provider in config, got: %s", content)
	}
	if !strings.Contains(content, "GOOSE_MODEL: claude-sonnet-4-20250514") {
		t.Errorf("expected model in config, got: %s", content)
	}
}
