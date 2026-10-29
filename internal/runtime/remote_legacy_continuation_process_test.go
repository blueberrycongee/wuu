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
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

type legacyNativePolicy struct {
	actor, root string
	seen        atomic.Bool
	revoked     atomic.Bool
}

func (p *legacyNativePolicy) Authorize(_ context.Context, req tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if strings.Contains(req.Arguments, `"path":"large.txt"`) {
		if p.revoked.Load() {
			return tools.AuthorizationDecision{Outcome: "deny", Reason: "concurrently revoked"}, nil
		}
		if req.ActorID != p.actor || req.CWD != p.root || req.Tool.Name != "read_file" || req.Tool.Kind != tools.ToolKindFile {
			return tools.AuthorizationDecision{Outcome: "deny", Reason: "incorrect native authority"}, nil
		}
		p.seen.Store(true)
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}

type legacyPauseExecutor struct {
	executionenv.Executor
	entered chan struct{}
	release chan struct{}
}

func (e *legacyPauseExecutor) Root() string { return e.Executor.(interface{ Root() string }).Root() }
func (e *legacyPauseExecutor) Backend() string {
	return e.Executor.(interface{ Backend() string }).Backend()
}
func (e *legacyPauseExecutor) Identity() string {
	return e.Executor.(interface{ Identity() string }).Identity()
}
func (e *legacyPauseExecutor) Execute(ctx context.Context, request executionenv.ToolRequest) (toolresult.Result, error) {
	result, err := e.Executor.Execute(ctx, request)
	close(e.entered)
	<-e.release
	return result, err
}

func TestRemoteLegacyNativeContinuationProcess(t *testing.T) {
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
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: false}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
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
	original := thread.Toolkit.ExecutionEnvironment()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"large.txt","byte_range":{"offset":0,"limit":20}}`}}
	prior := req
	prior.Call.Arguments = `{"path":"large.txt"}`
	_, err = original.Execute(ctx, prior)
	require.NoError(t, err)
	page, err := original.Execute(ctx, req)
	require.NoError(t, err)
	var native map[string]any
	require.NoError(t, json.Unmarshal([]byte(page.TextProjection()), &native))
	next, err := json.Marshal(native["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	legacy, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "legacy", Name: "read_file", Arguments: string(next)})
	require.NoError(t, err)
	var legacyPage map[string]any
	require.NoError(t, json.Unmarshal([]byte(legacy.TextProjection()), &legacyPage))
	require.Equal(t, "read_bytes", legacyPage["action"])
	require.Equal(t, string(content[20:40]), legacyPage["content"])
	require.Nil(t, legacyPage["source"], "legacy path remains native")
	policy := &legacyNativePolicy{actor: actor, root: remote}
	thread.Toolkit.SetAuthorizer(policy)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "native-policy", Name: "read_file", Arguments: string(next)})
	require.NoError(t, err)
	require.True(t, policy.seen.Load(), "native policy receives the restored path, actor and remote root")
	thread.Toolkit.SetAuthorizer(nil)
	// The token is only base64 JSON: a caller can construct an equivalent valid
	// cursor for another readable file without supplying a SHA-256.
	require.NoError(t, os.WriteFile(filepath.Join(remote, "forged.txt"), content, 0600))
	var nextArgs map[string]string
	require.NoError(t, json.Unmarshal(next, &nextArgs))
	decoded, err := base64.RawURLEncoding.DecodeString(nextArgs["continuation"])
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(decoded, &fields))
	fields["path"] = "forged.txt"
	// A forged cursor with a real digest still cannot bypass current native policy.
	forgedPayload, err := json.Marshal(fields)
	require.NoError(t, err)
	forgedArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(forgedPayload)})
	require.NoError(t, err)
	forged, err := original.Execute(ctx, executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: string(forgedArgs)}})
	require.NoError(t, err)
	require.False(t, forged.IsError)
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"forged.txt"`) {
			return fmt.Errorf("forged path denied")
		}
		return nil
	})
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "forged", Name: "read_file", Arguments: string(forgedArgs)})
	require.ErrorContains(t, err, "forged path denied")
	thread.Toolkit.SetPermissionRequestHook(nil)
	delete(fields, "expected_sha256")
	missingDigest, err := json.Marshal(fields)
	require.NoError(t, err)
	missingArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(missingDigest)})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "no-digest", Name: "read_file", Arguments: string(missingArgs)})
	require.ErrorContains(t, err, "snapshot digest")
	fields["path"] = "../outside.txt"
	sha := sha256.Sum256(content)
	fields["expected_sha256"] = fmt.Sprintf("%x", sha)
	escaping, err := json.Marshal(fields)
	require.NoError(t, err)
	escapeArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(escaping)})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "escape", Name: "read_file", Arguments: string(escapeArgs)})
	require.ErrorContains(t, err, "relative workspace path")
	fields["path"] = "C:/outside.txt"
	windowsPath, err := json.Marshal(fields)
	require.NoError(t, err)
	windowsArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(windowsPath)})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "windows-escape", Name: "read_file", Arguments: string(windowsArgs)})
	require.Error(t, err)
	secret := []byte("token=private\n")
	require.NoError(t, os.WriteFile(filepath.Join(remote, ".env"), secret, 0600))
	secretSHA := sha256.Sum256(secret)
	fields["path"], fields["expected_sha256"] = ".env", fmt.Sprintf("%x", secretSHA)
	secretCursor, err := json.Marshal(fields)
	require.NoError(t, err)
	secretArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(secretCursor)})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "sensitive", Name: "read_file", Arguments: string(secretArgs)})
	require.Error(t, err, "worker must retain native sensitive-path checks")
	var checks atomic.Int32
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"large.txt"`) && checks.Add(1) == 2 {
			return fmt.Errorf("revoked after read")
		}
		return nil
	})
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "revoked", Name: "read_file", Arguments: string(next)})
	require.ErrorContains(t, err, "revoked after read")
	thread.Toolkit.SetPermissionRequestHook(nil)
	// Another session must still authorize the restored native path for its own actor.
	require.NoError(t, owner.PinExecutionEnvironment("other-legacy", ""))
	foreignThread, err := owner.NewThreadRuntime("other-legacy")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(foreignThread.PluginGeneration)
	defer foreignThread.AgentControl.Close()
	foreignThread.Toolkit.SetPermissionMode("standard")
	foreignThread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"large.txt"`) {
			return fmt.Errorf("foreign actor denied")
		}
		return nil
	})
	foreignCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "other-legacy", ExecutionID: "turn"})
	foreignRich, ok := foreignThread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	_, err = foreignRich.ExecuteResult(foreignCtx, providers.ToolCall{ID: "foreign", Name: "read_file", Arguments: string(next)})
	require.ErrorContains(t, err, "foreign actor denied")
	// A legacy cursor has no authenticated owner. The worker accepts a different
	// session's cursor when the current actor can read the same snapshot.
	other := executionenv.NewEnvironment(cfg.ExecutionEnvironments.Profiles["remote"], "different-identity", "different-session", t.TempDir())
	defer other.Close()
	otherPage, err := other.Execute(ctx, executionenv.ToolRequest{Actor: "different-actor", PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: string(next)}})
	require.NoError(t, err)
	require.False(t, otherPage.IsError)
	// Native byte windows do not update or invalidate prior line observations.
	ticket, err := original.(executionenv.PreparedReadExecutor).PrepareUnobservedRead(ctx, prior)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed)

	// A real worker may have returned while a dynamic authorization provider is
	// concurrently revoked. The host must recheck and suppress that result.
	policy = &legacyNativePolicy{actor: actor, root: remote}
	thread.Toolkit.SetAuthorizer(policy)
	pausedPolicy := &legacyPauseExecutor{Executor: original, entered: make(chan struct{}), release: make(chan struct{})}
	thread.Toolkit.SetExecutionEnvironment(pausedPolicy)
	type outcome struct {
		result toolresult.Result
		err    error
	}
	policyDone := make(chan outcome, 1)
	go func() {
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "concurrent-revoke", Name: "read_file", Arguments: string(next)})
		policyDone <- outcome{result, err}
	}()
	select {
	case <-pausedPolicy.entered:
	case <-time.After(10 * time.Second):
		close(pausedPolicy.release)
		t.Fatal("worker did not return")
	}
	policy.revoked.Store(true)
	close(pausedPolicy.release)
	select {
	case got := <-policyDone:
		require.ErrorContains(t, got.err, "concurrently revoked")
		require.Empty(t, got.result.TextProjection())
	case <-time.After(10 * time.Second):
		t.Fatal("revoked read did not finish")
	}
	thread.Toolkit.SetExecutionEnvironment(original)
	thread.Toolkit.SetAuthorizer(nil)

	// Cancellation after the worker responds must not publish a late result.
	paused := &legacyPauseExecutor{Executor: original, entered: make(chan struct{}), release: make(chan struct{})}
	thread.Toolkit.SetExecutionEnvironment(paused)
	cancelCtx, cancelRead := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := rich.ExecuteResult(cancelCtx, providers.ToolCall{ID: "canceled", Name: "read_file", Arguments: string(next)})
		done <- err
	}()
	select {
	case <-paused.entered:
	case <-time.After(10 * time.Second):
		close(paused.release)
		cancelRead()
		t.Fatal("worker did not return")
	}
	cancelRead()
	close(paused.release)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "context canceled")
	case <-time.After(10 * time.Second):
		t.Fatal("canceled read did not finish")
	}
	thread.Toolkit.SetExecutionEnvironment(original)

	// Byte continuations can split a multibyte rune. Their utf-8/base64 pages
	// must concatenate to exactly the original bytes without gaps or overlap.
	unicodeBytes := []byte("a证🙂b")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "unicode.txt"), unicodeBytes, 0600))
	unicodeFirst, err := original.Execute(ctx, executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"unicode.txt","byte_range":{"offset":0,"limit":2}}`}})
	require.NoError(t, err)
	var u map[string]any
	require.NoError(t, json.Unmarshal([]byte(unicodeFirst.TextProjection()), &u))
	collected := []byte{}
	for i := 0; ; i++ {
		require.Less(t, i, 20)
		require.Equal(t, float64(len(collected)), u["byte_offset"])
		if u["encoding"] == "base64" {
			data, err := base64.StdEncoding.DecodeString(u["content_base64"].(string))
			require.NoError(t, err)
			collected = append(collected, data...)
		} else {
			collected = append(collected, []byte(u["content"].(string))...)
		}
		continuation := u["continuation"].(map[string]any)
		if continuation["has_more"] != true {
			break
		}
		args, err := json.Marshal(continuation["next"])
		require.NoError(t, err)
		page, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: fmt.Sprintf("unicode-%d", i), Name: "read_file", Arguments: string(args)})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal([]byte(page.TextProjection()), &u))
	}
	require.Equal(t, unicodeBytes, collected)

	// A session-owned archive uses the host's pinned artifact root, never a
	// similarly named file in the remote or local workspace.
	archiveName := strings.Repeat("a", 64) + ".txt"
	archiveDir := filepath.Join(thread.Toolkit.SessionDir(), "tool-results")
	require.NoError(t, os.MkdirAll(archiveDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, archiveName), content, 0600))
	archiveSHA := sha256.Sum256(content)
	archiveCursor := map[string]any{"path": "$SESSION_DIR/tool-results/" + archiveName, "byte_offset": 0, "byte_end_offset": len(content), "limit": 20, "expected_sha256": fmt.Sprintf("%x", archiveSHA), "result_source": "extension"}
	makeCursor := func(fields map[string]any) string {
		t.Helper()
		raw, err := json.Marshal(fields)
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
		require.NoError(t, err)
		return string(args)
	}
	archiveArgs := makeCursor(archiveCursor)
	archive, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "legacy-archive", Name: "read_file", Arguments: archiveArgs})
	require.NoError(t, err)
	var archiveView map[string]any
	require.NoError(t, json.Unmarshal([]byte(archive.TextProjection()), &archiveView))
	require.Equal(t, "extension", archiveView["source"])
	require.Equal(t, false, archiveView["content_verified"])
	delete(archiveCursor, "result_source")
	unattributed, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "forged-origin", Name: "read_file", Arguments: makeCursor(archiveCursor)})
	require.NoError(t, err)
	var unattributedView map[string]any
	require.NoError(t, json.Unmarshal([]byte(unattributed.TextProjection()), &unattributedView))
	require.Equal(t, "tool_result_archive", unattributedView["source"])
	require.Equal(t, false, unattributedView["content_verified"])
	_, err = foreignRich.ExecuteResult(foreignCtx, providers.ToolCall{ID: "foreign-archive", Name: "read_file", Arguments: archiveArgs})
	require.Error(t, err, "foreign session cannot read the original archive")
	archiveCursor["path"] = "$SESSION_DIR/../escape"
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "archive-escape", Name: "read_file", Arguments: makeCursor(archiveCursor)})
	require.Error(t, err)
	archiveCursor["path"] = "$SESSION_DIR/tool-results/" + archiveName
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, "$SESSION_DIR") {
			return fmt.Errorf("archive permission revoked")
		}
		return nil
	})
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "archive-denied", Name: "read_file", Arguments: archiveArgs})
	require.ErrorContains(t, err, "archive permission revoked")
	thread.Toolkit.SetPermissionRequestHook(nil)
	linkName := strings.Repeat("b", 64) + ".txt"
	if err := os.Symlink(filepath.Join(remote, "large.txt"), filepath.Join(archiveDir, linkName)); err == nil {
		archiveCursor["path"] = "$SESSION_DIR/tool-results/" + linkName
		_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "archive-link", Name: "read_file", Arguments: makeCursor(archiveCursor)})
		require.Error(t, err, "session archive must not follow symlinks")
	}
	require.NoError(t, os.WriteFile(filepath.Join(local, "only-local.txt"), content, 0600))
	archiveCursor["path"] = "only-local.txt"
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "no-local-fallback", Name: "read_file", Arguments: makeCursor(archiveCursor)})
	require.Error(t, err, "missing remote paths cannot fall back to local files")

	// A native line continuation is read by the original worker and records its
	// usual freshness observation; it is not an extension transaction.
	lineContent := []byte("one\ntwo\nthree\n")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "lines.txt"), lineContent, 0600))
	lineSHA := sha256.Sum256(lineContent)
	lineCursor, err := json.Marshal(map[string]any{"path": "lines.txt", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", lineSHA)})
	require.NoError(t, err)
	lineArgs, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(lineCursor)})
	require.NoError(t, err)
	line, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "legacy-line", Name: "read_file", Arguments: string(lineArgs)})
	require.NoError(t, err)
	require.Contains(t, line.TextProjection(), "two")
	lineReq := prior
	lineReq.Call.Arguments = `{"path":"lines.txt"}`
	lineTicket, err := original.(executionenv.PreparedReadExecutor).PrepareUnobservedRead(ctx, lineReq)
	require.NoError(t, err)
	lineChanged, err := lineTicket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, lineChanged, "native line continuation records its normal observation")

	// The host-owned native continuation remains available when its replacement
	// retires, but still cannot bypass native tool disablement or authorization.
	_, found := thread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("retired"))
	require.True(t, found)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "retired-legacy", Name: "read_file", Arguments: string(next)})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "retired-archive", Name: "read_file", Arguments: archiveArgs})
	require.NoError(t, err)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "retired-fresh", Name: "read_file", Arguments: `{"path":"large.txt"}`})
	require.Error(t, err)
	thread.Toolkit.DisableTools("read_file")
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "disabled-legacy", Name: "read_file", Arguments: string(next)})
	require.Error(t, err)
	thread.Toolkit.EnableTools("read_file")
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, `"path":"large.txt"`) {
			return fmt.Errorf("native denied after retirement")
		}
		return nil
	})
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "retired-denied", Name: "read_file", Arguments: string(next)})
	require.ErrorContains(t, err, "native denied after retirement")
	thread.Toolkit.SetPermissionRequestHook(nil)

	// Code Mode continues the native operation and preserves its normal failure
	// output semantics; no extension transaction is created for this read.
	cfg.PTC.Enabled = true
	ptcOwner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer ptcOwner.Cleanup()
	require.NoError(t, ptcOwner.PinExecutionEnvironment("ptc-legacy", ""))
	ptcThread, err := ptcOwner.NewThreadRuntime("ptc-legacy")
	require.NoError(t, err)
	defer ptcOwner.ReleasePluginGeneration(ptcThread.PluginGeneration)
	defer ptcThread.AgentControl.Close()
	ptcThread.Toolkit.SetPermissionMode("standard")
	ptcCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "ptc-legacy", ExecutionID: "turn"})
	quoted, err := json.Marshal(string(next))
	require.NoError(t, err)
	for _, retired := range []bool{false, true} {
		if retired {
			_, found := ptcThread.PluginHost.RetirePlugin(ptcCtx, plugin.ID, fmt.Errorf("retired"))
			require.True(t, found)
		}
		for _, fail := range []bool{false, true} {
			code := fmt.Sprintf(`const r=await tools.read_file(JSON.parse(%s)); console.log("NATIVE_OK",JSON.parse(r.content[0].text).action);`, quoted)
			if fail {
				code += `throw Error("controlled parent failure");`
			}
			args, err := json.Marshal(map[string]string{"code": code, "description": "native continuation"})
			require.NoError(t, err)
			id := fmt.Sprintf("native-ptc-%v-%v", retired, fail)
			run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: ptcThread.StreamRunner.Tools, Ledger: ptcThread.StreamRunner.ToolLedger, OperationID: id, RunContext: ptcCtx, Gate: agent.NewToolExecutionGate(1)})
			messages, err := run.ExecuteFinalCalls(ptcCtx, []providers.ToolCall{{ID: id, Name: "run_code", Arguments: string(args)}}, nil)
			run.Close()
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.Equal(t, fail, messages[0].ToolResult.IsError)
			require.Contains(t, messages[0].Content, "NATIVE_OK read_bytes")
		}
	}

	// Old native tokens are not worker-bound: an explicitly selected new worker
	// may continue the same snapshot, but never a changed file.
	thread.Toolkit.SetExecutionEnvironment(other)
	restarted, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "restart", Name: "read_file", Arguments: string(next)})
	require.NoError(t, err)
	require.Contains(t, restarted.TextProjection(), "native-data")
	require.NoError(t, other.Close())
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "stopped", Name: "read_file", Arguments: string(next)})
	require.ErrorContains(t, err, "closed")
	restartedEnv := executionenv.NewEnvironment(cfg.ExecutionEnvironments.Profiles["remote"], "different-identity", "different-session", t.TempDir())
	defer restartedEnv.Close()
	thread.Toolkit.SetExecutionEnvironment(restartedEnv)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "restart-again", Name: "read_file", Arguments: string(next)})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(remote, "large.txt"), []byte("changed"), 0600))
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "changed", Name: "read_file", Arguments: string(next)})
	require.ErrorContains(t, err, "stale")
}
