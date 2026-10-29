package personalcalendar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

// Models a secure backend whose commit/Get ignores context cancellation.
type controlledStore struct {
	credentialstore.Store
	beforeCommit func([]byte) error
	afterCommit  func()
	afterGet     func()
}

func (s *controlledStore) Get(ctx context.Context, n, k string) ([]byte, error) {
	v, e := s.Store.Get(ctx, n, k)
	if s.afterGet != nil {
		s.afterGet()
	}
	return v, e
}
func (s *controlledStore) Update(ctx context.Context, n, k string, f func([]byte) ([]byte, error)) error {
	e := s.Store.Update(ctx, n, k, func(raw []byte) ([]byte, error) {
		next, e := f(raw)
		if e == nil && s.beforeCommit != nil {
			e = s.beforeCommit(next)
		}
		return next, e
	})
	if s.afterCommit != nil {
		s.afterCommit()
	}
	return e
}

func TestCancelAndCloseDuringCredentialCommit(t *testing.T) {
	for _, operation := range []string{"cancel", "close"} {
		t.Run(operation, func(t *testing.T) {
			b, s := fixture(t)
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			b.store = &controlledStore{Store: s, beforeCommit: func(next []byte) error {
				if len(next) > 0 {
					once.Do(func() { close(started); <-release })
				}
				return nil
			}}
			a, e := b.Start(context.Background(), actor)
			if e != nil {
				t.Fatal(e)
			}
			u, _ := url.Parse(a.AuthorizationURL)
			callbackDone := make(chan struct{})
			go func() {
				defer close(callbackDone)
				b.callback(a.ID, httptest.NewRecorder(), httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=fixture", nil))
			}()
			<-started
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				if operation == "cancel" {
					_ = b.Cancel(actor, a.ID)
				} else {
					b.Close()
				}
			}()
			b.mu.Lock()
			p := b.attempts[a.ID]
			b.mu.Unlock()
			<-p.ctx.Done()
			close(release)
			<-stopped
			<-callbackDone
			view, _ := b.Attempt(actor, a.ID)
			if view.State == "connected" {
				t.Error("cancelled commit became connected")
			}
			raw, e := s.Get(context.Background(), namespace, key(actor.UserSpaceID))
			if !errors.Is(e, credentialstore.ErrNotFound) || len(raw) != 0 {
				t.Error("cancelled commit retained new credentials")
			}
		})
	}
}

func TestCloseDuringFinalCredentialGet(t *testing.T) {
	for _, operation := range []string{"events", "status"} {
		t.Run(operation, func(t *testing.T) {
			b, s := fixture(t)
			c := connect(t, b)
			q := EventQuery{CalendarID: "fixture@example.invalid", TimeMin: "2026-11-01T00:00:00Z", TimeMax: "2026-11-02T00:00:00Z", TimeZone: "UTC"}
			if e := b.Select(context.Background(), actor, c.ID, []string{q.CalendarID}); e != nil {
				t.Fatal(e)
			}
			started, release := make(chan struct{}), make(chan struct{})
			gets := 0
			b.store = &controlledStore{Store: s, afterGet: func() {
				gets++
				if operation == "status" || gets == 2 {
					close(started)
					<-release
				}
			}}
			done := make(chan error, 1)
			go func() {
				if operation == "events" {
					_, e := b.EventsPage(context.Background(), actor, c.ID, q)
					done <- e
				} else {
					_, e := b.Status(context.Background(), actor)
					done <- e
				}
			}()
			<-started
			b.Close()
			close(release)
			if e := <-done; e == nil {
				t.Error("closed broker returned late data")
			}
		})
	}
}

func TestRevocationFencesWhenPersistenceFails(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	q := EventQuery{CalendarID: "fixture@example.invalid", TimeMin: "2026-11-01T00:00:00Z", TimeMax: "2026-11-02T00:00:00Z", TimeZone: "UTC"}
	if e := b.Select(context.Background(), actor, c.ID, []string{q.CalendarID}); e != nil {
		t.Fatal(e)
	}
	b.store = &controlledStore{Store: s, beforeCommit: func([]byte) error { return errors.New("synthetic write failure") }}
	calls := 0
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return response(401, `{}`), nil })
	if _, e := b.EventsPage(context.Background(), actor, c.ID, q); e == nil || !strings.Contains(e.Error(), "authorization_denied") {
		t.Fatal(e)
	}
	status, e := b.Status(context.Background(), actor)
	if e != nil || status.State != "needs_reauth" {
		t.Error("known revocation not fenced after failed persistence", status.State, e)
	}
	_, _ = b.EventsPage(context.Background(), actor, c.ID, q)
	if calls != 1 {
		t.Error("known revoked grant reused", calls)
	}
	// Another broker/process has no in-memory deny map, so cached-read authority
	// must be revalidated with Google, not inferred from the old stored record.
	second := newBroker(b.config, b.store, b.client, b.activationDir)
	defer second.Close()
	if _, e = second.Authorize(context.Background(), actor); e == nil {
		t.Error("new process trusted revoked stored grant")
	}
}

func TestCancelledCommitCleanupPreservesNewerGeneration(t *testing.T) {
	b, s := fixture(t)
	committed, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b.store = &controlledStore{Store: s, afterCommit: func() { once.Do(func() { close(committed); <-release }) }}
	a, e := b.Start(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(a.AuthorizationURL)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.callback(a.ID, httptest.NewRecorder(), httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=fixture", nil))
	}()
	<-committed
	raw, _ := s.Get(context.Background(), namespace, key(actor.UserSpaceID))
	newer, _ := b.decode(raw, actor)
	newer.Connection.ID = "new-legitimate-connection"
	newer.Revision = "new-legitimate-revision"
	if e = b.writeActivation(context.Background(), newer); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(newer)
	if e = s.Set(context.Background(), namespace, key(actor.UserSpaceID), raw); e != nil {
		t.Fatal(e)
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- b.Cancel(actor, a.ID) }()
	b.mu.Lock()
	attempt := b.attempts[a.ID]
	b.mu.Unlock()
	<-attempt.ctx.Done()
	close(release)
	if e = <-cancelled; e != nil {
		t.Fatal(e)
	}
	<-done
	current, e := b.Status(context.Background(), actor)
	if e != nil || current.ID != newer.Connection.ID || current.State != "connected" {
		t.Fatal("cleanup deleted newer connection", e)
	}
}

func TestCancelledCommitCleanupFailureIsExplicit(t *testing.T) {
	for _, operation := range []string{"cancel", "close"} {
		t.Run(operation, func(t *testing.T) {
			b, s := fixture(t)
			s.failDelete = true
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			b.store = &controlledStore{Store: s, beforeCommit: func(next []byte) error {
				if len(next) > 0 {
					once.Do(func() { close(started); <-release })
				}
				return nil
			}}
			a, _ := b.Start(context.Background(), actor)
			u, _ := url.Parse(a.AuthorizationURL)
			done := make(chan struct{})
			go func() {
				defer close(done)
				b.callback(a.ID, httptest.NewRecorder(), httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=fixture", nil))
			}()
			<-started
			cancelled := make(chan error, 1)
			go func() {
				if operation == "cancel" {
					cancelled <- b.Cancel(actor, a.ID)
				} else {
					cancelled <- b.Close()
				}
			}()
			b.mu.Lock()
			attempt := b.attempts[a.ID]
			b.mu.Unlock()
			<-attempt.ctx.Done()
			close(release)
			if e := <-cancelled; e == nil || !strings.Contains(e.Error(), "credential_cleanup_failed") {
				t.Fatal("cleanup failure hidden", e)
			}
			<-done
			view, _ := b.Attempt(actor, a.ID)
			if view.State == "connected" || view.ErrorCode != "credential_cleanup_failed" {
				t.Fatal(view)
			}
			status, _ := b.Status(context.Background(), actor)
			if status.State == "connected" {
				t.Fatal("failed cleanup left usable grant")
			}
			// The grant remains valid at Google. Restart must still honor the cancelled
			// local attempt even after its in-memory denied map has disappeared.
			calls := 0
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { calls++; return response(200, `{"items":[]}`), nil })}
			restarted := newBroker(b.config, s, client, b.activationDir)
			defer restarted.Close()
			status, e := restarted.Authorize(context.Background(), actor)
			if e == nil && status.State == "connected" {
				t.Fatal("restart silently activated cancelled grant after failed cleanup")
			}
			if calls != 0 {
				t.Fatal("incomplete consent contacted Google after restart", calls)
			}
		})
	}
}

func TestRefreshDenialFencesWhenPersistenceFails(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	b.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	b.store = &controlledStore{Store: s, beforeCommit: func(next []byte) error {
		var record stored
		_ = json.Unmarshal(next, &record)
		if record.Connection.State == "needs_reauth" {
			return errors.New("synthetic failed revocation write")
		}
		return nil
	}}
	calls := 0
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return response(400, `{"error":"invalid_grant"}`), nil
	})
	_, e := b.Calendars(context.Background(), actor, c.ID)
	var failure *Failure
	if !errors.As(e, &failure) || failure.Code != "authorization_expired" || !failure.PersistenceFailed {
		t.Fatal("lost typed authentication failure", e)
	}
	_, _ = b.Calendars(context.Background(), actor, c.ID)
	if calls != 1 {
		t.Fatal("retried rejected refresh grant", calls)
	}
}
