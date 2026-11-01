package personalim

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type fixtureClient struct {
	calls   []string
	sends   []json.RawMessage
	history json.RawMessage
}

func (c *fixtureClient) Close() error { return nil }
func (c *fixtureClient) Call(_ context.Context, m string, p json.RawMessage) (json.RawMessage, error) {
	c.calls = append(c.calls, m)
	switch m {
	case "agents.ensure_default":
		return json.RawMessage(`{"agent":{"agent_id":"main","main_session_id":"session"}}`), nil
	case "agents.send":
		c.sends = append(c.sends, p)
		return json.RawMessage(`{"state":"queued"}`), nil
	case "tasks.get":
		return json.RawMessage(`{"task":{"source":{"session_id":"session","message_seq":1,"message_binding":"host_turn"}}}`), nil
	case "agents.channel_history":
		if c.history != nil {
			return c.history, nil
		}
		return json.RawMessage(`{"records":[],"head_seq":0,"snapshot_seq":0}`), nil
	}
	return json.RawMessage(`{}`), nil
}

type fixtureDriver struct {
	sent    []Outgoing
	failure error
}

func (d *fixtureDriver) Run(ctx context.Context, _ func(context.Context, Incoming) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func (d *fixtureDriver) Post(_ context.Context, o Outgoing) (Receipt, error) {
	d.sent = append(d.sent, o)
	if d.failure != nil {
		return Receipt{}, d.failure
	}
	return Receipt{ConversationID: o.ConversationID, MessageID: "posted"}, nil
}
func (d *fixtureDriver) Validate(context.Context, Binding) error { return nil }
func fixtureBridge(t *testing.T) (*Bridge, *fixtureClient, *fixtureDriver, persistence.Documents) {
	t.Helper()
	s, e := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	c, d := &fixtureClient{}, &fixtureDriver{}
	b, e := New(Options{Documents: s, Client: func(context.Context, Binding) (Client, error) { return c, nil }, Drivers: map[string]Driver{"slack": d}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = b.Call(context.Background(), "im.configure", json.RawMessage(`{"platform":"slack","binding":{"tenant_id":"team","sender_id":"owner","conversation_id":"chat"}}`))
	if e != nil {
		t.Fatal(e)
	}
	// Enable persists intent; tests drive processing deterministically without timers.
	if e = b.update(context.Background(), func(s *ledger) error { s.Connections["slack"].Enabled = true; return nil }); e != nil {
		t.Fatal(e)
	}
	return b, c, d, s
}
func TestIngressIdentityDedupeAndRestart(t *testing.T) {
	b, c, _, s := fixtureBridge(t)
	ctx := context.Background()
	e := Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "thread", MessageID: "message", EventID: "event", Text: "keep exact  text"}
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	forged := e
	forged.SenderID = "other"
	forged.MessageID = "forged"
	if err := b.Accept(ctx, forged); err == nil {
		t.Fatal("unbound sender admitted")
	}
	b2, err := New(Options{Documents: s, Client: func(context.Context, Binding) (Client, error) { return c, nil }, Drivers: b.options.Drivers})
	if err != nil {
		t.Fatal(err)
	}
	if err = b2.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if err = b2.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(c.sends) != 1 {
		t.Fatalf("got %d sends", len(c.sends))
	}
	var p map[string]any
	json.Unmarshal(c.sends[0], &p)
	if p["prompt"] != e.Text {
		t.Fatal("literal input changed")
	}
}
func TestDelayedWorkerReplyKeepsOriginalThreadAndQuestionContinuation(t *testing.T) {
	b, c, d, _ := fixtureBridge(t)
	ctx := context.Background()
	e := Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "first", MessageID: "message", EventID: "event", Text: "delegate this"}
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	json.Unmarshal(c.sends[0], &sent)
	rid := sent["request_id"].(string)
	c.history = json.RawMessage(`{"session_id":"session","head_seq":5,"snapshot_seq":5,"records":[{"seq":1,"role":"user","receipt":{"request_id":"` + rid + `"}},{"seq":2,"role":"assistant","content":"Started"},{"seq":3,"role":"user","content":"private desktop message"},{"seq":4,"role":"user","relation":{"metadata":{"task_links":[{"task_id":"original-task","requirements_revision":2}]}},"hidden":true},{"seq":5,"role":"assistant","content":"Worker asks: which color?"}]}`)
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 2 || d.sent[1].ThreadID != "first" {
		t.Fatalf("wrong routing: %+v", d.sent)
	}
	e.MessageID = "answer"
	e.EventID = "answer-event"
	e.Text = "blue"
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	var answer map[string]any
	json.Unmarshal(c.sends[1], &answer)
	if answer["relation"] == nil {
		t.Fatal("IM reply lost Personal reply linkage")
	}
}
func TestUnknownDeliveryDoesNotRepeatAndDisableHolds(t *testing.T) {
	b, _, d, _ := fixtureBridge(t)
	ctx := context.Background()
	d.failure = errors.New("network outcome unknown")
	err := b.update(ctx, func(s *ledger) error {
		s.Outbox["reply"] = &delivery{Platform: "slack", State: "pending", Output: Outgoing{ConversationID: "chat", ThreadID: "t", Text: "result", ClientMessageID: "reply"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if err = b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 1 {
		t.Fatal("uncertain provider write repeated")
	}
	_, err = b.Call(ctx, "im.disable", json.RawMessage(`{"platform":"slack"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = b.update(ctx, func(s *ledger) error {
		s.Outbox["next"] = &delivery{Platform: "slack", State: "pending", Output: Outgoing{ConversationID: "chat", Text: "held"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 1 {
		t.Fatal("disabled delivery escaped")
	}
}

func TestNativeMessageDedupeAcrossEventKinds(t *testing.T) {
	b, _, _, _ := fixtureBridge(t)
	ctx := context.Background()
	e := Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "thread", MessageID: "same", EventID: "message-event", Text: "literal"}
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.EventID = "mention-event"
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	s, err := b.snapshot(ctx)
	if err != nil || len(s.Inbox) != 1 {
		t.Fatalf("duplicate native message: %v %+v", err, s)
	}
}

func TestExplicitDiscardNeverRetriesUnknownProviderEffect(t *testing.T) {
	b, _, d, _ := fixtureBridge(t)
	ctx := context.Background()
	if err := b.update(ctx, func(s *ledger) error {
		s.Outbox["unknown"] = &delivery{Platform: "slack", State: "uncertain", Output: Outgoing{Text: "original", ConversationID: "chat"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := b.Call(ctx, "im.pending", json.RawMessage(`{"platform":"slack"}`))
	if err != nil || !strings.Contains(string(raw), "unknown") {
		t.Fatalf("pending %s %v", raw, err)
	}
	_, err = b.Call(ctx, "im.resolve", json.RawMessage(`{"platform":"slack","kind":"output","id":"unknown","decision":"discard"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 0 {
		t.Fatal("discard retried external send")
	}
}

func TestLongReplyIsDurablyChunkedWithoutTruncation(t *testing.T) {
	b, c, d, _ := fixtureBridge(t)
	ctx := context.Background()
	e := Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "thread", MessageID: "m", EventID: "e", Text: "long answer"}
	if err := b.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	json.Unmarshal(c.sends[0], &input)
	want := strings.Repeat("中文🙂", 2500)
	raw, _ := json.Marshal(map[string]any{"records": []any{map[string]any{"seq": 1, "role": "user", "receipt": map[string]any{"request_id": input["request_id"]}}, map[string]any{"seq": 2, "role": "assistant", "content": want}}})
	c.history = raw
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, m := range d.sent {
		if len([]rune(m.Text)) > 3000 {
			t.Fatal("provider chunk too long")
		}
		got += m.Text
	}
	if got != want || len(d.sent) < 2 {
		t.Fatal("long output lost or reordered")
	}
}

type receiptClient struct {
	fixtureClient
	accepted bool
	resumed  map[string]any
}

func (c *receiptClient) Call(ctx context.Context, m string, p json.RawMessage) (json.RawMessage, error) {
	if m == "agents.messages.status" && c.accepted {
		return json.RawMessage(`{"receipt":{"receipt_id":"original-receipt","receipt_state":"accepted"}}`), nil
	}
	if m == "agents.messages.resume" {
		json.Unmarshal(p, &c.resumed)
		c.accepted = false
		return json.RawMessage(`{"state":"queued"}`), nil
	}
	return c.fixtureClient.Call(ctx, m, p)
}
func TestAcceptedInputRecoveryKeepsOriginalRequestAndReceipt(t *testing.T) {
	b, _, _, store := fixtureBridge(t)
	c := &receiptClient{}
	b.options.Client = func(context.Context, Binding) (Client, error) { return c, nil }
	ctx := context.Background()
	event := Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "t", MessageID: "m", EventID: "e", Text: "continue original"}
	if err := b.Accept(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	json.Unmarshal(c.sends[0], &first)
	c.accepted = true
	reopened, err := New(Options{Documents: store, Drivers: b.options.Drivers, Client: b.options.Client})
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if c.resumed["request_id"] != first["request_id"] || c.resumed["receipt_id"] != "original-receipt" || len(c.sends) != 1 {
		t.Fatal("accepted input became a new submission")
	}
}
func TestRetiredProviderDoesNotAcknowledgeOrSend(t *testing.T) {
	b, _, d, _ := fixtureBridge(t)
	b.options.Current = func() bool { return false }
	ctx := context.Background()
	if err := b.Accept(ctx, Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", MessageID: "m", EventID: "e", Text: "x"}); err == nil {
		t.Fatal("retired ingress acknowledged")
	}
	if err := b.Tick(ctx, "slack"); err == nil || len(d.sent) != 0 {
		t.Fatal("retired provider dispatched")
	}
}

func TestPluginContextPreservesReplyRoute(t *testing.T) {
	b, c, d, _ := fixtureBridge(t)
	ctx := context.Background()
	if err := b.Accept(ctx, Incoming{Platform: "slack", TenantID: "team", SenderID: "owner", ConversationID: "chat", ThreadID: "thread", MessageID: "m", EventID: "event", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		RequestID string `json:"request_id"`
	}
	json.Unmarshal(c.sends[0], &sent)
	c.history = json.RawMessage(`{"records":[{"seq":1,"role":"user","receipt":{"request_id":"` + sent.RequestID + `"}},{"seq":2,"role":"user","origin":"plugin","hidden":true,"read_only":true,"content":"identity context"},{"seq":3,"role":"assistant","content":"reply"},{"seq":4,"role":"user","content":"desktop"},{"seq":5,"role":"assistant","content":"private reply"}]}`)
	if err := b.Tick(ctx, "slack"); err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 1 || d.sent[0].ThreadID != "thread" {
		t.Fatalf("context/desktop route: %+v", d.sent)
	}
}
