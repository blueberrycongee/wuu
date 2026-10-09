package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	codeModeExecToolName    = "run_code"
	minCodeModeOutputTokens = 0
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
		Freeform:    true,
		Description: codeModeDescription,
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"input"}, "properties": map[string]any{
			"input": map[string]any{"type": "string", "description": "JavaScript source. Optional first-line // @run_code: JSON sets max_output_tokens, timeout_ms or description."},
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
	args, err := decodeCodeModeArguments(call.Arguments)
	if err != nil {
		return toolresult.Result{}, err
	}
	if err := args.ResultView.Validate(); err != nil {
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
		result, err := remote.RunCode(ctx, executionenv.CodeRequest{GitAttributionEnabled: !e.toolkit.env.GitAttributionDisabled, Program: codemode.RunRequest{ResultView: args.ResultView, Code: args.Code, Tools: definitions}, TimeoutMS: timeout, Actor: e.toolkit.env.AgentID, PermissionMode: e.toolkit.env.PermissionMode}, executor)
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
	result, err := e.toolkit.CodeModeService().Run(ctx, codemode.RunRequest{ResultView: args.ResultView, Code: args.Code, TimeoutMS: timeout, Tools: definitions},
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
		if t.CodeModeDirectCallAllowed(d.Name) && t.SupportsTool(d.Name) {
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
	fmt.Fprintf(&b, "\n\nAvailable bindings: %d. Core bindings below include their complete arguments; invoke them directly without a discovery call. Other bindings have previews; use describeTool for their exact arguments and searchTools for further discovery.\n", len(nested))
	var previews strings.Builder
	coreBytes := 0
	shown := 0
	for _, tool := range nested {
		if t.CodeModeDirectCallAllowed(tool.Name) {
			if tool.Freeform {
				fmt.Fprintf(&b, "\n- tools.%s({input: literalText}) invokes the directly advertised %s tool.\n", tool.Name, tool.Name)
			} else {
				fmt.Fprintf(&b, "\n- tools.%s uses the same arguments as the directly advertised %s tool.\n", tool.Name, tool.Name)
			}
			shown++
			continue
		}
		// File, search and command tools are used throughout ordinary coding
		// tasks. Publish their executable schemas once in the stable prefix
		// instead of making every session rediscover them in its history.
		switch classifyToolKind(tool.Name) {
		case ToolKindFile, ToolKindSearch, ToolKindShell:
			schema, err := json.Marshal(tool.InputSchema)
			if err == nil {
				entry := fmt.Sprintf("\n### %s\n%s\nInput JSON schema: %s\n", tool.Name, tool.Description, schema)
				if coreBytes+len(entry) <= 12*1024 {
					b.WriteString(entry)
					coreBytes += len(entry)
					shown++
					continue
				}
			}
		}
		summary := []rune(strings.Join(strings.Fields(tool.Description), " "))
		if len(summary) > 120 {
			summary = append(summary[:119], '…')
		}
		line := fmt.Sprintf("- %s: %s\n", tool.Name, string(summary))
		if previews.Len()+len(line) > 6*1024 {
			continue
		}
		previews.WriteString(line)
		shown++
	}
	if previews.Len() > 0 {
		b.WriteString("\nOther binding previews:\n")
		b.WriteString(previews.String())
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
	// Ordinary file and command operations do not require a program wrapper.
	// The same tools remain composable when their arguments depend on results.
	switch name {
	case "read_file", "write_file", "edit_file", "bash", "process":
		return true
	}
	return t.codeModeDirectOnly(name)
}

func (t *Toolkit) codeModeDirectOnly(name string) bool {
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

const codeModeDescription = `Run JavaScript or erasable TypeScript to compose tool calls in a fresh, isolated async function body.

Execution:
- Call await tools.name(args). No direct filesystem, network, process, imports or timers; use tools for effects.
- Call the directly advertised file and command tools for individual operations. Use this tool for dependent sequences, parallel independent work, or processing results before displaying them. Literal file content can go directly to write_file or edit_file without JavaScript evaluation.
- Use the core binding declarations below directly. Find other tools with await searchTools(query, {limit:8, offset:0}); inspect exact arguments with await describeTool(name). Tool names are exact; bracket access handles punctuation.
- Await dependent calls and writes in order within the same program when the next action is already known; return to the model when an observation requires a new decision. Use bounded Promise.all for independent reads. Await every call before returning: programs have no continuation and tool effects are not transactional.
- For long work use bash run_in_background and process handles. Directly advertised interaction and lifecycle tools stay outside programs.

Text and output:
- Treat nested source as literal data. Ordinary JS strings process backslashes; template literals interpolate ${...}, including String.raw templates. A quoted shell heredoc protects against shell expansion only, after JavaScript has parsed the command. Preserve the intended bytes at each language boundary.
- text(value), console.log(value), or return value emits output. Strings print literally; other values print as JSON. Keep intermediate data private and print only evidence needed for the next decision.
- Image and audio results from nested tools are forwarded automatically and share one result size limit. Use small media batches; max_output_tokens controls text only.
- Tool results contain content and optional structured_content. Use these fields for filtering and computation. Successful image/audio results attach automatically.
- Printing a tool result emits structured_content, or content when no structured data exists. The program output budget applies after selection; producer pagination and transport limits remain explicit in the result.

Options:
- Optional first line: // @run_code: {"max_output_tokens": 14000}
- max_output_tokens: 0–32768, default 8192. Bounds emitted text, not intermediate values. Excess output is archived for recovery; earlier observations never change. Tiny budgets retain essential recovery metadata.
- timeout_ms: positive milliseconds; omitted means no elapsed deadline. Includes approval and tool waits. description is an optional short activity label.

State and failures:
- store(key,value), load(key), remove(key) manage cloned JSON checkpoints scoped to this conversation, actor and workspace. Missing keys return undefined. Only successful programs commit state; storage is memory-only, limited to 1 MiB and 256 keys. Do not store credentials.
- Failures reject with ToolCallError (toolName, message, and result for completed tool failures). Failure receipts list bounded call outcomes. Cancellation cannot undo completed effects; interrupted calls have unknown effects. Inspect before retrying; never blindly replay a failed program.`

type codeModeArguments struct {
	Code            string              `json:"code"`
	Input           *string             `json:"input"`
	Description     string              `json:"description"`
	TimeoutMS       *int                `json:"timeout_ms"`
	MaxOutputTokens *int                `json:"max_output_tokens"`
	ResultView      codemode.ResultView `json:"result_view"`
}

func decodeCodeModeArguments(raw string) (codeModeArguments, error) {
	var args codeModeArguments
	if err := decodeArgs(raw, &args); err != nil {
		return args, err
	}
	// Keep already-recorded JSON calls executable when resuming older sessions.
	if args.Input != nil {
		if args.Code != "" {
			return args, errors.New("run_code accepts either input or legacy code, not both")
		}
		args.Code = *args.Input
	}
	first, _, _ := strings.Cut(args.Code, "\n")
	if options, ok := strings.CutPrefix(strings.TrimSpace(first), "// @run_code:"); ok {
		var pragma struct {
			Description     string              `json:"description"`
			TimeoutMS       *int                `json:"timeout_ms"`
			MaxOutputTokens *int                `json:"max_output_tokens"`
			ResultView      codemode.ResultView `json:"result_view"`
		}
		decoder := json.NewDecoder(bytes.NewBufferString(options))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&pragma); err != nil {
			return args, fmt.Errorf("invalid run_code pragma: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return args, errors.New("run_code pragma must contain one JSON object")
		}
		args.Description = pragma.Description
		args.TimeoutMS = pragma.TimeoutMS
		args.MaxOutputTokens = pragma.MaxOutputTokens
		args.ResultView = pragma.ResultView
	}
	return args, nil
}
