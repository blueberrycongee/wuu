package personalconnectors

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	sync.Mutex
	values map[string][]byte
}

func (s *memoryStore) Get(c context.Context, n, k string) ([]byte, error) {
	s.Lock()
	defer s.Unlock()
	v, ok := s.values[n+k]
	if !ok {
		return nil, credentialstore.ErrNotFound
	}
	return append([]byte{}, v...), nil
}
func (s *memoryStore) Set(c context.Context, n, k string, v []byte) error {
	return s.Update(c, n, k, func([]byte) ([]byte, error) { return v, nil })
}
func (s *memoryStore) Delete(c context.Context, n, k string) error { return s.Set(c, n, k, nil) }
func (s *memoryStore) Update(c context.Context, n, k string, f func([]byte) ([]byte, error)) error {
	s.Lock()
	defer s.Unlock()
	v, e := f(append([]byte{}, s.values[n+k]...))
	if e == nil {
		if v == nil {
			delete(s.values, n+k)
		} else {
			s.values[n+k] = append([]byte{}, v...)
		}
	}
	return e
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestWriteConfirmationPersistenceAndUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	actor := Actor{"personal", "space", "session"}
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing host credential")
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"token":"must not leak"}`)), Header: http.Header{}}, nil
	})}
	b := New(Options{Authorization: func(context.Context, Actor) (string, error) { return "trusted-turn", nil }, Store: s, HTTPClient: client})
	defer b.Close()
	// Trusted composition supplies a grant already verified by its OAuth callback.
	if e := b.RegisterVerified(ctx, actor, Grant{Connection: Connection{ID: "gmail", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	req := Request{ConnectionID: "gmail", Operation: "gmail.send", RequestID: "request", Arguments: json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"test","body":"test"}`)}
	pending, e := b.Call(ctx, actor, req)
	if e != nil || pending.State != "awaiting_confirmation" || calls != 0 {
		t.Fatalf("pending %#v %v", pending, e)
	}
	if _, e = b.Decide(ctx, actor, pending.ActionID, "wrong", true); e == nil || calls != 0 {
		t.Fatal("digest bypass")
	}
	result, e := b.Decide(ctx, actor, pending.ActionID, pending.Digest, true)
	if e != nil || result.State != "uncertain" || calls != 1 {
		t.Fatalf("uncertain %#v %v", result, e)
	}
	b2 := New(Options{Authorization: func(context.Context, Actor) (string, error) { return "trusted-turn", nil }, Store: s, HTTPClient: client})
	defer b2.Close()
	again, e := b2.Call(ctx, actor, req)
	if e != nil || again.State != "uncertain" || calls != 1 {
		t.Fatal("replayed uncertain effect")
	}
	req.RequestID = "new-id"
	same, e := b2.Call(ctx, actor, req)
	if e != nil || same.State != "uncertain" || same.ActionID != again.ActionID || calls != 1 {
		t.Fatal("new request id bypassed unknown effect")
	}
	raw, _ := json.Marshal(again)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "must not leak") {
		t.Fatal("credential leak")
	}
	if _, e = b2.Call(ctx, Actor{"coding", "space", "session"}, req); e == nil {
		t.Fatal("coding crossed boundary")
	}
	if _, e = b2.Call(ctx, Actor{"personal", "other", "session"}, req); e == nil {
		t.Fatal("cross-space grant")
	}
}
func TestDisconnectInvalidatesPendingEffect(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	b := New(Options{Authorization: func(context.Context, Actor) (string, error) { return "trusted-turn", nil }, Store: s})
	defer b.Close()
	e := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	p, e := b.Call(ctx, a, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Disconnect(ctx, a, "g"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Decide(ctx, a, p.ActionID, p.Digest, true); e == nil {
		t.Fatal("revoked pending executed")
	}
}

func TestReadResponseCannotExposeCredentialOrForeignActions(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	b := New(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return "turn", nil }, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"secret","nested":{"note":"secret"},"threads":[]}`)), Header: http.Header{}}, nil
	})}})
	defer b.Close()
	e := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly", "https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	result, e := b.Call(ctx, a, Request{ConnectionID: "g", Operation: "gmail.search", Arguments: json.RawMessage(`{}`)})
	if e != nil || strings.Contains(string(result.Data), "secret") || strings.Contains(string(result.Data), "access_token") {
		t.Fatalf("response leak %s %v", result.Data, e)
	}
	_, e = b.Call(ctx, a, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"private","body":"private"}`), "id"})
	if e != nil {
		t.Fatal(e)
	}
	actions, e := b.Actions(ctx, Actor{"personal", "space", "other"})
	if e != nil || len(actions) != 0 {
		t.Fatalf("foreign actions %v %v", actions, e)
	}
}
func TestTaskRevisionChangeBlocksConfirmation(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	revision := "turn1"
	b := New(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return revision, nil }})
	defer b.Close()
	e := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	p, e := b.Call(ctx, a, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
	if e != nil {
		t.Fatal(e)
	}
	revision = "turn2"
	if _, e = b.Decide(ctx, a, p.ActionID, p.Digest, true); e == nil {
		t.Fatal("stale task mutation")
	}
}
func TestDisconnectCancelsInFlightRead(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	entered := make(chan struct{})
	b := New(Options{Store: s, HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}})
	defer b.Close()
	e := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := b.Call(ctx, a, Request{ConnectionID: "g", Operation: "gmail.search", Arguments: json.RawMessage(`{}`)})
		done <- e
	}()
	<-entered
	if e = b.Disconnect(ctx, a, "g"); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e == nil {
		t.Fatal("cancelled read delivered")
	}
}
