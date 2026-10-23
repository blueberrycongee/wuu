package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func indexBatchFixture(t *testing.T, turn string) pluginhost.DataAppendTurnParams {
	t.Helper()
	raw, err := sessiontrace.TurnEvents(sessiontrace.TurnRecord{ThreadID: "thread", TurnID: turn, Status: "completed"}, sessiontrace.FinalRecord{Status: "completed"}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]pluginhost.DataEvent, len(raw))
	for i, event := range raw {
		events[i] = pluginhost.DataEvent(event)
	}
	return pluginhost.DataAppendTurnParams{ThreadID: "thread", TurnID: turn, Events: events}
}
func indexProviderClient(provider *dataQueryService) *IndexClient {
	return NewIndexClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: pluginhost.KernelDataQueryService, Method: method, Params: raw})
	})
}

func TestIndexProviderAtomicBatchReceiptsAndLegacyMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy := sessiontrace.Path(statepath.SessionArtifactDir(dir, "thread"))
	if err := sessiontrace.AppendTurn(legacy, sessiontrace.TurnRecord{ThreadID: "thread", TurnID: "legacy", Status: "completed"}, sessiontrace.FinalRecord{Status: "completed"}, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	provider := activeDataQueryProvider(t, dir)
	client := indexProviderClient(provider)
	batch := indexBatchFixture(t, "new")
	receipt, err := client.AppendTurn(ctx, batch)
	if err != nil || !receipt.Committed || receipt.Duplicate {
		t.Fatalf("append receipt: %+v %v", receipt, err)
	}
	again, err := client.AppendTurn(ctx, batch)
	if err != nil || !again.Committed || !again.Duplicate {
		t.Fatalf("duplicate receipt: %+v %v", again, err)
	}
	batch.Events[1].Data = json.RawMessage(`{"status":"changed"}`)
	if _, err := client.AppendTurn(ctx, batch); err == nil {
		t.Fatal("conflicting batch accepted")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"}); err == nil {
		t.Fatal("closed index fell back")
	}
	reopened := indexProviderClient(activeDataQueryProvider(t, dir))
	// Once imported, a stale diagnostic export cannot replace selected facts.
	if err := os.WriteFile(legacy, []byte("invalid later export"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := reopened.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"})
	if err != nil || len(result.Events) != 4 || result.Events[0].TurnID != "legacy" || result.Events[2].TurnID != "new" {
		t.Fatalf("durable index lost facts: %+v %v", result, err)
	}
}

func TestIndexProviderConcurrentGenerationsDoNotLoseBatches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, second := indexProviderClient(activeDataQueryProvider(t, dir)), indexProviderClient(activeDataQueryProvider(t, dir))
	batches := []pluginhost.DataAppendTurnParams{indexBatchFixture(t, "one"), indexBatchFixture(t, "two")}
	var group sync.WaitGroup
	for i, client := range []*IndexClient{first, second} {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := client.AppendTurn(ctx, batches[i]); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	result, err := first.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"})
	if err != nil || len(result.Events) != 4 {
		t.Fatalf("concurrent batches lost: %+v %v", result, err)
	}
}

func TestIndexProviderMissingReadDoesNotInitializeStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	result, err := indexProviderClient(activeDataQueryProvider(t, dir)).Query(context.Background(), pluginhost.DataQueryParams{ThreadID: "thread"})
	if err != nil || len(result.Events) != 0 {
		t.Fatalf("missing index: %+v %v", result, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("missing read initialized native state: %v", err)
	}
}

func TestIndexProviderExportFailureDoesNotUndoCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	client := indexProviderClient(activeDataQueryProvider(t, dir))
	first, err := client.AppendTurn(ctx, indexBatchFixture(t, "first"))
	if err != nil || first.DiagnosticPath == "" {
		t.Fatalf("initial export: %+v %v", first, err)
	}
	if err := os.Remove(first.DiagnosticPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(first.DiagnosticPath, 0700); err != nil {
		t.Fatal(err)
	}
	second, err := client.AppendTurn(ctx, indexBatchFixture(t, "second"))
	if err != nil || !second.Committed || second.DiagnosticPath != "" {
		t.Fatalf("export failure hid committed batch: %+v %v", second, err)
	}
	result, err := client.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"})
	if err != nil || len(result.Events) != 4 {
		t.Fatalf("export failure lost authoritative facts: %v", err)
	}
}

func TestIndexClientUnknownAppendIsNotReplayed(t *testing.T) {
	calls := 0
	client := NewIndexClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("private-backend-payload")
	})
	_, err := client.AppendTurn(context.Background(), indexBatchFixture(t, "turn"))
	if err == nil || calls != 1 || strings.Contains(err.Error(), "private-backend-payload") {
		t.Fatalf("uncertain append retried or exposed backend: calls=%d err=%v", calls, err)
	}
}

func TestIndexCapturedGenerationAcceptsLateBatchWithoutRerouting(t *testing.T) {
	ctx := context.Background()
	capture := func(id string) (*State, *IndexClient) {
		t.Helper()
		provider := NewDataQueryService(id, t.TempDir())
		consumer := &runtimeServiceConsumer{id: StateServicesConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelDataQueryService, MajorVersion: 2, Required: true}}}
		registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
		if len(conflicts) != 0 {
			t.Fatal(conflicts)
		}
		host := pluginhost.New(provider, consumer)
		host.AttachServiceRegistry(registry, conflicts)
		if err := host.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		generation := &PluginGeneration{id: id, host: host}
		generation.retain()
		state := &State{Generation: generation, Host: host, close: func() error { generation.Release(); return generation.closeErr }}
		t.Cleanup(func() {
			if err := state.Close(); err != nil {
				t.Error(err)
			}
		})
		services, err := state.Services()
		if err != nil {
			t.Fatal(err)
		}
		return state, services.Index
	}
	old, previous := capture("index.old")
	_, current := capture("index.new")
	old.Host.StopBackground()
	if _, err := previous.AppendTurn(ctx, indexBatchFixture(t, "late")); err != nil {
		t.Fatalf("captured foreground completion lost admission: %v", err)
	}
	if result, err := previous.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"}); err != nil || len(result.Events) != 2 {
		t.Fatalf("old index lost late batch: %+v %v", result, err)
	}
	if result, err := current.Query(ctx, pluginhost.DataQueryParams{ThreadID: "thread"}); err != nil || len(result.Events) != 0 {
		t.Fatalf("late batch rerouted to new index: %+v %v", result, err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := previous.AppendTurn(ctx, indexBatchFixture(t, "released")); err == nil {
		t.Fatal("released generation accepted index write")
	}
}

// Limited reads stop at the requested event; a malformed unneeded tail is
// neither parsed nor required, while an unbounded query still rejects it.
func TestIndexProviderBoundedQueryDoesNotReadRemainingDocument(t *testing.T) {
	dir := t.TempDir()
	provider := activeDataQueryProvider(t, dir)
	client := indexProviderClient(provider)
	if _, err := client.AppendTurn(context.Background(), indexBatchFixture(t, "turn")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(statepath.SessionArtifactDir(dir, "thread"), nativeIndexFile)
	first, err := json.Marshal(indexBatchFixture(t, "turn").Events[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte(`{"version":1,"events":[`), first...), []byte(`,broken-tail`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := client.Query(context.Background(), pluginhost.DataQueryParams{ThreadID: "thread", Limit: 1}); err != nil || len(result.Events) != 1 {
		t.Fatalf("bounded read inspected unused tail: %+v %v", result, err)
	}
	if _, err := client.Query(context.Background(), pluginhost.DataQueryParams{ThreadID: "thread"}); err == nil {
		t.Fatal("full read accepted corrupt tail")
	}
}
