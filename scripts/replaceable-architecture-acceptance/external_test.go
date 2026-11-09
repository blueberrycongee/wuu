package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// Exercise public pagination through the actual registry and SDK process. The
// fixture's storage cost is not measured; page bounds and revision consistency are.
func TestAcceptanceProviderQueryParity(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			_, documents, _, _ := startAcceptanceProvider(t, kind, t.TempDir())
			ctx := context.Background()
			writes := []persistence.Write{}
			for n := 0; n < 257; n++ {
				writes = append(writes, persistence.Write{Namespace: "acceptance.pages", Key: fmt.Sprintf("history/%03d", n), Value: json.RawMessage(fmt.Sprintf(`{"ordinal":%d}`, n))})
			}
			for _, key := range []string{"outside", "unicode/\ue000", "unicode/\U00010000"} {
				writes = append(writes, persistence.Write{Namespace: "acceptance.pages", Key: key, Value: json.RawMessage(`true`)})
			}
			_, err := documents.Commit(ctx, persistence.Transaction{ID: "page-seed", Writes: writes})
			require.NoError(t, err)
			for _, reverse := range []bool{false, true} {
				query := persistence.Query{Namespace: "acceptance.pages", Prefix: "history/", Start: "history/017", End: "history/251", Reverse: reverse, Limit: 31}
				got := []string{}
				for page := 0; ; page++ {
					require.Less(t, page, 10, "cursor must advance")
					view, err := documents.Read(ctx, query)
					require.NoError(t, err)
					require.LessOrEqual(t, len(view.Documents), query.Limit)
					for _, row := range view.Documents {
						got = append(got, row.Key)
					}
					if view.NextCursor == "" {
						break
					}
					require.NotEmpty(t, view.Documents)
					require.Equal(t, view.Documents[len(view.Documents)-1].Key, view.NextCursor)
					query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
				}
				want := []string{}
				for n := 17; n < 251; n++ {
					want = append(want, fmt.Sprintf("history/%03d", n))
				}
				if reverse {
					slices.Reverse(want)
				}
				require.Equal(t, want, got, "all matching keys exactly once in public byte order")
			}
			for _, tc := range []struct {
				query persistence.Query
				keys  []string
			}{
				{persistence.Query{Namespace: "acceptance.pages", Prefix: "history/", Keys: []string{"outside", "history/001", "history/002", "history/002", "history/003"}, Start: "history/002", End: "history/003", Limit: 1}, []string{"history/002"}},
				{persistence.Query{Namespace: "acceptance.pages", Keys: []string{}, Limit: 1}, []string{}},
				{persistence.Query{Namespace: "acceptance.pages", Prefix: "unicode/", Reverse: true, Limit: 2}, []string{"unicode/\U00010000", "unicode/\ue000"}},
			} {
				view, err := documents.Read(ctx, tc.query)
				require.NoError(t, err)
				keys := []string{}
				for _, row := range view.Documents {
					keys = append(keys, row.Key)
				}
				require.Equal(t, tc.keys, keys)
				require.Empty(t, view.NextCursor)
			}
			first, err := documents.Read(ctx, persistence.Query{Namespace: "acceptance.pages", Limit: 1})
			require.NoError(t, err)
			_, err = documents.Commit(ctx, persistence.Transaction{ID: "other-namespace", ExpectedRevision: first.Revision, Writes: []persistence.Write{{Namespace: "acceptance.other", Key: "event", Value: json.RawMessage(`true`)}}})
			require.NoError(t, err)
			_, err = documents.Read(ctx, persistence.Query{Namespace: "acceptance.pages", Limit: 1, Cursor: first.NextCursor, AtRevision: &first.Revision})
			require.ErrorIs(t, err, persistence.ErrConflict)
			zero := persistence.Revision(0)
			_, err = documents.Read(ctx, persistence.Query{Namespace: "acceptance.pages", Keys: []string{}, AtRevision: &zero})
			require.ErrorIs(t, err, persistence.ErrConflict, "zero revision and empty key selector must survive JSON")
			for _, query := range []persistence.Query{
				{Namespace: "acceptance.pages", Cursor: "history/001", Limit: 1},
				{Namespace: "acceptance.pages", Limit: -1},
				{Namespace: "acceptance.pages", Limit: 10001},
				{Namespace: "acceptance.pages", Start: "z", End: "a"},
			} {
				_, err = documents.Read(ctx, query)
				require.ErrorIs(t, err, persistence.ErrInvalid)
			}
		})
	}
}
