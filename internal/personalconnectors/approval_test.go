package personalconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These cases protect the account-effect boundary, including an automatic
// decision revoked between review and dispatch and retries after a lost result.
func TestAutomaticApprovalRetainsExactEffectBoundary(t *testing.T) {
	for _, scenario := range []string{"allow", "ask", "deny", "unavailable", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			actor := Actor{"personal", "space", "session"}
			effects, reviews, guards := 0, 0, 0
			store := &memoryStore{values: map[string][]byte{}}
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				effects++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"sent","threadId":"thread"}`))}, nil
			})}
			b := newTestBroker(Options{Store: store, HTTPClient: client, Authorization: func(context.Context, Actor) (string, error) { return "task-v1", nil }})
			defer b.Close()
			b.SetReview(func(_ context.Context, a Actor, r Result) (ReviewDecision, error) {
				reviews++
				if a != actor || r.Digest == "" || r.AccountID != "me" || r.Operation != "gmail.send" {
					t.Fatal("review lost exact account action")
				}
				if scenario == "unavailable" {
					return ReviewDecision{}, errors.New("offline")
				}
				outcome := scenario
				if scenario == "revoked" {
					outcome = "allow"
				}
				return ReviewDecision{Outcome: outcome, Reason: "fixture decision", PolicyRevision: 1, RulesRevision: 1}, nil
			}, func(ctx context.Context, a Actor, authority string, r Result, d ReviewDecision, use func(context.Context) error) error {
				guards++
				if a != actor || authority != "task-v1" || r.Digest == "" || d.PolicyRevision != 1 {
					t.Fatal("dispatch lost review binding")
				}
				if scenario == "revoked" {
					return errors.New("policy changed")
				}
				return use(ctx)
			})
			if err := b.RegisterVerified(ctx, actor, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "test", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			req := Request{"g", "gmail.send", json.RawMessage(`{"to":["test@example.invalid"],"subject":"test","body":"test"}`), "once"}
			result, err := b.Call(ctx, actor, req)
			if err != nil {
				t.Fatal(err)
			}
			want := "awaiting_confirmation"
			if scenario == "allow" {
				want = "completed"
			}
			if scenario == "deny" {
				want = "denied"
			}
			if result.State != want {
				t.Fatalf("state %s, want %s", result.State, want)
			}
			if result.ReviewReason == "" {
				t.Fatal("missing saved review reason")
			}
			if reviews != 1 {
				t.Fatalf("reviews=%d", reviews)
			}
			if scenario == "allow" && (effects != 1 || guards != 1) {
				t.Fatalf("effects=%d guards=%d", effects, guards)
			}
			if scenario != "allow" && effects != 0 {
				t.Fatal("unapproved effect executed")
			}
			again, err := b.Call(ctx, actor, req)
			if err != nil || again.State != want || reviews != 1 {
				t.Fatalf("retry replayed review or effect: %#v %v, reviews %d", again, err, reviews)
			}
			if scenario == "ask" || scenario == "unavailable" || scenario == "revoked" {
				approved, err := b.Decide(ctx, actor, result.ActionID, result.Digest, true)
				if err != nil || approved.State != "completed" || effects != 1 || reviews != 1 {
					t.Fatalf("exact owner confirmation %#v %v effects=%d reviews=%d", approved, err, effects, reviews)
				}
			}
		})
	}
}
