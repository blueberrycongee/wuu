package personalconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// These fixtures use synchronous synthetic authority. Production must provide
// a selected boundary that fences revocation for the complete callback.
func newTestBroker(options Options) *Broker {
	if options.Authorization != nil && options.WithAuthorization == nil {
		options.WithAuthorization = func(ctx context.Context, a Actor, expected string, use func(context.Context) error) error {
			current, err := options.Authorization(ctx, a)
			if err != nil || current == "" || current != expected {
				return fault("authorization_context_changed")
			}
			return use(ctx)
		}
	}
	return New(options)
}

// A read-only authority digest is not a lease. Missing, rejected and cancelled
// dispatch boundaries must leave the action unclaimed and make zero requests.
func TestApprovalRequiresCheckThroughEffectBoundary(t *testing.T) {
	for _, mode := range []string{"missing", "revoked", "cancelled", "no-callback"} {
		t.Run(mode, func(t *testing.T) {
			a := Actor{"personal", "space", "session"}
			ctx := context.Background()
			calls := 0
			options := Options{Store: &memoryStore{values: map[string][]byte{}}, Authorization: func(context.Context, Actor) (string, error) { return "authority", nil }, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not reach HTTP") })}}
			if mode != "missing" {
				options.WithAuthorization = func(ctx context.Context, actor Actor, expected string, use func(context.Context) error) error {
					if mode == "no-callback" {
						return nil
					}
					if actor != a || expected != "authority" {
						t.Fatal("wrong action authority binding")
					}
					if mode == "revoked" {
						return fault("authorization_context_changed")
					}
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					return use(cancelled)
				}
			}
			b := New(options)
			defer b.Close()
			if err := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			pending, err := b.Call(ctx, a, Request{"g", "gmail.send", json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"t","body":"t"}`), "id"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.Decide(ctx, a, pending.ActionID, pending.Digest, true); err == nil {
				t.Fatal("unsupported authority reached dispatch")
			}
			actions, err := b.Actions(ctx, a)
			if err != nil || len(actions) != 1 || actions[0].State != "awaiting_confirmation" || calls != 0 {
				t.Fatalf("action was claimed: %+v %v calls=%d", actions, err, calls)
			}
			rejected, err := b.Decide(ctx, a, pending.ActionID, pending.Digest, false)
			if err != nil || rejected.State != "rejected" {
				t.Fatalf("safe rejection blocked: %+v %v", rejected, err)
			}
		})
	}
}
