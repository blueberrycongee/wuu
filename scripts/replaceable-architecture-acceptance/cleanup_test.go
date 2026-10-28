package acceptance_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

// A persistence commit method must reject ordinary autonomous work
// after retirement while still letting a dependent flush during its actual Close.
func TestAcceptanceCleanupMethodAdmission(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			host, documents, consumer, _ := startAcceptanceProvider(t, kind, dir)
			_, err := consumer.store.CreateInitialized(session.Session{ID: "cleanup-thread"}, []session.HistoryRecord{{Role: "user", Content: "normal active write"}})
			require.NoError(t, err)
			consumer.thread = "cleanup-thread"
			view, err := documents.Read(ctx, persistence.Query{Namespace: "acceptance.retirement"})
			require.NoError(t, err)
			host.StopBackground()
			_, err = documents.Commit(ctx, persistence.Transaction{ID: "forbidden-after-stop", ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "acceptance.retirement", Key: "late", Value: json.RawMessage(`{"should_not_persist":true}`)}}})
			if err == nil {
				t.Error("ordinary state.documents.commit accepted after StopBackground")
			}
			require.NoError(t, host.Close(ctx), "actual dependent shutdown must retain its persistence provider")
			_, reopened, restarted, _ := startAcceptanceProvider(t, kind, dir)
			history, err := restarted.store.LoadHistoryRecords("cleanup-thread", true)
			require.NoError(t, err)
			require.Len(t, history, 2)
			require.Equal(t, "dependent cleanup persisted", history[1].Content)
			after, err := reopened.Read(ctx, persistence.Query{Namespace: "acceptance.retirement"})
			require.NoError(t, err)
			if len(after.Documents) != 0 {
				t.Errorf("post-retirement ordinary write reached durable storage: %d document(s)", len(after.Documents))
			}
			t.Log("actual Close cleanup persisted and provider reopened; autonomous retirement assertion evaluated separately")
		})
	}
}
