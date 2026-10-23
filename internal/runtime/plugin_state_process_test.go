package runtime

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// This fixture is an independent process implementation of each stateful
// contract. Native clients, built-in presentation and configured registry
// bindings must work without opening the native providers' storage roots.
func TestStateServiceContractsThroughExternalProcess(t *testing.T) {
	if path := os.Getenv("WUU_STATE_SERVICE_FIXTURE"); path != "" {
		runStateServiceFixture(path)
		return
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "external.json")
	home := t.TempDir()
	nativePath := filepath.Join(home, "workspaces", "native")
	process, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "external.state", Command: os.Args[0], Args: []string{"-test.run=^TestStateServiceContractsThroughExternalProcess$"}, Env: map[string]string{"WUU_STATE_SERVICE_FIXTURE": path}, PluginRoot: t.TempDir(), ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), Timeout: 5 * time.Second, PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{config.ServiceName, pluginsettings.ServiceName, ArtifactServiceName, pluginhost.SecurityAuthorizeService, pluginhost.KernelDataQueryService}
	bindings := make([]composition.Binding, 0, len(names))
	requirements := make([]pluginhost.ServiceRequirement, 0, len(names))
	for _, name := range names {
		bindings = append(bindings, composition.Binding{Service: name, Major: 1, Provider: process.ID()})
		requirements = append(requirements, pluginhost.ServiceRequirement{Name: name, MajorVersion: 1, Required: true})
	}
	consumer := &runtimeServiceConsumer{id: StateServicesConsumer, state: pluginhost.StatePrepared, requirements: requirements}
	registry, err := pluginhost.NewServiceRegistryWithBindings(bindings)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := registry.RegisterClients(process, consumer, NewArtifactService(ArtifactModule, home, nativePath), NewDataQueryService(DataQueryModule, nativePath), pluginsettings.NewService(pluginsettings.Module, home, t.TempDir()), config.NewService(config.Module, filepath.Join(home, "config.json"), ""))
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(process, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := host.CallService(ctx, StateServicesConsumer, service, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	settings := pluginsettings.NewClient(call(pluginsettings.ServiceName))
	if _, err := settings.Update(ctx, "plugin:external", pluginsettings.ScopeUser, "fingerprint", func(values map[string]json.RawMessage) error {
		values["display"] = json.RawMessage(`"external"`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	document, err := settings.Read(ctx, "plugin:external", pluginsettings.ScopeUser)
	if err != nil || string(document.Values["display"]) != `"external"` {
		t.Fatalf("external settings: %+v %v", document, err)
	}
	if _, err := settings.UpdateState(ctx, "plugin:external", pluginsettings.ScopeUser, func(values map[string]string) error { values["job"] = "done"; return nil }); err != nil {
		t.Fatal(err)
	}
	state, err := settings.ReadState(ctx, "plugin:external", pluginsettings.ScopeUser)
	if err != nil || state.Values["job"] != "done" {
		t.Fatalf("external state: %+v %v", state, err)
	}
	configuration := config.NewClient(call(config.ServiceName))
	view, err := configuration.Read(ctx)
	if err != nil || string(view.Source) != `{}` {
		t.Fatalf("external config: %+v %v", view, err)
	}
	if _, err := configuration.CompareExchange(ctx, view.Revision, []byte(`{"external":true}`)); err != nil {
		t.Fatal(err)
	}
	artifacts := NewArtifactClient(call(ArtifactServiceName))
	part, err := NewArtifactPublisher(artifacts)(ctx, tools.ArtifactPublishRequest{ThreadID: "thread", CallID: "publish", Path: "/nonexistent/native/path"})
	if err != nil || !strings.HasPrefix(part.URI, "external-artifact:") {
		t.Fatalf("publisher bypassed process: %+v %v", part, err)
	}
	bytes, media, err := artifacts.Read(ctx, "thread", part.URI, 100)
	if err != nil || string(bytes) != "external artifact" || media != "text/plain" {
		t.Fatalf("external artifact read: %q %s %v", bytes, media, err)
	}
	fork, err := artifacts.Fork(ctx, "thread", "fork", part.URI)
	if err != nil || !strings.Contains(fork.URI, "/fork/") {
		t.Fatalf("external fork: %+v %v", fork, err)
	}
	decision, err := call(pluginhost.SecurityAuthorizeService)(ctx, "authorize", json.RawMessage(`{"cwd":"/workspace","permission_mode":"standard","tool":{"name":"write"}}`))
	if err != nil || !strings.Contains(string(decision), `"deny"`) {
		t.Fatalf("external policy: %s %v", decision, err)
	}
	events, err := call(pluginhost.KernelDataQueryService)(ctx, pluginhost.KernelServiceMethod, json.RawMessage(`{"thread_id":"thread"}`))
	if err != nil || !strings.Contains(string(events), "external_event") {
		t.Fatalf("external index: %s %v", events, err)
	}
	if _, err := os.Stat(nativePath); !os.IsNotExist(err) {
		t.Fatalf("native artifact/index files were accessed: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Read(ctx, "plugin:external", pluginsettings.ScopeUser); err == nil {
		t.Fatal("closed process settings fell back")
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(persisted), "external") {
		t.Fatalf("external state was not persisted: %v", err)
	}
}

func runStateServiceFixture(path string) {
	saved := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &saved) != nil {
			os.Exit(2)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	reply := func(id string, result any) {
		if encoder.Encode(map[string]any{"id": id, "result": result}) != nil {
			os.Exit(3)
		}
	}
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(4)
		}
		switch request.Method {
		case "initialize":
			var descriptors []pluginhost.ServiceDescriptor
			for name, methods := range map[string][]string{"state.config": {"read", "compare-exchange"}, "state.plugin-settings": {"read", "update", "read-state", "update-state"}, "state.artifacts": {"import", "resolve", "read", "fork"}, "security.authorize": {"authorize"}, "host.data.query": {"call"}} {
				descriptor := pluginhost.ServiceDescriptor{Name: name, Version: "1.0.0"}
				for _, method := range methods {
					descriptor.Methods = append(descriptor.Methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: name + "." + method + ".input.v1", OutputSchema: name + "." + method + ".output.v1", Cleanup: true})
				}
				descriptors = append(descriptors, descriptor)
			}
			reply(request.ID, pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: descriptors})
		case "prepare", "activate", "service.changed", "execution.cancel":
			reply(request.ID, struct{}{})
		case "service.invoke":
			var params pluginhost.ServiceInvokeParams
			if json.Unmarshal(request.Params, &params) != nil {
				os.Exit(5)
			}
			var input map[string]json.RawMessage
			if json.Unmarshal(params.Params, &input) != nil {
				os.Exit(6)
			}
			var result any
			switch params.Service {
			case "state.config":
				if params.Method == "compare-exchange" {
					saved["config"] = input["source"]
				}
				source := saved["config"]
				if source == nil {
					source = json.RawMessage(`"e30="`)
				}
				result = map[string]any{"source": source, "revision": "fixture-revision", "config": map[string]any{}, "committed": params.Method == "compare-exchange"}
			case "state.plugin-settings":
				var subject, scope string
				json.Unmarshal(input["subject"], &subject)
				json.Unmarshal(input["scope"], &scope)
				kind := "settings"
				if strings.HasSuffix(params.Method, "-state") {
					kind = "state"
				}
				key := kind + ":" + subject + ":" + scope
				if strings.HasPrefix(params.Method, "update") {
					raw, _ := json.Marshal(map[string]any{"schema_version": 1, "plugin_id": subject, "fingerprint": input["fingerprint"], "values": input["values"]})
					saved[key] = raw
				}
				document := saved[key]
				if document == nil {
					document, _ = json.Marshal(map[string]any{"schema_version": 1, "plugin_id": subject, "values": map[string]any{}})
				}
				result = map[string]any{"document": document, "revision": "fixture-revision"}
			case "state.artifacts":
				artifact := map[string]any{"id": "fixture", "uri": "external-artifact://fixture/thread/report.txt", "name": "report.txt", "mime_type": "text/plain", "size": 17, "sha256": "fixture-digest"}
				switch params.Method {
				case "import":
					result = artifact
				case "resolve":
					result = map[string]any{"artifact": artifact, "managed": true}
				case "read":
					result = map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("external artifact")), "mime_type": "text/plain"}
				case "fork":
					var target string
					json.Unmarshal(input["target_thread_id"], &target)
					artifact["uri"] = "external-artifact://fixture/" + target + "/report.txt"
					result = artifact
				}
			case "security.authorize":
				result = map[string]any{"outcome": "deny", "reason": "external policy"}
			case "host.data.query":
				result = map[string]any{"version": "1.0.0", "thread_id": "thread", "events": []any{map[string]any{"type": "external_event"}}}
			default:
				os.Exit(7)
			}
			raw, err := json.Marshal(saved)
			if err != nil || os.WriteFile(path, raw, 0600) != nil {
				os.Exit(8)
			}
			reply(request.ID, result)
		case "shutdown":
			reply(request.ID, struct{}{})
			return
		default:
			os.Exit(9)
		}
	}
}
