package session

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestMessageRelationSurvivesHistoryAndCheckpoint(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		store := NewStore(context.Background(), native.documents)
		sess, err := store.Create("reply-thread")
		if err != nil {
			t.Fatal(err)
		}
		relation := &providers.MessageRelation{
			ReplyTo:  &providers.MessageRef{SessionID: "source-thread", Seq: 7},
			Metadata: map[string]json.RawMessage{"extension.task": json.RawMessage(`{"id":"task-1","status":"done"}`)},
		}
		if err := store.AppendHistoryRecord(sess.ID, HistoryRecord{Role: "assistant", Content: "answer", Relation: relation}); err != nil {
			t.Fatal(err)
		}
		records, err := store.LoadHistoryRecords(sess.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || !reflect.DeepEqual(records[0].Relation, relation) {
			t.Fatalf("relation lost in durable history: %+v", records)
		}
		if _, err := store.StoreHistoryCheckpointAtBaseline(sess.ID, HistoryCheckpointKindProviderRewrite, records, 1); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.LoadProviderHistorySnapshot(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Records) != 1 || !reflect.DeepEqual(snapshot.Records[0].Relation, relation) {
			t.Fatalf("relation lost in checkpoint: %+v", snapshot)
		}
		// Provider fallback snapshots must not share mutable routing metadata.
		message := providers.ChatMessage{Relation: relation}
		cloned := providers.CloneChatMessage(message)
		cloned.Relation.ReplyTo.Seq = 99
		cloned.Relation.Metadata["extension.task"][0] = '['
		cloned.Relation.Metadata["new"] = json.RawMessage(`true`)
		if message.Relation.ReplyTo.Seq != 7 || string(message.Relation.Metadata["extension.task"]) != `{"id":"task-1","status":"done"}` || len(message.Relation.Metadata) != 1 {
			t.Fatal("cloned message aliases original relation")
		}
	})
}

// A stopped Personal SQLite source keeps both old empty metadata and new
// relation/scope columns during the explicit selected-provider migration.
func TestMessageRelationMigrationKeepsLegacyRows(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		dir := t.TempDir()
		if _, err := Create(dir, "legacy-thread"); err != nil {
			t.Fatal(err)
		}
		if err := AppendHistoryRecord(dir, "legacy-thread", HistoryRecord{Role: "user", Content: "legacy"}); err != nil {
			t.Fatal(err)
		}
		if _, err := Create(dir, "personal-thread"); err != nil {
			t.Fatal(err)
		}
		if err := AppendHistoryRecord(dir, "personal-thread", HistoryRecord{Role: "assistant", Content: "reply"}); err != nil {
			t.Fatal(err)
		}
		db, err := openStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, statement := range []string{
			`ALTER TABLE sessions ADD COLUMN user_space_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE sessions ADD COLUMN product_domain TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE sessions ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE session_messages ADD COLUMN relation_json TEXT NOT NULL DEFAULT ''`,
			`UPDATE sessions SET user_space_id='local', product_domain='personal', agent_id='agent' WHERE id='personal-thread'`,
			`UPDATE session_messages SET relation_json='{"reply_to":{"session_id":"legacy-thread","seq":1}}' WHERE session_id='personal-thread'`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := MigrateLegacySQLite(context.Background(), dir, store.documents); err != nil {
			t.Fatal(err)
		}
		legacy, err := store.LoadHistoryRecords("legacy-thread", false)
		if err != nil || len(legacy) != 1 || legacy[0].Relation != nil || legacy[0].Content != "legacy" {
			t.Fatalf("legacy changed: %+v %v", legacy, err)
		}
		personal, found, err := store.Find("personal-thread")
		if err != nil || !found || personal.UserSpaceID != "local" || personal.ProductDomain != "personal" || personal.AgentID != "agent" {
			t.Fatalf("scope lost: %+v %v", personal, err)
		}
		records, err := store.LoadHistoryRecords("personal-thread", false)
		if err != nil || len(records) != 1 || records[0].Relation == nil || records[0].Relation.ReplyTo == nil || records[0].Relation.ReplyTo.SessionID != "legacy-thread" {
			t.Fatalf("relation lost: %+v %v", records, err)
		}
	})
}
