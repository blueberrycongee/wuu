//go:build codemode_integration

package codemode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// This contract test is intentionally separate from the protocol peer tests:
// a simulator cannot verify the JavaScript runtime or its wire serialization.
// CI/build packaging must supply the exact binary it intends to ship.
func TestHostIntegration(t *testing.T) {
	executable := os.Getenv("WUU_CODE_MODE_HOST")
	if executable == "" {
		t.Skip("WUU_CODE_MODE_HOST is not set; real JavaScript host is not tested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	client, err := Start(ctx, executable, "integration", CellLimits{}, testDelegate{
		invoke: func(ctx context.Context, invocation Invocation) (json.RawMessage, error) {
			if invocation.ToolName.Name != "echo" || string(invocation.Input) != `{"value":42}` {
				t.Errorf("unexpected nested invocation: %+v", invocation)
			}
			close(entered)
			select {
			case <-release:
				return json.RawMessage(`{"answer":42}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		notify: func(context.Context, string, string, string) error { return nil },
	}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Execute(ctx, ExecuteRequest{ToolCallID: "isolation", Source: `text([typeof process, typeof require, typeof fetch, typeof console].join(","));`})
	if err != nil || result.ErrorText != nil || len(result.Content) != 1 || result.Content[0].Text != "undefined,undefined,undefined,undefined" {
		t.Fatalf("unexpected runtime globals: %+v, %v", result, err)
	}
	yield := uint64(1)
	result, err = client.Execute(ctx, ExecuteRequest{ToolCallID: "nested", YieldTimeMS: &yield,
		EnabledTools: []ToolDefinition{{Name: "echo", ToolName: ToolName{Name: "echo"}, Kind: "function",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}`)}},
		Source: `const value = await tools.echo({value:42}); text(value);`,
	})
	if err != nil || result.State != "Yielded" {
		t.Fatalf("pending delegate did not yield: %+v, %v", result, err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("nested invocation never reached Wuu")
	}
	close(release)
	var output strings.Builder
	for result.State == "Yielded" {
		result, err = client.Wait(ctx, result.CellID, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Content {
			output.WriteString(item.Text)
		}
	}
	if result.State != "Result" || result.ErrorText != nil || !strings.Contains(output.String(), "42") {
		t.Fatalf("nested result lost: %+v, %q", result, output.String())
	}
	// Each execute gets a fresh isolate, including after a syntax/runtime error.
	result, err = client.Execute(ctx, ExecuteRequest{ToolCallID: "error", Source: `throw new Error("intentional");`})
	if err != nil || result.ErrorText == nil {
		t.Fatalf("runtime error was not represented: %+v, %v", result, err)
	}
	result, err = client.Execute(ctx, ExecuteRequest{ToolCallID: "next", Source: `text("still alive");`})
	if err != nil || result.ErrorText != nil || len(result.Content) != 1 || result.Content[0].Text != "still alive" {
		t.Fatalf("cell error poisoned session: %+v, %v", result, err)
	}
}
