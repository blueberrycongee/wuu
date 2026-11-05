package personalhost

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/userspace"
)

// A verified session or live owner admission never replaces selected resource
// authorization. Missing policy, revoked policy and unknown operations must not
// reach the broker, including reads.
func TestHostRequiresResourceAuthorityBeforeBroker(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	for _, mode := range []string{"missing", "denied", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			options := Options{}
			if mode != "missing" {
				options.Authorize = func(context.Context, string, string) error {
					calls++
					return errors.New("synthetic permission revoked")
				}
			}
			host := New(binding, options)
			defer host.Close()
			operation := "calendar.read.status"
			if mode == "unknown" {
				operation = "calendar.read.unknown"
			}
			if _, err := host.Call(context.Background(), operation, nil, "verified"); err == nil {
				t.Fatal("missing authority accepted")
			}
			if mode == "unknown" && calls != 0 {
				t.Fatal("unknown operation delegated to permissive policy")
			}
		})
	}
}

func authorizeFixture(context.Context, string, string) error { return nil }
