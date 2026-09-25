package tools

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// stubTool is a minimal registry entry with a configurable definition,
// result, and declared capability.
type stubTool struct {
	name   string
	def    providers.ToolDefinition
	result string
	cap    capability.Capability
}

func (s *stubTool) Name() string                                    { return s.name }
func (s *stubTool) Definition() providers.ToolDefinition            { return s.def }
func (s *stubTool) Execute(context.Context, string) (string, error) { return s.result, nil }
func (s *stubTool) IsReadOnly() bool                                { return true }
func (s *stubTool) IsConcurrencySafe() bool                         { return true }
func (s *stubTool) DeclaredCapability() (capability.Capability, bool) {
	if s.cap == "" {
		return "", false
	}
	return s.cap, true
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
