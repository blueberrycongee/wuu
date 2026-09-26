package codexengine

import (
	"context"
	"encoding/json"
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

// buildFakeCodex compiles the fake app-server binary once per test run.
func buildFakeCodex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fakecodex")
	cmd := exec.Command("go", "build", "-o", binary, "./testdata/fakecodex")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake codex: %v\n%s", err, out)
	}
	return binary
}

func TestEngineSpeedSurvivesNativeResume(t *testing.T) {
	binary := buildFakeCodex(t)
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_REQUESTS", logPath)
	t.Setenv("WUU_TEST_CODEX_DEFAULT_TIER", "fast")
	host := NewHost(binary, t.TempDir())
	defer host.Release()
	engine := NewEngine(host)
	ref := ""
	for _, speed := range []string{"fast", "standard", ""} {
		sess, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
			ThreadID: "speed-thread", RootDir: t.TempDir(), Model: "gpt-6-astra",
			Effort: "high", Speed: speed, ExternalRef: ref,
			PersistRef: func(value string) error { ref = value; return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err = sess.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "hello"}}}, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request struct {
			Method string
			Params map[string]any
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "thread/start" && request.Method != "thread/resume" && request.Method != "turn/start" {
			continue
		}
		want := "fast"
		if len(methods) >= 2 && len(methods) < 4 {
			want = "default"
		}
		if request.Params["serviceTier"] != want {
			t.Fatalf("%s tier = %v, want %s", request.Method, request.Params["serviceTier"], want)
		}
		if request.Method == "turn/start" && request.Params["reasoningEffort"] != "high" {
			t.Fatalf("speed changed effort: %v", request.Params)
		}
		methods = append(methods, request.Method)
	}
	if strings.Join(methods, ",") != "thread/start,turn/start,thread/resume,turn/start,thread/resume,turn/start" {
		t.Fatalf("requests = %v", methods)
	}
}

func TestEngineEndToEndFakeCodex(t *testing.T) {
	binary := buildFakeCodex(t)
	host := NewHost(binary, t.TempDir())
	engine := NewEngine(host)
	defer host.Release()

	desc, err := engine.Descriptor(context.Background())
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if desc.ID != agentengine.EngineID("codex") {
		t.Fatalf("descriptor id = %q, want codex", desc.ID)
	}

	persisted := ""
	sess, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
		ThreadID: "wuu-thread-1",
		RootDir:  t.TempDir(),
		Model:    "gpt-5",
		PersistRef: func(ref string) error {
			persisted = ref
			return nil
		},
	})
	if err != nil {
		t.Fatalf("SessionForThread: %v", err)
	}

	var events []providers.StreamEvent
	var content strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := sess.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(ev providers.StreamEvent) {
		events = append(events, ev)
		if ev.Type == providers.EventContentDelta {
			content.WriteString(ev.Content)
		}
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if persisted != "codex-thread-1" {
		t.Fatalf("persisted ref = %q, want codex-thread-1", persisted)
	}
	if got := content.String(); got != "Hello from codex." {
		t.Fatalf("streamed content = %q, want %q", got, "Hello from codex.")
	}
	if got := result.Result.Content; got != "Hello from codex." {
		t.Fatalf("result content = %q, want %q", got, "Hello from codex.")
	}
	if len(result.Result.NewMessages) != 1 || result.Result.NewMessages[0].Role != "assistant" {
		t.Fatalf("NewMessages = %+v, want one assistant message", result.Result.NewMessages)
	}
	if result.Result.NewMessages[0].Phase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("NewMessages phase = %q, want final_answer", result.Result.NewMessages[0].Phase)
	}
	messageIndex, doneIndex := -1, -1
	for index, event := range events {
		if event.Type == providers.EventMessage && event.Message != nil {
			messageIndex = index
			if event.Message.Content != "Hello from codex." || event.Message.Phase != providers.MessagePhaseFinalAnswer {
				t.Fatalf("completed agent message = %+v, want final answer", event.Message)
			}
		}
		if event.Type == providers.EventDone {
			doneIndex = index
		}
	}
	if messageIndex < 0 || doneIndex < 0 || messageIndex >= doneIndex {
		t.Fatalf("event order message=%d done=%d, want completed message before turn done", messageIndex, doneIndex)
	}
	if result.Result.InputTokens != 60 || result.Result.OutputTokens != 20 || result.Result.CacheReadTokens != 40 {
		t.Fatalf("usage = in %d out %d cache %d, want 60/20/40",
			result.Result.InputTokens, result.Result.OutputTokens, result.Result.CacheReadTokens)
	}

	// A second turn on the same session reuses the persisted ref (no new
	// thread/start) and still completes.
	sess2, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
		ThreadID:    "wuu-thread-1",
		RootDir:     t.TempDir(),
		ExternalRef: "codex-thread-1",
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

	// Resume via the factory Open/Resume seam.
	resumed, err := engine.Resume(context.Background(), agentengine.ResumeRequest{
		OpenRequest:        agentengine.OpenRequest{ThreadID: "wuu-thread-2", RootDir: t.TempDir()},
		ExternalSessionRef: "codex-thread-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := resumed.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "resume"}},
	}, nil); err != nil {
		t.Fatalf("resumed RunTurn: %v", err)
	}
}

func TestEngineMissingBinaryFailsClearly(t *testing.T) {
	host := NewHost(filepath.Join(t.TempDir(), "no-such-codex"), t.TempDir())
	engine := NewEngine(host)
	_, err := engine.Open(context.Background(), agentengine.OpenRequest{ThreadID: "t", RootDir: t.TempDir()})
	if err == nil {
		t.Fatal("Open without a codex binary must fail")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error should mention codex, got: %v", err)
	}
}

func TestCompletedAgentMessageRequiresKnownPhaseForEarlyCompletion(t *testing.T) {
	var events []providers.StreamEvent
	sub := &turnSubscription{
		sink: func(event providers.StreamEvent) {
			events = append(events, event)
		},
	}

	sub.emitItem(NotifyItemCompleted, json.RawMessage(`{"id":"message-1","type":"agentMessage","text":"still working"}`))
	if len(events) != 0 {
		t.Fatalf("phase-less agent message emitted early events: %+v", events)
	}

	sub.emitItem(NotifyItemCompleted, json.RawMessage(`{"id":"message-2","type":"agentMessage","text":"done","phase":"final_answer"}`))
	if len(events) != 1 || events[0].Type != providers.EventMessage || events[0].Message == nil {
		t.Fatalf("final agent message events = %+v, want one message event", events)
	}
}

func TestSessionTranslatesCommandApproval(t *testing.T) {
	var got agentengine.ApprovalRequest
	sess := &Session{
		ref: "codex-thread-1",
		approval: func(_ context.Context, request agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
			got = request
			return agentengine.ApprovalAcceptForSession, nil
		},
	}
	raw, err := json.Marshal(CommandExecutionApprovalParams{
		ThreadID: "codex-thread-1", TurnID: "turn-1", ItemID: "item-1",
		Command: "git status", CWD: "/workspace", Reason: "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sess.handleCommandApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(ApprovalDecisionResponse)
	if !ok || response.Decision != DecisionAcceptForSession {
		t.Fatalf("response = %#v", result)
	}
	if got.Kind != agentengine.ApprovalCommandExecution || got.Command != "git status" || got.ItemID != "item-1" {
		t.Fatalf("translated request = %+v", got)
	}
}

func TestSessionDeclinesApprovalWithoutHostHandler(t *testing.T) {
	sess := &Session{ref: "codex-thread-1"}
	raw := json.RawMessage(`{"threadId":"codex-thread-1","turnId":"turn-1","filePath":"a.txt"}`)
	result, err := sess.handleFileChangeApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	response := result.(ApprovalDecisionResponse)
	if response.Decision != DecisionDecline {
		t.Fatalf("decision = %q, want decline", response.Decision)
	}
}

func TestResolveBinaryFindsUserInstallOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	t.Setenv("WUU_CODEX_BINARY", "")
	t.Setenv("PATH", t.TempDir())
	name := "codex"
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
	t.Setenv("WUU_CODEX_BINARY", "/nonexistent/codex")
	path, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary with env override: %v", err)
	}
	if path != "/nonexistent/codex" {
		t.Fatalf("ResolveBinary = %q, want env value", path)
	}
}

var _ = os.Getenv
