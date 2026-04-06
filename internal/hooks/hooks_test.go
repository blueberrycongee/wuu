package hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestRun_SuccessfulCommand(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "*", Command: "true"},
	}
	err := Run(context.Background(), entries, "any_tool", nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestRun_BlockedCommand(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "*", Command: "exit 2"},
	}
	err := Run(context.Background(), entries, "any_tool", nil)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got: %v", err)
	}
}

func TestRun_FailedCommand(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "*", Command: "exit 1"},
	}
	err := Run(context.Background(), entries, "any_tool", nil)
	if err == nil {
		t.Fatal("expected error for exit 1")
	}
	if errors.Is(err, ErrBlocked) {
		t.Fatal("exit 1 should not be ErrBlocked")
	}
}

func TestRun_ToolNameFiltering(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "read_file", Command: "exit 2"},
	}

	// Should not match a different tool name.
	err := Run(context.Background(), entries, "write_file", nil)
	if err != nil {
		t.Fatalf("expected no error for non-matching tool, got: %v", err)
	}

	// Should match the specified tool name.
	err = Run(context.Background(), entries, "read_file", nil)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked for matching tool, got: %v", err)
	}
}

func TestRun_WildcardMatchesAll(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "*", Command: "true"},
	}
	err := Run(context.Background(), entries, "anything", nil)
	if err != nil {
		t.Fatalf("wildcard should match all tools: %v", err)
	}
}

func TestRun_EmptyToolMatchesAll(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "", Command: "true"},
	}
	err := Run(context.Background(), entries, "anything", nil)
	if err != nil {
		t.Fatalf("empty tool pattern should match all: %v", err)
	}
}

func TestRun_EnvVarsPassed(t *testing.T) {
	entries := []config.HookEntry{
		{Tool: "*", Command: "test \"$WUU_TEST_VAR\" = \"hello\""},
	}
	env := map[string]string{"WUU_TEST_VAR": "hello"}
	err := Run(context.Background(), entries, "tool", env)
	if err != nil {
		t.Fatalf("expected env var to be passed: %v", err)
	}
}

func TestRun_NoEntries(t *testing.T) {
	err := Run(context.Background(), nil, "tool", nil)
	if err != nil {
		t.Fatalf("expected nil for empty entries: %v", err)
	}
}
