package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type authorityCleanupConsumer struct {
	*authorityConsumerFixture
	close func(context.Context) error
}

func (c *authorityCleanupConsumer) Close(ctx context.Context) error { return c.close(ctx) }

func TestStateAuthorityCustomCompositionOwnsActivationAndCleanup(t *testing.T) {
	for _, module := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(module, func(t *testing.T) {
			ctx := context.Background()
			home, root := t.TempDir(), t.TempDir()
			consumer := &authorityCleanupConsumer{authorityConsumerFixture: &authorityConsumerFixture{id: "external.documents-user", state: pluginhost.StatePrepared}}
			var documents persistence.Documents
			var cleanupRevision persistence.Revision
			write := func(ctx context.Context, id string) error {
				view, err := documents.Read(ctx, persistence.Query{Namespace: "business"})
				if err != nil {
					return err
				}
				receipt, err := documents.Commit(ctx, authorityWrite(id, view.Revision, id))
				if id == "cleanup" {
					cleanupRevision = receipt.Revision
				}
				return err
			}
			consumer.activate = func(ctx context.Context) error { return write(ctx, "activate") }
			consumer.close = func(ctx context.Context) error { return write(ctx, "cleanup") }
			factory := NativeProviderFactory{ID: consumer.ID(), Build: func(input NativeProviderContext) (pluginhost.Client, error) {
				documents = persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
					result, err := input.Registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
					if err != nil {
						return nil, err
					}
					return result, nil
				})
				return consumer, nil
			}}
			modules := []string{module, consumer.ID()}
			host, kernel, err := buildPluginHost(nil, root, "", home, filepath.Join(home, "workspaces", "proof"), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", factory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(ctx) })
			if kernel.state != nil {
				t.Fatal("custom composition enabled session state implicitly")
			}
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(home, "state-authority", "head.json")); err != nil {
				t.Fatalf("active writer has no durable authority: %v", err)
			}
			if err := host.Close(ctx); err != nil {
				t.Fatalf("cleanup lost selected authority before provider shutdown: %v", err)
			}
			authority, err := NewStateAuthority(home)
			if err != nil {
				t.Fatal(err)
			}
			record, err := authority.readRecord()
			if err != nil || record == nil || record.Pending != nil || cleanupRevision == 0 || record.Checkpoint.Revision != cleanupRevision {
				t.Fatalf("cleanup receipt was not durably accounted for: %+v, %v", record, err)
			}
		})
	}
}
