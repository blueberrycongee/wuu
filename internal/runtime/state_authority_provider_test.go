package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Contract failure cases: a lost reply must not create a local business journal;
// another authorized writer may advance the remote revision; identity changes,
// malformed declarations and unresolved local migrations must fail closed.
type providerAuthorityFixture struct {
	*authorityProviderFixture
	info    persistence.Authority
	failure error
}

func (p *providerAuthorityFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return p.Service.ProvidedServices()
}
func (p *providerAuthorityFixture) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "authority" {
		if p.failure != nil {
			return nil, p.failure
		}
		return json.Marshal(p.info)
	}
	return p.authorityProviderFixture.InvokeService(ctx, params)
}
func remoteAuthorityInfo() persistence.Authority {
	return persistence.Authority{Version: 1, Mode: persistence.ProviderAuthority, StoreID: "remote-account/dataset", StorageTopology: persistence.MultiHost, ExecutionTopology: persistence.SingleHost, Coordinator: "remote-transaction-domain", CAS: "store", DurableReceipts: true}
}
func providerAuthorityClient(t *testing.T, home string, provider *providerAuthorityFixture) (*StateAuthority, *persistence.Client) {
	t.Helper()
	gate, err := NewStateAuthority(home)
	if err != nil {
		t.Fatal(err)
	}
	invoker := gate.Wrap(provider)
	client := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return invoker.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: persistence.ServiceName, Method: method, Params: raw})
	})
	t.Cleanup(func() { _ = gate.Close(context.Background()) })
	return gate, client
}
func TestProviderAuthorityNoLocalIntentAndConcurrentRevision(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	provider := &providerAuthorityFixture{authorityProviderFixture: &authorityProviderFixture{Service: persistence.NewService("independent.store", t.TempDir(), "sqlite")}, info: remoteAuthorityInfo()}
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	gate, client := providerAuthorityClient(t, home, provider)
	if err := gate.Preflight(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := gate.Activate(ctx, client); err != nil {
		t.Fatal(err)
	}
	tx := authorityWrite("unknown", 0, "private-business-payload")
	provider.loseID = tx.ID
	if _, err := client.Commit(ctx, tx); err == nil {
		t.Fatal("lost response succeeded")
	}
	receipt, err := client.Commit(ctx, tx)
	if err != nil || receipt.Revision != 1 {
		t.Fatalf("exact retry: %+v %v", receipt, err)
	}
	if _, err := os.Stat(filepath.Join(home, "state-authority", "head.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider mode created local business journal: %v", err)
	}
	err = filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(raw), "private-business-payload") {
			t.Errorf("local business copy: %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	outside := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
	})
	if _, err := outside.Commit(ctx, authorityWrite("other-host", 1, "remote")); err != nil {
		t.Fatal(err)
	}
	view, err := client.Read(ctx, persistence.Query{Namespace: "business"})
	if err != nil || view.Revision != 2 {
		t.Fatalf("remote revision advance treated as binding change: %+v %v", view, err)
	}
	stale := authorityWrite("stale", 0, "bad")
	if _, err := client.Commit(ctx, stale); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CAS conflict: %v", err)
	}
	if err := gate.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedClient := providerAuthorityClient(t, home, provider)
	if err := reopened.Preflight(ctx, reopenedClient); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Activate(ctx, reopenedClient); err != nil {
		t.Fatal(err)
	}
	if _, err := reopenedClient.Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	provider.info.StoreID = "different-dataset"
	if _, err := reopenedClient.Read(ctx, persistence.Query{Namespace: "business"}); !errors.Is(err, persistence.ErrBindingChanged) {
		t.Fatalf("changed active identity: %v", err)
	}
	replacement, replacementClient := providerAuthorityClient(t, home, provider)
	if err := replacement.Preflight(ctx, replacementClient); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("changed persisted identity: %v", err)
	}
}

func TestProviderAuthorityDeclarationFailsClosed(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*providerAuthorityFixture)
	}{
		{"missing store", func(p *providerAuthorityFixture) { p.info.StoreID = "" }},
		{"missing coordinator", func(p *providerAuthorityFixture) { p.info.Coordinator = "" }},
		{"missing receipts", func(p *providerAuthorityFixture) { p.info.DurableReceipts = false }},
		{"unsupported version", func(p *providerAuthorityFixture) { p.info.Version = 2 }},
		{"unavailable", func(p *providerAuthorityFixture) { p.failure = errors.New("remote unavailable") }},
	} {
		t.Run(change.name, func(t *testing.T) {
			provider := &providerAuthorityFixture{authorityProviderFixture: &authorityProviderFixture{Service: persistence.NewService("external.store", t.TempDir(), "sqlite")}, info: remoteAuthorityInfo()}
			change.mutate(provider)
			gate, client := providerAuthorityClient(t, t.TempDir(), provider)
			if err := gate.Preflight(context.Background(), client); err == nil {
				t.Fatal("invalid advertised authority fell back to local authority")
			}
		})
	}
}

func TestProviderAuthorityMigrationRequiresQuiescentVerifiedSource(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	source := newAuthorityFixture(t, home, t.TempDir(), "sqlite")
	admitAuthority(t, source)
	head := authorityHead(t, source.documents)
	tx := authorityWrite("migrate", head.Revision, "preserve")
	if _, err := source.documents.Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.documents.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := &providerAuthorityFixture{authorityProviderFixture: &authorityProviderFixture{Service: persistence.NewService("remote.store", t.TempDir(), "jsonl")}, info: remoteAuthorityInfo()}
	if err := target.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer target.Close(ctx)
	raw := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return target.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: params})
	})
	if _, err := raw.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	gate, client := providerAuthorityClient(t, home, target)
	if err := gate.Preflight(ctx, client); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("live source migration: %v", err)
	}
	source.close(t)
	if err := gate.Preflight(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := gate.Activate(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Commit(ctx, tx); err != nil {
		t.Fatalf("migrated durable receipt: %v", err)
	}
	old := newAuthorityFixture(t, home, t.TempDir(), "sqlite")
	if err := old.gate.Preflight(ctx, old.documents); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("native authority revived behind remote binding: %v", err)
	}
}

func TestNativeExecutionRejectsSharedExecutionNotRemoteStorage(t *testing.T) {
	for _, executionTopology := range []string{persistence.SingleHost, persistence.MultiHost} {
		t.Run(executionTopology, func(t *testing.T) {
			registry := pluginhost.NewServiceRegistry()
			provider := &providerAuthorityFixture{authorityProviderFixture: &authorityProviderFixture{Service: persistence.NewService("remote.store", t.TempDir(), "sqlite")}, info: remoteAuthorityInfo()}
			provider.info.ExecutionTopology = executionTopology
			domain := newNativeSessionDomain(registry, t.TempDir())
			host := pluginhost.New(provider, domain)
			host.AttachServiceRegistry(registry, registry.RegisterClients(provider, domain))
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			err := host.Prepare(context.Background())
			if executionTopology == persistence.MultiHost {
				if err == nil || !strings.Contains(err.Error(), "execution coordinator") {
					t.Fatalf("shared execution was not rejected: %v", err)
				}
			} else if err != nil {
				t.Fatalf("cloud storage with one execution host rejected: %v", err)
			}
		})
	}
}

func TestProviderAuthorityRefusesUnresolvedLocalCommit(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed-response-lost", true: "request-lost"}[before], func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			source := newAuthorityFixture(t, home, t.TempDir(), "sqlite")
			admitAuthority(t, source)
			head := authorityHead(t, source.documents)
			source.provider.loseID, source.provider.loseBefore = "uncertain", before
			if _, err := source.documents.Commit(ctx, authorityWrite("uncertain", head.Revision, "pending-private")); err == nil {
				t.Fatal("unknown outcome hidden")
			}
			source.close(t)
			target := &providerAuthorityFixture{authorityProviderFixture: &authorityProviderFixture{Service: persistence.NewService("remote", t.TempDir(), "sqlite")}, info: remoteAuthorityInfo()}
			if err := target.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer target.Close(ctx)
			gate, client := providerAuthorityClient(t, home, target)
			if err := gate.Preflight(ctx, client); !errors.Is(err, persistence.ErrMigrationRequired) || !strings.Contains(err.Error(), "pending transaction") {
				t.Fatalf("unknown local outcome guessed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, "state-authority", "provider.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed transition published provider identity: %v", err)
			}
		})
	}
}
