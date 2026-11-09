package runtime

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
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Exercise the public SDK in an independent process: binding selection, durable
// retries across process restart and backend migration, empty key selection,
// stale writes, and provider revocation without a native fallback.
func TestDocumentStateProcess(t *testing.T) {
	fixture := os.Getenv("WUU_STATE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/document-state-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, home := t.TempDir(), t.TempDir()
	provider := testRuntimePlugin("example.document-state")
	provider.Root, provider.Runtime.Command = fixture, node
	provider.Runtime.Args = []string{"runtime.ts"}
	start := func(module string) *pluginhost.Host {
		t.Helper()
		modules := []string{module, provider.ID}
		host, _, err := buildPluginHost([]pluginpkg.Plugin{provider}, root, "state", home, t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { host.Close(context.Background()) })
		if err := activatePluginHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		if owner, ok := host.ServiceRegistry().ProviderPluginID(persistence.ServiceName, 1); !ok || owner != module {
			t.Fatalf("binding: %q, %v", owner, ok)
		}
		return host
	}
	tx := persistence.Transaction{ID: "create", Writes: []persistence.Write{{Namespace: "example.notes", Key: "one", Value: json.RawMessage(`{"title":"evidence"}`)}}}
	call := func(host *pluginhost.Host, transaction *persistence.Transaction, keys []string) (persistence.View, error) {
		t.Helper()
		args, err := json.Marshal(struct {
			Transaction *persistence.Transaction `json:"transaction,omitempty"`
			Query       persistence.Query        `json:"query"`
		}{transaction, persistence.Query{Namespace: "example.notes", Keys: keys}})
		if err != nil {
			t.Fatal(err)
		}
		definitions := host.ToolDefinitions()
		if len(definitions) != 1 {
			t.Fatalf("tools: %+v", definitions)
		}
		result, err := host.ExecuteTool(ctx, definitions[0].Name, pluginhost.ToolExecuteInput{Arguments: args})
		if err != nil {
			return persistence.View{}, err
		}
		var output struct {
			Receipt *persistence.Receipt `json:"receipt"`
			View    persistence.View     `json:"view"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].Text), &output); err != nil {
			t.Fatal(err)
		}
		if transaction != nil && (output.Receipt == nil || output.Receipt.ID != transaction.ID || output.Receipt.Revision != 1) {
			t.Fatalf("receipt: %+v", output.Receipt)
		}
		return output.View, nil
	}
	host := start(persistence.SQLiteModule)
	view, err := call(host, &tx, nil)
	if err != nil || view.Revision != 1 || len(view.Documents) != 1 {
		t.Fatalf("first commit: %+v, %v", view, err)
	}
	// An incompatible candidate must fail during preparation while the
	// current generation continues serving the persisted binding.
	candidateModules := []string{persistence.JSONLModule, provider.ID}
	candidate, _, candidateErr := buildPluginHost([]pluginpkg.Plugin{provider}, root, "state", home, t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &candidateModules}, "")
	if candidate != nil {
		candidate.Close(ctx)
	}
	if !errors.Is(candidateErr, persistence.ErrMigrationRequired) {
		t.Fatalf("incompatible preparation: %v", candidateErr)
	}
	if _, err := call(host, &tx, nil); err != nil {
		t.Fatal(err)
	}
	if view, err := call(host, nil, []string{}); err != nil || len(view.Documents) != 0 {
		t.Fatalf("empty selection: %+v, %v", view, err)
	}
	stale := tx
	stale.ID = "stale"
	if _, err := call(host, &stale, nil); err == nil {
		t.Fatal("stale transaction succeeded")
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	host = start(persistence.SQLiteModule)
	if _, err := call(host, &tx, nil); err != nil {
		t.Fatalf("restart retry: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(ctx, filepath.Join(statepath.SessionsDir(home), "documents"), "sqlite", "jsonl"); err != nil {
		t.Fatal(err)
	}
	host = start(persistence.JSONLModule)
	view, err = call(host, &tx, nil)
	if err != nil || view.Revision != 3 || len(view.Documents) != 1 {
		t.Fatalf("migrated process: %+v, %v", view, err)
	}
	if _, found := host.RetirePlugin(ctx, persistence.JSONLModule, errors.New("removed")); !found {
		t.Fatal("provider was not retired")
	}
	if len(host.ToolDefinitions()) != 0 {
		t.Fatal("required consumer remained active after provider removal")
	}
	_, snapshot, err := persistence.Inspect(ctx, filepath.Join(statepath.SessionsDir(home), "documents"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 4 {
		t.Fatalf("consumer cleanup did not persist: %+v", snapshot)
	}
	for _, doc := range snapshot.Documents {
		if doc.Namespace == "example.lifecycle" && string(doc.Value) != `{"shutdowns":3}` {
			t.Fatalf("cleanup count: %s", doc.Value)
		}
	}
	t.Log("independent public SDK consumer: SQLite commit/retry, process restart, JSONL migration, empty key selection, stale-write rejection, revocation with no fallback")
}
