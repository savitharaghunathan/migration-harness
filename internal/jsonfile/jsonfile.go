// Package jsonfile provides a shared helper for writing indented JSON to
// disk, used by every package in this project that persists a struct as
// a JSON artifact (session.json, results.json, phases.json, detect.json).
package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
)

// Write marshals v as indented JSON and writes it to path.
func Write(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return os.WriteFile(path, data, 0644)
}
