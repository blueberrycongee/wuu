package session

import "testing"

// Failure cases: retries advance a requirement twice; competing updates lose
// a requirement; an old report passes review; Stop races a result into Lead;
// completed work is replayed when the store is reopened.
func TestFusionTaskRevisionReviewAndCancellation(t *testing.T) {
	dir := t.TempDir()
	for _, s := range []Session{{ID: "lead"}, {ID: "side", Source: "fusion-side", ParentID: "lead"}} {
		if _, err := CreateInitialized(dir, s, nil); err != nil {
			t.Fatal(err)
		}
	}
	control, err := ChangeControl(dir, "side", "lead", ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	task := FusionTask{ID: "task", LeadID: "lead", SideID: "side", LeadTurnID: "lead-turn"}
	dispatch := FusionDispatch{ClientID: "first", LeadID: "lead", SideID: "side", LeadTurnID: "lead-turn", TaskID: task.ID, Kind: "delegate", Delivery: "waiting"}
	input := InboxMessage{ClientID: dispatch.ClientID, SessionID: "side", RelatedSessionID: "lead", Cause: "fusion", Content: "brief", Wake: true, Controls: []Control{control}}
	task, err = AdmitFusionTask(dir, task, dispatch, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	if task.Revision != 1 || task.State != FusionTaskQueued {
		t.Fatalf("admission: %+v", task)
	}
	replay, err := AdmitFusionTask(dir, task, dispatch, input, 0)
	if err != nil || replay.Revision != 1 {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	dispatch.ClientID, dispatch.Kind = "update", "update"
	input.ClientID, input.Content = "update", "new requirement"
	task, err = AdmitFusionTask(dir, task, dispatch, input, 1)
	if err != nil || task.Revision != 2 {
		t.Fatalf("update: %+v, %v", task, err)
	}
	if ok, err := RecordFusionReport(dir, task.ID, "first", 1, "old-report", FusionTaskAwaitingReview); err != nil || ok {
		t.Fatalf("old report admitted: %v, %v", ok, err)
	}
	if ok, err := RecordFusionReport(dir, task.ID, "update", 2, "report", FusionTaskAwaitingReview); err != nil || !ok {
		t.Fatalf("report: %v, %v", ok, err)
	}
	if err := AcceptFusionReport(dir, task.ID, "old-report", 1, "reviewed"); err == nil {
		t.Fatal("stale report accepted")
	}
	dispatch.ClientID, dispatch.Kind, input.ClientID, input.Content = "review-input", "review", "review-input", "Fix the reported edge case"
	task, err = AdmitFusionTask(dir, task, dispatch, input, 2)
	if err != nil || task.ReviewRounds != 1 {
		t.Fatalf("review correction: %+v, %v", task, err)
	}
	replay, err = AdmitFusionTask(dir, task, dispatch, input, 2)
	if err != nil || replay.ReviewRounds != 1 {
		t.Fatalf("review replay: %+v, %v", replay, err)
	}
	if ok, err := RecordFusionReport(dir, task.ID, "review-input", 3, "corrected-report", FusionTaskAwaitingReview); err != nil || !ok {
		t.Fatalf("corrected report: %v, %v", ok, err)
	}
	if err := AcceptFusionReport(dir, task.ID, "corrected-report", 3, "checked actual changes"); err != nil {
		t.Fatal(err)
	}
	if err := AcceptFusionReport(dir, task.ID, "corrected-report", 3, "checked actual changes"); err != nil {
		t.Fatalf("acceptance replay failed: %v", err)
	}
	task, found, err := ReadFusionTask(dir, task.ID)
	if err != nil || !found || task.State != FusionTaskCompleted {
		t.Fatalf("persisted review: %+v, %v", task, err)
	}
	dispatch.ClientID, input.ClientID = "late", "late"
	if _, err := AdmitFusionTask(dir, task, dispatch, input, 3); err == nil {
		t.Fatal("completed task reopened by update")
	}
	task.ID = "cancelled"
	dispatch.TaskID, dispatch.ClientID, dispatch.Kind = task.ID, "cancel-input", "delegate"
	input.ClientID = dispatch.ClientID
	task, err = AdmitFusionTask(dir, task, dispatch, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stopped, err := CancelFusionTask(dir, "lead", "task", 3, "late stop of old task"); err != nil || stopped {
		t.Fatalf("old task stopped current work: %v, %v", stopped, err)
	}
	current, _, err := ReadFusionTask(dir, task.ID)
	if err != nil || current.State != FusionTaskQueued {
		t.Fatalf("old stop changed new task: %+v, %v", current, err)
	}
	if err := CancelFusionTasks(dir, "lead", "user stopped"); err != nil {
		t.Fatal(err)
	}
	if ok, err := RecordFusionReport(dir, task.ID, dispatch.ClientID, 1, "late-result", FusionTaskAwaitingReview); err != nil || ok {
		t.Fatalf("cancelled report admitted: %v, %v", ok, err)
	}
	task, _, err = ReadFusionTask(dir, task.ID)
	if err != nil || task.State != FusionTaskCancelled || task.StopReason != "user stopped" {
		t.Fatalf("cancel persistence: %+v, %v", task, err)
	}
}
