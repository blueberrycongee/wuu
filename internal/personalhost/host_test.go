package personalhost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestUnavailableHostRejectsForgedAuthorityAndNeverClaimsProtection(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	host := New(binding, Options{Authorize: authorizeFixture})
	defer host.Close()
	for _, operation := range []string{"review.list", "review.resolve", "review.revoke"} {
		if _, err := host.Call(context.Background(), operation, json.RawMessage(`{"owner":true}`), ""); err == nil {
			t.Fatalf("forged owner admitted for %s", operation)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := host.Call(owner, "review.list", json.RawMessage(`{}`), "")
	if err != nil || !strings.Contains(string(value), `"unavailable"`) {
		t.Fatalf("not unavailable: %s %v", value, err)
	}
	for _, operation := range []string{"review.propose", "review.assess", "review.execute", "review.recover", "review.resolve"} {
		if _, err := host.Call(owner, operation, json.RawMessage(`{}`), ""); err == nil {
			t.Fatalf("unconfigured operation exposed: %s", operation)
		}
	}
	cancel()
	if _, err := host.Call(context.WithoutCancel(owner), "owner.check", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("closed owner transport retained control")
	}
	if _, err := host.Call(context.Background(), "review.execution.status", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("read without host-resolved session admitted")
	}
}
