package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A provider gate, rather than a timer, distinguishes independent progress
// from the later completion of the original read. No network model is used.
func TestRunStdioResumeKeepsOtherSessionsResponsiveAndSameSessionOrdered(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	provider := session.NewService("resume-stdio", rt.Documents)
	if err := provider.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })
	entered, release := make(chan struct{}), make(chan struct{})
	var enabled atomic.Bool
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Args []json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(raw, &args)
		if method == "LoadProviderHistorySnapshot" && len(args.Args) > 0 && string(args.Args[0]) == `"a"` && enabled.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "resume-stdio", Method: method, Params: raw})
	})
	for _, id := range []string{"a", "b"} {
		if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
	}
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	enabled.Store(true)
	wire := `{"id":"resume-a","method":"thread/resume","params":{"session_id":"a","response_only":true}}` + "\n"
	wire += `{"id":"rename-a","method":"thread/rename","params":{"thread_id":"a","title":"renamed in order"}}` + "\n"
	wire += `{"id":"queue-a","method":"turn/queue","params":{"thread_id":"a","prompt":"A after resume","client_id":"a-input","hold":true}}` + "\n"
	wire += `{"id":"resume-a-after","method":"thread/resume","params":{"session_id":"a","response_only":true}}` + "\n"
	wire += `{"id":"queue-b","method":"turn/queue","params":{"thread_id":"b","prompt":"B independent","client_id":"b-input","hold":true}}` + "\n"
	wire += `{"id":"stop-b","method":"turn/interrupt","params":{"thread_id":"b"}}` + "\n"
	done := make(chan error, 1)
	go func() { done <- runStdioScanner(context.Background(), server, strings.NewReader(wire)) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not reach provider gate")
	}
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !resumeStdioHasResponse(t, out, "stop-b") {
		select {
		case <-deadline:
			t.Fatal("another session's queue/interrupt waited for gated resume")
		case <-ticker.C:
		}
	}
	if resumeStdioHasResponse(t, out, "resume-a") || resumeStdioHasResponse(t, out, "queue-a") {
		t.Fatal("same-session successor overtook resume")
	}
	unblock()
	waitResumeStdioResponse(t, out, "resume-a-after")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scanner did not finish")
	}
	messages := parseOutput(t, out.String())
	positions := map[string]int{}
	for i, message := range messages {
		if id, ok := message["id"].(string); ok {
			positions[id] = i
		}
	}
	for _, id := range []string{"resume-a", "rename-a", "queue-a", "resume-a-after", "queue-b", "stop-b"} {
		if r := responseByID(t, messages, id); r["error"] != nil {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	ordered := []string{"resume-a", "rename-a", "queue-a", "resume-a-after"}
	for i := 1; i < len(ordered); i++ {
		if positions[ordered[i-1]] >= positions[ordered[i]] {
			t.Fatal("same-session read/mutation/input order changed")
		}
	}
	after := remarshal[ThreadResumeResult](t, responseByID(t, messages, "resume-a-after")["result"])
	if after.Thread.Title != "renamed in order" {
		t.Fatal("later resume did not observe predecessor mutation")
	}

	held, err := rt.Sessions.LoadHeldUserWork("a")
	if err != nil || len(held) != 1 {
		t.Fatalf("same-session input not persisted: %v, %v", held, err)
	}
	t.Log("queue/interrupt B completed while resume A remained blocked; A input followed resume; both completed")
}

func resumeStdioHasResponse(t *testing.T, out *lockedBuffer, id string) bool {
	t.Helper()
	for _, message := range parseOutput(t, out.String()) {
		if message["id"] == id {
			return true
		}
	}
	return false
}

func TestRunStdioActiveInterruptBypassesSameSessionResumeRead(t *testing.T) {
	for _, pendingInput := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending-input-%v", pendingInput), func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			model := newBlockingStreamClient("synthetic")
			rt.StreamRunner.Client = model
			attachTestModelGeneration(t, rt)
			t.Cleanup(func() { close(model.release) })
			provider := session.NewService("resume-stop", rt.Documents)
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(context.Background()) })
			var enabled atomic.Bool
			var once sync.Once
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := func() { once.Do(func() { close(release) }) }
			rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == "LoadHeldUserWork" && enabled.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "resume-stop", Method: method, Params: raw})
			})
			if _, err := rt.Sessions.CreateWithMetadata("running", rt.RootDir); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			server := New(rt, out)
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			dispatchPayload(t, server, "start", MethodTurnStart, TurnStartParams{ThreadID: "running", Prompt: "synthetic"})
			if response := responseByID(t, parseOutput(t, out.String()), "start"); response["error"] != nil {
				t.Fatal(response)
			}
			select {
			case <-model.started:
			case <-time.After(5 * time.Second):
				t.Fatal("synthetic model did not start")
			}
			if !threadIsRunning(server.thread("running")) {
				t.Fatal("fixture has no active turn")
			}
			in, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close(); _ = in.Close() })
			done := make(chan error, 1)
			go func() { done <- runStdioScanner(context.Background(), server, in) }()
			enabled.Store(true)
			if _, err := io.WriteString(writer, `{"id":"resume","method":"thread/resume","params":{"session_id":"running","response_only":true}}`+"\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("resume did not enter read gate")
			}
			if pendingInput {
				if _, err := io.WriteString(writer, `{"id":"queue","method":"turn/queue","params":{"thread_id":"running","prompt":"earlier user input","client_id":"queued-before-stop"}}`+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := io.WriteString(writer, `{"id":"stop","method":"turn/interrupt","params":{"thread_id":"running"}}`+"\n"); err != nil {
				t.Fatal(err)
			}
			if pendingInput {
				if _, err := io.WriteString(writer, `{"id":"sentinel","method":"thread/list","params":{}}`+"\n"); err != nil {
					t.Fatal(err)
				}
				waitResumeStdioResponse(t, out, "sentinel")
				if resumeStdioHasResponse(t, out, "stop") {
					t.Fatal("Stop overtook earlier submitted input")
				}
				unblock()
			}
			deadline := time.After(time.Second)
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for !resumeStdioHasResponse(t, out, "stop") {
				select {
				case <-deadline:
					t.Fatal("active Stop waited behind same-session read")
				case <-tick.C:
				}
			}
			if response := responseByID(t, parseOutput(t, out.String()), "stop"); response["error"] != nil {
				t.Fatal(response)
			}
			if !pendingInput && resumeStdioHasResponse(t, out, "resume") {
				t.Fatal("read gate was not still held")
			}
			deadline = time.After(5 * time.Second)
			for threadIsRunning(server.thread("running")) {
				select {
				case <-deadline:
					t.Fatal("Stop did not settle before the read returned")
				case <-tick.C:
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			unblock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scanner did not finish")
			}
			waitResumeStdioResponse(t, out, "resume")
			if !pendingInput {
				resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
				if resumed.Thread.Status == ThreadStatusInProgress || !resumed.Thread.TreeInterrupted {
					t.Fatal("delayed resume reverted the completed Stop snapshot")
				}
			}

			deadline = time.After(5 * time.Second)
			for threadIsRunning(server.thread("running")) {
				select {
				case <-deadline:
					t.Fatal("Stop ack did not cancel synthetic inference")
				case <-tick.C:
				}
			}
			waitResumeStdioDrained(t, server)
			if pendingInput {
				t.Log("Stop followed earlier submitted input; synthetic inference cancelled without a later restart")
			} else {
				t.Log("active Stop acknowledged before same-session resume read returned; late snapshot preserved Stop")
			}
		})
	}
}

func waitResumeStdioResponse(t *testing.T, out *lockedBuffer, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !resumeStdioHasResponse(t, out, id) {
		select {
		case <-deadline:
			t.Fatalf("response %s did not arrive", id)
		case <-ticker.C:
		}
	}
}

func TestRunStdioResumeKeepsCapturedAuthorityAndRechecksOrderedSuccessors(t *testing.T) {
	for _, outcome := range []string{"replacement", "revoked", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			provider := session.NewService("resume-authority", rt.Documents)
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(context.Background()) })
			entered, release := make(chan struct{}), make(chan struct{})
			var enabled atomic.Bool
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == "LoadProviderHistorySnapshot" && enabled.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "resume-authority", Method: method, Params: raw})
			})
			if _, err := rt.Sessions.CreateWithMetadata("a", rt.RootDir); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Sessions.UpdateTitle("a", "original generation"); err != nil {
				t.Fatal(err)
			}
			caller, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "resume-reader"})
			if err != nil {
				t.Fatal(err)
			}
			var revoked atomic.Bool
			var ownerChecks atomic.Int64
			policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
				if r.Owner {
					ownerChecks.Add(1)
					return access.Decision{Allow: true}, nil
				}
				if r.Principal != caller.Identity() || revoked.Load() {
					return access.Decision{}, nil
				}
				for _, resource := range r.Resources {
					if resource.Kind != "session" || resource.ID != "a" {
						return access.Decision{}, nil
					}
				}
				return access.Decision{Allow: true}, nil
			})
			out := &lockedBuffer{}
			server := New(rt, out, WithAccessPolicy(policy))
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			ctx, cancel := context.WithCancel(principal.WithContext(context.Background(), caller))
			defer cancel()
			enabled.Store(true)
			input := `{"id":"first","method":"thread/resume","params":{"session_id":"a","response_only":true}}` + "\n"
			input += `{"id":"second","method":"thread/resume","params":{"session_id":"a","response_only":true}}` + "\n"
			scannerDone := make(chan error, 1)
			go func() { scannerDone <- runStdioScanner(ctx, server, strings.NewReader(input)) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("read did not enter provider")
			}
			select {
			case err := <-scannerDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scanner blocked on admitted read")
			}
			if outcome == "replacement" {
				successor := newTestRuntime(t, &fakeClient{})
				if _, err := successor.Sessions.CreateWithMetadata("a", successor.RootDir); err != nil {
					t.Fatal(err)
				}
				if _, err := successor.Sessions.UpdateTitle("a", "replacement generation"); err != nil {
					t.Fatal(err)
				}
				server.rt = successor
				oldHost := rt.PluginHost
				if _, err := rt.Cleanup(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-oldHost.ShutdownDone():
					t.Fatal("admitted read lost captured generation")
				default:
				}
				unblock()
				waitResumeStdioResponse(t, out, "second")
				messages := parseOutput(t, out.String())
				for id, want := range map[string]string{"first": "original generation", "second": "replacement generation"} {
					response := responseByID(t, messages, id)
					if response["error"] != nil {
						t.Fatalf("%s: %+v", id, response)
					}
					got := remarshal[ThreadResumeResult](t, response["result"]).Thread.Title
					if got != want {
						t.Fatalf("%s selected %q, wanted %q", id, got, want)
					}
				}
			} else {
				if outcome == "cancelled" {
					cancel()
				} else {
					revoked.Store(true)
				}
				closed := make(chan struct{})
				go func() { server.Close(); close(closed) }()
				<-server.nativeHistoryCtx.Done()
				select {
				case <-closed:
					t.Fatal("Close abandoned provider still physically running")
				default:
				}
				unblock()
				select {
				case <-closed:
				case <-time.After(5 * time.Second):
					t.Fatal("Close did not join after provider release")
				}
				messages := parseOutput(t, out.String())
				for _, id := range []string{"first", "second"} {
					response := responseByID(t, messages, id)
					if response["error"] == nil || response["result"] != nil {
						t.Fatalf("revoked read leaked result: %+v", response)
					}
				}
				if strings.Contains(out.String(), "original generation") {
					t.Fatal("revoked snapshot leaked its contents")
				}
			}
			if ownerChecks.Load() != 0 {
				t.Fatal("deferred request borrowed connection owner authority")
			}
			waitResumeStdioDrained(t, server)
			server.access.mu.Lock()
			pending := len(server.access.pending)
			server.access.mu.Unlock()
			if pending != 0 {
				t.Fatalf("completed requests leaked %d authority records", pending)
			}
		})
	}
}

func waitResumeStdioDrained(t *testing.T, server *Server) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		server.stdioResumeMu.Lock()
		pending := len(server.stdioResumeTail)
		server.stdioResumeMu.Unlock()
		if pending == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("resume dependency keys leaked")
		case <-tick.C:
		}
	}
}

// An admitted read may keep a projection after another read evicts its cache
// entry, or when a foreign execution owner prevents installing that projection.
func TestThreadResumeRetainsReadOnlyHistoryOutsideCache(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	binding := testRuntimeState(t, rt)
	if _, err := binding.Sessions.CreateWithMetadata("detached", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	source := newThreadState("detached", nil, "", "", rt.RootDir, true, time.Now().UTC())
	source.Turns = []Turn{{ID: "captured-turn", Status: TurnStatusCompleted}}
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	if err := server.writeThreadResumeResult(binding, Request{ID: json.RawMessage(`"detached"`)}, source.snapshotTurnsLocked(nil), ThreadResumeParams{ResponseOnly: true}, source); err != nil {
		t.Fatal(err)
	}
	result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "detached")["result"])
	if len(result.Thread.Turns) != 1 || result.Thread.Turns[0].ID != "captured-turn" {
		t.Fatal("read-only history disappeared with its cache entry")
	}
}
