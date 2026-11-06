package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

type replayRegistrationClient struct {
	*pluginToolTestClient
	registration pluginhost.ToolRegistration
}

func (c *replayRegistrationClient) Tools() []pluginhost.ToolRegistration {
	return []pluginhost.ToolRegistration{c.registration}
}

func TestReplayDeclarationExternalRegistration(t *testing.T) {
	for index, payload := range []string{
		`{"id":"change","description":"Change state","input_schema":{"type":"object"},"activity":{"read_only":true,"concurrency_safe":true}}`,
		`{"id":"change","description":"Change state","input_schema":{"type":"object"},"replay_safe":true}`,
	} {
		var registration pluginhost.ToolRegistration
		if err := json.Unmarshal([]byte(payload), &registration); err != nil {
			t.Fatal(err)
		}
		client := &replayRegistrationClient{pluginToolTestClient: &pluginToolTestClient{}, registration: registration}
		host := pluginhost.New(client)
		definition := host.ToolDefinitions()[0]
		want := index == 1
		if registration.ReplaySafe != want {
			t.Fatalf("registration replay safe = %v, want %v", registration.ReplaySafe, want)
		}
		if definition.ReplaySafe != want {
			t.Fatalf("host definition replay safe = %v, want %v", definition.ReplaySafe, want)
		}
		executor := newPluginToolExecutor(&recordingToolExecutor{}, host, "thread", t.TempDir())
		metadata, found := executor.(agent.ToolMetadataProvider).ToolMetadata(providers.ToolCall{Name: definition.Name})
		if !found || metadata.ReplaySafe != want {
			t.Fatalf("registration metadata = %+v, found %v", metadata, found)
		}
	}
}

func TestReplayDeclarationSelectedServiceDefaultsAndOptIn(t *testing.T) {
	for _, declaration := range []string{"", `,"replay_safe":true`} {
		client := toolservice.NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
			if method == "list" {
				return json.RawMessage(`{"tools":[{"name":"external","input_schema":{}` + declaration + `}],"catalog":[],"catalog_prompt":"","system_fragment":""}`), nil
			}
			return json.RawMessage(`{"found":true,"supports":true,"metadata":{"read_only":true,"concurrency_safe":true` + declaration + `}}`), nil
		})
		executor := &selectedToolExecutor{client: client}
		want := declaration != ""
		if got := executor.Definitions(); len(got) != 1 || got[0].ReplaySafe != want {
			t.Fatalf("selected definitions = %+v", got)
		}
		if got, found := executor.ToolMetadata(providers.ToolCall{Name: "external"}); !found || got.ReplaySafe != want {
			t.Fatalf("selected metadata = %+v, found %v", got, found)
		}
	}
}

type replayReplacementTool struct {
	tools.Tool
	definition providers.ToolDefinition
}

func (r *replayReplacementTool) Definition() providers.ToolDefinition { return r.definition }

func TestReplayDeclarationNativeReplacementDoesNotInherit(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := kit.LookupTool("read_file")
	if !original.Definition().ReplaySafe {
		t.Fatal("native read_file must explicitly opt in")
	}
	replacement := original.Definition()
	replacement.ReplaySafe = false
	err = kit.SetNativeToolReplacements(map[string]func(tools.Tool) tools.Tool{
		"read_file": func(original tools.Tool) tools.Tool {
			return &replayReplacementTool{Tool: original, definition: replacement}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, found := kit.ToolMetadata(providers.ToolCall{Name: "read_file"})
	if !found || metadata.ReplaySafe {
		t.Fatalf("replacement inherited replay permission: %+v, found %v", metadata, found)
	}
}

// Native code mode promises replay only because its actual nested execution
// path uses the host-scoped journal; no orchestration or read-only hint opts in.
func TestReplayDeclarationNativeCodeJournal(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := codemode.NewService(codemode.ServiceConfig{})
	t.Cleanup(func() { _ = service.Close() })
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	definition := kit.LookupTool("run_code").Definition()
	if !definition.ReplaySafe {
		t.Fatal("journal-backed native run_code must explicitly declare replay safety")
	}
}
