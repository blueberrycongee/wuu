package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/conversations"
)

func TestTextSnapshotReadsFullDisplayTextWithoutResumingOrLeakingPayloads(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("history-copy", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now())
	text := strings.Repeat("完整正文", 3000)
	th.Turns = []Turn{{ID: "turn", Items: []ThreadItem{
		{ID: "user", Type: ThreadItemUserMessage, Text: "visible question", InputText: "private raw input", Images: []ThreadItemImage{{Data: "private image"}}},
		{ID: "tool", Type: ThreadItemToolCall, Text: "private tool display", Arguments: "private args", Result: "private terminal output"},
		{ID: "reason", Type: ThreadItemReasoning, Text: "private reasoning"},
		{ID: "answer", Type: ThreadItemAgentMessage, Text: text},
	}}}
	srv.threads[th.ID] = th
	raw, _ := json.Marshal(map[string]any{"id": "snapshot", "method": "thread/textSnapshot", "params": map[string]string{"thread_id": th.ID}})
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "snapshot")
	if response["error"] != nil {
		t.Fatal(response)
	}
	snapshot := remarshal[conversations.Thread](t, response["result"])
	if len(snapshot.Messages) != 2 || snapshot.Messages[1].Text != text || snapshot.Messages[0].Text != "visible question" {
		t.Fatal("display text lost or extra content exported")
	}
	if strings.Contains(out.String(), "private") {
		t.Fatal("non-display payload leaked")
	}
	if th.running || len(th.Turns[0].Items) != 4 {
		t.Fatal("read mutated execution/history")
	}
	th.Turns[0].Items[3].Text = strings.Repeat("x", conversations.MaxSnapshotBytes)
	raw, _ = json.Marshal(map[string]any{"id": "oversized", "method": "thread/textSnapshot", "params": map[string]string{"thread_id": th.ID}})
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "oversized")["error"] == nil {
		t.Fatal("oversized snapshot silently accepted")
	}
}
