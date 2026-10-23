package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

type domainConsumer struct {
	state  pluginhost.State
	store  *session.Store
	thread string
}

func (c *domainConsumer) ID() string { return "acceptance.domain" }
func (c *domainConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: c.state}
}
func (c *domainConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (c *domainConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (c *domainConsumer) Activate(context.Context) error {
	c.state = pluginhost.StateActive
	return nil
}
func (c *domainConsumer) Close(ctx context.Context) error {
	c.state = pluginhost.StateStopped
	if c.thread == "" {
		return nil
	}
	return c.store.WithContext(ctx).AppendHistoryRecord(c.thread, session.HistoryRecord{Role: "assistant", Content: "dependent cleanup persisted"})
}

// The same real session consumer uses the selected native or external provider.
// The unused native provider stays available so retirement detects a hidden
// fallback instead of merely observing an empty registry.
func startAcceptanceProvider(t *testing.T, kind, dir string) (*pluginhost.Host, *persistence.Client, *domainConsumer, *persistence.Service) {
	t.Helper()
	ctx := context.Background()
	selectedID := "acceptance.selected"
	var selected pluginhost.Client
	if kind == "external" {
		fixture := os.Getenv("WUU_REPLACEABLE_FIXTURE")
		if fixture == "" {
			t.Skip("external provider requires node scripts/replaceable-architecture-external.mjs")
		}
		node, err := exec.LookPath("node")
		require.NoError(t, err)
		selected, err = pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: selectedID, Command: node, Args: []string{filepath.Join(fixture, "provider.ts")},
			PluginRoot: fixture, ProjectRoot: dir, WuuHome: dir, Env: map[string]string{"WUU_ACCEPTANCE_STATE": filepath.Join(dir, "external.json")}, Timeout: 5 * time.Second, PrepareOnly: true})
		require.NoError(t, err)
	} else {
		selected = persistence.NewService(selectedID, filepath.Join(dir, "selected"), kind)
	}
	unused := persistence.NewService("acceptance.unselected", filepath.Join(dir, "unselected"), "sqlite")
	consumer := &domainConsumer{state: pluginhost.StatePrepared}
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: selectedID}})
	require.NoError(t, err)
	conflicts := registry.RegisterClients(selected, unused, consumer)
	require.Empty(t, conflicts)
	require.NoError(t, registry.ValidateBindings())
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		raw, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return raw, nil
	})
	consumer.store = session.NewStore(ctx, documents)
	host := pluginhost.New(consumer, unused, selected)
	host.AttachServiceRegistry(registry, conflicts)
	t.Cleanup(func() { require.NoError(t, host.Close(context.Background())) })
	require.NoError(t, host.Activate(ctx))
	provider, found := registry.ProviderPluginID(persistence.ServiceName, 1)
	require.True(t, found)
	require.Equal(t, selectedID, provider)
	return host, documents, consumer, unused
}

func TestAcceptanceSelectedProviderLifecycle(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			host, documents, consumer, unused := startAcceptanceProvider(t, kind, dir)
			_, err := consumer.store.CreateInitialized(session.Session{ID: "selected-thread"}, []session.HistoryRecord{{Role: "user", Content: "persisted through selected service"}})
			require.NoError(t, err)
			consumer.thread = "selected-thread"
			outcome, found := host.RetirePlugin(ctx, "acceptance.selected", errors.New("disabled by acceptance"))
			require.True(t, found)
			require.NoError(t, outcome.Err, "consumer cleanup must retain its selected provider")
			require.Equal(t, pluginhost.StateStopped, consumer.state)
			require.Equal(t, pluginhost.StateActive, unused.Status().State)
			_, err = documents.Read(ctx, persistence.Query{Namespace: "wuu.sessions.v1"})
			require.Error(t, err, "disabled selected provider must not fall back to the active native alternative")
			require.NoError(t, host.Close(ctx))
			_, _, restarted, _ := startAcceptanceProvider(t, kind, dir)
			history, err := restarted.store.LoadHistoryRecords("selected-thread", true)
			require.NoError(t, err)
			require.Len(t, history, 2)
			require.Equal(t, "persisted through selected service", history[0].Content)
			require.Equal(t, "dependent cleanup persisted", history[1].Content)
		})
	}
}

func TestAcceptanceProviderErrorParity(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			_, documents, _, _ := startAcceptanceProvider(t, kind, t.TempDir())
			ctx := context.Background()
			tx := persistence.Transaction{ID: "original", Writes: []persistence.Write{{Namespace: "acceptance.errors", Key: "one", Value: json.RawMessage(`1`)}}}
			_, err := documents.Commit(ctx, tx)
			require.NoError(t, err)
			t.Run("revision-conflict", func(t *testing.T) {
				stale := tx
				stale.ID = "stale"
				_, err := documents.Commit(ctx, stale)
				require.ErrorIs(t, err, persistence.ErrConflict)
			})
			t.Run("operation-conflict", func(t *testing.T) {
				changed := tx
				changed.Writes = []persistence.Write{{Namespace: "acceptance.errors", Key: "one", Value: json.RawMessage(`2`)}}
				_, err := documents.Commit(ctx, changed)
				require.ErrorIs(t, err, persistence.ErrOperationConflict)
			})
			t.Run("invalid-request", func(t *testing.T) {
				_, err := documents.Read(ctx, persistence.Query{})
				require.ErrorIs(t, err, persistence.ErrInvalid)
			})
		})
	}
}
