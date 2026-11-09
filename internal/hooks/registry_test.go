package hooks

import (
	"testing"
	"time"
)

func TestRegistry_MatchDefaultEmpty(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		SessionStart: {
			{Command: "setup.sh"},
		},
	})
	hooks := r.Match(SessionStart, "")
	if len(hooks) != 1 {
		t.Fatalf("expected empty matcher to match, got %d", len(hooks))
	}
}

func TestRegistry_NoMatch(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: "check.sh"},
		},
	})
	hooks := r.Match(PreToolUse, "read_file")
	if len(hooks) != 0 {
		t.Fatalf("expected 0 hooks, got %d", len(hooks))
	}
}

func TestRegistry_MultipleHooks(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: "check.sh"},
			{Matcher: "*", Command: "log.sh"},
		},
	})
	hooks := r.Match(PreToolUse, "run_shell")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
}

func TestRegistry_CustomTimeout(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Command: "slow.sh", Timeout: 60},
		},
	})
	hooks := r.Match(PreToolUse, "any")
	if len(hooks) != 1 {
		t.Fatal("expected 1 hook")
	}
	ch, ok := hooks[0].(*CommandHook)
	if !ok {
		t.Fatal("expected CommandHook")
	}
	if ch.Timeout != 60*time.Second {
		t.Fatalf("expected 60s timeout, got %s", ch.Timeout)
	}
}

func TestRegistry_CaseInsensitive(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "Run_Shell", Command: "x"},
		},
	})
	if hooks := r.Match(PreToolUse, "run_shell"); len(hooks) != 1 {
		t.Fatal("expected case-insensitive match")
	}
}
