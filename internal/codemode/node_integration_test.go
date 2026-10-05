package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
	for _, code := range []string{`console.log("started"); for (;;) {}`, `await tools.wait({})`} {
		result, err := s.Run(context.Background(), RunRequest{Code: code, TimeoutMS: 500, Tools: []ToolDefinition{{Name: "wait"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
			<-ctx.Done()
			return toolresult.Result{}, ctx.Err()
		})})
		if err != nil || !strings.Contains(result.Error, "deadline") {
			t.Fatalf("deadline=%+v %v", result, err)
		}
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `console.log("x".repeat(20000))`}, RunOptions{CWD: t.TempDir(), MaxOutputBytes: 4096})
	if err != nil || !strings.Contains(result.Error, "output") {
		t.Fatalf("output limit=%+v %v", result, err)
	}
}

func TestNodeUnsettleablePromisesReleaseScopeWithoutCommitting(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "stalled", Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return toolresult.FromText("ready"), nil
	})}
	seed, err := s.Run(context.Background(), RunRequest{Code: `store("checkpoint", "before");`}, opts)
	if err != nil || seed.Error != "" {
		t.Fatalf("seed=%+v %v", seed, err)
	}
	for _, code := range []string{
		`await new Promise(() => {});`,
		`await Promise.race([]);`,
		`await tools.echo({}); await Promise.race([]);`,
		`await searchTools(""); await describeTool("echo"); await Promise.race([]);`,
	} {
		t.Run(code, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := s.Run(ctx, RunRequest{Code: `store("checkpoint", "after"); console.log("started"); ` + code, Tools: []ToolDefinition{{Name: "echo"}}}, opts)
			if err != nil || !strings.Contains(result.Error, "never settle") || ctx.Err() != nil {
				t.Fatalf("stalled program did not fail without a deadline: %+v %v", result, err)
			}
			if strings.Join(result.Logs, "") != "started" {
				t.Fatalf("partial output lost: %+v", result)
			}
			next, err := s.Run(ctx, RunRequest{Code: `return load("checkpoint");`}, opts)
			if err != nil || next.Error != "" || string(next.Value) != `"before"` {
				t.Fatalf("scope remained occupied or committed failed state: %+v %v", next, err)
			}
		})
	}
}

func TestNodeMicrotasksAndHostCallsCanSettle(t *testing.T) {
	s := nodeService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.Run(ctx, RunRequest{Code: `
const catalog = await searchTools("");
await Promise.all(catalog.tools.map(tool => describeTool(tool.name)));
await new Promise(resolve => Promise.resolve().then(resolve));
await Promise.all([tools.echo({}), tools.echo({})]);
return 7;`, Tools: []ToolDefinition{{Name: "echo"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return toolresult.FromText("ready"), nil
	})})
	if err != nil || result.Error != "" || string(result.Value) != "7" {
		t.Fatalf("resolvable program was rejected: %+v %v", result, err)
	}
}

func TestNodeErrorDiagnosticsIdentifyUserSource(t *testing.T) {
	s := nodeService(t)
	for _, tc := range []struct {
		name, code string
		want       []string
	}{
		{"first line", `throw new TypeError("bad value");`, []string{"TypeError: bad value", "program.ts:1:"}},
		{"nested TypeScript", "function inner(value: string) {\n  throw new RangeError(value);\n}\ninner('out of range');", []string{"RangeError: out of range", "inner (program.ts:2:", "program.ts:4:"}},
		{"after await", "await Promise.resolve();\nthrow new TypeError('after await');", []string{"TypeError: after await", "program.ts:2:"}},
		{"syntax", "const ok = 1;\nconst = ;", []string{"SyntaxError:", "program.ts:2"}},
		{"unsupported TypeScript", `enum Color { Red }`, []string{"SyntaxError:", "program.ts:1"}},
		{"thrown string", `throw "plain failure";`, []string{"plain failure"}},
		{"empty throw", `throw "";`, []string{"Program failed"}},
		{"unprintable", `throw { get message() { throw 1; }, get name() { throw 2; }, get stack() { throw 3; } };`, []string{"unprintable error"}},
		{"mutated intrinsics", `String.prototype.slice = () => "lost"; JSON.stringify = () => "lost"; throw new TypeError("preserved");`, []string{"TypeError: preserved", "program.ts:1:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.Run(context.Background(), RunRequest{Code: tc.code}, RunOptions{CWD: t.TempDir()})
			if err != nil || result.Error == "" {
				t.Fatalf("missing diagnostic: %+v %v", result, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Error, want) {
					t.Errorf("diagnostic %q does not contain %q", result.Error, want)
				}
			}
			for _, internal := range []string{"<eval>", "node:internal", "data:text/javascript", "file://", "guestSetup"} {
				if strings.Contains(result.Error, internal) {
					t.Errorf("internal frame leaked: %q", result.Error)
				}
			}
		})
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `throw new Error("x".repeat(20000));`}, RunOptions{CWD: t.TempDir()})
	if err != nil || result.Error == "" || len(result.Error) > 8192 {
		t.Fatalf("unbounded diagnostic: %d bytes, %v", len(result.Error), err)
	}
}

func TestNodeFailureSummaryPreservesCallOutcomes(t *testing.T) {
	s := nodeService(t)
	entered := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resultCh := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(ctx, RunRequest{Code: `await tools.effect({}); await tools.wait({});`, Tools: []ToolDefinition{{Name: "effect"}, {Name: "wait"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
			if call.Name == "wait" {
				close(entered)
				<-ctx.Done()
				return toolresult.Result{}, ctx.Err()
			}
			return toolresult.FromText("private result"), nil
		})})
		if err != nil {
			t.Errorf("run: %v", err)
		}
		resultCh <- result
	}()
	select {
	case <-entered:
		cancel()
	case <-ctx.Done():
		t.Fatal("nested wait was not reached")
	}
	select {
	case result := <-resultCh:
		if result.Error != context.Canceled.Error() || !strings.Contains(result.CallSummary, `"effect": succeeded`) || !strings.Contains(result.CallSummary, `"wait": interrupted`) || !strings.Contains(result.CallSummary, "effects unknown") {
			t.Fatalf("lost cancellation outcomes: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not finish")
	}
}

func TestNodeFailureSummaryIsBoundedAndSurvivesOutputLimit(t *testing.T) {
	s := nodeService(t)
	// Long, escapable names exercise the encoded diagnostic budget as well as the call count.
	name := strings.Repeat("\x00", 256)
	opts := RunOptions{CWD: t.TempDir(), MaxOutputBytes: 4096, Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return toolresult.FromText("private result"), nil
	})}
	result, err := s.Run(context.Background(), RunRequest{Code: `const {tools: catalog} = await searchTools(""); for(let i=0;i<40;i++) await tools[catalog[0].name]({}); console.log("x".repeat(20000));`, Tools: []ToolDefinition{{Name: name}}}, opts)
	encoded, _ := json.Marshal(result.CallSummary)
	if err != nil || !strings.Contains(result.Error, "output") || !strings.Contains(result.CallSummary, "40 succeeded") || !strings.Contains(result.CallSummary, "earlier calls omitted") || len(encoded) > 8192 {
		t.Fatalf("missing or unbounded summary (%d bytes): %+v %v", len(encoded), result, err)
	}
	if strings.Contains(result.CallSummary, "private result") {
		t.Fatal("intermediate result leaked")
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `await tools.echo({}); return 7;`, Tools: []ToolDefinition{{Name: "echo"}}}, opts)
	if err != nil || result.Error != "" || result.CallSummary != "" {
		t.Fatalf("successful run included a recovery diagnostic: %+v %v", result, err)
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

func TestNodeOptionalDeadline(t *testing.T) {
	for _, tc := range []struct {
		name          string
		timeout       int
		parentTimeout time.Duration
	}{
		{name: "omitted"},
		{name: "explicit over ten minutes", timeout: 601000},
		{name: "largest representable duration", timeout: math.MaxInt64 / int(time.Millisecond)},
		{name: "inherited deadline", parentTimeout: time.Minute},
		{name: "earlier parent deadline", timeout: 601000, parentTimeout: time.Minute},
		{name: "earlier explicit deadline", timeout: 30000, parentTimeout: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := nodeService(t)
			ctx := context.Background()
			if tc.parentTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.parentTimeout)
				defer cancel()
			}
			var deadline time.Time
			var hasDeadline bool
			before := time.Now()
			result, err := s.Run(ctx, RunRequest{Code: `await tools.inspect({}); return true;`, TimeoutMS: tc.timeout, Tools: []ToolDefinition{{Name: "inspect"}}}, RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(callCtx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
				deadline, hasDeadline = callCtx.Deadline()
				return toolresult.FromText("inspected"), nil
			})})
			after := time.Now()
			if err != nil || result.Error != "" || string(result.Value) != "true" {
				t.Fatalf("run=%+v err=%v", result, err)
			}
			wantDeadline := tc.timeout > 0 || tc.parentTimeout > 0
			if hasDeadline != wantDeadline {
				t.Fatalf("nested deadline=%v, want %v", hasDeadline, wantDeadline)
			}
			if parentDeadline, ok := ctx.Deadline(); ok && (tc.timeout == 0 || tc.parentTimeout < time.Duration(tc.timeout)*time.Millisecond) {
				if !deadline.Equal(parentDeadline) {
					t.Fatalf("nested deadline=%v, parent=%v", deadline, parentDeadline)
				}
			} else if tc.timeout > 0 {
				duration := time.Duration(tc.timeout) * time.Millisecond
				if deadline.Before(before.Add(duration)) || deadline.After(after.Add(duration)) {
					t.Fatalf("explicit timeout was changed: deadline=%v", deadline)
				}
			}
		})
	}
	for _, timeout := range []int{-1, math.MaxInt64/int(time.Millisecond) + 1} {
		_, err := nodeService(t).Run(context.Background(), RunRequest{Code: `return true`, TimeoutMS: timeout}, RunOptions{CWD: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "timeout_ms") {
			t.Fatalf("invalid timeout %d accepted: %v", timeout, err)
		}
	}
}

func TestNodeCancellationStopsBindings(t *testing.T) {
	for _, tc := range []struct {
		name, code   string
		closeService bool
	}{
		{"waiting binding", `await tools.wait({})`, false},
		{"busy guest", `tools.wait({}); for (;;) {}`, false},
		{"service shutdown", `tools.wait({}); for (;;) {}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := nodeService(t)
			entered := make(chan struct{})
			stopped := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
				close(entered)
				<-ctx.Done()
				close(stopped)
				return toolresult.Result{}, ctx.Err()
			})}
			done := make(chan RunResult, 1)
			go func() {
				result, err := s.Run(ctx, RunRequest{Code: tc.code, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
				if err != nil {
					result.Error = err.Error()
				}
				done <- result
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("binding never started")
			}
			shutdown := make(chan error, 1)
			if tc.closeService {
				go func() { shutdown <- s.Close() }()
			} else {
				cancel()
			}
			select {
			case result := <-done:
				if result.Error != context.Canceled.Error() {
					t.Fatalf("cancellation=%+v", result)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cancel did not stop process")
			}
			select {
			case <-stopped:
			default:
				t.Fatal("binding outlived run")
			}
			if tc.closeService {
				select {
				case err := <-shutdown:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("service shutdown did not finish")
				}
			}
		})
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

func TestNodeToolCallErrorRetainsCanonicalResult(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Name == "transport" {
			return toolresult.Result{}, errors.New("transport unavailable")
		}
		return toolresult.Result{
			IsError:           true,
			Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "conflict"}, {Type: toolresult.ContentTypeResourceLink, URI: "https://example.com/conflict", Name: "details"}},
			StructuredContent: json.RawMessage(`{"code":"conflict","retryable":true}`),
			Meta:              json.RawMessage(`{"request_id":"request-1"}`),
		}, nil
	})}
	result, err := s.Run(context.Background(), RunRequest{Code: `try {await tools.fail({})} catch(e) {return {name:e.name,tool:e.toolName,result:{...e.result}};}`, Tools: []ToolDefinition{{Name: "fail"}}}, opts)
	want := `{"name":"ToolCallError","tool":"fail","result":{"content":[{"type":"text","text":"conflict"},{"type":"resource_link","uri":"https://example.com/conflict","name":"details"}],"structured_content":{"code":"conflict","retryable":true},"meta":{"request_id":"request-1"},"is_error":true}}`
	if err != nil || result.Error != "" || string(result.Value) != want {
		t.Fatalf("canonical error result lost=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `const results=[]; try {await tools.transport({})} catch(e) {results.push(typeof e.result)} try {await describeTool("missing")} catch(e) {results.push(typeof e.result)} return results;`, Tools: []ToolDefinition{{Name: "transport"}}}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `["undefined","undefined"]` {
		t.Fatalf("synthetic errors acquired fake results=%+v %v", result, err)
	}
}

// Exercise the real interpreter and wire: duplicate result views must not leak
// into output, while computation, checkpoints, and error recovery stay lossless.
func TestNodeToolResultOutputUsesOneProjection(t *testing.T) {
	s := nodeService(t)
	view := "visible evidence"
	opts := RunOptions{CWD: t.TempDir(), StateScope: "result-output", Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return toolresult.Result{
			Content:           []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "raw evidence"}},
			StructuredContent: json.RawMessage(`{"count":42}`),
			ModelText:         &view,
		}, nil
	})}
	result, err := s.Run(context.Background(), RunRequest{Code: `
const result = await tools.read({});
if (result.content[0].text !== "raw evidence" || result.structured_content.count !== 42) throw new Error("lost program data");
store("result", result);
text(result);
console.log({results: [result]});
return result;`, Tools: []ToolDefinition{{Name: "read"}}}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `"visible evidence"` || strings.Join(result.Logs, "\n") != "visible evidence\n{\"results\":[\"visible evidence\"]}" {
		t.Fatalf("result output repeated raw data: %+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `const saved = load("result"); return [saved.content[0].text, saved.structured_content.count];`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `["raw evidence",42]` {
		t.Fatalf("output projection changed checkpoints: %+v %v", result, err)
	}
	// Matching property names in ordinary user data do not make a tool result.
	result, err = s.Run(context.Background(), RunRequest{Code: `return {content: "user data", model_text: "keep both"};`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"content":"user data","model_text":"keep both"}` {
		t.Fatalf("ordinary data was projected: %+v %v", result, err)
	}
}

func TestNodeToolResultOutputPreservesEmptyAndFallbackViews(t *testing.T) {
	s := nodeService(t)
	for _, mode := range []ResultView{ResultViewCompact, ResultViewData} {
		for _, view := range []*string{nil, new(string)} {
			opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
				return toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "raw evidence"}}, ModelText: view}, nil
			})}
			result, err := s.Run(context.Background(), RunRequest{ResultView: mode, Code: `console.log(await tools.read({}));`, Tools: []ToolDefinition{{Name: "read"}}}, opts)
			want := "raw evidence"
			if view != nil && mode == ResultViewCompact {
				want = ""
			}
			if err != nil || result.Error != "" || len(result.Logs) != 1 || result.Logs[0] != want {
				t.Fatalf("empty/fallback output (%s): %+v %v", mode, result, err)
			}
		}
	}
}

func TestNodeUnsupportedRuntimeHasActionableStartupError(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for PTC integration tests:", err)
	}
	for _, tc := range []struct{ name, prelude string }{
		{"older major", `Object.defineProperty(process.versions,"node",{value:"20.19.0"});`},
		{"older minor", `Object.defineProperty(process.versions,"node",{value:"22.18.0"});`},
		{"missing TypeScript support", `import legacyModule from "node:module"; legacyModule.stripTypeScriptTypes=undefined; legacyModule.syncBuiltinESMExports();`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, node, "--input-type=module", "-e", tc.prelude+"\n"+bootstrap)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "PTC requires Node.js 22.19+ or the desktop Node runtime") {
				t.Fatalf("startup was not actionable: err=%v output=%s", err, output)
			}
		})
	}
}
