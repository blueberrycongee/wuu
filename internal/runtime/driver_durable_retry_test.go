package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// A real process dies with an inference in flight, then again after its retry
// deadline is committed. Neither reopening nor an early physical turn may
// bypass that durable deadline or count as a successful logical answer.
func TestDurableDriverModelRetryProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("requires independently built durable plugin fixture")
	}
	h := newDurableProcessHarness(t, fixture, "model-retry-double-kill")
	store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
	barrier := &durableCheckpointRecorder{CheckpointStore: store, directory: h.evidence}
	model := &durableInterruptedModel{barrier: barrier}
	initial := []providers.ChatMessage{{Role: "user", Content: "Answer after a durable model retry."}}
	runner := &agent.StreamRunner{Client: model, Model: "durable-retry", DriverBindingStore: store, DriverCheckpointStore: barrier}
	attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acquire, stop, pid := h.start(t, 1)
	runner.AcquireLoopDriver = acquire
	barrier.pid = pid
	_, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "interrupted"}), initial, nil)
	stop()
	if err == nil || !barrier.killed || model.calls != 1 {
		t.Fatalf("inflight kill missing: calls=%d killed=%v err=%v", model.calls, barrier.killed, err)
	}
	barrier.killed = false
	barrier.killPhase = "sleeping"
	acquire, stop, pid = h.start(t, 2)
	runner.AcquireLoopDriver = acquire
	barrier.pid = pid
	_, err = runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "persist-backoff"}), initial, nil)
	stop()
	if err == nil || !barrier.killed || model.calls != 1 {
		t.Fatalf("backoff commit kill missing: calls=%d killed=%v err=%v", model.calls, barrier.killed, err)
	}
	saved, found, err := store.Load(ctx)
	if err != nil || !found {
		t.Fatalf("load checkpoint found=%v err=%v", found, err)
	}
	var state struct {
		RunID        string `json:"run_id"`
		RetryAt      string `json:"retry_at"`
		RestartCount int    `json:"restart_count"`
	}
	if err = json.Unmarshal(saved.State, &state); err != nil {
		t.Fatal(err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, state.RetryAt)
	if err != nil {
		t.Fatal(err)
	}
	if state.RestartCount != 0 {
		t.Fatalf("durable progress did not reset crash budget: %s", saved.State)
	}
	originalRun := state.RunID
	barrier.killPhase = ""
	barrier.killed = false
	acquire, stop, _ = h.start(t, 3)
	runner.AcquireLoopDriver = acquire
	early, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "early-reopen"}), initial, nil)
	if err != nil || early.Content != "" || model.calls != 1 {
		t.Fatalf("backoff bypass: calls=%d result=%+v err=%v", model.calls, early, err)
	}
	if delay := time.Until(deadline); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "retry-due"}), initial, nil)
	stop()
	if err != nil || result.Content != "Retry answer committed." || model.calls != 2 {
		t.Fatalf("retry result: calls=%d result=%+v err=%v", model.calls, result, err)
	}
	saved, _, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(saved.State, &state); err != nil {
		t.Fatal(err)
	}
	if state.RunID != originalRun || state.RestartCount != 0 {
		t.Fatalf("retry changed logical run or retained crash budget: %s", saved.State)
	}
	h.write(t, "retry-result.json", map[string]any{"model_calls": model.calls, "run_id": state.RunID, "retry_at": deadline, "answer": result.Content, "checkpoint": saved})
}

type durableInterruptedModel struct {
	calls   int
	barrier *durableCheckpointRecorder
}

func (*durableInterruptedModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *durableInterruptedModel) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls++
	if m.calls == 1 {
		if err := m.barrier.kill(); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	stream := make(chan providers.StreamEvent, 2)
	stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Retry answer committed."}
	stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	close(stream)
	return stream, nil
}

// Rewriting the same ready checkpoint is not forward progress. A repeated
// crash at that boundary must exhaust the configured default budget without
// ever asking the provider or touching a tool.
func TestDurableDriverNoProgressProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("requires independently built durable plugin fixture")
	}
	h := newDurableProcessHarness(t, fixture, "no-progress-crash-budget")
	store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
	barrier := &durableCheckpointRecorder{CheckpointStore: store, directory: h.evidence, killPhase: "ready"}
	model := &durableControlledModel{}
	initial := []providers.ChatMessage{{Role: "user", Content: "Never infer while the ready checkpoint keeps crashing."}}
	runner := &agent.StreamRunner{Client: model, Model: "durable-crash-budget", DriverBindingStore: store, DriverCheckpointStore: barrier}
	attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for generation := 1; generation <= 7; generation++ {
		acquire, stop, pid := h.start(t, generation)
		runner.AcquireLoopDriver = acquire
		barrier.pid = pid
		barrier.killed = false
		_, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: fmt.Sprintf("crash-%d", generation)}), initial, nil)
		stop()
		if err == nil || model.calls != 0 {
			t.Fatalf("generation %d: model=%d err=%v", generation, model.calls, err)
		}
		if generation <= 6 && !barrier.killed {
			t.Fatalf("generation %d did not reach ready crash: %v", generation, err)
		}
		if generation == 7 && (barrier.killed || !strings.Contains(err.Error(), "restart_limit")) {
			t.Fatalf("budget did not fence seventh process: killed=%v err=%v", barrier.killed, err)
		}
	}
	h.write(t, "no-progress-result.json", map[string]any{"processes": 7, "crashes": 6, "model_calls": model.calls, "terminal_error": "restart_limit"})
}
