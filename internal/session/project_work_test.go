package session

import (
	"errors"
	"testing"
)

// Work persistence must reject concurrent overwrites, preserve replay identity,
// and fence already-admitted input after corrections or stops, including reopen.
func TestProjectWorkRevisionAndReplay(t *testing.T) {
	dir := t.TempDir()
	_, err := CreateInitialized(dir, Session{ID: "project"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateInitialized(dir, Session{ID: "lead"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := ProjectWork{ID: "work", ProjectID: "project", LeadID: "lead", Revision: 1, Phase: "planning", Brief: "Preserve the API", LeadInput: &ProjectWorkInput{ClientID: "assignment", Revision: 1}}
	w, err = SaveProjectWork(dir, w, 0, "create")
	if err != nil {
		t.Fatal(err)
	}
	msg := InboxMessage{ClientID: "assignment", Cause: "project", SessionID: "lead", Content: w.Brief, WorkID: w.ID, WorkRevision: w.Revision}
	if err := EnqueueInbox(dir, msg); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInboxControls(dir, msg.ClientID); err != nil {
		t.Fatal(err)
	}
	old := w
	w.Revision++
	w.Brief = "Also preserve the index"
	w.LeadInput = &ProjectWorkInput{ClientID: "corrected", Revision: w.Revision}
	w, err = SaveProjectWork(dir, w, w.Version, "correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateInboxControls(dir, msg.ClientID); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale admitted input: %v", err)
	}
	if _, err := SaveProjectWork(dir, old, old.Version, "racing-update"); !errors.Is(err, ErrProjectWorkChanged) {
		t.Fatalf("lost update accepted: %v", err)
	}
	replayed, err := SaveProjectWork(dir, old, 0, "create")
	if err != nil || replayed.ID != w.ID || replayed.Revision != 1 {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	current, err := ReadProjectWork(dir, w.ID)
	if err != nil || current.Revision != 2 || current.Brief != w.Brief {
		t.Fatalf("reopen: %+v %v", current, err)
	}
	msg.ClientID, msg.WorkRevision = "corrected", w.Revision
	if err := EnqueueInbox(dir, msg); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInboxControls(dir, msg.ClientID); err != nil {
		t.Fatal(err)
	}
	result := InboxMessage{ClientID: "accepted", SessionID: "project", Cause: "project_result", Content: "Accepted", WorkID: w.ID, WorkRevision: w.Revision, WorkVersion: w.Version}
	if err := EnqueueInbox(dir, result); err != nil {
		t.Fatal(err)
	}
	w.LeadInput = &ProjectWorkInput{ClientID: "next-phase", Revision: w.Revision}
	w, err = SaveProjectWork(dir, w, w.Version, "reassign-same-requirements")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{msg.ClientID, result.ClientID} {
		if err := ValidateInboxControls(dir, id); !errors.Is(err, ErrControlChanged) {
			t.Fatalf("superseded phase/result %s remained admissible: %v", id, err)
		}
	}
	msg.ClientID = "next-phase"
	if err := EnqueueInbox(dir, msg); err != nil {
		t.Fatal(err)
	}

	w.Phase = "stopped"
	if _, err := SaveProjectWork(dir, w, w.Version, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInboxControls(dir, msg.ClientID); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stopped input: %v", err)
	}
}
