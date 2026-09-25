package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// executeEnvelope runs a tool and returns its producer payload (the JSON
// envelope clients and durable records consume), not the settled model view.
func executeEnvelope(kit *Toolkit, ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := kit.ExecuteResult(ctx, call)
	return producerText(result), err
}

func lineCount(s string) int {
	if strings.TrimRight(s, "\n") == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

// startBackgroundForTest starts a command through bash run_in_background and
// returns the envelope of the start.
func startBackgroundForTest(t *testing.T, kit *Toolkit, args map[string]any) proc.Process {
	t.Helper()
	args["run_in_background"] = true
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{Name: "bash", Arguments: string(raw)})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started proc.Process
	if err := json.Unmarshal([]byte(resp), &started); err != nil || started.ID == "" {
		t.Fatalf("parse start background: %v\n%s", err, resp)
	}
	return started
}

// waitProcessOutputForTest reads a process log from the start until it
// contains want.
func waitProcessOutputForTest(t *testing.T, kit *Toolkit, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{
			Name:      "process",
			Arguments: `{"action":"read","process_id":"` + id + `","offset_bytes":0,"wait_ms":500}`,
		})
		if err != nil {
			t.Fatalf("read background: %v", err)
		}
		var read struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal([]byte(resp), &read); err != nil {
			t.Fatalf("parse read: %v\n%s", err, resp)
		}
		if strings.Contains(read.Output, want) {
			return read.Output
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %s output never contained %q: %q", id, want, read.Output)
		}
	}
}
