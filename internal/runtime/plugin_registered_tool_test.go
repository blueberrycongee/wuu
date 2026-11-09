package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type registeredRuntimeToolClient struct {
	events []string
	params pluginhost.ToolExecuteParams
	fail   bool
}

func (c *registeredRuntimeToolClient) ID() string { return "runtime-tools" }

func (c *registeredRuntimeToolClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}

func (c *registeredRuntimeToolClient) Close(context.Context) error { return nil }

func (c *registeredRuntimeToolClient) Tools() []pluginhost.ToolRegistration {
	return []pluginhost.ToolRegistration{{
		ID:          "lookup",
		Description: "Look up plugin data",
		InputSchema: map[string]any{"type": "object"},
		Activity: &pluginhost.ToolActivityMetadata{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            "low",
			Reason:          "reads plugin data",
		},
		Display: &providers.ToolCallDisplay{Kind: "plugin.lookup", Text: "Looking up data", Capability: "plugin.data"},
	}}
}

func (c *registeredRuntimeToolClient) ExecuteTool(_ context.Context, params pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	c.events = append(c.events, "execute")
	c.params = params
	result := toolresult.FromText("plugin")
	result.StructuredContent = json.RawMessage(`{"ok":true}`)
	if c.fail {
		return pluginhost.ToolExecuteResult{}, errors.New("plugin execution failed")
	}
	return pluginhost.ToolExecuteResult{Result: result}, nil
}

func TestPluginToolExecutorExposesAndRunsRegisteredTool(t *testing.T) {
	inner := &recordingToolExecutor{}
	plugin := &registeredRuntimeToolClient{}
	host := pluginhost.New(plugin)
	executor := newPluginToolExecutor(inner, host, "thread-plugin", "/workspace")
	definitions := executor.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "demo" {
		t.Fatalf("definitions = %+v", definitions)
	}
	publicName := definitions[1].Name
	if support, ok := executor.(agent.ToolSupportProvider); !ok || !support.SupportsTool(publicName) {
		t.Fatalf("registered tool %q is not supported", publicName)
	}
	metadataProvider := executor.(agent.ToolMetadataProvider)
	metadata, ok := metadataProvider.ToolMetadata(providers.ToolCall{Name: publicName})
	if !ok || !metadata.ReadOnly || !metadata.ConcurrencySafe || metadata.Risk != "low" {
		t.Fatalf("metadata = %+v, ok = %v", metadata, ok)
	}
	displayProvider := executor.(agent.ToolDisplayProvider)
	display, ok := displayProvider.ToolDisplay(providers.ToolCall{Name: publicName})
	if !ok || display.Kind != "plugin.lookup" || display.Text != "Looking up data" {
		t.Fatalf("display = %+v, ok = %v", display, ok)
	}

	rich := executor.(agent.RichToolExecutor)
	result, err := rich.ExecuteResult(toolctx.WithStepIndex(context.Background(), 7), providers.ToolCall{
		ID: "call-plugin", Name: publicName, Arguments: `{"original":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TextProjection() != "plugin" || string(result.StructuredContent) != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("inner calls = %+v", inner.calls)
	}
	if !reflect.DeepEqual(plugin.events, []string{"execute"}) {
		t.Fatalf("events = %v", plugin.events)
	}
	if plugin.params.ToolID != "lookup" || plugin.params.Tool != publicName || plugin.params.ThreadID != "thread-plugin" || plugin.params.StepIndex != 7 || string(plugin.params.Arguments) != `{"original":true}` {
		t.Fatalf("params = %+v", plugin.params)
	}
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5"), true)
	kit.SetSkills([]skills.Skill{
		{Name: "lookup-guide", Content: "Use the lookup tool.", AllowedTools: []string{publicName}},
		{Name: "missing-guide", AllowedTools: []string{"plugin_missing_tool"}},
	})
	pluginExecutor := newPluginToolExecutor(kit, host, "thread-plugin", kit.RootDir())
	if visible := tools.FilterSkillsForSurface(kit.Skills(), kit.ActiveSurface()); len(visible) != 1 || visible[0].Name != "lookup-guide" {
		t.Fatalf("plugin skill admission = %+v", visible)
	}
	if _, err := pluginExecutor.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"lookup-guide"}`}); err != nil {
		t.Fatalf("load contributed-tool skill: %v", err)
	}
	pluginExecutor.(agent.ToolSurfaceFreezer).FreezeToolSurface()
	defer pluginExecutor.(agent.ToolSurfaceFreezer).UnfreezeToolSurface()
	if _, retired := host.RetirePlugin(context.Background(), plugin.ID(), nil); !retired {
		t.Fatal("plugin was not retired")
	}
	if _, err := pluginExecutor.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"lookup-guide"}`}); err == nil {
		t.Fatal("retired plugin's pinned schema kept its dependent skill loadable")
	}
}

var _ pluginhost.ToolClient = (*registeredRuntimeToolClient)(nil)
