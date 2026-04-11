package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkit_WriteAndReadFile(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	writeResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"dir/a.txt","content":"hello"}`,
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(writeResp, "written_bytes") {
		t.Fatalf("unexpected write response: %s", writeResp)
	}

	readResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"dir/a.txt"}`,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(readResp, "hello") {
		t.Fatalf("unexpected read response: %s", readResp)
	}
}

func TestToolkit_PathEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"../secret.txt"}`,
	})
	if err == nil {
		t.Fatal("expected path escape error")
	}
}

// fakeAskBridge is a stub AskUserBridge for tests — it returns a
// canned response for any request without involving a TUI.
type fakeAskBridge struct {
	resp tools_internal_response // sentinel; set by helper below
}

type tools_internal_response struct {
	answers map[string]string
}

func (f *fakeAskBridge) AskUser(_ context.Context, req AskUserRequest) (AskUserResponse, error) {
	answers := map[string]string{}
	for _, q := range req.Questions {
		// Always pick the first option in tests.
		if len(q.Options) > 0 {
			answers[q.Question] = q.Options[0].Label
		}
	}
	return AskUserResponse{Answers: answers}, nil
}

func TestToolkit_AskUser_RegisteredInDefinitions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defs := kit.Definitions()
	found := false
	for _, d := range defs {
		if d.Name == "ask_user" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ask_user must be present in tool definitions")
	}
}

func TestToolkit_AskUser_FailsWithoutBridge(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Don't call SetAskUserBridge — simulates a worker toolkit.
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "ask_user",
		Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}]}`,
	})
	if err == nil {
		t.Fatal("expected error when bridge is not configured (worker isolation)")
	}
	if !strings.Contains(err.Error(), "main agent") {
		t.Fatalf("expected isolation message, got: %v", err)
	}
}

func TestToolkit_AskUser_RoutesThroughBridge(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetAskUserBridge(&fakeAskBridge{})

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "ask_user",
		Arguments: `{"questions":[{"question":"Which auth?","header":"Auth","options":[{"label":"OAuth","description":"delegate"},{"label":"JWT","description":"self-signed"}]}]}`,
	})
	if err != nil {
		t.Fatalf("ask_user: %v", err)
	}
	var parsed AskUserResponse
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Answers["Which auth?"] != "OAuth" {
		t.Fatalf("expected first option, got %v", parsed.Answers)
	}
}

func TestToolkit_AskUser_ValidatesInput(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetAskUserBridge(&fakeAskBridge{})

	// Header too long should error before reaching the bridge.
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "ask_user",
		Arguments: `{"questions":[{"question":"Q?","header":"this header is way too long","options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}]}`,
	})
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("expected header validation error, got: %v", err)
	}
}

func TestToolkit_RunShell(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"echo hi"}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if parsed["exit_code"].(float64) != 0 {
		t.Fatalf("unexpected exit code: %v", parsed["exit_code"])
	}
	if !strings.Contains(parsed["output"].(string), "hi") {
		t.Fatalf("unexpected output: %v", parsed["output"])
	}
}
