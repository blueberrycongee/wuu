package compactionservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestNativeNotePlanOwnsCadenceAndPreparedHistory(t *testing.T) {
	host := pluginhost.New(&noteCapability{})
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := WithCapabilityHost(context.Background(), host)
	c := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	messages := []providers.ChatMessage{{Role: "user", Content: "short", Seq: 1}}
	plan, err := c.NotePlan(ctx, NoteRequest{PolicyKey: "notes:agent.compaction", Messages: messages})
	if err != nil || plan.Due {
		t.Fatalf("premature checkpoint %+v %v", plan, err)
	}
	plan, err = c.NotePlan(ctx, NoteRequest{PolicyKey: "notes:agent.compaction", Messages: messages, Force: true})
	if err != nil || !plan.Due || plan.CoveredMessages != 1 || len(plan.Messages) != 1 || messages[0].Content != "short" {
		t.Fatalf("forced plan %+v %v", plan, err)
	}
	_, err = c.NotePlan(ctx, NoteRequest{PolicyKey: "notes:agent.compaction", Messages: []providers.ChatMessage{{Role: "system", Content: strings.Repeat("fixed ", 1000)}, {Role: "user", Content: "tail"}}, PreviousNote: "checkpoint", PreviousCoveredMessages: 1, InputTokens: 10, Force: true})
	if err == nil {
		t.Fatal("oversized fixed context admitted")
	}
}
