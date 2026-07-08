package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectManifests_FindsPomXML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0644); err != nil {
		t.Fatal(err)
	}

	m := DetectManifests(dir)
	if !m.PomXML {
		t.Error("expected PomXML to be true")
	}
	if m.PackageJSON {
		t.Error("expected PackageJSON to be false")
	}
}

func TestSummarize_CountsFilesAndGraphStats(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0644); err != nil {
		t.Fatal(err)
	}

	graphJSON := []byte(`{
		"nodes": [
			{"source_file": "src/Foo.java", "degree": 5},
			{"source_file": "src/Bar.java", "degree": 25},
			{"source_file": "src/main.py", "degree": 1}
		],
		"links": [{}, {}],
		"communities": [{"id": 0}, {"id": 0}, {"id": 1}]
	}`)

	summary, err := Summarize(dir, graphJSON)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if !summary.Manifests.PomXML {
		t.Error("expected PomXML to be true")
	}
	if summary.Files.Java != 2 {
		t.Errorf("expected 2 java files, got %d", summary.Files.Java)
	}
	if summary.Files.Python != 1 {
		t.Errorf("expected 1 python file, got %d", summary.Files.Python)
	}
	if summary.Graph.Nodes != 3 {
		t.Errorf("expected 3 nodes, got %d", summary.Graph.Nodes)
	}
	if summary.Graph.Edges != 2 {
		t.Errorf("expected 2 edges, got %d", summary.Graph.Edges)
	}
	if summary.Graph.Communities != 2 {
		t.Errorf("expected 2 communities, got %d", summary.Graph.Communities)
	}
	if summary.Graph.GodNodes != 1 {
		t.Errorf("expected 1 god node (degree>20), got %d", summary.Graph.GodNodes)
	}
	if summary.GraphFile != "graph.json" {
		t.Errorf("expected GraphFile to be graph.json, got %q", summary.GraphFile)
	}
}

func TestSummarize_GodNodeThresholdIsStrictlyGreaterThan20(t *testing.T) {
	dir := t.TempDir()

	graphJSON := []byte(`{
		"nodes": [
			{"source_file": "src/Boundary.java", "degree": 20}
		],
		"links": [],
		"communities": []
	}`)

	summary, err := Summarize(dir, graphJSON)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if summary.Graph.GodNodes != 0 {
		t.Errorf("expected 0 god nodes for a node at exactly degree=20 (threshold is strictly > 20), got %d", summary.Graph.GodNodes)
	}
}

func TestSummarize_ErrorsOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	_, err := Summarize(dir, []byte("not json"))
	if err == nil {
		t.Fatal("expected an error for invalid graph.json")
	}
}
