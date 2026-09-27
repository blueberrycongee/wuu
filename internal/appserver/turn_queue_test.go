package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestReplaceQueuedUserTurnPreservesOrder(t *testing.T) {
	const threadID = "thread-1"
	s := &Server{}
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id:  "queue-1",
		msg: providers.ChatMessage{Role: "user", Content: "first"},
	})
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id:  "queue-2",
		msg: providers.ChatMessage{Role: "user", Content: "second"},
	})
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id:  "queue-3",
		msg: providers.ChatMessage{Role: "user", Content: "third"},
	})

	updated, ok := s.replaceQueuedUserTurn(threadID, "queue-2", providers.ChatMessage{
		Role:    "user",
		Content: "second edited",
	})
	if !ok {
		t.Fatal("replaceQueuedUserTurn returned false")
	}
	if updated.id != "queue-2" || updated.msg.ClientID != "queue-2" {
		t.Fatalf("replacement lost queue id: %+v", updated)
	}
	if updated.msg.Steered {
		t.Fatalf("queued replacement should not be marked steered: %+v", updated.msg)
	}

	var got []string
	for {
		entry, ok := s.takeNextQueuedUserTurn(threadID)
		if !ok {
			break
		}
		got = append(got, entry.id+":"+entry.msg.Content)
	}
	want := []string{
		"queue-1:first",
		"queue-2:second edited",
		"queue-3:third",
	}
	if len(got) != len(want) {
		t.Fatalf("drained %d queued turns, want %d: %v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("drain order mismatch at %d: got %q want %q (all got %v)", index, got[index], want[index], got)
		}
	}
}

func TestReplaceQueuedUserTurnPreservesRuntimeSnapshot(t *testing.T) {
	readOnly := config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	s := &Server{}
	s.enqueueQueuedUserTurn("thread-1", queuedTurn{
		id:       "queue-1",
		msg:      providers.ChatMessage{Role: "user", Content: "first"},
		snapshot: turnRuntimeSnapshot{}.withPermissions(readOnly),
	})

	updated, ok := s.replaceQueuedUserTurn("thread-1", "queue-1", providers.ChatMessage{
		Role:    "user",
		Content: "edited",
	})
	if !ok {
		t.Fatal("replaceQueuedUserTurn returned false")
	}
	if got := updated.snapshot.permissions(); got.Mode != config.PermissionModeReadOnly {
		t.Fatalf("replacement lost permission snapshot: %+v", got)
	}
}

func TestReplaceQueuedUserTurnReturnsFalseWhenMissing(t *testing.T) {
	s := &Server{}
	s.enqueueQueuedUserTurn("thread-1", queuedTurn{
		id:  "queue-1",
		msg: providers.ChatMessage{Role: "user", Content: "first"},
	})

	if _, ok := s.replaceQueuedUserTurn("thread-1", "missing", providers.ChatMessage{
		Role:    "user",
		Content: "edited",
	}); ok {
		t.Fatal("replaceQueuedUserTurn returned true for a missing queue id")
	}

	entry, ok := s.takeNextQueuedUserTurn("thread-1")
	if !ok {
		t.Fatal("queued turn was removed by failed replace")
	}
	if entry.id != "queue-1" || entry.msg.Content != "first" {
		t.Fatalf("failed replace mutated queue: %+v", entry)
	}
}

func TestQueuedTurnCanBeCancelledWhileClaimedForAdmission(t *testing.T) {
	const threadID = "thread-1"
	const queueID = "queue-1"
	s := &Server{}
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id:  queueID,
		msg: providers.ChatMessage{Role: "user", Content: "cancel me"},
	})

	entry, ok := s.takeNextQueuedUserTurn(threadID)
	if !ok {
		t.Fatal("queued turn was not claimed")
	}
	removed, ok := s.removeQueuedUserTurn(threadID, queueID)
	if !ok || removed.id != queueID {
		t.Fatalf("cancel claimed turn = %+v, %v", removed, ok)
	}
	if err := s.commitQueuedTurnClaim(threadID, queueID); !errors.Is(err, errQueuedTurnCancelled) {
		t.Fatalf("commit after cancellation = %v, want errQueuedTurnCancelled", err)
	}
	if cancelled := s.settleQueuedTurnClaim(threadID, entry, true); !cancelled {
		t.Fatal("settlement did not observe cancellation")
	}
	if s.hasQueuedUserTurns(threadID) {
		t.Fatal("cancelled claimed turn was requeued")
	}
}

func TestQueuedTurnCannotBeCancelledAfterCommit(t *testing.T) {
	const threadID = "thread-1"
	const queueID = "queue-1"
	s := &Server{}
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id:  queueID,
		msg: providers.ChatMessage{Role: "user", Content: "already committed"},
	})

	entry, ok := s.takeNextQueuedUserTurn(threadID)
	if !ok {
		t.Fatal("queued turn was not claimed")
	}
	if err := s.commitQueuedTurnClaim(threadID, queueID); err != nil {
		t.Fatalf("commit claim: %v", err)
	}
	if _, removed := s.removeQueuedUserTurn(threadID, queueID); removed {
		t.Fatal("committed queued turn was cancelled")
	}
	s.settleQueuedTurnClaim(threadID, entry, false)
}

func TestTurnRequeueAtomicallyMovesPendingSteerBackToQueue(t *testing.T) {
	const threadID = "thread-1"
	const steerID = "steer-1"
	out := &lockedBuffer{}
	th := &threadState{
		ID:          threadID,
		running:     true,
		currentTurn: "turn-1",
		pendingSteers: []providers.ChatMessage{{
			Role: "user", Content: "send this next", ClientID: steerID, Steered: true,
		}},
		steerDocumentOverrides: []activeDocumentOverride{{
			steerID: steerID, document: &ActiveDocument{Path: "docs/next.md"},
		}},
		activeSteerContextSet: true,
		activeSteerDocument:   &ActiveDocument{Path: "docs/next.md"},
	}
	s := &Server{
		out:                 out,
		threads:             map[string]*threadState{threadID: th},
		pendingQueuedTurns:  make(map[string][]queuedTurn),
		claimedQueuedTurns:  make(map[string]*queuedTurnClaim),
		drainingQueuedTurns: make(map[string]bool),
		activeRunByThread:   make(map[string]string),
	}
	params, err := json.Marshal(TurnRequeueParams{ThreadID: threadID, SteerID: steerID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handleTurnRequeue(Request{ID: json.RawMessage(`"requeue"`), Params: params}); err != nil {
		t.Fatalf("turn/requeue: %v", err)
	}

	th.mu.Lock()
	pendingSteers := len(th.pendingSteers)
	activeDocument := th.activeSteerDocument
	th.mu.Unlock()
	if pendingSteers != 0 || activeDocument != nil {
		t.Fatalf("steer state remained after requeue: pending=%d document=%+v", pendingSteers, activeDocument)
	}
	queued, ok := s.findQueuedUserTurn(threadID, steerID)
	if !ok || queued.msg.Content != "send this next" || queued.msg.Steered {
		t.Fatalf("requeued turn = %+v, %v", queued, ok)
	}
	if queued.snapshot.ActiveDocument == nil || queued.snapshot.ActiveDocument.Path != "docs/next.md" {
		t.Fatalf("requeued document snapshot = %+v", queued.snapshot.ActiveDocument)
	}
	result := remarshal[TurnRequeueResult](
		t,
		responseByID(t, parseOutput(t, out.String()), "requeue")["result"],
	)
	if !result.OK || result.State != "queued" || result.Queued.ID != steerID {
		t.Fatalf("turn/requeue result = %+v", result)
	}
}

func TestTurnRequeueSupersedesStaleQueueCancellation(t *testing.T) {
	const threadID = "thread-1"
	const steerID = "message-1"
	out := &lockedBuffer{}
	th := &threadState{
		ID: threadID,
		pendingSteers: []providers.ChatMessage{{
			Role: "user", Content: "keep me", ClientID: steerID, Steered: true,
		}},
	}
	s := &Server{
		out:                         out,
		threads:                     map[string]*threadState{threadID: th},
		pendingQueuedTurns:          make(map[string][]queuedTurn),
		claimedQueuedTurns:          make(map[string]*queuedTurnClaim),
		cancelledPendingSubmissions: make(map[string]string),
		drainingQueuedTurns:         make(map[string]bool),
		activeRunByThread:           make(map[string]string),
	}
	s.recordCancelledPendingSubmission(threadID, steerID, session.HeldUserWorkOriginQueue)
	params, err := json.Marshal(TurnRequeueParams{ThreadID: threadID, SteerID: steerID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handleTurnRequeue(Request{ID: json.RawMessage(`"requeue"`), Params: params}); err != nil {
		t.Fatalf("turn/requeue: %v", err)
	}

	th.mu.Lock()
	pendingSteers := len(th.pendingSteers)
	th.mu.Unlock()
	queued, ok := s.findQueuedUserTurn(threadID, steerID)
	if pendingSteers != 0 || !ok || queued.msg.Content != "keep me" {
		t.Fatalf("requeue lost message: pending steers=%d queued=%+v ok=%t", pendingSteers, queued, ok)
	}
}

func TestCancelledLateQueueIsNotHeldDuringInterrupt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out)
	if err := s.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := s.thread(threadID)
	th.mu.Lock()
	th.interrupting = true
	th.mu.Unlock()
	s.recordCancelledPendingSubmission(threadID, "message-1", session.HeldUserWorkOriginQueue)
	req := fmt.Sprintf(`{"id":"2","method":"turn/queue","params":{"thread_id":%q,"prompt":"too late","client_id":"message-1"}}`, threadID)
	if err := s.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatal(err)
	}
	held, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Fatalf("cancelled late queue was persisted as held work: %+v", held)
	}
}

func TestPendingUserMessageSummariesIncludeLiveQueueAndSteer(t *testing.T) {
	const threadID = "thread-1"
	th := &threadState{
		ID: threadID,
		pendingSteers: []providers.ChatMessage{{
			Role: "user", Content: "Guide", ClientID: "guide-1", Steered: true,
		}},
		steerDocumentOverrides: []activeDocumentOverride{{
			steerID: "guide-1", document: &ActiveDocument{Path: "docs/guide.md"},
		}},
	}
	s := &Server{
		threads:            map[string]*threadState{threadID: th},
		pendingQueuedTurns: make(map[string][]queuedTurn),
		claimedQueuedTurns: make(map[string]*queuedTurnClaim),
	}
	s.enqueueQueuedUserTurn(threadID, queuedTurn{
		id: "queue-1", msg: providers.ChatMessage{Role: "user", Content: "Next"},
	})

	messages := s.pendingUserMessageSummaries(threadID)
	if len(messages) != 2 {
		t.Fatalf("pending summaries = %+v, want guide and queue", messages)
	}
	if messages[0].ID != "guide-1" || messages[0].Origin != session.HeldUserWorkOriginSteer {
		t.Fatalf("guide summary = %+v", messages[0])
	}
	if messages[0].ActiveDocument == nil || messages[0].ActiveDocument.Path != "docs/guide.md" {
		t.Fatalf("guide summary lost active document: %+v", messages[0])
	}
	if messages[1].ID != "queue-1" || messages[1].Origin != session.HeldUserWorkOriginQueue {
		t.Fatalf("queue summary = %+v", messages[1])
	}
}

func TestEnqueueQueuedUserTurnDeduplicatesStableID(t *testing.T) {
	s := &Server{}
	entry := queuedTurn{
		id: "queue-1", msg: providers.ChatMessage{Role: "user", Content: "Only once"},
	}
	if !s.enqueueQueuedUserTurn("thread-1", entry) {
		t.Fatal("first enqueue was not accepted")
	}
	if s.enqueueQueuedUserTurn("thread-1", entry) {
		t.Fatal("duplicate enqueue was accepted")
	}
	s.queuedTurnMu.Lock()
	count := len(s.pendingQueuedTurns["thread-1"])
	s.queuedTurnMu.Unlock()
	if count != 1 {
		t.Fatalf("queued entries = %d, want 1", count)
	}
}

func TestQueuedTurnCancellationTombstoneRejectsLateSubmissionAndRetry(t *testing.T) {
	const threadID = "thread-1"
	const queueID = "queue-1"
	s := &Server{}
	s.recordCancelledPendingSubmission(threadID, queueID, session.HeldUserWorkOriginQueue)
	entry := queuedTurn{
		id: queueID, msg: providers.ChatMessage{Role: "user", Content: "Too late"},
	}

	if s.enqueueQueuedUserTurn(threadID, entry) {
		t.Fatal("late queue submission was accepted")
	}
	if s.enqueueQueuedUserTurn(threadID, entry) {
		t.Fatal("retried late queue submission was accepted")
	}
	if s.hasQueuedUserTurns(threadID) {
		t.Fatal("cancelled submission was resurrected")
	}
}

func TestSteerCancellationTombstoneRejectsMatchingLateSubmission(t *testing.T) {
	s := &Server{}
	s.recordCancelledPendingSubmission("thread-1", "steer-1", session.HeldUserWorkOriginSteer)
	if !s.isCancelledPendingSubmission("thread-1", "steer-1", session.HeldUserWorkOriginSteer) {
		t.Fatal("steer cancellation tombstone was not retained")
	}
	if s.isCancelledPendingSubmission("thread-1", "steer-1", session.HeldUserWorkOriginQueue) {
		t.Fatal("steer cancellation tombstone blocked the queue delivery mode")
	}
}

func TestExplicitlyHeldQueueDoesNotStartAfterStopSettles(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out)
	if err := s.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	// Stop has already settled: there is no active turn and interrupting=false.
	for i := 0; i < 2; i++ {
		req := fmt.Sprintf(`{"id":"queue-%d","method":"turn/queue","params":{"thread_id":%q,"prompt":"retain me","client_id":"input-%d","hold":true}}`, i, threadID, i)
		if err := s.handleLine(context.Background(), []byte(req)); err != nil {
			t.Fatal(err)
		}
	}
	held, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 || held[0].id != "input-0" || held[1].id != "input-1" {
		t.Fatalf("held input lost or reordered: %+v", held)
	}
	if s.hasQueuedUserTurns(threadID) || threadIsRunning(s.thread(threadID)) {
		t.Fatal("held input must not dispatch")
	}
	// The durable held input survives a new server instance.
	restored, err := New(rt, &lockedBuffer{}).loadHeldUserTurns(threadID)
	if err != nil || len(restored) != 2 {
		t.Fatalf("held input not recoverable: %+v, %v", restored, err)
	}
}
