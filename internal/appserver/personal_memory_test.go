package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalmemory"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// Bound source provenance is resolved by the host, not worker-selected history
// access. A same-space session alone never grants background source authority.
func TestPersonalMemoryBoundSource(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	seq, err := f.rt.Sessions.AppendHistoryRecordReturningSeq(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Original memory fact", At: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	f.sourceSeq = seq
	_, err = pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(v map[string]string) error {
		v["fixture.facts."+f.id] = fmt.Sprintf(`{"session_id":%q,"agent_id":"main","main_session_id":%q,"active":true}`, f.id, f.id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"ref": fmt.Sprintf("personal-message:%s:%d", f.id, f.sourceSeq)})
	got, err := f.host.actions.callMemory(f.owner, "memory.source", raw, f.id)
	if err != nil {
		t.Fatal(err)
	}
	var source memorySourceEvidence
	if json.Unmarshal(got, &source) != nil || source.Content == "" || source.OccurredAt == "" {
		t.Fatalf("invalid source %s", got)
	}
	for _, ref := range []string{"personal-message:foreign:1", fmt.Sprintf("personal-message:%s:01", f.id), fmt.Sprintf("personal-message:%s:9999", f.id)} {
		raw, _ = json.Marshal(map[string]any{"ref": ref})
		if _, err = f.host.actions.callMemory(f.owner, "memory.source", raw, f.id); err == nil {
			t.Fatalf("unbound source accepted %s", ref)
		}
	}
}

// The public host path uses the selected journal outside the frozen task subject;
// retries use its exact durable request rather than reconstructing timestamps.
func TestPersonalMemoryCommitAndReplay(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	_, err := pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(v map[string]string) error {
		v["fixture.facts."+f.id] = fmt.Sprintf(`{"session_id":%q,"agent_id":"main","main_session_id":%q,"active":true}`, f.id, f.id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "mem_" + personalHash([]byte("public-boundary"))
	value := fmt.Sprintf(`{"id":%q,"revision":1,"product_domain":"personal","user_space_id":%q,"status":"active","tier":"detail","content":"selected journal","contributor_agent_id":"main","updated_by_agent_id":"main","sources":[{"kind":"message","ref":"manual-main-evidence","occurred_at":"2026-01-01T00:00:00Z"}],"contributions":[{"agent_id":"main","executor_session_id":%q}]}`, id, f.rt.UserSpaceID, f.id)
	input, _ := json.Marshal(memoryWrite{ID: "stable-operation", Key: id, Value: value})
	first, err := f.host.actions.callMemory(f.owner, "memory.commit", input, f.id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.host.actions.callMemory(f.owner, "memory.commit", input, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("receipt changed: %s %s", first, second)
	}
	records, err := f.host.actions.callMemory(f.owner, "memory.read", json.RawMessage(`{}`), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(records, []byte("selected journal")) {
		t.Fatalf("missing record %s", records)
	}
}

type unavailableMemoryDocuments struct{ persistence.Documents }

func (unavailableMemoryDocuments) ClaimNamespaces(context.Context, []string) error {
	return errors.New("synthetic documents outage")
}

// A stopped writer hands durable uncertainty to restart recovery, not an
// unreachable service scope that prevents every future provider retirement.
func TestPersonalMemoryShutdownOutageReleasesGeneration(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	state, err := f.host.actions.retainState(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	selected, _, err := f.host.actions.selectedTaskProvider(state)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.host.actions.openMemoryLease(f.owner, state, selected)
	if err != nil {
		t.Fatal(err)
	}
	lease.journal = personalmemory.NewJournal(unavailableMemoryDocuments{}, "space", "subject")
	lease.operation = personalmemory.Operation{ID: "uncertain", Digest: "exact", State: "pending"}
	f.host.actions.memoryLeases.Store("uncertain", lease)
	if err = f.host.actions.closeMemoryLeases(); err == nil {
		t.Fatal("outage not reported")
	}
	if _, ok := f.host.actions.memoryLeases.Load("uncertain"); ok {
		t.Fatal("dead generation retained")
	}
	if _, err = lease.scopes[0].CallCleanup(context.Background(), "personal.agents", "unused", nil); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("scope survived shutdown: %v", err)
	}
}

func TestPersonalMemoryLeaseOwnsInvocationState(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	borrowed, err := f.host.actions.retainState(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	selected, _, err := f.host.actions.selectedTaskProvider(borrowed)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.host.actions.openMemoryLease(f.owner, borrowed, selected)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	if err = borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := runtime.RetainInvocationState(lease.ctx)
	if err != nil {
		t.Fatalf("lease borrowed a closed invocation instead of its owned state: %v", err)
	}
	defer retained.Close()
}
