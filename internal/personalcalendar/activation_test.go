package personalcalendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func TestCompletedConsentSurvivesRestartAndIncompleteConsentDoesNot(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	restarted := newBroker(b.config, s, b.client, b.activationDir)
	defer restarted.Close()
	if status, e := restarted.Authorize(context.Background(), actor); e != nil || status.State != "connected" {
		t.Fatal("completed consent did not survive restart", e)
	}
	r, e := b.load(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	path := b.activationPath(r)
	for _, failure := range []string{"missing", "corrupt", "unsafe-permissions", "symlink"} {
		t.Run(failure, func(t *testing.T) {
			_ = os.Remove(path)
			switch failure {
			case "corrupt":
				e = os.WriteFile(path, []byte("not-completed"), 0600)
			case "unsafe-permissions":
				e = os.WriteFile(path, []byte(activationProof), 0644)
			case "symlink":
				other := filepath.Join(t.TempDir(), "fake-complete")
				if e = os.WriteFile(other, []byte(activationProof), 0600); e == nil {
					e = os.Symlink(other, path)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { calls++; return response(200, `{"items":[]}`), nil })}
			other := newBroker(b.config, s, client, b.activationDir)
			defer other.Close()
			status, err := other.Authorize(context.Background(), actor)
			if err == nil && status.State == "connected" {
				t.Fatal("missing/invalid completion proof enabled credentials")
			}
			_, _ = other.Calendars(context.Background(), actor, c.ID)
			if calls != 0 {
				t.Fatal("uncompleted consent contacted Google", calls)
			}
		})
	}
}

func TestActivationWriteFailureNeverEnablesStoredGrant(t *testing.T) {
	b, s := fixture(t)
	s.failDelete = true
	// A file in place of the proof directory deterministically rejects writes.
	b.activationDir = filepath.Join(t.TempDir(), "not-a-directory")
	if e := os.WriteFile(b.activationDir, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := b.Start(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(a.AuthorizationURL)
	w := httptest.NewRecorder()
	b.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=fixture", nil))
	if w.Code == 200 {
		t.Fatal("proof failure reported connected")
	}
	raw, e := s.Get(context.Background(), namespace, key(actor.UserSpaceID))
	if e != nil || len(raw) == 0 {
		t.Fatal("fixture did not preserve undeletable grant", e)
	}
	other := newBroker(b.config, s, b.client, b.activationDir)
	defer other.Close()
	if c, e := other.Authorize(context.Background(), actor); e == nil && c.State == "connected" {
		t.Fatal("proof-write failure activated after restart")
	}
	if e := b.Close(); e == nil {
		t.Fatal("failed credential deletion was hidden")
	}
}

func TestCancellationAfterCompletedConsentIsExplicit(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	var id string
	b.mu.Lock()
	for key := range b.attempts {
		id = key
	}
	b.mu.Unlock()
	if e := b.Cancel(actor, id); e == nil {
		t.Fatal("cancel reported success for already completed consent")
	}
	other := newBroker(b.config, s, b.client, b.activationDir)
	defer other.Close()
	if status, e := other.Authorize(context.Background(), actor); e != nil || status.ID != c.ID || status.State != "connected" {
		t.Fatal("completed consent was implicitly disconnected", e)
	}
	if _, e := b.Disconnect(context.Background(), actor, c.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get(context.Background(), namespace, key(actor.UserSpaceID)); !errors.Is(e, credentialstore.ErrNotFound) {
		t.Fatal(e)
	}
}
