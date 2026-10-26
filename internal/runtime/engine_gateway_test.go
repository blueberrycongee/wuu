package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func TestEngineGatewayRejectsForeignProviderAndRetiredHandle(t *testing.T) {
	table := newEngineGatewayTable()
	calls := 0
	gateway := &engineSessionGateway{owner: "selected.engine", binding: agentengine.ThreadBinding{PersistRef: func(string) error { calls++; return nil }}}
	id, remove := table.register(gateway)
	raw, _ := json.Marshal(pluginprotocol.EngineGatewayParams{GatewayID: id, Reference: "native-ref"})
	invoke := func(caller string) error {
		_, err := table.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: caller, Method: "persist_reference", Params: raw})
		return err
	}
	if err := invoke("other.engine"); err == nil || calls != 0 {
		t.Fatalf("foreign caller accepted: calls=%d err=%v", calls, err)
	}
	if err := invoke("selected.engine"); err != nil || calls != 1 {
		t.Fatalf("selected caller failed: calls=%d err=%v", calls, err)
	}
	remove()
	if err := invoke("selected.engine"); err == nil || calls != 1 {
		t.Fatalf("retired handle accepted: calls=%d err=%v", calls, err)
	}
}

// Cancellation may stop new work, but an authenticated terminal receipt must
// still clear a durable pending marker without admitting another turn.
func TestEngineGatewaySettlementCannotAdmitPendingTurn(t *testing.T) {
	table := newEngineGatewayTable()
	calls := 0
	id, remove := table.register(&engineSessionGateway{owner: "engine", binding: agentengine.ThreadBinding{PersistTurnRef: func(ref string, pending bool) error {
		calls++
		if pending {
			t.Fatal("settlement admitted pending work")
		}
		return nil
	}}})
	invoke := func(caller string, pending bool) error {
		raw, _ := json.Marshal(pluginprotocol.EngineGatewayParams{GatewayID: id, Reference: "confirmed-turn", Pending: pending})
		_, err := table.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: caller, Method: "settle_turn", Params: raw})
		return err
	}
	if err := invoke("engine", true); err == nil {
		t.Fatal("pending settlement accepted")
	}
	if err := invoke("other", false); err == nil {
		t.Fatal("foreign settlement accepted")
	}
	if err := invoke("engine", false); err != nil || calls != 1 {
		t.Fatalf("terminal receipt lost: calls=%d err=%v", calls, err)
	}
	remove()
	if err := invoke("engine", false); err == nil {
		t.Fatal("retired settlement accepted")
	}
}

func TestNativeLoopEngineCloseDrainsActiveRun(t *testing.T) {
	done := make(chan struct{})
	canceled := make(chan struct{})
	session := &nativeWuuEngineSession{done: done, cancel: func() {
		select {
		case <-canceled:
		default:
			close(canceled)
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Close(ctx); err == nil {
		t.Fatal("active native loop closed without draining")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("close did not cancel active loop")
	}
	close(done)
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.RunTurn(context.Background(), agentengine.TurnInput{}, nil); err == nil {
		t.Fatal("closed native loop admitted work")
	}
}
