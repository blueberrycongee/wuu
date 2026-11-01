package personalim

import (
	"context"
	"encoding/json"
	"testing"
)

type commandClient struct {
	resolved int
	state    string
}

func (c *commandClient) Close() error { return nil }
func (c *commandClient) Call(_ context.Context, m string, p json.RawMessage) (json.RawMessage, error) {
	if m == "review.list" {
		return json.RawMessage(`{"ok":true,"value":{"kind":"ready","requests":[{"revision":2,"status":"` + c.state + `","action":{"id":"act","digest":"abc","scope":{"space_id":"space"},"summary":{"title":"Write file"}}}]}}`), nil
	}
	if m == "review.resolve" {
		c.resolved++
		c.state = "succeeded"
		return json.RawMessage(`{"ok":true,"value":{"kind":"ready","requests":[]}}`), nil
	}
	return json.RawMessage(`{}`), nil
}
func TestExplicitCommandApprovalBindsDigestAndDoesNotReplay(t *testing.T) {
	c := &commandClient{state: "awaiting_confirmation"}
	ctx := context.Background()
	if _, _, err := channelCommand(ctx, c, "!wuu approve act wrong 2", "main"); err == nil {
		t.Fatal("wrong digest approved")
	}
	handled, _, err := channelCommand(ctx, c, "!wuu approve act abc 2", "main")
	if err != nil || !handled || c.resolved != 1 {
		t.Fatalf("approval %v %v %d", handled, err, c.resolved)
	}
	if _, _, err = channelCommand(ctx, c, "!wuu approve act abc 2", "main"); err != nil || c.resolved != 1 {
		t.Fatal("approved effect replayed")
	}
	handled, _, err = channelCommand(ctx, c, "quoted !wuu approve act abc 2", "main")
	if err != nil || handled {
		t.Fatal("ordinary text interpreted as control")
	}
}

func TestCommandRejectsTrailingNaturalLanguage(t *testing.T) {
	handled, _, err := channelCommand(context.Background(), &commandClient{}, "!wuu pause but do not pause", "main")
	if !handled || err == nil {
		t.Fatal("ambiguous command admitted")
	}
}
