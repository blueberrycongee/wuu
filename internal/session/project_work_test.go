package session

import (
	"errors"
	"testing"
)

// Work persistence must reject concurrent overwrites, preserve replay identity,
// and fence already-admitted input after corrections or stops, including reopen.
func TestProjectWorkRevisionAndReplay(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		_, err := store.CreateInitialized(Session{ID: "project"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateInitialized(Session{ID: "lead"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		w := ProjectWork{ID: "work", ProjectID: "project", LeadID: "lead", Revision: 1, Phase: "planning", Brief: "Preserve the API", LeadInput: &ProjectWorkInput{ClientID: "assignment", Revision: 1}}
		w, err = store.SaveProjectWork(w, 0, "create")
		if err != nil {
			t.Fatal(err)
		}
		msg := InboxMessage{ClientID: "assignment", Cause: "project", SessionID: "lead", Content: w.Brief, WorkID: w.ID, WorkRevision: w.Revision}
		if err := store.EnqueueInbox(msg); err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateInboxControls(msg.ClientID); err != nil {
			t.Fatal(err)
		}
		old := w
		w.Revision++
		w.Brief = "Also preserve the index"
		w.LeadInput = &ProjectWorkInput{ClientID: "corrected", Revision: w.Revision}
		w, err = store.SaveProjectWork(w, w.Version, "correct")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateInboxControls(msg.ClientID); !errors.Is(err, ErrControlChanged) {
			t.Fatalf("stale admitted input: %v", err)
		}
		if _, err := store.SaveProjectWork(old, old.Version, "racing-update"); !errors.Is(err, ErrProjectWorkChanged) {
			t.Fatalf("lost update accepted: %v", err)
		}
		replayed, err := store.SaveProjectWork(old, 0, "create")
		if err != nil || replayed.ID != w.ID || replayed.Revision != 1 {
			t.Fatalf("replay: %+v %v", replayed, err)
		}
		current, err := store.ReadProjectWork(w.ID)
		if err != nil || current.Revision != 2 || current.Brief != w.Brief {
			t.Fatalf("reopen: %+v %v", current, err)
		}
		msg.ClientID, msg.WorkRevision = "corrected", w.Revision
		if err := store.EnqueueInbox(msg); err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateInboxControls(msg.ClientID); err != nil {
			t.Fatal(err)
		}
		result := InboxMessage{ClientID: "accepted", SessionID: "project", Cause: "project_result", Content: "Accepted", WorkID: w.ID, WorkRevision: w.Revision, WorkVersion: w.Version}
		if err := store.EnqueueInbox(result); err != nil {
			t.Fatal(err)
		}
		w.LeadInput = &ProjectWorkInput{ClientID: "next-phase", Revision: w.Revision}
		w, err = store.SaveProjectWork(w, w.Version, "reassign-same-requirements")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{msg.ClientID, result.ClientID} {
			if err := store.ValidateInboxControls(id); !errors.Is(err, ErrControlChanged) {
				t.Fatalf("superseded phase/result %s remained admissible: %v", id, err)
			}
		}
		msg.ClientID = "next-phase"
		if err := store.EnqueueInbox(msg); err != nil {
			t.Fatal(err)
		}

		w.Phase = "stopped"
		if _, err := store.SaveProjectWork(w, w.Version, "stop"); err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateInboxControls(msg.ClientID); !errors.Is(err, ErrControlChanged) {
			t.Fatalf("stopped input: %v", err)
		}
	})
}
