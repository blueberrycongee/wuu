package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type mockStreamAttempt struct {
	events []providers.StreamEvent
	err    error
}

type mockStreamClient struct {
	events        []providers.StreamEvent
	attempts      []mockStreamAttempt
	chatResponses []providers.ChatResponse
	chatErrs      []error
	requests      []providers.ChatRequest
	callCount     int
	chatCallCount int
}

type streamOnlyNoteClient struct {
	chatCalls   int
	streamCalls int
	request     providers.ChatRequest
}

type cancelDuringNoteClient struct {
	mu          sync.Mutex
	streamCalls int
	noteStarted chan struct{}
	noteOnce    sync.Once
	releaseNote chan struct{}
}

func (c *cancelDuringNoteClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unary chat is unsupported")
}

func (c *cancelDuringNoteClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.streamCalls++
	c.mu.Unlock()
	isNote := false
	for _, message := range req.Messages {
		if message.Cause == "compaction_note_fork" {
			isNote = true
			break
		}
	}
	if !isNote {
		events := make(chan providers.StreamEvent, 2)
		events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "main response"}
		events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
		close(events)
		return events, nil
	}
	c.noteOnce.Do(func() { close(c.noteStarted) })
	events := make(chan providers.StreamEvent, 2)
	go func() {
		select {
		case <-c.releaseNote:
			events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "# Updated note"}
			events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
		case <-ctx.Done():
			events <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("stream request failed: %w", ctx.Err())}
		}
		close(events)
	}()
	return events, nil
}

type cancellationNoteProvider struct{}

func (cancellationNoteProvider) CompactionKey() string   { return "cancel-note" }
func (cancellationNoteProvider) CompactionPriority() int { return 10 }
func (cancellationNoteProvider) CompactionNotesEnabled() bool {
	return true
}
func (cancellationNoteProvider) Compact(_ context.Context, _ string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return messages, nil
}
func (cancellationNoteProvider) PlanCompactionNote(_ context.Context, _ string, _, _ []providers.ChatMessage, _ CompactionNote) (CompactionNotePlan, error) {
	return CompactionNotePlan{Prompt: "update the note", MaxBytes: 24_000}, nil
}
func (cancellationNoteProvider) PlanHandoffBrief(_ context.Context, _ string, _ []providers.ChatMessage, _ CompactionNote, _, _ string, _ int) (CompactionNotePlan, error) {
	return CompactionNotePlan{Prompt: "write the brief", MaxBytes: 24_000}, nil
}
func (cancellationNoteProvider) CompactWithNote(_ context.Context, _ string, messages []providers.ChatMessage, _ CompactionNote) (CompactionNoteReplacement, error) {
	return CompactionNoteReplacement{Messages: messages, CoveredMessages: len(messages)}, nil
}

type replacementNoteProvider struct{ cancellationNoteProvider }

func (replacementNoteProvider) CompactWithNote(_ context.Context, _ string, _ []providers.ChatMessage, note CompactionNote) (CompactionNoteReplacement, error) {
	return CompactionNoteReplacement{
		Messages: []providers.ChatMessage{{
			Role:    "system",
			Content: compact.BuildSummaryContent(note.Markdown),
			Hidden:  true,
		}},
		CoveredMessages: 1,
	}, nil
}

type cancellationNoteStore struct {
	mu         sync.Mutex
	note       CompactionNote
	storeCalls int
	stored     chan CompactionNote
}

func (s *cancellationNoteStore) LoadCompactionNote(context.Context, string) (CompactionNote, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.note, true, nil
}

func (s *cancellationNoteStore) StoreCompactionNote(_ context.Context, _ string, note CompactionNote) error {
	s.mu.Lock()
	s.storeCalls++
	s.note = note
	stored := s.stored
	s.mu.Unlock()
	if stored != nil {
		stored <- note
	}
	return nil
}

func (c *streamOnlyNoteClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	c.chatCalls++
	return providers.ChatResponse{}, errors.New("unary chat is unsupported")
}

func (c *streamOnlyNoteClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.streamCalls++
	c.request = req
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	usage := &providers.TokenUsage{InputTokens: 12, OutputTokens: 4}
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "# Context note\n\nContinue the task."}
	events <- providers.StreamEvent{Type: providers.EventDone, Usage: usage, FinishReason: providers.FinishReasonStop}
	close(events)
	return events, nil
}

func TestCompactionNoteForkUsesStreamingProviderPath(t *testing.T) {
	client := &streamOnlyNoteClient{}
	runner := &StreamRunner{Client: client, ProviderName: "test", Model: "test-model"}
	result, err := runner.compactionNoteFork(nil)(context.Background(), []providers.ChatMessage{{Seq: 42, Role: "user", Content: "history"}}, CompactionNotePlan{
		Prompt: "write the note", MaxBytes: 24_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.chatCalls != 0 || client.streamCalls != 1 {
		t.Fatalf("provider calls: unary=%d stream=%d", client.chatCalls, client.streamCalls)
	}
	if !strings.Contains(result.Markdown, "Context note") || result.Usage == nil || result.Usage.OutputTokens != 4 {
		t.Fatalf("result = %+v", result)
	}
	if len(client.request.Messages) == 0 || !strings.HasPrefix(client.request.Messages[0].Content, "[History Seq 42]\n") {
		t.Fatalf("note fork did not expose stable history address: %+v", client.request.Messages)
	}
}

func TestStreamRunnerNoteCompactionEmitsOneCompletedEvent(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("older request ", 100)},
		{Role: "assistant", Content: strings.Repeat("older response ", 100)},
	}
	store := &cancellationNoteStore{note: CompactionNote{
		Markdown:        "# Existing note\n\nContinue the task.",
		CoveredMessages: len(history),
		CoveredHash:     CompactionHistoryHash(history),
	}}
	registry := NewCompactionRegistry()
	registry.Register(replacementNoteProvider{})
	runner := &StreamRunner{
		Client:              &mockStreamClient{},
		Model:               "test-model",
		CompactionRegistry:  registry,
		CompactionNoteStore: store,
		ForceInitialCompact: true,
		CompactOnly:         true,
	}
	var compactEvents []providers.StreamEvent
	result, err := runner.RunWithCallback(context.Background(), history, func(event providers.StreamEvent) {
		if event.Type == providers.EventCompact {
			compactEvents = append(compactEvents, event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HistoryRewritten {
		t.Fatal("expected note compaction to rewrite history")
	}
	if len(compactEvents) != 2 {
		t.Fatalf("compact events = %+v, want one started and one completed event", compactEvents)
	}
	if compactEvents[0].CompactPhase != providers.CompactPhaseStarted || compactEvents[1].CompactPhase != providers.CompactPhaseCompleted {
		t.Fatalf("compact event phases = %q, %q", compactEvents[0].CompactPhase, compactEvents[1].CompactPhase)
	}
	if compactEvents[1].Content == "" || strings.Contains(compactEvents[1].Content, "Context note") {
		t.Fatalf("completed event should contain only the authoritative compaction result: %+v", compactEvents[1])
	}
}

type streamCompactionProvider struct{ calls int }

func (p *streamCompactionProvider) CompactionKey() string   { return "stream-test" }
func (p *streamCompactionProvider) CompactionPriority() int { return 10 }
func (p *streamCompactionProvider) Compact(_ context.Context, _ string, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
	p.calls++
	return []providers.ChatMessage{{Role: "system", Content: "compacted by registry"}}, nil
}

func TestStreamRunnerUsesCompactionRegistry(t *testing.T) {
	provider := &streamCompactionProvider{}
	registry := NewCompactionRegistry()
	registry.Register(provider)
	runner := &StreamRunner{
		Client:              &mockStreamClient{},
		Model:               "test-model",
		CompactionRegistry:  registry,
		ForceInitialCompact: true,
		CompactOnly:         true,
	}
	result, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: strings.Repeat("old history ", 100)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || !result.HistoryRewritten {
		t.Fatalf("calls = %d history_rewritten = %v", provider.calls, result.HistoryRewritten)
	}
}

func (m *mockStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	m.requests = append(m.requests, req)
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "unary"})
	}
	idx := m.chatCallCount
	m.chatCallCount++
	if idx < len(m.chatErrs) && m.chatErrs[idx] != nil {
		return providers.ChatResponse{}, m.chatErrs[idx]
	}
	if idx < len(m.chatResponses) {
		return m.chatResponses[idx], nil
	}
	return providers.ChatResponse{}, nil
}

func (m *mockStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.requests = append(m.requests, req)
	if isCompactSummaryRequest(req) {
		idx := m.chatCallCount
		m.chatCallCount++
		if idx < len(m.chatErrs) && m.chatErrs[idx] != nil {
			return nil, m.chatErrs[idx]
		}
		if req.Attempt.Valid() {
			req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
		}
		ch := make(chan providers.StreamEvent, 2)
		if idx < len(m.chatResponses) {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: m.chatResponses[idx].Content}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone}
		close(ch)
		return ch, nil
	}
	if len(m.attempts) > 0 {
		idx := m.callCount
		m.callCount++
		if idx >= len(m.attempts) {
			return nil, errors.New("unexpected extra stream attempt")
		}
		attempt := m.attempts[idx]
		if attempt.err != nil {
			return nil, attempt.err
		}
		if req.Attempt.Valid() {
			req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
		}
		ch := make(chan providers.StreamEvent, len(attempt.events))
		for _, e := range attempt.events {
			ch <- e
		}
		close(ch)
		return ch, nil
	}
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan providers.StreamEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func isCompactSummaryRequest(req providers.ChatRequest) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "Conversation to summarize") {
			return true
		}
	}
	return false
}

func TestStreamRunnerUsesAPIModelForProviderRequest(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}},
	}
	runner := &StreamRunner{
		Client:   client,
		Model:    "gpt-5.5-fast",
		APIModel: "gpt-5.5",
	}

	if _, err := runner.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	if client.requests[0].Model != "gpt-5.5" {
		t.Fatalf("request model = %q", client.requests[0].Model)
	}
}

func TestStreamRunnerRecordsProviderProvenanceForNativeState(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "ok", ProviderItemID: "msg_1"},
			{Type: providers.EventDone},
		},
	}
	runner := &StreamRunner{
		Client:       client,
		ProviderName: "gateway-a",
		Model:        "shared-model",
	}

	result, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if len(client.requests) != 1 || client.requests[0].Provider != "gateway-a" {
		t.Fatalf("request provider provenance missing: %+v", client.requests)
	}
	if len(result.NewMessages) != 1 {
		t.Fatalf("new messages = %+v", result.NewMessages)
	}
	got := result.NewMessages[0]
	if got.ProviderItemID != "msg_1" || got.ProviderItemProvider != "gateway-a" || got.ProviderItemModel != "shared-model" {
		t.Fatalf("native-state provenance missing: %+v", got)
	}
}

func TestStreamRunner_AfterTurnRunsAfterSuccess(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}},
	}
	called := false
	runner := &StreamRunner{
		Client: client,
		Model:  "test-model",
	}
	runner.AfterTurn = func(_ context.Context, gotRunner *StreamRunner, history []providers.ChatMessage, result LoopResult) {
		called = true
		if gotRunner != runner {
			t.Fatalf("AfterTurn runner mismatch")
		}
		if result.Content != "ok" {
			t.Fatalf("AfterTurn content = %q, want ok", result.Content)
		}
		visible := visibleMessagesForTest(history)
		if len(visible) != 2 || visible[1].Role != "assistant" || visible[1].Content != "ok" {
			t.Fatalf("AfterTurn history = %+v", history)
		}
	}

	if _, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if !called {
		t.Fatal("AfterTurn was not called")
	}
}

func TestStreamRunner_AfterTurnDoesNotRunAfterSetupError(t *testing.T) {
	called := false
	runner := &StreamRunner{
		Model: "test-model",
		AfterTurn: func(context.Context, *StreamRunner, []providers.ChatMessage, LoopResult) {
			called = true
		},
	}

	if _, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("expected missing client error")
	}
	if called {
		t.Fatal("AfterTurn should not run after an error")
	}
}

func TestStreamRunner_SimpleContent(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "Hello "},
			{Type: providers.EventContentDelta, Content: "world"},
			{Type: providers.EventDone},
		},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		OnEvent: func(ev providers.StreamEvent) {
			received = append(received, ev)
		},
	}

	result, err := runner.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "Hello world" {
		t.Fatalf("unexpected result: %q", result)
	}

	if len(received) != 7 {
		t.Fatalf("expected 7 events including lifecycle/context/message, got %d", len(received))
	}
	if received[0].Type != providers.EventRequestContext || received[0].RequestContext == nil {
		t.Fatalf("expected request context event, got %+v", received[0])
	}
	if received[1].Type != providers.EventLifecycle || received[1].Lifecycle == nil || received[1].Lifecycle.Phase != providers.StreamPhaseConnecting {
		t.Fatalf("unexpected first lifecycle event: %+v", received[1])
	}
	if received[2].Type != providers.EventLifecycle || received[2].Lifecycle == nil || received[2].Lifecycle.Phase != providers.StreamPhaseConnected {
		t.Fatalf("unexpected second lifecycle event: %+v", received[2])
	}
	if received[3].Type != providers.EventContentDelta || received[3].Content != "Hello " {
		t.Fatalf("unexpected first content event: %+v", received[3])
	}
	if received[5].Type != providers.EventDone {
		t.Fatalf("expected done event before committed message, got %s", received[5].Type)
	}
	if received[6].Type != providers.EventMessage || received[6].Message == nil || received[6].Message.Role != "assistant" {
		t.Fatalf("expected committed assistant message event, got %+v", received[6])
	}
}

func TestStreamRunner_EventUsageUpdatesResultUsage(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "ok"},
			{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 11, OutputTokens: 7, CacheCreationTokens: 5, CacheReadTokens: 3}},
			{Type: providers.EventDone},
		},
	}
	runner := &StreamRunner{
		Client: client,
		Model:  "test-model",
	}

	result, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if result.InputTokens != 11 || result.OutputTokens != 7 || result.CacheCreationTokens != 5 || result.CacheReadTokens != 3 {
		t.Fatalf("usage not preserved from EventUsage: %+v", result)
	}
}

func TestStreamRunnerCarriesStreamedMessagePhaseToCommittedMessage(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "Done.", Phase: providers.MessagePhaseFinalAnswer},
			{Type: providers.EventDone},
		},
	}

	var committed *providers.ChatMessage
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventMessage {
				committed = ev.Message
			}
		},
	}

	if _, err := runner.Run(context.Background(), "answer"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if committed == nil {
		t.Fatal("expected committed assistant message")
	}
	if committed.Phase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("committed message phase = %q", committed.Phase)
	}
}

func TestStreamRunner_EmitsTodoUpdateEventAfterUpdateTodo(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{
						Type: providers.EventToolUseStart,
						ToolCall: &providers.ToolCall{
							ID:      "call-todo",
							Name:    "plugin_todo_update_todo_abc123",
							Display: &providers.ToolCallDisplay{Kind: "todo", Text: "Updating TODO", Capability: "todo"},
						},
					},
					{
						Type: providers.EventToolUseEnd,
						ToolCall: &providers.ToolCall{
							ID:        "call-todo",
							Name:      "plugin_todo_update_todo_abc123",
							Arguments: `{"explanation":"start","todos":[{"content":"inspect","status":"completed"},{"content":"report","status":"in_progress"}]}`,
							Display:   &providers.ToolCallDisplay{Kind: "todo", Text: "Updating TODO", Capability: "todo"},
						},
					},
					{Type: providers.EventDone},
				},
			},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "done"},
					{Type: providers.EventDone},
				},
			},
		},
	}
	tools := &fakeLoopTools{
		defs: []providers.ToolDefinition{{Name: "plugin_todo_update_todo_abc123"}},
		results: map[string]string{
			"call-todo": `{"status":"updated"}`,
		},
	}
	var todoUpdate *providers.TodoUpdate
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		Tools:  tools,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventTodoUpdate {
				todoUpdate = ev.TodoUpdate
			}
		},
	}
	result, err := runner.Run(context.Background(), "todo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "done" {
		t.Fatalf("unexpected result: %q", result)
	}
	if todoUpdate == nil || todoUpdate.Explanation != "start" || len(todoUpdate.Todos) != 2 || todoUpdate.Todos[1].Status != "in_progress" {
		t.Fatalf("unexpected TODO update event: %+v", todoUpdate)
	}
}

type displayLoopTools struct {
	fakeLoopTools
	display *providers.ToolCallDisplay
}

func (f *displayLoopTools) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	if call.Name != "read_file" {
		return providers.ToolCallDisplay{}, false
	}
	if f.display != nil {
		return *f.display, true
	}
	return providers.ToolCallDisplay{Kind: "read", Text: "读取 model.go"}, true
}

func TestStreamRunner_EnrichesToolCallDisplay(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{
						Type:     providers.EventToolUseStart,
						ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file"},
					},
					{
						Type: providers.EventToolUseEnd,
						ToolCall: &providers.ToolCall{
							ID:        "call-read",
							Name:      "read_file",
							Arguments: `{"path":"internal/appserver/model.go"}`,
						},
					},
					{Type: providers.EventDone},
				},
			},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "done"},
					{Type: providers.EventDone},
				},
			},
		},
	}
	tools := &displayLoopTools{
		fakeLoopTools: fakeLoopTools{
			defs:    []providers.ToolDefinition{{Name: "read_file"}},
			results: map[string]string{"call-read": `{"ok":true}`},
		},
	}
	var seen []*providers.ToolCall
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		Tools:  tools,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventToolUseEnd && ev.ToolCall != nil {
				seen = append(seen, ev.ToolCall)
			}
		},
	}
	result, err := runner.Run(context.Background(), "inspect")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "done" {
		t.Fatalf("unexpected result: %q", result)
	}
	if len(seen) < 2 {
		t.Fatalf("expected streamed and result tool end events, got %+v", seen)
	}
	for _, call := range seen {
		if call.Display == nil || call.Display.Text != "读取 model.go" || call.Display.Kind != "read" {
			t.Fatalf("expected display metadata on tool event, got %+v", call)
		}
	}
}

func TestStreamRunner_EnrichesLabelOnlyToolDisplay(t *testing.T) {
	want := &providers.ToolCallDisplay{Label: "Working notes", LabelTranslations: map[string]string{"zh-CN": "工作笔记"}}
	tools := &displayLoopTools{display: want}
	got := enrichToolCallDisplay(tools, providers.ToolCall{Name: "read_file"})
	if !reflect.DeepEqual(got.Display, want) {
		t.Fatalf("expected label-only display metadata, got %+v", got.Display)
	}
	got.Display.LabelTranslations["zh-CN"] = "changed"
	if want.LabelTranslations["zh-CN"] == "changed" {
		t.Fatal("enrichment aliased the provider display map")
	}
}

func TestStreamRunnerAnnotatesProviderStateWithStepIndex(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{
						Type: providers.EventProviderState,
						ProviderState: &providers.ProviderStateSummary{
							Provider:   "openai",
							Protocol:   "responses_websocket",
							ReplayMode: "full_request",
						},
					},
					{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file"}},
					{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file", Arguments: `{"path":"README.md"}`}},
					{Type: providers.EventDone},
				},
			},
			{
				events: []providers.StreamEvent{
					{
						Type: providers.EventProviderState,
						ProviderState: &providers.ProviderStateSummary{
							Provider:               "openai",
							Protocol:               "responses_websocket",
							ReplayMode:             "previous_response_id",
							PreviousResponseIDUsed: true,
						},
					},
					{Type: providers.EventContentDelta, Content: "done"},
					{Type: providers.EventDone},
				},
			},
		},
	}
	tools := &fakeLoopTools{
		defs:    []providers.ToolDefinition{{Name: "read_file"}},
		results: map[string]string{"call-read": `{"content":"hi"}`},
	}
	var states []providers.ProviderStateSummary
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		Tools:  tools,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventProviderState && ev.ProviderState != nil {
				states = append(states, *ev.ProviderState)
			}
		},
	}

	result, err := runner.Run(context.Background(), "inspect")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "done" {
		t.Fatalf("unexpected result: %q", result)
	}
	if len(states) != 2 {
		t.Fatalf("provider states = %+v", states)
	}
	if states[0].StepIndex != 0 || states[1].StepIndex != 1 {
		t.Fatalf("provider state step indexes not aligned with model steps: %+v", states)
	}
}

func TestStreamRunner_AllowsNaturalEmptyCompletionWithoutPersistingAssistantMessage(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventDone, StopReason: "end_turn"},
		},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		OnEvent: func(ev providers.StreamEvent) {
			received = append(received, ev)
		},
	}

	result, err := runner.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
	for _, ev := range received {
		if ev.Type == providers.EventMessage {
			t.Fatalf("did not expect persisted assistant message event, got %+v", ev)
		}
	}
}

func TestStreamRunner_NoToolCallsWhenNoneRequested(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "answer"},
			{Type: providers.EventDone},
		},
	}

	tools := &fakeTools{}
	runner := StreamRunner{
		Client: client,
		Tools:  tools,
		Model:  "test-model",
	}

	result, err := runner.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "answer" {
		t.Fatalf("unexpected result: %q", result)
	}
	// Tools should not have been called.
	if len(tools.calls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(tools.calls))
	}
}

func TestStreamRunner_ValidationErrors(t *testing.T) {
	// Run validates blank prompt.
	runner := StreamRunner{Model: "m"}
	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error for nil client")
	}

	client := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventDone}}}
	runner = StreamRunner{Client: client}
	_, err = runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error for empty model")
	}

	runner = StreamRunner{Client: client, Model: "m"}
	_, err = runner.Run(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected error for blank prompt")
	}

	// RunWithCallback validates client and model but not prompt.
	runner = StreamRunner{Model: "m"}
	_, err = runner.RunWithCallback(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil client in RunWithCallback")
	}

	runner = StreamRunner{Client: client}
	_, err = runner.RunWithCallback(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty model in RunWithCallback")
	}
}

func TestStreamRunner_StreamError(t *testing.T) {
	// Use a non-retryable error so the test completes immediately
	// regardless of whether output was already seen.
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "partial"},
			{Type: providers.EventError, Error: errors.New("permanent stream failure")},
		},
	}

	runner := StreamRunner{Client: client, Model: "m"}
	result, err := runner.RunWithCallback(
		context.Background(),
		[]providers.ChatMessage{{Role: "user", Content: "hi"}},
		nil,
	)
	if err == nil {
		t.Fatal("expected error from stream error event")
	}
	visible := visibleMessagesForTest(result.NewMessages)
	if len(visible) != 1 || visible[0].Role != "assistant" || visible[0].Content != "partial" {
		t.Fatalf("partial assistant output was not returned for persistence: %+v", result.NewMessages)
	}
}

func TestStreamRunner_RetryOnMidStreamError(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "partial"}, {Type: providers.EventError, Error: context.DeadlineExceeded}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}},
	}}

	var reconnectMsgs []string
	runner := StreamRunner{Client: client, Model: "m", OnEvent: func(ev providers.StreamEvent) {
		if ev.Type == providers.EventReconnect {
			reconnectMsgs = append(reconnectMsgs, ev.Content)
		}
	}}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if len(reconnectMsgs) != 1 {
		t.Fatalf("expected 1 reconnect message, got %d", len(reconnectMsgs))
	}
}

func TestStreamRunner_RetryOnInitialConnectError(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{err: errors.New("Post https://example.com/v1/chat/completions: EOF")},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}},
	}}

	var reconnectMsgs []string
	runner := StreamRunner{Client: client, Model: "m", OnEvent: func(ev providers.StreamEvent) {
		if ev.Type == providers.EventReconnect {
			reconnectMsgs = append(reconnectMsgs, ev.Content)
		}
	}}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
	if len(reconnectMsgs) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(reconnectMsgs))
	}
}

func TestStreamRunner_RetriesOnIncompleteStreamBeforeOutput(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: nil},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}},
	}}

	var reconnectMsgs []string
	runner := StreamRunner{Client: client, Model: "m", OnEvent: func(ev providers.StreamEvent) {
		if ev.Type == providers.EventReconnect {
			reconnectMsgs = append(reconnectMsgs, ev.Content)
		}
	}}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
	if len(reconnectMsgs) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(reconnectMsgs))
	}
}

func TestStreamRunner_EmitsStructuredLifecycleEvents(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{err: errors.New("EOF")},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}}},
	}}

	var lifecycle []*providers.StreamLifecycle
	runner := StreamRunner{Client: client, Model: "m", OnEvent: func(ev providers.StreamEvent) {
		if ev.Type == providers.EventLifecycle && ev.Lifecycle != nil {
			lifecycle = append(lifecycle, ev.Lifecycle)
		}
	}}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
	if len(lifecycle) != 4 {
		t.Fatalf("expected 4 lifecycle events, got %d: %+v", len(lifecycle), lifecycle)
	}
	if lifecycle[0].Phase != providers.StreamPhaseConnecting || lifecycle[0].Attempt != 1 {
		t.Fatalf("unexpected first lifecycle event: %+v", lifecycle[0])
	}
	if lifecycle[1].Phase != providers.StreamPhaseReconnecting || lifecycle[1].RetryCount != 1 || lifecycle[1].Attempt != 2 {
		t.Fatalf("unexpected reconnect lifecycle event: %+v", lifecycle[1])
	}
	if lifecycle[2].Phase != providers.StreamPhaseConnecting || lifecycle[2].Attempt != 2 {
		t.Fatalf("unexpected second connecting lifecycle event: %+v", lifecycle[2])
	}
	if lifecycle[3].Phase != providers.StreamPhaseConnected || lifecycle[3].Attempt != 2 {
		t.Fatalf("unexpected connected lifecycle event: %+v", lifecycle[3])
	}
	if lifecycle[1].MaxAttempts != 11 || lifecycle[1].MaxRetries != 10 {
		t.Fatalf("unexpected retry budget: %+v", lifecycle[1])
	}
	if lifecycle[0].OperationID == "" || lifecycle[0].AttemptID == "" || lifecycle[0].AttemptID == lifecycle[3].AttemptID {
		t.Fatalf("unexpected lifecycle identities: %+v", lifecycle)
	}
}

func TestStreamRunner_RetryOnInitialConnectHTTP500(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{err: &providers.HTTPError{StatusCode: 500, Body: "upstream error"}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunnerToleratesUnansweredNetworkReplaysAcrossAgentRounds(t *testing.T) {
	// Every round fails once with a pre-response transport error and succeeds
	// on replay. Those replays are unbillable and must not accumulate against
	// the workflow replay budget, so the turn survives any number of them
	// (per-operation attempt limits still bound each round on their own).
	const successfulToolRounds = 6
	attempts := make([]mockStreamAttempt, 0, successfulToolRounds*2)
	results := make(map[string]string, successfulToolRounds)
	for round := 0; round < successfulToolRounds; round++ {
		callID := fmt.Sprintf("call-%d", round)
		attempts = append(attempts,
			mockStreamAttempt{err: errors.New("connection reset by peer")},
			mockStreamAttempt{events: []providers.StreamEvent{
				{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: callID, Name: "read_file"}},
				{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: callID, Name: "read_file", Arguments: `{}`}},
				{Type: providers.EventDone},
			}},
		)
		results[callID] = `{"ok":true}`
	}
	attempts = append(attempts, mockStreamAttempt{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "done"},
		{Type: providers.EventDone},
	}})
	client := &mockStreamClient{attempts: attempts}
	runner := StreamRunner{
		Client: client,
		Model:  "m",
		Tools: &fakeLoopTools{
			defs:    []providers.ToolDefinition{{Name: "read_file"}},
			results: results,
		},
	}

	result, err := runner.Run(context.Background(), "read files")
	if err != nil {
		t.Fatalf("Run error = %v, want transient network replays tolerated", err)
	}
	if result != "done" {
		t.Fatalf("result = %q, want %q", result, "done")
	}
	wantPhysicalAttempts := successfulToolRounds*2 + 1
	if client.callCount != wantPhysicalAttempts {
		t.Fatalf("physical stream attempts = %d, want %d", client.callCount, wantPhysicalAttempts)
	}
	workflowID := client.requests[0].Operation.WorkflowID
	if workflowID == "" {
		t.Fatal("first agent round has no workflow identity")
	}
	for index, req := range client.requests {
		if req.Operation.WorkflowID != workflowID {
			t.Fatalf("request %d workflow = %q, want %q", index, req.Operation.WorkflowID, workflowID)
		}
	}
}

func TestStreamRunner_DoesNotRetryTerminalUsageLimit(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventError, Error: providers.NewProviderStreamError("usage_limit_reached", "The usage limit has been reached")}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "should not run"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}

	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected terminal usage-limit error")
	}
	if client.callCount != 1 {
		t.Fatalf("expected no retry after terminal usage limit, got %d stream attempts", client.callCount)
	}
}

func TestStreamRunner_RetryOnEarlyStreamErrorEvent(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventError, Error: errors.New("connection reset by peer")}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_RetryAfterPartialOutput(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "partial"}, {Type: providers.EventError, Error: errors.New("EOF")}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "second"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}
	var contentEvents []providers.StreamEvent
	runner.OnEvent = func(event providers.StreamEvent) {
		if event.Type == providers.EventContentDelta || event.Type == providers.EventContentReplace {
			contentEvents = append(contentEvents, event)
		}
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "second" {
		t.Fatalf("unexpected result: %q", result)
	}
	if got := streamContentEventSummary(contentEvents); got != "content_delta:partial,content_replace:,content_delta:second" {
		t.Fatalf("unexpected content event sequence: %s", got)
	}
	if client.callCount != 2 {
		t.Fatalf("expected reconnect after partial output, got %d attempts", client.callCount)
	}
}

func TestStreamRunner_RetryIncompleteStreamAfterPartialOutput(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "partial"}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "second"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}
	var contentEvents []providers.StreamEvent
	runner.OnEvent = func(event providers.StreamEvent) {
		if event.Type == providers.EventContentDelta || event.Type == providers.EventContentReplace {
			contentEvents = append(contentEvents, event)
		}
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "second" {
		t.Fatalf("unexpected result: %q", result)
	}
	if got := streamContentEventSummary(contentEvents); got != "content_delta:partial,content_replace:,content_delta:second" {
		t.Fatalf("unexpected content event sequence: %s", got)
	}
	if client.callCount != 2 {
		t.Fatalf("expected reconnect after partial output, got %d attempts", client.callCount)
	}
}

func streamContentEventSummary(events []providers.StreamEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%s:%s", event.Type, event.Content))
	}
	return strings.Join(parts, ",")
}

func TestStreamRunner_AcceptsHistory(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "turn2 reply"},
			{Type: providers.EventDone},
		},
	}

	history := []providers.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "follow up"},
	}

	runner := StreamRunner{Client: client, Model: "test-model"}
	res, err := runner.RunWithCallback(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	result := res.Content
	newMsgs := res.NewMessages
	if result != "turn2 reply" {
		t.Fatalf("unexpected result: %q", result)
	}

	// All history messages should have been sent to the API.
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	sent := client.requests[0].Messages
	visibleSent := visibleMessagesForTest(sent)
	if len(visibleSent) != len(history) {
		t.Fatalf("expected %d messages sent, got %d", len(history), len(sent))
	}
	for i, msg := range history {
		if visibleSent[i].Role != msg.Role || visibleSent[i].Content != msg.Content {
			t.Fatalf("message %d mismatch: got %+v, want %+v", i, visibleSent[i], msg)
		}
	}

	// newMsgs should contain exactly the assistant reply.
	visibleNew := visibleMessagesForTest(newMsgs)
	if len(visibleNew) != 1 {
		t.Fatalf("expected 1 visible new message, got %+v", newMsgs)
	}
	if visibleNew[0].Role != "assistant" {
		t.Fatalf("expected assistant message, got %q", visibleNew[0].Role)
	}
	if visibleNew[0].Content != "turn2 reply" {
		t.Fatalf("unexpected new message content: %q", visibleNew[0].Content)
	}
}

func TestStreamRunner_FiltersSystemReminderHistoryAndEvents(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventDone, StopReason: "end_turn"},
		},
	}

	reminder := "<system-reminder>\n# Environment\n- CWD: /tmp\n</system-reminder>"
	history := []providers.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
		{Role: "user", Name: wuucontext.SystemReminderMessageName, Content: reminder},
		{Role: "user", Name: "wuu_context_anchor", Content: "<system>CHECKPOINT 7</system>", Hidden: true},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages([]providers.ChatMessage{{
				Role:    "user",
				Name:    wuucontext.SystemReminderMessageName,
				Content: reminder,
			}})
		},
	}

	res, err := runner.RunWithCallback(context.Background(), history, func(ev providers.StreamEvent) {
		received = append(received, ev)
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}

	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	sent := client.requests[0].Messages
	if len(sent) != 4 {
		t.Fatalf("expected legacy reminder to be filtered while checkpoint and request-only context are sent, got %+v", sent)
	}
	if sent[0].Role != "system" || sent[1].Role != "user" {
		t.Fatalf("unexpected request messages: %+v", sent)
	}
	if sent[2].Name != "wuu_context_anchor" || !sent[2].Hidden {
		t.Fatalf("expected durable checkpoint before request-only context, got %+v", sent)
	}
	if sent[3].Name != wuucontext.SystemReminderMessageName || !sent[3].Hidden {
		t.Fatalf("expected hidden request-only system reminder, got %+v", sent)
	}

	if len(res.NewMessages) != 0 {
		t.Fatalf("expected transient model context to stay out of durable new messages, got %+v", res.NewMessages)
	}
	for _, ev := range received {
		if ev.Type == providers.EventMessage && ev.Message != nil && wuucontext.IsSystemReminder(ev.Message.Name, ev.Message.Content) {
			t.Fatalf("unexpected reminder event: %+v", ev)
		}
	}
}

func TestStreamRunner_ReusesUsageAcrossTurnsForPreRequestCompact(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "turn1"},
				{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 1300}},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "turn2"},
				{Type: providers.EventDone},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "summarized"},
		},
	}

	runner := StreamRunner{
		Client:                client,
		Model:                 "gpt-4-turbo",
		ContextWindowOverride: 5000,
	}

	firstHistory := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("u1 ", 100)},
		{Role: "assistant", Content: strings.Repeat("a1 ", 100)},
		{Role: "user", Content: strings.Repeat("u2 ", 100)},
		{Role: "assistant", Content: strings.Repeat("a2 ", 100)},
	}
	first, err := runner.RunWithCallback(context.Background(), firstHistory, nil)
	if err != nil {
		t.Fatalf("first RunWithCallback: %v", err)
	}

	secondHistory := append([]providers.ChatMessage{}, firstHistory...)
	secondHistory = append(secondHistory, first.NewMessages...)
	secondHistory = append(secondHistory, providers.ChatMessage{Role: "user", Content: "follow up"})

	_, err = runner.RunWithCallback(context.Background(), secondHistory, nil)
	if err != nil {
		t.Fatalf("second RunWithCallback: %v", err)
	}

	if len(client.requests) != 3 {
		t.Fatalf("expected 3 total requests (stream, compact, stream), got %d", len(client.requests))
	}
	if len(visibleMessagesForTest(client.requests[2].Messages)) >= len(visibleMessagesForTest(secondHistory)) {
		t.Fatalf("expected compacted second request, got %d messages from %d-history input",
			len(client.requests[2].Messages), len(secondHistory))
	}
	if got := client.requests[2].Messages[0].Content; got != "sys" {
		t.Fatalf("expected original system prompt to remain first, got %q", got)
	}
	if got := client.requests[2].Messages[1].Content; !compact.IsConversationSummaryContent(got) || !strings.Contains(got, "summarized") {
		t.Fatalf("expected compacted summary after system prompt, got %q", got)
	}
}

func TestStreamRunner_ExpandedDurableHistoryKeepsLastProviderBaseline(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "done"},
		{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 190_100}},
	}}}}
	runner := StreamRunner{
		Client:                 client,
		ProviderName:           "openai-codex",
		Model:                  "gpt-5.6-sol",
		ContextWindowOverride:  272_000,
		MaxInputTokens:         272_000,
		OutputReserveTokens:    128_000,
		CompactThresholdTokens: 239_000,
	}

	lastResponse := providers.ChatMessage{Role: "assistant", Content: "previous answer"}
	compactedHistory := []providers.ChatMessage{
		{Role: "system", Content: "[Conversation summary] bounded"},
		lastResponse,
	}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 190_000})
	runner.commitUsageTracker(tracker, compactedHistory)

	// Simulate a durable reload that expands the old prefix with raw tool
	// results while retaining the exact response boundary measured above. The
	// lossless durable content is intentionally much larger than the provider
	// projection stored on ToolResult.
	expanded := []providers.ChatMessage{
		{Role: "system", Content: "[Conversation summary] expanded"},
		{Role: "user", Content: "old request"},
	}
	for i := 0; i < 48; i++ {
		callID := fmt.Sprintf("call-%d", i)
		expanded = append(expanded, providers.ChatMessage{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID: callID, Name: "read_file", Arguments: fmt.Sprintf(`{"path":"file-%d"}`, i),
			}},
		})
		projected := toolresult.FromText("bounded provider projection")
		expanded = append(expanded, providers.ChatMessage{
			Role:       "tool",
			Name:       "read_file",
			ToolCallID: callID,
			Content:    strings.Repeat("raw durable output ", 1_300),
			ToolResult: &projected,
		})
	}
	expanded = append(expanded, lastResponse, userMsg("short follow-up"))

	if raw := estimateMessages(expanded); raw < runner.CompactThresholdTokens {
		t.Fatalf("raw durable estimate = %d, want above threshold %d", raw, runner.CompactThresholdTokens)
	}
	projected, err := providers.PrepareMessagesForProviderRequest(runner.ProviderName, runner.Model, expanded)
	if err != nil {
		t.Fatalf("project provider request: %v", err)
	}
	if got := estimateMessages(projected); got >= runner.CompactThresholdTokens {
		t.Fatalf("provider projection estimate = %d, want below threshold %d", got, runner.CompactThresholdTokens)
	}

	prepared, tracked := runner.prepareUsageTracker(expanded)
	breakdown := prepared.Breakdown()
	if tracked != len(expanded) || breakdown.LastResponseTotal != 190_000 {
		t.Fatalf("rebased tracker = %+v tracked=%d, want provider baseline and %d messages", breakdown, tracked, len(expanded))
	}
	if breakdown.Adjustment != UsageAdjustmentRequestShapeTailRebase {
		t.Fatalf("usage adjustment = %q, want %q", breakdown.Adjustment, UsageAdjustmentRequestShapeTailRebase)
	}
	if breakdown.Total() >= runner.CompactThresholdTokens {
		t.Fatalf("rebased estimate = %d, want below threshold %d", breakdown.Total(), runner.CompactThresholdTokens)
	}

	// The app-server synchronizes the durable snapshot before admitting the
	// next user message. A missing persisted total must still preserve the live
	// provider baseline when the same response boundary is present.
	runner.SynchronizeConversationUsage(expanded[:len(expanded)-1], 0)
	synced, _ := runner.prepareUsageTracker(expanded)
	if got := synced.Breakdown(); got.LastResponseTotal != 190_000 || got.Adjustment != UsageAdjustmentRequestShapeTailRebase || got.Total() >= runner.CompactThresholdTokens {
		t.Fatalf("synchronized tracker lost provider baseline: %+v", got)
	}

	var attempts []CompactAttemptInfo
	runner.OnCompactAttempt = func(info CompactAttemptInfo) { attempts = append(attempts, info) }
	res, err := runner.RunWithCallback(context.Background(), expanded, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if res.Content != "done" || len(client.requests) != 1 {
		t.Fatalf("normal request did not run directly: result=%q requests=%d", res.Content, len(client.requests))
	}
	if len(attempts) != 0 {
		t.Fatalf("short follow-up triggered proactive compact: %+v", attempts)
	}
}

func TestStreamRunner_PreRequestCompactUsesColdStartEstimate(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "ok"},
			{Type: providers.EventDone},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "summarized"},
		},
	}

	runner := StreamRunner{
		Client:                 client,
		Model:                  "gpt-4-turbo",
		ContextWindowOverride:  16000,
		CompactThresholdTokens: 5000,
	}

	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("older ", 2000)},
		{Role: "assistant", Content: strings.Repeat("older ", 2000)},
		{Role: "user", Content: "continue"},
	}
	var compactEvents []providers.StreamEvent
	res, err := runner.RunWithCallback(context.Background(), history, func(event providers.StreamEvent) {
		if event.Type == providers.EventCompact {
			compactEvents = append(compactEvents, event)
		}
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected history rewrite after cold-start compact")
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected compact request plus stream request, got %d", len(client.requests))
	}
	if len(visibleMessagesForTest(client.requests[1].Messages)) >= len(history) {
		t.Fatalf("expected compacted stream request, got %d messages from %d-history input",
			len(client.requests[1].Messages), len(history))
	}
	if got := client.requests[1].Messages[1].Content; !compact.IsConversationSummaryContent(got) || !strings.Contains(got, "summarized") {
		t.Fatalf("expected compacted summary after system prompt, got %q", got)
	}
	if len(compactEvents) != 2 {
		t.Fatalf("compact events = %+v, want started and completed", compactEvents)
	}
	if compactEvents[0].CompactPhase != providers.CompactPhaseStarted || compactEvents[0].CompactReason != string(CompactReasonProactive) {
		t.Fatalf("first compact event should start proactive progress, got %+v", compactEvents[0])
	}
	if compactEvents[1].CompactPhase != providers.CompactPhaseCompleted || compactEvents[1].Content == "" {
		t.Fatalf("second compact event should complete progress with a notice, got %+v", compactEvents[1])
	}
}

func TestStreamRunner_ProactiveCompactFailureEmitsVisibleEvent(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "ok"},
			{Type: providers.EventDone},
		},
		chatErrs: []error{errors.New("compact summary unavailable")},
	}
	var events []providers.StreamEvent
	var attempts []CompactAttemptInfo
	runner := StreamRunner{
		Client:                client,
		Model:                 "gpt-4-turbo",
		ContextWindowOverride: 5000,
		OnCompactAttempt: func(info CompactAttemptInfo) {
			attempts = append(attempts, info)
		},
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("older ", 2000)},
		{Role: "assistant", Content: strings.Repeat("older ", 2000)},
		{Role: "user", Content: "continue"},
	}

	res, err := runner.RunWithCallback(context.Background(), history, func(event providers.StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if len(attempts) != 1 || attempts[0].Reason != CompactReasonProactive || attempts[0].Status != CompactAttemptFailed {
		t.Fatalf("expected failed proactive compact diagnostic, got %+v", attempts)
	}
	var compactEvents []providers.StreamEvent
	for _, event := range events {
		if event.Type == providers.EventCompact {
			compactEvents = append(compactEvents, event)
		}
	}
	if len(compactEvents) != 2 {
		t.Fatalf("expected compact progress and failure events, got %+v", compactEvents)
	}
	if compactEvents[0].CompactPhase != providers.CompactPhaseStarted {
		t.Fatalf("expected compact progress to start first, got %+v", compactEvents[0])
	}
	if compactEvents[1].CompactPhase != providers.CompactPhaseFailed {
		t.Fatalf("expected failure notice to complete progress, got %+v", compactEvents[1])
	}
	if compactEvents[1].CompactReason != string(CompactReasonProactive) {
		t.Fatalf("expected proactive compact reason, got %+v", compactEvents[1])
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want failed compact then agent request", len(client.requests))
	}
	if got := client.requests[1].Operation.ParentOperationID; got != "" {
		t.Fatalf("agent request parent = %q after failed compaction", got)
	}
}

func TestFormatCompactAttemptNoticeClosesVisibleProgress(t *testing.T) {
	if notice, ok := formatCompactAttemptNotice(CompactAttemptInfo{Reason: CompactReasonProactive, Status: CompactAttemptSucceeded}); ok || notice != "" {
		t.Fatalf("successful attempts use OnCompact completion, got ok=%v notice=%q", ok, notice)
	}

	cases := []CompactAttemptInfo{
		{Reason: CompactReasonProactive, Status: CompactAttemptFailed},
		{Reason: CompactReasonOverflow, Status: CompactAttemptFailed},
		{Reason: CompactReasonManual, Status: CompactAttemptFailed},
		{Reason: CompactReasonProactive, Status: CompactAttemptUnchanged},
		{Reason: CompactReasonOverflow, Status: CompactAttemptUnchanged},
		{Reason: CompactReasonManual, Status: CompactAttemptUnchanged},
	}
	for _, tc := range cases {
		if notice, ok := formatCompactAttemptNotice(tc); !ok || notice == "" {
			t.Fatalf("attempt %+v must emit a terminal notice, got ok=%v notice=%q", tc, ok, notice)
		}
	}
}

func TestFormatCompactNoticeIncludesReplacementTokenEstimate(t *testing.T) {
	notice := formatCompactNotice(CompactInfo{
		Reason:         CompactReasonProactive,
		TokensBefore:   239_000,
		TokensAfter:    49_300,
		MessagesBefore: 119,
		MessagesAfter:  1,
	})
	const want = "✦ Compacted history: 119 → 1 messages (~239k → ~49k tokens)"
	if notice != want {
		t.Fatalf("notice = %q, want %q", notice, want)
	}
}

func TestFormatCompactAttemptNoticeExplainsOutputLimitRecovery(t *testing.T) {
	notice, ok := formatCompactAttemptNotice(CompactAttemptInfo{
		Reason:      CompactReasonManual,
		Status:      CompactAttemptFailed,
		OutputLimit: true,
	})
	if !ok {
		t.Fatal("output-limit failure must emit a terminal notice")
	}
	for _, want := range []string{"after retry", "history is unchanged", "Retry compaction", "larger output limit"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q does not contain %q", notice, want)
		}
	}
}

func TestStreamRunner_StopsProactiveCompactAfterRepeatedFailures(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "ok"},
			{Type: providers.EventDone},
		},
		chatErrs: []error{
			errors.New("compact failure 1"),
			errors.New("compact failure 2"),
			errors.New("compact failure 3"),
		},
	}
	runner := StreamRunner{
		Client:                client,
		Model:                 "gpt-4-turbo",
		ContextWindowOverride: 5000,
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("older ", 2000)},
		{Role: "assistant", Content: strings.Repeat("older ", 2000)},
		{Role: "user", Content: "continue"},
	}

	for i := 0; i < maxConsecutiveProactiveCompactFailures+1; i++ {
		res, err := runner.RunWithCallback(context.Background(), history, nil)
		if err != nil {
			t.Fatalf("RunWithCallback %d: %v", i+1, err)
		}
		if res.Content != "ok" {
			t.Fatalf("RunWithCallback %d content = %q", i+1, res.Content)
		}
	}
	if client.chatCallCount != maxConsecutiveProactiveCompactFailures {
		t.Fatalf("expected compact summary to stop after %d failures, got %d calls", maxConsecutiveProactiveCompactFailures, client.chatCallCount)
	}
}

func TestStreamRunner_CompactedHistoryDoesNotTriggerImmediateSecondCompact(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "first"},
				{Type: providers.EventDone},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "second"},
				{Type: providers.EventDone},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "summarized older context"},
		},
	}

	runner := StreamRunner{
		Client:                 client,
		Model:                  "gpt-4-turbo",
		ContextWindowOverride:  16000,
		CompactThresholdTokens: 5000,
	}

	firstHistory := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("older ", 2000)},
		{Role: "assistant", Content: strings.Repeat("older ", 2000)},
		{Role: "user", Content: "continue"},
	}
	first, err := runner.RunWithCallback(context.Background(), firstHistory, nil)
	if err != nil {
		t.Fatalf("first RunWithCallback: %v", err)
	}
	if first.Content != "first" {
		t.Fatalf("unexpected first content %q", first.Content)
	}
	if !first.HistoryRewritten {
		t.Fatal("expected first run to rewrite history")
	}

	secondHistory := append([]providers.ChatMessage{}, first.NewMessages...)
	secondHistory = append(secondHistory, providers.ChatMessage{Role: "user", Content: "follow up"})
	second, err := runner.RunWithCallback(context.Background(), secondHistory, nil)
	if err != nil {
		t.Fatalf("second RunWithCallback: %v", err)
	}
	if second.Content != "second" {
		t.Fatalf("unexpected second content %q", second.Content)
	}
	if second.HistoryRewritten {
		t.Fatal("did not expect immediate second history rewrite")
	}

	if len(client.requests) != 3 {
		t.Fatalf("expected compact request plus two stream requests, got %d", len(client.requests))
	}
	if !isCompactSummaryRequest(client.requests[0]) {
		t.Fatalf("expected first request to summarize for compact, got %+v", client.requests[0].Messages)
	}
	if isCompactSummaryRequest(client.requests[1]) || isCompactSummaryRequest(client.requests[2]) {
		t.Fatalf("expected only one compact request, got compact flags: %t %t %t",
			isCompactSummaryRequest(client.requests[0]),
			isCompactSummaryRequest(client.requests[1]),
			isCompactSummaryRequest(client.requests[2]))
	}
	if client.chatCallCount != 1 {
		t.Fatalf("expected one compact summary call, got %d", client.chatCallCount)
	}

	secondSent := client.requests[2].Messages
	if len(visibleMessagesForTest(secondSent)) != len(visibleMessagesForTest(secondHistory)) {
		t.Fatalf("expected second request to preserve visible compacted history, got %d messages from %d-history input",
			len(secondSent), len(secondHistory))
	}
	if got := secondSent[1].Content; !compact.IsConversationSummaryContent(got) ||
		!strings.Contains(got, "summarized older context") ||
		strings.Contains(got, "older older older older") {
		t.Fatalf("expected compacted summary without raw older context in second request, got %q", got)
	}
}

func TestStreamRunner_ContextOverflowStreamErrorCompactsSingleUserTurn(t *testing.T) {
	overflow := providers.NewProviderStreamError(
		"context_length_exceeded",
		"Your input exceeds the context window of this model. Please adjust your input and try again.",
	)
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventError, Error: overflow},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "WUU_COMPACT_OK"},
				{Type: providers.EventDone},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "summarized single-turn tool run"},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "test-model",
		ContextWindowOverride:   16000,
		CompactKeepRecentTokens: 1000,
	}
	history := []providers.ChatMessage{
		{Role: "user", Content: "debug the issue"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "run_shell", Arguments: `{"command":"rg ContextOverflow"}`},
		}},
		{Role: "tool", Name: "run_shell", ToolCallID: "call_1", Content: strings.Repeat("result ", 1000)},
		{Role: "assistant", Content: "I found the first clue."},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_2", Name: "run_shell", Arguments: `{"command":"sed -n 1,220p internal/agent/loop.go"}`},
		}},
		{Role: "tool", Name: "run_shell", ToolCallID: "call_2", Content: strings.Repeat("result ", 1000)},
		{Role: "assistant", Content: "I will continue from the runtime path."},
	}

	var compactSeen bool
	var eventErrorSeen bool
	res, err := runner.RunWithCallback(context.Background(), history, func(ev providers.StreamEvent) {
		if ev.Type == providers.EventCompact {
			compactSeen = true
		}
		if ev.Type == providers.EventError {
			eventErrorSeen = true
		}
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if res.Content != "WUU_COMPACT_OK" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected rewritten history after overflow compact")
	}
	if !compactSeen {
		t.Fatal("expected compact stream event")
	}
	if eventErrorSeen {
		t.Fatal("context overflow recovery should not surface a terminal error event")
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected stream, compact, stream requests, got %d", len(client.requests))
	}
	initialOperation := client.requests[0].Operation
	compactOperation := client.requests[1].Operation
	resumedOperation := client.requests[2].Operation
	if compactOperation.ParentOperationID != initialOperation.ID {
		t.Fatalf("compact parent = %q, want overflow operation %q", compactOperation.ParentOperationID, initialOperation.ID)
	}
	if resumedOperation.ParentOperationID != compactOperation.ID {
		t.Fatalf("resumed parent = %q, want compact operation %q", resumedOperation.ParentOperationID, compactOperation.ID)
	}
	finalRequest := client.requests[2]
	if got := finalRequest.Messages[0].Content; !compact.IsConversationSummaryContent(got) ||
		!strings.Contains(got, "summarized single-turn tool run") {
		t.Fatalf("expected compact summary in retry request, got %q", got)
	}
	for _, msg := range finalRequest.Messages {
		if strings.Contains(msg.Content, strings.Repeat("result ", 100)) {
			t.Fatalf("compacted retry request should not resend raw oversized tool output: %+v", finalRequest.Messages)
		}
	}
}

func TestStreamRunner_CancelledCtxStopsRetry(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{err: context.DeadlineExceeded},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "should not reach"}, {Type: providers.EventDone}}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner := StreamRunner{Client: client, Model: "m"}
	_, err := runner.Run(ctx, "hi")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if client.callCount > 0 {
		t.Fatalf("expected 0 stream attempts on cancelled ctx, got %d", client.callCount)
	}
}

func TestStreamRunner_RetryOnDeadlineExceeded(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{err: context.DeadlineExceeded},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}},
	}}

	runner := StreamRunner{Client: client, Model: "m"}
	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_ZeroMaxStepsIsUnlimited(t *testing.T) {
	// Regression: previously MaxSteps == 0 was silently coerced to 8,
	// which broke long coordinator sessions. With the fix, 0 means
	// unlimited and a 12-round tool-call run completes successfully.
	const rounds = 12

	attempts := make([]mockStreamAttempt, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("call_%d", i)
		attempts = append(attempts, mockStreamAttempt{
			events: []providers.StreamEvent{
				{
					Type:     providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{ID: id, Name: "run_shell"},
				},
				{
					Type: providers.EventToolUseEnd,
					ToolCall: &providers.ToolCall{
						ID: id, Name: "run_shell",
						Arguments: `{"command":"echo hi"}`,
					},
				},
				{Type: providers.EventDone},
			},
		})
	}
	// Final round: content only, no tool calls — runner exits cleanly.
	attempts = append(attempts, mockStreamAttempt{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "all done"},
			{Type: providers.EventDone},
		},
	})

	client := &mockStreamClient{attempts: attempts}
	tools := &fakeTools{}
	runner := StreamRunner{
		Client: client,
		Tools:  tools,
		Model:  "test-model",
		// MaxSteps left at zero — must mean "no cap".
	}

	out, err := runner.Run(context.Background(), "long task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "all done" {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(tools.calls) != rounds {
		t.Fatalf("expected %d tool calls, got %d", rounds, len(tools.calls))
	}
}

func TestStreamRunner_ReplaysReasoningContentAfterToolCall(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventThinkingDelta, Content: "inspect repo before tool use"},
				{
					Type: providers.EventThinkingDone,
					ReasoningBlock: &providers.ReasoningBlock{
						Type:      "thinking",
						Thinking:  "inspect repo before tool use",
						Signature: "sig_1",
					},
				},
				{
					Type:     providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{ID: "call_1", Name: "list_files"},
				},
				{
					Type: providers.EventToolUseEnd,
					ToolCall: &providers.ToolCall{
						ID:        "call_1",
						Name:      "list_files",
						Arguments: `{}`,
					},
				},
				{Type: providers.EventDone},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done"},
				{Type: providers.EventDone},
			}},
		},
	}

	runner := StreamRunner{
		Client: client,
		Tools:  &fakeTools{},
		Model:  "test-model",
	}

	out, err := runner.Run(context.Background(), "inspect this repo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(client.requests))
	}
	second := client.requests[1].Messages
	visibleSecond := visibleMessagesForTest(second)
	if len(visibleSecond) != 3 {
		t.Fatalf("expected user + assistant + tool in second request, got %+v", second)
	}
	assistant := visibleSecond[1]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant message, got %+v", assistant)
	}
	if assistant.ReasoningContent != "inspect repo before tool use" {
		t.Fatalf("unexpected reasoning content replay: %q", assistant.ReasoningContent)
	}
	if len(assistant.ReasoningBlocks) != 1 || assistant.ReasoningBlocks[0].Signature != "sig_1" {
		t.Fatalf("unexpected reasoning blocks replay: %+v", assistant.ReasoningBlocks)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls on assistant replay: %+v", assistant.ToolCalls)
	}
}

func TestStreamRunner_OutputTruncationCompletesTurn(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "part1 "},
				{Type: providers.EventDone, StopReason: "length", Truncated: true},
			}},
		},
	}

	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
	}

	res, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{userMsg("long output")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "part1 " {
		t.Fatalf("expected first partial content, got %q", res.Content)
	}
	if res.FinishReason != providers.FinishReasonLength || res.StopReason != "length" || !res.Truncated {
		t.Fatalf("expected length finish metadata, got reason=%q stop=%q truncated=%v", res.FinishReason, res.StopReason, res.Truncated)
	}
	if client.callCount != 1 {
		t.Fatalf("expected 1 stream attempt, got %d", client.callCount)
	}
}

func TestStreamRunner_MaxStepsExceeded(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{
				Type: providers.EventToolUseStart,
				ToolCall: &providers.ToolCall{
					ID:   "call_1",
					Name: "run_shell",
				},
			},
			{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        "call_1",
					Name:      "run_shell",
					Arguments: `{"command":"echo hi"}`,
				},
			},
			{Type: providers.EventDone},
		},
	}

	tools := &fakeTools{}
	runner := StreamRunner{
		Client:   client,
		Tools:    tools,
		Model:    "test-model",
		MaxSteps: 2,
	}

	_, err := runner.Run(context.Background(), "loop")
	if err == nil {
		t.Fatal("expected max steps error")
	}
	if len(tools.calls) != 2 {
		t.Fatalf("expected 2 tool executions, got %d", len(tools.calls))
	}
}

func TestStreamRunner_NonStreamingFallbackOnEmptyStream(t *testing.T) {
	// Stream returns empty content with no stop reason (proxy issue),
	// but the non-streaming Chat() fallback succeeds.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventDone, StopReason: ""},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "fallback answer", StopReason: "stop"},
		},
	}
	runner := &StreamRunner{Client: client, Model: "test"}
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fallback answer" {
		t.Fatalf("expected fallback answer, got %q", result)
	}
	if client.chatCallCount != 1 {
		t.Fatalf("expected 1 Chat() call, got %d", client.chatCallCount)
	}
	if len(client.requests) != 2 || client.requests[0].Execution == nil || client.requests[0].Execution != client.requests[1].Execution {
		t.Fatalf("stream and unary fallback did not share one execution: %+v", client.requests)
	}
	ledger := client.requests[0].Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 || ledger.Submissions[0].Mode != "stream" || ledger.Submissions[1].Mode != "unary" {
		t.Fatalf("fallback ledger = %+v", ledger)
	}
}

func TestStreamRunner_NoFallbackOnNormalStop(t *testing.T) {
	// Stream returns empty content with stop_reason=stop — this is a
	// legitimate model choice, not a proxy issue. No fallback.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventDone, StopReason: "stop"},
			}},
		},
	}
	runner := &StreamRunner{Client: client, Model: "test"}
	_, err := runner.Run(context.Background(), "hello")
	// Should produce an EmptyAnswerError (from the loop), not trigger fallback.
	if err == nil {
		t.Fatal("expected error for empty content with stop reason")
	}
	if !IsEmptyAnswer(err) {
		t.Fatalf("expected EmptyAnswerError, got %v", err)
	}
	if client.chatCallCount != 0 {
		t.Fatalf("expected 0 Chat() calls (no fallback), got %d", client.chatCallCount)
	}
}

func TestStreamRunner_NoFallbackOnEmptyLengthFinish(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventDone, StopReason: "max_tokens", FinishReason: providers.FinishReasonLength, Truncated: true},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "fallback should not run", StopReason: "stop"},
		},
	}
	runner := &StreamRunner{Client: client, Model: "test"}
	res, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{userMsg("hello")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "" {
		t.Fatalf("expected empty content, got %q", res.Content)
	}
	if res.FinishReason != providers.FinishReasonLength || res.StopReason != "max_tokens" || !res.Truncated {
		t.Fatalf("expected length finish metadata, got reason=%q stop=%q truncated=%v", res.FinishReason, res.StopReason, res.Truncated)
	}
	if client.chatCallCount != 0 {
		t.Fatalf("expected 0 Chat() calls (no fallback), got %d", client.chatCallCount)
	}
}

type slowStreamedRuntimeTools struct {
	mu       sync.Mutex
	calls    []providers.ToolCall
	canceled bool
}

func (f *slowStreamedRuntimeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_file"}}
}

func (f *slowStreamedRuntimeTools) ToolMetadata(_ providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}

func (f *slowStreamedRuntimeTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	select {
	case <-time.After(150 * time.Millisecond):
		return `{"ok":"` + call.ID + `"}`, nil
	case <-ctx.Done():
		f.mu.Lock()
		f.canceled = true
		f.mu.Unlock()
		return "", ctx.Err()
	}
}

func (f *slowStreamedRuntimeTools) wasCanceled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canceled
}

// A tool that starts during streaming and is still running when the stream
// completes must survive into the final batch: the stream attempt context
// ends with the stream, but the tool belongs to the turn.
func TestStreamRunner_StreamStartedToolSurvivesStreamCompletion(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{
			{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call-slow", Name: "read_file"}},
			{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "call-slow", Name: "read_file", Arguments: `{"path":"main.go"}`}},
			{Type: providers.EventDone, StopReason: "tool_use"},
		}},
		{events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "done"},
			{Type: providers.EventDone, StopReason: "stop"},
		}},
	}}
	tools := &slowStreamedRuntimeTools{}
	runner := &StreamRunner{
		Client:                 client,
		Model:                  "test",
		Tools:                  tools,
		StreamingToolExecution: true,
	}
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Fatalf("expected final content, got %q", result)
	}
	if tools.wasCanceled() {
		t.Fatal("stream-started tool was canceled by stream completion")
	}
	if len(client.requests) < 2 {
		t.Fatalf("expected a second model request, got %d", len(client.requests))
	}
	var toolMsg string
	for _, msg := range client.requests[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-slow" {
			toolMsg = msg.Content
		}
	}
	if !strings.Contains(toolMsg, `"ok"`) {
		t.Fatalf("tool result lost to stream completion: %q", toolMsg)
	}
}

func TestStreamRunner_CancelsOrphanStreamingToolWhenNoFinalToolCalls(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        "orphan",
					Name:      "read_file",
					Arguments: `{"path":"orphan.go"}`,
				},
			},
			{Type: providers.EventContentDelta, Content: "done"},
			{Type: providers.EventDone, StopReason: "stop"},
		},
	}
	tools := &cancelAwareRuntimeTools{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	runner := &StreamRunner{
		Client:                 client,
		Model:                  "test",
		Tools:                  tools,
		StreamingToolExecution: true,
		OnEvent: func(event providers.StreamEvent) {
			if event.Type != providers.EventToolUseEnd {
				return
			}
			select {
			case <-tools.started:
			case <-time.After(time.Second):
				t.Fatal("expected orphan tool to start during streaming")
			}
		},
	}
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Fatalf("expected final content, got %q", result)
	}
	select {
	case <-tools.canceled:
	case <-tools.started:
		select {
		case <-tools.canceled:
		case <-time.After(time.Second):
			t.Fatal("expected started orphan streaming tool to be canceled")
		}
	case <-time.After(50 * time.Millisecond):
		if calls := tools.recordedCalls(); len(calls) != 0 {
			t.Fatalf("orphan tool entered executor without completing cancellation: %+v", calls)
		}
	}
}

func TestStreamRunner_BlocksReplayAfterDurableToolAdmission(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{
			{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        "orphan",
					Name:      "read_file",
					Arguments: `{"path":"important.go"}`,
				},
			},
			{Type: providers.EventError, Error: providers.NewIncompleteStreamError("temporary drop")},
		}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "must not replay"}, {Type: providers.EventDone}}},
	}}
	tools := &cancelAwareRuntimeTools{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	ledger, err := toolledger.New(t.TempDir(), "thread-replay-fence")
	if err != nil {
		t.Fatal(err)
	}
	runner := &StreamRunner{
		Client:                 client,
		Model:                  "test",
		Tools:                  tools,
		ToolLedger:             ledger,
		StreamingToolExecution: true,
	}

	_, err = runner.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected replay fence error")
	}
	var blocked *providers.ReplayBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want ReplayBlockedError", err)
	}
	if client.callCount != 1 {
		t.Fatalf("stream attempts = %d, want unsafe replay blocked", client.callCount)
	}
	deadline := time.Now().Add(time.Second)
	for {
		pending, pendingErr := ledger.PendingProjection(context.Background())
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if len(pending) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled tool result was not durably settled")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStreamRunnerRecoversSettledUnprojectedToolResult(t *testing.T) {
	ctx := context.Background()
	ledger, err := toolledger.New(t.TempDir(), "thread-recovery")
	if err != nil {
		t.Fatal(err)
	}
	batchID, err := ledger.BeginBatch(ctx, "operation-crashed", 1)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := ledger.Prepare(ctx, batchID, providers.ToolCall{
		ID: "call-crashed", Name: "write_file", Arguments: `{"path":"a"}`,
	}, toolledger.ReplayAtMostOnce)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.FinalizeBatch(ctx, batchID); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Start(ctx, invocation.ID); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Settle(ctx, invocation.ID, toolresult.FromText("written")); err != nil {
		t.Fatal(err)
	}

	client := &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "continued"}, {Type: providers.EventDone},
	}}}}
	runner := &StreamRunner{Client: client, Model: "test", ToolLedger: ledger}
	result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || len(client.requests[0].Messages) != 3 {
		t.Fatalf("recovery request = %+v", client.requests)
	}
	requestMessages := client.requests[0].Messages
	if len(requestMessages[1].ToolCalls) != 1 || requestMessages[1].ToolCalls[0].ID != "call-crashed" ||
		requestMessages[2].ToolInvocationID != invocation.ID || requestMessages[2].Content != "written" {
		t.Fatalf("recovered request messages = %+v", requestMessages)
	}
	if len(result.NewMessages) != 3 || result.NewMessages[1].ToolInvocationID != invocation.ID || result.Content != "continued" {
		t.Fatalf("recovered result = %+v", result)
	}
	pending, err := ledger.PendingProjection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("result was marked projected before durable history append: %+v", pending)
	}

	client = &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "again"}, {Type: providers.EventDone},
	}}}}
	runner.Client = client
	if _, err := runner.RunWithCallback(ctx, result.NewMessages, nil); err != nil {
		t.Fatal(err)
	}
	invocationMessages := 0
	for _, message := range client.requests[0].Messages {
		if message.ToolInvocationID == invocation.ID {
			invocationMessages++
		}
	}
	if invocationMessages != 1 {
		t.Fatalf("durably present tool result was injected again: %+v", client.requests[0].Messages)
	}
	if pending, err := ledger.PendingProjection(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("durably present result was not reconciled as projected: %+v, %v", pending, err)
	}
}

// TestStreamRunner_ResetConversationUsageReflectsCompaction verifies that an
// out-of-loop history rewrite (out-of-loop history replacement) which calls
// ResetConversationUsage drops the cross-turn usage baseline to the compacted
// size immediately, instead of carrying the inflated pre-compaction estimate
// until the length heuristic in prepareUsageTracker happens to fire.
func TestStreamRunner_ResetConversationUsageReflectsCompaction(t *testing.T) {
	r := &StreamRunner{}

	preCompact := make([]providers.ChatMessage, 0, 40)
	for i := 0; i < 40; i++ {
		preCompact = append(preCompact, providers.ChatMessage{Role: "assistant", Content: strings.Repeat("x", 200)})
	}

	// Seed a large cross-turn baseline as if several big turns had landed.
	big := NewUsageTracker()
	big.RecordResponse(&providers.TokenUsage{InputTokens: 50000, OutputTokens: 2000})
	r.commitUsageTracker(big, preCompact)

	pre, _ := r.prepareUsageTracker(preCompact)
	if got := pre.EstimateCurrent(); got != 52000 {
		t.Fatalf("expected inflated pre-compaction estimate 52000, got %d", got)
	}

	// Out-of-loop compaction replaces history with a small joint summary.
	compacted := []providers.ChatMessage{{Role: "user", Content: "bounded replacement summary"}}
	r.ResetConversationUsage(compacted)

	want := estimateMessages(compacted)
	if want <= 0 || want >= 52000 {
		t.Fatalf("compacted estimate sanity check failed: %d", want)
	}
	if got := r.conversationUsage.EstimateCurrent(); got != want {
		t.Fatalf("baseline after reset = %d, want compacted estimate %d", got, want)
	}
	if got := r.conversationUsage.LastResponseTotal(); got != 0 {
		t.Fatalf("reset must clear the inflated ground truth, got %d", got)
	}

	// The next turn over the compacted history must not resurrect the inflated
	// estimate, and must not depend on the message-count-shrink heuristic.
	post, tracked := r.prepareUsageTracker(compacted)
	if got := post.EstimateCurrent(); got != want {
		t.Fatalf("post-compaction turn estimate = %d, want %d (no inflation)", got, want)
	}
	if tracked != len(compacted) {
		t.Fatalf("tracked history length = %d, want %d", tracked, len(compacted))
	}
}

// TestStreamRunner_ResetConversationUsageNilAndEmpty guards the edge cases the
// history rewrite can hit: a runner that never recorded usage, and an empty
// compacted history.
func TestStreamRunner_ResetConversationUsageNilAndEmpty(t *testing.T) {
	r := &StreamRunner{}
	// No prior usage recorded: must not panic and stays at zero.
	r.ResetConversationUsage(nil)
	if r.conversationUsage == nil {
		t.Fatal("ResetConversationUsage should allocate the tracker")
	}
	if got := r.conversationUsage.EstimateCurrent(); got != 0 {
		t.Fatalf("empty reset estimate = %d, want 0", got)
	}
	if r.trackedHistoryLen != 0 {
		t.Fatalf("tracked history length = %d, want 0", r.trackedHistoryLen)
	}
}

// TestSeedConversationUsageBaseline locks the resume-seeding contract: a
// rebuilt runner primes its baseline from persisted ground truth exactly once,
// and never clobbers live tracker state.
func TestSeedConversationUsageBaseline(t *testing.T) {
	r := &StreamRunner{}
	r.SeedConversationUsageBaseline(54_000, 30)
	r.usageMu.Lock()
	tracker := r.conversationUsage
	trackedLen := r.trackedHistoryLen
	r.usageMu.Unlock()
	if tracker == nil || tracker.EstimateCurrent() != 54_000 {
		t.Fatalf("EstimateCurrent = %d, want seeded 54000", tracker.EstimateCurrent())
	}
	if trackedLen != 30 {
		t.Fatalf("trackedHistoryLen = %d, want 30", trackedLen)
	}

	// A second seed (stale persisted row) must not override live state.
	r.SeedConversationUsageBaseline(10, 1)
	if tracker.EstimateCurrent() != 54_000 {
		t.Fatalf("EstimateCurrent = %d, want 54000 preserved over stale seed", tracker.EstimateCurrent())
	}

	// Live state also wins when pending estimates already accumulated.
	fresh := &StreamRunner{}
	fresh.usageMu.Lock()
	fresh.conversationUsage = NewUsageTracker()
	fresh.conversationUsage.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "hello there"}})
	fresh.usageMu.Unlock()
	before := fresh.conversationUsage.EstimateCurrent()
	fresh.SeedConversationUsageBaseline(99_000, 5)
	if fresh.conversationUsage.EstimateCurrent() != before {
		t.Fatalf("EstimateCurrent = %d, want pending-estimate state %d preserved", fresh.conversationUsage.EstimateCurrent(), before)
	}

	// Zero/negative totals are ignored.
	empty := &StreamRunner{}
	empty.SeedConversationUsageBaseline(0, 3)
	empty.usageMu.Lock()
	seeded := empty.conversationUsage
	empty.usageMu.Unlock()
	if seeded != nil && seeded.HasGroundTruth() {
		t.Fatal("zero total must not seed ground truth")
	}
}

func TestStreamRunner_PauseTurnContinuesTheTurn(t *testing.T) {
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "searching..."},
			{Type: providers.EventDone, StopReason: "pause_turn"},
		}},
		{events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "final answer"},
			{Type: providers.EventDone, StopReason: "end_turn"},
		}},
	}}
	runner := &StreamRunner{Client: client, Model: "test"}
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "final answer" {
		t.Fatalf("pause_turn must continue the turn, got %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("attempts = %d, want the paused turn to be resent once", client.callCount)
	}
}

// TestStreamRunner_ResetConversationUsagePreservesRetainedContext guards the
// exact regression where the desktop turn-entry path
// (ensureThreadRuntimeAfterAdmission) re-seeds usage on every turn via
// ResetConversationUsage. That reseed must not discard the cross-turn
// request-context state, or prompt-cache continuity breaks on every turn.
func TestStreamRunner_ResetConversationUsagePreservesRetainedContext(t *testing.T) {
	runner := &StreamRunner{Client: &mockStreamClient{}, Model: "m"}
	state := &RetainedRequestContextState{
		Messages: []RetainedContextMessage{{
			AfterDurable: 1,
			Message:      providers.ChatMessage{Role: "user", Name: "reminder", Content: "ctx", Hidden: true},
		}},
		DurableLen:  1,
		DurableHash: "hash",
	}
	runner.storeRetainedRequestContext(state)

	runner.ResetConversationUsage([]providers.ChatMessage{userMsg("reconstructed history")})

	if got := runner.takeRetainedRequestContext(); got != state {
		t.Fatalf("ResetConversationUsage must not clear retained request context: got %v", got)
	}
}

func TestStreamRunner_SynchronizeConversationUsagePreservesMatchingGroundTruth(t *testing.T) {
	history := []providers.ChatMessage{userMsg("ask"), {Role: "assistant", Content: "answer"}}
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, history)

	runner.SynchronizeConversationUsage(history, 7_000)

	got, tracked := runner.prepareUsageTracker(history)
	if got.EstimateCurrent() != 52_000 || tracked != len(history) {
		t.Fatalf("ordinary turn entry lost live usage ground truth: estimate=%d tracked=%d", got.EstimateCurrent(), tracked)
	}
}

func TestStreamRunner_SynchronizeConversationUsageAdoptsExternalRewrite(t *testing.T) {
	oldHistory := []providers.ChatMessage{userMsg("old"), {Role: "assistant", Content: "old answer"}}
	newHistory := []providers.ChatMessage{userMsg("rewritten"), {Role: "assistant", Content: "new answer"}}
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, oldHistory)

	runner.SynchronizeConversationUsage(newHistory, 9_000)

	got, tracked := runner.prepareUsageTracker(newHistory)
	if got.EstimateCurrent() != 9_000 || tracked != len(newHistory) {
		t.Fatalf("external rewrite did not adopt persisted usage: estimate=%d tracked=%d", got.EstimateCurrent(), tracked)
	}
}

func TestStreamRunner_SynchronizeConversationUsageAdoptsExternalCompletedTurn(t *testing.T) {
	oldHistory := []providers.ChatMessage{userMsg("old"), {Role: "assistant", Content: "old answer"}}
	newHistory := append(providers.CloneChatMessages(oldHistory),
		userMsg("external ask"),
		providers.ChatMessage{Role: "assistant", Content: "external answer"},
	)
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, oldHistory)

	runner.SynchronizeConversationUsage(newHistory, 9_000)

	got, tracked := runner.prepareUsageTracker(newHistory)
	breakdown := got.Breakdown()
	if breakdown.Total() != 9_000 || breakdown.Adjustment != UsageAdjustmentExternalRewriteSeed || tracked != len(newHistory) {
		t.Fatalf("external completed turn did not adopt persisted usage: usage=%+v tracked=%d", breakdown, tracked)
	}
}

func TestStreamRunner_SynchronizeConversationUsageRejectsRepeatedTailAnchor(t *testing.T) {
	oldHistory := []providers.ChatMessage{userMsg("old"), {
		Role: "assistant", Content: "Done", ProviderItemID: "msg-old", ProviderItemProvider: "openai-codex", ProviderItemModel: "gpt-test",
	}}
	newHistory := append(providers.CloneChatMessages(oldHistory),
		userMsg("external ask"),
		providers.ChatMessage{Role: "assistant", Content: "Done", ProviderItemID: "msg-new", ProviderItemProvider: "openai-codex", ProviderItemModel: "gpt-test"},
	)
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, oldHistory)

	runner.SynchronizeConversationUsage(newHistory, 52_000)

	got, tracked := runner.prepareUsageTracker(newHistory)
	breakdown := got.Breakdown()
	if breakdown.Total() != 52_000 || breakdown.Adjustment != UsageAdjustmentExternalRewriteSeed || tracked != len(newHistory) {
		t.Fatalf("repeated tail anchor retained stale usage: usage=%+v tracked=%d", breakdown, tracked)
	}
}

func TestStreamRunner_SynchronizeConversationUsageAllowsEarlierMatchingContent(t *testing.T) {
	current := providers.ChatMessage{
		Role: "assistant", Content: "Done", ProviderItemID: "msg-current", ProviderItemProvider: "openai-codex", ProviderItemModel: "gpt-test",
	}
	oldHistory := []providers.ChatMessage{userMsg("old"), current}
	expanded := []providers.ChatMessage{
		userMsg("expanded old ask"),
		{Role: "assistant", Content: "Done", ProviderItemID: "msg-earlier", ProviderItemProvider: "openai-codex", ProviderItemModel: "gpt-test"},
		userMsg("current ask"),
		current,
	}
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, oldHistory)

	runner.SynchronizeConversationUsage(expanded, 0)

	got, tracked := runner.prepareUsageTracker(expanded)
	breakdown := got.Breakdown()
	if breakdown.Total() != 52_000 || breakdown.Adjustment != UsageAdjustmentRequestShapeTailRebase || tracked != len(expanded) {
		t.Fatalf("earlier matching content discarded live usage: usage=%+v tracked=%d", breakdown, tracked)
	}
}

func TestStreamRunner_SynchronizeConversationUsageDoesNotDoubleCountPendingToolDelta(t *testing.T) {
	oldHistory := []providers.ChatMessage{userMsg("old"), {Role: "assistant", Content: "old answer"}}
	toolCall := providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{}`}}}
	toolResult := providers.ChatMessage{Role: "tool", Name: "read_file", ToolCallID: "call-1", Content: "result"}
	newHistory := append(providers.CloneChatMessages(oldHistory), toolCall, toolResult)
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000})
	tracker.RecordPendingMessages([]providers.ChatMessage{toolCall, toolResult})
	runner.commitUsageTracker(tracker, oldHistory)

	runner.SynchronizeConversationUsage(newHistory, 0)

	got, tracked := runner.prepareUsageTracker(newHistory)
	breakdown := got.Breakdown()
	want := estimateMessages(newHistory)
	if breakdown.LastResponseTotal != 0 || breakdown.PendingDelta != want || breakdown.Adjustment != UsageAdjustmentExternalRewriteEstimate || tracked != len(newHistory) {
		t.Fatalf("pending tool delta was retained across durable replay: usage=%+v want=%d tracked=%d", breakdown, want, tracked)
	}
}

func TestStreamRunner_PrepareUsageTrackerResetsSameLengthRewrite(t *testing.T) {
	original := []providers.ChatMessage{userMsg("old"), {Role: "assistant", Content: "old answer"}}
	rewritten := []providers.ChatMessage{userMsg("new"), {Role: "assistant", Content: "new answer"}}
	runner := &StreamRunner{}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 50_000, OutputTokens: 2_000})
	runner.commitUsageTracker(tracker, original)

	got, tracked := runner.prepareUsageTracker(rewritten)
	want := NewUsageTracker()
	want.RecordPendingMessages(rewritten)
	if got.EstimateCurrent() != want.EstimateCurrent() || tracked != len(rewritten) {
		t.Fatalf("same-length rewrite kept stale usage: estimate=%d want=%d tracked=%d", got.EstimateCurrent(), want.EstimateCurrent(), tracked)
	}
}

// TestStreamRunner_CrossTurnContinuitySurvivesUsageSynchronization reproduces the real
// desktop path end to end: run a turn that emits request-only context, apply
// the per-turn usage synchronization the app-server performs at turn
// entry, then run the next turn and assert its first provider request
// byte-extends the previous turn's last request (prompt-cache continuity).
func TestStreamRunner_CrossTurnContinuitySurvivesUsageSynchronization(t *testing.T) {
	activeFiles := wuucontext.Block{
		Kind:    wuucontext.BlockActiveFiles,
		Title:   "Active files",
		Source:  "runtime.active_files",
		Content: "files:\n- go.mod",
	}
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file"}},
				{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file", Arguments: `{"path":"README.md"}`}},
				{Type: providers.EventDone},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done turn 1"},
				{Type: providers.EventDone},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done turn 2"},
				{Type: providers.EventDone},
			}},
		},
	}
	runner := &StreamRunner{
		Client: client,
		Model:  "m",
		Tools: &fakeLoopTools{
			defs:    []providers.ToolDefinition{{Name: "read_file"}},
			results: map[string]string{"call-read": `{"content":"hi"}`},
		},
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextBlocks([]wuucontext.Block{activeFiles})
		},
	}

	history1 := []providers.ChatMessage{userMsg("first ask")}
	res1, err := runner.RunWithCallback(context.Background(), history1, nil)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	boundary := len(client.requests)
	if boundary < 2 {
		t.Fatalf("turn 1 should have made at least two requests, got %d", boundary)
	}
	turn1Last := client.requests[boundary-1].Messages

	// Rebuild next-turn history the way the app-server does: prior durable
	// history + this turn's new durable messages + the new user prompt.
	history2 := append(append(append([]providers.ChatMessage(nil), history1...), res1.NewMessages...), userMsg("second ask"))

	// Simulate the app-server synchronization that runs at turn entry.
	runner.SynchronizeConversationUsage(history2, 0)

	if _, err := runner.RunWithCallback(context.Background(), history2, nil); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if len(client.requests) <= boundary {
		t.Fatalf("turn 2 produced no request")
	}
	turn2First := client.requests[boundary].Messages

	if len(turn2First) < len(turn1Last) || !equalChatMessages(turn1Last, turn2First[:len(turn1Last)]) {
		t.Fatalf("turn 2 first request must byte-extend turn 1 last request:\nturn1Last=%+v\nturn2First=%+v", turn1Last, turn2First)
	}
	if got := countMessagesContaining(turn2First, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("retained request-only context should appear exactly once across turns, got %d in %+v", got, turn2First)
	}
}

// TestStreamRunner_DerivedLedgersDoNotReappearAcrossTurns locks the issue-128
// acceptance behavior: a derived ledger sent by an earlier turn must not ride
// the retained stream into later turns once the producer stops emitting it,
// and the dropped key must not earn an inactive tombstone on every turn.
func TestStreamRunner_DerivedLedgersDoNotReappearAcrossTurns(t *testing.T) {
	t.Setenv(wuucontext.DerivedContextLedgersEnvVar, "")
	ledger := wuucontext.Block{
		Kind: wuucontext.BlockActiveFiles, Title: "Active files", Source: "read_file", Content: "files:\n- go.mod",
	}
	client := &mockStreamClient{attempts: []mockStreamAttempt{
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "turn one"}, {Type: providers.EventDone}}},
		{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "turn two"}, {Type: providers.EventDone}}},
	}}
	runner := &StreamRunner{
		Client: client, Model: "m",
		BeforeRequestContext: func() []ContextSegment { return RequestOnlyContextBlocks([]wuucontext.Block{ledger}) },
	}
	history1 := []providers.ChatMessage{userMsg("first ask")}
	res1, err := runner.RunWithCallback(context.Background(), history1, nil)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if got := countMessagesContaining(client.requests[0].Messages, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("turn 1 should carry the ledger, got %d", got)
	}

	// Post-upgrade turn: the producer no longer emits the ledger.
	runner.BeforeRequestContext = func() []ContextSegment { return nil }
	history2 := append(append(providers.CloneChatMessages(history1), res1.NewMessages...), userMsg("second ask"))
	if _, err := runner.RunWithCallback(context.Background(), history2, nil); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	turn2 := client.requests[1].Messages
	if got := countMessagesContaining(turn2, "[ACTIVE_FILES]"); got != 0 {
		t.Fatalf("derived ledger must not reappear from retained state, got %d in %+v", got, turn2)
	}
	if got := countMessagesContaining(turn2, "status: inactive"); got != 0 {
		t.Fatalf("dropped ledgers must not earn tombstones, got %d in %+v", got, turn2)
	}
}

func TestStreamRunner_CanceledTurnRetainsPrefixForRetry(t *testing.T) {
	block := wuucontext.Block{
		Kind: wuucontext.BlockActiveFiles, Title: "Active files", Source: "read_file", Content: "files:\n- go.mod",
	}
	firstClient := &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "turn one"}, {Type: providers.EventDone},
	}}}}
	runner := &StreamRunner{
		Client: firstClient, Model: "m",
		BeforeRequestContext: func() []ContextSegment { return RequestOnlyContextBlocks([]wuucontext.Block{block}) },
	}
	history1 := []providers.ChatMessage{userMsg("first ask")}
	res1, err := runner.RunWithCallback(context.Background(), history1, nil)
	if err != nil {
		t.Fatal(err)
	}
	history2 := append(append(providers.CloneChatMessages(history1), res1.NewMessages...), userMsg("second ask"))

	canceledClient := &mockStreamClient{attempts: []mockStreamAttempt{{err: context.Canceled}}}
	runner.Client = canceledClient
	if _, err := runner.RunWithCallback(context.Background(), history2, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run error = %v", err)
	}
	if len(canceledClient.requests) != 1 {
		t.Fatalf("canceled run requests = %d", len(canceledClient.requests))
	}
	canceledRequest := canceledClient.requests[0].Messages

	retryClient := &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "retried"}, {Type: providers.EventDone},
	}}}}
	runner.Client = retryClient
	if _, err := runner.RunWithCallback(context.Background(), history2, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(retryClient.requests) != 1 || !reflect.DeepEqual(canceledRequest, retryClient.requests[0].Messages) {
		t.Fatalf("retry must reuse the canceled request prefix exactly:\ncanceled=%+v\nretry=%+v", canceledRequest, retryClient.requests)
	}
}

func equalChatMessages(a, b []providers.ChatMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
