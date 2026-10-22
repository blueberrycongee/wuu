package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	host, _, err := buildPluginHost(plugins, "", "", "", "", nil, start, nil, nil, nil, "")
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

func TestBuildPluginHostMissingTransitiveServiceRevokesProvider(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}, required: []pluginhost.ServiceRequirement{{Name: "missing.storage", MajorVersion: 1, Required: true}}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "notes", "search")
	defer host.Close(context.Background())
	if !provider.closed || !consumer.closed || host.ServiceRegistry().HasProvider("search.provider", 1) {
		t.Fatal("transitive missing service left a closed provider or live consumer registered")
	}
	for _, id := range []string{"search", "notes"} {
		status := requireRuntimeStatus(t, host, id)
		if status.State != pluginhost.StateFailed {
			t.Fatalf("unavailable dependency state: %+v", status)
		}
	}
}

func TestRetainedGenerationRevokesSelectedServiceConsumers(t *testing.T) {
	provider := &fakeServicePluginClient{id: "search", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	consumer := &fakeServicePluginClient{id: "notes", required: []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	host := buildServiceTestHost(t, map[string]pluginhost.Client{"search": provider, "notes": consumer}, "notes", "search")
	defer host.Close(context.Background())
	generation := &PluginGeneration{host: host, active: []pluginpkg.Plugin{testRuntimePlugin("notes"), testRuntimePlugin("search")}}
	generation.revokeMissingPlugins(map[string]bool{"notes": true}, nil, "")
	if !provider.closed || !consumer.closed || generation.PluginAvailable("notes") {
		t.Fatal("retained service consumer survived the removal of its selected provider")
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
    if (q.method === 'initialize') result = { protocol_version: 3, lifecycle_version: 2, provided_services: [{ name: 'host.storage.get', version: '1.0.0', methods: [{ name: 'call', input_schema: 'host.storage.get.input.v1', output_schema: 'host.storage.get.output.v1' }] }] };
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
		}, nil, nil, selection, "")
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

// Preparation must resolve the selected provider before reading persisted state.
// Initialize has no call authority; prepare cannot mutate state or run activation.
func TestCompositionPreparationUsesSelectedProvider(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for external provider verification")
	}
	for _, external := range []bool{false, true} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("external=%t/corrupt=%t", external, corrupt), func(t *testing.T) {
				root := t.TempDir()
				source := `import { createInterface } from 'node:readline';
import { appendFileSync, readFileSync } from 'node:fs';
const role = process.argv[2], pending = new Map(); let sequence = 0;
const send = x => process.stdout.write(JSON.stringify(x) + '\n');
const call = (service, params) => new Promise(resolve => { const id = 'host-' + ++sequence; pending.set(id, resolve); send({ id, method: 'host.service.call', params: { service, method: 'call', params } }); });
createInterface({ input: process.stdin }).on('line', async line => {
  const q = JSON.parse(line);
  if (pending.has(q.id)) { pending.get(q.id)(q); pending.delete(q.id); return; }
  if (!q.method) return;
  try {
    let result = null;
    if (q.method === 'initialize') {
      const early = await call('host.storage.get', { scope: 'workspace', key: 'state' });
      if (early.error?.code !== 'service_not_authorized') throw new Error('initialize gained undeclared authority');
      result = { protocol_version: 3, lifecycle_version: 2 };
      if (role === 'provider') result.provided_services = [{ name: 'host.storage.get', version: '1.0.0', methods: [{ name: 'call', input_schema: 'host.storage.get.input.v1', output_schema: 'host.storage.get.output.v1', preparation: true }] }];
      else result.required_services = ['host.storage.get', 'host.storage.set'].map(name => ({ name, major_version: 1, required: true }));
    } else if (q.method === 'prepare') {
      appendFileSync('events', role + ':prepare\n');
      if (role === 'reader') {
        const write = await call('host.storage.set', { scope: 'workspace', key: 'state', value: 'forbidden' });
        if (write.error?.code !== 'service_unavailable') throw new Error('prepare admitted a write: ' + JSON.stringify(write));
        const read = await call('host.storage.get', { scope: 'workspace', key: 'state' });
        if (read.error) throw new Error(read.error.message);
        if (read.result.value !== (process.argv[3] === 'external' ? 'external-state' : null)) throw new Error('wrong storage selected');
        if (process.argv[4] === 'corrupt') throw new Error('invalid persisted state');
      }
    } else if (q.method === 'service.invoke') result = { value: readFileSync('state', 'utf8') };
    else if (q.method === 'activate' || q.method === 'shutdown') appendFileSync('events', role + ':' + q.method + '\n');
    send({ id: q.id, result });
  } catch (error) { send({ id: q.id, error: { message: error.message } }); }
});`
				if err := os.WriteFile(filepath.Join(root, "runtime.mjs"), []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "state"), []byte("external-state"), 0o600); err != nil {
					t.Fatal(err)
				}
				reader := testRuntimePlugin("reader")
				reader.Root, reader.Runtime.Command = root, node
				reader.SubjectID, reader.Fingerprint = "plugin:user:reader", "prepare-test"
				reader.Runtime.Args = []string{"runtime.mjs", "reader", "native", "valid"}
				if external {
					reader.Runtime.Args[2] = "external"
				}
				if corrupt {
					reader.Runtime.Args[3] = "corrupt"
				}
				plugins := []pluginpkg.Plugin{reader}
				var selection *composition.Config
				if external {
					provider := testRuntimePlugin("file-storage")
					provider.Root, provider.Runtime.Command = root, node
					provider.Runtime.Args = []string{"runtime.mjs", "provider"}
					plugins = append(plugins, provider)
					selection = &composition.Config{Version: 1, Services: []composition.Binding{{Service: "host.storage.get", Major: 1, Provider: provider.ID}}}
				}
				oldClient := &generationClient{id: "old"}
				old := testPluginGeneration("old", oldClient)
				session := testGenerationSession(old)
				session.RootDir, session.WorkspaceID = root, "workspace"
				session.WuuHome, session.StateDir = t.TempDir(), t.TempDir()
				defer session.Cleanup()
				candidate, err := session.buildPluginGeneration(config.Config{Composition: selection}, plugins, map[string]bool{"reader": true}, nil, startPluginClient)
				var host *pluginhost.Host
				if candidate != nil {
					host = candidate.host
				}
				if oldClient.closed || !session.IsCurrentPluginGeneration(old) {
					t.Fatal("candidate preparation replaced the running generation")
				}
				if corrupt {
					if host != nil || err == nil || !strings.Contains(err.Error(), "invalid persisted state") {
						t.Fatalf("corrupt state candidate = %v, %v", host, err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					defer host.Close(context.Background())
				}
				events, err := os.ReadFile(filepath.Join(root, "events"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(events), ":activate") || !strings.Contains(string(events), "reader:prepare") {
					t.Fatalf("preparation ran effects or skipped validation: %s", events)
				}
				if external && !strings.HasPrefix(string(events), "provider:prepare\nreader:prepare\n") {
					t.Fatalf("provider was not ready before its consumer: %s", events)
				}
				if corrupt && (!strings.Contains(string(events), "reader:shutdown") || external && !strings.HasSuffix(string(events), "provider:shutdown\n")) {
					t.Fatalf("failed candidate did not drain consumers before provider: %s", events)
				}
			})
		}
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

type nativeLifecycleConsumer struct {
	*fakeServicePluginClient
	flush func(context.Context) error
}

func (c *nativeLifecycleConsumer) Close(ctx context.Context) error {
	c.closed = true
	return c.flush(ctx)
}

func TestNativeProviderRetirementDrainsConsumers(t *testing.T) {
	for _, entireGeneration := range []bool{false, true} {
		t.Run(fmt.Sprintf("generation=%t", entireGeneration), func(t *testing.T) {
			reader := testRuntimePlugin("reader")
			reader.SubjectID, reader.Fingerprint = "plugin:user:reader", "native-lifecycle"
			consumer := &nativeLifecycleConsumer{fakeServicePluginClient: &fakeServicePluginClient{id: reader.ID,
				required: pluginhost.KernelServiceRequirements(pluginhost.KernelStorageSetService)}}
			host, native, err := buildPluginHost([]pluginpkg.Plugin{reader}, t.TempDir(), "workspace", t.TempDir(), t.TempDir(), nil,
				func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error) { return consumer, nil }, nil, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			flushed := false
			consumer.flush = func(ctx context.Context) error {
				if native.Status().State != pluginhost.StateActive {
					return errors.New("native provider stopped before consumer cleanup")
				}
				_, err := host.ServiceRegistry().Call(ctx, reader.ID, pluginhost.ServiceCallParams{
					Service: pluginhost.KernelStorageSetService, Method: "call",
					Params: json.RawMessage(`{"scope":"workspace","key":"final","value":"flushed"}`),
				})
				flushed = err == nil
				if err != nil {
					return err
				}
				return nil
			}
			if entireGeneration {
				if err := host.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				outcome, found := host.RetirePlugin(context.Background(), native.ID(), errors.New("provider disabled"))
				if !found || outcome.Err != nil {
					t.Fatalf("native retirement = %+v, found=%t", outcome, found)
				}
				if host.ServiceRegistry().HasProvider(pluginhost.KernelStorageSetService, 1) {
					t.Fatal("retired native provider remained routable")
				}
			}
			if !flushed || !consumer.closed || native.Status().State != pluginhost.StateStopped {
				t.Fatalf("native retirement did not finish: flush=%t, consumer closed=%t, native=%s", flushed, consumer.closed, native.Status().State)
			}
		})
	}
}

func TestCompositionRejectsDuplicateRuntimeIdentity(t *testing.T) {
	nativeID := newKernelHostServices(nil, nil).ID()
	for _, ids := range [][]string{{nativeID}, {"repeated", "repeated"}} {
		t.Run(strings.Join(ids, "+"), func(t *testing.T) {
			var plugins []pluginpkg.Plugin
			for _, id := range ids {
				plugins = append(plugins, testRuntimePlugin(id))
			}
			started := false
			host, _, err := buildPluginHost(plugins, "", "", "", "", nil,
				func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
					started = true
					return &fakeServicePluginClient{id: cfg.ID}, nil
				}, nil, nil, nil, "")
			if host != nil {
				defer host.Close(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), "duplicate runtime identity") || started {
				t.Fatalf("duplicate identity candidate = %v, error=%v, started=%t", host, err, started)
			}
		})
	}
}

func TestBuildPluginHostDuplicateProviderRejectsCandidate(t *testing.T) {
	first := &fakeServicePluginClient{id: "search-a", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
	second := &fakeServicePluginClient{id: "search-b", provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.1.0")}}
	clients := map[string]pluginhost.Client{"search-a": first, "search-b": second}
	host, _, err := buildPluginHost([]pluginpkg.Plugin{testRuntimePlugin("search-a"), testRuntimePlugin("search-b")}, "", "", "", "", nil,
		func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
			return clients[cfg.ID], nil
		}, nil, nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "select a provider") || host != nil {
		t.Fatalf("ambiguous candidate = %v, %v", host, err)
	}
	if !first.closed || !second.closed {
		t.Fatal("rejected candidate must close all prepared providers")
	}
}
