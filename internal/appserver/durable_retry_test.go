package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Recovery failures to guard before implementation: provider failures retry in
// memory instead of releasing the turn; reopen samples before the saved deadline;
// an overdue retry is lost during host downtime; retry creates another logical
// run or duplicates the original user input; known usage is charged twice.
// The provider stays unavailable until the persisted sleep is observed, so host
// transport retries cannot accidentally satisfy this durable-retry acceptance.
type durableRetryModel struct {
	unavailable atomic.Bool
	mu          sync.Mutex
	requests    []providers.ChatRequest
	times       []time.Time
	next        *providers.ToolCall
}

func (*durableRetryModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "Retry context summary."}, nil
}

func (m *durableRetryModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.times = append(m.times, time.Now())
	call := m.next
	m.next = nil
	m.mu.Unlock()
	if m.unavailable.Load() {
		return nil, &providers.HTTPError{StatusCode: 503, ProviderCode: "temporary_unavailable", Body: "controlled temporary outage"}
	}
	ch := make(chan providers.StreamEvent, 3)
	if call != nil {
		ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
		close(ch)
		return ch, nil
	}

	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "The durable retry recovered."}
	ch <- providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 7, OutputTokens: 3}}
	ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	close(ch)
	return ch, nil
}

func (m *durableRetryModel) observations() ([]providers.ChatRequest, []time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]providers.ChatRequest(nil), m.requests...), append([]time.Time(nil), m.times...)
}

func TestDurableCloudModelRetry(t *testing.T) {
	for _, mode := range []string{"automatic", "restart-before-deadline", "restart-overdue", "same-text-user", "cas-conflict"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WUU_DURABLE_MODEL_RETRIES", "2")
			t.Setenv("WUU_DURABLE_RETRY_BASE_MS", "4000")
			t.Setenv("WUU_DURABLE_RETRY_MAX_MS", "4000")
			originalPrompt := "Recover this original request"
			if mode == "same-text-user" {
				originalPrompt = "Durable sleep finished. Continue the pending task."
			}
			conflictMarker := ""
			if mode == "cas-conflict" {
				conflictMarker = durableRetryConflictFixture(t)
			}
			m := &durableRetryModel{}
			m.unavailable.Store(true)
			j := newDurableControlJourney(t, "model-retry-"+mode, m)
			started := durableIngressCall(t, j.server, j.out, j.ctx, "retry-start", "turn/start", map[string]string{
				"thread_id": j.id, "prompt": originalPrompt, "client_id": "original-retry-input",
			})
			if started["error"] != nil {
				t.Fatal(started)
			}
			j.completed(t, 1)
			checkpoint, exists, err := j.rt.Sessions.LoadDriverCheckpoint(j.id)
			if err != nil || !exists {
				t.Fatalf("retry checkpoint: %v, exists=%v", err, exists)
			}
			var state struct {
				RunID        string `json:"run_id"`
				Phase        string `json:"phase"`
				RetryAt      string `json:"retry_at"`
				ModelAttempt int    `json:"model_attempt"`
				Round        int    `json:"round"`
			}
			if err = json.Unmarshal(checkpoint.State, &state); err != nil {
				t.Fatal(err)
			}
			deadline, err := time.Parse(time.RFC3339Nano, state.RetryAt)
			if err != nil || state.Phase != "sleeping" || state.RunID == "" {
				t.Fatalf("provider failure did not persist a finite retry: %+v, %v; events=%s", state, err, j.out.String())
			}
			before, _ := m.observations()
			if len(before) != 1 {
				t.Fatalf("host retry loop multiplied durable attempts: %d requests", len(before))
			}
			m.unavailable.Store(false)
			completed := 2
			if strings.HasPrefix(mode, "restart-") {
				j.server.Close()
				if err = j.closeRuntime(); err != nil {
					t.Fatal(err)
				}
				if mode == "restart-overdue" {
					if delay := time.Until(deadline.Add(100 * time.Millisecond)); delay > 0 {
						timer := time.NewTimer(delay)
						defer timer.Stop()
						select {
						case <-timer.C:
						case <-j.ctx.Done():
							t.Fatal(j.ctx.Err())
						}
					}
				}
				if requests, _ := m.observations(); len(requests) != len(before) {
					t.Fatal("closed host continued inference")
				}
				reopened, openErr := runtime.NewSession(j.options)
				if openErr != nil {
					t.Fatal(openErr)
				}
				t.Cleanup(func() { reopened.Cleanup() })
				j.rt = reopened
				j.out = &lockedBuffer{}
				j.server = New(reopened, j.out)
				t.Cleanup(j.server.Close)
				completed = 1
			}
			j.completed(t, completed)
			waitForThreadLeaseRelease(t, j.rt.Sessions, j.rt.SessionDir, j.id)
			if conflictMarker != "" {
				if _, err := os.Stat(conflictMarker); err != nil {
					t.Fatalf("selected document conflict barrier not reached: %v", err)
				}
			}
			requests, times := m.observations()
			if len(requests) != len(before)+1 || times[len(before)].Before(deadline) {
				t.Fatalf("retry duplicated or ignored deadline: before=%d total=%d deadline=%s requests=%v", len(before), len(requests), deadline, times)
			}
			wakeID := pluginSessionRequestClientID("example.durable-driver", fmt.Sprintf("%s:retry:%d:result", state.RunID, state.Round))
			original := 0
			for _, message := range requests[len(before)].Messages {
				if message.Role == "user" && message.Content == originalPrompt {
					original++
				}
			}
			if original != 1 {
				t.Fatalf("retry model context contains %d original inputs", original)
			}
			history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			original = 0
			wakes := 0
			wakeContent := ""
			for _, message := range history {
				if message.ClientID == wakeID {
					wakes++
					wakeContent = message.Content
				}
				if message.ClientID == "original-retry-input" {
					original++
				}
			}
			if wakes != 1 {
				t.Fatalf("internal retry wake must remain in physical history once, got %d", wakes)
			}
			if wakeContent == "" {
				t.Fatal("retry wake history content missing")
			}
			wakeTextCount := 0
			for _, message := range requests[len(before)].Messages {
				wakeTextCount += strings.Count(message.Content, wakeContent)
				if originalPrompt != wakeContent && strings.Contains(message.Content, wakeContent) {
					t.Fatal("host-admitted internal retry wake leaked into retry model context")
				}
			}
			if originalPrompt == wakeContent && wakeTextCount != 1 {
				t.Fatalf("same-text human input must remain once, got %d", wakeTextCount)
			}
			if original != 1 {
				t.Fatalf("retry persisted %d copies of original input", original)
			}
			runs, failed, succeeded := 0, 0, 0
			tasks := durableRetryTerminalTasks(t, j, state.RunID, "completed")
			for _, raw := range tasks {
				task := raw.(map[string]any)
				if task["kind"] == "run" {
					runs++
					if task["id"] != state.RunID || task["status"] != "completed" {
						t.Fatalf("retry replaced or failed original logical run: %v", task)
					}
					usage, _ := task["usage"].(map[string]any)
					if usage["input_tokens"] != float64(7) || usage["output_tokens"] != float64(3) {
						t.Fatalf("known retry usage duplicated or lost: %v", task)
					}
				}
				if task["kind"] == "model_round" {
					if task["status"] == "failed" || task["status"] == "aborted" {
						failed++
					}
					if task["status"] == "completed" {
						succeeded++
					}
				}
			}
			if runs != 1 || failed != 1 || succeeded != 1 {
				t.Fatalf("retry task lifecycle: runs=%d failed=%d succeeded=%d tasks=%v", runs, failed, succeeded, tasks)
			}
			if evidence := os.Getenv("WUU_DURABLE_EVIDENCE"); evidence != "" {
				if err = os.MkdirAll(evidence, 0700); err != nil {
					t.Fatal(err)
				}
				raw, marshalErr := json.MarshalIndent(map[string]any{
					"scenario": "cloud-model-retry-" + mode, "checkpoint_before": state,
					"request_times": times, "original_input_count": original, "tasks": tasks,
					"events": j.out.String(), "host_reopened": strings.HasPrefix(mode, "restart-"),
				}, "", "  ")
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if err = os.WriteFile(filepath.Join(evidence, "cloud-model-retry-"+mode+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Physical completion precedes asynchronous owned-task cleanup. Observe the
// public terminal state instead of treating a released turn as a settled run.
func durableRetryTerminalTasks(t *testing.T, j *durableControlJourney, id, status string) []any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var tasks []any
	for time.Now().Before(deadline) {
		tasks = j.tasks(t)
		for _, raw := range tasks {
			task := raw.(map[string]any)
			if task["id"] == id && task["status"] == status {
				return tasks
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %q did not reach %q: %v", id, status, tasks)
	return nil
}

// A finite backoff is owned work: explicit cancellation fences its wake, while
// repeated provider failure consumes the persisted budget instead of resetting
// it on each physical turn or host restart.
func TestDurableCloudModelRetryCancellationAndBudget(t *testing.T) {
	for _, mode := range []string{"cancel", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WUU_DURABLE_MODEL_RETRIES", "2")
			backoff := "200"
			if mode == "cancel" {
				backoff = "5000"
			}
			t.Setenv("WUU_DURABLE_RETRY_BASE_MS", backoff)
			t.Setenv("WUU_DURABLE_RETRY_MAX_MS", backoff)
			m := &durableRetryModel{}
			m.unavailable.Store(true)
			j := newDurableControlJourney(t, "model-retry-"+mode, m)
			j.start(t, "", nil, "Request with a bounded retry budget")
			j.completed(t, 1)
			checkpoint, exists, err := j.rt.Sessions.LoadDriverCheckpoint(j.id)
			if err != nil || !exists {
				t.Fatalf("retry checkpoint missing: %v", err)
			}
			var state struct {
				RunID   string `json:"run_id"`
				RetryAt string `json:"retry_at"`
			}
			if err = json.Unmarshal(checkpoint.State, &state); err != nil || state.RunID == "" {
				t.Fatalf("invalid retry checkpoint: %+v, %v", state, err)
			}
			terminal := "failed"
			if mode == "cancel" {
				m.unavailable.Store(false)
				args, err := json.Marshal(map[string]string{"run_id": state.RunID})
				if err != nil {
					t.Fatal(err)
				}
				m.mu.Lock()
				m.next = &providers.ToolCall{ID: "cancel-retry", Name: j.names["durable_cancel"], Arguments: string(args)}
				m.mu.Unlock()
				j.start(t, "", nil, "Cancel the sleeping retry now")
				terminal = "aborted"
			}
			tasks := durableRetryTerminalTasks(t, j, state.RunID, terminal)
			before, _ := m.observations()
			if mode == "cancel" {
				found := false
				for _, request := range before[1:] {
					for _, message := range request.Messages {
						if message.Role == "user" && message.Content == "Cancel the sleeping retry now" {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("new human input was removed while retry was sleeping")
				}
			}
			if mode == "exhausted" && len(before) != 3 {
				t.Fatalf("bounded retries produced %d attempts, want 3", len(before))
			}
			j.server.Close()
			if err = j.closeRuntime(); err != nil {
				t.Fatal(err)
			}
			reopened, err := runtime.NewSession(j.options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Cleanup() })
			j.rt = reopened
			j.out = &lockedBuffer{}
			j.server = New(reopened, j.out)
			t.Cleanup(j.server.Close)
			deadline := time.Now().Add(1500 * time.Millisecond)
			if retryAt, err := time.Parse(time.RFC3339Nano, state.RetryAt); err == nil && retryAt.After(deadline) {
				deadline = retryAt.Add(1500 * time.Millisecond)
			}
			for time.Now().Before(deadline) {
				if requests, _ := m.observations(); len(requests) != len(before) {
					t.Fatalf("terminal retry resumed after restart: %d to %d", len(before), len(requests))
				}
				time.Sleep(20 * time.Millisecond)
			}
			if evidence := os.Getenv("WUU_DURABLE_EVIDENCE"); evidence != "" {
				if err = os.MkdirAll(evidence, 0700); err != nil {
					t.Fatal(err)
				}
				raw, err := json.MarshalIndent(map[string]any{"scenario": "cloud-model-retry-" + mode, "attempts": len(before), "tasks": tasks, "host_reopened": true, "checkpoint_before": state}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(evidence, "cloud-model-retry-"+mode+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// This independent package wraps the selected public document adapter, not
// inference. It advances the real CAS revision with the unchanged canonical
// document, so the first success projection is rejected without committing it.
func durableRetryConflictFixture(t *testing.T) string {
	t.Helper()
	source := os.Getenv("WUU_DURABLE_FIXTURE")
	if source == "" {
		t.Skip("durable plugin fixture required")
	}
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "package.json", "dist/runtime.js"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "dist/runtime.js" {
			marker := filepath.Join(target, "conflict-injected")
			needle := "return await store.commit(transaction);"
			if !strings.Contains(string(data), needle) {
				t.Fatal("selected document commit wrapper target missing")
			}
			injected := fmt.Sprintf(`if (!globalThis.__durableConflictInjected && doc.tasks.some(t => t.kind === "model_round" && t.status === "completed" && t.usage?.input_tokens === 7)) {
 globalThis.__durableConflictInjected = true;
 for (let conflictAttempt=0;;conflictAttempt++) {
  const prior=await store.read({namespace:NAMESPACE,keys:[session_id],limit:1});
  try {
   await store.commit({id:randomUUID(),expected_revision:prior.revision,writes:[{namespace:NAMESPACE,key:session_id,value:prior.documents[0].value}]});
   (await import("node:fs")).writeFileSync(%q,"selected CAS revision advanced");
   break;
  } catch(e) {if(codeOf(e)!=="state_conflict" || conflictAttempt>=11) throw e;}
 }
}
return await store.commit(transaction);`, marker)
			data = []byte(strings.Replace(string(data), needle, injected, 1))
		}
		if err := os.WriteFile(filepath.Join(target, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("WUU_DURABLE_FIXTURE", target)
	return filepath.Join(target, "conflict-injected")
}
