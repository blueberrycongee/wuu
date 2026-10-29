package personalconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A selected credential provider may reject CAS and repeat its pure callback.
// Losing dispatch admission must discard every decision from the failed attempt.
type retryStore struct {
	*memoryStore
	retry func([]byte, []byte) ([]byte, bool)
}

func (s *retryStore) Update(ctx context.Context, n, k string, f func([]byte) ([]byte, error)) error {
	return s.memoryStore.Update(ctx, n, k, func(raw []byte) ([]byte, error) {
		next, err := f(raw)
		if err != nil {
			return nil, err
		}
		if s.retry != nil {
			if current, again := s.retry(raw, next); again {
				return f(current)
			}
		}
		return next, nil
	})
}
func TestCredentialCASRetryDoesNotReplayAdmittedWrite(t *testing.T) {
	ctx := context.Background()
	s := &retryStore{memoryStore: &memoryStore{values: map[string][]byte{}}}
	a := Actor{"personal", "space", "session"}
	calls := 0
	b := newTestBroker(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return "trusted", nil }, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic"}`))}, nil
	})}})
	defer b.Close()
	if err := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pending, err := b.Call(ctx, a, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
	if err != nil {
		t.Fatal(err)
	}
	retried := false
	s.retry = func(raw, next []byte) ([]byte, bool) {
		var before, after stored
		_ = json.Unmarshal(raw, &before)
		_ = json.Unmarshal(next, &after)
		r, ok := after.Actions[pending.ActionID]
		if retried || !ok || r.Result.State != "executing" {
			return nil, false
		}
		retried = true
		r.Result.State = "completed"
		r.Result.Data = json.RawMessage(`{"id":"other-writer"}`)
		before.Actions[pending.ActionID] = r
		current, _ := json.Marshal(before)
		return current, true
	}
	result, err := b.Decide(ctx, a, pending.ActionID, pending.Digest, true)
	if err != nil || !retried || result.State != "completed" || calls != 0 {
		t.Fatalf("CAS loser executed: %+v %v retried=%v calls=%d", result, err, retried, calls)
	}
	s.retry = func(raw, next []byte) ([]byte, bool) { return raw, true }
	actions, err := b.Actions(ctx, a)
	if err != nil || len(actions) != 1 {
		t.Fatalf("retry duplicated actions: %+v %v", actions, err)
	}
	connections, err := b.List(ctx, a)
	if err != nil || len(connections) != 1 {
		t.Fatalf("retry duplicated connections: %+v %v", connections, err)
	}
}

func TestCredentialRevocationWinsBeforeDispatchClaim(t *testing.T) {
	for _, mode := range []string{"disconnect", "revision", "expiry", "scope", "resource"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := &retryStore{memoryStore: &memoryStore{values: map[string][]byte{}}}
			actor := Actor{"personal", "space", "session"}
			calls := 0
			b := newTestBroker(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return "admitted", nil }, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })}})
			defer b.Close()
			grant := Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}
			if err := b.RegisterVerified(ctx, actor, grant); err != nil {
				t.Fatal(err)
			}
			pending, err := b.Call(ctx, actor, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
			if err != nil {
				t.Fatal(err)
			}
			retried := false
			s.retry = func(raw, next []byte) ([]byte, bool) {
				var before, after stored
				_ = json.Unmarshal(raw, &before)
				_ = json.Unmarshal(next, &after)
				if retried || after.Actions[pending.ActionID].Result.State != "executing" {
					return nil, false
				}
				retried = true
				g := before.Grants["g"]
				switch mode {
				case "disconnect":
					delete(before.Grants, "g")
				case "revision":
					g.Connection.Revision = "new"
					before.Grants["g"] = g
				case "expiry":
					g.ExpiresAt = time.Now().Add(-time.Minute)
					before.Grants["g"] = g
				case "scope":
					g.Connection.Scopes = nil
					before.Grants["g"] = g
				case "resource":
					g.Connection.Resources = nil
					before.Grants["g"] = g
				}
				current, _ := json.Marshal(before)
				return current, true
			}
			if _, err = b.Decide(ctx, actor, pending.ActionID, pending.Digest, true); err == nil || !retried || calls != 0 {
				t.Fatalf("revocation lost claim race: %v retried=%v calls=%d", err, retried, calls)
			}
		})
	}
}

func TestAdmittedConnectorAccountCannotSwitchAndRevocationDoesNotPromiseRollback(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	actor := Actor{"personal", "space", "session"}
	other := newTestBroker(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return "admitted", nil }})
	defer other.Close()
	calls := 0
	b := newTestBroker(Options{Store: s, Authorization: func(context.Context, Actor) (string, error) { return "admitted", nil }, HTTPClient: &http.Client{Transport: transport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "Bearer original" {
			t.Fatal("admitted account switched")
		}
		if err := other.Disconnect(ctx, actor, "g"); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"already-admitted"}`))}, nil
	})}})
	defer b.Close()
	if err := b.RegisterVerified(ctx, actor, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "original", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pending, err := b.Call(ctx, actor, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Decide(ctx, actor, pending.ActionID, pending.Digest, true)
	if err != nil || result.State != "uncertain" || calls != 1 {
		t.Fatalf("inflight revocation falsely promised rollback: %+v %v calls=%d", result, err, calls)
	}
	result, err = b.Decide(ctx, actor, pending.ActionID, pending.Digest, true)
	if err != nil || result.State != "uncertain" || calls != 1 {
		t.Fatalf("uncertain admitted request replayed: %+v %v", result, err)
	}
}
