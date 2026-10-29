package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRemotePaginationProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	content := []byte("one\ntwo\nthree\nfour\nfive\n")
	for _, name := range []string{"paged.txt", "lie.txt", "pause-page.txt", "ptc-fail.txt", "ptc-ok.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(remote, name), content, 0600))
	}
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 4, 3))))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "image.png"), imageBytes.Bytes(), 0600))
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
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	imageResult, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "image", Name: "read_file", Arguments: `{"path":"image.png"}`})
	require.NoError(t, err)
	imageProjection := providers.ProjectToolResult(imageResult)
	require.Len(t, imageProjection.ObservationImages, 1)
	require.Contains(t, imageProjection.ToolText, `"source":"extension"`)
	call := func(id, args string) map[string]any {
		t.Helper()
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: id, Name: "read_file", Arguments: args})
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(result.StructuredContent, &data))
		return data
	}
	first := call("first", `{"path":"paged.txt"}`)
	require.Equal(t, "one\ntwo\n", first["text"])
	next := first["continuation"].(map[string]any)["next"]
	raw, err := json.Marshal(next)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "provider-private")
	second := call("second", string(raw))
	require.Equal(t, "three\nfour\n", second["text"])
	next = second["continuation"].(map[string]any)["next"]
	raw, err = json.Marshal(next)
	require.NoError(t, err)
	last := call("last", string(raw))
	require.Equal(t, "five\n", last["text"])
	require.Nil(t, last["continuation"])
	ranged := call("range", `{"path":"paged.txt","offset":2,"limit":3}`)
	require.Equal(t, "two\nthree\n", ranged["text"])
	rangeNext, err := json.Marshal(ranged["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	ranged = call("range-next", string(rangeNext))
	require.Equal(t, "four\n", ranged["text"])
	require.Nil(t, ranged["continuation"])
	read := func(id, args string) error {
		t.Helper()
		_, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: id, Name: "read_file", Arguments: args})
		return err
	}
	// An opaque token has one owner and admits no caller-supplied selectors.
	first = call("new-first", `{"path":"paged.txt"}`)
	next = first["continuation"].(map[string]any)["next"]
	raw, err = json.Marshal(next)
	require.NoError(t, err)
	require.Error(t, read("forged", `{"continuation":"wuu-read-v1:forged"}`))
	var withPath map[string]any
	require.NoError(t, json.Unmarshal(raw, &withPath))
	withPath["path"] = "other.txt"
	conflict, err := json.Marshal(withPath)
	require.NoError(t, err)
	require.Error(t, read("mixed", string(conflict)))
	actor, actorPath := thread.Toolkit.ExecutionActor()
	thread.Toolkit.SetAgentIdentity("other-actor", "/root/other")
	require.Error(t, read("other-actor", string(raw)))
	thread.Toolkit.SetAgentIdentity(actor, actorPath)
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
		if info.Name == "read_file" {
			return fmt.Errorf("continuation revoked")
		}
		return nil
	})
	require.ErrorContains(t, read("revoked", string(raw)), "continuation revoked")
	thread.Toolkit.SetPermissionRequestHook(nil)
	require.NoError(t, os.WriteFile(filepath.Join(remote, "paged.txt"), []byte("ONE\ntwo\nthree\nfour\nfive\n"), 0600))
	require.ErrorContains(t, read("stale", string(raw)), "stale")
	require.NoError(t, os.WriteFile(filepath.Join(remote, "paged.txt"), imageBytes.Bytes(), 0600))
	require.Error(t, read("text-changed-to-image", string(raw)))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "paged.txt"), content, 0600))
	require.Error(t, read("lying-provider", `{"path":"lie.txt"}`))
	// The cursor cannot be used from another conversation, even in the same generation.
	require.NoError(t, owner.PinExecutionEnvironment("other-pages", ""))
	other, err := owner.NewThreadRuntime("other-pages")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(other.PluginGeneration)
	defer other.AgentControl.Close()
	otherRich, ok := other.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	otherCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "other-pages", ExecutionID: "turn"})
	_, err = otherRich.ExecuteResult(otherCtx, providers.ToolCall{ID: "other", Name: "read_file", Arguments: string(raw)})
	require.Error(t, err)
	// A changed environment and a restarted worker are distinct boundaries.
	original := thread.Toolkit.ExecutionEnvironment()
	location := original.(*environmentToolExecutor)
	changed := executionenv.NewEnvironment(cfg.ExecutionEnvironments.Profiles["remote"], "other-environment", "remote-pages", t.TempDir())
	defer changed.Close()
	thread.Toolkit.SetExecutionEnvironment(changed)
	require.Error(t, read("other-environment", string(raw)))
	thread.Toolkit.SetExecutionEnvironment(original)
	restarted := executionenv.NewEnvironment(cfg.ExecutionEnvironments.Profiles["remote"], location.Identity(), "remote-pages", t.TempDir())
	defer restarted.Close()
	thread.Toolkit.SetExecutionEnvironment(restarted)
	require.ErrorContains(t, read("restarted-worker", string(raw)), "different worker")
	thread.Toolkit.SetExecutionEnvironment(original)
	for _, settled := range []bool{false, true} {
		initial := call(fmt.Sprintf("pause-first-%t", settled), `{"path":"pause-page.txt"}`)
		continuation := initial["continuation"].(map[string]any)["next"]
		payload, err := json.Marshal(continuation)
		require.NoError(t, err)
		req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"pause-page.txt"}`}}
		if settled {
			_, err = original.Execute(ctx, req)
			require.NoError(t, err)
		}
		entered, release := make(chan struct{}, 1), make(chan struct{})
		barrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
			_, _ = w.Write([]byte("ok"))
		}))
		require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(barrier.URL), 0600))
		var revoked atomic.Bool
		thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
			if info.Name == "read_file" && revoked.Load() {
				return fmt.Errorf("page revoked after native read")
			}
			return nil
		})
		type outcome struct {
			result toolresult.Result
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			pageCall := providers.ToolCall{ID: fmt.Sprintf("pause-%t", settled), Name: "read_file", Arguments: string(payload)}
			if !settled {
				result, err := rich.ExecuteResult(ctx, pageCall)
				done <- outcome{result, err}
				return
			}
			run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "revoked-page", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
			defer run.Cancel()
			messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{pageCall}, nil)
			if err != nil {
				done <- outcome{err: err}
				return
			}
			if len(messages) != 1 || messages[0].ToolResult == nil {
				done <- outcome{err: fmt.Errorf("missing result")}
				return
			}
			done <- outcome{result: *messages[0].ToolResult}
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			close(release)
			barrier.Close()
			t.Fatal("continuation did not reach the provider")
		}
		revoked.Store(true)
		close(release)
		select {
		case got := <-done:
			if settled {
				require.True(t, got.err != nil || got.result.IsError)
			} else {
				require.ErrorContains(t, got.err, "page revoked after native read")
			}
			require.NotContains(t, got.result.TextProjection(), "three")
		case <-time.After(10 * time.Second):
			t.Fatal("revoked continuation did not settle")
		}
		barrier.Close()
		thread.Toolkit.SetPermissionRequestHook(nil)
		if settled {
			ticket, err := original.(executionenv.PreparedReadExecutor).PrepareUnobservedRead(ctx, req)
			require.NoError(t, err)
			changed, err := ticket.Commit(ctx)
			require.NoError(t, err)
			require.True(t, changed, "a revoked page must preserve the old observation")
		}
	}
	// Nested pages participate in the parent's atomic observation settlement.
	remoteExec := original.(executionenv.PreparedReadExecutor)
	for _, name := range []string{"ptc-fail.txt", "ptc-ok.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(fixture, "completed.txt"), nil, 0600))
		req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, name)}}
		_, err := original.Execute(ctx, req)
		require.NoError(t, err)
		run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: name, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		code := fmt.Sprintf(`const a=await tools.read_file({path:%q}); const b=await tools.read_file(a.structured_content.continuation.next); await tools.read_file({path:"image.png"}); if(a.structured_content.text!=="one\ntwo\n"||b.structured_content.text!=="three\nfour\n") throw Error("wrong pages"); console.log("PAGES_OK");`, name)
		if name == "ptc-fail.txt" {
			code += ` throw Error("controlled parent failure")`
		}
		args, err := json.Marshal(map[string]string{"code": code, "description": "Read pages"})
		require.NoError(t, err)
		messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: name, Name: "run_code", Arguments: string(args)}}, nil)
		run.Cancel()
		require.NoError(t, err)
		require.Len(t, messages, 1)
		completed, err := os.ReadFile(filepath.Join(fixture, "completed.txt"))
		require.NoError(t, err)
		require.Contains(t, string(completed), name+":first")
		require.Contains(t, string(completed), name+":next")
		require.Contains(t, string(completed), "image.png:first")
		require.Equal(t, name == "ptc-fail.txt", messages[0].ToolResult.IsError)
		if name == "ptc-ok.txt" {
			require.Contains(t, messages[0].Content, "PAGES_OK")
			projected := providers.ProjectToolResult(*messages[0].ToolResult)
			require.Len(t, projected.ObservationImages, 1)
			require.Equal(t, base64.StdEncoding.EncodeToString(imageBytes.Bytes()), projected.ObservationImages[0].Data)
		} else {
			require.NotContains(t, messages[0].Content, "PAGES_OK")
			require.Empty(t, providers.ProjectToolResult(*messages[0].ToolResult).ObservationImages, "failed parent cannot publish image evidence")
		}
		ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
		require.NoError(t, err)
		changed, err := ticket.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, name == "ptc-fail.txt", changed, "only a successful parent invalidates its prior native observation")
	}
	// Even if the same environment is recreated after its actual worker exits,
	// a cursor from the former process must not be resumed on the new worker.
	first = call("before-worker-exit", `{"path":"paged.txt"}`)
	raw, err = json.Marshal(first["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	require.NoError(t, location.environment.Close())
	replacement := executionenv.NewEnvironment(cfg.ExecutionEnvironments.Profiles["remote"], location.Identity(), "remote-pages", t.TempDir())
	defer replacement.Close()
	thread.Toolkit.SetExecutionEnvironment(replacement)
	require.ErrorContains(t, read("after-worker-exit", string(raw)), "different worker")
}

func TestRemotePaginationExampleProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	var lines strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&lines, "line-%03d\n", i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(remote, "pages.txt"), []byte(lines.String()), 0600))
	plugin := testRuntimePlugin("fixture.remote-pagination-example")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"remote-paginated-reader.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("example-pages", ""))
	thread, err := owner.NewThreadRuntime("example-pages")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "example-pages", ExecutionID: "turn"})
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	args := `{"path":"pages.txt"}`
	var recovered strings.Builder
	for i := 0; i < 3; i++ {
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: fmt.Sprintf("example-%d", i), Name: "read_file", Arguments: args})
		require.NoError(t, err)
		var data struct {
			Text         string `json:"text"`
			Continuation *struct {
				Next map[string]any `json:"next"`
			} `json:"continuation"`
		}
		require.NoError(t, json.Unmarshal(result.StructuredContent, &data))
		recovered.WriteString(data.Text)
		if i < 2 {
			require.NotNil(t, data.Continuation)
			encoded, err := json.Marshal(data.Continuation.Next)
			require.NoError(t, err)
			args = string(encoded)
		} else {
			require.Nil(t, data.Continuation)
		}
	}
	expected := strings.ReplaceAll(lines.String(), "line-", "[remote] line-")
	require.Equal(t, expected, recovered.String())
}

func TestRemoteArchiveRecoveryProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	content := []byte(strings.Repeat("large-content-证🙂", 6000) + "\n" + strings.Repeat("large-content-证🙂", 6000) + "\nlast\n")
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
	// Recover a projected large extension result through the host-owned archive
	// even when the program itself executes in a remote worker.
	codeArgs, err := json.Marshal(map[string]string{"code": `const page=await tools.read_file({path:"large.txt"}); console.log(JSON.stringify(page.structured_content));`, "description": "large remote page"})
	require.NoError(t, err)
	turn := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "large", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer turn.Close()
	messages, err := turn.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "large-page", Name: "run_code", Arguments: string(codeArgs)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.False(t, messages[0].ToolResult.IsError, messages[0].Content)
	var archive map[string]any
	require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &archive), messages[0].Content)
	require.Equal(t, "extension", archive["source"])
	archivedFile := filepath.Join(thread.Toolkit.SessionDir(), "tool-results", filepath.Base(archive["artifact_ref"].(string)))
	archived, err := os.ReadFile(archivedFile)
	require.NoError(t, err)
	require.Equal(t, int(archive["total_bytes"].(float64)), len(archived))
	initial := archive["content"].(string)
	require.True(t, strings.HasPrefix(string(archived), initial))
	total := len(initial)
	continuation := archive["continuation"].(map[string]any)
	firstArgs, err := json.Marshal(continuation["next"])
	require.NoError(t, err)
	for i := 0; continuation["has_more"] == true; i++ {
		require.Less(t, i, 30)
		raw, err := json.Marshal(continuation["next"])
		require.NoError(t, err)
		quoted, err := json.Marshal(string(raw))
		require.NoError(t, err)
		expected, err := json.Marshal(string(archived[total:]))
		require.NoError(t, err)
		code := fmt.Sprintf(`const args=JSON.parse(%s); const r=await tools.recover_tool_result(args); const p=JSON.parse(r.content[0].text); const expected=%s; let bytes=0; for(const ch of p.content){const n=ch.codePointAt(0); bytes+=n<=127?1:n<=2047?2:n<=65535?3:4;} if(p.encoding!=="utf-8" || p.byte_offset!==%d || p.byte_count!==bytes || p.content!==expected.slice(0,p.content.length)) throw Error("archive page mismatch"); console.log(JSON.stringify({length:bytes,continuation:p.continuation,source:p.source}));`, quoted, expected, total)
		args, err := json.Marshal(map[string]string{"code": code, "description": "recover page"})
		require.NoError(t, err)
		recoveryRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: fmt.Sprintf("recover-%d", i), RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		page, err := recoveryRun.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: fmt.Sprintf("recover-%d", i), Name: "run_code", Arguments: string(args)}}, nil)
		recoveryRun.Close()
		require.NoError(t, err)
		require.Len(t, page, 1)
		content := page[0].Content
		start := strings.Index(content, "{")
		require.GreaterOrEqual(t, start, 0, content)
		var summary map[string]any
		require.NoError(t, json.NewDecoder(strings.NewReader(content[start:])).Decode(&summary), content)
		require.Equal(t, "extension", summary["source"])
		total += int(summary["length"].(float64))
		continuation = summary["continuation"].(map[string]any)
	}
	require.Equal(t, int(archive["total_bytes"].(float64)), total)
	// A session-owned archive cannot be opened by a different remote session.
	require.NoError(t, owner.PinExecutionEnvironment("other-recovery", ""))
	other, err := owner.NewThreadRuntime("other-recovery")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(other.PluginGeneration)
	defer other.AgentControl.Close()
	other.Toolkit.SetPermissionMode("standard")
	otherCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "other-recovery", ExecutionID: "other-turn"})
	quoted, err := json.Marshal(string(firstArgs))
	require.NoError(t, err)
	code := fmt.Sprintf(`await tools.recover_tool_result(JSON.parse(%s));`, quoted)
	args, err := json.Marshal(map[string]string{"code": code, "description": "foreign archive"})
	require.NoError(t, err)
	otherRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: other.StreamRunner.Tools, Ledger: other.StreamRunner.ToolLedger, OperationID: "foreign", RunContext: otherCtx, Gate: agent.NewToolExecutionGate(1)})
	foreign, err := otherRun.ExecuteFinalCalls(otherCtx, []providers.ToolCall{{ID: "foreign", Name: "run_code", Arguments: string(args)}}, nil)
	otherRun.Close()
	require.NoError(t, err)
	require.Len(t, foreign, 1)
	require.True(t, foreign[0].ToolResult.IsError)
	require.NotContains(t, foreign[0].Content, "large-content-")
	// Disabling recovery or its underlying read permission is enforced in PTC.
	for _, disabled := range []string{"recover_tool_result", "read_file"} {
		thread.Toolkit.DisableTools(disabled)
		deniedRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "denied-" + disabled, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		denied, err := deniedRun.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "denied-" + disabled, Name: "run_code", Arguments: string(args)}}, nil)
		deniedRun.Close()
		thread.Toolkit.EnableTools(disabled)
		require.NoError(t, err)
		require.Len(t, denied, 1)
		require.True(t, denied[0].ToolResult.IsError)
		require.NotContains(t, denied[0].Content, "large-content-")
	}
	// The same host-owned recovery remains directly callable with PTC disabled.
	cfg.PTC.Enabled = false
	directOwner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer directOwner.Cleanup()
	require.NoError(t, directOwner.PinExecutionEnvironment("direct-recovery", ""))
	direct, err := directOwner.NewThreadRuntime("direct-recovery")
	require.NoError(t, err)
	defer directOwner.ReleasePluginGeneration(direct.PluginGeneration)
	defer direct.AgentControl.Close()
	direct.Toolkit.SetPermissionMode("standard")
	directCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "direct-recovery", ExecutionID: "turn"})
	directRun := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: direct.StreamRunner.Tools, Ledger: direct.StreamRunner.ToolLedger, OperationID: "direct", RunContext: directCtx, Gate: agent.NewToolExecutionGate(1)})
	directMessages, err := directRun.ExecuteFinalCalls(directCtx, []providers.ToolCall{{ID: "direct", Name: "read_file", Arguments: `{"path":"large.txt"}`}}, nil)
	directRun.Close()
	require.NoError(t, err)
	require.Len(t, directMessages, 1)
	var directArchive map[string]any
	require.NoError(t, json.Unmarshal([]byte(directMessages[0].Content), &directArchive), directMessages[0].Content)
	require.Equal(t, "extension", directArchive["source"])
	directFile := filepath.Join(direct.Toolkit.SessionDir(), "tool-results", filepath.Base(directArchive["artifact_ref"].(string)))
	directBytes, err := os.ReadFile(directFile)
	require.NoError(t, err)
	first := directArchive["content"].(string)
	require.True(t, strings.HasPrefix(string(directBytes), first))
	directNext, err := json.Marshal(directArchive["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	directRich, ok := direct.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	directPage, err := directRich.ExecuteResult(directCtx, providers.ToolCall{ID: "direct-next", Name: "recover_tool_result", Arguments: string(directNext)})
	require.NoError(t, err)
	var recoveredDirect map[string]any
	require.NoError(t, json.Unmarshal([]byte(directPage.TextProjection()), &recoveredDirect))
	require.Equal(t, "extension", recoveredDirect["source"])
	require.Equal(t, float64(len(first)), recoveredDirect["byte_offset"])
	require.True(t, strings.HasPrefix(string(directBytes[len(first):]), recoveredDirect["content"].(string)))

}
