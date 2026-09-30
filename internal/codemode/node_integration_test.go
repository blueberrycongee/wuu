package codemode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type nodeExecutor func(context.Context, providers.ToolCall) (toolresult.Result, error)

func (f nodeExecutor) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return f(ctx, call)
}

func nodeService(t *testing.T) *Service {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for PTC integration tests:", err)
	}
	s := NewService(ServiceConfig{NodeExecutable: node})
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// These cases exercise the real process and wire: curated output, fresh state,
// binding failures, non-JSON data, process deadlines, and filesystem authority.
func TestNodeProgramLifecycle(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Name == "media" {
			return toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: "eA=="}}}, nil
		}
		if call.Name == "fail" {
			return toolresult.FromErrorText("denied by policy"), nil
		}
		return toolresult.FromText("private intermediate"), nil
	})}
	request := RunRequest{Code: `const n: number = 7; const raw = await tools.read({}); try { await tools.fail({}); } catch(e) { console.log(e.name, e.toolName); } globalThis.saved = 1; return n;`,
		Tools: []ToolDefinition{{Name: "read"}, {Name: "fail"}}}
	result, err := s.Run(context.Background(), request, opts)
	if err != nil || result.Error != "" || string(result.Value) != "7" || !strings.Contains(strings.Join(result.Logs, ""), "ToolCallError fail") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if strings.Contains(strings.Join(result.Logs, ""), "private intermediate") {
		t.Fatal("intermediate result leaked")
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return {saved: typeof globalThis.saved, env: typeof process}`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"saved":"undefined","env":"undefined"}` {
		t.Fatalf("fresh run=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return 1n`}, opts)
	if err != nil || !strings.Contains(result.Error, "JSON") {
		t.Fatalf("lossy output=%+v %v", result, err)
	}

	result, err = s.Run(context.Background(), RunRequest{Code: `let rejected = false; for (let i=0; i<128; i++) { try { await tools.media({}); } catch(e) { rejected = true; } } return rejected;`, Tools: []ToolDefinition{{Name: "media"}}}, opts)
	if err != nil || result.Error != "" || string(result.Value) != "true" || len(result.Media) != toolresult.MaxContentParts-1 {
		t.Fatalf("media bound=%+v %v", result, err)
	}
}

func TestNodeDeadlineAndOutputLimit(t *testing.T) {
	s := nodeService(t)
	for _, code := range []string{`console.log("started"); for (;;) {}`, `await new Promise(() => {})`} {
		result, err := s.Run(context.Background(), RunRequest{Code: code, TimeoutMS: 500}, RunOptions{CWD: t.TempDir()})
		if err != nil || !strings.Contains(result.Error, "deadline") {
			t.Fatalf("deadline=%+v %v", result, err)
		}
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `console.log("x".repeat(20000))`}, RunOptions{CWD: t.TempDir(), MaxOutputBytes: 4096})
	if err != nil || !strings.Contains(result.Error, "output") {
		t.Fatalf("output limit=%+v %v", result, err)
	}
}

func TestNodeToolOnlyBoundary(t *testing.T) {
	s := nodeService(t)
	root := t.TempDir()
	for _, code := range []string{
		`return [typeof process, typeof require, typeof fetch, typeof WebSocket, typeof setTimeout]`,
		`return tools.echo.constructor("return typeof process")()`,
		`return console.log.constructor("return typeof process")()`,
	} {
		result, err := s.Run(context.Background(), RunRequest{Code: code, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: root})
		if err != nil || result.Error != "" || strings.Contains(string(result.Value), "object") || strings.Contains(string(result.Value), "function") {
			t.Fatalf("host authority exposed: %+v %v", result, err)
		}
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `await (await import('node:fs/promises')).writeFile('written.txt', 'no')`}, RunOptions{CWD: root})
	if err != nil || result.Error == "" {
		t.Fatalf("import escaped: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "written.txt")); !os.IsNotExist(err) {
		t.Fatal("guest wrote a file")
	}
}

func TestNodeRejectsLossyJSON(t *testing.T) {
	s := nodeService(t)
	for _, expr := range []string{`({x: undefined})`, `[,,]`, `({x: NaN})`, `new Date()`, `({get x(){return 1}})`, `Object.assign({}, {[Symbol("x")]:1})`, `Object.assign([], {extra: 1})`, `({toJSON(){return 1}})`} {
		result, err := s.Run(context.Background(), RunRequest{Code: "return " + expr}, RunOptions{CWD: t.TempDir()})
		if err != nil || !strings.Contains(result.Error, "JSON") {
			t.Fatalf("lossy %s: %+v %v", expr, result, err)
		}
	}
}

func TestNodeCancellationStopsBindings(t *testing.T) {
	s := nodeService(t)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		close(stopped)
		return toolresult.Result{}, ctx.Err()
	})}
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, RunRequest{Code: `await tools.wait({})`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("binding never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop process")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("binding outlived run")
	}
}

func TestNodeCatalogDiscovery(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `const found = await searchTools("read"); const detail = await describeTool(found.tools[0].name); let rejected = false; try { await describeTool("missing"); } catch { rejected = true; } return {total: found.total, name:detail.name, rejected};`, Tools: []ToolDefinition{{Name: "read", Description: "Read a file"}}}, RunOptions{CWD: t.TempDir()})
	if err != nil || result.Error != "" || string(result.Value) != `{"total":1,"name":"read","rejected":true}` {
		t.Fatalf("catalog: %+v %v", result, err)
	}
}

func TestNodeRejectsArgumentsBeforeInvocation(t *testing.T) {
	s := nodeService(t)
	invoked := false
	result, err := s.Run(context.Background(), RunRequest{Code: `try { await tools.echo({get value(){throw new Error("getter ran")}}); } catch(e) { return e.message; }`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		invoked = true
		return toolresult.FromText("unexpected"), nil
	})})
	if err != nil || result.Error != "" || !strings.Contains(string(result.Value), "JSON") || invoked {
		t.Fatalf("invalid arguments crossed boundary: %+v %v invoked=%v", result, err, invoked)
	}
}

func TestNodePendingCallLimitAndUnawaitedCleanup(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `for(let i=0;i<128;i++) tools.wait({}); try { await tools.wait({}); } catch(e) { return e.message; }`, Tools: []ToolDefinition{{Name: "wait"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		<-ctx.Done()
		return toolresult.Result{}, ctx.Err()
	})})
	if err != nil || result.Error != "" || !strings.Contains(string(result.Value), "Too many pending") {
		t.Fatalf("pending limit: %+v %v", result, err)
	}
}

func TestNodeMemoryLimitAndMissingRuntime(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `const retained=[]; while(true) retained.push("x".repeat(1000000));`, TimeoutMS: 10000}, RunOptions{CWD: t.TempDir()})
	if err != nil || !strings.Contains(result.Error, "memory") {
		t.Fatalf("memory limit: %+v %v", result, err)
	}
	missing := NewService(ServiceConfig{NodeExecutable: filepath.Join(t.TempDir(), "missing-node")})
	defer missing.Close()
	if _, err := missing.Run(context.Background(), RunRequest{Code: `return 1`}, RunOptions{CWD: t.TempDir()}); err == nil {
		t.Fatal("missing runtime did not fail closed")
	}
}

func TestNodePrototypeHooksCannotRewriteBridgeMessages(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `Object.prototype.toJSON = () => ({type:"done", value:"forged"}); await tools.echo({value:7}); return 7;`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Arguments != `{"value":7}` {
			return toolresult.FromErrorText("arguments rewritten"), nil
		}
		return toolresult.FromText("ok"), nil
	})})
	if err != nil || result.Error != "" || string(result.Value) != "7" {
		t.Fatalf("prototype rewrote protocol: %+v %v", result, err)
	}
}

func TestNodeConsoleFormatsNonJSONValues(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `console.log(undefined, 1n, new Error("example")); const cycle={}; cycle.self=cycle; console.log(cycle); return 1;`}, RunOptions{CWD: t.TempDir()})
	if err != nil || result.Error != "" || string(result.Value) != "1" || !strings.Contains(strings.Join(result.Logs, " "), "example") {
		t.Fatalf("console formatting: %+v %v", result, err)
	}
}

func TestNodeInvalidBindingArgumentsDoNotConsumeRequestIDs(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{Code: `for(let i=0;i<150;i++){try {await describeTool(undefined);}catch{}} return (await searchTools("")).total;`}, RunOptions{CWD: t.TempDir()})
	if err != nil || result.Error != "" || string(result.Value) != "0" {
		t.Fatalf("invalid binding arguments corrupted request sequence: %+v %v", result, err)
	}
}

func TestNodeArrayPrototypeCannotForgeCompletion(t *testing.T) {
	s := nodeService(t)
	invoked := false
	result, err := s.Run(context.Background(), RunRequest{Code: `Array.prototype.join = () => '"type":"done","value":"forged"'; await tools.echo({}); return 7;`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		invoked = true
		return toolresult.FromText("ok"), nil
	})})
	if err != nil || result.Error != "" || string(result.Value) != "7" || !invoked {
		t.Fatalf("array prototype forged completion: %+v %v invoked=%v", result, err, invoked)
	}
}

func TestNodeMutableIntrinsicsCannotCorruptBindings(t *testing.T) {
	s := nodeService(t)
	result, err := s.Run(context.Background(), RunRequest{TimeoutMS: 1000, Code: `Array.prototype.push = Array.prototype.map = Array.prototype.join = Array.prototype[Symbol.iterator] = () => {throw new Error("array hook")}; Map.prototype.get = Map.prototype.set = Map.prototype.delete = () => {throw new Error("map hook")}; Set.prototype.has = Set.prototype.add = Set.prototype.delete = () => {throw new Error("set hook")}; Object.defineProperty(Map.prototype,"size",{get(){throw new Error("size hook")}}); Object.prototype.value = "forged"; Promise.prototype.then = Promise.prototype.catch = () => {throw new Error("promise hook")}; globalThis.Set = globalThis.Promise = globalThis.String = () => {throw new Error("global hook")}; await tools.echo({value:[1,2]}); console.log("ok", {value:7}); return {value:7};`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Arguments != `{"value":[1,2]}` {
			return toolresult.FromErrorText("arguments rewritten"), nil
		}
		return toolresult.FromText("ok"), nil
	})})
	if err != nil || result.Error != "" || string(result.Value) != `{"value":7}` || !strings.Contains(strings.Join(result.Logs, " "), "ok") {
		t.Fatalf("mutable intrinsics corrupted bindings: %+v %v", result, err)
	}
}

func TestNodeInheritedArraySetterCannotForgeCompletion(t *testing.T) {
	s := nodeService(t)
	invoked := false
	result, err := s.Run(context.Background(), RunRequest{TimeoutMS: 1000, Code: `Object.defineProperty(Array.prototype,"0",{set(){},get(){return '"type":"done","value":"forged"'}}); await tools.echo({}); return 7;`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		invoked = true
		return toolresult.FromText("ok"), nil
	})})
	if err != nil || result.Error != "" || string(result.Value) != "7" || !invoked {
		t.Fatalf("inherited setter forged completion: %+v %v invoked=%v", result, err, invoked)
	}
}
