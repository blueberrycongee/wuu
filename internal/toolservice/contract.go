// Package toolservice defines the selected tool registry and execution service.
package toolservice

import (
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const ServiceName = "agent.tools"
const DefaultModule = "wuu.tools.default"

func Descriptor() pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, 3)
	for _, name := range []string{"list", "resolve", "execute", "freeze", "unfreeze"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ServiceName + "." + name + ".input.v1", OutputSchema: ServiceName + "." + name + ".output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: ServiceName, Version: "1.0.0", Methods: methods}
}

// Definition is the stable service encoding, independent of model transports.
type Definition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	Freeform     bool           `json:"freeform,omitempty"`
	DirectOnly   bool           `json:"direct_only,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
	CacheStable  bool           `json:"cache_stable,omitempty"`
}

func (d Definition) ProviderDefinition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema, Freeform: d.Freeform, DirectOnly: d.DirectOnly, DeferLoading: d.DeferLoading, CacheStable: d.CacheStable}
}

type ListResult struct {
	Tools   []Definition                     `json:"tools"`
	Catalog []tools.DeferredToolCatalogEntry `json:"catalog"`
	// CatalogPrompt belongs to the selected provider, including its discovery policy.
	CatalogPrompt  string             `json:"catalog_prompt"`
	SystemFragment string             `json:"system_fragment"`
	Surface        capability.Surface `json:"surface"`
}
type CallRequest struct {
	Call providers.ToolCall `json:"call"`
}
type Metadata struct {
	Orchestrator    bool   `json:"orchestrator"`
	ReadOnly        bool   `json:"read_only"`
	ConcurrencySafe bool   `json:"concurrency_safe"`
	Destructive     bool   `json:"destructive"`
	Risk            string `json:"risk"`
	Reason          string `json:"reason"`
}

func (m Metadata) AgentMetadata() agent.ToolMetadata {
	return agent.ToolMetadata{Orchestrator: m.Orchestrator, ReadOnly: m.ReadOnly, ConcurrencySafe: m.ConcurrencySafe, Destructive: m.Destructive, Risk: m.Risk, Reason: m.Reason}
}

type ResolveResult struct {
	Found    bool                       `json:"found"`
	Metadata *Metadata                  `json:"metadata,omitempty"`
	Supports bool                       `json:"supports"`
	Display  *providers.ToolCallDisplay `json:"display,omitempty"`
}

// ExecutionResult preserves provider-owned post-tool state alongside rich output.
type ExecutionResult struct {
	Result            toolresult.Result                  `json:"result"`
	DiscoveredTools   []providers.LoadableToolDefinition `json:"discovered_tools,omitempty"`
	AdditionalContext string                             `json:"additional_context,omitempty"`
}
