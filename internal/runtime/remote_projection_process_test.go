package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"github.com/stretchr/testify/require"
)

type projectionPolicy struct {
	denied, pause    atomic.Bool
	entered, release chan struct{}
	once             sync.Once
}

type revokeOnCommittedObservation struct {
	persistence.Documents
	policy *projectionPolicy
	fired  atomic.Bool
	pause  bool
}

func (d *revokeOnCommittedObservation) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err != nil {
		return receipt, err
	}
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.Observation != nil && inv.Observation.Outcome == "committed" && d.fired.CompareAndSwap(false, true) {
			if d.pause {
				d.policy.pause.Store(true)
			} else {
				d.policy.denied.Store(true)
			}
		}
	}
	return receipt, nil
}

func (p *projectionPolicy) Authorize(context.Context, tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if p.pause.Load() {
		p.once.Do(func() { close(p.entered); <-p.release })
	}
	if p.denied.Load() {
		return tools.AuthorizationDecision{Outcome: "deny", Reason: "revoked"}, nil
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}

func TestRemoteProjectionRechecksCurrentAuthorityBeforeRealTurn(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	kit, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	_, err = owner.Sessions.CreateWithMetadata("projection", local)
	require.NoError(t, err)
	require.NoError(t, owner.PinExecutionEnvironment("projection", ""))
	thread, err := owner.NewThreadRuntime("projection")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("read_only")
	policy := &projectionPolicy{}
	thread.Toolkit.SetAuthorizer(policy)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "projection", ExecutionID: "turn"})
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "earlier", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "read", Name: "read_file", Arguments: `{"path":"shared.txt"}`}}, nil)
	run.Close()
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "REMOTE-ONLY")
	originalEnv := thread.Toolkit.ExecutionEnvironment()
	replacementEnv := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}, "replacement", "projection", t.TempDir())
	thread.Toolkit.SetExecutionEnvironment(replacementEnv)
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil)
	require.ErrorContains(t, err, "observation result unavailable")
	require.Zero(t, calls.Load())
	thread.Toolkit.SetExecutionEnvironment(originalEnv)
	require.NoError(t, replacementEnv.Close())
	policy.denied.Store(true)
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "observation result unavailable")
	require.Zero(t, calls.Load(), "no model request may contain the unprojected result")
	policy.denied.Store(false)
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil)
	require.NoError(t, err)
	require.Greater(t, calls.Load(), int32(0))

	// A revocation triggered by the final durable acknowledgement is checked
	// before the tool callback receives content, despite the committed effect.
	docs := &revokeOnCommittedObservation{Documents: testSessionDocuments(t, t.TempDir()), policy: policy}
	ledger, err := toolledger.New(docs, "projection")
	require.NoError(t, err)
	run = agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger, OperationID: "ack-revoke", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	var delivered atomic.Bool
	run.SetResultCallback(func(providers.ToolCall, toolresult.Result) { delivered.Store(true) })
	messages, err = run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "read-after-ack", Name: "read_file", Arguments: `{"path":"shared.txt"}`}}, nil)
	run.Close()
	require.True(t, docs.fired.Load())
	require.ErrorContains(t, err, "observation output authorization failed")
	require.Empty(t, messages)
	require.False(t, delivered.Load())
	pending, err := ledger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pending)
	policy.denied.Store(false)
	pending, err = ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Contains(t, pending[0].Result.TextProjection(), "REMOTE-ONLY")

	// Exercise a genuinely concurrent policy revocation while post-commit
	// authorization is paused, with no result or callback before it finishes.
	policy = &projectionPolicy{entered: make(chan struct{}), release: make(chan struct{})}
	thread.Toolkit.SetAuthorizer(policy)
	docs2 := &revokeOnCommittedObservation{Documents: testSessionDocuments(t, t.TempDir()), policy: policy, pause: true}
	ledger2, err := toolledger.New(docs2, "projection")
	require.NoError(t, err)
	run2 := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger2, OperationID: "concurrent-ack-revoke", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	var delivered2 atomic.Bool
	run2.SetResultCallback(func(providers.ToolCall, toolresult.Result) { delivered2.Store(true) })
	type asyncResult struct {
		messages []providers.ChatMessage
		err      error
	}
	done := make(chan asyncResult, 1)
	go func() {
		messages, err := run2.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "concurrent", Name: "read_file", Arguments: `{"path":"shared.txt"}`}}, nil)
		done <- asyncResult{messages, err}
	}()
	select {
	case <-policy.entered:
	case <-time.After(10 * time.Second):
		close(policy.release)
		run2.Close()
		t.Fatal("post-commit authorization did not pause")
	}
	require.False(t, delivered2.Load())
	policy.denied.Store(true)
	close(policy.release)
	var concurrent asyncResult
	select {
	case concurrent = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("post-commit authorization did not finish")
	}
	run2.Close()
	require.ErrorContains(t, concurrent.err, "observation output authorization failed")
	require.Empty(t, concurrent.messages)
	require.False(t, delivered2.Load())
	policy.pause.Store(false)
	policy.denied.Store(false)
	pending, err = ledger2.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
}
