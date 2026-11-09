package access

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/principal"
	"testing"
)

func TestDecisionBoundaryFailsClosed(t *testing.T) {
	r := Request{Principal: principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "a"}, Action: "thread/resume", Resources: []Resource{{Kind: "session", ID: "s"}}}
	if err := Check(context.Background(), OwnerOnly{}, r); err == nil {
		t.Fatal("non-owner allowed")
	}
	r.Owner = true
	if err := Check(context.Background(), OwnerOnly{}, r); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), PolicyFunc(func(context.Context, Request) (Decision, error) { return Decision{Allow: true}, errors.New("offline") }), r); err == nil {
		t.Fatal("policy failure allowed")
	}
	r.Resources = nil
	if err := Check(context.Background(), OwnerOnly{}, r); err == nil {
		t.Fatal("empty resources allowed")
	}
}
