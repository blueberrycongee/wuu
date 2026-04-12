package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunVersionAliasForwardsJSONFlag(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run([]string{"--version", "--json"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", output, err)
	}
	if _, ok := payload["version"]; !ok {
		t.Fatalf("expected version field in JSON output: %v", payload)
	}
}

func TestRunVersionAliasForwardsLongFlag(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run([]string{"-v", "--long"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "version:") {
		t.Fatalf("expected long version output, got %q", output)
	}
	if !strings.Contains(output, "commit:") {
		t.Fatalf("expected long version output to include commit, got %q", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer r.Close()

	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	return strings.TrimSpace(buf.String())
}
