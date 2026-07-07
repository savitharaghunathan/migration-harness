package phases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultPipeline_HasThreePhasesInOrder(t *testing.T) {
	pipeline := DefaultPipeline()
	if len(pipeline) != 3 {
		t.Fatalf("expected 3 phases, got %d", len(pipeline))
	}
	wantNames := []string{"plan", "execute", "verify-fix"}
	for i, want := range wantNames {
		if pipeline[i].Name != want {
			t.Errorf("phase %d: expected name %q, got %q", i, want, pipeline[i].Name)
		}
	}
}

func TestWriteJSON_ProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "phases.json")
	pipeline := DefaultPipeline()

	if err := WriteJSON(path, pipeline); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected phases.json to exist: %v", err)
	}

	var roundTripped []Phase
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("phases.json is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, pipeline) {
		t.Errorf("round-tripped phases don't match: got %+v, want %+v", roundTripped, pipeline)
	}
}

func TestCheckCompletion_ReportsCompletedAndIncomplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte("plan"), 0644); err != nil {
		t.Fatal(err)
	}
	// execution-log.md and verify-report.md are NOT created — those phases
	// should be reported as incomplete.

	testPhases := []Phase{
		{Name: "plan", ExpectedOutputs: []string{"PLAN.md"}},
		{Name: "execute", ExpectedOutputs: []string{"execution-log.md"}},
		{Name: "verify-fix", ExpectedOutputs: []string{"verify-report.md"}},
	}

	completed, incomplete := CheckCompletion(dir, testPhases)

	if !reflect.DeepEqual(completed, []string{"plan"}) {
		t.Errorf("expected completed=[plan], got %v", completed)
	}
	if !reflect.DeepEqual(incomplete, []string{"execute", "verify-fix"}) {
		t.Errorf("expected incomplete=[execute verify-fix], got %v", incomplete)
	}
}

func TestCheckCompletion_RequiresAllExpectedOutputsForOnePhase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	// b.txt is missing — the phase requires both.

	testPhases := []Phase{
		{Name: "multi-output", ExpectedOutputs: []string{"a.txt", "b.txt"}},
	}

	completed, incomplete := CheckCompletion(dir, testPhases)
	if len(completed) != 0 {
		t.Errorf("expected no completed phases, got %v", completed)
	}
	if !reflect.DeepEqual(incomplete, []string{"multi-output"}) {
		t.Errorf("expected incomplete=[multi-output], got %v", incomplete)
	}
}
