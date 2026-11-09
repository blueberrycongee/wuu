package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
)

func TestCompositionResolveCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "composition.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"profile":"headless","patches":[{"modules":{"example.module":true}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runComposition([]string{"resolve", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var resolved composition.Resolved
	if err := json.Unmarshal(output.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.Includes("example.module") || resolved.Includes("wuu.default-shell") {
		t.Fatalf("wrong resolution: %s", output.String())
	}
	output.Reset()
	if err := runComposition([]string{"resolve", "--profile", "missing"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid profile produced success output")
	}
}
