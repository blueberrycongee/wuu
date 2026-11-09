package session

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkStoreReads measures the public read paths, including connection
// configuration and idempotent migration, against an already initialized store.
func BenchmarkStoreReads(b *testing.B) {
	dir := b.TempDir()
	db, err := openStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		id := fmt.Sprintf("session-%03d", i)
		if err := insertSessionTx(tx, Session{ID: id, CreatedAt: at, CWD: "/synthetic/project"}); err != nil {
			b.Fatal(err)
		}
		for seq := 1; seq <= 10; seq++ {
			if err := insertHistoryRecordTx(tx, id, seq, HistoryRecord{Role: "user", Content: "synthetic history record"}); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}

	b.Run("open", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			db, err := OpenStore(dir)
			if err != nil {
				b.Fatal(err)
			}
			if err := db.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("list_100", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sessions, err := List(dir, 0)
			if err != nil || len(sessions) != 100 {
				b.Fatalf("list sessions: count=%d, err=%v", len(sessions), err)
			}
		}
	})
	b.Run("history_10", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			records, err := LoadHistoryRecords(dir, "session-000", false)
			if err != nil || len(records) != 10 {
				b.Fatalf("load history: count=%d, err=%v", len(records), err)
			}
		}
	})
}
