package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type admissionCheckpointBarrier struct {
	*persistence.Service
	checkpoint chan struct{}
	release    chan struct{}
	once       sync.Once
}

func (p *admissionCheckpointBarrier) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	raw, err := p.Service.InvokeService(ctx, params)
	if err == nil && params.Method == "checkpoint" {
		p.once.Do(func() {
			close(p.checkpoint)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
	return raw, err
}

type admissionDocumentAdapter struct {
	*runtimeServiceConsumer
	registry *pluginhost.ServiceRegistry
}

func (p *admissionDocumentAdapter) ProvidedServices() []pluginhost.ServiceDescriptor {
	return persistence.NewService("descriptor", "", "sqlite").ProvidedServices()
}

func (p *admissionDocumentAdapter) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	raw, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: params.Service, Method: params.Method, Params: params.Params, ExecutionID: params.ExecutionID})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// A candidate's proof writes a temporary record through the live graph. Retiring
// that graph after the write must preserve authority for the matching cleanup,
// even before the authority's own Close callback is reached.
func TestStateAuthorityRetirementFinishesAdmissionProbeCleanup(t *testing.T) {
	for _, scenario := range []struct{ kind, route string }{
		{"sqlite", "direct"}, {"sqlite", "delegate"},
		{"sqlite", "process-delegate"},
		{"jsonl", "direct"}, {"jsonl", "delegate"},
		{"jsonl", "process-delegate"},
	} {
		t.Run(scenario.kind+"/"+scenario.route, func(t *testing.T) {
			kind := scenario.kind
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			home, root, dir := t.TempDir(), t.TempDir(), t.TempDir()
			const providerID = "external.state"
			closing, finishClose := make(chan struct{}), make(chan struct{})
			var releaseClose sync.Once
			unblockClose := func() { releaseClose.Do(func() { close(finishClose) }) }
			defer unblockClose()
			consumer := &authorityCleanupConsumer{authorityConsumerFixture: &authorityConsumerFixture{id: "external.documents-user", state: pluginhost.StatePrepared}}
			consumer.close = func(ctx context.Context) error {
				close(closing)
				select {
				case <-finishClose:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			modules := []string{providerID, consumer.ID()}
			factories := []NativeProviderFactory{
				{ID: providerID, Build: func(NativeProviderContext) (pluginhost.Client, error) {
					return persistence.NewService(providerID, dir, kind), nil
				}},
				{ID: consumer.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return consumer, nil }},
			}
			var plugins []pluginpkg.Plugin
			selection := &composition.Config{Version: composition.Version, Modules: &modules}
			if scenario.route != "direct" {
				const adapterID = "external.state-adapter"
				modules = append(modules, adapterID)
				selection.Services = []composition.Binding{
					{Service: persistence.ServiceName, Major: 1, Provider: adapterID},
					{Consumer: adapterID, Service: persistence.ServiceName, Major: 1, Provider: providerID},
				}
				if scenario.route == "delegate" {
					factories = append(factories, NativeProviderFactory{ID: adapterID, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
						return &admissionDocumentAdapter{runtimeServiceConsumer: &runtimeServiceConsumer{id: adapterID, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}}, registry: input.Registry}, nil
					}})
				} else {
					node, err := exec.LookPath("node")
					if err != nil {
						t.Skip("Node.js is required for external provider verification")
					}
					adapter := testRuntimePlugin(adapterID)
					adapter.Root, adapter.Runtime.Command = root, node
					adapter.Runtime.Args = []string{"state-delegate.mjs"}
					descriptors, err := json.Marshal(persistence.NewService("descriptor", "", kind).ProvidedServices())
					if err != nil {
						t.Fatal(err)
					}
					source := strings.ReplaceAll(admissionProcessDelegate, "DESCRIPTORS", string(descriptors))
					if err := os.WriteFile(filepath.Join(root, "state-delegate.mjs"), []byte(source), 0o600); err != nil {
						t.Fatal(err)
					}
					plugins = append(plugins, adapter)
				}
			}
			host, _, err := buildPluginHostWithSelection(ctx, plugins, root, "", home, filepath.Join(home, "workspaces", "proof"), nil, startPluginClient, nil, nil, selection, true, "", factories...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { unblockClose(); _ = host.Close(context.Background()) })
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				result, callErr := host.CallService(ctx, consumer.ID(), persistence.ServiceName, method, raw)
				if callErr != nil {
					return nil, callErr
				}
				return result, nil
			})
			view, err := documents.Read(ctx, persistence.Query{Namespace: "business"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := documents.Commit(ctx, authorityWrite("preserved-business", view.Revision, "keep")); err != nil {
				t.Fatal(err)
			}

			barrier := &admissionCheckpointBarrier{Service: persistence.NewService(providerID, dir, kind), checkpoint: make(chan struct{}), release: make(chan struct{})}
			var releaseCheckpoint sync.Once
			unblockCheckpoint := func() { releaseCheckpoint.Do(func() { close(barrier.release) }) }
			defer unblockCheckpoint()
			type preparation struct {
				host *pluginhost.Host
				err  error
			}
			prepared := make(chan preparation, 1)
			go func() {
				modules := []string{providerID}
				candidate, _, err := buildPluginHostWithSelection(ctx, nil, root, "", home, filepath.Join(home, "workspaces", "candidate"), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, true, "",
					NativeProviderFactory{ID: providerID, Build: func(NativeProviderContext) (pluginhost.Client, error) { return barrier, nil }})
				prepared <- preparation{candidate, err}
			}()
			select {
			case <-barrier.checkpoint:
			case <-ctx.Done():
				t.Fatal("candidate did not reach its live-authority proof")
			}
			closed := make(chan error, 1)
			go func() { closed <- host.Close(ctx) }()
			select {
			case <-closing:
			case <-ctx.Done():
				t.Fatal("host did not begin consumer cleanup")
			}
			if _, err := documents.Read(ctx, persistence.Query{Namespace: "business"}); err == nil {
				t.Fatal("retiring graph admitted an ordinary business read")
			}
			if _, err := host.CallService(ctx, consumer.ID(), persistence.ServiceName, "commit", json.RawMessage(`{"id":"forbidden","expected_revision":0,"writes":[{"namespace":"business","key":"forbidden","value":true}]}`)); err == nil || err.Code != "generation_closed" {
				t.Fatalf("retiring graph did not close ordinary business write admission: %v", err)
			}
			// The native consumer's Close proves shutdown admission is sealed, but
			// keeps authority.Close from supplying a late cleanup context.
			unblockCheckpoint()
			select {
			case candidate := <-prepared:
				// Cancellation may reject this unpublished candidate. Either result
				// must leave the live store's probe fully and durably cleaned up.
				if candidate.err != nil {
					t.Logf("unpublished candidate rejected during retirement: %v", candidate.err)
				}
				if candidate.host != nil {
					if err := candidate.host.Close(ctx); err != nil {
						t.Fatal(err)
					}
				}
			case <-ctx.Done():
				t.Fatal("candidate proof did not settle after retirement")
			}
			unblockClose()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("retiring provider did not finish cleanup")
			}
			if executions := host.ExecutionSnapshots(); len(executions) != 0 {
				t.Fatalf("retirement retained host executions: %+v", executions)
			}
			host.ServiceRegistry().Close(ctx)
			if err := ctx.Err(); err != nil {
				t.Fatalf("service registry did not drain after retirement: %v", err)
			}

			store, err := persistence.Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			snapshot, err := store.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			probes, cleanups, business := 0, 0, 0
			for _, record := range snapshot.Receipts {
				if strings.HasPrefix(record.Receipt.ID, "state-admission-probe:") {
					probes++
				}
				if strings.HasPrefix(record.Receipt.ID, "state-admission-cleanup:") {
					cleanups++
				}
			}
			for _, document := range snapshot.Documents {
				if document.Namespace == "wuu.state.authority.v1" && document.Key != "anchor" {
					t.Errorf("retired authority left an admission probe in durable state: %s", document.Key)
				}
				if document.Namespace == "business" && document.Key == "same-thread" && string(document.Value) == `"keep"` {
					business++
				}
			}
			if probes != 1 || cleanups != probes || business != 1 {
				t.Errorf("retirement lost durable cleanup or business state: probes=%d cleanups=%d business=%d", probes, cleanups, business)
			}
			authority, err := NewStateAuthority(home)
			if err != nil {
				t.Fatal(err)
			}
			record, err := authority.readRecord()
			checkpoint, checkpointErr := store.Checkpoint(ctx)
			if err != nil || checkpointErr != nil || record == nil || record.Pending != nil || record.Checkpoint != checkpoint {
				t.Errorf("retirement did not preserve the durable authority checkpoint: record=%+v read=%v checkpoint=%v", record, err, checkpointErr)
			}
		})
	}
}

// The process forwards only the authenticated callback identity and payload.
// All persistence and lifecycle policy remains in the selected provider graph.
const admissionProcessDelegate = `import { createInterface } from 'node:readline';
const send = value => process.stdout.write(JSON.stringify(value) + '\n');
const pending = new Map();
let sequence = 0;
createInterface({input: process.stdin}).on('line', line => {
  const request = JSON.parse(line);
  if (pending.has(request.id) && !request.method) {
    const id = pending.get(request.id);
    pending.delete(request.id);
    send({id, result: request.result, error: request.error});
    return;
  }
  if (!request.id) return;
  if (request.method === 'initialize') {
    send({id: request.id, result: {protocol_version: 3, lifecycle_version: 2,
      provided_services: DESCRIPTORS,
      required_services: [{name:'state.documents', major_version:1, required:true}]}});
  } else if (request.method === 'service.invoke') {
    const id = 'delegate-' + sequence++;
    pending.set(id, request.id);
    const input = request.params;
    send({id, method:'host.service.call', params:{service:input.service,
      method:input.method, params:input.params, execution_id:input.execution_id}});
  } else {
    send({id:request.id, result:{}});
  }
});`

type admissionCommitFailure struct {
	*persistence.Service
	prefix string
	before bool
	always bool
	once   sync.Once
}

func (p *admissionCommitFailure) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	lose := false
	if params.Method == "commit" {
		var transaction persistence.Transaction
		if err := json.Unmarshal(params.Params, &transaction); err != nil {
			return nil, err
		}
		if strings.HasPrefix(transaction.ID, p.prefix) {
			if p.always {
				lose = true
			} else {
				p.once.Do(func() { lose = true })
			}
		}
	}
	if lose && p.before {
		return nil, errors.New("lost provider commit request")
	}
	raw, err := p.Service.InvokeService(ctx, params)
	if lose && err == nil {
		return nil, errors.New("lost provider commit reply")
	}
	return raw, err
}

func TestStateAuthorityRetirementReportsUnrecoverableCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	home, root, dir := t.TempDir(), t.TempDir(), t.TempDir()
	const providerID = "external.state"
	modules := []string{providerID}
	selection := &composition.Config{Version: composition.Version, Modules: &modules}
	build := func(provider pluginhost.Client) (*pluginhost.Host, error) {
		host, _, err := buildPluginHostWithSelection(ctx, nil, root, "", home, t.TempDir(), nil, startPluginClient, nil, nil, selection, true, "",
			NativeProviderFactory{ID: providerID, Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }})
		return host, err
	}
	provider := &admissionCommitFailure{Service: persistence.NewService(providerID, dir, "jsonl"), prefix: "state-admission-cleanup:", before: true, always: true}
	host, err := build(provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	candidate, err := build(persistence.NewService(providerID, dir, "jsonl"))
	if candidate != nil {
		_ = candidate.Close(ctx)
	}
	if err == nil {
		t.Fatal("candidate concealed the failed probe cleanup")
	}
	if err := host.Close(ctx); err == nil {
		t.Fatal("Close reported success while its provider still rejected cleanup")
	}
	if err := host.Close(ctx); err == nil {
		t.Fatal("repeated Close forgot the durable cleanup failure")
	}
	if executions := host.ExecutionSnapshots(); len(executions) != 0 {
		t.Fatalf("failed cleanup retained service executions: %+v", executions)
	}
	authority, err := NewStateAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	record, err := authority.readRecord()
	if err != nil || record == nil || record.Pending == nil || !strings.HasPrefix(record.Pending.Transaction.ID, "state-admission-cleanup:") {
		t.Fatalf("failed cleanup lost its original recovery intent: record=%+v err=%v", record, err)
	}
	if members, err := authority.liveMembers(); err != nil || len(members) != 0 {
		t.Fatalf("failed retired writer retained authority membership: %v %v", members, err)
	}
	// Repair the selected provider and reopen through the same real lifecycle.
	// Activation must resolve the original receipt, not silently abandon it.
	reopened, err := build(persistence.NewService(providerID, dir, "jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	if err := reopened.Activate(ctx); err != nil {
		t.Fatalf("repaired provider could not resolve the retained intent: %v", err)
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cleanups := 0
	for _, receipt := range snapshot.Receipts {
		if strings.HasPrefix(receipt.Receipt.ID, "state-admission-cleanup:") {
			cleanups++
		}
	}
	if cleanups != 1 {
		t.Errorf("repaired provider lost or duplicated cleanup receipt: %d", cleanups)
	}
	for _, document := range snapshot.Documents {
		if document.Namespace != "wuu.state.authority.v1" || document.Key != "anchor" {
			t.Errorf("failed cleanup wrote business state or stranded a probe: %+v", document)
		}
	}
	record, err = authority.readRecord()
	checkpoint, checkpointErr := store.Checkpoint(ctx)
	if err != nil || checkpointErr != nil || record == nil || record.Pending != nil || record.Checkpoint != checkpoint {
		t.Errorf("repaired provider did not settle durable authority: record=%+v read=%v checkpoint=%v", record, err, checkpointErr)
	}
}

// Both the probe and its deletion are ordinary durable transactions. An unknown
// response cannot make Close forget the probe, or replay changed transaction
// intent when reconciling it through the original provider.
func TestStateAuthorityRetirementResolvesUnknownProbeOutcome(t *testing.T) {
	for _, phase := range []string{"probe", "cleanup"} {
		for _, before := range []bool{false, true} {
			name := phase + "/lost-reply"
			if before {
				name = phase + "/lost-request"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				home, root, dir := t.TempDir(), t.TempDir(), t.TempDir()
				provider := &admissionCommitFailure{Service: persistence.NewService("external.state", dir, "jsonl"), prefix: "state-admission-" + phase + ":", before: before}
				modules := []string{provider.ID()}
				selection := &composition.Config{Version: composition.Version, Modules: &modules}
				host, _, err := buildPluginHostWithSelection(ctx, nil, root, "", home, t.TempDir(), nil, startPluginClient, nil, nil, selection, true, "",
					NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = host.Close(context.Background()) })
				if err := host.Activate(ctx); err != nil {
					t.Fatal(err)
				}
				candidate, _, err := buildPluginHostWithSelection(ctx, nil, root, "", home, t.TempDir(), nil, startPluginClient, nil, nil, selection, true, "",
					NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) {
						return persistence.NewService(provider.ID(), dir, "jsonl"), nil
					}})
				if candidate != nil {
					_ = candidate.Close(ctx)
				}
				if err == nil {
					t.Fatal("candidate concealed an unknown admission transaction outcome")
				}
				if err := host.Close(ctx); err != nil {
					t.Fatalf("retirement did not reconcile its durable transaction: %v", err)
				}
				if executions := host.ExecutionSnapshots(); len(executions) != 0 {
					t.Fatalf("retirement retained host executions: %+v", executions)
				}
				host.ServiceRegistry().Close(ctx)
				if err := ctx.Err(); err != nil {
					t.Fatalf("service registry did not drain after retirement: %v", err)
				}
				store, err := persistence.Open(ctx, dir, "jsonl")
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				snapshot, err := store.Export(ctx)
				if err != nil {
					t.Fatal(err)
				}
				probes, cleanups := 0, 0
				for _, receipt := range snapshot.Receipts {
					if strings.HasPrefix(receipt.Receipt.ID, "state-admission-probe:") {
						probes++
					}
					if strings.HasPrefix(receipt.Receipt.ID, "state-admission-cleanup:") {
						cleanups++
					}
				}
				if probes != 1 || cleanups != 1 {
					t.Errorf("unknown outcome lost or duplicated a durable admission transaction: probes=%d cleanups=%d", probes, cleanups)
				}
				for _, document := range snapshot.Documents {
					if document.Namespace == "wuu.state.authority.v1" && document.Key != "anchor" {
						t.Errorf("unknown outcome stranded an admission probe: %s", document.Key)
					}
				}
				authority, err := NewStateAuthority(home)
				if err != nil {
					t.Fatal(err)
				}
				record, err := authority.readRecord()
				checkpoint, checkpointErr := store.Checkpoint(ctx)
				if err != nil || checkpointErr != nil || record == nil || record.Pending != nil || record.Checkpoint != checkpoint {
					t.Errorf("retirement left an unresolved admission intent: record=%+v read=%v checkpoint=%v", record, err, checkpointErr)
				}
			})
		}
	}
}
