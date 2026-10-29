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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRemoteReadOnlyContinuationWorkerProcess(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run with a built execution worker")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(local, "shared.txt"), []byte("LOCAL-ONLY\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY needle\n"), 0600))
	env := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}, "remote-test", "thread", t.TempDir())
	defer env.Close()
	kit, err := tools.New(local)
	require.NoError(t, err)
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("worker", "/root/worker")
	kit.SetPermissionMode("read_only")
	kit.SetExecutionEnvironment(env)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, call := range []providers.ToolCall{
		{ID: "read", Name: "read_file", Arguments: `{"path":"shared.txt"}`},
		{ID: "grep", Name: "grep", Arguments: `{"pattern":"REMOTE-ONLY"}`},
	} {
		t.Run(call.Name, func(t *testing.T) {
			result, err := kit.ContinueRemoteReadOnly(ctx, kit.LookupTool(call.Name), call)
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Contains(t, result.TextProjection(), "REMOTE-ONLY")
			require.NotContains(t, result.TextProjection(), "LOCAL-ONLY")
		})
	}
}

func TestRemoteReadOnlyContinuationPluginProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance script")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(local, "shared.txt"), []byte("LOCAL-ONLY\n"), 0600))
	for _, name := range []string{"shared.txt", "forgery.txt", "duplicate.txt", "skip.txt", "pause.txt", "envpause.txt", "fail-after.txt", "bad-after.txt", "after-pause.txt", "cancel-after.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(remote, name), []byte("REMOTE-ONLY\n"), 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(remote, "image.bin"), []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, ".env"), []byte("SECRET=do-not-return\n"), 0600))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("OUTSIDE-SECRET\n"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(remote, "outside-link")))
	kit, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	modelRequests := make(chan []providers.ChatMessage, 4)
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		modelRequests <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		if modelCalls.Add(1) == 1 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"model-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"shared.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer model.Close()
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	_, err = owner.Sessions.CreateWithMetadata("remote-read", local)
	require.NoError(t, err)
	require.NoError(t, owner.PinExecutionEnvironment("remote-read", ""))
	thread, err := owner.NewThreadRuntime("remote-read")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remote-read", ExecutionID: "turn"})
	thread.Toolkit.SetPermissionMode("read_only")
	thread.Toolkit.SetGitAttributionEnabled(true)
	var directSequence atomic.Int64
	executeSettled := func(callCtx context.Context, call providers.ToolCall) (string, error) {
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: fmt.Sprintf("direct-%d", directSequence.Add(1)), RunContext: callCtx})
		defer run.Close()
		messages, err := run.ExecuteFinalCalls(callCtx, []providers.ToolCall{call}, nil)
		if err != nil {
			return "", err
		}
		if len(messages) != 1 || messages[0].ToolResult == nil {
			return "", fmt.Errorf("missing direct tool result")
		}
		text := messages[0].ToolResult.TextProjection()
		if messages[0].ToolResult.IsError {
			return text, fmt.Errorf("%s", text)
		}
		return text, nil
	}
	actor, _ := thread.Toolkit.ExecutionActor()
	nativeReq := executionenv.ToolRequest{Actor: actor, PermissionMode: "read_only", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, nativeReq)
	require.NoError(t, err)
	text, err := executeSettled(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.NoError(t, err)
	require.Contains(t, text, "plugin:REMOTE-ONLY")
	require.NotContains(t, text, "LOCAL-ONLY")
	require.Contains(t, text, `"source":"extension"`)
	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	check, err := remoteExec.PrepareUnobservedRead(ctx, nativeReq)
	require.NoError(t, err)
	changed, err := check.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed, "successful replacement should invalidate the captured native observation")
	for _, name := range []string{"fail-after.txt", "bad-after.txt"} {
		req := nativeReq
		req.Call.Arguments = fmt.Sprintf(`{"path":%q}`, name)
		_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
		require.NoError(t, err)
		_, err = executeSettled(ctx, providers.ToolCall{ID: name, Name: "read_file", Arguments: req.Call.Arguments})
		require.Error(t, err)
		check, err = remoteExec.PrepareUnobservedRead(ctx, req)
		require.NoError(t, err)
		changed, err = check.Commit(ctx)
		require.NoError(t, err)
		require.True(t, changed, "failed replacement must preserve native observation")
	}
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Read the file"}}, nil)
	require.NoError(t, err)
	<-modelRequests
	second := <-modelRequests
	var observed strings.Builder
	for _, message := range second {
		observed.WriteString(message.Content)
	}
	require.Contains(t, observed.String(), "plugin:REMOTE-ONLY")
	require.NotContains(t, observed.String(), "LOCAL-ONLY")
	for _, name := range []string{"forgery.txt", "duplicate.txt"} {
		text, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: name, Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, name)})
		require.NoError(t, err)
		require.Contains(t, text, "plugin:REMOTE-ONLY")
	}
	for _, raw := range []string{`{"path":"skip.txt"}`, `{"path":"missing.txt"}`, `{"path":"image.bin"}`, `{"path":".env"}`, `{"path":"outside-link"}`, `{"path":"../shared.txt"}`, `{"continuation":"forged"}`} {
		_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: raw, Name: "read_file", Arguments: raw})
		require.Error(t, err)
	}
	var checks int
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
		if info.Name == "read_file" {
			checks++
			if checks > 1 {
				return fmt.Errorf("revoked before remote worker")
			}
		}
		return nil
	})
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "revoked", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.ErrorContains(t, err, "revoked before remote worker")
	thread.Toolkit.SetPermissionRequestHook(nil)
	thread.Toolkit.DisableTools("read_file")
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "disabled", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.Error(t, err)
	thread.Toolkit.EnableTools("read_file")
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = thread.StreamRunner.Tools.Execute(canceled, providers.ToolCall{ID: "cancel", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.Error(t, err)
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(server.URL), 0600))
	for _, name := range []string{"after-pause.txt", "cancel-after.txt"} {
		req := nativeReq
		req.Call.Arguments = fmt.Sprintf(`{"path":%q}`, name)
		_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
		require.NoError(t, err)
		callCtx, stopCall := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, e := executeSettled(callCtx, providers.ToolCall{ID: name, Name: "read_file", Arguments: req.Call.Arguments})
			done <- e
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("plugin did not pause after worker read")
		}
		if name == "after-pause.txt" {
			_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
			require.NoError(t, err)
		} else {
			stopCall()
		}
		release <- struct{}{}
		callErr := <-done
		if name == "after-pause.txt" {
			require.NoError(t, callErr)
		} else {
			require.Error(t, callErr)
		}
		stopCall()
		check, err = remoteExec.PrepareUnobservedRead(ctx, req)
		require.NoError(t, err)
		changed, err = check.Commit(ctx)
		require.NoError(t, err)
		require.True(t, changed, "new native observation or canceled provider must survive")
	}
	otherRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(otherRoot, "pause.txt"), []byte("WRONG-ENVIRONMENT\n"), 0600))
	other := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: otherRoot}, "other-env", "remote-read", t.TempDir())
	defer other.Close()
	original := thread.Toolkit.ExecutionEnvironment()
	swappedResult := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, e := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "captured", Name: "read_file", Arguments: `{"path":"pause.txt"}`})
		swappedResult <- struct {
			text string
			err  error
		}{text, e}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause before environment change")
	}
	thread.Toolkit.SetExecutionEnvironment(other)
	release <- struct{}{}
	swap := <-swappedResult
	require.NoError(t, swap.err)
	require.Contains(t, swap.text, "plugin:REMOTE-ONLY")
	require.NotContains(t, swap.text, "WRONG-ENVIRONMENT")
	thread.Toolkit.SetExecutionEnvironment(original)

	envDone := make(chan error, 1)
	go func() {
		_, e := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "env-change", Name: "read_file", Arguments: `{"path":"envpause.txt"}`})
		envDone <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause before environment query")
	}
	thread.Toolkit.SetExecutionEnvironment(other)
	release <- struct{}{}
	require.Error(t, <-envDone)
	thread.Toolkit.SetExecutionEnvironment(original)

	oldActor, oldPath := thread.Toolkit.ExecutionActor()
	actorDone := make(chan error, 1)
	go func() {
		_, e := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "actor-change", Name: "read_file", Arguments: `{"path":"pause.txt"}`})
		actorDone <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause before actor change")
	}
	thread.Toolkit.SetAgentIdentity("other-actor", "/root/other")
	release <- struct{}{}
	require.Error(t, <-actorDone)
	thread.Toolkit.SetAgentIdentity(oldActor, oldPath)

	pendingCtx, cancelPending := context.WithCancel(ctx)
	pendingDone := make(chan error, 1)
	go func() {
		_, e := thread.StreamRunner.Tools.Execute(pendingCtx, providers.ToolCall{ID: "inflight-cancel", Name: "read_file", Arguments: `{"path":"pause.txt"}`})
		pendingDone <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause")
	}
	cancelPending()
	release <- struct{}{}
	require.Error(t, <-pendingDone)

	pendingDone = make(chan error, 1)
	go func() {
		_, e := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "inflight-retire", Name: "read_file", Arguments: `{"path":"pause.txt"}`})
		pendingDone <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause before retirement")
	}
	_, found := thread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("retired"))
	release <- struct{}{}
	require.Error(t, <-pendingDone)
	require.True(t, found)
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "retired", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.Error(t, err)

	cfg.PTC.Enabled = true
	ptcOwner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer ptcOwner.Cleanup()
	require.NoError(t, ptcOwner.PinExecutionEnvironment("remote-ptc", ""))
	ptcThread, err := ptcOwner.NewThreadRuntime("remote-ptc")
	require.NoError(t, err)
	defer ptcOwner.ReleasePluginGeneration(ptcThread.PluginGeneration)
	defer ptcThread.AgentControl.Close()
	ptcCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remote-ptc", ExecutionID: "turn"})
	rt := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: ptcThread.StreamRunner.Tools, RunContext: ptcCtx, Gate: agent.NewToolExecutionGate(1)})
	defer rt.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const x=await tools.read_file({path:"shared.txt"}); if(x.structured_content.text!=="plugin:REMOTE-ONLY\n" || x.structured_content.source!=="extension") throw Error("bad remote result"); console.log("REMOTE_PTC_OK")`, "description": "Remote read"})
	require.NoError(t, err)
	messages, err := rt.ExecuteFinalCalls(ptcCtx, []providers.ToolCall{{ID: "ptc", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "REMOTE_PTC_OK")

	example := plugin
	exampleRuntime := *plugin.Runtime
	example.Runtime = &exampleRuntime
	example.Runtime.Args = []string{"remote-text-reader.js"}
	examplePlugins := []pluginpkg.Plugin{example}
	exampleCfg := cfg
	exampleCfg.PTC.Enabled = false
	exampleOwner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: exampleCfg, DiscoveredPlugins: &examplePlugins, NonInteractive: true})
	require.NoError(t, err)
	defer exampleOwner.Cleanup()
	require.NoError(t, exampleOwner.PinExecutionEnvironment("example", ""))
	exampleThread, err := exampleOwner.NewThreadRuntime("example")
	require.NoError(t, err)
	defer exampleOwner.ReleasePluginGeneration(exampleThread.PluginGeneration)
	defer exampleThread.AgentControl.Close()
	exampleCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "example", ExecutionID: "turn"})
	exampleResult, err := exampleThread.StreamRunner.Tools.Execute(exampleCtx, providers.ToolCall{ID: "example", Name: "read_file", Arguments: `{"path":"shared.txt"}`})
	require.NoError(t, err)
	require.Contains(t, exampleResult, "[remote] REMOTE-ONLY")

	require.NoError(t, os.WriteFile(filepath.Join(fixture, "disable-remote"), []byte("1"), 0600))
	defer os.Remove(filepath.Join(fixture, "disable-remote"))
	unsupported, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer unsupported.Cleanup()
	require.NoError(t, unsupported.PinExecutionEnvironment("unsupported", ""))
	_, err = unsupported.NewThreadRuntime("unsupported")
	require.ErrorContains(t, err, "unavailable in a remote execution environment")
}

func TestRemoteReadOnlyContinuationWorkerActorModes(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote-read acceptance script")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("native\n"), 0600))
	env := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: root}, "actor-modes", "thread", t.TempDir())
	defer env.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}
	_, err := env.Execute(ctx, executionenv.ToolRequest{Actor: "a", PermissionMode: "standard", Call: call})
	require.NoError(t, err)
	_, err = env.ExecuteUnobservedRead(ctx, executionenv.ToolRequest{Actor: "a", PermissionMode: "read_only", Call: call})
	require.ErrorContains(t, err, "existing native read observation")
	_, err = env.ExecuteUnobservedRead(ctx, executionenv.ToolRequest{Actor: "a", PermissionMode: "standard", GitAttributionEnabled: true, Call: call})
	require.ErrorContains(t, err, "existing native read observation")
	_, err = env.ExecuteUnobservedRead(ctx, executionenv.ToolRequest{Actor: "b", PermissionMode: "read_only", Call: call})
	require.NoError(t, err)
}
