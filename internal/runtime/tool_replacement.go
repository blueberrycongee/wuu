package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// replacementTool retains the native name and surface while using the selected
// plugin's schema, classification and authenticated host execution.
type replacementTool struct {
	original     tools.Tool
	definition   providers.ToolDefinition
	registration pluginhost.ToolRegistration
	publicName   string
	owner        string
	executor     *pluginToolExecutor
	toolkit      *tools.Toolkit
}

func (r *replacementTool) ToolAvailable() bool {
	if r.toolkit.IsToolDisabled(r.publicName) || !r.executor.pluginToolAllowed(r.publicName) {
		return false
	}
	tool, ok := r.executor.host.Tool(r.publicName)
	return ok && tool.PluginID == r.owner && tool.Registration.ID == r.registration.ID
}
func (r *replacementTool) Name() string                         { return r.original.Name() }
func (r *replacementTool) Definition() providers.ToolDefinition { return r.definition }
func (r *replacementTool) IsReadOnly() bool {
	return r.original.IsReadOnly() && r.registration.Activity != nil && r.registration.Activity.ReadOnly
}
func (r *replacementTool) IsConcurrencySafe() bool {
	return r.original.IsConcurrencySafe() && r.registration.Activity != nil && r.registration.Activity.ConcurrencySafe
}
func (r *replacementTool) IsOrchestrator(args string) bool { return false }
func (r *replacementTool) DeclaredCapability() (capability.Capability, bool) {
	if d, ok := r.original.(tools.CapabilityDeclaringTool); ok {
		return d.DeclaredCapability()
	}
	return "", false
}
func (r *replacementTool) NativeToolForCall(args string) (tools.Tool, bool) {
	if r.Name() == "read_file" && r.toolkit.IsNativeReadRequest(args) {
		return r.original, true
	}
	return nil, false
}
func (r *replacementTool) Classify(args string) tools.ToolClassification {
	original := tools.ClassifyTool(r.original, args)
	activity := r.registration.Activity
	original.ReadOnly = original.ReadOnly && activity != nil && activity.ReadOnly
	original.ConcurrencySafe = original.ConcurrencySafe && activity != nil && activity.ConcurrencySafe
	if activity == nil || (activity.Risk != "low" && activity.Risk != "medium" && activity.Risk != "high") {
		original.Risk = tools.ToolRiskHigh
	}
	if activity != nil {
		original.Destructive = original.Destructive || activity.Destructive
		if activity.Risk == "high" {
			original.Risk = tools.ToolRiskHigh
		} else if activity.Risk == "medium" && original.Risk != tools.ToolRiskHigh {
			original.Risk = tools.ToolRiskMedium
		}
	}
	return original
}
func (r *replacementTool) ValidateInput(args string) error {
	if validator, ok := r.original.(tools.InputValidatingTool); ok {
		return validator.ValidateInput(args)
	}
	return nil
}
func (r *replacementTool) Execute(ctx context.Context, args string) (string, error) {
	result, err := r.ExecuteResultCall(ctx, providers.ToolCall{Name: r.Name(), Arguments: args})
	return result.TextProjection(), err
}
func (r *replacementTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	var externalRead *tools.ExternalReadScope
	if r.Name() == "read_file" {
		var err error
		externalRead, err = r.toolkit.PrepareExternalRead(ctx, call.Arguments)
		if err != nil {
			return toolresult.Result{}, err
		}
		if externalRead.UseNative() {
			original, ok := r.original.(tools.RichTool)
			if !ok {
				return toolresult.Result{}, fmt.Errorf("native read_file implementation unavailable")
			}
			return original.ExecuteResult(ctx, call.Arguments)
		}
	}
	if r.toolkit.IsToolDisabled(r.publicName) {
		return toolresult.Result{}, fmt.Errorf("plugin tool %q is disabled", r.publicName)
	}
	registered, ok := r.executor.host.Tool(r.publicName)
	if !ok || registered.PluginID != r.owner || registered.Registration.ID != r.registration.ID {
		return toolresult.Result{}, fmt.Errorf("replacement for %q is unavailable", r.Name())
	}
	if !r.executor.pluginToolAllowed(r.publicName) {
		return toolresult.Result{}, fmt.Errorf("replacement for %q is unavailable in this execution scope", r.Name())
	}
	if r.registration.DirectOnly && toolctx.IsNestedCall(ctx) {
		return toolresult.Result{}, fmt.Errorf("tool %q must be called directly", r.Name())
	}
	pluginCall := call
	pluginCall.Name = r.publicName
	metadata, _ := r.executor.ToolMetadata(pluginCall)
	if err := r.toolkit.AuthorizeTool(ctx, pluginCall, metadata); err != nil {
		return toolresult.Result{}, err
	}
	input, err := r.executor.toolInput(ctx, pluginCall)
	if err != nil {
		return toolresult.Result{}, err
	}
	ctx = context.WithValue(ctx, pluginProcessToolkitKey{}, r.toolkit)
	result, err := r.executor.host.ExecuteTool(ctx, r.publicName, input)
	if err != nil || result.IsError {
		return result, err
	}
	if externalRead != nil {
		if err := ctx.Err(); err != nil {
			return toolresult.Result{}, err
		}
		return externalRead.Render(result)
	}
	text, err := validateSearchReplacement(result, call.Arguments)
	if err != nil {
		return toolresult.Result{}, err
	}
	r.toolkit.RecordWebSearchEvidence(text)
	return result, nil
}

var evidenceToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Both views, when supplied, describe exactly the same evidence.
func validateSearchReplacement(result toolresult.Result, argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	expectedVersion, err := tools.WebSearchEvidenceVersion(argsJSON)
	if err != nil {
		return "", err
	}
	if result.ModelText != nil || len(result.Content) > 1 || (len(result.Content) == 1 && result.Content[0].Type != toolresult.ContentTypeText) {
		return "", fmt.Errorf("invalid web_search replacement content")
	}
	var text []byte
	if len(result.Content) == 1 {
		text = []byte(result.Content[0].Text)
	}
	structured := result.StructuredContent
	if len(text) == 0 && len(structured) == 0 {
		return "", fmt.Errorf("missing web_search replacement result")
	}
	if len(text) > 0 && len(structured) > 0 {
		var a, b any
		if json.Unmarshal(text, &a) != nil || json.Unmarshal(structured, &b) != nil || !reflect.DeepEqual(a, b) {
			return "", fmt.Errorf("web_search result views disagree")
		}
	}
	if len(text) == 0 {
		text = structured
	}
	var response struct {
		Action   string `json:"action"`
		Query    string `json:"query"`
		Evidence struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Source    string `json:"source"`
			Tier      string `json:"source_tier"`
			Retrieved string `json:"retrieved_at"`
			Version   string `json:"version_matched_to_repo"`
		} `json:"evidence"`
		Results json.RawMessage `json:"results"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(text, &response); err != nil || response.Action != "web_search" || response.Query != args.Query || !evidenceToken.MatchString(response.Evidence.ID) || response.Evidence.Kind != "search" || !evidenceToken.MatchString(response.Evidence.Tier) || strings.TrimSpace(response.Evidence.Source) == "" || response.Evidence.Version != expectedVersion {
		return "", fmt.Errorf("invalid web_search replacement result")
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Evidence.Retrieved); err != nil {
		return "", fmt.Errorf("invalid web_search evidence timestamp")
	}
	if len(response.Results) == 0 {
		if strings.TrimSpace(response.Error) == "" {
			return "", fmt.Errorf("web_search result requires results or error")
		}
	} else {
		if strings.TrimSpace(response.Error) != "" {
			return "", fmt.Errorf("web_search cannot return results and error together")
		}
		var items []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		}
		if err := json.Unmarshal(response.Results, &items); err != nil || len(items) > 10 {
			return "", fmt.Errorf("invalid web_search results")
		}
		for _, item := range items {
			if strings.TrimSpace(item.URL) == "" {
				return "", fmt.Errorf("invalid web_search result URL")
			}
		}
	}
	return string(text), nil
}

func configureToolReplacements(executor agent.ToolExecutor, kit *tools.Toolkit, bindings map[string]config.ToolReplacement) error {
	if len(bindings) == 0 {
		kit.SetReplacementBindings(nil)
		if e, ok := executor.(*pluginToolExecutor); ok {
			e.replacementTargets = nil
		}
		return kit.SetNativeToolReplacements(nil)
	}
	e, ok := executor.(*pluginToolExecutor)
	if !ok {
		return fmt.Errorf("tool replacements require native plugin tool executor")
	}
	replacements := make(map[string]func(tools.Tool) tools.Tool, len(bindings))
	definitions := e.host.ToolDefinitions()
	for name, binding := range bindings {
		if name != "web_search" && name != "read_file" {
			return fmt.Errorf("tool %q cannot be replaced", name)
		}
		var definition providers.ToolDefinition
		var registration pluginhost.ToolRegistration
		var publicName string
		for _, d := range definitions {
			tool, found := e.host.Tool(d.Name)
			if found && tool.PluginID == binding.PluginID && tool.Registration.ID == binding.ToolID {
				if publicName != "" {
					return fmt.Errorf("ambiguous replacement for %q", name)
				}
				publicName = d.Name
				definition = d
				registration = tool.Registration
			}
		}
		if publicName == "" {
			return fmt.Errorf("replacement for %q not found", name)
		}
		if registration.Activity != nil && registration.Activity.Orchestrator {
			return fmt.Errorf("replacement for %q cannot be an orchestrator", name)
		}
		if name == "read_file" {
			if err := validateExternalReadRegistration(registration); err != nil {
				return err
			}
		}
		definition.Name = name
		replacements[name] = func(original tools.Tool) tools.Tool {
			return &replacementTool{original: original, definition: definition, registration: registration, publicName: publicName, executor: e, toolkit: kit, owner: binding.PluginID}
		}
	}
	if err := kit.SetNativeToolReplacements(replacements); err != nil {
		return err
	}
	kit.SetReplacementBindings(bindings)
	e.replacementTargets = make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		for _, d := range definitions {
			tool, found := e.host.Tool(d.Name)
			if found && tool.PluginID == binding.PluginID && tool.Registration.ID == binding.ToolID {
				e.replacementTargets[d.Name] = struct{}{}
			}
		}
	}
	return nil
}

// validateToolReplacements inspects only initialized declarations and a local
// registry; it creates no MCP or process resources.
func validateToolReplacements(root string, host *pluginhost.Host, bindings map[string]config.ToolReplacement) error {
	if len(bindings) == 0 {
		return nil
	}
	selected, found := host.ServiceRegistry().ProviderPluginIDFor(toolExecutionConsumer, toolservice.ServiceName, 1)
	if !found || selected != toolservice.DefaultModule {
		return fmt.Errorf("tool replacements require the native agent.tools provider")
	}
	kit, err := tools.New(root)
	if err != nil {
		return err
	}
	replacements := make(map[string]func(tools.Tool) tools.Tool, len(bindings))
	for name, binding := range bindings {
		if name != "web_search" && name != "read_file" {
			return fmt.Errorf("tool %q cannot be replaced", name)
		}
		tool, ok := host.DeclaredTool(binding.PluginID, binding.ToolID)
		if !ok {
			return fmt.Errorf("replacement for %q not found", name)
		}
		if tool.Registration.Activity != nil && tool.Registration.Activity.Orchestrator {
			return fmt.Errorf("replacement for %q cannot be an orchestrator", name)
		}
		if name == "read_file" {
			if err := validateExternalReadRegistration(tool.Registration); err != nil {
				return err
			}
		}
		d := providers.ToolDefinition{Name: name, Description: tool.Registration.Description, InputSchema: tool.Registration.InputSchema, DirectOnly: tool.Registration.DirectOnly}
		replacements[name] = func(original tools.Tool) tools.Tool { return &replacementTool{original: original, definition: d} }
	}
	return kit.SetNativeToolReplacements(replacements)
}

// read_file.text.v1 is a read-only extension text provider; old host cursors
// and host-managed session artifacts retain their original native reader.
func validateExternalReadRegistration(registration pluginhost.ToolRegistration) error {
	if registration.Activity == nil || !registration.Activity.ReadOnly || registration.Activity.Orchestrator {
		return fmt.Errorf("read_file replacement requires a read-only leaf")
	}
	for _, contract := range registration.ReplacementContracts {
		if contract == "read_file.text.v1" {
			return nil
		}
	}
	return fmt.Errorf("read_file replacement requires read_file.text.v1")
}
