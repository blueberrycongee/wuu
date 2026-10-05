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
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	codeModeExecToolName = "run_code"
	// Reserve room for an immutable artifact reference and recovery cursor.
	minCodeModeOutputTokens = 1024
	maxCodeModeOutputTokens = 32768
)

// CodeModeExecTool orchestrates tools without occupying a leaf execution slot.
// All effects pass through nested tool authorization and execution.
type CodeModeExecTool struct{ toolkit *Toolkit }

func NewCodeModeExecTool(t *Toolkit) *CodeModeExecTool { return &CodeModeExecTool{t} }
func (*CodeModeExecTool) Name() string                 { return codeModeExecToolName }
func (e *CodeModeExecTool) IsReadOnly() bool {
	// Preserve the program-level review gate as well as per-call authorization.
	return e.toolkit.boundary.Enforce && !e.toolkit.boundary.AllowMutations
}
func (*CodeModeExecTool) IsConcurrencySafe() bool    { return false }
func (*CodeModeExecTool) IsOrchestrator(string) bool { return true }
func (*CodeModeExecTool) Execute(context.Context, string) (string, error) {
	return "", errors.New("run_code requires the rich tool execution path")
}
func (*CodeModeExecTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        codeModeExecToolName,
		Description: "Execute an async JavaScript or erasable TypeScript function body in a fresh tool-only interpreter. No filesystem, network, process, module imports, or timers are available. Discover bindings with await searchTools(query, {limit:8, offset:0}); it returns {tools:[{name,description}],total,next_offset?}. An empty query pages through the catalog. Read exact arguments with await describeTool(name), returning {name,description,input_schema}. Invoke await tools[name](args). Names are exact; bracket access handles punctuation. Calls return canonical tool-result objects with content, optional structured_content, and optional model_text display view, which may omit data. Failures reject with ToolCallError (toolName, message, and result for completed tool failures). Use text(result), console.log(result), or return result to emit one compact display view of a tool result, also when nested in arrays or objects. Raw content and structured_content remain available for computation and store; explicitly selected fields and copied or loaded objects are ordinary JSON data. Strings print as text; other values print as JSON. Emit only what is needed; intermediate values stay out of the conversation. Successful image/audio results are attached automatically. Await writes and dependent calls sequentially; use bounded Promise.all for independent reads. store(key,value), load(key), and remove(key) manage lossless JSON checkpoints scoped to this conversation, actor and workspace. Values are cloned; load returns undefined for a missing key. Only successful programs commit state; tool effects are not transactional. State is memory-only, limited to 1 MiB and 256 keys per scope; use remove to reclaim it. Do not store credentials. There is no JS continuation: await all calls before returning. For long work use bash run_in_background and process read/write/stop in later programs; keep their handles with store. Separately advertised interaction, artifact delivery and lifecycle controls must be called directly. There is no default elapsed deadline; an explicit timeout includes approval/tool waits. Cancellation stops active calls but cannot undo completed effects. Failures include bounded call outcome summaries without arguments or results; interrupted calls have unknown effects. Never blindly replay a failed program.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"code"}, "properties": map[string]any{
			"code":              map[string]any{"type": "string", "description": "Async function body. Type annotations are erased; enum and namespaces are unsupported."},
			"description":       map[string]any{"type": "string", "description": "Optional short description of this program."},
			"timeout_ms":        map[string]any{"type": "integer", "minimum": 1, "maximum": codemode.MaxTimeoutMS, "description": "Optional total elapsed timeout in milliseconds, including approval and tool waits. Omit for no program deadline."},
			"max_output_tokens": map[string]any{"type": "integer", "minimum": minCodeModeOutputTokens, "maximum": maxCodeModeOutputTokens, "description": "Estimated token budget for this program's emitted text (default 8192). Select data in JS before emitting; this does not limit intermediate tool data. Excess text is archived with a recovery cursor. Earlier results are never resized."},
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
		Code            string `json:"code"`
		Description     string `json:"description"`
		TimeoutMS       *int   `json:"timeout_ms"`
		MaxOutputTokens *int   `json:"max_output_tokens"`
	}
	if err := decodeArgs(call.Arguments, &args); err != nil {
		return toolresult.Result{}, err
	}
	if strings.TrimSpace(args.Code) == "" {
		return toolresult.Result{}, errors.New("run_code requires non-empty code")
	}
	if args.MaxOutputTokens != nil && (*args.MaxOutputTokens < minCodeModeOutputTokens || *args.MaxOutputTokens > maxCodeModeOutputTokens) {
		return toolresult.Result{}, fmt.Errorf("run_code max_output_tokens must be between %d and %d", minCodeModeOutputTokens, maxCodeModeOutputTokens)
	}
	timeout := 0
	if args.TimeoutMS != nil {
		if *args.TimeoutMS <= 0 {
			return toolresult.Result{}, errors.New("run_code timeout_ms must be positive when provided")
		}
		timeout = *args.TimeoutMS
	}
	definitions, err := e.toolkit.CodeModeNestedSurface()
	if err != nil {
		return toolresult.Result{}, err
	}
	if remote, ok := e.toolkit.env.ExecutionEnvironment.(interface {
		RunCode(context.Context, executionenv.CodeRequest, toolctx.NestedExecutor) (codemode.RunResult, error)
	}); ok {
		result, err := remote.RunCode(ctx, executionenv.CodeRequest{GitAttributionEnabled: !e.toolkit.env.GitAttributionDisabled, Program: codemode.RunRequest{Code: args.Code, Tools: definitions}, TimeoutMS: timeout, Actor: e.toolkit.env.AgentID, PermissionMode: e.toolkit.env.PermissionMode}, executor)
		if err != nil {
			return toolresult.Result{}, err
		}
		return codeModeResponseResult(result), nil
	}
	policy, _, err := e.toolkit.env.processSandboxPolicy(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	cwd, err := e.toolkit.env.ExecRootDir(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	result, err := e.toolkit.CodeModeService().Run(ctx, codemode.RunRequest{Code: args.Code, TimeoutMS: timeout, Tools: definitions},
		codemode.RunOptions{StateScope: e.toolkit.codeModeStateScope(cwd), StateOwner: e.toolkit.env.CodeModeStateOwner, CWD: cwd, Executor: executor, Sandbox: policy, SandboxProvider: e.toolkit.env.ProcessSandboxProvider})
	if err != nil {
		return toolresult.Result{}, err
	}
	return codeModeResponseResult(result), nil
}
func codeModeResponseResult(response codemode.RunResult) toolresult.Result {
	parts := append([]string{}, response.Logs...)
	if len(response.Value) > 0 {
		var text string
		if json.Unmarshal(response.Value, &text) == nil && string(response.Value) != "null" {
			parts = append(parts, text)
		} else {
			parts = append(parts, string(response.Value))
		}
	}
	if response.Error != "" {
		parts = append(parts, "PTC failed: "+response.Error)
		if response.CallSummary != "" {
			parts = append(parts, response.CallSummary)
		}
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
	if surface.ProfileName == "" || !t.CodeModeOnly() {
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
		if (d.Name == codeModeExecToolName || d.DirectOnly) && t.SupportsTool(d.Name) {
			if d.Name == codeModeExecToolName {
				d.Description += t.codeModeToolCatalog()
			}
			out = append(out, d)
		}
	}
	return out
}
func (t *Toolkit) codeModeToolCatalog() string {
	nested := t.codeModeNestedDefinitions()
	sort.Slice(nested, func(i, j int) bool { return nested[i].Name < nested[j].Name })
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nAvailable bindings: %d. Preview only; use searchTools and describeTool for complete discovery and schemas.\n", len(nested))
	shown := 0
	for _, tool := range nested {
		summary := []rune(strings.Join(strings.Fields(tool.Description), " "))
		if len(summary) > 120 {
			summary = append(summary[:119], '…')
		}
		line := fmt.Sprintf("- %s: %s\n", tool.Name, string(summary))
		if b.Len()+len(line) > 6*1024 {
			continue
		}
		b.WriteString(line)
		shown++
	}
	if shown < len(nested) {
		fmt.Fprintf(&b, "%d more bindings available through searchTools.\n", len(nested)-shown)
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
	definitions := t.codeModeNestedDefinitions()
	out := make([]codemode.ToolDefinition, 0, len(definitions))
	for _, d := range definitions {
		definition, err := codeModeToolDefinition(d)
		if err != nil {
			return nil, err
		}
		out = append(out, definition)
	}
	return out, nil
}

func (t *Toolkit) codeModeNestedDefinitions() []providers.ToolDefinition {
	t.refreshMCPToolSnapshot(false)
	all := t.registry.Definitions()
	for _, tool := range t.mcpToolsSnapshot() {
		all = append(all, tool.Definition())
	}
	out := make([]providers.ToolDefinition, 0, len(all))
	for _, d := range all {
		if d.Name == codeModeExecToolName || d.DirectOnly || d.Name == "tool_search" || !t.SupportsTool(d.Name) {
			continue
		}
		out = append(out, d)
	}
	t.codeModeMu.RLock()
	additional := t.codeModeAdditionalTools
	t.codeModeMu.RUnlock()
	if additional != nil {
		for _, definition := range additional() {
			if definition.Name != codeModeExecToolName && !definition.DirectOnly && definition.Name != "tool_search" && !t.IsToolDisabled(definition.Name) {
				out = append(out, definition)
			}
		}
	}
	return out
}

func codeModeToolDefinition(d providers.ToolDefinition) (codemode.ToolDefinition, error) {
	schema, err := json.Marshal(d.InputSchema)
	if err != nil {
		return codemode.ToolDefinition{}, err
	}
	return codemode.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: schema}, nil
}

// CodeModeDirectCallAllowed is the shared top-level routing policy. Availability
// and authorization are checked separately at dispatch time.
func (t *Toolkit) CodeModeDirectCallAllowed(name string) bool {
	if name == codeModeExecToolName {
		return true
	}
	tool := t.LookupTool(name)
	return tool != nil && tool.Definition().DirectOnly
}

// State is host-scoped and cannot address another conversation or workspace.
func (t *Toolkit) codeModeStateScope(executionRoot string) string {
	if t.env.SessionID == "" || t.env.SessionID == "session-pending" {
		return ""
	}
	identity, _ := json.Marshal([]string{t.env.SessionID, t.env.AgentID, executionRoot})
	return string(identity)
}
