package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreDurableRunLifecycleAndReload(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		created, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.Status != StatusAccepted || created.Ephemeral || len(created.Turns) != 0 {
			t.Fatalf("created run = %+v", created)
		}

		startedAt := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
		running, err := store.AttachTurn(ctx, created.ID, "thread-1", "turn-1", startedAt)
		if err != nil {
			t.Fatalf("AttachTurn: %v", err)
		}
		if running.Status != StatusRunning || running.StartedAt == nil || len(running.Turns) != 1 {
			t.Fatalf("running run = %+v", running)
		}
		secondAt := startedAt.Add(time.Minute)
		if _, err := store.AttachTurn(ctx, created.ID, "thread-1", "turn-2", secondAt); err != nil {
			t.Fatalf("AttachTurn second: %v", err)
		}
		finishedAt := secondAt.Add(time.Minute)
		if _, err := store.FinishTurn(ctx, created.ID, "turn-1", TurnTerminal{TracePath: "/trace/one.jsonl", At: finishedAt}); err != nil {
			t.Fatalf("FinishTurn: %v", err)
		}
		completed, err := store.Complete(ctx, created.ID, Result{FinalTurnID: "turn-2", TracePath: "/trace/two.jsonl"}, finishedAt.Add(time.Minute))
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if completed.Status != StatusCompleted || completed.CompletedAt == nil || completed.Result == nil || completed.Result.FinalTurnID != "turn-2" {
			t.Fatalf("completed run = %+v", completed)
		}

		reopened, err := restartExecutionStore(t, store)
		if err != nil {
			t.Fatalf("NewStore reopen: %v", err)
		}
		got, err := reopened.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != StatusCompleted || len(got.Turns) != 2 || got.Turns[0].TracePath != "/trace/one.jsonl" {
			t.Fatalf("reloaded run = %+v", got)
		}
	})
}

func TestStoreEphemeralRunStaysInProcess(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		params := testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "ephemeral-thread")
		params.Ephemeral = true
		run, err := store.Create(ctx, params)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.AttachTurn(ctx, run.ID, "ephemeral-thread", "turn-1", time.Now()); err != nil {
			t.Fatalf("AttachTurn: %v", err)
		}
		if got, err := store.Get(ctx, run.ID); err != nil || !got.Ephemeral {
			t.Fatalf("Get ephemeral = %+v, %v", got, err)
		}

		reopened, err := restartExecutionStore(t, store)
		if err != nil {
			t.Fatalf("NewStore reopen: %v", err)
		}
		if _, err := reopened.Get(ctx, run.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get reopened error = %v, want ErrNotFound", err)
		}
	})
}

func TestStoreRejectsInvalidTransitionsAndThreadMismatch(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		run, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeResume, "source-thread", "thread-1"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.AttachTurn(ctx, run.ID, "thread-2", "turn-1", time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("thread mismatch error = %v", err)
		}
		if _, err := store.AttachTurn(ctx, run.ID, "thread-1", "turn-1", time.Now()); err != nil {
			t.Fatalf("AttachTurn: %v", err)
		}
		if _, err := store.Fail(ctx, run.ID, StatusFailed, Result{FinalTurnID: "turn-1"}, Error{Message: "provider failed"}, time.Now()); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if _, err := store.AttachTurn(ctx, run.ID, "thread-1", "turn-2", time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("terminal attach error = %v", err)
		}
		if _, err := store.Complete(ctx, run.ID, Result{}, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("terminal status change error = %v", err)
		}
	})
}

func TestStoreEnforcesOneActiveRunAndOneRunPerTurn(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		first, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1"))
		if err != nil {
			t.Fatalf("Create first: %v", err)
		}
		if _, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1")); !errors.Is(err, ErrConflict) {
			t.Fatalf("second active run error = %v, want ErrConflict", err)
		}
		if _, err := store.AttachTurn(ctx, first.ID, "thread-1", "turn-shared", time.Now()); err != nil {
			t.Fatalf("AttachTurn first: %v", err)
		}
		if _, err := store.Complete(ctx, first.ID, Result{FinalTurnID: "turn-shared"}, time.Now()); err != nil {
			t.Fatalf("Complete first: %v", err)
		}
		second, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeResume, "thread-1", "thread-1"))
		if err != nil {
			t.Fatalf("Create second: %v", err)
		}
		if _, err := store.AttachTurn(ctx, second.ID, "thread-1", "turn-shared", time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("shared turn error = %v, want ErrConflict", err)
		}
	})
}

func TestStoreTerminalReplayMustMatchAndErrorIsSingleLine(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		run, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		result := Result{FinalTurnID: "turn-1", ExitCode: 7}
		terminal, err := store.Fail(ctx, run.ID, StatusFailed, result, Error{Code: "provider", Message: "provider\nfailed"}, time.Now())
		if err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if terminal.Error == nil || terminal.Error.Message != "provider failed" {
			t.Fatalf("terminal error = %+v", terminal.Error)
		}
		if _, err := store.Fail(ctx, run.ID, StatusFailed, result, Error{Code: "provider", Message: "provider failed"}, time.Now()); err != nil {
			t.Fatalf("idempotent Fail: %v", err)
		}
		if _, err := store.Fail(ctx, run.ID, StatusFailed, Result{FinalTurnID: "other", ExitCode: 7}, Error{Code: "provider", Message: "provider failed"}, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflicting replay error = %v", err)
		}
	})
}

func TestStoreListFiltersAndReconcileActive(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		first, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1"))
		if err != nil {
			t.Fatalf("Create first: %v", err)
		}
		secondParams := testCreateParams(store.runtime.RuntimeID(), ModeFork, "source-thread", "thread-2")
		secondParams.Workspace = WorkspaceRef{ID: "other", Root: "/other"}
		second, err := store.Create(ctx, secondParams)
		if err != nil {
			t.Fatalf("Create second: %v", err)
		}
		if _, err := store.AttachTurn(ctx, first.ID, "thread-1", "turn-1", time.Now()); err != nil {
			t.Fatalf("AttachTurn: %v", err)
		}
		runs, err := store.List(ctx, ListOptions{WorkspaceID: "workspace-1"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) != 1 || runs[0].ID != first.ID {
			t.Fatalf("filtered runs = %+v", runs)
		}

		if err := store.runtime.Close(); err != nil {
			t.Fatal(err)
		}
		recovered, err := store.ReconcileOrphans(ctx, "runtime-2", time.Now())
		if err != nil {
			t.Fatalf("ReconcileOrphans: %v", err)
		}
		if len(recovered) != 2 {
			t.Fatalf("reconciled = %d, want 2", len(recovered))
		}
		for _, id := range []string{first.ID, second.ID} {
			got, err := store.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get %s: %v", id, err)
			}
			if got.Status != StatusInterrupted || got.Error == nil || got.Error.Code != "process_restarted" {
				t.Fatalf("reconciled run = %+v", got)
			}
		}
	})
}

func TestStoreValidatesCanonicalRequest(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		params := testCreateParams(store.runtime.RuntimeID(), ModeResume, "", "thread-1")
		if _, err := store.Create(context.Background(), params); err == nil {
			t.Fatal("expected missing source thread error")
		}
		params = testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-1")
		params.Request.TimeoutMS = -1
		if _, err := store.Create(context.Background(), params); err == nil {
			t.Fatal("expected invalid timeout error")
		}
	})
}

func TestStoreReadSnapshotDoesNotMixRunAndTurnVersions(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, store *testExecutionStore) {
		ctx := context.Background()
		run, err := store.Create(ctx, testCreateParams(store.runtime.RuntimeID(), ModeStart, "", "thread-snapshot"))
		if err != nil {
			t.Fatal(err)
		}
		paused := &pausedExecutionDocuments{Documents: store.documents, read: make(chan struct{}), resume: make(chan struct{})}
		reader, err := NewStore(paused)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan Run, 1)
		failures := make(chan error, 1)
		go func() {
			snapshot, err := reader.Get(ctx, run.ID)
			result <- snapshot
			failures <- err
		}()
		<-paused.read
		_, writeErr := store.AttachTurn(ctx, run.ID, run.ThreadID, "turn-snapshot", time.Now().UTC())
		close(paused.resume)
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
		snapshot := <-result
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if snapshot.Status != StatusAccepted || len(snapshot.Turns) != 0 {
			t.Fatalf("mixed snapshot: %+v", snapshot)
		}
		latest, err := store.Get(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if latest.Status != StatusRunning || len(latest.Turns) != 1 {
			t.Fatalf("latest run: %+v", latest)
		}
		listed, err := store.List(ctx, ListOptions{Status: StatusRunning})
		if err != nil || len(listed) != 1 || len(listed[0].Turns) != 1 {
			t.Fatalf("mixed list snapshot: %+v, %v", listed, err)
		}
	})
}

func testCreateParams(runtimeID string, mode Mode, source, threadID string) CreateParams {
	return CreateParams{
		RuntimeID: runtimeID,
		Request:   Request{Mode: mode, SourceThreadID: source, HasPrompt: true},
		Runtime: RuntimeManifest{
			Resolved:        Selection{Provider: "test-provider", Model: "test-model", PermissionMode: "read-only"},
			ProtocolVersion: "wuu-app-server/v0.1",
		},
		Workspace: WorkspaceRef{ID: "workspace-1", Root: "/workspace"},
		ThreadID:  threadID,
	}
}
