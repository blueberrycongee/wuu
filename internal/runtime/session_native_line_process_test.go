package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

type sessionResolutionFailure struct {
	persistence.Documents
	after bool
	fired atomic.Bool
}

type sessionStageCancel struct {
	persistence.Documents
	cancel context.CancelFunc
	fired  atomic.Bool
}

type sessionParticipantPolicy struct {
	revoked atomic.Bool
	denials atomic.Int32
}

func (p *sessionParticipantPolicy) Authorize(_ context.Context, req tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if strings.Contains(req.Arguments, "multi-b.txt") && p.revoked.Load() {
		p.denials.Add(1)
		return tools.AuthorizationDecision{Outcome: "deny", Reason: "second participant revoked"}, nil
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}

type revokeSecondAfterCommit struct {
	persistence.Documents
	policy *sessionParticipantPolicy
}

func (d revokeSecondAfterCommit) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err != nil {
		return receipt, err
	}
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.Observation != nil && inv.Observation.Outcome == "committed" {
			d.policy.revoked.Store(true)
		}
	}
	return receipt, nil
}

func (d *sessionStageCancel) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err != nil {
		return receipt, err
	}
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.Observation != nil && inv.Observation.Outcome == "" && d.fired.CompareAndSwap(false, true) {
			d.cancel()
		}
	}
	return receipt, nil
}

func (d *sessionResolutionFailure) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	for _, w := range tx.Writes {
		var inv toolstate.Invocation
		if w.Namespace == toolstate.Invocations && json.Unmarshal(w.Value, &inv) == nil && inv.Observation != nil && inv.Observation.Outcome == "committed" && d.fired.CompareAndSwap(false, true) {
			if d.after {
				receipt, err := d.Documents.Commit(ctx, tx)
				if err != nil {
					return receipt, err
				}
				return receipt, fmt.Errorf("lost resolution acknowledgement")
			}
			return persistence.Receipt{}, fmt.Errorf("resolution unavailable")
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestRemoteSessionLineTransactionsProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
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
	content := []byte("first\nsecond\nthird\n")
	sum := sha256.Sum256(content)
	cursor := func(name string) string {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/" + name, "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
		require.NoError(t, err)
		return string(args)
	}
	makeFile := func(name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), name), content, 0600))
	}
	codeFor := func(name string) string {
		quoted, _ := json.Marshal(cursor(name))
		return fmt.Sprintf(`{const r=await tools.read_file(JSON.parse(%s)); if(r.structured_content.text!=="second\n") throw Error("wrong native content"); await tools.write_file({path:"witness-%s",content:r.structured_content.text}); console.log("LINE_READ",r.structured_content.text);}`, quoted, name)
	}
	runCode := func(id, code string, executor agent.ToolExecutor) providers.ChatMessage {
		t.Helper()
		args, err := json.Marshal(map[string]string{"code": code, "description": "session line"})
		require.NoError(t, err)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, Ledger: thread.StreamRunner.ToolLedger, OperationID: id, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	makeFile("failed.txt")
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "session-without-ledger", Name: "read_file", Arguments: cursor("failed.txt")})
	require.ErrorContains(t, err, "durable tool ledger required")
	failed := runCode("session-failed", codeFor("failed.txt")+`throw Error("failed parent");`, thread.StreamRunner.Tools)
	require.True(t, failed.ToolResult.IsError)
	require.NotContains(t, failed.Content, "second")
	w, err := os.ReadFile(filepath.Join(remote, "witness-failed.txt"))
	require.NoError(t, err)
	require.Equal(t, "second\n", string(w))
	_, present := thread.Toolkit.ActiveFilesContextBlock()
	require.False(t, present, "failed parent must not publish host observation")
	traversal := runCode("session-traversal", codeFor("../escape.txt"), thread.StreamRunner.Tools)
	require.True(t, traversal.ToolResult.IsError)
	makeFile("stale.txt")
	require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), "stale.txt"), []byte("changed\n"), 0600))
	stale := runCode("session-stale", codeFor("stale.txt"), thread.StreamRunner.Tools)
	require.True(t, stale.ToolResult.IsError)
	outside := filepath.Join(remote, "outside.txt")
	require.NoError(t, os.WriteFile(outside, content, 0600))
	if os.Symlink(outside, filepath.Join(thread.Toolkit.SessionDir(), "outside-link.txt")) == nil {
		link := runCode("session-link", codeFor("outside-link.txt"), thread.StreamRunner.Tools)
		require.True(t, link.ToolResult.IsError)
		require.NotContains(t, link.Content, "second")
	}
	_, present = thread.Toolkit.ActiveFilesContextBlock()
	require.False(t, present)
	makeFile("success.txt")
	success := runCode("session-success", codeFor("success.txt"), thread.StreamRunner.Tools)
	require.False(t, success.ToolResult.IsError, success.Content)
	require.Contains(t, success.Content, "second")
	block, present := thread.Toolkit.ActiveFilesContextBlock()
	require.True(t, present)
	require.Contains(t, block.Content, "current=1")
	makeFile("finalizer.txt")
	finalized := runCode("session-finalizer", codeFor("finalizer.txt"), nativeParentFailure{thread.StreamRunner.Tools})
	require.True(t, finalized.ToolResult.IsError)
	require.NotContains(t, finalized.Content, "second")
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=1")
	makeFile("revoked.txt")
	checks := 0
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, "revoked.txt") {
			checks++
			if checks >= 2 {
				return fmt.Errorf("revoked")
			}
		}
		return nil
	})
	revoked := runCode("session-revoked", codeFor("revoked.txt"), thread.StreamRunner.Tools)
	thread.Toolkit.SetPermissionRequestHook(nil)
	require.True(t, revoked.ToolResult.IsError)
	require.NotContains(t, revoked.Content, "second")
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=1")
	makeFile("concurrent-revoke.txt")
	barrier := &nativeBarrierPolicy{path: "$SESSION_DIR/concurrent-revoke.txt", entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer release()
	thread.Toolkit.SetAuthorizer(barrier)
	type asyncResult struct {
		messages []providers.ChatMessage
		err      error
	}
	done := make(chan asyncResult, 1)
	go func() {
		args, _ := json.Marshal(map[string]string{"code": codeFor("concurrent-revoke.txt"), "description": "concurrent session revocation"})
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "session-concurrent", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "session-concurrent", Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		done <- asyncResult{messages, err}
	}()
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("post-read authorization did not pause")
	}
	barrier.deny.Store(true)
	release()
	var deniedResult asyncResult
	select {
	case deniedResult = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent session read did not finish")
	}
	thread.Toolkit.SetAuthorizer(nil)
	require.NoError(t, deniedResult.err)
	require.Len(t, deniedResult.messages, 1)
	require.True(t, deniedResult.messages[0].ToolResult.IsError)
	require.NotContains(t, deniedResult.messages[0].Content, "second")
	_, err = os.Stat(filepath.Join(remote, "witness-concurrent-revoke.txt"))
	require.True(t, os.IsNotExist(err))
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=1")
	makeFile("mix.txt")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "extension-distinct.txt"), content, 0600))
	_ = os.Remove(filepath.Join(fixture, "native-line-witness.txt"))
	mixed := runCode("session-mixed", codeFor("mix.txt")+`await tools.read_file({path:"extension-distinct.txt"});`, thread.StreamRunner.Tools)
	require.True(t, mixed.ToolResult.IsError)
	require.NotContains(t, mixed.Content, "second")
	pluginWitness, err := os.ReadFile(filepath.Join(fixture, "native-line-witness.txt"))
	require.NoError(t, err)
	require.Contains(t, string(pluginWitness), "extension-distinct.txt")
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=1")
	makeFile("other-a.txt")
	makeFile("other-b.txt")
	both := runCode("session-both", codeFor("other-a.txt")+codeFor("other-b.txt"), thread.StreamRunner.Tools)
	require.False(t, both.ToolResult.IsError, both.Content)
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=3")
	conflict := runCode("session-conflict", codeFor("other-a.txt")+codeFor("other-a.txt"), thread.StreamRunner.Tools)
	require.True(t, conflict.ToolResult.IsError)
	block, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Contains(t, block.Content, "current=3")
	if os.Link(filepath.Join(thread.Toolkit.SessionDir(), "other-a.txt"), filepath.Join(thread.Toolkit.SessionDir(), "alias.txt")) == nil {
		aliasConflict := runCode("session-alias-conflict", codeFor("other-a.txt")+codeFor("alias.txt"), thread.StreamRunner.Tools)
		require.True(t, aliasConflict.ToolResult.IsError)
		require.NotContains(t, aliasConflict.Content, "second")
		block, _ = thread.Toolkit.ActiveFilesContextBlock()
		require.Contains(t, block.Content, "current=3")
	}
	for _, after := range []bool{false, true} {
		name := fmt.Sprintf("resolution-%t.txt", after)
		makeFile(name)
		docs := &sessionResolutionFailure{Documents: testSessionDocuments(t, t.TempDir()), after: after}
		ledger, err := toolledger.New(docs, "remote-pages")
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"code": codeFor(name), "description": "lost session resolution"})
		require.NoError(t, err)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger, OperationID: name, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		messages, runErr := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: name, Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		require.ErrorContains(t, runErr, "observation settlement outcome unknown")
		require.Empty(t, messages)
		require.True(t, docs.fired.Load())
		if !after {
			require.Error(t, ledger.ValidateContinuation(ctx))
		}
		require.NoError(t, ledger.Reconcile(ctx))
		require.NoError(t, executionenv.RecoverLedgerObservations(ctx, ledger, thread.Toolkit.ObservationRecoverer()))
		require.NoError(t, ledger.ValidateContinuation(ctx))
		projection, err := ledger.PendingProjection(ctx)
		require.NoError(t, err)
		require.Len(t, projection, 1)
		require.Contains(t, projection[0].Result.TextProjection(), "second")
	}
	makeFile("post-stage-revoke.txt")
	before, _ := thread.Toolkit.ActiveFilesContextBlock()
	var denied atomic.Bool
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, "post-stage-revoke.txt") && denied.Load() {
			return fmt.Errorf("session authorization revoked after stage")
		}
		return nil
	})
	stageDocs := stageRevokeDocuments{Documents: testSessionDocuments(t, t.TempDir()), revoked: &denied}
	stageLedger, err := toolledger.New(stageDocs, "remote-pages")
	require.NoError(t, err)
	stageArgs, err := json.Marshal(map[string]string{"code": codeFor("post-stage-revoke.txt"), "description": "revoke after stage"})
	require.NoError(t, err)
	stageRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: stageLedger, OperationID: "session-post-stage", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	stageMessages, stageErr := stageRun.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "stage", Name: "run_code", Arguments: string(stageArgs)}}, nil)
	stageRun.Close()
	thread.Toolkit.SetPermissionRequestHook(nil)
	require.ErrorContains(t, stageErr, "session authorization revoked after stage")
	require.Empty(t, stageMessages)
	require.True(t, denied.Load())
	require.NoError(t, stageLedger.ValidateContinuation(ctx))
	afterBlock, _ := thread.Toolkit.ActiveFilesContextBlock()
	require.Equal(t, before.Content, afterBlock.Content)
	makeFile("cancel.txt")
	cancelCtx, stop := context.WithCancel(ctx)
	cancelDocs := &sessionStageCancel{Documents: testSessionDocuments(t, t.TempDir()), cancel: stop}
	cancelLedger, err := toolledger.New(cancelDocs, "remote-pages")
	require.NoError(t, err)
	cancelArgs, err := json.Marshal(map[string]string{"code": codeFor("cancel.txt"), "description": "cancel after stage"})
	require.NoError(t, err)
	cancelRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: cancelLedger, OperationID: "session-cancel", RunContext: cancelCtx, Gate: agent.NewToolExecutionGate(1)})
	cancelMessages, cancelErr := cancelRun.ExecuteFinalCalls(cancelCtx, []providers.ToolCall{{ID: "cancel", Name: "run_code", Arguments: string(cancelArgs)}}, nil)
	cancelRun.Close()
	stop()
	require.Error(t, cancelErr)
	require.Empty(t, cancelMessages)
	require.True(t, cancelDocs.fired.Load())
	require.NoError(t, cancelLedger.ValidateContinuation(ctx))
	afterBlock, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Equal(t, before.Content, afterBlock.Content)
	archiveName := strings.Repeat("a", 64) + ".txt"
	require.NoError(t, os.MkdirAll(filepath.Join(thread.Toolkit.SessionDir(), "tool-results"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), "tool-results", archiveName), content, 0600))
	archiveToken, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/tool-results/" + archiveName, "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum), "result_source": "extension"})
	require.NoError(t, err)
	archiveArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(archiveToken)})
	require.NoError(t, err)
	quoted, _ := json.Marshal(string(archiveArgs))
	archive := runCode("session-archive", fmt.Sprintf(`{const r=await tools.read_file(JSON.parse(%s));console.log(r.structured_content.source,r.structured_content.content_verified);}`, quoted), thread.StreamRunner.Tools)
	require.False(t, archive.ToolResult.IsError, archive.Content)
	require.Contains(t, archive.Content, "tool_result_archive false")
	require.NotContains(t, archive.Content, "extension true")
	makeFile("multi-a.txt")
	makeFile("multi-b.txt")
	multiPolicy := &sessionParticipantPolicy{}
	thread.Toolkit.SetAuthorizer(multiPolicy)
	multiLedger, err := toolledger.New(revokeSecondAfterCommit{Documents: testSessionDocuments(t, t.TempDir()), policy: multiPolicy}, "remote-pages")
	require.NoError(t, err)
	multiArgs, err := json.Marshal(map[string]string{"code": codeFor("multi-a.txt") + codeFor("multi-b.txt"), "description": "second participant revoked"})
	require.NoError(t, err)
	multiRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: multiLedger, OperationID: "session-multi-revoke", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	var multiDelivered atomic.Bool
	multiRun.SetResultCallback(func(providers.ToolCall, toolresult.Result) { multiDelivered.Store(true) })
	multiMessages, multiErr := multiRun.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "multi", Name: "run_code", Arguments: string(multiArgs)}}, nil)
	multiRun.Close()
	require.ErrorContains(t, multiErr, "observation output authorization failed")
	require.Empty(t, multiMessages)
	require.False(t, multiDelivered.Load())
	require.Greater(t, multiPolicy.denials.Load(), int32(0))
	for _, name := range []string{"multi-a.txt", "multi-b.txt"} {
		_, err := os.Stat(filepath.Join(remote, "witness-"+name))
		require.NoError(t, err)
	}
	pendingMulti, err := multiLedger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pendingMulti)
	multiPolicy.revoked.Store(false)
	pendingMulti, err = multiLedger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pendingMulti, 1)
	thread.Toolkit.SetAuthorizer(nil)
	// Legacy cursors are not session-authenticated: the current session root
	// and its current authority decide what the restored path can access.
	require.NoError(t, owner.PinExecutionEnvironment("other-session", ""))
	other, err := owner.NewThreadRuntime("other-session")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(other.PluginGeneration)
	defer other.AgentControl.Close()
	other.Toolkit.SetPermissionMode("standard")
	otherCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "other-session", ExecutionID: "turn"})
	otherCode := func(id string) providers.ChatMessage {
		t.Helper()
		args, err := json.Marshal(map[string]string{"code": codeFor("success.txt"), "description": "other session"})
		require.NoError(t, err)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: other.StreamRunner.Tools, Ledger: other.StreamRunner.ToolLedger, OperationID: id, RunContext: otherCtx, Gate: agent.NewToolExecutionGate(1)})
		messages, err := run.ExecuteFinalCalls(otherCtx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	foreign := otherCode("foreign-missing")
	require.True(t, foreign.ToolResult.IsError)
	require.NotContains(t, foreign.Content, "second")
	require.NoError(t, os.WriteFile(filepath.Join(other.Toolkit.SessionDir(), "success.txt"), content, 0600))
	foreign = otherCode("foreign-present")
	require.False(t, foreign.ToolResult.IsError, foreign.Content)
	require.Contains(t, foreign.Content, "second")
	for _, managed := range []bool{false, true} {
		for _, ptc := range []bool{false, true} {
			t.Run(fmt.Sprintf("byte-managed=%v-ptc=%v", managed, ptc), func(t *testing.T) {
				name := fmt.Sprintf("byte-%v-%v.txt", managed, ptc)
				path := name
				if managed {
					path = "$SESSION_DIR/" + name
					require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), name), content, 0600))
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(remote, name), content, 0600))
				}
				raw, err := json.Marshal(map[string]any{"path": path, "byte_offset": 0, "byte_end_offset": len(content), "limit": len(content), "expected_sha256": fmt.Sprintf("%x", sum)})
				require.NoError(t, err)
				args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
				require.NoError(t, err)
				ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "remote-pages")
				require.NoError(t, err)
				policy := &projectionPolicy{}
				thread.Toolkit.SetAuthorizer(policy)
				defer thread.Toolkit.SetAuthorizer(nil)
				call := providers.ToolCall{ID: name, Name: "read_file", Arguments: string(args)}
				if ptc {
					quoted, _ := json.Marshal(string(args))
					code := fmt.Sprintf(`const r=await tools.read_file(JSON.parse(%s));console.log("BYTE_VALUE",r.content[0].text);`, quoted)
					encoded, err := json.Marshal(map[string]string{"code": code, "description": "native byte"})
					require.NoError(t, err)
					call = providers.ToolCall{ID: name, Name: "run_code", Arguments: string(encoded)}
				}
				run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger, OperationID: name, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
				messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
				run.Close()
				require.NoError(t, err)
				require.Len(t, messages, 1)
				require.False(t, messages[0].ToolResult.IsError)
				require.Contains(t, messages[0].Content, "first")
				policy.denied.Store(true)
				pending, err := ledger.PendingProjection(ctx)
				require.Error(t, err, "current native permission must gate unprojected byte content")
				require.Empty(t, pending)
				policy.denied.Store(false)
				pending, err = ledger.PendingProjection(ctx)
				require.NoError(t, err)
				require.Len(t, pending, 1)
			})
		}
	}
	// Authorization-only byte reads can share a host batch with session line
	// observations. A remote native line has a separate durable owner and is
	// rejected before either observation can commit.
	makeFile("byte-mixed-session.txt")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "byte-mixed-workspace.txt"), content, 0600))
	byteCode := func(path string) string {
		raw, err := json.Marshal(map[string]any{"path": path, "byte_offset": 0, "byte_end_offset": len(content), "limit": len(content), "expected_sha256": fmt.Sprintf("%x", sum)})
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
		require.NoError(t, err)
		quoted, _ := json.Marshal(string(args))
		return fmt.Sprintf(`{const r=await tools.read_file(JSON.parse(%s));console.log("mixed-byte",r.content[0].text);}`, quoted)
	}
	for _, after := range []bool{false, true} {
		docs := &sessionResolutionFailure{Documents: testSessionDocuments(t, t.TempDir()), after: after}
		ledger, err := toolledger.New(docs, "remote-pages")
		require.NoError(t, err)
		policy := &projectionPolicy{}
		thread.Toolkit.SetAuthorizer(policy)
		args, err := json.Marshal(map[string]string{"code": byteCode("byte-mixed-workspace.txt"), "description": "byte resolution acknowledgement"})
		require.NoError(t, err)
		id := fmt.Sprintf("byte-resolution-%v", after)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger, OperationID: id, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		messages, runErr := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
		run.Close()
		require.ErrorContains(t, runErr, "observation settlement outcome unknown")
		require.Empty(t, messages)
		require.True(t, docs.fired.Load())
		require.NoError(t, ledger.Reconcile(ctx))
		require.NoError(t, executionenv.RecoverLedgerObservations(ctx, ledger, thread.Toolkit.ObservationRecoverer()))
		policy.denied.Store(true)
		projection, err := ledger.PendingProjection(ctx)
		require.Error(t, err)
		require.Empty(t, projection)
		policy.denied.Store(false)
		projection, err = ledger.PendingProjection(ctx)
		require.NoError(t, err)
		require.Len(t, projection, 1)
		require.Contains(t, projection[0].Result.TextProjection(), "mixed-byte")
		thread.Toolkit.SetAuthorizer(nil)
	}
	beforeMixed, _ := thread.Toolkit.ActiveFilesContextBlock()
	byteOnly := runCode("byte-only-combined", byteCode("byte-mixed-workspace.txt")+byteCode("$SESSION_DIR/byte-mixed-session.txt"), thread.StreamRunner.Tools)
	require.False(t, byteOnly.ToolResult.IsError, byteOnly.Content)
	unchanged, _ := thread.Toolkit.ActiveFilesContextBlock()
	require.Equal(t, beforeMixed.Content, unchanged.Content, "byte reads must not modify freshness")
	byteFailure := runCode("byte-parent-failure", byteCode("byte-mixed-workspace.txt")+`throw Error("parent failed");`, thread.StreamRunner.Tools)
	require.True(t, byteFailure.ToolResult.IsError)
	require.NotContains(t, byteFailure.Content, "mixed-byte")
	unchanged, _ = thread.Toolkit.ActiveFilesContextBlock()
	require.Equal(t, beforeMixed.Content, unchanged.Content)
	combined := runCode("byte-session-combined", byteCode("byte-mixed-workspace.txt")+byteCode("$SESSION_DIR/byte-mixed-session.txt")+codeFor("byte-mixed-session.txt"), thread.StreamRunner.Tools)
	require.False(t, combined.ToolResult.IsError, combined.Content)
	require.Contains(t, combined.Content, "mixed-byte")
	afterMixed, _ := thread.Toolkit.ActiveFilesContextBlock()
	require.NotEqual(t, beforeMixed.Content, afterMixed.Content, "session line must commit")
	require.NotContains(t, afterMixed.Content, "byte-mixed-workspace.txt", "byte read must not create freshness")
	remoteLineRaw, err := json.Marshal(map[string]any{"path": "byte-mixed-workspace.txt", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
	require.NoError(t, err)
	remoteLineArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(remoteLineRaw)})
	require.NoError(t, err)
	remoteLineQuoted, _ := json.Marshal(string(remoteLineArgs))
	remoteLineCode := fmt.Sprintf(`{const r=await tools.read_file(JSON.parse(%s));console.log("remote-line",r.content[0].text);}`, remoteLineQuoted)
	conflicting := runCode("byte-remote-combined", byteCode("byte-mixed-workspace.txt")+remoteLineCode, thread.StreamRunner.Tools)
	require.True(t, conflicting.ToolResult.IsError)
	require.NotContains(t, conflicting.Content, "mixed-byte")
	require.NotContains(t, conflicting.Content, "remote-line")
	afterConflict, _ := thread.Toolkit.ActiveFilesContextBlock()
	require.Equal(t, afterMixed.Content, afterConflict.Content)

}
