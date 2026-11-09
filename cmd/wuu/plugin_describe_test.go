package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginTypesMatchEmbeddedLoaderAndDoNotOverwrite(t *testing.T) {
	target := filepath.Join(t.TempDir(), "sdk")
	if err := exportPluginTypes(target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "src", "index.ts"))
	if err != nil || len(data) == 0 {
		t.Fatalf("missing public types: %v", err)
	}
	if err := exportPluginTypes(target); err == nil {
		t.Fatal("overwrote existing SDK snapshot")
	}
}
