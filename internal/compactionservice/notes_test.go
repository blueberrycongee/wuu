package compactionservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSelectedNoteOperationsDoNotFallBack(t *testing.T) {
	failure := &pluginhost.HostServiceError{Code: "note_unavailable", Message: "selected note unavailable"}
	calls := []string{}
	c := NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		return nil, failure
	})
	if _, err := c.NotePlan(context.Background(), NoteRequest{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := c.CompactNote(context.Background(), NoteRequest{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := c.Recover(context.Background(), RecoveryRequest{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0] != "note-plan" || calls[1] != "compact-note" || calls[2] != "recover" {
		t.Fatalf("unexpected recovery call: %v", calls)
	}
}

func TestNativeRecoveryDoesNotInferOnCancellation(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &countingModel{}
	ctx := WithModelClient(context.Background(), client)
	raw, _ := json.Marshal(RecoveryRequest{Failure: Failure{Code: "canceled", Message: "stopped"}})
	if _, err := s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "recover", Params: raw}); err == nil {
		t.Fatal("recovery swallowed cancellation")
	}
	if client.calls != 0 {
		t.Fatal("cancelled note recovery called model")
	}
	raw, _ = json.Marshal(NoteRequest{})
	if _, err := s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "note-plan", Params: raw}); err == nil {
		t.Fatal("absent note contribution synthesized plan")
	}
}

type countingModel struct{ calls int }

func (c *countingModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	c.calls++
	return providers.ChatResponse{}, errors.New("model unavailable")
}

func TestNativeProviderAdaptsV2NoteCapabilityAndRejectsStaleBinding(t *testing.T) {
	legacy := &noteCapability{}
	host := pluginhost.New(legacy)
	ctx := WithCapabilityHost(context.Background(), host)
	s := NewService()
	if err := s.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	policy, err := c.Describe(ctx, DescribeRequest{})
	if err != nil || !policy.NotesEnabled || policy.ContextWindows || policy.Key != "notes:agent.compaction" {
		t.Fatalf("policy %+v %v", policy, err)
	}
	plan, err := c.NotePlan(ctx, NoteRequest{PolicyKey: policy.Key, Messages: []providers.ChatMessage{{Role: "user", Content: "task"}}})
	if err != nil || plan.Prompt != "note policy" || plan.IntervalTokens != 100 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	replacement, err := c.CompactNote(ctx, NoteRequest{PolicyKey: policy.Key, Note: "checkpoint"})
	if err != nil || replacement.CoveredMessages != 1 || len(replacement.Messages) != 1 {
		t.Fatalf("replacement %+v %v", replacement, err)
	}
	if _, err = c.NotePlan(ctx, NoteRequest{PolicyKey: "another:agent.compaction"}); err == nil {
		t.Fatal("stale policy silently rebound")
	}
}

type noteCapability struct{}

func (*noteCapability) ID() string { return "notes" }
func (*noteCapability) Status() pluginhost.Status {
	return pluginhost.Status{ID: "notes", State: pluginhost.StateActive}
}
func (*noteCapability) Close(context.Context) error { return nil }
func (*noteCapability) ProtocolVersion() int        { return pluginhost.CapabilityProtocolVersion }
func (*noteCapability) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentCompaction, Kind: "decision", Version: 2, Priority: 10}}
}
func (*noteCapability) InvokeCapability(_ context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var in pluginhost.CompactionInput
	if err := json.Unmarshal(p.Input, &in); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	out := pluginhost.CompactionOutput{NotePrompt: "note policy", CheckpointIntervalTokens: 100, MaxNoteBytes: 500}
	if in.Operation == "compact_with_note" {
		out.Messages = []providers.ChatMessage{{Role: "user", Content: in.Note}}
		out.CoveredMessages = 1
	}
	raw, err := json.Marshal(out)
	return pluginhost.CapabilityInvokeResult{Output: raw}, err
}
