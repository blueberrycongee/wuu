package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestPersonalReplacementFactsDriveReviewMemoryAndConnector(t *testing.T) {
	f := newPersonalBoundaryFixtureWithProvider(t, nil, "independent-assistant")
	subject := "plugin:bundled:independent-assistant"
	facts := personalExecutionFacts{SessionID: f.id, AgentID: "main", MainSessionID: f.id, Active: true}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(facts)
		_, err := pluginsettings.UpdateState(f.rt.ProductDataRoot(), f.rt.RootDir, subject, pluginsettings.ScopeUser, func(values map[string]string) error { values["fixture.facts."+f.id] = string(raw); return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	save()
	seq, err := f.rt.Sessions.AppendHistoryRecordReturningSeq(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Save my requested note and remember the fact.", At: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	f.sourceSeq = seq
	// No bundled Personal keys exist; the replacement chooses its own format.
	document, err := pluginsettings.ReadState(f.rt.ProductDataRoot(), f.rt.RootDir, subject, pluginsettings.ScopeUser)
	if err != nil || len(document.Values) != 1 {
		t.Fatalf("replacement state: %+v %v", document, err)
	}
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: f.rt.UserSpaceID, SessionID: f.id}
	authority, err := f.host.actionsForTest(t).connectorAuthority(f.owner, actor)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := f.host.actionsForTest(t).withConnectorAuthorization(f.owner, actor, authority, func(context.Context) error {
		called = true
		_, updateErr := pluginsettings.UpdateState(f.rt.ProductDataRoot(), f.rt.RootDir, subject, pluginsettings.ScopeUser, func(values map[string]string) error { values["fixture.facts."+f.id] = `{}`; return nil })
		if updateErr == nil {
			t.Error("provider facts changed while effect inspection was held")
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("connector effect: %v %v", called, err)
	}
	raw, _ := json.Marshal(map[string]string{"ref": memoryCanonicalRef(f.id, f.sourceSeq)})
	sourceRaw, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.source", raw, f.id)
	if err != nil {
		t.Fatal(err)
	}
	var source memorySourceEvidence
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal(err)
	}
	value := personalMemoryReplayValue(t, f, "mem_"+personalHash([]byte("replacement-fact")), 1, "candidate", "Requested note", []memoryRecordSource{{Kind: "message", Ref: source.Ref, OccurredAt: source.OccurredAt}}, nil, nil)
	commitPersonalMemoryReplay(t, f, memoryWrite{ID: "replacement-write", Key: "mem_" + personalHash([]byte("replacement-fact")), Value: value})
	record := f.pending(f.sourceContext())
	// Hold dispatch while the owner approves, then revoke the provider's facts.
	f.host.actionsForTest(t).drainMu.Lock()
	if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(record), ""); err != nil {
		f.host.actionsForTest(t).drainMu.Unlock()
		t.Fatal(err)
	}
	facts.Active = false
	save()
	f.host.actionsForTest(t).drainMu.Unlock()
	called = false
	if err := f.host.actionsForTest(t).withConnectorAuthorization(f.owner, actor, authority, func(context.Context) error { called = true; return nil }); err == nil || called {
		t.Fatalf("inactive replacement executed: %v %v", called, err)
	}
	if err := f.host.actionsForTest(t).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale approval wrote file: %v", err)
	}
}

func TestPersonalReplacementReviewExecutes(t *testing.T) {
	f := newPersonalBoundaryFixtureWithProvider(t, nil, "independent-assistant")
	record := f.pending(f.sourceContext())
	if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(record), ""); err != nil {
		t.Fatal(err)
	}
	if err := f.host.actionsForTest(t).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertContents(filepath.Join(f.rt.RootDir, "note.txt"), "saved once")
}

func TestPersonalReplacementTaskRevisionInvalidatesApprovedAction(t *testing.T) {
	f := newPersonalBoundaryFixtureWithProvider(t, nil, "independent-assistant")
	worker := "replacement-worker"
	_, err := f.rt.Sessions.CreateInitialized(session.Session{ID: worker, ParentID: f.id, AgentID: "main", UserSpaceID: f.rt.UserSpaceID, ProductDomain: "personal", Owner: "plugin:independent-assistant", Visibility: "plugin", CWD: f.rt.RootDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	facts := personalExecutionFacts{SessionID: worker, AgentID: "main", MainSessionID: f.id, Active: true, Task: &personalTaskFacts{ID: "replacement-task", Revision: 1, RunID: "replacement-run", Goal: "Save note", Active: true, SourceSessionID: f.id, SourceMessageSeq: f.sourceSeq}}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(facts)
		_, err := pluginsettings.UpdateState(f.rt.ProductDataRoot(), f.rt.RootDir, "plugin:bundled:independent-assistant", pluginsettings.ScopeUser, func(values map[string]string) error { values["fixture.facts."+worker] = string(raw); return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	save()
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	selected, _, err := f.host.actionsForTest(t).selectedTaskProvider(state)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := f.host.actionsForTest(t).authority(f.owner, state, selected, worker, "replacement-task")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _, err := state.Sessions.Find(worker)
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest, _ := json.Marshal(struct {
		Parts  []string
		Source []personalreview.HumanMessage
	}{
		[]string{metadata.ID, metadata.CWD, metadata.EngineID, metadata.PermissionMode, selected.ID, selected.SubjectID, selected.Fingerprint, "replacement-task", "Save note", "1", "replacement-run"}, observed.UserMessages,
	})
	if observed.Scope.ContextDigest != personalHash(legacyDigest) {
		t.Fatal("public facts changed an unchanged task's approval digest")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	f.kit.SetSessionID(worker)
	ctx, cancel := context.WithCancel(f.owner)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.kit.Execute(ctx, f.call()); done <- err }()
	var record personalreview.Record
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
waitPending:
	for {
		select {
		case err := <-done:
			t.Fatalf("worker returned before decision: %v", err)
		case <-deadline.C:
			t.Fatal("pending approval unavailable")
		case <-tick.C:
			records, err := f.host.actionsForTest(t).engine.Records(ctx, f.rt.UserSpaceID)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) == 1 && records[0].Status == personalreview.AwaitingConfirmation {
				record = records[0]
				break waitPending
			}
		}
	}
	f.host.actionsForTest(t).drainMu.Lock()
	if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(record), ""); err != nil {
		f.host.actionsForTest(t).drainMu.Unlock()
		t.Fatal(err)
	}
	facts.Task.Revision = 2
	facts.Task.Goal = "Different request"
	save()
	f.host.actionsForTest(t).drainMu.Unlock()
	if err := f.host.actionsForTest(t).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("old revision approval executed: %v", err)
	}
}
