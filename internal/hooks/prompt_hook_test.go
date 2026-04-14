package hooks

import (
	"context"
	"errors"
	"testing"
)

type mockModelClient struct {
	response string
	err      error
}

func (m *mockModelClient) ChatJSON(_ context.Context, _, _, _ string) (string, error) {
	return m.response, m.err
}

func TestPromptHook_OK(t *testing.T) {
	h := &PromptHook{
		PromptTemplate: "Is this safe? $ARGUMENTS",
		Client:         &mockModelClient{response: `{"ok": true, "reason": "looks safe"}`},
	}
	out, err := h.Execute(context.Background(), &Input{ToolName: "bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.IsBlocked() {
		t.Error("expected non-blocking result")
	}
	if out.Context != "looks safe" {
		t.Errorf("expected context 'looks safe', got %q", out.Context)
	}
}

func TestPromptHook_Block(t *testing.T) {
	h := &PromptHook{
		PromptTemplate: "Is this safe? $ARGUMENTS",
		Client:         &mockModelClient{response: `{"ok": false, "reason": "dangerous command"}`},
	}
	out, err := h.Execute(context.Background(), &Input{ToolName: "bash"})
	if err == nil {
		t.Fatal("expected blocking error")
	}
	if !IsBlocked(err) {
		t.Error("expected ErrBlocked")
	}
	if out.Decision != "block" {
		t.Error("expected block decision")
	}
	if out.Reason != "dangerous command" {
		t.Errorf("expected reason 'dangerous command', got %q", out.Reason)
	}
}

func TestPromptHook_ModelError(t *testing.T) {
	h := &PromptHook{
		PromptTemplate: "test",
		Client:         &mockModelClient{err: errors.New("api down")},
	}
	out, err := h.Execute(context.Background(), &Input{})
	if err != nil {
		t.Fatalf("model errors should be non-blocking, got: %v", err)
	}
	if out.IsBlocked() {
		t.Error("model errors should not block")
	}
}

func TestPromptHook_InvalidJSON(t *testing.T) {
	h := &PromptHook{
		PromptTemplate: "test",
		Client:         &mockModelClient{response: "not json"},
	}
	out, err := h.Execute(context.Background(), &Input{})
	if err != nil {
		t.Fatalf("parse errors should be non-blocking, got: %v", err)
	}
	if out.IsBlocked() {
		t.Error("parse errors should not block")
	}
}

func TestPromptHook_NilClient(t *testing.T) {
	h := &PromptHook{PromptTemplate: "test"}
	out, err := h.Execute(context.Background(), &Input{})
	if err != nil {
		t.Fatalf("nil client should pass through, got: %v", err)
	}
	if out.IsBlocked() {
		t.Error("nil client should not block")
	}
}
