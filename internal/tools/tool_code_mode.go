package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	codeModeExecToolName = "exec"
	codeModeWaitToolName = "wait"
)

// CodeModeExecTool is the model-visible entry into the code-mode runtime. The
// model emits a code-mode source program; the host executes it in its V8
// sandbox and the program drives Wuu tools through the nested execution
// pipeline. The tool call itself is an orchestrator: it performs no leaf
// operations and does not hold an execution slot while its cell is live.
type CodeModeExecTool struct{ toolkit *Toolkit }

func NewCodeModeExecTool(toolkit *Toolkit) *CodeModeExecTool {
	return &CodeModeExecTool{toolkit: toolkit}
}

func (*CodeModeExecTool) Name() string            { return codeModeExecToolName }
func (*CodeModeExecTool) IsReadOnly() bool        { return true }
func (*CodeModeExecTool) IsConcurrencySafe() bool { return true }
func (*CodeModeExecTool) IsOrchestrator() bool    { return true }

func (*CodeModeExecTool) Execute(context.Context, string) (string, error) {
	return "", errors.New("code-mode exec requires the rich tool execution path")
}

func (*CodeModeExecTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: codeModeExecToolName,
		Description: "Execute a code-mode program in Wuu's isolated runtime. The program receives the " +
			"enabled tools as tools.<name>(arguments) and runs until it yields or finishes. " +
			"Discover tools through ALL_TOOLS, whose entries contain name and description including the input schema. " +
			"Filter by name or description and print matching entries with text(), for example: " +
			"text(ALL_TOOLS.filter(t => /file|search/.test(t.name))); " +
			"Use await for tool calls and text(value) to return output, for example: " +
			"const result = await tools.read_file({path: 'README.md'}); text(result). Prefer this tool " +
			"for multi-step reasoning, data transformation, and batched tool orchestration; each program " +
			"starts with a clean sandbox (no process, network, or file access unless provided by tools). " +
			"After starting, use wait to collect output or terminate the cell.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"source"},
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Code-mode source program executed by the runtime. Tools are available as globals.",
				},
				"yield_time_ms": map[string]any{
					"type":        "number",
					"description": "Time to run before yielding the first output. Defaults to the session default.",
				},
				"max_output_tokens": map[string]any{
					"type":        "number",
					"description": "Output token budget for this execution.",
				},
			},
		},
	}
}

func (e *CodeModeExecTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	service := e.toolkit.CodeModeService()
	if service == nil {
		return toolresult.Result{}, errors.New("code mode is not enabled in this session")
	}
	executor, ok := toolctx.OutlivingNested(ctx)
	if !ok {
		return toolresult.Result{}, errors.New("code-mode exec requires an orchestrator execution scope")
	}
	var args struct {
		Source          string  `json:"source"`
		YieldTimeMS     *uint64 `json:"yield_time_ms"`
		MaxOutputTokens *int32  `json:"max_output_tokens"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return toolresult.Result{}, fmt.Errorf("invalid code-mode exec arguments: %w", err)
	}
	if args.Source == "" {
		return toolresult.Result{}, errors.New("code-mode exec requires source")
	}
	enabled, err := e.toolkit.CodeModeNestedSurface()
	if err != nil {
		return toolresult.Result{}, err
	}
	response, err := service.ExecuteBound(ctx, codemode.ExecuteRequest{
		ToolCallID:      call.ID,
		EnabledTools:    enabled,
		Source:          args.Source,
		YieldTimeMS:     args.YieldTimeMS,
		MaxOutputTokens: args.MaxOutputTokens,
	}, executor)
	if err != nil {
		return toolresult.Result{}, err
	}
	return codeModeResponseResult(response), nil
}

// CodeModeWaitTool collects output from a yielded exec cell or terminates it.
// The cell keeps executing between wait calls; the tool itself performs no
// leaf operations, so it never holds a leaf execution slot.
type CodeModeWaitTool struct{ toolkit *Toolkit }

func NewCodeModeWaitTool(toolkit *Toolkit) *CodeModeWaitTool {
	return &CodeModeWaitTool{toolkit: toolkit}
}

func (*CodeModeWaitTool) Name() string            { return codeModeWaitToolName }
func (*CodeModeWaitTool) IsReadOnly() bool        { return true }
func (*CodeModeWaitTool) IsConcurrencySafe() bool { return true }
func (*CodeModeWaitTool) IsOrchestrator() bool    { return true }

func (*CodeModeWaitTool) Execute(context.Context, string) (string, error) {
	return "", errors.New("code-mode wait requires the rich tool execution path")
}

func (*CodeModeWaitTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: codeModeWaitToolName,
		Description: "Wait on a yielded exec cell and return its new output or completion. The cell keeps " +
			"running between calls; use terminate to stop it explicitly.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"cell_id"},
			"properties": map[string]any{
				"cell_id": map[string]any{
					"type":        "string",
					"description": "Identifier of the running exec cell.",
				},
				"yield_time_ms": map[string]any{
					"type":        "number",
					"description": "Wait before yielding more output. Defaults to 10000 ms.",
				},
				"terminate": map[string]any{
					"type":        "boolean",
					"description": "True stops the running exec cell; false or omitted waits for output.",
				},
			},
		},
	}
}

func (w *CodeModeWaitTool) ExecuteResult(ctx context.Context, args string) (toolresult.Result, error) {
	service := w.toolkit.CodeModeService()
	if service == nil {
		return toolresult.Result{}, errors.New("code mode is not enabled in this session")
	}
	var request struct {
		CellID      string `json:"cell_id"`
		YieldTimeMS uint64 `json:"yield_time_ms"`
		Terminate   bool   `json:"terminate"`
	}
	if err := json.Unmarshal([]byte(args), &request); err != nil {
		return toolresult.Result{}, fmt.Errorf("invalid code-mode wait arguments: %w", err)
	}
	if request.CellID == "" {
		return toolresult.Result{}, errors.New("code-mode wait requires a cell_id")
	}
	var response codemode.Response
	var err error
	if request.Terminate {
		response, err = service.Terminate(ctx, request.CellID)
	} else {
		response, err = service.Wait(ctx, request.CellID, request.YieldTimeMS)
	}
	if err != nil {
		return toolresult.Result{}, err
	}
	return codeModeResponseResult(response), nil
}

func codeModeResponseResult(response codemode.Response) toolresult.Result {
	data, err := json.Marshal(struct {
		State          string                 `json:"state"`
		CellID         string                 `json:"cell_id"`
		Content        []codemode.ContentItem `json:"content_items"`
		ErrorText      *string                `json:"error_text,omitempty"`
		HostDurationNS uint64                 `json:"code_mode_host_duration_ns"`
		Missing        bool                   `json:"missing,omitempty"`
	}{
		State:          response.State,
		CellID:         response.CellID,
		Content:        response.Content,
		ErrorText:      response.ErrorText,
		HostDurationNS: response.HostDurationNS,
		Missing:        response.Missing,
	})
	if err != nil {
		return toolresult.FromErrorText(fmt.Sprintf("encode code-mode response: %v", err))
	}
	return toolresult.FromText(string(data))
}

// Code-mode entry tools belong to the runtime, independently of the model's
// leaf-tool profile. Project both discovery and execution from the same surface.
func (t *Toolkit) withCodeModeSurface(surface capability.Surface) capability.Surface {
	if t.IsRoomAgent() || surface.ProfileName == "" || t.CodeModeService() == nil {
		return surface
	}
	out := cloneSurface(surface)
	if out.Tools == nil {
		out.Tools = make(map[string]capability.Capability)
	}
	out.Tools[codeModeExecToolName] = capability.CapabilityCodeMode
	out.Tools[codeModeWaitToolName] = capability.CapabilityCodeMode
	if !surfaceHasCapability(out.Capabilities, capability.CapabilityCodeMode) {
		out.Capabilities = append(out.Capabilities, capability.CapabilityCodeMode)
	}
	return out
}

// Context switches remain top-level controls: nested cell output cannot signal
// the agent loop, and a live cell may yield before finishing its writes.
func (t *Toolkit) codeModeEntryDefinitions() []providers.ToolDefinition {
	all := t.registry.Definitions()
	out := make([]providers.ToolDefinition, 0, 2)
	for _, d := range all {
		if (d.Name == codeModeExecToolName || d.Name == codeModeWaitToolName || d.Name == newContextToolName) && t.SupportsTool(d.Name) {
			if d.Name == codeModeExecToolName {
				d.Description += t.codeModeToolCatalog()
			}
			out = append(out, d)
		}
	}
	return out
}

// Use the execution surface so profile changes and extension reloads are
// reflected in the next request, without modifying cached registry definitions.
func (t *Toolkit) codeModeToolCatalog() string {
	nested, err := t.CodeModeNestedSurface()
	if err != nil {
		return "\nTool catalog unavailable: " + err.Error()
	}
	sort.SliceStable(nested, func(i, j int) bool { return nested[i].Name < nested[j].Name })
	var b strings.Builder
	b.WriteString("\n\nAvailable tools (await tools.<name>(arguments); arguments follow each input JSON Schema):\n")
	for _, tool := range nested {
		fmt.Fprintf(&b, "\n### %s\n%s\n", codeModeGlobalName(tool.Name), tool.Description)
	}
	return b.String()
}

// Match the host's normalize_code_mode_identifier: ALL_TOOLS and tools use
// JavaScript identifiers even when the registered name contains punctuation.
func codeModeGlobalName(name string) string {
	var b strings.Builder
	for i, ch := range name {
		if ch == '_' || ch == '$' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// SetCodeModeAdditionalTools supplies live extension definitions for this toolkit.
// Thread clones bind their own provider so execution scopes and host reloads remain current.
func (t *Toolkit) SetCodeModeAdditionalTools(provider func() []providers.ToolDefinition) {
	t.codeModeMu.Lock()
	t.codeModeAdditionalTools = provider
	t.codeModeMu.Unlock()
}

// CodeModeNestedSurface lists every tool a code-mode cell may invoke. It is
// the underlying executable surface, not the model-visible one: Code Mode
// Only hides top-level entries from the model without disabling them, and
// live cells keep invoking the same tools through the nested pipeline. The
// code-mode entry tools themselves are excluded so a cell cannot recurse
// into another exec.
func (t *Toolkit) CodeModeNestedSurface() ([]codemode.ToolDefinition, error) {
	t.refreshMCPToolSnapshot(false)
	all := t.registry.Definitions()
	out := make([]codemode.ToolDefinition, 0, len(all))
	for _, d := range all {
		if d.Name == codeModeExecToolName || d.Name == codeModeWaitToolName || d.Name == newContextToolName || !t.SupportsTool(d.Name) {
			continue
		}
		definition, err := codeModeToolDefinition(d)
		if err != nil {
			return nil, err
		}
		out = append(out, definition)
	}
	for _, tool := range t.mcpToolsSnapshot() {
		d := tool.Definition()
		if d.Name == codeModeExecToolName || d.Name == codeModeWaitToolName || !t.SupportsTool(d.Name) {
			continue
		}
		definition, err := codeModeToolDefinition(d)
		if err != nil {
			return nil, err
		}
		out = append(out, definition)
	}
	t.codeModeMu.RLock()
	additional := t.codeModeAdditionalTools
	t.codeModeMu.RUnlock()
	if additional != nil {
		for _, d := range additional() {
			definition, err := codeModeToolDefinition(d)
			if err != nil {
				return nil, err
			}
			out = append(out, definition)
		}
	}
	return out, nil
}

func codeModeToolDefinition(d providers.ToolDefinition) (codemode.ToolDefinition, error) {
	schema, err := json.Marshal(d.InputSchema)
	if err != nil {
		return codemode.ToolDefinition{}, fmt.Errorf("encode code-mode tool schema for %q: %w", d.Name, err)
	}
	return codemode.ToolDefinition{
		Name:     d.Name,
		ToolName: codemode.ToolName{Name: d.Name},
		// Both prompt and runtime discovery carry the same argument contract.
		Description: d.Description + "\nInput schema: " + string(schema),
		Kind:        "function",
		InputSchema: schema,
	}, nil
}
