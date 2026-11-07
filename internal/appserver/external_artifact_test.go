package appserver

import (
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// The live transcript keeps text, artifact, text as separate rows. Replay must
// persist the same boundary instead of appending the whole assistant message
// after the artifact.
func TestExternalArtifactToolNamesMatchAdapterPrefixes(t *testing.T) {
	for _, name := range []string{
		"present_artifact",
		"mcp:wuu_host:present_artifact",
		"mcp__wuu_host__present_artifact",
		"wuu_host_present_artifact",
		"wuu_host.present_artifact",
	} {
		if !externalArtifactToolName(name) {
			t.Errorf("externalArtifactToolName(%q) = false", name)
		}
	}
	for _, name := range []string{"browser_screenshot", "present_artifact_result"} {
		if externalArtifactToolName(name) {
			t.Errorf("externalArtifactToolName(%q) = true", name)
		}
	}
}

// A real adapter announces the host MCP call before Wuu publishes the
// artifact. Reuse that row and ignore the engine's later completion so the
// transcript keeps one artifact instead of a generic tool row plus a preview.
func TestExternalArtifactToolRowIgnoresEngineCompletion(t *testing.T) {
	now := time.Now().UTC()
	turnID := "thread-turn-0001"
	detail := toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeFile, Name: "preview.png", MIMEType: "image/png"}}}
	item := ThreadItem{
		ID: turnID + "-item-2", Type: ThreadItemToolCall, Status: ThreadItemStatusCompleted,
		Name: "present_artifact", SourceID: "bridge-call", ResultDetail: &detail, externalArtifact: true,
	}
	th := &threadState{
		Turns:     []Turn{{ID: turnID, Items: []ThreadItem{item}}},
		toolItems: map[string]string{"engine-call": item.ID},
	}
	batch := th.applyStreamEventLocked(turnID, providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &providers.ToolCall{ID: "engine-call", Name: "mcp:wuu_host:present_artifact"},
		ToolResult: "Artifact presented to the user.",
	}, now)
	if len(batch) != 0 {
		t.Fatalf("engine completion emitted %d notifications", len(batch))
	}
	items := th.Turns[0].Items
	if len(items) != 1 || items[0].ID != item.ID || items[0].Result != "" || items[0].ResultDetail == nil {
		t.Fatalf("engine completion changed the artifact row: %+v", items)
	}
}

func TestExternalArtifactReplayKeepsTextAroundArtifact(t *testing.T) {
	store := newAppserverTestStore(t, t.TempDir())
	sess, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	binding := &runtime.State{Sessions: store}
	now := time.Now().UTC()
	turnID := sess.ID + "-turn-0001"
	th := &threadState{
		ID: sess.ID, state: binding, PersistHistory: true, currentTurn: turnID,
		History: []providers.ChatMessage{{Role: "user", Content: "present fixture"}},
		Turns: []Turn{{ID: turnID, Items: []ThreadItem{
			{ID: turnID + "-item-2", Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Text: "BEFORE"},
		}}},
	}
	if _, err := store.AppendHistoryRecordReturningSeq(sess.ID, historyRecordFromPersistedMessage(persistedMessage{Role: "user", Content: "present fixture", At: now})); err != nil {
		t.Fatal(err)
	}
	server := &Server{}
	if err := server.persistExternalArtifactPrefixLocked(binding, th, turnID, now); err != nil {
		t.Fatal(err)
	}
	detail := toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeFile, Name: "preview.png", MIMEType: "image/png"}}}
	meta := persistedMessage{
		Role: "meta", Content: externalArtifactHistoryRecord, ClientID: turnID,
		ToolCallID: "artifact-call", Name: "present_artifact", DisplayContent: `{"path":"preview.png"}`,
		ToolResult: &detail, At: now,
	}
	if _, err := store.AppendHistoryRecordReturningSeq(sess.ID, historyRecordFromPersistedMessage(meta)); err != nil {
		t.Fatal(err)
	}

	result := agent.LoopResult{NewMessages: []providers.ChatMessage{{Role: "assistant", Content: "BEFOREAFTER"}}}
	result.NewMessages = th.trimExternalArtifactMessagesLocked(turnID, result.NewMessages)
	th.History = append(th.History, result.NewMessages...)
	if err := server.persistTurnResultLocked(th, result, false, "", "", 1); err != nil {
		t.Fatal(err)
	}

	records, _, err := loadProviderPersistedMessages(store, sess.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %+v, want user, assistant, artifact, assistant", records)
	}
	if records[0].Role != "user" || records[1].Role != "assistant" || records[1].Content != "BEFORE" || records[2].Content != externalArtifactHistoryRecord || records[3].Content != "AFTER" {
		t.Fatalf("durable order = %+v", records)
	}
	turns := turnsFromPersistedHistory(sess.ID, records, now, nil)
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	items := turns[0].Items
	if len(items) != 4 || items[1].Text != "BEFORE" || items[2].Name != "present_artifact" || items[3].Text != "AFTER" {
		t.Fatalf("replayed items = %+v", items)
	}
	if items[1].Seq != records[1].Seq || items[2].Seq != records[2].Seq || items[3].Seq != records[3].Seq {
		t.Fatalf("replayed item seqs do not match durable rows: %+v", items)
	}
}
