package hooks

import (
	"testing"
	"time"
)

func TestRegistry_MatchExact(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: "check.sh"},
		},
	})
	hooks := r.Match(PreToolUse, "run_shell")
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
}

func TestRegistry_MatchWildcard(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "*", Command: "log.sh"},
		},
	})
	hooks := r.Match(PreToolUse, "write_file")
	if len(hooks) != 1 {
		t.Fatalf("expected wildcard to match, got %d", len(hooks))
	}
}

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

func TestRegistry_NoEntriesForEvent(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Command: "x"}},
	})
	if hooks := r.Match(Stop, ""); hooks != nil {
		t.Fatalf("expected nil, got %v", hooks)
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

func TestRegistry_DefaultTimeout(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Command: "x.sh"},
		},
	})
	hooks := r.Match(PreToolUse, "any")
	ch := hooks[0].(*CommandHook)
	if ch.Timeout != defaultTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultTimeout, ch.Timeout)
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

func TestRegistry_HasHooks(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Command: "x"}},
	})
	if !r.HasHooks(PreToolUse) {
		t.Fatal("expected HasHooks true")
	}
	if r.HasHooks(Stop) {
		t.Fatal("expected HasHooks false for Stop")
	}
}

func TestRegistry_NilInput(t *testing.T) {
	r := NewRegistry(nil)
	if r.HasHooks(PreToolUse) {
		t.Fatal("nil registry should have no hooks")
	}
	if hooks := r.Match(PreToolUse, "x"); hooks != nil {
		t.Fatal("expected nil match from empty registry")
	}
}
