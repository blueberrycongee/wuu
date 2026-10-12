package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// doneObservedContext makes the queued-write cancellation deterministic: the
// request has reached its cancellable wait while another write owns the lock.
type doneObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *doneObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

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

	// Exercise failure recovery through the real subprocess and NDJSON adapter,
	// with a second live conversation sharing the same process. No inference or
	// user credentials are involved in this journey.
	for _, mode := range []string{"completed-exit", "crash", "invalid-json", "overflow-completed", "overflow-unconfirmed", "cancel-unconfirmed", "cancel-start-confirmed", "start-response-invalid", "error-unconfirmed", "invalid-terminal"} {
		t.Run(mode, func(t *testing.T) {
			scenarioCtx, scenarioCancel := context.WithTimeout(context.Background(), 8*time.Second)
			t.Cleanup(scenarioCancel)
			scenarioHost := NewHost(binary, t.TempDir())
			requestLog := filepath.Join(t.TempDir(), "requests.jsonl")
			scenarioHost.env = append(os.Environ(), "WUU_TEST_CODEX_REQUESTS="+requestLog)
			scenarioEngine := NewEngine(scenarioHost)
			peer, err := scenarioEngine.Open(scenarioCtx, agentengine.OpenRequest{ThreadID: "peer", RootDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			primary, err := scenarioEngine.Open(scenarioCtx, agentengine.OpenRequest{ThreadID: "primary", RootDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			client := primary.(*Session).client
			t.Cleanup(func() { _ = client.Close() })
			await := func(out <-chan turnOutcome) turnOutcome {
				t.Helper()
				select {
				case result := <-out:
					return result
				case <-scenarioCtx.Done():
					t.Fatalf("%s: turn remained running: %v", mode, scenarioCtx.Err())
					return turnOutcome{}
				}
			}
			peerStarted := make(chan struct{})
			peerResult := make(chan turnOutcome, 1)
			peerCtx, stopPeer := context.WithCancel(scenarioCtx)
			go func() {
				var once sync.Once
				result, err := peer.RunTurn(peerCtx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "e2e:pending"}}}, func(event providers.StreamEvent) {
					if event.Type == providers.EventContentDelta {
						once.Do(func() { close(peerStarted) })
					}
				})
				peerResult <- turnOutcome{result: result, err: err}
			}()
			select {
			case <-peerStarted:
			case <-scenarioCtx.Done():
				t.Fatalf("%s: peer failed to start", mode)
			}

			started := make(chan struct{})
			if mode == "cancel-start-confirmed" {
				unsub := client.OnNotification(NotifyAgentMessageDelta, func(raw json.RawMessage) {
					var params struct {
						ThreadID string `json:"threadId"`
					}
					if json.Unmarshal(raw, &params) == nil && params.ThreadID != peer.(*Session).ref {
						select {
						case <-started:
						default:
							close(started)
						}
					}
				})
				defer unsub()
			}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			primaryResult := make(chan turnOutcome, 1)
			primaryCtx, stopPrimary := context.WithCancel(scenarioCtx)
			go func() {
				var once sync.Once
				result, err := primary.RunTurn(primaryCtx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "e2e:" + mode}}}, func(event providers.StreamEvent) {
					if event.Type == providers.EventContentDelta {
						once.Do(func() {
							if mode != "cancel-start-confirmed" {
								close(started)
							}
						})
						if strings.HasPrefix(mode, "overflow-") {
							<-release
						}
					}
				})
				primaryResult <- turnOutcome{result: result, err: err}
			}()
			select {
			case <-started:
			case <-scenarioCtx.Done():
				t.Fatalf("%s: primary failed to start", mode)
			}
			if strings.HasPrefix(mode, "overflow-") {
				primary.(*Session).mu.Lock()
				ref := primary.(*Session).ref
				primary.(*Session).mu.Unlock()
				if err := client.Request(scenarioCtx, "test/emitBurst", map[string]string{"threadId": ref}, nil); err != nil {
					t.Fatalf("%s: emit output burst: %v", mode, err)
				}
				unblock()
			} else if mode == "invalid-terminal" {
				if err := client.Request(scenarioCtx, "test/emitInvalidTerminal", map[string]string{"threadId": primary.(*Session).ref}, nil); err != nil {
					t.Fatalf("emit invalid terminal: %v", err)
				}
			} else if mode == "cancel-unconfirmed" || mode == "cancel-start-confirmed" {
				stopPrimary()
			}
			out := await(primaryResult)
			stopPrimary()
			if mode == "completed-exit" {
				if out.err != nil || out.result.Result.Content != "Hello from codex." {
					t.Fatalf("terminal output lost before process exit: %+v", out)
				}
			} else {
				if out.err == nil || out.result.Result.Content != "Hello from" || len(out.result.Result.NewMessages) == 0 {
					t.Fatalf("%s: missing explicit failure or retained partial output: %+v", mode, out)
				}
				if strings.HasPrefix(mode, "overflow-") && !errors.Is(out.err, errTurnStreamOverflow) {
					t.Fatalf("%s: overflow not reported: %v", mode, out.err)
				}
			}
			if mode == "overflow-completed" || mode == "cancel-start-confirmed" || mode == "start-response-invalid" || mode == "invalid-terminal" {
				// Proof that the native turn stopped must leave its healthy peer alive.
				if mode == "overflow-completed" {
					func() {
						client.transport.writeMu.Lock()
						defer client.transport.writeMu.Unlock()
						queuedCtx, cancelQueued := context.WithCancel(scenarioCtx)
						defer cancelQueued()
						observed := &doneObservedContext{Context: queuedCtx, observed: make(chan struct{})}
						queuedResult := make(chan error, 1)
						go func() { queuedResult <- client.Request(observed, "config/read", map[string]any{}, nil) }()
						select {
						case <-observed.observed:
						case <-scenarioCtx.Done():
							t.Fatal("queued request did not enter its write wait")
						}
						cancelQueued()
						if err := <-queuedResult; !errors.Is(err, context.Canceled) {
							t.Fatalf("queued write cancellation = %v", err)
						}
					}()
				}
				if err := client.Request(scenarioCtx, "config/read", map[string]any{}, nil); err != nil {
					t.Fatalf("healthy peer lost its shared transport: %v", err)
				}
				select {
				case out := <-peerResult:
					t.Fatalf("unrelated thread was affected by primary failure: %+v", out)
				default:
				}
				stopPeer()
			}
			peerOut := await(peerResult)
			stopPeer()
			if peerOut.err == nil || peerOut.result.Result.Content != "Hello from" {
				t.Fatalf("%s: peer did not settle with its partial output: %+v", mode, peerOut)
			}
			requests, err := os.ReadFile(requestLog)
			if err != nil {
				t.Fatal(err)
			}
			starts := 0
			for _, line := range strings.Split(strings.TrimSpace(string(requests)), "\n") {
				var request struct{ Method string }
				if err := json.Unmarshal([]byte(line), &request); err != nil {
					t.Fatal(err)
				}
				if request.Method == MethodTurnStart {
					starts++
				}
			}
			if starts != 2 {
				t.Fatalf("%s: turn submission replayed: got %d starts, want two conversations", mode, starts)
			}
			t.Logf("journey=%s primary_error=%v peer_error=%v partial_output_retained=true turn_starts=%d", mode, out.err, peerOut.err, starts)
			scenarioCancel()
			_ = client.Close()
		})
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
