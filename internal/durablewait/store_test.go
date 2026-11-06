package durablewait

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"testing"
	"time"
)

// Recovery boundaries: changed intent and wrong owners cannot reuse an ID;
// expiry cannot authorize dispatch; duplicate decisions cannot reverse a denial;
// only a pre-dispatch host fence proves not_started; a committed dispatch marker
// remains unknown after a crash, including when approval expires.
func TestWaitRecoveryContract(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			documents, err := persistence.Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			store := New(documents, func() time.Time { return now })
			owner := Owner{PluginID: "driver", SessionID: "session", ExecutionID: "execution", CallID: "call"}
			request := Intent{Version: 1, ID: "wait", ExpiresAt: now.Add(time.Minute), Question: "Proceed?"}
			wait, err := store.Register(ctx, owner, request)
			if err != nil || wait.EffectState != "unknown" {
				t.Fatalf("register %+v %v", wait, err)
			}
			if _, err := store.Fence(ctx, owner, "wait"); !errors.Is(err, ErrUnknown) {
				t.Fatalf("unproven fence: %v", err)
			}
			other := owner
			other.SessionID = "other"
			if _, err := store.Inspect(ctx, other, "wait"); !errors.Is(err, ErrOwner) {
				t.Fatalf("owner: %v", err)
			}
			changed := request
			changed.Question = "Different?"
			if _, err := store.Register(ctx, owner, changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed: %v", err)
			}
			request.ID = "safe"
			if _, err := store.RegisterBeforeDispatch(ctx, owner, request); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Decide(ctx, owner, "safe", "deny"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Decide(ctx, owner, "safe", "approve"); !errors.Is(err, ErrConflict) {
				t.Fatalf("reverse: %v", err)
			}
			if _, err := store.Fence(ctx, owner, "safe"); !errors.Is(err, ErrConflict) {
				t.Fatalf("denied reask: %v", err)
			}
			if _, err := store.BeginDispatch(ctx, owner, "safe"); !errors.Is(err, ErrConflict) {
				t.Fatalf("denied dispatch: %v", err)
			}
			request.ID = "expired"
			if _, err := store.RegisterBeforeDispatch(ctx, owner, request); err != nil {
				t.Fatal(err)
			}
			request.ID = "started"
			if _, err := store.RegisterBeforeDispatch(ctx, owner, request); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Decide(ctx, owner, "started", "approve"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.BeginDispatch(ctx, owner, "started"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.BeginDispatch(ctx, owner, "started"); !errors.Is(err, ErrUnknown) {
				t.Fatalf("duplicate dispatch: %v", err)
			}
			if err := documents.Close(); err != nil {
				t.Fatal(err)
			}
			documents, err = persistence.Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			now = now.Add(time.Minute)
			store = New(documents, func() time.Time { return now })
			if _, err := store.Decide(ctx, owner, "expired", "approve"); !errors.Is(err, ErrExpired) {
				t.Fatalf("expiry: %v", err)
			}
			fenced, err := store.Fence(ctx, owner, "expired")
			if err != nil || fenced.EffectState != "not_started" || fenced.Status != "fenced" {
				t.Fatalf("fence: %+v %v", fenced, err)
			}
			if _, err := store.BeginDispatch(ctx, owner, "expired"); err == nil {
				t.Fatal("fenced dispatch")
			}
			if _, err := store.Fence(ctx, owner, "started"); !errors.Is(err, ErrUnknown) {
				t.Fatalf("crash fence: %v", err)
			}
		})
	}
}

// Gate integration must reject post-dispatch registration, unattested restores,
// missing/changed batch claims, and dispatch after a concurrent pre-effect fence.
func TestBatchGateRecovery(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := New(documents, func() time.Time { return now })
	owner := Owner{PluginID: "driver", SessionID: "session", ExecutionID: "first", CallID: "approval"}
	intent := Intent{Version: 1, ID: "approval", Question: "Write file?", Kind: "approval", BatchID: "batch", CallsDigest: "digest", ExpiresAt: now.Add(time.Minute)}
	if _, err := s.ReserveBatch(ctx, owner, intent, false); !errors.Is(err, ErrUnknown) {
		t.Fatalf("restored unproven: %v", err)
	}
	if _, err := s.ReserveBatch(ctx, owner, intent, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchBatch(ctx, owner, "batch", "digest", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("bypass: %v", err)
	}
	if _, err := s.Decide(ctx, owner, "approval", "approve"); err != nil {
		t.Fatal(err)
	}
	next := owner
	next.ExecutionID = "resumed"
	if err := s.DispatchBatch(ctx, next, "batch", "changed", "approval"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed digest: %v", err)
	}
	if err := s.DispatchBatch(ctx, next, "batch", "digest", "approval"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fence(ctx, owner, "approval"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("started fence: %v", err)
	}
	if err := s.DispatchBatch(ctx, next, "batch", "digest", "approval"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("replay: %v", err)
	}
	intent.ID = "late"
	if _, err := s.ReserveBatch(ctx, next, intent, true); !errors.Is(err, ErrUnknown) {
		t.Fatalf("late registration: %v", err)
	}
	intent.BatchID = "expires"
	intent.ID = "old"
	if _, err := s.ReserveBatch(ctx, owner, intent, true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := s.Fence(ctx, owner, "old"); err != nil {
		t.Fatal(err)
	}
	intent.ID = "new"
	intent.ExpiresAt = now.Add(time.Minute)
	if _, err := s.ReserveBatch(ctx, next, intent, false); err != nil {
		t.Fatalf("fenced reask: %v", err)
	}
	if _, err := s.Decide(ctx, next, "new", "approve"); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchBatch(ctx, next, "expires", "digest", "new"); err != nil {
		t.Fatal(err)
	}
}

// Only a later host-admitted input can resolve input/event waits. The original
// input, foreign session, approval, and an expired wait cannot be relabeled.
func TestAdmittedInputResolvesWait(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := New(documents, func() time.Time { return now })
	owner := Owner{PluginID: "driver", SessionID: "session", ExecutionID: "first", CallID: "wait"}
	intent := Intent{Version: 1, ID: "input", Kind: "input", Question: "Which file?", ExpiresAt: now.Add(time.Minute), InputBoundary: &InputRef{SessionID: "session", Seq: 2}}
	if _, err := s.Register(ctx, owner, intent); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ResolveInput(ctx, owner, "input", InputRef{SessionID: "session", Seq: 2}); err != nil || r.Status != "pending" {
		t.Fatalf("old input: %+v %v", r, err)
	}
	next := owner
	next.ExecutionID = "next"
	if _, err := s.ResolveInput(ctx, next, "input", InputRef{SessionID: "foreign", Seq: 3}); !errors.Is(err, ErrOwner) {
		t.Fatalf("foreign input: %v", err)
	}
	r, err := s.ResolveInput(ctx, next, "input", InputRef{SessionID: "session", Seq: 3})
	if err != nil || r.Status != "resolved" || r.ResponseRef == nil || r.ResponseRef.Seq != 3 {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	if _, err := s.ResolveInput(ctx, next, "input", InputRef{SessionID: "session", Seq: 3}); err != nil {
		t.Fatalf("duplicate source: %v", err)
	}
	if _, err := s.ResolveInput(ctx, next, "input", InputRef{SessionID: "session", Seq: 4}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source: %v", err)
	}
	intent.ID = "approval"
	intent.Kind = "approval"
	if _, err := s.Register(ctx, owner, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveInput(ctx, next, "approval", InputRef{SessionID: "session", Seq: 4}); !errors.Is(err, ErrConflict) {
		t.Fatalf("input approved effect: %v", err)
	}
	intent.ID = "event"
	intent.Kind = "event"
	if _, err := s.Register(ctx, owner, intent); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := s.ResolveInput(ctx, next, "event", InputRef{SessionID: "session", Seq: 4}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired input: %v", err)
	}
}
