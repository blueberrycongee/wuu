package loopdriver_test

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// Embedded bindings must obey the same identity fence as managed providers.
func TestAcquireLocalBinding(t *testing.T) {
	ctx := context.Background()
	acquire := loopdriver.AcquireLocal(agent.DefaultDriver{})
	driver, binding, release, err := acquire(ctx, nil)
	if err != nil || driver == nil || release == nil {
		t.Fatalf("acquire explicit driver: driver=%v release=%v err=%v", driver, release != nil, err)
	}
	release()
	_, resumed, release, err := acquire(ctx, &binding)
	if err != nil || resumed != binding {
		t.Fatalf("resume binding: %+v, %v", resumed, err)
	}
	release()
	for _, field := range []string{"profile", "provider", "contract", "driver", "version"} {
		t.Run(field, func(t *testing.T) {
			incompatible := binding
			switch field {
			case "profile":
				incompatible.Profile = "other"
			case "provider":
				incompatible.ProviderID = "other"
			case "contract":
				incompatible.ContractVersion++
			case "driver":
				incompatible.DriverID = "other"
			case "version":
				incompatible.DriverVersion = "other"
			}
			if _, _, _, err := acquire(ctx, &incompatible); err == nil {
				t.Fatal("accepted an incompatible binding")
			}
		})
	}
	if _, _, _, err := loopdriver.AcquireLocal(nil)(ctx, nil); err == nil {
		t.Fatal("missing driver acquired an implicit default")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := acquire(canceled, nil); err == nil {
		t.Fatal("canceled acquisition succeeded")
	}
}
