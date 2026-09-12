package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestYieldTurnRequiresRecordedReason(t *testing.T) {
	tool := NewYieldTurnTool()
	for _, input := range []string{`{}`, `{"reason":" "}`, `broken`} {
		if _, err := tool.Execute(context.Background(), input); err == nil {
			t.Fatalf("accepted invalid yield: %s", input)
		}
	}
	text, err := tool.Execute(context.Background(), `{"reason":"  The peer only confirmed receipt.  "}`)
	var result struct {
		Yielded bool   `json:"yielded"`
		Reason  string `json:"reason"`
	}
	if err != nil || json.Unmarshal([]byte(text), &result) != nil || !result.Yielded || result.Reason != "The peer only confirmed receipt." {
		t.Fatalf("yield = %s, %v", text, err)
	}
}
