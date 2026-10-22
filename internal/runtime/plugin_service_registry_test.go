package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type fakeServicePluginClient struct {
	id       string
	provided []pluginhost.ServiceDescriptor
	required []pluginhost.ServiceRequirement
	closed   bool
}

func (c *fakeServicePluginClient) ID() string { return c.id }
func (c *fakeServicePluginClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StatePrepared}
}
func (c *fakeServicePluginClient) Close(context.Context) error { c.closed = true; return nil }
func (c *fakeServicePluginClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return c.provided
}
func (c *fakeServicePluginClient) RequiredServices() []pluginhost.ServiceRequirement {
	return c.required
}
func (c *fakeServicePluginClient) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func searchServiceDescriptor(version string) pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{
		Name:    "search.provider",
		Version: version,
		Methods: []pluginhost.ServiceMethodDescriptor{
			{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"},
		},
	}
}

func buildServiceTestHost(t *testing.T, clients map[string]pluginhost.Client, pluginIDs ...string) *pluginhost.Host {
	t.Helper()
	plugins := make([]pluginpkg.Plugin, 0, len(pluginIDs))
	for _, id := range pluginIDs {
		plugins = append(plugins, testRuntimePlugin(id))
	}
	start := func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		return clients[cfg.ID], nil
	}
	host, _, err := buildPluginHost(plugins, "", "", "", "", nil, start, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildPluginHost() = %v", err)
	}
	return host
}

func TestBuildPluginHostSatisfiedServiceRequirements(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "search", "notes")

	registry := host.ServiceRegistry()
	if registry == nil {
		t.Fatal("expected an attached service registry")
	}
	if !registry.HasProvider("search.provider", 1) {
		t.Fatal("provider must be registered")
	}
	if consumer.closed {
		t.Fatal("satisfied consumer must stay live")
	}
	for _, status := range host.Statuses() {
		if status.State == pluginhost.StateFailed {
			t.Fatalf("unexpected failed plugin: %+v", status)
		}
	}
}

func TestBuildPluginHostUnsatisfiedRequiredServiceBlocksConsumer(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "memory.index", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "search", "notes")

	if !consumer.closed {
		t.Fatal("unsatisfied consumer must be closed before registration")
	}
	var notesStatus *pluginhost.Status
	statuses := host.Statuses()
	for index := range statuses {
		if statuses[index].ID == "notes" {
			notesStatus = &statuses[index]
		}
	}
	if notesStatus == nil || notesStatus.State != pluginhost.StateFailed || !strings.Contains(notesStatus.Error, "memory.index") {
		t.Fatalf("notes status = %+v, want failed with service diagnostic", notesStatus)
	}
	if !host.ServiceRegistry().HasProvider("search.provider", 1) {
		t.Fatal("unaffected provider must stay registered")
	}
}

func TestBuildPluginHostOptionalServiceRequirementDoesNotBlock(t *testing.T) {
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "memory.index", MajorVersion: 1}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"notes": consumer}, "notes")
	if consumer.closed {
		t.Fatal("optional requirement must not block activation")
	}
	for _, status := range host.Statuses() {
		if status.State == pluginhost.StateFailed {
			t.Fatalf("unexpected failed plugin: %+v", status)
		}
	}
}

func TestCompositionReplacesNativeStorageWithExternalProcess(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for external provider verification")
	}
	root := t.TempDir()
	provider := testRuntimePlugin("file-storage")
	provider.Root = root
	provider.Runtime.Command = node
	provider.Runtime.Args = []string{"provider.mjs"}
	// This independent implementation reads the caller's file on every call;
	// the selected process owns storage semantics and sees external writes.
	source := `import { createInterface } from 'node:readline';
import { readFileSync } from 'node:fs';
const send = x => process.stdout.write(JSON.stringify(x) + '\n');
createInterface({ input: process.stdin }).on('line', line => {
  const q = JSON.parse(line);
  if (!q.id) return;
  try {
    let result = null;
    if (q.method === 'initialize') result = { protocol_version: 3, lifecycle_version: 1, provided_services: [{ name: 'host.storage.get', version: '1.0.0', methods: [{ name: 'call', input_schema: 'host.storage.get.input.v1', output_schema: 'host.storage.get.output.v1' }] }] };
    else if (q.method === 'service.invoke') {
      const state = JSON.parse(readFileSync(q.params.caller + '.json', 'utf8'));
      result = { value: state[q.params.params.key] ?? null };
    }
    send({ id: q.id, result });
  } catch (e) { send({ id: q.id, error: { code: 'storage_read_failed', message: e.message } }); }
});`
	if err := os.WriteFile(filepath.Join(root, "provider.mjs"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := testRuntimePlugin("reader")
	selection := &composition.Config{Version: 1, Services: []composition.Binding{{Service: "host.storage.get", Major: 1, Provider: "file-storage"}}}
	host, _, err := buildPluginHost([]pluginpkg.Plugin{reader, provider}, root, "workspace", t.TempDir(), t.TempDir(), nil,
		func(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
			if cfg.ID == "file-storage" {
				return pluginhost.Start(ctx, cfg)
			}
			return &fakeServicePluginClient{id: cfg.ID, required: pluginhost.KernelServiceRequirements("host.storage.get")}, nil
		}, nil, nil, selection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := activatePluginHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	call := pluginhost.ServiceCallParams{Service: "host.storage.get", Method: "call", Params: json.RawMessage(`{"scope":"workspace","key":"note"}`)}
	for _, value := range []string{"first", "changed-on-disk"} {
		data, _ := json.Marshal(map[string]string{"note": value})
		if err := os.WriteFile(filepath.Join(root, "reader.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		raw, err := host.ServiceRegistry().Call(context.Background(), "reader", call)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || result.Value != value {
			t.Fatalf("external storage read = %s, %v", raw, err)
		}
	}
	host.RetirePlugin(context.Background(), "file-storage", errors.New("provider removed"))
	if _, err := host.ServiceRegistry().Call(context.Background(), "reader", call); err == nil {
		t.Fatal("removed external storage fell back to native storage")
	}
}

func TestCompositionPreparationFailurePreservesRunningGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)
	defer session.Cleanup()
	cfg := config.Config{Composition: &composition.Config{Version: 1, Services: []composition.Binding{{Service: "search.provider", Major: 1, Provider: "missing"}}}}
	candidate, err := session.buildPluginGeneration(cfg, nil, nil, nil, startPluginClient)
	if err == nil || candidate != nil || oldClient.closed || !session.IsCurrentPluginGeneration(old) {
		t.Fatalf("failed selection changed active generation: candidate=%v, error=%v, old closed=%v", candidate, err, oldClient.closed)
	}
}

func TestBuildPluginHostDuplicateProviderRejectsCandidate(t *testing.T) {
	first := &fakeServicePluginClient{id: "search-a", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	second := &fakeServicePluginClient{id: "search-b", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.1.0")}}
	clients := map[string]pluginhost.Client{"search-a": first, "search-b": second}
	host, _, err := buildPluginHost([]pluginpkg.Plugin{testRuntimePlugin("search-a"), testRuntimePlugin("search-b")}, "", "", "", "", nil,
		func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
			return clients[cfg.ID], nil
		}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "select a provider") || host != nil {
		t.Fatalf("ambiguous candidate = %v, %v", host, err)
	}
	if !first.closed || !second.closed {
		t.Fatal("rejected candidate must close all prepared providers")
	}
}
