package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// A tool permission hook that ignores cancellation represents in-flight work.
// The bounded server shutdown must return but retain its archive handle until
// that work actually exits, then release it.
func TestServerCloseRetainsArchiveForInFlightTurn(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(resume) })
	client := &fakeClient{responses: []providers.ChatResponse{{ToolCalls: []providers.ToolCall{{ID: "held", Name: "read_file", Arguments: `{"path":"sample.txt"}`}}}, providersResponse("done")}}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)
	srv.shutdownDrainTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	th := srv.thread(start.Thread.ID)
	thread, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if thread == nil || thread.Toolkit == nil {
		t.Fatal("thread toolkit missing")
	}
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
		if call.ID == "held" {
			close(entered)
			<-resume
		}
		return nil
	})
	settled := thread.Toolkit.FinalizeToolResult(providers.ToolCall{ID: "archive", Name: "synthetic"}, toolresult.FromText(strings.Repeat("evidence", 10000)))
	var first map[string]any
	if err := json.Unmarshal([]byte(settled.TextProjection()), &first); err != nil {
		t.Fatal(err)
	}
	next := first["continuation"].(map[string]any)["next"].(map[string]any)
	args, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "recover", Name: "recover_tool_result", Arguments: string(args)}
	if page, err := thread.Toolkit.Execute(context.Background(), call); err != nil || !strings.Contains(page, "evidence") {
		t.Fatalf("archive invalid before close: %q %v", page, err)
	}
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: start.Thread.ID, Prompt: "hold"})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach tool")
	}
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded close blocked")
	}
	page, err := thread.Toolkit.Execute(context.Background(), call)
	if err != nil || !strings.Contains(page, "evidence") {
		t.Fatalf("archive released before turn finished: %q %v", page, err)
	}
	once.Do(func() { close(resume) })
	deadline := time.After(5 * time.Second)
	for {
		_, err = thread.Toolkit.Execute(context.Background(), call)
		if err != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("archive retained after turn drained")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestServerCloseReleasesArchiveAfterCanceledModelTurn(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(resume) })
	client := &fakeClient{response: providersResponse("done"), onChat: func(int, providers.ChatRequest) { close(entered); <-resume }}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)
	srv.shutdownDrainTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	th := srv.thread(start.Thread.ID)
	thread, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if thread == nil || thread.Toolkit == nil {
		t.Fatal("thread toolkit missing")
	}
	settled := thread.Toolkit.FinalizeToolResult(providers.ToolCall{ID: "archive", Name: "synthetic"}, toolresult.FromText(strings.Repeat("evidence", 10000)))
	var first map[string]any
	if err := json.Unmarshal([]byte(settled.TextProjection()), &first); err != nil {
		t.Fatal(err)
	}
	next := first["continuation"].(map[string]any)["next"].(map[string]any)
	args, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "recover", Name: "recover_tool_result", Arguments: string(args)}
	if page, err := thread.Toolkit.Execute(context.Background(), call); err != nil || !strings.Contains(page, "evidence") {
		t.Fatalf("archive invalid before close: %q %v", page, err)
	}
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: start.Thread.ID, Prompt: "hold"})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach model")
	}
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded close blocked")
	}

	th.mu.Lock()
	completion := th.turnCompletion
	th.mu.Unlock()
	if completion == nil {
		t.Fatal("turn has no completion")
	}
	select {
	case <-completion.done:
	case <-time.After(5 * time.Second):
		t.Fatal("model turn did not physically finish")
	}
	select {
	case <-srv.shutdownBackgroundDrained:
	case <-time.After(5 * time.Second):
		t.Fatal("background shutdown did not drain")
	}
	// The reliable-stream adapter has already ended the turn on cancellation,
	// even though the underlying non-cooperative model callback is still held.
	// The detached callback has not returned at this point.
	deadline := time.After(5 * time.Second)
	for {
		_, err = thread.Toolkit.Execute(context.Background(), call)
		if err != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("archive retained after canceled turn completed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	once.Do(func() { close(resume) })
}
