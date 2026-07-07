package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Params holds the KONVEYOR_PARAM_* and related env vars the harness needs.
type Params struct {
	SourceURL    string
	TargetBranch string
	Instructions string
	SkillsDir    string
}

// LoadParamsFromEnv reads the harness's required configuration from the
// environment. Instructions comes from KONVEYOR_INSTRUCTIONS (not
// KONVEYOR_PARAM_INSTRUCTIONS) because PR #295 treats AgentRun's
// `instructions` field as distinct from declared `params`.
func LoadParamsFromEnv() (Params, error) {
	p := Params{
		SourceURL:    os.Getenv("KONVEYOR_PARAM_SOURCE_URL"),
		TargetBranch: os.Getenv("KONVEYOR_PARAM_TARGET_BRANCH"),
		Instructions: os.Getenv("KONVEYOR_INSTRUCTIONS"),
		SkillsDir:    os.Getenv("KONVEYOR_SKILLS_DIR"),
	}
	if p.SkillsDir == "" {
		p.SkillsDir = "/opt/skills"
	}
	if p.SourceURL == "" {
		return Params{}, fmt.Errorf("KONVEYOR_PARAM_SOURCE_URL is required")
	}
	if p.TargetBranch == "" {
		return Params{}, fmt.Errorf("KONVEYOR_PARAM_TARGET_BRANCH is required")
	}
	return p, nil
}

// GenerateSecretKey produces a random hex string for GOOSE_SERVER__SECRET_KEY.
// Generated fresh per run — local to this pod/process, never shared with
// the controller or UI (see spec's "Controller/UI auth to /acp" open item).
func GenerateSecretKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteGooseConfig writes goose's config.yaml from GOOSE_PROVIDER/GOOSE_MODEL
// env vars (passed through via envFrom from the LLMProvider Secret).
func WriteGooseConfig(configDir string) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	contents := fmt.Sprintf("GOOSE_PROVIDER: %s\nGOOSE_MODEL: %s\n",
		os.Getenv("GOOSE_PROVIDER"), os.Getenv("GOOSE_MODEL"))
	return os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(contents), 0644)
}
