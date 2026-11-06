package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Admission failures to guard: an active steer is lost or inserted twice,
// a later follow-up resumes a terminal run, and an explicit host continue is
// mistaken for an automatic restart or requires fabricating another user input.
type durableInboxModel struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
	entered  chan struct{}
	release  chan struct{}
}

func (m *durableInboxModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (m *durableInboxModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	n := len(m.requests)
	m.mu.Unlock()
	ch := make(chan providers.StreamEvent)
	go func() {
		defer close(ch)
		if n == 1 {
			close(m.entered)
			select {
			case <-m.release:
			case <-ctx.Done():
				return
			}
		}
		for _, e := range []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "Inbox answer."}, {Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 7, OutputTokens: 3}}, {Type: providers.EventDone, FinishReason: providers.FinishReasonStop}} {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
func (m *durableInboxModel) snapshots() []providers.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]providers.ChatRequest(nil), m.requests...)
}
func durableInboxEvidence(t *testing.T, j *durableControlJourney, m *durableInboxModel, name string) {
	t.Helper()
	records, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
		raw, err := json.MarshalIndent(map[string]any{"scenario": name, "model_requests": len(m.snapshots()), "history": records, "tasks": j.tasks(t), "events": j.out.String()}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDurableCloudInbox(t *testing.T) {
	m := &durableInboxModel{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(m.release) })
	defer release()
	j := newDurableControlJourney(t, "inbox-admission", m)
	started := durableIngressCall(t, j.server, j.out, j.ctx, "start", "turn/start", map[string]string{"thread_id": j.id, "prompt": "initial request", "client_id": "initial-client"})
	if started["error"] != nil {
		t.Fatal(started)
	}
	turn := remarshal[TurnStartResult](t, started["result"]).Turn.ID
	select {
	case <-m.entered:
	case <-j.ctx.Done():
		t.Fatal("model admission barrier not reached")
	}
	secondOut := &lockedBuffer{}
	second := New(j.rt, secondOut)
	defer second.Close()
	if response := durableIngressCall(t, second, secondOut, j.ctx, "attach", "thread/resume", map[string]string{"session_id": j.id}); response["error"] != nil {
		t.Fatal(response)
	}
	steer := map[string]string{"thread_id": j.id, "expected_turn_id": turn, "prompt": "steer-visible-once", "client_id": "steer-client"}
	for _, id := range []string{"steer", "duplicate-steer"} {
		if response := durableIngressCall(t, j.server, j.out, j.ctx, id, "turn/steer", steer); response["error"] != nil {
			t.Fatal(response)
		}
	}
	if len(m.snapshots()) != 1 {
		t.Fatal("steer started inference while the original model was blocked")
	}
	release()
	waitUntil(t, 15*time.Second, func() bool { return len(m.snapshots()) >= 2 })
	waitForThreadLeaseRelease(t, j.rt.Sessions, j.rt.SessionDir, j.id)
	requests := m.snapshots()
	if len(requests) != 2 {
		t.Fatalf("steer inference count=%d", len(requests))
	}
	count := 0
	for _, msg := range requests[1].Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "steer-visible-once") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("steer in next model context=%d", count)
	}
	records, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, msg := range records {
		if msg.ClientID == "steer-client" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("steer canonical history count=%d", count)
	}
	if response := durableIngressCall(t, j.server, j.out, j.ctx, "followup", "turn/start", map[string]string{"thread_id": j.id, "prompt": "later followup", "client_id": "followup-client"}); response["error"] != nil {
		t.Fatal(response)
	}
	waitUntil(t, 15*time.Second, func() bool { return len(m.snapshots()) >= 3 })
	waitForThreadLeaseRelease(t, j.rt.Sessions, j.rt.SessionDir, j.id)
	if len(m.snapshots()) != 3 {
		t.Fatal("followup duplicated inference")
	}
	completedRuns, rounds, inputTokens, outputTokens, runInputs, runOutputs := 0, 0, 0, 0, 0, 0
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["kind"] == "run" && task["status"] == "completed" {
			completedRuns++
		}
		if task["kind"] == "run" || task["kind"] == "model_round" {
			usage, ok := task["usage"].(map[string]any)
			if !ok || task["usage_status"] != "available" {
				t.Fatalf("host usage missing from committed task: %v", task)
			}
			if task["kind"] == "model_round" {
				rounds++
				inputTokens += int(usage["input_tokens"].(float64))
				outputTokens += int(usage["output_tokens"].(float64))
			} else {
				runInputs += int(usage["input_tokens"].(float64))
				runOutputs += int(usage["output_tokens"].(float64))
			}
		}
	}
	if completedRuns < 2 {
		t.Fatalf("postanswer followup did not create a new logical run: %v", j.tasks(t))
	}
	if rounds != 3 || inputTokens != 21 || outputTokens != 9 || runInputs != inputTokens || runOutputs != outputTokens {
		t.Fatalf("usage duplicated/lost: rounds=%d model=%d/%d run=%d/%d", rounds, inputTokens, outputTokens, runInputs, runOutputs)
	}
	durableInboxEvidence(t, j, m, "cloud-inbox")
}
func TestDurableCloudAbortContinue(t *testing.T) {
	m := &durableInboxModel{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(m.release)
	j := newDurableControlJourney(t, "continue-admission", m)
	started := durableIngressCall(t, j.server, j.out, j.ctx, "start", "turn/start", map[string]string{"thread_id": j.id, "prompt": "original request", "client_id": "only-user-input"})
	if started["error"] != nil {
		t.Fatal(started)
	}
	source := remarshal[TurnStartResult](t, started["result"]).Turn.ID
	select {
	case <-m.entered:
	case <-j.ctx.Done():
		t.Fatal("model admission barrier not reached")
	}
	if response := durableIngressCall(t, j.server, j.out, j.ctx, "stop", "turn/interrupt", map[string]string{"thread_id": j.id}); response["error"] != nil {
		t.Fatal(response)
	}
	waitForThreadLeaseRelease(t, j.rt.Sessions, j.rt.SessionDir, j.id)
	if len(m.snapshots()) != 1 {
		t.Fatal("abort auto restarted inference")
	}
	if response := durableIngressCall(t, j.server, j.out, j.ctx, "continue", "turn/start", map[string]string{"thread_id": j.id, "resume_after_turn_id": source}); response["error"] != nil {
		t.Fatal(response)
	}
	waitForThreadLeaseRelease(t, j.rt.Sessions, j.rt.SessionDir, j.id)
	j.completed(t, 1)
	if len(m.snapshots()) != 2 {
		t.Fatalf("explicit continue model count=%d; events=%s", len(m.snapshots()), j.out.String())
	}
	records, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range records {
		if record.Role == "user" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("continue fabricated/duplicated user input: %d", count)
	}
	aborted := 0
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["status"] == "aborted" {
			aborted++
			if task["duration_ms"] == nil || task["usage_status"] != "unavailable" {
				t.Fatalf("interrupted task hides duration or missing usage: %v", task)
			}
		}
	}
	if aborted != 2 {
		t.Fatalf("expected interrupted run and model task, got %d", aborted)
	}
	durableInboxEvidence(t, j, m, "cloud-abort-continue")
}
