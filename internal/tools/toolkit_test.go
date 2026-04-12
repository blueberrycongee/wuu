package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestToolkit_ReadFileSizeSemantics(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	big := strings.Repeat("x", defaultMaxFileBytes+64)
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"big.txt","content":"` + big + `"}`,
	}); err != nil {
		t.Fatalf("write_file: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"big.txt"}`,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse read_file response: %v", err)
	}
	if parsed["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", parsed["truncated"])
	}
	size := int(parsed["size"].(float64))
	returned := int(parsed["returned_size"].(float64))
	if size != len(big) {
		t.Fatalf("expected size=%d, got %d", len(big), size)
	}
	if returned != defaultMaxFileBytes {
		t.Fatalf("expected returned_size=%d, got %d", defaultMaxFileBytes, returned)
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

func TestToolkit_GrepIncludeMatchesRelativePaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	if err != nil {
		t.Fatalf("New: %v", err)
	}

	files := map[string]string{
		"internal/a.go":   "package internal\nvar target = true\n",
		"internal/a.txt":  "target\n",
		"src/app/main.ts": "const target = true;\n",
		"src/app/util.js": "const target = true;\n",
		"pkg/nested/x.go": "package nested\nvar target = true\n",
		"main.go":         "package main\nvar target = true\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"internal/*.go"}`,
	})
	if err != nil {
		t.Fatalf("grep internal/*.go: %v", err)
	}
	var parsed struct {
		Matches []struct {
			File string `json:"file"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "internal/a.go" {
		t.Fatalf("unexpected matches for internal/*.go: %+v", parsed.Matches)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("grep src/**/*.ts: %v", err)
	}
	parsed.Matches = nil
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "src/app/main.ts" {
		t.Fatalf("unexpected matches for src/**/*.ts: %+v", parsed.Matches)
	}
}

func TestToolkit_GrepReturnsScannerErrors(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	if err != nil {
		t.Fatalf("New: %v", err)
	}

	path := filepath.Join(root, "huge.txt")
	tooLongLine := strings.Repeat("a", bufio.MaxScanTokenSize+1)
	if err := os.WriteFile(path, []byte(tooLongLine), 0o644); err != nil {
		t.Fatalf("write huge.txt: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"needle","path":"huge.txt"}`,
	})
	if err == nil {
		t.Fatal("expected scanner error")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected bufio.ErrTooLong, got: %v", err)
	}
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

func TestToolkit_SendMessageToAgent_RegisteredInDefinitions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defs := kit.Definitions()
	for _, d := range defs {
		if d.Name == "send_message_to_agent" {
			if strings.Contains(strings.ToLower(d.Description), "currently unavailable") {
				t.Fatalf("send_message_to_agent description should not say unavailable: %q", d.Description)
			}
			return
		}
	}
	t.Fatal("send_message_to_agent must be present in tool definitions")
}

func TestToolkit_ForkAgent_RegisteredInDefinitions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defs := kit.Definitions()
	found := false
	for _, d := range defs {
		if d.Name == "fork_agent" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fork_agent must be present in tool definitions")
	}
}

func TestToolkit_ForkAgent_FailsWithoutCoordinator(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Don't call SetCoordinator — simulates a worker toolkit.
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "fork_agent",
		Arguments: `{"description":"test","prompt":"do thing"}`,
	})
	if err == nil {
		t.Fatal("expected error when coordinator is not configured")
	}
	if !strings.Contains(err.Error(), "coordinator not configured") {
		t.Fatalf("expected coordinator-not-configured error, got: %v", err)
	}
}

func TestWrapForkPrompt_OverridesParentReadOnlyClaims(t *testing.T) {
	prompt := wrapForkPrompt("fix the bug")
	if !strings.Contains(prompt, "main interactive") || !strings.Contains(prompt, "read-only") {
		t.Fatalf("fork override must cancel inherited main-agent read-only guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "If a tool is in") {
		t.Fatalf("fork override must restore worker authority to use its tools: %q", prompt)
	}
}

func TestStripDanglingToolUses(t *testing.T) {
	// Last message is an assistant turn with tool_calls — should be stripped.
	with := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "ok", ToolCalls: []providers.ToolCall{{Name: "fork_agent"}}},
	}
	got := stripDanglingToolUses(with)
	if len(got) != 2 {
		t.Fatalf("expected last assistant w/ tool_calls stripped, got %d msgs", len(got))
	}
	if got[len(got)-1].Role != "user" {
		t.Fatalf("expected last remaining message to be user, got %s", got[len(got)-1].Role)
	}

	// Last message is a tool result — should NOT be stripped (the
	// previous tool_use already has its matching result).
	clean := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "ok", ToolCalls: []providers.ToolCall{{Name: "read_file"}}},
		{Role: "tool", Name: "read_file", Content: "result"},
	}
	got = stripDanglingToolUses(clean)
	if len(got) != 4 {
		t.Fatalf("clean history should pass through unchanged, got %d msgs", len(got))
	}

	// Last message is an assistant turn WITHOUT tool_calls — should
	// not be stripped (it's a normal text reply).
	textOnly := []providers.ChatMessage{
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "ok"},
	}
	got = stripDanglingToolUses(textOnly)
	if len(got) != 2 {
		t.Fatalf("text-only assistant should not be stripped, got %d msgs", len(got))
	}

	// Empty history — should pass through.
	if got := stripDanglingToolUses(nil); got != nil {
		t.Fatal("nil history should pass through unchanged")
	}
}

func TestToolkit_DisableTools_HidesDefinitionsAndBlocksExecute(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.DisableTools("write_file", "edit_file", "run_shell")

	defs := kit.Definitions()
	for _, d := range defs {
		if d.Name == "write_file" || d.Name == "edit_file" || d.Name == "run_shell" {
			t.Fatalf("disabled tool %q should not appear in definitions", d.Name)
		}
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"x"}`,
	})
	if err == nil {
		t.Fatal("expected disabled write_file to error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"echo hi"}`,
	})
	if err == nil {
		t.Fatal("expected disabled run_shell to error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
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

type execCommandFunc func(context.Context, string, ...string) *exec.Cmd

func withRGTestHooks(t *testing.T, lookup func(string) (string, error), cmd execCommandFunc) {
	t.Helper()
	origLookup := rgLookupPath
	origCmd := rgCommand
	rgLookupPath = lookup
	if cmd != nil {
		rgCommand = cmd
	}
	resetRGForTests()
	t.Cleanup(func() {
		rgLookupPath = origLookup
		rgCommand = origCmd
		resetRGForTests()
	})
}

func TestToolkit_GlobRipgrepIncludesHiddenFiles(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for path, content := range map[string]string{
		".env":          "TOKEN=abc\n",
		"visible.env":   "TOKEN=visible\n",
		"dir/.env.test": "TOKEN=nested\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.env*"}`,
	})
	if err != nil {
		t.Fatalf("glob *.env*: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if !reflect.DeepEqual(parsed.Files, []string{".env", "dir/.env.test", "visible.env"}) {
		t.Fatalf("unexpected hidden glob matches: %+v", parsed.Files)
	}
}

func TestToolkit_GrepRipgrepIncludesHiddenFiles(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for path, content := range map[string]string{
		".env":        "API_KEY=secret\n",
		"visible.env": "API_KEY=visible\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"API_KEY","include":"*.env"}`,
	})
	if err != nil {
		t.Fatalf("grep *.env: %v", err)
	}
	var parsed struct {
		Matches []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	want := []grepMatch{{File: ".env", Line: 1, Content: "API_KEY=secret"}, {File: "visible.env", Line: 1, Content: "API_KEY=visible"}}
	if !reflect.DeepEqual(parsed.Matches, want) {
		t.Fatalf("unexpected hidden grep matches: got %+v want %+v", parsed.Matches, want)
	}
}

func TestToolkit_GlobRipgrepFirst(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	files := map[string]string{
		"src/app/main.ts": "export const main = true\n",
		"src/lib/util.ts": "export const util = true\n",
		"src/lib/util.js": "export const util = true\n",
		"README.md":       "# readme\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.md"}`,
	})
	if err != nil {
		t.Fatalf("glob *.md: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != "README.md" {
		t.Fatalf("unexpected matches for *.md: %+v", parsed.Files)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("glob src/**/*.ts: %v", err)
	}
	parsed.Files = nil
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	want := []string{"src/app/main.ts", "src/lib/util.ts"}
	if !reflect.DeepEqual(parsed.Files, want) {
		t.Fatalf("unexpected matches for src/**/*.ts: got %+v want %+v", parsed.Files, want)
	}
}

func TestToolkit_GlobFallbackWithoutRG(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	for path, content := range map[string]string{
		"src/app/main.ts": "main\n",
		"src/app/main.js": "main\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("glob fallback: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if !reflect.DeepEqual(parsed.Files, []string{"src/app/main.ts"}) {
		t.Fatalf("unexpected fallback matches: %+v", parsed.Files)
	}
}

func TestToolkit_GrepIncludeMatchesRelativePaths_Ripgrep(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	files := map[string]string{
		"internal/a.go":   "package internal\nvar target = true\n",
		"internal/a.txt":  "target\n",
		"src/app/main.ts": "const target = true;\n",
		"src/app/util.js": "const target = true;\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"internal/*.go"}`,
	})
	if err != nil {
		t.Fatalf("grep internal/*.go: %v", err)
	}
	var parsed struct {
		Matches []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "internal/a.go" {
		t.Fatalf("unexpected matches for internal/*.go: %+v", parsed.Matches)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("grep src/**/*.ts: %v", err)
	}
	parsed.Matches = nil
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "src/app/main.ts" {
		t.Fatalf("unexpected matches for src/**/*.ts: %+v", parsed.Matches)
	}
}
