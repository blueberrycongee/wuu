package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalmemory"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/session"
)

func newPersonalMemoryReplayFixture(t *testing.T, documents ...func(*persistence.Service) pluginhost.Client) (*personalBoundaryFixture, memoryRecordSource) {
	t.Helper()
	f := newPersonalBoundaryFixture(t, nil, documents...)
	_, err := pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(v map[string]string) error {
		v["fixture.facts."+f.id] = fmt.Sprintf(`{"session_id":%q,"agent_id":"main","main_session_id":%q,"active":true}`, f.id, f.id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := f.rt.Sessions.AppendHistoryRecordReturningSeq(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Original private memory fact", At: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"ref": memoryCanonicalRef(f.id, seq)})
	got, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.source", raw, f.id)
	if err != nil {
		t.Fatal(err)
	}
	var source memorySourceEvidence
	if err := json.Unmarshal(got, &source); err != nil || source.OccurredAt == "" {
		t.Fatalf("canonical source: %s %v", got, err)
	}
	return f, memoryRecordSource{Kind: "message", Ref: source.Ref, OccurredAt: source.OccurredAt}
}

func personalMemoryReplayValue(t *testing.T, f *personalBoundaryFixture, id string, revision int, status, content string, sources []memoryRecordSource, blocked, superseded []string) string {
	t.Helper()
	value, err := json.Marshal(map[string]any{
		"id": id, "revision": revision, "product_domain": "personal", "user_space_id": f.rt.UserSpaceID,
		"status": status, "tier": "detail", "content": content, "contributor_agent_id": "main", "updated_by_agent_id": "main",
		"sources": sources, "blocked_sources": blocked, "superseded_sources": superseded,
		"contributions": []map[string]string{{"agent_id": "main", "executor_session_id": f.id}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func commitPersonalMemoryReplay(t *testing.T, f *personalBoundaryFixture, write memoryWrite) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(write)
	got, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.commit", raw, f.id)
	if err != nil {
		t.Fatal(err)
	}
	var receipt pluginhost.StorageCompareExchangeResult
	if err := json.Unmarshal(got, &receipt); err != nil || !receipt.Swapped || receipt.Value == nil || *receipt.Value != write.Value {
		t.Fatalf("unexpected receipt: %s %v", got, err)
	}
	return got
}

// Retrying a successful request is a read as well as a write boundary. Old
// candidate and accept receipts cannot expose content after the row changes.
func TestPersonalMemoryReplayRequiresCurrentAuthoritativeRecord(t *testing.T) {
	for _, finalStatus := range []string{"forgotten", "corrected"} {
		t.Run(finalStatus, func(t *testing.T) {
			f, source := newPersonalMemoryReplayFixture(t)
			id := "mem_" + personalHash([]byte("replay-visibility"))
			candidate := memoryWrite{ID: "candidate", Key: id, Value: personalMemoryReplayValue(t, f, id, 1, "candidate", "Original private memory fact", []memoryRecordSource{source}, nil, nil)}
			commitPersonalMemoryReplay(t, f, candidate)
			accepted := memoryWrite{ID: "accept", Key: id, Expected: &candidate.Value, Value: personalMemoryReplayValue(t, f, id, 2, "active", "Original private memory fact", []memoryRecordSource{source}, nil, nil)}
			first := commitPersonalMemoryReplay(t, f, accepted)
			if replay := commitPersonalMemoryReplay(t, f, accepted); !bytes.Equal(first, replay) {
				t.Fatalf("unchanged committed receipt differs: %s %s", first, replay)
			}
			final := memoryWrite{ID: finalStatus, Key: id, Expected: &accepted.Value}
			wantError := "forgotten_record"
			if finalStatus == "forgotten" {
				final.Value = personalMemoryReplayValue(t, f, id, 3, "forgotten", "", nil, []string{memorySourceToken(source)}, nil)
			} else {
				correction := memoryRecordSource{Kind: "message", Ref: "manual-main-correction", OccurredAt: "2026-01-01T00:00:00Z"}
				final.Value = personalMemoryReplayValue(t, f, id, 3, "active", "Corrected memory fact", []memoryRecordSource{correction}, nil, []string{memorySourceToken(source)})
				wantError = "revision_conflict"
			}
			commitPersonalMemoryReplay(t, f, final)
			for _, stale := range []memoryWrite{candidate, accepted} {
				for _, operation := range []string{"memory.commit", "memory.operation"} {
					raw, _ := json.Marshal(stale)
					if operation == "memory.operation" {
						raw, _ = json.Marshal(map[string]string{"id": stale.ID})
					}
					got, err := f.host.actionsForTest(t).callMemory(f.owner, operation, raw, f.id)
					if err == nil || !strings.Contains(err.Error(), wantError) || bytes.Contains(got, []byte("Original private memory fact")) {
						t.Errorf("%s replay of %s exposed stale receipt: %s %v", operation, stale.ID, got, err)
					}
				}
			}
			read, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.read", json.RawMessage(`{}`), f.id)
			var records struct {
				Records map[string]string `json:"records"`
			}
			if err != nil || json.Unmarshal(read, &records) != nil || records.Records[id] != final.Value || len(records.Records) != 1 {
				t.Fatalf("replay changed authoritative row: %s %v", read, err)
			}
			if finalStatus == "corrected" {
				commitPersonalMemoryReplay(t, f, final)
			}
			// Neither a different candidate ID nor an operation ID bypasses the
			// canonical source barrier established by forget or correction.
			newID := "mem_" + personalHash([]byte("reintroduced-source"))
			reintroduced := memoryWrite{ID: "reintroduced", Key: newID, Value: personalMemoryReplayValue(t, f, newID, 1, "candidate", "Original private memory fact", []memoryRecordSource{source}, nil, nil)}
			raw, _ := json.Marshal(reintroduced)
			if got, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.commit", raw, f.id); err == nil || !strings.Contains(err.Error(), "source") || len(got) != 0 {
				t.Fatalf("canonical source was reintroduced: %s %v", got, err)
			}
		})
	}
}

// A process can stop after its intent is durable but before publication. Forget
// must also redact that intent so owner recovery cannot return the old content.
func TestPersonalMemoryForgetRedactsPendingRecovery(t *testing.T) {
	f, source := newPersonalMemoryReplayFixture(t)
	id := "mem_" + personalHash([]byte("pending-recovery"))
	accepted := memoryWrite{ID: "accepted", Key: id, Value: personalMemoryReplayValue(t, f, id, 1, "active", "Original private memory fact", []memoryRecordSource{source}, nil, nil)}
	commitPersonalMemoryReplay(t, f, accepted)
	state, err := f.host.actionsForTest(t).retainState(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	selected, _, err := f.host.actionsForTest(t).selectedTaskProvider(state)
	if err != nil {
		t.Fatal(err)
	}
	journal := personalmemory.NewJournal(f.host.actionsForTest(t).memoryDocuments(state), f.rt.UserSpaceID, selected.SubjectID)
	pending, err := journal.Operation(f.owner, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending.ID, pending.Digest = "pending", "pending-exact-request"
	pending.ReceiptDigest = ""
	pending.Expected = &accepted.Value
	pending.Value = personalMemoryReplayValue(t, f, id, 2, "active", "Unpublished private correction", []memoryRecordSource{source}, nil, nil)
	pending.TaskGuardID, pending.SessionGuardID = "pending-task-guard", "pending-session-guard"
	if _, err := journal.Prepare(f.owner, pending); err != nil {
		t.Fatal(err)
	}
	forgotten := memoryWrite{ID: "forgotten", Key: id, Expected: &accepted.Value, Value: personalMemoryReplayValue(t, f, id, 2, "forgotten", "", nil, []string{memorySourceToken(source)}, nil)}
	commitPersonalMemoryReplay(t, f, forgotten)
	for _, operation := range []string{"memory.operations", "memory.reconcile"} {
		input := json.RawMessage(`{}`)
		if operation == "memory.reconcile" {
			input = json.RawMessage(`{"id":"pending","writers_stopped":true,"reason":"Test writer stopped before publication"}`)
		}
		got, err := f.host.actionsForTest(t).callMemory(f.owner, operation, input, f.id)
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		if bytes.Contains(got, []byte("Original private memory fact")) || bytes.Contains(got, []byte("Unpublished private correction")) {
			t.Fatalf("%s exposed forgotten intent: %s", operation, got)
		}
	}
	recovered, err := journal.Operation(f.owner, pending.ID)
	if err != nil || recovered.State != "not-committed" || !recovered.Redacted || recovered.Expected != nil || recovered.Value != "" || recovered.Result != "" {
		t.Fatalf("pending recovery restored forgotten intent: %+v %v", recovered, err)
	}
	if _, err := journal.Publish(f.owner, pending.ID, pending.Digest, nil, nil); err != personalmemory.ErrAborted {
		t.Fatalf("delayed publication crossed forget and recovery: %v", err)
	}
	read, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.read", json.RawMessage(`{}`), f.id)
	if err != nil || bytes.Contains(read, []byte("Original private memory fact")) || bytes.Contains(read, []byte("Unpublished private correction")) {
		t.Fatalf("recovery revived forgotten content: %s %v", read, err)
	}
}

// Canceling an uncertain caller drains its process-generation resources, but
// cannot turn uncertainty into an unlocked task or session. Owner recovery must
// remain possible using the durable intent and original selected providers.
func TestPersonalMemoryCanceledLeaseHandsOffDurableGuards(t *testing.T) {
	f, source := newPersonalMemoryReplayFixture(t)
	state, err := f.host.actionsForTest(t).retainState(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	selected, _, err := f.host.actionsForTest(t).selectedTaskProvider(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.owner)
	defer cancel()
	lease, err := f.host.actionsForTest(t).openMemoryLease(ctx, state, selected)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	bindings, err := f.host.actionsForTest(t).memoryBindings(state)
	if err != nil {
		t.Fatal(err)
	}
	id := "mem_" + personalHash([]byte("canceled-pending"))
	intent := personalmemory.Operation{
		ID: "canceled-pending", Digest: "exact-pending-intent", Key: id,
		Value:     personalMemoryReplayValue(t, f, id, 1, "candidate", "Uncertain private candidate", []memoryRecordSource{source}, nil, nil),
		SessionID: f.id, AgentID: "main", Bindings: bindings,
		TaskGuardID: "canceled-task-guard", SessionGuardID: "canceled-session-guard",
	}
	lease.operation, err = lease.journal.Prepare(lease.ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	recovery := json.RawMessage(`{"id":"canceled-pending","writers_stopped":true,"reason":"Canceled publication returned and its process scopes drained"}`)
	defer func() {
		_, _ = f.host.actionsForTest(t).callMemory(f.owner, "memory.reconcile", recovery, f.id)
	}()
	taskGuard := pluginsettings.InspectionRequest{ReadRequest: pluginsettings.ReadRequest{Subject: selected.SubjectID, Scope: pluginsettings.ScopeUser}, ID: intent.TaskGuardID}
	if _, err := lease.settings.AcquireInspection(lease.ctx, taskGuard); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.sessions.AcquireInspection(lease.ctx, intent.SessionGuardID); err != nil {
		t.Fatal(err)
	}
	// Cancellation cannot close scopes before the active publication stack has
	// drained. The gate supplies that ordering without a scheduling delay.
	f.host.actionsForTest(t).effectGate <- struct{}{}
	f.host.actionsForTest(t).retainMemoryLease(lease)
	cancel()
	<-lease.ctx.Done()
	stillHeld := lease.state.Retain()
	if stillHeld != nil {
		_ = stillHeld.Close()
	}
	<-f.host.actionsForTest(t).effectGate
	if stillHeld == nil {
		t.Fatal("cancellation closed the generation before the publication stack drained")
	}
	// Scope closure has no exported notification. Await its observable state,
	// rather than choosing a delay that would merely assume the callback ran.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		held := lease.state.Retain()
		if held == nil {
			break
		}
		_ = held.Close()
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("canceled memory lease retained its process generation")
		}
	}
	if _, ok := f.host.actionsForTest(t).memoryLeases.Load(intent.ID); ok {
		t.Fatal("canceled process lease remained in the host")
	}
	if _, err := lease.scopes[0].CallCleanup(context.Background(), "personal.agents", "unused", nil); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("canceled process retained service cleanup authority: %v", err)
	}
	writeSettings := func() error {
		_, err := pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, selected.SubjectID, pluginsettings.ScopeUser, func(values map[string]string) error {
			values["handoff-probe"] = "owner recovered"
			return nil
		})
		return err
	}
	writeSession := func() error {
		return f.rt.Sessions.AppendHistoryRecord(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Owner recovered source", At: time.Now().Add(-time.Minute)})
	}
	if err := writeSettings(); !errors.Is(err, persistence.ErrInspectionActive) {
		t.Fatalf("handoff released the durable task guard: %v", err)
	}
	if err := writeSession(); !errors.Is(err, persistence.ErrInspectionActive) {
		t.Fatalf("handoff released the durable session guard: %v", err)
	}
	journal := personalmemory.NewJournal(f.host.actionsForTest(t).memoryDocuments(state), f.rt.UserSpaceID, selected.SubjectID)
	pending, err := journal.Operation(f.owner, intent.ID)
	if err != nil || pending.State != "pending" {
		t.Fatalf("handoff guessed an uncertain publication outcome: %+v %v", pending, err)
	}
	got, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.reconcile", recovery, f.id)
	var recovered personalmemory.Operation
	if err != nil || json.Unmarshal(got, &recovered) != nil || recovered.State != "not-committed" {
		t.Fatalf("owner could not reconcile canceled intent: %s %v", got, err)
	}
	if err := writeSettings(); err != nil {
		t.Fatalf("recovery did not release the task guard: %v", err)
	}
	if err := writeSession(); err != nil {
		t.Fatalf("recovery did not release the session guard: %v", err)
	}
	if _, err := journal.Publish(f.owner, intent.ID, intent.Digest, nil, nil); err != personalmemory.ErrAborted {
		t.Fatalf("late canceled publication crossed owner recovery: %v", err)
	}
}

type uncertainMemoryPublicationService struct {
	*persistence.Service
	mu      sync.Mutex
	mode    string
	delayed *pluginhost.ServiceInvokeParams
	aborted int
}

func (s *uncertainMemoryPublicationService) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var transaction persistence.Transaction
	publication := false
	if input.Method == "commit" {
		var err error
		transaction, err = persistence.DecodeTransaction(input.Params)
		if err != nil {
			return nil, err
		}
		for _, write := range transaction.Writes {
			if write.Namespace == "wuu.personal_memory_records.v1" {
				publication = true
			}
			if write.Namespace == "wuu.personal_memory_operations.v1" {
				var op personalmemory.Operation
				if json.Unmarshal(write.Value, &op) == nil && op.State == "not-committed" {
					s.mu.Lock()
					s.aborted++
					s.mu.Unlock()
				}
			}
		}
	}
	s.mu.Lock()
	fail := publication && s.delayed == nil
	if fail {
		captured := input
		captured.Params = bytes.Clone(input.Params)
		s.delayed = &captured
	}
	mode := s.mode
	s.mu.Unlock()
	if fail {
		if mode == "committed-reply-lost" {
			if _, err := s.Service.InvokeService(ctx, input); err != nil {
				return nil, err
			}
		}
		return nil, errors.New("synthetic memory publication response lost")
	}
	return s.Service.InvokeService(ctx, input)
}

// A mutated local Operation is not a receipt: the selected document provider
// may fail before committing or after committing the exact same transaction.
// The public host must resolve that uncertainty before returning or unlocking.
func TestPersonalMemoryUnknownPublicationRequiresDurableOutcome(t *testing.T) {
	for _, mode := range []string{"delayed-before-commit", "committed-reply-lost"} {
		t.Run(mode, func(t *testing.T) {
			var documents *uncertainMemoryPublicationService
			f, source := newPersonalMemoryReplayFixture(t, func(service *persistence.Service) pluginhost.Client {
				documents = &uncertainMemoryPublicationService{Service: service, mode: mode}
				return documents
			})
			id := "mem_" + personalHash([]byte("unknown-publication"))
			write := memoryWrite{ID: "unknown-publication", Key: id, Value: personalMemoryReplayValue(t, f, id, 1, "candidate", "Uncertain private candidate", []memoryRecordSource{source}, nil, nil)}
			raw, _ := json.Marshal(write)
			got, commitErr := f.host.actionsForTest(t).callMemory(f.owner, "memory.commit", raw, f.id)
			documents.mu.Lock()
			delayed, aborted := documents.delayed, documents.aborted
			documents.mu.Unlock()
			if delayed == nil {
				t.Fatal("test did not reach the selected provider publication boundary")
			}
			if mode == "delayed-before-commit" {
				if commitErr == nil || len(got) != 0 || aborted != 1 {
					t.Fatalf("uncommitted local result was reported as success without terminal abort: result=%s err=%v aborts=%d", got, commitErr, aborted)
				}
			} else {
				var receipt pluginhost.StorageCompareExchangeResult
				if commitErr != nil || json.Unmarshal(got, &receipt) != nil || !receipt.Swapped || receipt.Value == nil || *receipt.Value != write.Value {
					t.Fatalf("durable lost-reply commit was not recovered: %s %v", got, commitErr)
				}
			}
			operations, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.operations", json.RawMessage(`{}`), f.id)
			var listed struct {
				Operations []personalmemory.Operation `json:"operations"`
			}
			if err != nil || json.Unmarshal(operations, &listed) != nil || len(listed.Operations) != 1 {
				t.Fatalf("missing durable outcome: %s %v", operations, err)
			}
			wantState := "not-committed"
			if mode == "committed-reply-lost" {
				wantState = "committed"
			}
			if listed.Operations[0].State != wantState {
				t.Fatalf("returned before durable outcome: %+v", listed.Operations[0])
			}
			// Complete the originally admitted provider transaction after the host
			// has returned. An abort must fence it; a committed request is stable.
			_, lateErr := documents.Service.InvokeService(context.Background(), *delayed)
			var serviceErr *pluginhost.HostServiceError
			if mode == "delayed-before-commit" && (!errors.As(lateErr, &serviceErr) || serviceErr.Code != "state_conflict") {
				t.Fatalf("delayed original CAS crossed terminal abort: %v", lateErr)
			}
			if mode == "committed-reply-lost" && lateErr != nil {
				t.Fatalf("committed transaction lost durable idempotency: %v", lateErr)
			}
			read, err := f.host.actionsForTest(t).callMemory(f.owner, "memory.read", json.RawMessage(`{}`), f.id)
			var records struct {
				Records map[string]string `json:"records"`
			}
			if err != nil || json.Unmarshal(read, &records) != nil {
				t.Fatalf("read after resolution: %s %v", read, err)
			}
			if mode == "delayed-before-commit" && len(records.Records) != 0 || mode == "committed-reply-lost" && (len(records.Records) != 1 || records.Records[id] != write.Value) {
				t.Fatalf("incorrect authoritative records after late completion: %s", read)
			}
		})
	}
}
