package session

import (
	"testing"
	"time"
)

func TestLatestSubscriptionActivityReadsNewestSessionOnly(t *testing.T) {
	dir := t.TempDir()
	older, err := CreateWithMetadata(dir, "older-codex", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetEngine(dir, older.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, older.ID, HistoryRecord{
		Role:           "meta",
		Content:        turnTerminalContent,
		StopReason:     "failed",
		DisplayContent: "old failure",
		At:             time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	newer, err := CreateWithMetadata(dir, "newer-codex", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetEngine(dir, newer.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, newer.ID, HistoryRecord{
		Role:       "meta",
		Content:    turnTerminalContent,
		StopReason: "completed",
		At:         time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, newer.ID, HistoryRecord{
		Role:         "meta",
		Content:      tokenUsageContent,
		Model:        "gpt-5-codex",
		InputTokens:  12,
		OutputTokens: 4,
	}); err != nil {
		t.Fatal(err)
	}

	provider, err := CreateWithMetadata(dir, "xai-thread", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, provider.ID, RuntimeSelection{
		Provider: "xai-subscription",
		Model:    "grok-4",
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, provider.ID, HistoryRecord{
		Role:           "meta",
		Content:        turnTerminalContent,
		StopReason:     "failed",
		DisplayContent: "subscription rejected",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := LatestSubscriptionActivity(dir, []SubscriptionActivityKey{
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
}

func TestLatestSubscriptionActivityMissingStore(t *testing.T) {
	got, err := LatestSubscriptionActivity(t.TempDir(), []SubscriptionActivityKey{{EngineID: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing store returned %+v", got)
	}
}

func TestSubscriptionUsageTracksRecordedProviderAcrossSelectionChanges(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "usage", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, sess.ID, RuntimeSelection{Provider: "new", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []HistoryRecord{
		{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 10, OutputTokens: 3, CacheReadTokens: 5},
		{Role: "meta", Content: tokenUsageContent, Provider: "new", InputTokens: 20, OutputTokens: 7},
		{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 4, OutputTokens: 2},
		{Role: "meta", Content: tokenUsageContent, Provider: "new", ContextTokens: 900},
	} {
		if err := AppendHistoryRecord(dir, sess.ID, record); err != nil {
			t.Fatal(err)
		}
	}
	other, err := CreateWithMetadata(dir, "external", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetEngine(dir, other.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, other.ID, HistoryRecord{Role: "meta", Content: tokenUsageContent, Provider: "old", InputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	got, err := LatestSubscriptionActivity(dir, []SubscriptionActivityKey{{Provider: "old"}, {Provider: "new"}, {EngineID: "codex"}})
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
}
