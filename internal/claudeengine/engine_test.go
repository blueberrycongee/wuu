package claudeengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func buildFakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fakeclaude")
	cmd := exec.Command("go", "build", "-o", binary, "./testdata/fakeclaude")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake claude: %v\n%s", err, out)
	}
	return binary
}

func TestEngineEndToEndFakeClaude(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.jsonl")
	t.Setenv("WUU_TEST_CLAUDE_ARGS", argsPath)
	binary := buildFakeClaude(t)
	engine := NewEngine(binary, t.TempDir())

	desc, err := engine.Descriptor(context.Background())
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if desc.ID != agentengine.EngineID("claude") {
		t.Fatalf("descriptor id = %q, want claude", desc.ID)
	}
	if !strings.Contains(desc.Version, "2.1.226") {
		t.Fatalf("descriptor version = %q, want fake 2.1.226", desc.Version)
	}

	persisted := ""
	sess, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
		ThreadID: "wuu-thread-1",
		Model:    "opus", Effort: "high", Speed: "fast",
		RootDir: t.TempDir(),
		PersistRef: func(ref string) error {
			persisted = ref
			return nil
		},
	})
	if err != nil {
		t.Fatalf("SessionForThread: %v", err)
	}

	var content strings.Builder
	var thinking strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := sess.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(ev providers.StreamEvent) {
		switch ev.Type {
		case providers.EventContentDelta:
			content.WriteString(ev.Content)
		case providers.EventThinkingDelta:
			thinking.WriteString(ev.Content)
		}
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if persisted != "fake-session-1" {
		t.Fatalf("persisted ref = %q, want fake-session-1", persisted)
	}
	if got := content.String(); got != "Hello from claude." {
		t.Fatalf("content = %q, want %q", got, "Hello from claude.")
	}
	if !strings.Contains(thinking.String(), "let me think") {
		t.Fatalf("thinking = %q, want it to contain thinking delta", thinking.String())
	}
	if result.Result.InputTokens != 80 || result.Result.OutputTokens != 12 || result.Result.CacheReadTokens != 30 {
		t.Fatalf("usage = in %d out %d cache %d, want 80/12/30",
			result.Result.InputTokens, result.Result.OutputTokens, result.Result.CacheReadTokens)
	}
	if len(result.Result.NewMessages) != 1 || result.Result.NewMessages[0].Role != "assistant" {
		t.Fatalf("NewMessages = %+v, want one assistant message", result.Result.NewMessages)
	}

	// Second turn on the same session resumes with the persisted id.
	sess2, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
		ThreadID: "wuu-thread-1",
		Model:    "opus", Effort: "high", Speed: "standard",
		RootDir:     t.TempDir(),
		ExternalRef: "fake-session-1",
		PersistRef:  func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("second SessionForThread: %v", err)
	}
	if _, err := sess2.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "again"}},
	}, nil); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	// Resume through the factory seam.
	resumed, err := engine.Resume(context.Background(), agentengine.ResumeRequest{
		OpenRequest:        agentengine.OpenRequest{ThreadID: "wuu-thread-2", RootDir: t.TempDir()},
		ExternalSessionRef: "fake-session-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := resumed.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "resume"}},
	}, nil); err != nil {
		t.Fatalf("resumed RunTurn: %v", err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("native launches = %d, want 3", len(lines))
	}
	for i, line := range lines {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		flags := map[string]string{}
		for index := 0; index+1 < len(args); index++ {
			flags[args[index]] = args[index+1]
		}
		if i == 2 {
			if _, ok := flags["--settings"]; ok {
				t.Fatal("inherited speed sent an override")
			}
			continue
		}
		var settings map[string]bool
		if err := json.Unmarshal([]byte(flags["--settings"]), &settings); err != nil {
			t.Fatal(err)
		}
		enabled, present := settings["fastMode"]
		if !present || enabled != (i == 0) || flags["--model"] != "opus" || flags["--effort"] != "high" {
			t.Fatalf("launch %d changed the native selection: %v", i, args)
		}
	}

}

func TestEngineMissingBinaryFailsClearly(t *testing.T) {
	engine := NewEngine(filepath.Join(t.TempDir(), "no-such-claude"), t.TempDir())
	// Open is lazy (the child spawns on the first turn), so the missing
	// binary surfaces as a clear RunTurn error.
	sess, err := engine.Open(context.Background(), agentengine.OpenRequest{ThreadID: "t", RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open must not fail before the first turn: %v", err)
	}
	_, err = sess.RunTurn(context.Background(), agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	}, nil)
	if err == nil {
		t.Fatal("RunTurn without a claude binary must fail")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should mention claude, got: %v", err)
	}
}

func TestResolveBinaryFindsUserInstallOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	t.Setenv("WUU_CLAUDE_BINARY", "")
	t.Setenv("PATH", t.TempDir())
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(home, ".local", "bin", name)
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		content = []byte("placeholder")
	}
	if err := os.WriteFile(binary, content, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("ResolveBinary = %q, want %q", got, binary)
	}
}

func TestResolveBinaryEnvOverride(t *testing.T) {
	t.Setenv("WUU_CLAUDE_BINARY", "/nonexistent/claude")
	path, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary with env override: %v", err)
	}
	if path != "/nonexistent/claude" {
		t.Fatalf("ResolveBinary = %q, want env value", path)
	}
}

func TestTurnSubscriptionReconcilesStreamAndResult(t *testing.T) {
	var events []providers.StreamEvent
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(func(event providers.StreamEvent) {
		events = append(events, event)
	}, done)

	sub.handleLine(`{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude-opus"}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"abc"}}}`)
	sub.handleLine(`not-json`)
	sub.handleLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"abc"}]}}`)
	sub.handleLine(`{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","result":"abcdef"}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("turn error: %v", out.err)
	}
	if got := out.result.Result.Content; got != "abcdef" {
		t.Fatalf("result content = %q, want abcdef", got)
	}
	var streamed strings.Builder
	for _, event := range events {
		if event.Type == providers.EventContentDelta {
			streamed.WriteString(event.Content)
		}
	}
	if got := streamed.String(); got != "abcdef" {
		t.Fatalf("streamed content = %q, want abcdef", got)
	}
}

func TestTurnSubscriptionDeduplicatesPartialAssistantEchoWithSparseContentIndex(t *testing.T) {
	var events []providers.StreamEvent
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(func(event providers.StreamEvent) {
		events = append(events, event)
	}, done)

	sub.handleLine(`{"type":"stream_event","event":{"type":"message_start"}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text"}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"abc"}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"def"}}}`)
	// Partial assistant echoes contain only the completed block. Its local
	// position is 0 even though the corresponding stream block has index 1.
	sub.handleLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"abcdef"}]}}`)
	sub.handleLine(`{"type":"result","is_error":false,"result":"abcdef"}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("turn error: %v", out.err)
	}
	var streamed strings.Builder
	for _, event := range events {
		if event.Type == providers.EventContentDelta {
			streamed.WriteString(event.Content)
		}
	}
	if got := streamed.String(); got != "abcdef" {
		t.Fatalf("streamed content = %q, want one copy of the assistant text", got)
	}
	if got := out.result.Result.Content; got != "abcdef" {
		t.Fatalf("result content = %q, want abcdef", got)
	}
}

func TestTurnSubscriptionIgnoresChildAgentContent(t *testing.T) {
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(nil, done)

	sub.handleLine(`{"type":"stream_event","event":{"type":"message_start"}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"A"}}}`)
	sub.handleLine(`{"type":"stream_event","parent_tool_use_id":"toolu_child","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"X"}}}`)
	sub.handleLine(`{"type":"assistant","parent_tool_use_id":"toolu_child","message":{"content":[{"type":"text","text":"child answer"}]}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"B"}}}`)
	sub.handleLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"AB"}]}}`)
	sub.handleLine(`{"type":"result","is_error":false,"result":"AB"}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("turn error: %v", out.err)
	}
	if got := out.result.Result.Content; got != "AB" {
		t.Fatalf("result content = %q, want AB", got)
	}
}

func TestTurnSubscriptionDeduplicatesToolAndUsesEchoResult(t *testing.T) {
	var events []providers.StreamEvent
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(func(event providers.StreamEvent) {
		events = append(events, event)
	}, done)

	sub.handleLine(`{"type":"stream_event","event":{"type":"message_start"}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}}`)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"pwd\"}"}}}`)
	sub.handleLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"pwd"}}]}}`)
	sub.handleLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"/tmp"}]}}`)
	sub.handleLine(`{"type":"result","is_error":false,"result":"done"}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("turn error: %v", out.err)
	}
	starts, ends := 0, 0
	for _, event := range events {
		switch event.Type {
		case providers.EventToolUseStart:
			starts++
		case providers.EventToolUseEnd:
			ends++
			if event.ToolCall == nil || event.ToolCall.Arguments != `{"command":"pwd"}` {
				t.Fatalf("tool arguments = %+v, want valid complete JSON", event.ToolCall)
			}
			if event.ToolResult != "/tmp" {
				t.Fatalf("tool result = %q, want /tmp", event.ToolResult)
			}
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("tool events starts/ends = %d/%d, want 1/1", starts, ends)
	}
}

func TestTurnSubscriptionUsesResultErrorText(t *testing.T) {
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(nil, done)
	sub.handleLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: rate limit exceeded"}`)

	out := <-done
	if out.err == nil || !strings.Contains(out.err.Error(), "API Error: rate limit exceeded") {
		t.Fatalf("turn error = %v, want result error detail", out.err)
	}
}

func TestTurnSubscriptionTransportClosePreservesPartialOutput(t *testing.T) {
	var events []providers.StreamEvent
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(func(event providers.StreamEvent) {
		events = append(events, event)
	}, done)
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}}`)
	sub.handleTransportClose("claude exited")
	sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}}`)

	out := <-done
	if out.err == nil || out.result.Result.Content != "partial" {
		t.Fatalf("transport close result = content %q, err %v", out.result.Result.Content, out.err)
	}
	var visible strings.Builder
	for _, event := range events {
		if event.Type == providers.EventContentDelta {
			visible.WriteString(event.Content)
		}
	}
	if got := visible.String(); got != "partial" {
		t.Fatalf("visible content after close = %q, want partial", got)
	}
}

func TestEngineInterruptIsSilentAndNextTurnCanResume(t *testing.T) {
	binary := buildFakeClaude(t)
	engine := NewEngine(binary, t.TempDir())
	sess, err := engine.Open(context.Background(), agentengine.OpenRequest{ThreadID: "t", RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var sawError bool
	_, err = sess.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "wait_forever"}},
	}, func(event providers.StreamEvent) {
		if event.Type == providers.EventError {
			sawError = true
		}
		if event.Type == providers.EventContentDelta {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted RunTurn error = %v, want context.Canceled", err)
	}
	if sawError {
		t.Fatal("interrupted RunTurn emitted a visible error event")
	}

	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resumeCancel()
	result, err := sess.RunTurn(resumeCtx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn after interruption: %v", err)
	}
	if result.Result.Content != "Hello from claude." {
		t.Fatalf("resumed content = %q", result.Result.Content)
	}
}

func TestEnginePersistsSessionIDBeforeTurnCompletes(t *testing.T) {
	binary := buildFakeClaude(t)
	engine := NewEngine(binary, t.TempDir())
	persisted := make(chan string, 1)
	sess, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
		ThreadID: "t",
		RootDir:  t.TempDir(),
		PersistRef: func(ref string) error {
			select {
			case persisted <- ref:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("SessionForThread: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, runErr := sess.RunTurn(ctx, agentengine.TurnInput{
			History: []providers.ChatMessage{{Role: "user", Content: "wait_forever"}},
		}, nil)
		done <- runErr
	}()

	select {
	case ref := <-persisted:
		if ref != "fake-session-1" {
			t.Fatalf("persisted ref = %q, want fake-session-1", ref)
		}
	case err := <-done:
		t.Fatalf("turn ended before session id was persisted: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("session id was not persisted while turn remained active")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted RunTurn error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted turn did not stop")
	}
}

func TestTransportCloseStillReapsProcessAfterStdoutCloses(t *testing.T) {
	binary := buildFakeClaude(t)
	transport, err := NewTransport(TransportOptions{
		BinaryPath:  binary,
		Args:        []string{"--close-stdout-and-hang"},
		GracePeriod: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	closed := make(chan struct{})
	transport.OnClose(func(string) { close(closed) })
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("stdout close was not observed")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close after stdout EOF: %v", err)
	}
}

var _ = os.Getenv
