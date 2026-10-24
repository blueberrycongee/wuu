package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// The default binding must be observable, removable, and subject to the same
// callback lifetime as an external driver. Failed calls must retain output
// already accepted by the execution for final persistence.
func TestDefaultDriverComposition(t *testing.T) {
	for _, name := range []string{"success", "default-callback", "partial-failure", "cancel"} {
		fail := name == "partial-failure" || name == "cancel"
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home, state := testWorkspaceState(t, root)
			host, kernel, err := buildPluginHost(nil, root, "workspace", home, state, nil, startPluginClient, nil, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			owner, found := host.ServiceRegistry().ProviderPluginID("driver.default", loopdriver.DriverServiceMajor)
			if !found || owner != loopdriver.DefaultDriverID {
				t.Fatalf("default binding = %q, %v", owner, found)
			}
			client := &defaultBindingModel{fail: fail}
			runCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := &agent.StreamRunner{Client: client, Model: "controlled", LoopDriver: resolveLoopDriver("", host, kernel.driverGateways)}
			ctx := loopdriver.WithExecutionContext(runCtx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: name})
			if name == "success" {
				// Embedded and evaluation callers have no app-server turn ID.
				ctx = runCtx
			}
			deltas := 0
			callback := func(event providers.StreamEvent) {
				if event.Type == providers.EventContentDelta {
					deltas++
					if name == "cancel" {
						cancel()
					}
				}
			}
			if name == "default-callback" {
				runner.OnEvent = callback
				callback = nil
			}
			result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Say hello"}}, callback)
			if deltas != 1 {
				t.Fatalf("lost configured callback: deltas=%d", deltas)
			}
			if (err != nil) != fail || client.calls != 1 {
				t.Fatalf("calls=%d result=%+v err=%v", client.calls, result, err)
			}
			if len(result.NewMessages) != 1 || result.NewMessages[0].Content != "Hello" {
				t.Fatalf("lost observed history: %+v", result)
			}
			if name == "partial-failure" && !strings.Contains(err.Error(), "permanent fixture failure") {
				t.Fatalf("lost failure diagnostic: %v", err)
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost its classification: %v", err)
			}
			if err := host.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.RunWithCallback(ctx, nil, nil); err == nil || client.calls != 1 {
				t.Fatalf("closed driver ran a hidden loop: calls=%d err=%v", client.calls, err)
			}
		})
	}
	t.Run("removed", func(t *testing.T) {
		modules := append(testStateModules(), kernelHostModule)
		selection := &composition.Config{Version: composition.Version, Modules: &modules}
		root := t.TempDir()
		home, state := testWorkspaceState(t, root)
		host, kernel, err := buildPluginHost(nil, root, "workspace", home, state, nil, startPluginClient, nil, nil, selection, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		if err := host.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		client := &defaultBindingModel{}
		runner := &agent.StreamRunner{Client: client, Model: "controlled", LoopDriver: resolveLoopDriver("", host, kernel.driverGateways)}
		if _, err := runner.RunWithCallback(context.Background(), nil, nil); err == nil || client.calls != 0 {
			t.Fatalf("removed driver ran a hidden loop: calls=%d err=%v", client.calls, err)
		}
	})
}

type defaultBindingModel struct {
	fail  bool
	calls int
}

func (m *defaultBindingModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming call")
}

func (m *defaultBindingModel) StreamChat(_ context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls++
	request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "controlled", Transport: "memory", Mode: "stream"})
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Hello"}
	if m.fail {
		events <- providers.StreamEvent{Type: providers.EventError, Error: errors.New("permanent fixture failure")}
	} else {
		events <- providers.StreamEvent{Type: providers.EventDone}
	}
	close(events)
	return events, nil
}
