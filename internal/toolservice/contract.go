// Package toolservice defines the selected tool registry and execution service.
package toolservice

import (
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const ServiceName = "agent.tools"
const DefaultModule = "wuu.tools.default"

func Descriptor() pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, 3)
	for _, name := range []string{"list", "resolve", "resolve-own", "execute", "freeze", "unfreeze", "prepare", "finalize"} {
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

// PrepareRequest carries read-only model history at the model-step boundary.
type PrepareRequest struct {
	History []pluginprotocol.ModelMessageV1 `json:"history"`
}

// PrepareResult messages are appended to ordinary history; an empty array means
// the selected provider has no additions. Missing/null messages are invalid.
type PrepareResult struct {
	Messages []pluginprotocol.ModelMessageV1 `json:"messages"`
}

// FinalizeRequest settles the normalized result after execution/hook errors
// have been converted into ordinary tool results by the caller.
type FinalizeRequest struct {
	Call   providers.ToolCall `json:"call"`
	Result *toolresult.Result `json:"result"`
}

// ResolveOwnResult identifies a declaration in the selected provider generation.
// It is not proof that a target session policy exposes or permits this tool.
type ResolveOwnResult struct {
	Found      bool   `json:"found"`
	Name       string `json:"name,omitempty"`
	DirectOnly bool   `json:"direct_only,omitempty"`
}
type ResolveOwnRequest struct {
	ToolID string `json:"tool_id"`
}
