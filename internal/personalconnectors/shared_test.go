package personalconnectors

import (
	"context"
	"encoding/json"
	"testing"
)

type sharedFixture struct {
	connection Connection
	calls      int
	uncertain  bool
}

func (f *sharedFixture) List(context.Context, Actor) ([]Connection, error) {
	return []Connection{f.connection}, nil
}
func (f *sharedFixture) Call(_ context.Context, _ Actor, expected Connection, _ string, _ json.RawMessage) (json.RawMessage, bool, error) {
	if expected.Revision != f.connection.Revision {
		return nil, false, fault("connection_changed")
	}
	f.calls++
	if f.uncertain {
		return nil, true, fault("provider_unavailable")
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"accepted"}]}`), false, nil
}

// One receipt contract covers legacy HTTP and MCP effects. Transport replacement
// must not turn a pending or uncertain action into an automatic external retry.
func TestSharedConnectorConfirmationRecoveryAndPolling(t *testing.T) {
	ctx := context.Background()
	a := Actor{"personal", "local", "task"}
	f := &sharedFixture{connection: Connection{ID: "mcp:plugin.fixture.docs", Provider: "mcp", AccountID: "account", Domain: "work", WorkReadPolicy: "selected_resources", Revision: "grant-1", State: "connected", Resources: []string{"https://fixture.invalid/mcp"}, Tools: []Operation{{Name: "read", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "write", InputSchema: json.RawMessage(`{"type":"object"}`)}}}}
	store := &memoryStore{values: map[string][]byte{}}
	newBroker := func() *Broker {
		b := newTestBroker(Options{Store: store, Authorization: func(context.Context, Actor) (string, error) { return "task-authority", nil }})
		b.SetSharedConnections(f)
		t.Cleanup(func() { _ = b.Close() })
		return b
	}
	b := newBroker()
	request := Request{ConnectionID: f.connection.ID, Operation: "write", Arguments: json.RawMessage(`{"text":"once"}`), RequestID: "intent"}
	pending, err := b.Call(ctx, a, request)
	if err != nil || pending.State != "awaiting_confirmation" || f.calls != 0 {
		t.Fatalf("prepare = %+v %v, calls %d", pending, err, f.calls)
	}
	if _, err = b.Decide(ctx, a, pending.ActionID, "forged", true); err == nil {
		t.Fatal("forged digest accepted")
	}
	b = newBroker()
	f.uncertain = true
	result, err := b.Decide(ctx, a, pending.ActionID, pending.Digest, true)
	if err != nil || result.State != "uncertain" || f.calls != 1 {
		t.Fatalf("dispatch = %+v %v, calls %d", result, err, f.calls)
	}
	b = newBroker()
	request.RequestID = "new-intent"
	result, err = b.Call(ctx, a, request)
	if err != nil || result.State != "uncertain" || f.calls != 1 {
		t.Fatalf("replay = %+v %v, calls %d", result, err, f.calls)
	}
	f.uncertain = false
	q := PollRequest{SourceID: "query", ConnectionID: f.connection.ID, Operation: "read", Arguments: json.RawMessage(`{}`)}
	baseline, err := b.Poll(ctx, a, q)
	if err != nil || baseline.State != "baseline" {
		t.Fatalf("baseline = %+v %v", baseline, err)
	}
	unchanged, err := newBroker().Poll(ctx, a, q)
	if err != nil || unchanged.State != "unchanged" {
		t.Fatalf("poll restart = %+v %v", unchanged, err)
	}
	q.Operation = "write"
	if _, err = b.Poll(ctx, a, q); err == nil {
		t.Fatal("poll admitted write")
	}
	request.RequestID, request.Arguments = "changed-grant", json.RawMessage(`{"text":"different"}`)
	pending, err = b.Call(ctx, a, request)
	if err != nil {
		t.Fatal(err)
	}
	f.connection.Revision = "grant-2"
	if _, err = b.Decide(ctx, a, pending.ActionID, pending.Digest, true); err == nil {
		t.Fatal("changed grant admitted old confirmation")
	}
}
