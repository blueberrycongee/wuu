package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type fusionTestClient struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
	reports  []string
	started  chan struct{}
	settled  chan struct{}
}

func (c *fusionTestClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming request")
}
func (c *fusionTestClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	var report string
	if len(c.reports) > 0 {
		report, c.reports = c.reports[0], c.reports[1:]
	}
	c.mu.Unlock()
	ch := make(chan providers.StreamEvent, 3)
	if report == "" {
		close(c.started)
		go func() {
			<-ctx.Done()
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
			close(ch)
			close(c.settled)
		}()
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: report}
		ch <- providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 4}}
		ch <- providers.StreamEvent{Type: providers.EventDone}
		close(ch)
	}
	return ch, nil
}

func newFusionTestRuntime(t *testing.T, root string, client providers.StreamClient) *ThreadRuntime {
	t.Helper()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	control, err := agentcontrol.New(agentcontrol.Config{
		Client: client, ProviderName: "sidekick-provider", DefaultModel: "sidekick-model",
		ParentRepo: root, SessionID: "fusion-test", WorktreeRoot: filepath.Join(root, "worktrees"),
		HistoryDir: filepath.Join(root, "workers"), ThreadDir: filepath.Join(root, "threads"), HarnessDir: filepath.Join(root, "harness"),
		WorkerFactory: func(workerRoot string, wt agentcontrol.WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
			worker, err := kit.CloneForRoot(workerRoot)
			if err == nil {
				worker.SetAgentIdentity(meta.ID, meta.Path)
				applyWorkerToolFilter(worker, wt)
			}
			return worker, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	control.SetProviderClientResolver(func(provider string) (providers.StreamClient, error) {
		if provider != "sidekick-provider" {
			return nil, errors.New("wrong restored provider")
		}
		return client, nil
	})
	control.StartQueuedWork()
	rt := &ThreadRuntime{Toolkit: kit, AgentControl: control}
	kit.SetFusionDelegate(rt.delegateFusion)
	t.Cleanup(func() { control.StopAll(); control.Close() })
	return rt
}

func TestFusionReusesSidekickContextAndRecoversReports(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	client := &fusionTestClient{reports: []string{
		`{"outcome":"blocked","summary":"Need a format choice","questions":["JSON or CSV?"]}`,
		`{"outcome":"completed","summary":"Implemented JSON","changed_files":["result.json"],"checks":["schema passed"]}`,
		`{"outcome":"needs_decision","summary":"Choose the follow-up scope"}`,
		`not a valid report`,
	}}
	rt := newFusionTestRuntime(t, root, client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var id string
	for i, outcome := range []string{"blocked", "completed", "needs_decision", "failed"} {
		if i == 2 {
			rt.AgentControl.Close()
			rt = newFusionTestRuntime(t, root, client)
		}
		brief := "Implement export with the unique marker first-brief"
		if i > 0 {
			brief = "Use JSON; keep the previous context and verify the schema"
		}
		args, _ := json.Marshal(map[string]string{"brief": brief})
		text, err := rt.Toolkit.Execute(ctx, providers.ToolCall{Name: "fusion_delegate", Arguments: string(args)})
		if err != nil {
			t.Fatal(err)
		}
		var result fusionResult
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatal(err)
		}
		if result.Outcome != outcome {
			t.Fatalf("handoff %d: %s", i, text)
		}
		if id == "" {
			id = result.AgentID
		}
		if result.AgentID != id {
			t.Fatal("created a replacement Sidekick")
		}
		if result.Provider != "sidekick-provider" || result.Model != "sidekick-model" {
			t.Fatalf("runtime drift: %+v", result)
		}
		if result.InputTokens != 10 || result.OutputTokens != 5 || result.CacheReadTokens != 4 {
			t.Fatalf("usage counted across delegations: %+v", result)
		}
		client.mu.Lock()
		req := client.requests[len(client.requests)-1]
		client.mu.Unlock()
		if req.Model != "sidekick-model" {
			t.Fatalf("wrong execution model: %s", req.Model)
		}
		for _, tool := range req.Tools {
			if tool.Name == "fusion_delegate" {
				t.Fatal("Sidekick can recursively delegate")
			}
		}
		if i > 0 {
			data, _ := json.Marshal(req.Messages)
			if !strings.Contains(string(data), "first-brief") || !strings.Contains(string(data), "Need a format choice") {
				t.Fatal("Sidekick lost prior context")
			}
		}
	}
}

func TestFusionCancellationDrainsSidekickBeforeReturning(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	client := &fusionTestClient{started: make(chan struct{}), settled: make(chan struct{})}
	rt := newFusionTestRuntime(t, t.TempDir(), client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := rt.Toolkit.Execute(ctx, providers.ToolCall{Name: "fusion_delegate", Arguments: `{"brief":"wait until cancelled"}`})
		done <- err
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Sidekick did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delegation did not cancel")
	}
	select {
	case <-client.settled:
	default:
		t.Fatal("returned while Sidekick still running")
	}
	for _, snap := range rt.AgentControl.List() {
		if !subagent.IsTerminal(snap.Status) {
			t.Fatalf("live Sidekick after cancellation: %s", snap.Status)
		}
	}
}

func TestFusionReadOnlySidekickCannotWrite(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	call := providers.ToolCall{ID: "attempt-write", Name: "write_file", Arguments: `{"path":"forbidden.txt","content":"should not be written"}`}
	client := &sessionRecordingClient{streamBatches: [][]providers.StreamEvent{
		{{Type: providers.EventToolUseStart, ToolCall: &call}, {Type: providers.EventToolUseEnd, ToolCall: &call}, {Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}},
		{{Type: providers.EventContentDelta, Content: `{"outcome":"blocked","summary":"Write denied"}`}, {Type: providers.EventDone}},
	}}
	rt := newFusionTestRuntime(t, root, client)
	rt.Toolkit.SetBoundary(tools.ReadOnlyBoundary())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := rt.Toolkit.Execute(ctx, providers.ToolCall{Name: "fusion_delegate", Arguments: `{"brief":"attempt a file change"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden.txt")); !os.IsNotExist(err) {
		t.Fatal("Sidekick bypassed read-only authority")
	}
	request := client.LastRequest()
	found := false
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, "read-only") {
			found = true
		}
	}
	if !found {
		t.Fatal("Sidekick did not receive the permission failure")
	}
}
