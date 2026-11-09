package session

import (
	"reflect"
	"testing"
	"time"
)

func TestLatestSubscriptionActivityUsesRequestTimeAndIgnoresNewEmptySessions(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		older, err := store.CreateWithMetadata("older-codex", "/tmp/project")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEngine(older.ID, "codex"); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendHistoryRecord(older.ID, HistoryRecord{
			Role:           "meta",
			Content:        turnTerminalContent,
			StopReason:     "failed",
			DisplayContent: "old failure",
			At:             time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}

		newer, err := store.CreateWithMetadata("newer-codex", "/tmp/project")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEngine(newer.ID, "codex"); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendHistoryRecord(newer.ID, HistoryRecord{
			Role:         "meta",
			Content:      tokenUsageContent,
			Model:        "gpt-5-codex",
			InputTokens:  12,
			OutputTokens: 4,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendHistoryRecord(newer.ID, HistoryRecord{
			Role:       "meta",
			Content:    turnTerminalContent,
			ClientID:   "newer-codex-turn-0001",
			StopReason: "completed",
			At:         time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}

		provider, err := store.CreateWithMetadata("xai-thread", "/tmp/project")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetRuntimeSelection(provider.ID, RuntimeSelection{
			Provider: "xai-subscription",
			Model:    "grok-4",
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendHistoryRecord(provider.ID, HistoryRecord{
			Role:           "meta",
			Content:        turnTerminalContent,
			StopReason:     "failed",
			DisplayContent: "subscription rejected",
			Provider:       "xai-subscription",
		}); err != nil {
			t.Fatal(err)
		}

		if err := store.UpdateIndex(older.ID, 0, "renamed recently"); err != nil {
			t.Fatal(err)
		}
		empty, err := store.CreateWithMetadata("empty-codex", "/tmp/project")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEngine(empty.ID, "codex"); err != nil {
			t.Fatal(err)
		}
		got, err := store.LatestSubscriptionActivity([]SubscriptionActivityKey{
			{EngineID: "codex"},
			{Provider: "xai-subscription"},
			{EngineID: "claude"},
			{},
		})
		if err != nil {
			t.Fatal(err)
		}
		codex := got[SubscriptionActivityKey{EngineID: "codex"}]
		if codex.Status != "completed" || codex.Error != "" || !codex.UsageReported || codex.InputTokens != 12 || codex.OutputTokens != 4 || codex.Model != "gpt-5-codex" {
			t.Fatalf("codex activity = %+v", codex)
		}
		xai := got[SubscriptionActivityKey{Provider: "xai-subscription"}]
		if xai.Status != "failed" || xai.Error != "subscription rejected" || xai.UsageReported {
			t.Fatalf("xai activity = %+v", xai)
		}
		if _, ok := got[SubscriptionActivityKey{EngineID: "claude"}]; ok {
			t.Fatal("unused engine reported activity")
		}
	})
}

func TestLatestSubscriptionActivityMissingStore(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		got, err := store.LatestSubscriptionActivity([]SubscriptionActivityKey{{EngineID: "codex"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("missing store returned %+v", got)
		}
	})
}

func TestSubscriptionUsageTracksRecordedProviderAcrossSelectionChanges(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		sess, err := store.CreateWithMetadata("usage", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetRuntimeSelection(sess.ID, RuntimeSelection{Provider: "new", Model: "model"}); err != nil {
			t.Fatal(err)
		}
		for _, record := range []HistoryRecord{
			{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 10, OutputTokens: 3, CacheReadTokens: 5},
			{Role: "meta", Content: tokenUsageContent, Provider: "new", InputTokens: 20, OutputTokens: 7},
			{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 4, OutputTokens: 2},
			{Role: "meta", Content: tokenUsageContent, Provider: "new", ContextTokens: 900},
		} {
			if err := store.AppendHistoryRecord(sess.ID, record); err != nil {
				t.Fatal(err)
			}
		}
		other, err := store.CreateWithMetadata("external", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEngine(other.ID, "codex"); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendHistoryRecord(other.ID, HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 100}); err != nil {
			t.Fatal(err)
		}
		got, err := store.LatestSubscriptionActivity([]SubscriptionActivityKey{{Provider: "old"}, {Provider: "new"}, {EngineID: "codex"}})
		if err != nil {
			t.Fatal(err)
		}
		old := got[SubscriptionActivityKey{Provider: "old"}].LocalUsage
		if old.InputTokens != 14 || old.OutputTokens != 5 || old.CacheReadTokens != 5 || old.ReportedTurns != 2 {
			t.Fatalf("old = %+v", old)
		}
		newUsage := got[SubscriptionActivityKey{Provider: "new"}].LocalUsage
		if newUsage.InputTokens != 20 || newUsage.ReportedTurns != 1 {
			t.Fatalf("new = %+v", newUsage)
		}
		if external := got[SubscriptionActivityKey{EngineID: "codex"}].LocalUsage; external.InputTokens != 100 {
			t.Fatalf("external = %+v", external)
		}
		// A shared scan must match isolated reads even when external and built-in
		// requests record the same provider name, and when a source has only usage.
		for key, activity := range got {
			single, err := store.LatestSubscriptionActivity([]SubscriptionActivityKey{key, key, {}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(single[key], activity) {
				t.Fatalf("batch changed attribution for %+v: batch=%+v single=%+v", key, activity, single[key])
			}
		}
	})
}

func TestSubscriptionLatestRequestDoesNotMixTurnsOrProviders(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		sess, err := store.CreateWithMetadata("switching", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		appendRecord := func(record HistoryRecord) {
			t.Helper()
			at = at.Add(time.Second)
			record.At = at
			if err := store.AppendHistoryRecord(sess.ID, record); err != nil {
				t.Fatal(err)
			}
		}
		appendRecord(HistoryRecord{Role: "user", Content: "old request"})
		appendRecord(HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "old", Model: "old-model", InputTokens: 12})
		// A legacy terminal can use a provider recorded within this same request.
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, ClientID: "switching-turn-0001", StopReason: "completed"})
		appendRecord(HistoryRecord{Role: "user", Content: "new request"})
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, Provider: "new", Model: "new-model", StopReason: "failed", DisplayContent: "new failure"})
		// Changing selection afterwards must not move either request.
		if _, err := store.SetRuntimeSelection(sess.ID, RuntimeSelection{Provider: "unused", Model: "unused"}); err != nil {
			t.Fatal(err)
		}
		keys := []SubscriptionActivityKey{{Provider: "old"}, {Provider: "new"}, {Provider: "unused"}}
		got, err := store.LatestSubscriptionActivity(keys)
		if err != nil {
			t.Fatal(err)
		}
		old, newer := got[keys[0]], got[keys[1]]
		if old.Status != "completed" || old.Model != "old-model" || old.InputTokens != 12 || !old.UsageReported {
			t.Fatalf("old request = %+v", old)
		}
		if newer.Status != "failed" || newer.Error != "new failure" || newer.Model != "new-model" || newer.UsageReported {
			t.Fatalf("new request = %+v", newer)
		}
		if _, ok := got[keys[2]]; ok {
			t.Fatal("current selection inherited historical activity")
		}
		appendRecord(HistoryRecord{Role: "user", Content: "old provider fails without usage"})
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, Provider: "old", StopReason: "failed"})
		got, err = store.LatestSubscriptionActivity(keys)
		if err != nil {
			t.Fatal(err)
		}
		if latest := got[keys[0]]; latest.Status != "failed" || latest.UsageReported || latest.Model != "" || latest.LocalUsage.InputTokens != 12 {
			t.Fatalf("failed request reused old usage: %+v", latest)
		}
		// Internal continuations have no new user boundary or success terminal.
		appendRecord(HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 8})
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, Provider: "old", StopReason: "failed"})
		got, err = store.LatestSubscriptionActivity(keys)
		if err != nil {
			t.Fatal(err)
		}
		if latest := got[keys[0]]; latest.UsageReported || latest.LocalUsage.InputTokens != 20 {
			t.Fatalf("continuation reused earlier usage: %+v", latest)
		}
		appendRecord(HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "legacy-unknown", InputTokens: 1})
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, StopReason: "failed"})
		legacyKey := SubscriptionActivityKey{Provider: "legacy-unknown"}
		legacy, err := store.LatestSubscriptionActivity([]SubscriptionActivityKey{legacyKey})
		if err != nil {
			t.Fatal(err)
		}
		if got := legacy[legacyKey]; got.UsageReported || got.Status != "" {
			t.Fatalf("unattributed terminal inherited usage: %+v", got)
		}

		appendRecord(HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 3})
		appendRecord(HistoryRecord{Role: "meta", Content: turnTerminalContent, Provider: "old", StopReason: "failed", InputTokens: 3})
		got, err = store.LatestSubscriptionActivity(keys)
		if err != nil {
			t.Fatal(err)
		}
		if latest := got[keys[0]]; !latest.UsageReported || latest.InputTokens != 3 || latest.LocalUsage.InputTokens != 23 {
			t.Fatalf("terminal usage lost or double counted: %+v", latest)
		}
	})
}
