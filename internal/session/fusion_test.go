package session

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

// Guard durable pair identity, replay-safe dispatch admission, and terminal
// delivery receipts that cannot be reopened by a late waiter or recovery.
func TestFusionPersistenceAndDispatchReplay(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateInitialized(dir, Session{ID: "lead"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateInitialized(dir, Session{ID: "side", Source: "fusion-side", ParentID: "lead"}, nil); err != nil {
		t.Fatal(err)
	}
	pair := config.FusionSelection{Lead: config.ModelRoleConfig{Provider: "p", Model: "lead"}, Sidekick: config.ModelRoleConfig{Provider: "p", Model: "side"}}
	if _, err := SetFusion(dir, "lead", pair, true); err != nil {
		t.Fatal(err)
	}
	changed := pair
	changed.Sidekick.Model = "replacement"
	if _, err := SetFusion(dir, "lead", changed, false); err != nil {
		t.Fatal(err)
	}
	metadata, err := SetFusion(dir, "lead", changed, true)
	if err != nil || metadata.Fusion == nil || *metadata.Fusion != pair || !metadata.FusionEnabled {
		t.Fatalf("pair was repinned: %+v, %v", metadata, err)
	}
	control, err := ChangeControl(dir, "side", "lead", ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := FusionDispatch{ClientID: "call", LeadID: "lead", LeadTurnID: "lead-turn", SideID: "side", Delivery: "waiting"}
	message := InboxMessage{ClientID: "call", SessionID: "side", RelatedSessionID: "lead", Cause: "fusion", Content: "brief", Wake: true, Controls: []Control{control}}
	for i := 0; i < 2; i++ {
		if err := EnqueueFusionDispatch(dir, dispatch, message); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ListFusionDispatches(dir, "lead")
	if err != nil || len(rows) != 1 {
		t.Fatalf("replayed dispatch: %+v, %v", rows, err)
	}
	pending, err := PendingInbox(dir, "side")
	if err != nil || len(pending) != 1 {
		t.Fatalf("replayed brief: %+v, %v", pending, err)
	}
	if err := SetFusionDelivery(dir, "call", "settled"); err != nil {
		t.Fatal(err)
	}
	if err := SetFusionDelivery(dir, "call", "background"); err != nil {
		t.Fatal(err)
	}
	rows, _ = ListFusionDispatches(dir, "lead")
	if rows[0].Delivery != "settled" {
		t.Fatal("late waiter reopened terminal delivery")
	}
	if _, err := ChangeControl(dir, "side", "lead", ControlActive, control.Revision); err != nil {
		t.Fatal(err)
	}
	message.ClientID = "stale"
	dispatch.ClientID = "stale"
	if err := EnqueueFusionDispatch(dir, dispatch, message); err != ErrControlChanged {
		t.Fatalf("stale dispatch admitted: %v", err)
	}
	rows, _ = ListFusionDispatches(dir, "lead")
	if len(rows) != 1 {
		t.Fatal("failed admission left a receipt")
	}
	// A late completion must copy the original dispatch fence, never the new
	// revision that Stop just created. Admission and receipt settlement are atomic.
	if err := EnqueueFusionResult(dir, "call", "late result"); err != ErrControlChanged {
		t.Fatalf("completion crossed the Stop fence: %v", err)
	}
	if messages, err := PendingInbox(dir, "lead"); err != nil || len(messages) != 0 {
		t.Fatalf("late completion woke Lead: %+v, %v", messages, err)
	}
}
