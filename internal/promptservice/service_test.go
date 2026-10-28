package promptservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestSelectedPromptOwnsCompleteResult(t *testing.T) {
	calls := 0
	c := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		var in Request
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Fatal(err)
		}
		if method != "assemble" || in.CWD != "/synthetic" {
			t.Fatalf("wrong request: %s %+v", method, in)
		}
		return json.Marshal(Result{Content: "external-only", Sections: []Section{{Key: "external", Bytes: 13, Hash: "independent"}}})
	})
	got, err := c.Assemble(context.Background(), Request{Role: "main", CWD: "/synthetic"})
	if err != nil || got.Content != "external-only" || len(got.Sections) != 1 || calls != 1 {
		t.Fatalf("got %+v, %v, calls=%d", got, err, calls)
	}
	unavailable := errors.New("selected provider stopped")
	c = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, unavailable })
	if _, err = c.Assemble(context.Background(), Request{}); !errors.Is(err, unavailable) {
		t.Fatalf("lost error: %v", err)
	}
}

func TestPromptRejectsMissingResultAndInactiveProvider(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"content":null,"sections":[]}`} {
		c := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		})
		if _, err := c.Assemble(context.Background(), Request{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	s := NewService()
	raw, _ := json.Marshal(Request{Role: "main", CWD: "/synthetic", Date: "2026-01-01"})
	params := pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "assemble", Params: raw}
	if _, err := s.InvokeService(context.Background(), params); err == nil {
		t.Fatal("prepared provider admitted call")
	}
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeService(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeService(context.Background(), params); err == nil {
		t.Fatal("closed provider admitted call")
	}
}
