package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestClientInputReadFollowsModelContext(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		store := NewStore(context.Background(), native.documents)
		const sessionID = "personal"
		if _, err := store.CreateInitialized(Session{ID: sessionID, UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin"}, nil); err != nil {
			t.Fatal(err)
		}
		baseline := Control{SessionID: sessionID}
		runtime, _ := json.Marshal(struct{ ExecutionControlBaseline *Control }{&baseline})
		scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, SessionID: sessionID, PluginID: "plugin"}
		accept := func(id string) AcceptedClientInput {
			t.Helper()
			input := AcceptedClientInput{Receipt: providers.ClientInputReceipt{SessionID: sessionID, UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin", PluginID: "plugin", HostRequestID: "host-" + id, RequestID: "request-" + id, ClientID: "client-" + id, ReceiptID: "receipt-" + id, QueueID: "queue-" + id}, Fingerprint: "fingerprint-" + id, MessageJSON: []byte(`{}`), RuntimeJSON: runtime, LiveOwner: "owner"}
			accepted, _, err := store.AcceptClientInput(input, nil, &baseline)
			if err != nil {
				t.Fatal(err)
			}
			return accepted
		}
		read := func(id string) (string, int64) {
			t.Helper()
			input, found, err := store.ReadClientInput(scope, "host-"+id)
			if err != nil || !found {
				t.Fatalf("read %s: found=%v err=%v", id, found, err)
			}
			raw, err := json.Marshal(input.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			var projected struct {
				ReadAt   string `json:"read_at"`
				Revision int64  `json:"revision"`
			}
			if err := json.Unmarshal(raw, &projected); err != nil {
				t.Fatal(err)
			}
			return projected.ReadAt, projected.Revision
		}
		first, second := accept("one"), accept("two")
		if got, _ := read("one"); got != "" {
			t.Fatalf("queued input was read: %s", got)
		}
		appendInput := func(input AcceptedClientInput, turn string) int {
			t.Helper()
			seq, err := store.AppendAcceptedClientHistoryRecord(sessionID, HistoryRecord{Role: "user", Origin: "user", ClientID: input.Receipt.ClientID, Content: "same content"}, input, turn, nil, &baseline)
			if err != nil {
				t.Fatal(err)
			}
			return seq
		}
		firstSeq := appendInput(first, "turn-one")
		if got, _ := read("one"); got != "" {
			t.Fatalf("history admission was read: %s", got)
		}
		other := scope
		other.SessionID = "other-session"
		if _, err := store.CreateInitialized(Session{ID: other.SessionID, UserSpaceID: "local", ProductDomain: "personal", AgentID: "other", Owner: "plugin"}, nil); err != nil {
			t.Fatal(err)
		}
		markContext := func(target ClientInputScope, refs ...ClientInputContextRef) {
			t.Helper()
			if err := store.MarkClientInputsRead(target, refs); err != nil {
				t.Fatal(err)
			}
		}
		markContext(scope, ClientInputContextRef{ClientID: first.Receipt.ClientID, MessageSeq: firstSeq + 100})
		markContext(other, ClientInputContextRef{ClientID: first.Receipt.ClientID, MessageSeq: firstSeq})
		markContext(scope, ClientInputContextRef{ClientID: second.Receipt.ClientID, MessageSeq: firstSeq})
		if got, _ := read("one"); got != "" {
			t.Fatalf("mismatched context marked input read: %s", got)
		}
		markContext(scope, ClientInputContextRef{ClientID: first.Receipt.ClientID, MessageSeq: firstSeq})
		readAt, revision := read("one")
		if readAt == "" {
			t.Fatal("input in model context was not marked read")
		}
		if _, err := time.Parse(time.RFC3339Nano, readAt); err != nil {
			t.Fatalf("invalid read time %q: %v", readAt, err)
		}
		if got, _ := read("two"); got != "" {
			t.Fatalf("unrelated queued input was read: %s", got)
		}
		secondSeq := appendInput(second, "turn-two")
		markContext(scope, ClientInputContextRef{ClientID: first.Receipt.ClientID, MessageSeq: firstSeq}, ClientInputContextRef{ClientID: second.Receipt.ClientID, MessageSeq: secondSeq})
		if got, nextRevision := read("one"); got != readAt || nextRevision != revision {
			t.Fatalf("read status changed on repeated context: %s/%d", got, nextRevision)
		}
		if got, _ := read("two"); got == "" {
			t.Fatal("second input in model context was not marked read")
		}
		store = NewStore(context.Background(), native.documents)
		if got, _ := read("one"); got != readAt {
			t.Fatalf("read status lost after reopening: got %s want %s", got, readAt)
		}
	})
}
