package appserver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalim"
)

func TestPersonalChannelSourceBindsNativeIdentityAndExactEffect(t *testing.T) {
	binding := personalim.Binding{Platform: "slack", TenantID: "T1", SenderID: "U1", ConversationID: "D1"}
	source := personalim.Incoming{Platform: "slack", TenantID: "T1", SenderID: "U1", ConversationID: "D1", MessageID: "123.45", Text: "literal native input"}
	requestID := fmt.Sprintf("im-%x", sha256.Sum256([]byte("slack\x00T1\x00D1\x00123.45")))
	send := map[string]any{"request_id": requestID, "prompt": source.Text, "channel_source": source}
	check := func(method string, input any, want bool) {
		t.Helper()
		raw, _ := json.Marshal(input)
		out, err := validateChannelSource(binding, method, raw)
		if (err == nil) != want {
			t.Fatalf("%s accepted=%v want=%v: %v", method, err == nil, want, err)
		}
		if err == nil && strings.Contains(string(out), "channel_source") {
			t.Fatal("native attestation leaked to Personal input")
		}
	}
	check("agents.send", send, true)
	send["prompt"] = "rewritten"
	check("agents.send", send, false)
	send["prompt"] = source.Text
	send["request_id"] = "new-id"
	check("agents.send", send, false)
	send["request_id"] = requestID
	wrong := source
	wrong.SenderID = "other"
	send["channel_source"] = wrong
	check("agents.send", send, false)
	send["channel_source"] = source
	check("agents.messages.resume", map[string]any{"request_id": requestID, "receipt_id": "receipt-1", "channel_source": source}, true)
	check("agents.messages.resume", send, false)
	check("im.enable", map[string]any{}, false)
	check("review.list", map[string]any{}, true)
	check("agents.pause", map[string]any{}, false)
	source.Text = "!wuu pause"
	check("agents.pause", map[string]any{"channel_source": source}, true)
	check("agents.resume", map[string]any{"channel_source": source}, false)
	source.Text = "!wuu stop task-1 3"
	check("tasks.stop", map[string]any{"channel_source": source, "task_id": "task-1", "expected_revision": 3}, true)
	check("tasks.stop", map[string]any{"channel_source": source, "task_id": "task-1", "expected_revision": 4}, false)
	source.Text = "!wuu approve action-1 digest-1 2"
	approval := map[string]any{"channel_source": source, "id": "action-1", "digest": "digest-1", "expected_revision": 2, "choice": "approve_once"}
	check("review.resolve", approval, true)
	approval["digest"] = "changed"
	check("review.resolve", approval, false)
}
