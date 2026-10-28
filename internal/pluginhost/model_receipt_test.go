package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
)

func TestModelReceiptAuthorityAndSettlementLifetime(t *testing.T) {
	first, second := New(), New()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	id, release := first.RegisterModelReceipt(ctx, "adapter", func(_ string, _ json.RawMessage) (json.RawMessage, error) { calls++; return json.RawMessage(`{}`), nil })
	if _, err := first.InvokeModelReceipt("other", id, "create", nil); err == nil {
		t.Fatal("foreign owner admitted")
	}
	if _, err := second.InvokeModelReceipt("adapter", id, "create", nil); err == nil {
		t.Fatal("same owner in another generation admitted")
	}
	cancel()
	if _, err := first.InvokeModelReceipt("adapter", id, "create", nil); err == nil {
		t.Fatal("canceled submission admitted")
	}
	if _, err := first.InvokeModelReceipt("adapter", id, "settle", nil); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err := first.InvokeModelReceipt("adapter", id, "settle", nil); err == nil {
		t.Fatal("released receipt admitted")
	}
	if calls != 1 {
		t.Fatalf("callback count %d", calls)
	}
}
