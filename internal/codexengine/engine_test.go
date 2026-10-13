package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

// Pause a selected real pipe write after an exact prefix. Cancellation then
// observes either no frame, a partial frame, or a fully accepted request.
type pipeWriteBarrier struct {
	io.WriteCloser
	release    <-chan struct{}
	unblock    func()
	entered    chan struct{}
	prefix     int
	method     string
	noDeadline bool
	failReset  bool
	once       sync.Once
}

func (w *pipeWriteBarrier) Write(p []byte) (int, error) {
	if w.method != "" && !strings.Contains(string(p), `"method":"`+w.method+`"`) {
		return w.WriteCloser.Write(p)
	}
	first := false
	w.once.Do(func() { first = true })
	if !first {
		return w.WriteCloser.Write(p)
	}
	prefix := w.prefix
	if prefix < 0 {
		prefix = len(p)
	}
	var n int
	var err error
	if prefix > 0 {
		n, err = w.WriteCloser.Write(p[:prefix])
	}
	close(w.entered)
	<-w.release
	if n < len(p) && err == nil {
		var rest int
		rest, err = w.WriteCloser.Write(p[n:])
		n += rest
	}
	return n, err
}

func (w *pipeWriteBarrier) SetWriteDeadline(deadline time.Time) error {
	if w.noDeadline {
		return os.ErrNoDeadline
	}
	if w.failReset && deadline.IsZero() {
		return errors.New("cannot reset pipe deadline")
	}
	err := w.WriteCloser.(interface{ SetWriteDeadline(time.Time) error }).SetWriteDeadline(deadline)
	if !deadline.IsZero() {
		w.unblock()
	}
	return err
}

func (w *pipeWriteBarrier) Close() error {
	err := w.WriteCloser.Close()
	w.unblock()
	return err
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
		defer sess.Close(context.Background())
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
		if request.Method == "turn/start" {
			if request.Params["effort"] != "high" {
				t.Fatalf("speed changed native effort: %v", request.Params)
			}
			if _, exists := request.Params["reasoningEffort"]; exists {
				t.Fatalf("turn/start uses effort, not the model/list reasoningEffort field: %v", request.Params)
			}
		}
		methods = append(methods, request.Method)
	}
	if strings.Join(methods, ",") != "thread/start,turn/start,thread/resume,turn/start,thread/resume,turn/start" {
		t.Fatalf("requests = %v", methods)
	}
}

func TestEngineEndToEndFakeCodex(t *testing.T) {
	binary := buildFakeCodex(t)
	t.Run("host ownership", func(t *testing.T) { testHostOwnership(t, binary) })
	for _, tc := range []struct {
		name                  string
		prefix                int
		noDeadline, failReset bool
		wantClosed            bool
	}{
		{"empty", 0, false, false, false},
		{"partial", 1, false, false, true},
		{"unsupported deadline", 1, true, false, true},
		{"reset failure", -1, false, true, true},
	} {
		t.Run("write cancellation/"+tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			host := NewHost(binary, t.TempDir())
			session, err := NewEngine(host).Open(ctx, agentengine.OpenRequest{ThreadID: "writer", RootDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			client := session.(*Session).client
			t.Cleanup(func() { _ = client.Close() })
			if err := client.transport.stdin.(interface{ SetWriteDeadline(time.Time) error }).SetWriteDeadline(time.Time{}); err != nil && !tc.noDeadline {
				t.Skip("pipe deadlines unavailable:", err)
			}
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			writer := &pipeWriteBarrier{WriteCloser: client.transport.stdin, release: release, unblock: unblock, entered: make(chan struct{}), prefix: tc.prefix, noDeadline: tc.noDeadline, failReset: tc.failReset}
			client.transport.mu.Lock()
			client.transport.stdin = writer
			client.transport.mu.Unlock()
			requestCtx, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			done := make(chan error, 1)
			go func() { done <- client.Request(requestCtx, "config/read", map[string]any{}, nil) }()
			select {
			case <-writer.entered:
			case <-ctx.Done():
				t.Fatal("write did not reach its cancellation boundary")
			}
			cancelRequest()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("request cancellation=%v", err)
				}
			case <-ctx.Done():
				t.Fatal("canceled write did not settle")
			}
			if err := client.Request(ctx, "config/read", map[string]any{}, nil); (err != nil) != tc.wantClosed {
				t.Fatalf("subsequent request=%v, want transport closed=%v", err, tc.wantClosed)
			}
		})
	}
	host := NewHost(binary, t.TempDir())
	engine := NewEngine(host)

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

	defer sess.Close(context.Background())
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
	defer sess2.Close(context.Background())
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
	defer resumed.Close(context.Background())
	if _, err := resumed.RunTurn(ctx, agentengine.TurnInput{
		History: []providers.ChatMessage{{Role: "user", Content: "resume"}},
	}, nil); err != nil {
		t.Fatalf("resumed RunTurn: %v", err)
	}

	// Exercise failure recovery through the real subprocess and NDJSON adapter,
	// with a second live conversation sharing the same process. No inference or
	// user credentials are involved in this journey.
	for _, mode := range []string{"completed-exit", "crash", "invalid-json", "overflow-completed", "overflow-unconfirmed", "cancel-unconfirmed", "cancel-start-confirmed", "start-response-invalid", "error-unconfirmed", "invalid-terminal", "invalid-terminal-status"} {
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
			if mode == "cancel-start-confirmed" {
				client.transport.mu.Lock()
				if writer := client.transport.stdin.(interface{ SetWriteDeadline(time.Time) error }); writer.SetWriteDeadline(time.Time{}) == nil {
					client.transport.stdin = &pipeWriteBarrier{WriteCloser: client.transport.stdin, release: release, unblock: unblock, entered: make(chan struct{}), prefix: -1, method: "turn/start"}
				}
				client.transport.mu.Unlock()
			}
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
			} else if strings.HasPrefix(mode, "invalid-terminal") {
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
				partialRetained := out.result.Result.Content == "Hello from"
				if strings.HasPrefix(mode, "overflow-") {
					partialRetained = strings.HasPrefix(out.result.Result.Content, "Hello from burst ")
				}
				if out.err == nil || !partialRetained || len(out.result.Result.NewMessages) == 0 {
					t.Fatalf("%s: missing explicit failure or retained partial output: %+v", mode, out)
				}
				if strings.HasPrefix(mode, "overflow-") && !errors.Is(out.err, errTurnStreamOverflow) {
					t.Fatalf("%s: overflow not reported: %v", mode, out.err)
				}
			}
			if mode == "overflow-completed" || mode == "cancel-start-confirmed" || mode == "start-response-invalid" || strings.HasPrefix(mode, "invalid-terminal") {
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
			starts, interrupts := 0, 0
			for _, line := range strings.Split(strings.TrimSpace(string(requests)), "\n") {
				var request struct{ Method string }
				if err := json.Unmarshal([]byte(line), &request); err != nil {
					t.Fatal(err)
				}
				if request.Method == MethodTurnStart {
					starts++
				}
				if request.Method == MethodTurnInterrupt {
					interrupts++
				}
			}
			if mode == "invalid-terminal-status" && interrupts != 2 {
				t.Fatalf("invalid native terminal status left a turn without confirmed interruption: got %d interrupts, want primary and peer", interrupts)
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

// observedAcquireContext coordinates cancellation only after Acquire starts
// observing the caller's context, while the child handshake remains gated.
type observedAcquireContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedAcquireContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func testHostOwnership(t *testing.T, binary string) {
	for _, scenario := range []string{"concurrent startup", "cancel one waiter", "cancel all waiters", "retained cancelled startup", "retired lease cleanup", "failed startup", "closed generation", "crashed generation", "retained crashed generation", "duplicate close"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			lifecycle := filepath.Join(root, "lifecycle.jsonl")
			t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
			host := NewHost(binary, root)
			engine := NewEngine(host)
			open := func(ctx context.Context) (agentengine.Session, error) {
				return engine.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: "owner", RootDir: root, ExternalRef: "native-owned-thread"})
			}
			keep := func(session agentengine.Session, err error) *Session {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close(context.Background()) })
				return session.(*Session)
			}
			turn := func(session *Session) {
				t.Helper()
				result, err := session.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "hello"}}}, nil)
				if err != nil || result.Result.Content != "Hello from codex." {
					t.Fatalf("healthy peer turn = %q, %v", result.Result.Content, err)
				}
			}
			count := func(method string) int {
				t.Helper()
				data, err := os.ReadFile(lifecycle)
				if os.IsNotExist(err) {
					return 0
				}
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var entry struct{ Method string }
					if json.Unmarshal([]byte(line), &entry) == nil && entry.Method == method {
						count++
					}
				}
				return count
			}
			wait := func(description string, ready func() bool) {
				t.Helper()
				for !ready() {
					select {
					case <-ctx.Done():
						t.Fatalf("waiting for %s: %v", description, ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			switch scenario {
			case "retired lease cleanup":
				releaseOwner := host.Retain()
				defer releaseOwner()
				exitGate := filepath.Join(root, "exit")
				t.Setenv("WUU_TEST_CODEX_EXIT_GATE", exitGate)
				peer := keep(open(ctx))
				t.Cleanup(func() { _ = os.WriteFile(exitGate, nil, 0600) })
				turn(peer)
				// A settings replacement drops the warm owner while its live
				// turn still owns the old generation until terminal cleanup.
				releaseOwner()
				done := make(chan struct{})
				go func() { _ = peer.Close(ctx); close(done) }()
				wait("retired child begins exit", func() bool { return count("exiting") == 1 })
				select {
				case <-done:
					t.Fatal("final turn release returned before the retired child exited")
				default:
				}
				if err := os.WriteFile(exitGate, nil, 0600); err != nil {
					t.Fatal(err)
				}
				<-done
				if count("exited") != 1 {
					t.Fatal("retired child cleanup outlived its final turn release")
				}
			case "retained cancelled startup":
				releaseOwner := host.Retain()
				defer releaseOwner()
				gate, exitGate := filepath.Join(root, "ready"), filepath.Join(root, "exit")
				t.Setenv("WUU_TEST_CODEX_INITIALIZE_GATE", gate)
				t.Setenv("WUU_TEST_CODEX_EXIT_GATE", exitGate)
				childCtx, cancelChild := context.WithCancel(ctx)
				defer cancelChild()
				result := make(chan error, 1)
				go func() { _, err := open(childCtx); result <- err }()
				wait("owned initialization", func() bool { return count("initialize") == 1 })
				cancelChild()
				wait("startup cancellation admission", func() bool {
					host.mu.Lock()
					defer host.mu.Unlock()
					return host.starting == nil
				})
				if err := os.WriteFile(gate, nil, 0600); err != nil {
					t.Fatal(err)
				}
				wait("native child begins exit", func() bool { return count("exiting") == 1 })
				select {
				case err := <-result:
					t.Fatalf("last canceled acquire returned before its child exited: %v", err)
				default:
				}
				if err := os.WriteFile(exitGate, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatalf("startup error = %v", err)
				}
				releaseOwner()
				if count("exited") != 1 {
					t.Fatal("final owner outlived canceled-startup cleanup")
				}
			case "concurrent startup", "cancel one waiter", "cancel all waiters":
				gate := filepath.Join(root, "initialize-ready")
				t.Setenv("WUU_TEST_CODEX_INITIALIZE_GATE", gate)
				type outcome struct {
					session agentengine.Session
					err     error
				}
				start := func(parent context.Context) (context.CancelFunc, <-chan outcome) {
					child, cancel := context.WithCancel(parent)
					observed := &observedAcquireContext{Context: child, observed: make(chan struct{})}
					done := make(chan outcome, 1)
					go func() { session, err := open(observed); done <- outcome{session, err} }()
					select {
					case <-observed.observed:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					return cancel, done
				}
				cancelFirst, first := start(ctx)
				defer cancelFirst()
				wait("first initialization", func() bool { return count("initialize") == 1 })
				cancelSecond, second := start(ctx)
				defer cancelSecond()
				if scenario != "concurrent startup" {
					cancelFirst()
					if result := <-first; !errors.Is(result.err, context.Canceled) {
						t.Fatalf("canceled acquire = %v", result.err)
					}
				}
				if scenario == "cancel all waiters" {
					cancelSecond()
					if err := os.WriteFile(gate, nil, 0600); err != nil {
						t.Fatal(err)
					}
					if result := <-second; !errors.Is(result.err, context.Canceled) {
						t.Fatalf("canceled peer = %v", result.err)
					}
				}
				if err := os.WriteFile(gate, nil, 0600); err != nil {
					t.Fatal(err)
				}
				var peer *Session
				if scenario == "cancel all waiters" {
					peer = keep(open(ctx))
				} else {
					result := <-second
					peer = keep(result.session, result.err)
				}
				turn(peer)
				if scenario == "concurrent startup" {
					result := <-first
					owner := keep(result.session, result.err)
					turn(owner)
					_ = owner.Close(ctx)
					turn(peer)
				}
				wantStarts := 1
				if scenario == "cancel all waiters" {
					wantStarts = 2
				}
				if got := count("initialize"); got != wantStarts {
					t.Fatalf("native initializations = %d, want %d", got, wantStarts)
				}
				_ = peer.Close(ctx)
				wait("all subprocesses exit", func() bool { return count("exited") == count("started") })
			case "failed startup":
				t.Setenv("WUU_TEST_CODEX_FAIL_INITIALIZE_ONCE", filepath.Join(root, "initialize-failed"))
				if session, err := open(ctx); err == nil {
					_ = session.Close(ctx)
					t.Fatal("first startup must fail")
				}
				peer := keep(open(ctx))
				turn(peer)
				_ = peer.Close(ctx)
				wait("retry subprocesses exit", func() bool { return count("exited") == 2 })
			case "closed generation", "crashed generation", "retained crashed generation":
				releaseOwner := func() {}
				if scenario == "retained crashed generation" {
					releaseOwner = host.Retain()
					defer releaseOwner()
				}
				first, oldPeer := keep(open(ctx)), keep(open(ctx))
				if strings.Contains(scenario, "crashed generation") {
					if err := first.client.transport.cmd.Process.Kill(); err != nil {
						t.Fatal(err)
					}
					wait("crashed connection closes", func() bool {
						first.client.mu.Lock()
						defer first.client.mu.Unlock()
						return first.client.closed
					})
				} else {
					_ = first.client.Close()
				}
				peer := keep(open(ctx))
				_ = first.Close(ctx)
				_ = first.Close(ctx)
				_ = oldPeer.Close(ctx)
				turn(peer)
				_ = peer.Close(ctx)
				releaseOwner()
				wantExits := 2
				if strings.Contains(scenario, "crashed generation") {
					wantExits = 1 // The killed process cannot run its graceful-exit hook.
				}
				wait("replacement generation exits", func() bool { return count("exited") == wantExits })
			case "duplicate close":
				first, peer := keep(open(ctx)), keep(open(ctx))
				_ = first.Close(ctx)
				_ = first.Close(ctx)
				turn(peer)
				_ = peer.Close(ctx)
				wait("shared subprocess exits", func() bool { return count("exited") == 1 })
			}
			t.Logf("native subprocess starts=%d, initialize=%d, graceful exits=%d", count("started"), count("initialize"), count("exited"))
		})
	}
}
