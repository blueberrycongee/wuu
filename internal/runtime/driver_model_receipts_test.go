package runtime

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"testing"
)

type receiptBoundaryGateway struct {
	loopdriver.KernelGateway
	loopdriver.TurnGateway
	reads int
}

func (g *receiptBoundaryGateway) ReadModelReceipts(context.Context, string) (pluginprotocol.DriverModelReceipts, error) {
	g.reads++
	return pluginprotocol.DriverModelReceipts{Status: "unavailable", Receipts: []pluginprotocol.DriverModelUsageReceipt{}}, nil
}
func TestDriverModelReceiptAuthorization(t *testing.T) {
	table := newDriverGatewayTable()
	gateway := &receiptBoundaryGateway{}
	revoke, err := table.register("execution", gateway, "owner")
	if err != nil {
		t.Fatal(err)
	}
	invoker := &driverTurnInvoker{parent: &kernelHostServices{driverGateways: table}}
	call := func(caller, execution string) error {
		raw, _ := json.Marshal(map[string]string{"execution_id": execution, "request_id": "boundary"})
		_, err := invoker.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: caller, Method: "model_receipts", Params: raw})
		return err
	}
	if err := call("forged", "execution"); err == nil {
		t.Fatal("forged driver read admitted")
	}
	if err := call("owner", "other-session-execution"); err == nil {
		t.Fatal("foreign execution read admitted")
	}
	if gateway.reads != 0 {
		t.Fatal("unauthorized read reached receipt store")
	}
	if err := call("owner", "execution"); err != nil {
		t.Fatal(err)
	}
	revoke()
	if err := call("owner", "execution"); err == nil {
		t.Fatal("revoked execution read admitted")
	}
	if gateway.reads != 1 {
		t.Fatalf("unexpected read count %d", gateway.reads)
	}
}

// Real session construction exposes recovery reads, but the default driver's
// uncorrelated calls must not create cumulative prompt snapshots.
func TestDefaultSessionModelReceiptOptIn(t *testing.T) {
	adapter := &stepSelectionAdapter{selectNext: func() error { return nil }}
	rt := newStepSelectionRuntime(t, adapter)
	if _, err := rt.Sessions.Create("default-receipts"); err != nil {
		t.Fatal(err)
	}
	thread, err := rt.NewThreadRuntime("default-receipts")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(thread.PluginGeneration)
	if thread.StreamRunner.ModelInputReceiptStore == nil {
		t.Fatal("normal session has no selected correlation store")
	}
	result, err := thread.StreamRunner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "finish without durable model correlation"}}, nil)
	if err != nil || result.Content != "finished" {
		t.Fatalf("default execution: %+v %v", result, err)
	}
	receipts, err := rt.Sessions.LoadModelInputReceipts("default-receipts")
	if err != nil || len(receipts) != 0 {
		t.Fatalf("default loop persisted prompt snapshots: %+v %v", receipts, err)
	}
}
