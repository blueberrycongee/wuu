package tools

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// stubTool lets us count how many times Definition() is actually invoked,
// so we can assert the cache short-circuits subsequent calls.
type stubTool struct {
	name     string
	defCalls int
	def      providers.ToolDefinition
}

func (s *stubTool) Name() string                                 { return s.name }
func (s *stubTool) Definition() providers.ToolDefinition         { s.defCalls++; return s.def }
func (s *stubTool) Execute(context.Context, string) (string, error) { return "", nil }
func (s *stubTool) IsReadOnly() bool                             { return true }
func (s *stubTool) IsConcurrencySafe() bool                      { return true }

func TestRegistry_DefinitionsCachedAfterFirstCall(t *testing.T) {
	a := &stubTool{name: "a", def: providers.ToolDefinition{Name: "a", Description: "x"}}
	b := &stubTool{name: "b", def: providers.ToolDefinition{Name: "b", Description: "y"}}
	r := NewRegistry(a, b)

	first := r.Definitions()
	second := r.Definitions()

	if a.defCalls != 1 || b.defCalls != 1 {
		t.Fatalf("Definition() should be invoked exactly once per tool; got a=%d b=%d",
			a.defCalls, b.defCalls)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached result must be identical on repeat call:\n first =%+v\n second=%+v",
			first, second)
	}
	// Identity check: the same backing slice is returned (a new allocation
	// on each call would silently double our per-turn allocation budget).
	if &first[0] != &second[0] {
		t.Fatal("Definitions() must return the cached slice, not a per-call copy")
	}
}

func TestRegistry_DefinitionsByteEqualToRebuild(t *testing.T) {
	// Use real tools to catch regressions where a Definition() implementation
	// captures non-deterministic state (currently none do, but this pins it).
	env := &Env{RootDir: t.TempDir()}
	r := NewRegistry(
		NewGrepTool(env),
		NewReadFileTool(env),
		NewWriteFileTool(env),
	)

	cached := r.Definitions()

	// Rebuild manually to compare: this simulates the pre-cache behaviour.
	rebuilt := make([]providers.ToolDefinition, 0, len(cached))
	for _, tool := range r.All() {
		rebuilt = append(rebuilt, tool.Definition())
	}

	if !reflect.DeepEqual(cached, rebuilt) {
		t.Fatalf("cached definitions drifted from fresh build:\n cached =%+v\n rebuilt=%+v",
			cached, rebuilt)
	}
}

// TestRegistry_DefinitionsConcurrent verifies sync.Once guards correctness:
// under many concurrent first-callers, each tool's Definition() is still
// called exactly once, and every caller sees the same slice.
func TestRegistry_DefinitionsConcurrent(t *testing.T) {
	a := &stubTool{name: "a", def: providers.ToolDefinition{Name: "a"}}
	r := NewRegistry(a)

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make([]*providers.ToolDefinition, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			defs := r.Definitions()
			results[i] = &defs[0]
		}()
	}
	wg.Wait()

	if a.defCalls != 1 {
		t.Fatalf("sync.Once breached: Definition() called %d times", a.defCalls)
	}
	// All goroutines should have received the exact same element address.
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d saw a different backing slice than goroutine 0", i)
		}
	}
}

// BenchmarkRegistry_Definitions_Cached is the hot path: every agent turn
// calls Definitions() to ship tool schemas to the provider.
func BenchmarkRegistry_Definitions_Cached(b *testing.B) {
	env := &Env{RootDir: b.TempDir()}
	r := NewRegistry(
		NewReadFileTool(env),
		NewWriteFileTool(env),
		NewListFilesTool(env),
		NewEditFileTool(env),
		NewGrepTool(env),
		NewGlobTool(env),
		NewGitTool(env),
	)
	// Prime the cache.
	_ = r.Definitions()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Definitions()
	}
}
