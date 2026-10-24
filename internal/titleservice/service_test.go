package titleservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Failure cases: malformed policy replies, a removed provider, external title
// ownership, and default eligibility must not silently select another policy.
func TestSelectedTitlePolicyOwnsResultAndErrors(t *testing.T) {
	ctx := context.Background()
	calls := 0
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"action":"complete","title":"External title longer than the native forty character limit is preserved"}`), nil
	})
	plan, err := client.Plan(ctx, PlanRequest{})
	if err != nil || plan.Action != "complete" || len(plan.Title) <= 40 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	unavailable := errors.New("selected title unavailable")
	client = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, unavailable })
	if _, err := client.Plan(ctx, PlanRequest{}); !errors.Is(err, unavailable) {
		t.Fatalf("lost selected error: %v", err)
	}
	for _, reply := range []string{`{}`, `{"action":"infer"}`, `{"action":"complete","title":""}`, `null`} {
		client = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(reply), nil
		})
		if _, err := client.Plan(ctx, PlanRequest{}); err == nil {
			t.Fatalf("accepted malformed reply %s", reply)
		}
	}
}

func TestNativeTitlePolicyUsesPublicInvocation(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
		if method == "plan" && strings.Contains(string(result), `"action":"infer"`) {
			var wire struct {
				Messages []map[string]json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(result, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Messages) == 0 || string(wire.Messages[0]["role"]) != `"system"` {
				t.Fatalf("title policy did not return public model message wire: %s", result)
			}
		}
		return result, err
	})
	plan, err := client.Plan(ctx, PlanRequest{Model: "test", HumanMessages: []string{"Original question"}})
	if err != nil || plan.Action != "infer" || plan.Source != "Original question" || len(plan.Messages) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	plan, err = client.Plan(ctx, PlanRequest{Model: "test", HumanMessages: []string{"one", "two"}})
	if err != nil || plan.Action != "skip" {
		t.Fatalf("ineligible plan=%+v err=%v", plan, err)
	}
	plan, err = client.Plan(ctx, PlanRequest{Model: "test", HumanMessages: []string{"one", "two"}, Summary: "Persisted original"})
	if err != nil || plan.Source != "Persisted original" || plan.Action != "infer" {
		t.Fatalf("retry plan=%+v err=%v", plan, err)
	}
	plan, err = client.Plan(ctx, PlanRequest{Model: "test", Persist: true, Title: "Manual title", HumanMessages: []string{"one"}})
	if err != nil || plan.Action != "skip" {
		t.Fatalf("manual title replaced: %+v %v", plan, err)
	}
	title, err := client.Resolve(ctx, "<think>ignored</think>Title: \""+strings.Repeat("a", 60)+"\"")
	if err != nil || title != strings.Repeat("a", MaxRuneLength) {
		t.Fatalf("title=%q err=%v", title, err)
	}
	_ = service.Close(ctx)
	if _, err := client.Plan(ctx, PlanRequest{Model: "test", HumanMessages: []string{"one"}}); err == nil {
		t.Fatal("removed policy remained active")
	}
}
