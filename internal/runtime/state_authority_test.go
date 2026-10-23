package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type authorityProviderFixture struct {
	*persistence.Service
	mu         sync.Mutex
	exports    int
	loseID     string
	loseBefore bool
}

func (p *authorityProviderFixture) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	p.mu.Lock()
	if params.Method == "export" {
		p.exports++
	}
	lose := false
	before := p.loseBefore
	if params.Method == "commit" {
		var tx persistence.Transaction
		_ = json.Unmarshal(params.Params, &tx)
		if tx.ID == p.loseID && p.loseID != "" {
			lose = true
			p.loseID = ""
		}
	}
	p.mu.Unlock()
	if lose && before {
		return nil, errors.New("lost commit request")
	}
	result, err := p.Service.InvokeService(ctx, params)
	if err == nil && lose {
		return nil, errors.New("lost durable commit reply")
	}
	return result, err
}

type authorityConsumerFixture struct {
	id                string
	mu                sync.Mutex
	state             pluginhost.State
	prepare, activate func(context.Context) error
}

func (c *authorityConsumerFixture) Prepare(ctx context.Context) error {
	if c.prepare != nil {
		return c.prepare(ctx)
	}
	return nil
}
func (c *authorityConsumerFixture) Activate(ctx context.Context) error {
	if c.activate != nil {
		if err := c.activate(ctx); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.state = pluginhost.StateActive
	c.mu.Unlock()
	return nil
}
func (c *authorityConsumerFixture) ID() string { return c.id }
func (c *authorityConsumerFixture) Status() pluginhost.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return pluginhost.Status{ID: c.id, State: c.state}
}
func (c *authorityConsumerFixture) Close(context.Context) error                      { return nil }
func (c *authorityConsumerFixture) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (c *authorityConsumerFixture) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}

type authorityFixture struct {
	consumer          *authorityConsumerFixture
	gate              *StateAuthority
	provider          *authorityProviderFixture
	host              *pluginhost.Host
	documents, plugin *persistence.Client
}

func newAuthorityFixture(t *testing.T, home, dir, kind string) *authorityFixture {
	return makeAuthorityFixture(t, home, dir, kind, true)
}
func makeAuthorityFixture(t *testing.T, home, dir, kind string, activate bool) *authorityFixture {
	t.Helper()
	provider := &authorityProviderFixture{Service: persistence.NewService("external-or-native", dir, kind)}
	state := pluginhost.StateActive
	if !activate {
		state = pluginhost.StatePrepared
	}
	consumer := &authorityConsumerFixture{id: "session-state", state: state}
	plugin := &authorityConsumerFixture{id: "third-party", state: state}
	host := pluginhost.New(provider, consumer, plugin)
	registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer, plugin)
	gate, err := NewStateAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.WrapServiceInvoker(persistence.ServiceName, 1, gate.Wrap); err != nil {
		t.Fatal(err)
	}
	host.AttachServiceRegistry(registry, conflicts)
	if activate {
		if err := host.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	client := func(id string) *persistence.Client {
		return persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			var raw json.RawMessage
			var err *pluginhost.HostServiceError
			owner := consumer
			if id == plugin.id {
				owner = plugin
			}
			if owner.Status().State == pluginhost.StatePrepared {
				raw, err = registry.Call(ctx, id, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
			} else {
				raw, err = host.CallService(ctx, id, persistence.ServiceName, method, params)
			}
			if err != nil {
				return nil, err
			}
			return raw, nil
		})
	}
	fixture := &authorityFixture{consumer: consumer, gate: gate, provider: provider, host: host, documents: client(consumer.id), plugin: client(plugin.id)}
	t.Cleanup(func() { fixture.close(t) })
	return fixture
}
func (f *authorityFixture) close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.gate.Close(ctx); err != nil {
		t.Error(err)
	}
	if err := f.host.Close(ctx); err != nil {
		t.Error(err)
	}
}
func authorityWrite(id string, revision persistence.Revision, value string) persistence.Transaction {
	raw, _ := json.Marshal(value)
	return persistence.Transaction{ID: id, ExpectedRevision: revision, Writes: []persistence.Write{{Namespace: "business", Key: "same-thread", Value: raw}}}
}
func authorityHead(t *testing.T, docs persistence.Documents) persistence.View {
	t.Helper()
	view, err := docs.Read(context.Background(), persistence.Query{Namespace: "business"})
	if err != nil {
		t.Fatal(err)
	}
	return view
}
func admitAuthority(t *testing.T, f *authorityFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.gate.Preflight(ctx, f.documents); err != nil {
		t.Fatal(err)
	}
	if err := f.gate.Activate(ctx, f.documents); err != nil {
		t.Fatal(err)
	}
}

// Every registered caller shares the durable CAS boundary; ordinary requests
// must not export the whole store or change the original receipt on retry.
func TestStateAuthorityProtectsAllCallersWithoutHotExports(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			f := newAuthorityFixture(t, t.TempDir(), t.TempDir(), kind)
			if _, err := f.plugin.Commit(context.Background(), authorityWrite("before", 0, "denied")); err == nil {
				t.Fatal("unadmitted plugin wrote")
			}
			admitAuthority(t, f)
			for _, request := range []struct{ method, params string }{
				{"read", `{"namespace":"business","cursor":"same-thread"}`},
				{"read", `{"namespace":"business","unexpected":true}`},
				{"commit", `{"id":"invalid","expected_revision":1,"writes":[],"unexpected":true}`},
				{"export", `{"unexpected":true}`},
				{"compact", `{"unexpected":true}`},
			} {
				_, err := f.host.CallService(context.Background(), "third-party", persistence.ServiceName, request.method, json.RawMessage(request.params))
				if err == nil || err.Code != "invalid_request" {
					t.Errorf("%s accepted invalid public input %s: %v", request.method, request.params, err)
				}
			}
			f.provider.mu.Lock()
			f.provider.exports = 0
			f.provider.mu.Unlock()
			tx := authorityWrite("first", authorityHead(t, f.documents).Revision, "saved")
			receipt, err := f.plugin.Commit(context.Background(), tx)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := f.documents.Commit(context.Background(), tx); err != nil || got != receipt {
				t.Fatalf("receipt retry: %+v %v", got, err)
			}
			if _, err := f.documents.Commit(context.Background(), authorityWrite("first", tx.ExpectedRevision, "different")); !errors.Is(err, persistence.ErrOperationConflict) {
				t.Fatalf("changed intent accepted: %v", err)
			}
			view := authorityHead(t, f.documents)
			if len(view.Documents) != 1 || string(view.Documents[0].Value) != `"saved"` {
				t.Fatalf("state mismatch: %+v", view)
			}
			f.provider.mu.Lock()
			exports := f.provider.exports
			f.provider.mu.Unlock()
			if exports != 0 {
				t.Fatalf("ordinary requests exported %d times", exports)
			}
		})
	}
}

// Current copies are as unsafe as empty/stale stores while the original is
// alive. Failed admission must leave the original writable and candidate inert.
func TestStateAuthorityRejectsLiveCopiesAndAcceptsSharedHandles(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			home, dir := t.TempDir(), t.TempDir()
			first := newAuthorityFixture(t, home, dir, kind)
			admitAuthority(t, first)
			if _, err := first.documents.Commit(context.Background(), authorityWrite("first", authorityHead(t, first.documents).Revision, "saved")); err != nil {
				t.Fatal(err)
			}
			snapshot, err := first.documents.Export(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, copied := range []bool{false, true} {
				t.Run(fmt.Sprint(copied), func(t *testing.T) {
					target := t.TempDir()
					if copied {
						if err := persistence.Import(context.Background(), target, kind, snapshot); err != nil {
							t.Fatal(err)
						}
					}
					candidate := newAuthorityFixture(t, home, target, kind)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := candidate.gate.Preflight(ctx, candidate.documents); !errors.Is(err, persistence.ErrMigrationRequired) {
						t.Fatalf("independent writer admitted: %v", err)
					}
					if _, err := candidate.plugin.Commit(ctx, authorityWrite("fork", snapshot.Revision, "forked")); err == nil {
						t.Fatal("rejected candidate wrote through another caller")
					}
				})
			}
			head := authorityHead(t, first.documents)
			if _, err := first.documents.Commit(context.Background(), authorityWrite("after-rejection", head.Revision, "still writable")); err != nil {
				t.Fatal(err)
			}
			second := newAuthorityFixture(t, home, dir, kind)
			// Multiple ordinary preparation reads must obtain fresh live proofs,
			// never reuse a previous probe transaction's operation identity.
			if _, err := second.plugin.Read(context.Background(), persistence.Query{Namespace: "business", Limit: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := second.plugin.Export(context.Background()); err != nil {
				t.Fatal(err)
			}
			admitAuthority(t, second)
			head = authorityHead(t, second.plugin)
			if _, err := second.plugin.Commit(context.Background(), authorityWrite("shared", head.Revision, "shared value")); err != nil {
				t.Fatal(err)
			}
			if got := authorityHead(t, first.documents); len(got.Documents) != 1 || string(got.Documents[0].Value) != `"shared value"` {
				t.Fatalf("shared successor forked: %+v", got)
			}
		})
	}
}

// Restart must resolve only the durable pending intent, whether the reply was
// lost before or after the provider committed. An empty replacement cannot
// receive that intent and become a fabricated successor.
func TestStateAuthorityRecoversExactPendingAcrossRestart(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprint(before), func(t *testing.T) {
			home, dir := t.TempDir(), t.TempDir()
			first := newAuthorityFixture(t, home, dir, "jsonl")
			admitAuthority(t, first)
			first.provider.mu.Lock()
			first.provider.loseID = "uncertain"
			first.provider.loseBefore = before
			first.provider.mu.Unlock()
			tx := authorityWrite("uncertain", authorityHead(t, first.documents).Revision, "durable")
			if _, err := first.plugin.Commit(context.Background(), tx); err == nil {
				t.Fatal("lost reply reported success")
			}
			first.close(t)
			empty := newAuthorityFixture(t, home, t.TempDir(), "jsonl")
			if err := empty.gate.Preflight(context.Background(), empty.documents); !errors.Is(err, persistence.ErrMigrationRequired) {
				t.Fatalf("pending intent copied into replacement: %v", err)
			}
			empty.close(t)
			reopened := newAuthorityFixture(t, home, dir, "jsonl")
			admitAuthority(t, reopened)
			got, err := reopened.plugin.Commit(context.Background(), tx)
			if err != nil || got.Revision != tx.ExpectedRevision+1 {
				t.Fatalf("pending replay: %+v %v", got, err)
			}
			view := authorityHead(t, reopened.documents)
			if view.Revision != tx.ExpectedRevision+1 || len(view.Documents) != 1 {
				t.Fatalf("pending replay duplicated or lost state: %+v", view)
			}
		})
	}
}

func TestStateAuthorityRestartRejectsEmptyAndMissingHead(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	first := newAuthorityFixture(t, home, dir, "sqlite")
	admitAuthority(t, first)
	if _, err := first.documents.Commit(context.Background(), authorityWrite("saved", authorityHead(t, first.documents).Revision, "preserved")); err != nil {
		t.Fatal(err)
	}
	first.close(t)
	wrong := newAuthorityFixture(t, home, t.TempDir(), "sqlite")
	if err := wrong.gate.Preflight(context.Background(), wrong.documents); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("restart forgot authority: %v", err)
	}
	wrong.close(t)
	if err := os.Remove(filepath.Join(home, "state-authority", "head.json")); err != nil {
		t.Fatal(err)
	}
	reopened := newAuthorityFixture(t, home, dir, "sqlite")
	if err := reopened.gate.Preflight(context.Background(), reopened.documents); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("deleted head was silently bootstrapped: %v", err)
	}
}

// The real test process owns a distinct registry and authority instance. It has
// no in-memory access to the parent provider or its membership state.
func TestStateAuthorityProcessHelper(t *testing.T) {
	if os.Getenv("WUU_AUTHORITY_HELPER") != "1" {
		return
	}
	f := newAuthorityFixture(t, os.Getenv("WUU_AUTHORITY_HOME"), os.Getenv("WUU_AUTHORITY_STORE"), "jsonl")
	admitAuthority(t, f)
	enc, dec := json.NewEncoder(os.Stdout), json.NewDecoder(os.Stdin)
	if err := enc.Encode(map[string]bool{"ready": true}); err != nil {
		t.Fatal(err)
	}
	var command string
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	view := authorityHead(t, f.plugin)
	if _, err := f.plugin.Commit(context.Background(), authorityWrite("child", view.Revision, command)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(map[string]bool{"written": true}); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
}
func TestStateAuthoritySharesAcrossProcesses(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	parent := newAuthorityFixture(t, home, dir, "jsonl")
	admitAuthority(t, parent)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStateAuthorityProcessHelper$")
	child.Env = append(os.Environ(), "WUU_AUTHORITY_HELPER=1", "WUU_AUTHORITY_HOME="+home, "WUU_AUTHORITY_STORE="+dir)
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	decoder, encoder := json.NewDecoder(output), json.NewEncoder(input)
	var response map[string]bool
	if err := decoder.Decode(&response); err != nil || !response["ready"] {
		t.Fatalf("child admission: %+v %v", response, err)
	}
	if err := encoder.Encode("from child"); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil || !response["written"] {
		t.Fatalf("child write: %+v %v", response, err)
	}
	if got := authorityHead(t, parent.documents); len(got.Documents) != 1 || string(got.Documents[0].Value) != `"from child"` {
		t.Fatalf("cross-process fork: %+v", got)
	}
	if err := encoder.Encode("close"); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}

// Host.Prepare verifies candidate storage before config publication; it cannot
// initialize an empty provider or the host authority. Activation finalizes both.
func TestStateAuthorityPrepareDoesNotInitializeCandidate(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(t.TempDir(), "candidate")
			f := makeAuthorityFixture(t, home, dir, kind, false)
			f.consumer.prepare = func(ctx context.Context) error { return f.gate.Preflight(ctx, f.documents) }
			f.consumer.activate = func(ctx context.Context) error { return f.gate.Activate(ctx, f.documents) }
			if err := f.host.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepare initialized provider: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, "state-authority", "head.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepare published host head: %v", err)
			}
			if _, err := f.plugin.Commit(context.Background(), authorityWrite("early", 0, "forbidden")); err == nil {
				t.Fatal("prepared provider was writable")
			}
			if err := f.host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			head := authorityHead(t, f.documents)
			if _, err := f.plugin.Commit(context.Background(), authorityWrite("active", head.Revision, "allowed")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type authorityServiceRouter struct {
	mu       sync.Mutex
	registry *pluginhost.ServiceRegistry
}

func (r *authorityServiceRouter) RouteServiceCall(ctx context.Context, id string, params pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	r.mu.Lock()
	registry := r.registry
	r.mu.Unlock()
	return registry.Call(ctx, id, params)
}

// All consumers can read during their real Prepare callback, including process
// consumers ordered before the session consumer. None can initialize or write.
func TestStateAuthorityOrdinaryConsumersReadDuringPrepare(t *testing.T) {
	for _, transport := range []string{"native", "process"} {
		for _, kind := range []string{"sqlite", "jsonl"} {
			t.Run(transport+"/"+kind, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				home, root := t.TempDir(), t.TempDir()
				dir := filepath.Join(root, "candidate")
				provider := persistence.NewService("provider", dir, kind)
				gate, err := NewStateAuthority(home)
				if err != nil {
					t.Fatal(err)
				}
				router := &authorityServiceRouter{}
				client := func(id string) *persistence.Client {
					return persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
						result, err := router.RouteServiceCall(ctx, id, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
						if err != nil {
							return nil, err
						}
						return result, nil
					})
				}
				var consumer pluginhost.Client
				if transport == "native" {
					native := &authorityConsumerFixture{id: "a-consumer", state: pluginhost.StatePrepared}
					native.prepare = func(ctx context.Context) error {
						documents := client(native.ID())
						view, err := documents.Read(ctx, persistence.Query{Namespace: "business", Limit: 1})
						if err != nil {
							return err
						}
						snapshot, err := documents.Export(ctx)
						if err != nil {
							return err
						}
						if view.Revision != 0 || snapshot.Revision != 0 || len(view.Documents) != 0 {
							return errors.New("prepared read changed empty storage")
						}
						if _, err := documents.Commit(ctx, authorityWrite("early", 0, "forbidden")); err == nil {
							return errors.New("prepared consumer wrote")
						}
						return nil
					}
					consumer = native
				} else {
					node, err := exec.LookPath("node")
					if err != nil {
						t.Skip("Node.js is required for process consumer verification")
					}
					script := filepath.Join(root, "consumer.mjs")
					if err := os.WriteFile(script, []byte(authorityPrepareConsumerScript), 0600); err != nil {
						t.Fatal(err)
					}
					consumer, err = pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "a-consumer", Command: node, Args: []string{script}, PluginRoot: root, ProjectRoot: root, WuuHome: home, ServiceRouter: router, PrepareOnly: true, Timeout: 10 * time.Second})
					if err != nil {
						t.Fatal(err)
					}
				}
				session := &authorityConsumerFixture{id: "z-session-state", state: pluginhost.StatePrepared}
				session.prepare = func(ctx context.Context) error {
					gate.mu.Lock()
					prepared := gate.prepared
					gate.mu.Unlock()
					if !prepared {
						return errors.New("ordinary consumer did not prepare before session consumer")
					}
					return gate.Preflight(ctx, client(session.ID()))
				}
				host := pluginhost.New(consumer, provider, session)
				registry, conflicts := pluginhost.BuildServiceRegistry(consumer, provider, session)
				router.mu.Lock()
				router.registry = registry
				router.mu.Unlock()
				if err := registry.WrapServiceInvoker(persistence.ServiceName, 1, gate.Wrap); err != nil {
					t.Fatal(err)
				}
				host.AttachServiceRegistry(registry, conflicts)
				defer host.Close(context.Background())
				defer gate.Close(context.Background())
				if err := host.Prepare(ctx); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{dir, filepath.Join(home, "state-authority", "head.json")} {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("prepare initialized %s: %v", path, err)
					}
				}
				gate.mu.Lock()
				admitted := gate.admitted
				gate.mu.Unlock()
				if admitted || provider.Status().State != pluginhost.StatePrepared {
					t.Fatal("ordinary preparation activated or admitted candidate")
				}
			})
		}
	}
}

const authorityPrepareConsumerScript = `
import readline from 'node:readline';
const pending = new Map();
let nextID = 0;
const send = frame => process.stdout.write(JSON.stringify(frame) + '\n');
const call = (method, params) => new Promise((resolve, reject) => {
  const id = 'storage-' + ++nextID;
  pending.set(id, {resolve, reject});
  send({id, method: 'host.service.call', params: {service: 'state.documents', method, params}});
});
readline.createInterface({input: process.stdin}).on('line', async line => {
  const request = JSON.parse(line);
  if (!request.method) {
    const callback = pending.get(request.id);
    if (callback) {
      pending.delete(request.id);
      request.error ? callback.reject(new Error(request.error.message)) : callback.resolve(request.result);
    }
    return;
  }
  try {
    if (request.method === 'initialize') {
      send({id: request.id, result: {protocol_version: 3, lifecycle_version: 2,
        required_services: [{name: 'state.documents', major_version: 1, required: true}]}});
    } else if (request.method === 'prepare') {
      const view = await call('read', {namespace: 'business', limit: 1});
      const snapshot = await call('export', {});
      if (view.revision !== '0' || snapshot.revision !== '0' || view.documents.length !== 0) throw new Error('prepare changed empty state');
      let rejected = false;
      try { await call('commit', {id: 'forbidden', expected_revision: 0, writes: [{namespace: 'business', key: 'early', value: true}]}); }
      catch { rejected = true; }
      if (!rejected) throw new Error('prepare wrote business state');
      send({id: request.id, result: {}});
    } else if (request.id) send({id: request.id, result: {}});
  } catch (error) { send({id: request.id, error: {code: 'prepare_failed', message: error.message}}); }
});
`
