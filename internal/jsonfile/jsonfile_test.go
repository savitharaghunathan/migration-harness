package jsonfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestWrite_RoundTripsSimpleStruct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	in := sample{Name: "widget", Count: 3}

	if err := Write(path, in); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}

	var out sample
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if out != in {
		t.Errorf("round-tripped value = %+v, want %+v", out, in)
	}
}

func TestWrite_ReturnsErrorOnUnmarshalableValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	// A channel cannot be marshaled to JSON, so this should return an
	// error rather than panicking.
	bad := struct {
		Ch chan int
	}{Ch: make(chan int)}

	err := Write(path, bad)
	if err == nil {
		t.Fatal("expected Write to return an error for an unmarshalable value")
	}

	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("expected file to not be created when marshal fails")
	}
}
