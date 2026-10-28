package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// The installed SDK fixture runs outside the repository; no internal imports or
// shared provider singleton participate. Both compositions use the same plugin
// identity but different implementations concurrently.
func TestSelectedLoopPolicyProcess(t *testing.T) {
	fixture := os.Getenv("WUU_POLICY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/loop-policy-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"stop", "continue"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			root := t.TempDir()
			home, state := testWorkspaceState(t, root)
			plugin := testRuntimePlugin("independent-policy")
			plugin.Root, plugin.Runtime.Command = fixture, node
			plugin.Runtime.Args = []string{mode + ".ts"}
			selection := &composition.Config{Version: composition.Version, Services: []composition.Binding{{Service: "agent.policy", Major: 1, Provider: plugin.ID}}}
			host, kernel, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, mode, home, state, nil, startPluginClient, nil, nil, selection, "")
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			model := &policyContinuationModel{}
			runner := &agent.StreamRunner{Client: model, Model: "controlled", LoopDriver: resolveLoopDriver("", host, kernel.driverGateways), SelectedPolicy: newSelectedLoopPolicyClient(host)}
			run := func(id string) (agent.LoopResult, error) {
				return runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: mode, ExecutionID: id}), []providers.ChatMessage{{Role: "user", Content: "test continuation"}}, nil)
			}
			result, err := run(mode + "-main")
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "continue" {
				expected = 2
			}
			if model.calls != expected || result.Content != "observed" {
				t.Fatalf("calls=%d result=%+v", model.calls, result)
			}
			// Child runner inherits the captured service route, not the other session's.
			child := cloneStreamRunnerForThread(runner, nil)
			model.calls = 0
			if _, err := child.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: mode + "-child", ExecutionID: mode + "-child"}), []providers.ChatMessage{{Role: "user", Content: "child"}}, nil); err != nil {
				t.Fatal(err)
			}
			if model.calls != expected {
				t.Fatalf("child used another policy: calls=%d", model.calls)
			}
			if err := host.Close(ctx); err != nil {
				t.Fatal(err)
			}
			before := model.calls
			if _, err := run(mode + "-removed"); err == nil || model.calls != before {
				t.Fatalf("removed provider fell back: calls=%d err=%v", model.calls, err)
			}
			t.Logf("public SDK policy %s: main and child model calls=%d each; isolated process and closed-provider rejection verified", mode, expected)
		})
	}
}

type policyContinuationModel struct{ calls int }

func (m *policyContinuationModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming model")
}
func (m *policyContinuationModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls++
	request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "controlled", Transport: "memory", Mode: "stream"})
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "observed"}
	reason := providers.FinishReasonContinue
	if m.calls > 1 {
		reason = providers.FinishReasonStop
	}
	events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: reason}
	close(events)
	return events, nil
}
