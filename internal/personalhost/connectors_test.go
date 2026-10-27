package personalhost

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"strings"
	"sync/atomic"
	"testing"
)

// Owner admission and execution identity are separate public host contracts.
// Model booleans must not substitute for either, including after disconnect.
func TestConnectorHostAuthorityBoundary(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	fixture := &connectorFixture{}
	host := New(binding, Options{Connectors: fixture})
	defer host.Close()
	for _, operation := range []string{"connectors.list", "connectors.actions", "connectors.decide", "connectors.disconnect"} {
		if _, err := host.Call(context.Background(), operation, json.RawMessage(`{}`), "model-session"); err == nil {
			t.Fatalf("forged owner admitted: %s", operation)
		}
	}
	if _, err := host.Call(context.Background(), "connectors.call", json.RawMessage(`{"connection_id":"x","operation":"gmail.search","arguments":{}}`), ""); err == nil {
		t.Fatal("unbound execution admitted")
	}
	if _, err := host.Call(context.Background(), "connectors.call", json.RawMessage(`{"connection_id":"x","operation":"gmail.search","arguments":{}}`), "verified-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(context.Background(), "connectors.poll", json.RawMessage(`{"source_id":"s","connection_id":"x","operation":"gmail.search","arguments":{}}`), ""); err == nil {
		t.Fatal("poll admitted without host session")
	}
	if fixture.actor.SessionID != "verified-session" || fixture.actor.UserSpaceID != "local" || fixture.actor.ProductDomain != "personal" {
		t.Fatalf("wrong host identity: %+v", fixture.actor)
	}
	live, cancel := context.WithCancel(context.Background())
	owner, err := clientauthority.AdmitDesktop(live, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Call(owner, "connectors.decide", json.RawMessage(`{"action_id":"a","digest":"d","approve":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fixture.actor.SessionID, "owner-ui:") || fixture.decisions != 1 {
		t.Fatal("decision did not bind live owner")
	}
	cancel()
	if _, err = host.Call(context.WithoutCancel(owner), "connectors.decide", json.RawMessage(`{"action_id":"a","digest":"d","approve":true}`), ""); err == nil {
		t.Fatal("revoked owner retained decision authority")
	}
	if fixture.decisions != 1 {
		t.Fatal("revoked decision reached broker")
	}
}
func TestUnconfiguredConnectorStatusDoesNotClaimConnected(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	h := New(binding, Options{})
	defer h.Close()
	value, err := h.Call(context.Background(), "connectors.read.list", json.RawMessage(`{}`), "verified")
	if err != nil || !strings.Contains(string(value), `"unavailable"`) {
		t.Fatalf("unconfigured status %s %v", value, err)
	}
	if _, err = h.Call(context.Background(), "connectors.call", json.RawMessage(`{"connection_id":"x","operation":"gmail.search","arguments":{}}`), "verified"); err == nil {
		t.Fatal("unconfigured call admitted")
	}
}

type connectorFixture struct {
	actor     personalconnectors.Actor
	decisions int
}

func (f *connectorFixture) Close() error { return nil }
func (f *connectorFixture) List(_ context.Context, a personalconnectors.Actor) ([]personalconnectors.Connection, error) {
	f.actor = a
	return []personalconnectors.Connection{}, nil
}
func (f *connectorFixture) Actions(_ context.Context, a personalconnectors.Actor) ([]personalconnectors.Result, error) {
	f.actor = a
	return nil, nil
}
func (f *connectorFixture) Call(_ context.Context, a personalconnectors.Actor, _ personalconnectors.Request) (personalconnectors.Result, error) {
	f.actor = a
	return personalconnectors.Result{State: "complete"}, nil
}
func (f *connectorFixture) Decide(_ context.Context, a personalconnectors.Actor, _, _ string, _ bool) (personalconnectors.Result, error) {
	f.actor = a
	f.decisions++
	return personalconnectors.Result{State: "complete"}, nil
}
func (f *connectorFixture) Disconnect(_ context.Context, a personalconnectors.Actor, _ string) error {
	f.actor = a
	return nil
}

func TestConnectorOAuthNeverReturnsProviderURLAndCancelsFailedOpener(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &connectorOAuthFixture{}
	h := New(binding, Options{Connectors: fixture, OpenConnectorAuthorizationURL: func(context.Context, string) error { return nil }})
	defer h.Close()
	result, err := h.Call(owner, "connectors.start", json.RawMessage(`{"provider":"gmail","connection_id":"g","domain":"personal","scopes":["read"],"resources":["me"]}`), "")
	if err != nil || strings.Contains(string(result), "authorization_url") || strings.Contains(string(result), "secret-state") {
		t.Fatalf("URL escaped host: %s %v", result, err)
	}
	h.options.OpenConnectorAuthorizationURL = func(context.Context, string) error {
		return &personalconnectors.Failure{Code: "synthetic_opener_failure"}
	}
	if _, err = h.Call(owner, "connectors.start", json.RawMessage(`{"provider":"gmail","connection_id":"g","domain":"personal","scopes":["read"],"resources":["me"]}`), ""); err == nil {
		t.Fatal("opener failure lost")
	}
	if fixture.cancelled.Load() == 0 {
		t.Fatal("failed opener retained live attempt")
	}
}

type connectorOAuthFixture struct {
	connectorFixture
	cancelled atomic.Int64
}

func (f *connectorOAuthFixture) Start(context.Context, personalconnectors.Actor, personalconnectors.ConnectRequest) (personalconnectors.AuthorizationAttempt, error) {
	return personalconnectors.AuthorizationAttempt{ID: "attempt", State: "pending", AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth?state=secret-state"}, nil
}
func (f *connectorOAuthFixture) Attempt(personalconnectors.Actor, string) (personalconnectors.AuthorizationAttempt, error) {
	return personalconnectors.AuthorizationAttempt{ID: "attempt", State: "pending"}, nil
}
func (f *connectorOAuthFixture) Cancel(personalconnectors.Actor, string) error {
	f.cancelled.Add(1)
	return nil
}
