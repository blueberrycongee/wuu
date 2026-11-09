package compactionservice

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSelectedCompactionPreservesMessagesAndErrors(t *testing.T) {
	input := Request{Messages: []providers.ChatMessage{{Role: "user", Content: "synthetic", Seq: 9, Origin: "plugin", DisplayContent: "shown"}}}
	calls := 0
	c := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		var request Request
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if method != "compact" || !reflect.DeepEqual(request.Messages, input.Messages) {
			t.Fatalf("wire lost history: %+v", request)
		}
		return json.Marshal(Replacement{Messages: request.Messages})
	})
	got, err := c.Compact(context.Background(), input)
	if err != nil || !reflect.DeepEqual(got.Messages, input.Messages) || calls != 1 {
		t.Fatalf("got %+v %v", got, err)
	}
	failure := errors.New("external rejected")
	c = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, failure })
	if _, err = c.Compact(context.Background(), input); !errors.Is(err, failure) {
		t.Fatalf("lost failure %v", err)
	}
	if input.Messages[0].Content != "synthetic" {
		t.Fatal("mutated input")
	}
}

func TestNativePolicyLifecycleAndWindowProposal(t *testing.T) {
	s := NewService()
	call := func(ctx context.Context, method string, input any) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	}
	if _, err := call(context.Background(), "describe", DescribeRequest{}); err == nil {
		t.Fatal("inactive admitted")
	}
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call(ctx, "compact", Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := call(context.Background(), "compact", Request{}); err == nil {
		t.Fatal("missing model binding accepted")
	}
	history := []providers.ChatMessage{{Role: "system", Content: "policy"}, {Role: "user", Content: "task", Seq: 1}, {Role: "assistant", Content: strings.Repeat("old history ", 3000), Seq: 2}}
	raw, err := call(context.Background(), "window", WindowRequest{Messages: history, HistoryHead: 2, TargetTokens: 5000})
	if err != nil {
		t.Fatal(err)
	}
	var result Replacement
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 3 || result.Messages[0].Content != "policy" || result.Messages[2].Content != "task" {
		t.Fatalf("invalid window: %+v", result)
	}
	if history[2].Content != strings.Repeat("old history ", 3000) {
		t.Fatal("proposal mutated transcript")
	}
	if _, err = call(context.Background(), "window", WindowRequest{Messages: history, HistoryHead: 2, TargetTokens: 1}); err == nil {
		t.Fatal("unfittable window accepted")
	}
	if _, err = call(context.Background(), "handoff", HandoffRequest{Messages: history, SourceSessionID: "synthetic", SourceThroughSeq: 2}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = call(context.Background(), "describe", DescribeRequest{}); err == nil {
		t.Fatal("closed admitted")
	}
}

func TestCompactionRejectsMissingResult(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"messages":null}`} {
		c := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		})
		if _, err := c.Compact(context.Background(), Request{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
