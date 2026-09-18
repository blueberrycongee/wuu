package codexengine

import (
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTurnUsageCountsCallsWithoutReplays(t *testing.T) {
	var snapshots []*providers.TokenUsage
	sub := &turnSubscription{
		turnID: "turn", done: make(chan turnOutcome, 1), events: make(chan turnNotification, 8),
		sink: func(ev providers.StreamEvent) {
			if ev.Usage != nil {
				snapshots = append(snapshots, ev.Usage)
			}
		},
	}
	// A resumed thread already has usage. Equal call sizes still represent
	// distinct calls when the cumulative cursor advances.
	for _, cursor := range []int{1120, 1120, 1000, 1240, 1240} {
		raw, err := json.Marshal(map[string]any{
			"turnId": "turn", "tokenUsage": map[string]any{
				"total": map[string]int{"totalTokens": cursor},
				"last": map[string]int{"inputTokens": 100, "cachedInputTokens": 40,
					"cacheWriteInputTokens": 10, "outputTokens": 20, "reasoningOutputTokens": 5},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		sub.events <- turnNotification{NotifyTokenUsageUpdated, raw}
	}
	sub.events <- turnNotification{NotifyTurnCompleted, json.RawMessage(`{"turn":{"id":"turn","status":"completed"}}`)}
	close(sub.events)
	sub.run()
	out := <-sub.done
	result := out.result.Result
	if out.err != nil || result.InputTokens != 100 || result.OutputTokens != 40 || result.CacheReadTokens != 80 || result.CacheCreationTokens != 20 {
		t.Fatalf("incorrect per-turn usage: %+v, error %v", result, out.err)
	}
	if len(snapshots) != 2 || snapshots[0].OutputTokens != 20 || snapshots[1].OutputTokens != 40 {
		t.Fatalf("usage snapshots were replayed or mutated: %+v", snapshots)
	}
}
