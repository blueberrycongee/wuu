package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/defaultpolicy"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/coder/websocket"
)

// Exercise the shipped policy through the same PolicyClient validation and
// transport planner used by the runtime, rather than the unselected fallback.
func selectedDefaultPolicy(t *testing.T) *loopdriver.PolicyClient {
	t.Helper()
	s := defaultpolicy.NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return loopdriver.NewPolicyClient(func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: pluginprotocol.AgentPolicyService, Method: "evaluate", Params: input})
	})
}

func TestSelectedDefaultPolicyRetriesInterruptedText(t *testing.T) {
	o := runChatStream(t, chatPartial, false, true)
	if o.err != nil || o.result.Content != "Complete answer" || o.requests != 2 || o.effects != 0 {
		t.Fatalf("partial response did not recover: %+v", o)
	}
	want := []string{"content_delta:Partial", "content_replace:", "content_delta:Complete answer"}
	if !reflect.DeepEqual(o.contentEvents, want) {
		t.Fatalf("failed output was not replaced on retry: got %v want %v", o.contentEvents, want)
	}
	for _, msg := range o.result.NewMessages {
		if strings.Contains(msg.Content, "Partial") {
			t.Fatalf("failed fragment leaked into recovered history: %+v", msg)
		}
	}
}

func TestSelectedDefaultPolicyResponsesWebSocketDropFallsBackToSSE(t *testing.T) {
	var wsRequests, sseRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			sseRequests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fresh answer\",\"item_id\":\"fresh\",\"output_index\":0}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fresh\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			return
		}
		wsRequests.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			t.Errorf("read model request: %v", err)
			return
		}
		for _, frame := range []string{
			`{"type":"response.created","response":{"id":"resp_partial","status":"in_progress"}}`,
			`{"type":"response.output_text.delta","delta":"old partial","item_id":"old","output_index":0}`,
		} {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				t.Errorf("write websocket event: %v", err)
				return
			}
		}
	}))
	defer server.Close()
	store := false
	client, err := openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "fixture", WireAPI: "responses", ResponsesStore: &store, ResponsesTransport: providers.StreamTransportAuto, ResponsesWebSocketCache: openai.NewResponsesWebSocketCache()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runner := &agent.StreamRunner{LoopDriver: agent.DefaultDriver{}, Client: client, Model: "fixture", PromptCacheKey: "selected-policy-responses", SelectedPolicy: selectedDefaultPolicy(t)}
	var contentEvents []string
	result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "task"}}, func(event providers.StreamEvent) {
		if event.Type == providers.EventContentReplace || (event.Type == providers.EventContentDelta && event.Content != "") {
			contentEvents = append(contentEvents, string(event.Type)+":"+event.Content)
		}
	})
	if err != nil || result.Content != "fresh answer" || wsRequests.Load() != 1 || sseRequests.Load() != 1 {
		t.Fatalf("unexpected fallback: ws=%d SSE=%d result=%+v err=%v", wsRequests.Load(), sseRequests.Load(), result, err)
	}
	want := []string{"content_delta:old partial", "content_replace:", "content_delta:fresh answer"}
	if !reflect.DeepEqual(contentEvents, want) {
		t.Fatalf("failed attempt was not reset: got %v want %v", contentEvents, want)
	}
}

func TestSelectedDefaultPolicyRejectsUnguardedPartialTransport(t *testing.T) {
	failure := loopdriver.FailureToWire(providers.NewIncompleteStreamError("fixture disconnected"))
	failure.Recovery = &pluginprotocol.DriverModelRecovery{AttemptCount: 1, MaxAttempts: 3, SubmissionCount: 1}
	decision, err := selectedDefaultPolicy(t).Evaluate(context.Background(), pluginprotocol.AgentPolicyInput{
		Phase: "transport_retry", Attempt: 1, MaxAttempts: 3, PartialOutput: true, Failure: failure,
	})
	if err != nil || decision.Action != "fail" {
		t.Fatalf("unguarded plugin stream may not be replayed: decision=%+v err=%v", decision, err)
	}
}

func TestSelectedDefaultPolicyDoesNotRetryTerminalFailure(t *testing.T) {
	o := runChatStream(t, chatPartial+strings.Replace(chatFailure, "server_error", "insufficient_quota", 1), false, true)
	if o.err == nil || o.requests != 1 || o.reconnects != 0 || o.effects != 0 {
		t.Fatalf("terminal error was retried: %+v", o)
	}
}

func TestSelectedDefaultPolicyDiscardsUnfinishedToolDraft(t *testing.T) {
	o := runChatStream(t, chatDraft+chatFailure, false, true)
	if o.err != nil || o.requests != 2 || o.effects != 0 || o.result.Content != "Complete answer" {
		t.Fatalf("failed tool draft was not safely discarded: %+v", o)
	}
	for _, message := range o.result.NewMessages {
		if message.Role == "tool" || len(message.ToolCalls) != 0 {
			t.Fatalf("failed tool draft entered history: %+v", message)
		}
	}
}

func TestSelectedDefaultPolicyNeverReplaysExecutedTool(t *testing.T) {
	o := runChatStream(t, chatDraft+chatToolFinish, true, true)
	if o.err == nil || o.requests != 1 || o.reconnects != 0 || o.effects != 1 {
		t.Fatalf("accepted tool was replayed or lost: %+v", o)
	}
}

type metadataRecoveryModel struct{ calls int }

func (*metadataRecoveryModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected unary request")
}

func (m *metadataRecoveryModel) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls++
	if req.Attempt.Valid() {
		_, _ = req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "fixture", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan providers.StreamEvent, 2)
	if m.calls == 1 {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, ProviderItemID: "stale-item", Phase: providers.MessagePhaseFinalAnswer}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: providers.NewIncompleteStreamError("fixture disconnected")}
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "fresh"}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(ch)
	return ch, nil
}

func TestSelectedDefaultPolicyClearsMetadataOnlyFailedAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	model := &metadataRecoveryModel{}
	runner := &agent.StreamRunner{LoopDriver: agent.DefaultDriver{}, Client: model, Model: "fixture", ProviderName: "fixture", SelectedPolicy: selectedDefaultPolicy(t)}
	result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "task"}}, nil)
	if err != nil || model.calls != 2 || result.Content != "fresh" {
		t.Fatalf("metadata-only failure did not recover: calls=%d result=%+v err=%v", model.calls, result, err)
	}
	for _, msg := range result.NewMessages {
		if msg.Role == "assistant" && (msg.ProviderItemID != "" || msg.Phase != "") {
			t.Fatalf("failed attempt's provider metadata leaked into successful output: %+v", msg)
		}
	}
}

func TestSelectedDefaultPolicyDoesNotReplayUnguardedPluginStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	model := &metadataRecoveryModel{}
	client := providers.NewReliableStreamClient(model, nil, providers.WithStreamRecoveryPlanner(selectedDefaultPolicy(t).StreamRecoveryPlanner()))
	stream, err := client.StreamChat(ctx, providers.ChatRequest{Provider: "fixture", Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "task"}}})
	if err != nil {
		t.Fatal(err)
	}
	var terminal error
	for event := range stream {
		if event.Type == providers.EventError {
			terminal = event.Error
		}
	}
	if terminal == nil || model.calls != 1 {
		t.Fatalf("unguarded stream silently replayed: calls=%d err=%v", model.calls, terminal)
	}
}

type checkpointRecoveryModel struct {
	requests []providers.ChatRequest
}

func (*checkpointRecoveryModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected unary request")
}

func (m *checkpointRecoveryModel) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.requests = append(m.requests, req)
	if req.Attempt.Valid() {
		_, _ = req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "fixture", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan providers.StreamEvent, 4)
	if len(m.requests) == 1 {
		checkpoint := providers.ChatMessage{Role: "assistant", Content: "completed checkpoint"}
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: checkpoint.Content}
		ch <- providers.StreamEvent{Type: providers.EventAssistantItemDone, Message: &checkpoint}
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "unfinished tail"}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: providers.NewIncompleteStreamError("fixture disconnected")}
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "continued"}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(ch)
	return ch, nil
}

func TestSelectedDefaultPolicyResumesCompletedCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	model := &checkpointRecoveryModel{}
	runner := &agent.StreamRunner{LoopDriver: agent.DefaultDriver{}, Client: model, Model: "fixture", ProviderName: "fixture", SelectedPolicy: selectedDefaultPolicy(t)}
	result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "task"}}, nil)
	if err != nil || len(model.requests) != 2 {
		t.Fatalf("checkpoint did not continue: calls=%d result=%+v err=%v", len(model.requests), result, err)
	}
	if !model.requests[1].ResumeSampling {
		t.Fatal("checkpoint was replayed instead of continuing sampling")
	}
	var checkpoint, displayFragment bool
	for _, msg := range result.NewMessages {
		if msg.Content == "unfinished tail" && msg.Cause == providers.IncompleteStreamFragment {
			displayFragment = true
		}
	}
	if !displayFragment {
		t.Fatalf("unfinished tail was not retained as display-only: %+v", result.NewMessages)
	}
	projected := providers.ResolveProviderHistory(model.requests[1].Messages, model.requests[1].Provider, model.requests[1].ProviderStateScope)
	for _, msg := range projected {
		checkpoint = checkpoint || msg.Content == "completed checkpoint"
		if strings.Contains(msg.Content, "unfinished tail") {
			t.Fatalf("unfinished tail entered continuation: %+v", msg)
		}
	}
	if !checkpoint {
		t.Fatalf("completed checkpoint missing from continuation: %+v", projected)
	}
}
