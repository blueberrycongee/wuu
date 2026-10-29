package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// State ownership must follow the selected graph: no path-based reopen after
// retirement, and the journal's final receipt must precede provider shutdown.
func TestRuntimeStateBindingLifecycle(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "documents")
			registry := pluginhost.NewServiceRegistry()
			provider := persistence.NewService("example.state", dir, backend)
			state := newRuntimeState(registry, "workspace:test")
			host := pluginhost.New()
			host.Add(state)
			host.Add(provider)
			domain := newNativeSessionDomain(registry, t.TempDir(), host)
			host.Add(domain)
			host.AttachServiceRegistry(registry, registry.RegisterClients(state, provider, domain))
			t.Cleanup(func() { _ = host.Close(ctx) })
			if err := host.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			if state.journal != nil {
				t.Fatal("preparation started the inference journal")
			}
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := state.journal.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			runtimeID := state.journal.RuntimeID()
			if _, err := state.documents.Read(ctx, persistence.Query{Namespace: "wuu.inference_journal_runtimes.v1", Keys: []string{runtimeID}}); err != nil {
				t.Fatal(err)
			}
			outcome, found := host.RetirePlugin(ctx, provider.ID(), errors.New("disabled"))
			if !found || outcome.Err != nil {
				t.Fatalf("retire provider: %+v, found=%v", outcome, found)
			}
			if _, err := state.documents.Read(ctx, persistence.Query{Namespace: "wuu.sessions.v1"}); err == nil {
				t.Fatal("retired state binding reopened storage")
			}
			physical, err := persistence.Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer physical.Close()
			if err := persistence.Update(ctx, physical, func(tx *persistence.Tx) error {
				record, err := session.ReadInferenceRuntime(tx, runtimeID)
				if err == nil && record.ClosedAt == 0 {
					t.Error("provider closed before journal cleanup persisted")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A retired graph can still own an admitted inference. Its journal must stay
// live until that graph actually drains, while unrelated autonomous calls stop.
func TestRetainedInferenceJournalHeartbeatAfterPublication(t *testing.T) {
	ctx := context.Background()
	registry := pluginhost.NewServiceRegistry()
	owner := newRuntimeState(registry, "heartbeat-test")
	provider := persistence.NewService("example.state", t.TempDir(), "jsonl")
	host := pluginhost.New(provider, owner)
	domain := newNativeSessionDomain(registry, t.TempDir(), host)
	host.Add(domain)
	host.AttachServiceRegistry(registry, registry.RegisterClients(provider, owner, domain))
	owner.host = host
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(ctx) })
	if err := owner.journal.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	id := owner.journal.RuntimeID()
	read := func() session.InferenceRuntimeRecord {
		t.Helper()
		var record session.InferenceRuntimeRecord
		err := persistence.Update(ctx, owner.documents, func(tx *persistence.Tx) error {
			var err error
			record, err = session.ReadInferenceRuntime(tx, id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	before := read()
	host.StopBackground()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if after := read(); after.HeartbeatAt > before.HeartbeatAt && after.ClosedAt == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("retained inference journal stopped heartbeating after publication")
}

func TestRuntimeStateRequiresSelectedAuthority(t *testing.T) {
	registry := pluginhost.NewServiceRegistry()
	state := newRuntimeState(registry, "workspace:test")
	host := pluginhost.New()
	host.Add(state)
	host.AttachServiceRegistry(registry, registry.RegisterClients(state))
	defer host.Close(context.Background())
	if failures := host.DependencyFailures(); failures[state.ID()] == nil {
		t.Fatal("missing persistence authority was accepted")
	}
	if err := state.Activate(context.Background()); err == nil {
		t.Fatal("state owner silently opened a default backend")
	}
}

func TestDocumentMethodsDoNotAdmitRetiredAutonomousWork(t *testing.T) {
	ctx := context.Background()
	registry := pluginhost.NewServiceRegistry()
	owner := newRuntimeState(registry, "test")
	provider := persistence.NewService("example.state", t.TempDir(), "jsonl")
	host := pluginhost.New(provider, owner)
	domain := newNativeSessionDomain(registry, t.TempDir(), host)
	host.Add(domain)
	host.AttachServiceRegistry(registry, registry.RegisterClients(provider, owner, domain))
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	host.StopBackground()
	raw := json.RawMessage(`{"id":"late-write","expected_revision":"0","writes":[{"namespace":"test","key":"late","value":true}]}`)
	if _, err := registry.Call(ctx, owner.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: "commit", Params: raw}); err == nil || err.Code != "generation_closed" {
		t.Fatalf("retired autonomous write: %v", err)
	}
}
