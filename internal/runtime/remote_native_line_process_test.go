package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeBarrierPolicy struct {
	path             string
	checks           atomic.Int32
	deny             atomic.Bool
	entered, release chan struct{}
}

func (p *nativeBarrierPolicy) Authorize(_ context.Context, req tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if req.Tool.Name == "read_file" && strings.Contains(req.Arguments, fmt.Sprintf(`"path":%q`, p.path)) {
		if p.checks.Add(1) == 2 {
			close(p.entered)
			<-p.release
		}
		if p.deny.Load() {
			return tools.AuthorizationDecision{Outcome: "deny", Reason: "concurrently revoked"}, nil
		}
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}

type nativeParentFailure struct{ agent.ToolExecutor }

func (f nativeParentFailure) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return f.ToolExecutor.(agent.RichToolExecutor).ExecuteResult(ctx, call)
}
func (f nativeParentFailure) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	return f.ToolExecutor.(agent.ToolMetadataProvider).ToolMetadata(call)
}
func (f nativeParentFailure) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) agent.ToolFinalization {
	out := agent.FinalizeToolOutcome(f.ToolExecutor, call, result)
	if call.Name == "run_code" {
		out.Result = toolresult.FromErrorText("controlled finalizer failure")
	}
	return out
}

func TestRemoteNativeLineTransactionsProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	content := []byte(strings.Repeat("native-data-", 100) + "\n")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "large.txt"), content, 0600))
	kit, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-pagination")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"pagination-fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: true}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("remote-pages", ""))
	thread, err := owner.NewThreadRuntime("remote-pages")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remote-pages", ExecutionID: "turn"})

	actor, _ := thread.Toolkit.ExecutionActor()
	remoteExec := thread.Toolkit.ExecutionEnvironment()
	prepared := remoteExec.(executionenv.PreparedReadExecutor)
	text := []byte("first\nsecond\nthird\n")
	makeFile := func(name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(remote, name), text, 0600))
	}
	cursor := func(name string) string {
		t.Helper()
		sum := sha256.Sum256(text)
		payload, err := json.Marshal(map[string]any{"path": name, "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(payload)})
		require.NoError(t, err)
		return string(args)
	}
	nativeReq := func(name string) executionenv.ToolRequest {
		return executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, name)}}
	}
	seed := func(name string) {
		t.Helper()
		_, err := remoteExec.Execute(ctx, nativeReq(name))
		require.NoError(t, err)
	}
	observed := func(name string) bool {
		t.Helper()
		ticket, err := prepared.PrepareUnobservedRead(ctx, nativeReq(name))
		require.NoError(t, err)
		changed, err := ticket.Commit(ctx)
		require.NoError(t, err)
		return changed
	}
	runCode := func(id, code string, executor agent.ToolExecutor) providers.ChatMessage {
		t.Helper()
		args, err := json.Marshal(map[string]string{"code": code, "description": "native line transaction"})
		require.NoError(t, err)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, Ledger: thread.StreamRunner.ToolLedger, OperationID: id, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	codeFor := func(name string) string {
		quoted, _ := json.Marshal(cursor(name))
		witness, err := json.Marshal("witness-" + name)
		require.NoError(t, err)
		return fmt.Sprintf(`{ const r=await tools.read_file(JSON.parse(%s)); if(r.structured_content.text!=="second\n") throw Error("wrong native line"); await tools.write_file({path:%s,content:r.structured_content.text}); console.log("LINE_READ",r.structured_content.text); }`, quoted, witness)
	}
	_ = os.Remove(filepath.Join(fixture, "native-line-witness.txt"))
	makeFile("failure.txt")
	seed("failure.txt")
	failed := runCode("failure", codeFor("failure.txt")+`throw Error("parent failure: second");`, thread.StreamRunner.Tools)
	require.True(t, failed.ToolResult.IsError)
	require.NotContains(t, failed.Content, "second")
	require.True(t, observed("failure.txt"), "failed parent keeps old observation")
	witness, err := os.ReadFile(filepath.Join(remote, "witness-failure.txt"))
	require.NoError(t, err)
	require.Equal(t, "second\n", string(witness))
	makeFile("success.txt")
	success := runCode("success", codeFor("success.txt"), thread.StreamRunner.Tools)
	require.False(t, success.ToolResult.IsError, success.Content)
	require.Contains(t, success.Content, "LINE_READ second")
	require.True(t, observed("success.txt"), "successful parent publishes observation")
	makeFile("finalizer.txt")
	seed("finalizer.txt")
	finalized := runCode("finalizer", codeFor("finalizer.txt"), nativeParentFailure{thread.StreamRunner.Tools})
	require.True(t, finalized.ToolResult.IsError)
	require.NotContains(t, finalized.Content, "second")
	require.True(t, observed("finalizer.txt"))
	makeFile("revoked.txt")
	seed("revoked.txt")
	var checks atomic.Int32
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"revoked.txt"`) && checks.Add(1) >= 2 {
			return fmt.Errorf("native authorization revoked")
		}
		return nil
	})
	revoked := runCode("revoked", codeFor("revoked.txt"), thread.StreamRunner.Tools)
	thread.Toolkit.SetPermissionRequestHook(nil)
	require.True(t, revoked.ToolResult.IsError)
	require.GreaterOrEqual(t, checks.Load(), int32(2))
	require.NotContains(t, revoked.Content, "second")
	require.True(t, observed("revoked.txt"))
	// A policy change after the worker read but before settlement must abort.
	makeFile("concurrent-revoke.txt")
	seed("concurrent-revoke.txt")
	barrier := &nativeBarrierPolicy{path: "concurrent-revoke.txt", entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer release()
	thread.Toolkit.SetAuthorizer(barrier)
	type asyncResult struct {
		messages []providers.ChatMessage
		err      error
	}
	asyncRun := func(id, code string) <-chan asyncResult {
		args, err := json.Marshal(map[string]string{"code": code, "description": "concurrent native line"})
		require.NoError(t, err)
		done := make(chan asyncResult, 1)
		go func() {
			run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: id, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
			messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
			run.Close()
			done <- asyncResult{messages, err}
		}()
		return done
	}
	done := asyncRun("concurrent-revoke", codeFor("concurrent-revoke.txt"))
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("native authorization did not pause")
	}
	barrier.deny.Store(true)
	release()
	var concurrent asyncResult
	select {
	case concurrent = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("revoked transaction did not finish")
	}
	thread.Toolkit.SetAuthorizer(nil)
	require.NoError(t, concurrent.err)
	require.Len(t, concurrent.messages, 1)
	require.True(t, concurrent.messages[0].ToolResult.IsError)
	require.NotContains(t, concurrent.messages[0].Content, "second")
	require.True(t, observed("concurrent-revoke.txt"))

	// A later same-actor native read during settlement wins over this earlier
	// prepared observation, even when both entries have the same file hash.
	makeFile("newer.txt")
	seed("newer.txt")
	newerBarrier := &nativeBarrierPolicy{path: "newer.txt", entered: make(chan struct{}), release: make(chan struct{})}
	var newerOnce sync.Once
	releaseNewer := func() { newerOnce.Do(func() { close(newerBarrier.release) }) }
	defer releaseNewer()
	thread.Toolkit.SetAuthorizer(newerBarrier)
	done = asyncRun("newer", codeFor("newer.txt"))
	select {
	case <-newerBarrier.entered:
	case <-time.After(10 * time.Second):
		releaseNewer()
		t.Fatal("native authorization did not pause")
	}
	newerReq := nativeReq("newer.txt")
	newerReq.Call.Arguments = `{"path":"newer.txt","offset":3,"limit":1}`
	_, err = remoteExec.Execute(ctx, newerReq)
	require.NoError(t, err)
	laterTicket, err := prepared.PrepareUnobservedRead(ctx, nativeReq("newer.txt"))
	require.NoError(t, err)
	releaseNewer()
	select {
	case concurrent = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("native transaction did not finish")
	}
	thread.Toolkit.SetAuthorizer(nil)
	require.NoError(t, concurrent.err)
	require.Len(t, concurrent.messages, 1)
	require.False(t, concurrent.messages[0].ToolResult.IsError)
	laterChanged, err := laterTicket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, laterChanged, "newer native observation must remain unchanged")

	// A later native read through another hard link must also win.
	makeFile("later-alias-original.txt")
	require.NoError(t, os.Link(filepath.Join(remote, "later-alias-original.txt"), filepath.Join(remote, "later-alias-other.txt")))
	seed("later-alias-original.txt")
	aliasBarrier := &nativeBarrierPolicy{path: "later-alias-original.txt", entered: make(chan struct{}), release: make(chan struct{})}
	var aliasOnce sync.Once
	releaseAlias := func() { aliasOnce.Do(func() { close(aliasBarrier.release) }) }
	defer releaseAlias()
	thread.Toolkit.SetAuthorizer(aliasBarrier)
	done = asyncRun("later-alias", codeFor("later-alias-original.txt"))
	select {
	case <-aliasBarrier.entered:
	case <-time.After(10 * time.Second):
		releaseAlias()
		t.Fatal("native alias authorization did not pause")
	}
	_, err = remoteExec.Execute(ctx, nativeReq("later-alias-other.txt"))
	require.NoError(t, err)
	aliasTicket, err := prepared.PrepareUnobservedRead(ctx, nativeReq("later-alias-other.txt"))
	require.NoError(t, err)
	releaseAlias()
	select {
	case concurrent = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("native alias transaction did not finish")
	}
	thread.Toolkit.SetAuthorizer(nil)
	require.NoError(t, concurrent.err)
	require.False(t, concurrent.messages[0].ToolResult.IsError)
	changed, err := aliasTicket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	remaining, err := prepared.PrepareUnobservedRead(ctx, nativeReq("later-alias-original.txt"))
	require.NoError(t, err)
	changed, err = remaining.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed, "older staged observation must not overwrite a later alias read")

	// Recheck again after the durable intent is staged, immediately before
	// the worker is allowed to commit the native observation.
	makeFile("stage-revoked.txt")
	seed("stage-revoked.txt")
	var stageRevoked atomic.Bool
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"stage-revoked.txt"`) && stageRevoked.Load() {
			return fmt.Errorf("native authorization revoked after stage")
		}
		return nil
	})
	stageLedger, err := toolledger.New(stageRevokeDocuments{Documents: owner.Documents, revoked: &stageRevoked}, "remote-pages")
	require.NoError(t, err)
	stageArgs, err := json.Marshal(map[string]string{"code": codeFor("stage-revoked.txt"), "description": "revoke after intent"})
	require.NoError(t, err)
	stageRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: stageLedger, OperationID: "stage-revoked", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	stageMessages, stageErr := stageRun.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "stage-revoked", Name: "run_code", Arguments: string(stageArgs)}}, nil)
	stageRun.Close()
	thread.Toolkit.SetPermissionRequestHook(nil)
	require.ErrorContains(t, stageErr, "native authorization revoked after stage")
	for _, message := range stageMessages {
		require.NotContains(t, message.Content, "second")
	}
	require.True(t, stageRevoked.Load())
	require.NoError(t, stageLedger.ValidateContinuation(ctx))
	require.True(t, observed("stage-revoked.txt"))

	makeFile("conflict.txt")
	seed("conflict.txt")
	conflict := runCode("conflict", codeFor("conflict.txt")+`await tools.read_file({path:"conflict.txt"});`, thread.StreamRunner.Tools)
	require.True(t, conflict.ToolResult.IsError)
	require.NotContains(t, conflict.Content, "second")
	require.True(t, observed("conflict.txt"), "conflicting batch makes no observation changes")
	pluginWitness, err := os.ReadFile(filepath.Join(fixture, "native-line-witness.txt"))
	require.NoError(t, err)
	require.Contains(t, string(pluginWitness), "conflict.txt")
	makeFile("native-conflict.txt")
	seed("native-conflict.txt")
	duplicate := runCode("duplicate-native", codeFor("native-conflict.txt")+codeFor("native-conflict.txt"), thread.StreamRunner.Tools)
	require.True(t, duplicate.ToolResult.IsError)
	require.NotContains(t, duplicate.Content, "second")
	require.True(t, observed("native-conflict.txt"), "duplicate native observations fail atomically")
	makeFile("alias-original.txt")
	require.NoError(t, os.Link(filepath.Join(remote, "alias-original.txt"), filepath.Join(remote, "alias-other.txt")))
	seed("alias-original.txt")
	alias := runCode("alias-conflict", codeFor("alias-original.txt")+`await tools.read_file({path:"alias-other.txt"});`, thread.StreamRunner.Tools)
	require.True(t, alias.ToolResult.IsError)
	require.NotContains(t, alias.Content, "second")
	require.True(t, observed("alias-original.txt"), "hard-link aliases conflict before any commit")
	pluginWitness, err = os.ReadFile(filepath.Join(fixture, "native-line-witness.txt"))
	require.NoError(t, err)
	require.Contains(t, string(pluginWitness), "alias-other.txt")
	makeFile("native-distinct.txt")
	makeFile("extension-distinct.txt")
	seed("extension-distinct.txt")
	composed := runCode("composed", codeFor("native-distinct.txt")+`await tools.read_file({path:"extension-distinct.txt"});`, thread.StreamRunner.Tools)
	require.False(t, composed.ToolResult.IsError, composed.Content)
	require.True(t, observed("native-distinct.txt"), "native observation is committed")
	require.False(t, observed("extension-distinct.txt"), "extension invalidates its prior native observation")
}
