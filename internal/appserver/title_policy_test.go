package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/titleservice"
)

type directTitlePolicy struct {
	*titleservice.Service
	calls int
}

func (*directTitlePolicy) ID() string { return "fixture.title" }
func (p *directTitlePolicy) InvokeService(_ context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	p.calls++
	return json.Marshal(titleservice.Plan{Action: "complete", Title: "Provider-owned title without an inference runtime"})
}

// A selected policy can complete without configured model transport or the
// services needed to construct an overridden model. Neither may block plan.
func TestSelectedTitleCompletesWithoutModelRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	policy := &directTitlePolicy{Service: titleservice.NewService()}
	clients := []pluginhost.Client{policy, promptservice.NewService(), &embeddedCompactionFixture{compactionservice.NewService()}, &embeddedModelConsumer{}}
	host := pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	binding := &runtime.State{Sessions: rt.Sessions, Host: host}
	history := []providers.ChatMessage{{Role: "user", Content: "Question"}}
	result, err := srv.generateThreadTitleCore(binding, "title-only", history, false, false, true, nil)
	if err != nil || result.CleanedTitle != "Provider-owned title without an inference runtime" || result.RequestMessages != 0 {
		t.Fatalf("direct title requires inference: %+v, %v", result, err)
	}
	err = srv.regenerateTitleWithOverride(binding, context.Background(), Request{ID: json.RawMessage(`"override"`)}, "title-only", history, ThreadRegenerateTitleParams{DryRun: true, ModelOverride: "unconfigured-model"})
	if err != nil || policy.calls != 2 {
		t.Fatalf("override blocked policy: calls=%d error=%v output=%s", policy.calls, err, out.String())
	}
}
