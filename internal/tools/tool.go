package tools

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Tool is the interface every tool must implement. It is the single
// abstraction the toolkit dispatches through — replacing the old
// switch-case monolith with a registry of self-describing tools.
//
// Design aligned with Claude Code's Tool<Input, Output> interface,
// simplified for Go idioms and wuu's highest-permission model.
type Tool interface {
	// Name returns the tool's unique identifier (e.g. "read_file").
	Name() string

	// Definition returns the JSON-schema tool definition sent to the
	// model. The Name field of the returned value must equal Name().
	Definition() providers.ToolDefinition

	// Execute runs the tool and returns a JSON result string.
	Execute(ctx context.Context, args string) (string, error)

	// IsReadOnly reports whether the tool never modifies the
	// filesystem or external state. Read-only tools can safely
	// run concurrently with other read-only tools.
	IsReadOnly() bool

	// IsConcurrencySafe reports whether multiple instances of this
	// tool can run in parallel without conflicting. A tool that is
	// read-only is implicitly concurrency-safe; this flag exists for
	// tools that write but whose writes don't conflict (rare).
	IsConcurrencySafe() bool
}

// Registry holds the set of available tools and provides name-based
// lookup. It replaces the old switch-case dispatch in Toolkit.Execute.
type Registry struct {
	tools []Tool
	index map[string]Tool
}

// NewRegistry builds a registry from the given tools. Panics on
// duplicate names — that's a programming error, not a runtime one.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools: tools,
		index: make(map[string]Tool, len(tools)),
	}
	for _, t := range tools {
		name := t.Name()
		if _, dup := r.index[name]; dup {
			panic("duplicate tool name: " + name)
		}
		r.index[name] = t
	}
	return r
}

// Lookup returns the tool with the given name, or nil.
func (r *Registry) Lookup(name string) Tool {
	return r.index[name]
}

// Definitions returns JSON-schema definitions for every registered
// tool, in registration order.
func (r *Registry) Definitions() []providers.ToolDefinition {
	defs := make([]providers.ToolDefinition, len(r.tools))
	for i, t := range r.tools {
		defs[i] = t.Definition()
	}
	return defs
}

// All returns all registered tools in registration order.
func (r *Registry) All() []Tool {
	out := make([]Tool, len(r.tools))
	copy(out, r.tools)
	return out
}
