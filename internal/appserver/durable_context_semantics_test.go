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

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Recovery contracts: reset must end its run without replaying its old tools,
// handoff must continue with only the note, and neither may erase audit history.
// Capture requests at the real selected-model boundary, not plugin internals.
type durableContextModel struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
	next     *providers.ToolCall
}

// Failure/recovery cases are exercised through the installed plugin: a blocking
// summary failure must prevent inference, overflow gets at most one compaction
// retry, and background summarization must let the next real model request run.
type durableCompactionModel struct {
	mu                                            sync.Mutex
	requests, summaries                           int
	failSummary                                   bool
	overflowFrom, overflowCount                   int
	summaryStarted, releaseSummary, modelAdvanced chan struct{}
}

func (m *durableCompactionModel) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	m.mu.Lock()
	m.summaries++
	started, release := m.summaryStarted, m.releaseSummary
	m.summaryStarted = nil
	fail := m.failSummary
	m.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return providers.ChatResponse{}, ctx.Err()
		case <-release:
		}
	}
	if fail {
		return providers.ChatResponse{}, &providers.HTTPError{StatusCode: 400, Body: `{"error":{"message":"summary refused"}}`}
	}
	return providers.ChatResponse{Content: "Preserve the original decision and continue the latest task.", FinishReason: providers.FinishReasonStop}, nil
}

func (m *durableCompactionModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if len(request.Tools) == 0 {
		response, err := m.Chat(ctx, request)
		if err != nil {
			return nil, err
		}
		ch := make(chan providers.StreamEvent, 2)
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: response.Content}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: response.FinishReason}
		close(ch)
		return ch, nil
	}
	m.mu.Lock()
	m.requests++
	count := m.requests
	overflow := m.overflowFrom > 0 && count >= m.overflowFrom && m.overflowCount > 0
	if overflow {
		m.overflowCount--
	}
	advanced := m.modelAdvanced
	if count == 2 {
		m.modelAdvanced = nil
	}
	m.mu.Unlock()
	if count == 2 && advanced != nil {
		close(advanced)
	}
	if overflow {
		return nil, &providers.HTTPError{StatusCode: 400, ContextOverflow: true, Body: `{"error":{"code":"context_length_exceeded","message":"input too long"}}`}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Model answered."}
	ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	close(ch)
	return ch, nil
}

func TestDurableCloudAutomaticCompaction(t *testing.T) {
	for _, scenario := range []string{"threshold-failure", "overflow-once", "overflow-repeated", "background"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("WUU_DURABLE_COMPACTION_BACKGROUND_TOKENS", "0")
			model := &durableCompactionModel{}
			if scenario == "threshold-failure" {
				model.failSummary = true
			}
			if scenario == "overflow-once" {
				model.overflowFrom, model.overflowCount = 3, 1
			}
			if scenario == "overflow-repeated" {
				model.overflowFrom, model.overflowCount = 3, 2
			}
			if scenario == "background" {
				t.Setenv("WUU_DURABLE_COMPACTION_BACKGROUND_TOKENS", "2000000")
				model.summaryStarted, model.releaseSummary, model.modelAdvanced = make(chan struct{}), make(chan struct{}), make(chan struct{})
			}
			j := newConfiguredDurableControlJourney(t, "auto-compact-"+scenario, func(cfg *config.Config) {
				cfg.Agent.CompactKeepRecentTokens = 64
				if scenario == "threshold-failure" {
					provider := cfg.Providers["fixture"]
					provider.ContextWindow = 32000
					cfg.Providers["fixture"] = provider
				}
				if scenario == "background" {
					provider := cfg.Providers["fixture"]
					provider.ContextWindow = 128000
					cfg.Providers["fixture"] = provider
				}
			}, model)
			first := "Remember the first decision."
			if scenario == "threshold-failure" {
				first = strings.Repeat("A long original decision. ", 10000)
			}
			if scenario == "background" {
				first = strings.Repeat("Preserve this decision. ", 1000)
			}
			j.start(t, "", nil, first)
			j.completed(t, 1)
			if strings.HasPrefix(scenario, "overflow") {
				j.start(t, "", nil, "Remember the second decision.")
				j.completed(t, 2)
			}
			model.mu.Lock()
			started, release, advanced := model.summaryStarted, model.releaseSummary, model.modelAdvanced
			model.mu.Unlock()
			j.start(t, "", nil, "Now continue the task.")
			if scenario == "background" {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatalf("background summary did not start: tasks=%+v events=%s", j.tasks(t), j.out.String())
				}
				select {
				case <-advanced:
				case <-j.ctx.Done():
					t.Fatal("summary held model request gate")
				}
				close(release)
			}
			wantRequests := 4
			if scenario == "threshold-failure" {
				wantRequests = 1
			}
			if scenario == "background" {
				wantRequests = 2
			}
			deadline := time.Now().Add(10 * time.Second)
			terminal := false
			for time.Now().Before(deadline) {
				for _, value := range j.tasks(t) {
					task := value.(map[string]any)
					if task["kind"] == "run" && task["status"] == "failed" {
						terminal = true
					}
				}
				if scenario == "overflow-once" {
					terminal = turnCompletedCountForThread(t, j.out, j.id) >= 3
				}
				if scenario == "background" {
					terminal = turnCompletedCountForThread(t, j.out, j.id) >= 2
				}
				if terminal {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			model.mu.Lock()
			requests, summaries := model.requests, model.summaries
			model.mu.Unlock()
			if !terminal || requests != wantRequests || summaries != 1 {
				t.Fatalf("%s terminal=%v model=%d want=%d summaries=%d events=%s", scenario, terminal, requests, wantRequests, summaries, j.out.String())
			}
			writeDurableContextEvidence(t, j, "cloud-automatic-compaction-"+scenario, requests, summaries)
		})
	}
}

func (m *durableContextModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "Preserved decisions and remaining work."}, nil
}

func (m *durableContextModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.requests = append(m.requests, request)
	call := m.next
	m.next = nil
	m.mu.Unlock()
	ch := make(chan providers.StreamEvent, 3)
	if call != nil {
		ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Finished the current task."}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(ch)
	return ch, nil
}

func TestDurableCloudContextRunSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, action, note string
		requests           int
	}{
		{name: "empty-reset", action: "reset", requests: 2},
		{name: "reset-with-note", action: "reset", note: "Continue only from this note.", requests: 2},
		{name: "handoff", action: "handoff", note: "Continue only from this note.", requests: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &durableContextModel{}
			j := newDurableControlJourney(t, "context-semantics-"+tc.name, model)
			j.start(t, "", nil, "Old conversation must remain only in the archive.")
			j.completed(t, 1)
			args, err := json.Marshal(map[string]string{"action": tc.action, "note": tc.note})
			if err != nil {
				t.Fatal(err)
			}
			model.mu.Lock()
			model.next = &providers.ToolCall{ID: "context-control", Name: j.names["durable_context"], Arguments: string(args)}
			model.mu.Unlock()
			j.start(t, "", nil, "This triggering input must not survive the context change.")
			j.completed(t, 2)
			model.mu.Lock()
			count := len(model.requests)
			model.mu.Unlock()
			if count != tc.requests {
				t.Fatalf("%s issued %d model requests, want %d: %s", tc.action, count, tc.requests, j.out.String())
			}
			if tc.action == "reset" {
				found := false
				for _, value := range j.tasks(t) {
					task := value.(map[string]any)
					outcome, _ := task["outcome"].(map[string]any)
					if task["kind"] == "run" && outcome["status"] == "unanswered" && outcome["reason"] == "reset" {
						found = true
					}
				}
				if !found {
					t.Fatalf("reset did not truthfully settle its logical run: %s", j.out.String())
				}
				j.start(t, "", nil, "A new task after reset.")
				j.completed(t, 3)
			}
			model.mu.Lock()
			request := model.requests[len(model.requests)-1]
			model.mu.Unlock()
			noteFound := false
			for _, message := range request.Messages {
				if message.Role == "system" {
					continue
				}
				if message.Content == "Old conversation must remain only in the archive." || message.Content == "This triggering input must not survive the context change." || len(message.ToolCalls) != 0 || message.Role == "tool" {
					t.Fatalf("old context leaked into %s model request: %+v", tc.action, request.Messages)
				}
				noteFound = noteFound || (tc.note != "" && message.Content == tc.note)
			}
			if noteFound != (tc.note != "") {
				t.Fatalf("handoff note lost: %+v", request.Messages)
			}
			history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			oldFound, callFound, resultFound := false, false, false
			for _, record := range history {
				oldFound = oldFound || record.Content == "Old conversation must remain only in the archive."
				callFound = callFound || len(record.ToolCalls) > 0
				resultFound = resultFound || record.ToolCallID == "context-control"
			}
			if !oldFound || !callFound || !resultFound {
				t.Fatalf("context replacement removed physical history: %+v", history)
			}
			model.mu.Lock()
			requests := len(model.requests)
			model.mu.Unlock()
			writeDurableContextEvidence(t, j, "cloud-context-semantics-"+tc.name, requests, 0)
		})
	}
}

func writeDurableContextEvidence(t *testing.T, j *durableControlJourney, name string, requests, summaries int) {
	t.Helper()
	j.evidence(t, name)
	dir := os.Getenv("WUU_DURABLE_EVIDENCE")
	if dir == "" {
		return
	}
	path := filepath.Join(dir, name+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]any
	if err = json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence["model_requests"], evidence["summary_requests"] = requests, summaries
	raw, err = json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
