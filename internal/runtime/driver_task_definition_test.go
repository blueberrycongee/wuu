package runtime

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// Definition lookup precedes task execution in an independently installed
// process. An unavailable implementation cannot silently run the default loop.
func TestDurableTaskDefinitionProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("requires independently built durable plugin fixture")
	}
	h := newDurableProcessHarness(t, fixture, "task-definitions")
	acquire, stop, _ := h.start(t, 1)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	driver, _, release, err := acquire(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, test := range []struct {
		name    string
		version int
		want    string
	}{
		{"missing.definition", 1, "missing_task"},
		{"durable.run", 999, "task_too_old"},
	} {
		state, err := json.Marshal(map[string]any{
			"schema_version": 1, "session_id": h.sessionID, "run_id": "blocked-" + test.want,
			"turn_id": "previous", "phase": "ready", "attempt": 1, "restart_count": 0, "round": 0,
			"task_name": test.name, "task_version": test.version, "task_input": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		cp := loopdriver.Checkpoint{ContractVersion: 1, DriverID: "example.durable-driver", DriverVersion: "1.0.0", State: state}
		instance, err := driver.Resume(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "blocked-" + test.want}, loopdriver.PersistedInput{}, cp)
		if instance != nil {
			instance.Shutdown()
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("definition %s did not block before execution: %v", test.name, err)
		}
		h.write(t, test.want+".json", map[string]any{"error": err.Error(), "checkpoint": cp})
	}
}

// Reopen real v1 process state with a separately compiled v2 definition. The
// provider's versioned checkpoint migrates before the pending tool is invoked.
func TestDurableTaskMigrationProcess(t *testing.T) {
	fixture, upgrade := os.Getenv("WUU_DURABLE_FIXTURE"), os.Getenv("WUU_DURABLE_UPGRADE_FIXTURE")
	if fixture == "" || upgrade == "" {
		t.Skip("requires independent v1 and v2 fixtures")
	}
	h := newDurableProcessHarness(t, fixture, "task-migration")
	initial := []providers.ChatMessage{{Role: "user", Content: "Record exactly one marker, then report the result."}}
	tools := &durableMarkerTools{path: filepath.Join(h.root, "effects.txt")}
	model := &durableControlledModel{}
	store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
	ledger, err := toolledger.New(testSessionDocuments(t, h.sessions), h.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	barrier := &durableCheckpointRecorder{CheckpointStore: store, directory: h.evidence, killPhase: "tools_pending"}
	runner := &agent.StreamRunner{Client: model, Model: "controlled-durable", Tools: tools, ToolLedger: ledger, DriverBindingStore: store, DriverCheckpointStore: barrier}
	attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acquire, stop, pid := h.start(t, 1)
	runner.AcquireLoopDriver = acquire
	barrier.pid = pid
	_, err = runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "before-upgrade"}), initial, nil)
	stop()
	if err == nil || !barrier.killed || tools.count(t) != 0 {
		t.Fatalf("upgrade crash barrier failed: %v", err)
	}
	before, found, err := store.Load(ctx)
	if err != nil || !found {
		t.Fatalf("checkpoint missing: %v", err)
	}
	h.write(t, "before-upgrade.json", before)
	history, err := testSessionStore(t, h.sessions).LoadProviderHistorySnapshot(h.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.fixture = upgrade
	barrier.killPhase = ""
	acquire, stop, _ = h.start(t, 2)
	runner.AcquireLoopDriver = acquire
	result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "after-upgrade"}), workflowRecordsToMessages(t, history.Records), nil)
	stop()
	if err != nil || result.Content != "Marker recorded once." || tools.count(t) != 1 {
		t.Fatalf("upgraded result=%+v effects=%d err=%v", result, tools.count(t), err)
	}
	after, found, err := store.Load(ctx)
	if err != nil || !found {
		t.Fatalf("migrated checkpoint missing: %v", err)
	}
	var state struct {
		TaskVersion int `json:"task_version"`
		TaskInput   struct {
			Migrated bool `json:"migrated"`
		} `json:"task_input"`
	}
	if err := json.Unmarshal(after.State, &state); err != nil {
		t.Fatal(err)
	}
	if state.TaskVersion != 2 || !state.TaskInput.Migrated {
		t.Fatalf("migration not retained: %s", after.State)
	}
	h.write(t, "after-upgrade.json", after)
	h.write(t, "result.json", map[string]any{"version": state.TaskVersion, "migration_retained": state.TaskInput.Migrated, "effect_count": tools.count(t), "final_answer": result.Content})
}
