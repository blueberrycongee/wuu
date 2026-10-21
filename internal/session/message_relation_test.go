package session

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestMessageRelationSurvivesHistoryAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	sess, err := Create(dir, "reply-thread")
	if err != nil {
		t.Fatal(err)
	}
	relation := &providers.MessageRelation{
		ReplyTo:  &providers.MessageRef{SessionID: "source-thread", Seq: 7},
		Metadata: map[string]json.RawMessage{"extension.task": json.RawMessage(`{"id":"task-1","status":"done"}`)},
	}
	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{Role: "assistant", Content: "answer", Relation: relation}); err != nil {
		t.Fatal(err)
	}
	records, err := LoadHistoryRecords(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !reflect.DeepEqual(records[0].Relation, relation) {
		t.Fatalf("relation lost in durable history: %+v", records)
	}
	if _, err := StoreHistoryCheckpointAtBaseline(dir, sess.ID, HistoryCheckpointKindProviderRewrite, records, 1); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadProviderHistorySnapshot(dir, sess.ID)
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
}

func TestMessageRelationMigrationKeepsLegacyRows(t *testing.T) {
	dir := t.TempDir()
	sess, err := Create(dir, "legacy-thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{Role: "user", Content: "legacy"}); err != nil {
		t.Fatal(err)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE session_messages DROP COLUMN relation_json"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	records, err := LoadHistoryRecords(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Relation != nil || records[0].Content != "legacy" {
		t.Fatalf("legacy history changed: %+v", records)
	}
	relation := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: sess.ID, Seq: 1}}
	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{Role: "assistant", Content: "reply", Relation: relation}); err != nil {
		t.Fatal(err)
	}
	records, err = LoadHistoryRecords(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !reflect.DeepEqual(records[1].Relation, relation) {
		t.Fatalf("migrated history lost relation: %+v", records)
	}
}
