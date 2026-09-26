package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const codeModeExecToolName = "run_code"

// CodeModeExecTool orchestrates tools without occupying a leaf execution slot.
// Direct Node effects are confined by the same session policy as shell commands.
type CodeModeExecTool struct{ toolkit *Toolkit }

func NewCodeModeExecTool(t *Toolkit) *CodeModeExecTool { return &CodeModeExecTool{t} }
func (*CodeModeExecTool) Name() string                 { return codeModeExecToolName }
func (*CodeModeExecTool) IsReadOnly() bool             { return true }
func (*CodeModeExecTool) IsConcurrencySafe() bool      { return false }
func (*CodeModeExecTool) IsOrchestrator(string) bool   { return true }
func (*CodeModeExecTool) Execute(context.Context, string) (string, error) {
	return "", errors.New("run_code requires the rich tool execution path")
}
func (*CodeModeExecTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        codeModeExecToolName,
		Description: "Execute the body of an async TypeScript function in a fresh Node process. Use await tools[name](args) for the SDK bindings below. Calls return Wuu ToolResult objects: content contains text/media, and structured_content may carry JSON. Failed calls reject with ToolCallError (toolName and message); catch failures explicitly. Return a JSON value and/or console.log only the information needed for the next step. Intermediate tool values stay out of the conversation; successful image/audio results are attached automatically. Node APIs are available through await import(...); process.env starts empty. Direct filesystem writes obey this session's process sandbox. There is no retained state, yield or wait. Use bounded Promise.all for independent reads; await dependent work and writes sequentially. The elapsed deadline includes tool and approval waits. Inspect completed effects before retrying; programs are never replayed automatically.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"code", "description"}, "properties": map[string]any{
			"code":        map[string]any{"type": "string", "description": "Async function body. Type annotations are erased; enum and namespaces are unsupported."},
			"description": map[string]any{"type": "string", "description": "Short description of this program."},
			"timeout_ms":  map[string]any{"type": "integer", "minimum": 1, "maximum": codemode.MaxTimeoutMS, "description": fmt.Sprintf("Elapsed deadline; default %d ms, maximum %d ms.", codemode.DefaultTimeoutMS, codemode.MaxTimeoutMS)},
		}},
	}
}
func (e *CodeModeExecTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if !e.toolkit.CodeModeOnly() {
		return toolresult.Result{}, errors.New("PTC is disabled for this model")
	}
	executor, ok := toolctx.Nested(ctx)
	if !ok {
		return toolresult.Result{}, errors.New("run_code requires an orchestrator execution scope")
	}
	var args struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		TimeoutMS   int    `json:"timeout_ms"`
	}
	if err := decodeArgs(call.Arguments, &args); err != nil {
		return toolresult.Result{}, err
	}
	if strings.TrimSpace(args.Code) == "" || strings.TrimSpace(args.Description) == "" {
		return toolresult.Result{}, errors.New("run_code requires non-empty code and description")
	}
	definitions, err := e.toolkit.CodeModeNestedSurface()
	if err != nil {
		return toolresult.Result{}, err
	}
	policy, _, err := e.toolkit.env.processSandboxPolicy(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	cwd, err := e.toolkit.env.ExecRootDir(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	result, err := e.toolkit.CodeModeService().Run(ctx, codemode.RunRequest{Code: args.Code, TimeoutMS: args.TimeoutMS, Tools: definitions},
		codemode.RunOptions{CWD: cwd, Executor: executor, Sandbox: policy, SandboxProvider: e.toolkit.env.ProcessSandboxProvider})
	if err != nil {
		return toolresult.Result{}, err
	}
	return codeModeResponseResult(result), nil
}
func codeModeResponseResult(response codemode.RunResult) toolresult.Result {
	parts := append([]string{}, response.Logs...)
	if len(response.Value) > 0 {
		parts = append(parts, string(response.Value))
	}
	if response.Error != "" {
		parts = append(parts, "PTC failed: "+response.Error)
	}
	if len(parts) == 0 {
		parts = append(parts, "Program completed with no output.")
	}
	result := toolresult.FromText(strings.Join(parts, "\n"))
	result.IsError = response.Error != ""
	result.Content = append(result.Content, response.Media...)
	return result
}

func (t *Toolkit) withCodeModeSurface(surface capability.Surface) capability.Surface {
	if t.IsRoomAgent() || surface.ProfileName == "" || !t.CodeModeOnly() {
		return surface
	}
	out := cloneSurface(surface)
	if out.Tools == nil {
		out.Tools = map[string]capability.Capability{}
	}
	out.Tools[codeModeExecToolName] = capability.CapabilityCodeMode
	if !surfaceHasCapability(out.Capabilities, capability.CapabilityCodeMode) {
		out.Capabilities = append(out.Capabilities, capability.CapabilityCodeMode)
	}
	return out
}
func (t *Toolkit) codeModeEntryDefinitions() []providers.ToolDefinition {
	var out []providers.ToolDefinition
	for _, d := range t.registry.Definitions() {
		if (d.Name == codeModeExecToolName || d.Name == newContextToolName) && t.SupportsTool(d.Name) {
			if d.Name == codeModeExecToolName {
				d.Description += t.codeModeToolCatalog()
			}
			out = append(out, d)
		}
	}
	return out
}
func (t *Toolkit) codeModeToolCatalog() string {
	nested, err := t.CodeModeNestedSurface()
	if err != nil {
		return "\nTool catalog unavailable: " + err.Error()
	}
	sort.Slice(nested, func(i, j int) bool { return nested[i].Name < nested[j].Name })
	var b strings.Builder
	b.WriteString("\n\nProgram-only tool bindings (names are exact; use bracket access for punctuation):\n")
	for _, tool := range nested {
		fmt.Fprintf(&b, "\n### tools[%s]\n%s\nArguments JSON Schema: %s\n", strconv.Quote(tool.Name), tool.Description, tool.InputSchema)
	}
	return b.String()
}
func (t *Toolkit) SetCodeModeAdditionalTools(provider func() []providers.ToolDefinition) {
	t.codeModeMu.Lock()
	t.codeModeAdditionalTools = provider
	t.codeModeMu.Unlock()
}

// CodeModeNestedSurface preserves the active model family's edit primitives,
// restrictions and live extension tools without recursively exposing run_code.
func (t *Toolkit) CodeModeNestedSurface() ([]codemode.ToolDefinition, error) {
	t.refreshMCPToolSnapshot(false)
	all := t.registry.Definitions()
	for _, tool := range t.mcpToolsSnapshot() {
		all = append(all, tool.Definition())
	}
	out := make([]codemode.ToolDefinition, 0, len(all))
	for _, d := range all {
		if d.Name == codeModeExecToolName || d.Name == newContextToolName || !t.SupportsTool(d.Name) {
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
		return codemode.ToolDefinition{}, err
	}
	return codemode.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: schema}, nil
}
