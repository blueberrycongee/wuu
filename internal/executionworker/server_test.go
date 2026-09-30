package executionworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestWorkerFilesystemAndPermissionBoundary(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	s := New()
	defer s.Close()
	initData, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "test-session"})
	if _, err := s.Handle(context.Background(), "initialize", initData); err != nil {
		t.Fatal(err)
	}
	call := func(name, args, mode string) (toolresult.Result, error) {
		data, _ := json.Marshal(executionenv.ToolRequest{Call: providers.ToolCall{Name: name, Arguments: args}, PermissionMode: mode, Actor: "root"})
		raw, err := s.Handle(context.Background(), "execute", data)
		var result toolresult.Result
		if err == nil {
			err = json.Unmarshal(raw, &result)
		}
		return result, err
	}
	if _, err := call("write_file", `{"path":"hello.txt","content":"remote contents"}`, "standard"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := call("read_file", `{"path":"hello.txt"}`, "read_only"); err != nil {
		t.Fatal(err)
	}
	if _, err := call("write_file", `{"path":"denied.txt","content":"no"}`, "read_only"); err == nil {
		t.Fatal("read-only write succeeded")
	}
	if _, err := call("read_file", `{"path":"../outside.txt"}`, "standard"); err == nil {
		t.Fatal("file escaped worker workspace")
	}
	if _, err := call("notes", `{"action":"read"}`, "standard"); err == nil {
		t.Fatal("host control tool accepted by worker")
	}
}

func TestWorkerRejectsProtocolMismatch(t *testing.T) {
	for _, version := range []int{executionenv.ProtocolVersion - 1, executionenv.ProtocolVersion + 1} {
		s := New()
		data, _ := json.Marshal(executionenv.Init{Version: version, Root: t.TempDir(), Session: "test"})
		if _, err := s.Handle(context.Background(), "initialize", data); err == nil {
			t.Fatal("protocol mismatch accepted")
		}
		s.Close()
	}
}

func TestWorkerProgramUsesToolOnlyAuthority(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	s := New()
	defer s.Close()
	initData, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "code-test"})
	if _, err := s.Handle(context.Background(), "initialize", initData); err != nil {
		t.Fatal(err)
	}
	invoked := 0
	ctx := toolctx.WithNestedExecutor(context.Background(), callbackExecutor{invoke: func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("omitted program timeout imposed a worker deadline")
		}
		invoked++
		return toolresult.FromText(call.Name), nil
	}})
	for _, test := range []struct {
		code   string
		failed bool
	}{
		{`return (await tools.read_file({path:'file.txt'})).content[0].text`, false},
		{`await import('node:fs/promises')`, true},
	} {
		data, _ := json.Marshal(executionenv.CodeRequest{Actor: "a", PermissionMode: "unconfined", Program: codemode.RunRequest{Code: test.code, Tools: []codemode.ToolDefinition{{Name: "read_file"}}}})
		raw, err := s.Handle(ctx, "run_code", data)
		if err != nil {
			t.Fatal(err)
		}
		var result codemode.RunResult
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if (result.Error != "") != test.failed {
			t.Fatalf("program result=%+v", result)
		}
	}
	if invoked != 1 {
		t.Fatalf("unexpected nested calls: %d", invoked)
	}
}

// Run the real worker transport in a subprocess so protocol cancellation and
// host-routed tool callbacks cross the same stdio boundary as remote execution.
func TestWorkerTransportProcess(t *testing.T) {
	if os.Getenv("WUU_EXECUTION_TEST_WORKER") != "1" {
		return
	}
	if err := Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestWorkerTransportProgramCancellation(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
	client := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, os.Environ())
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close worker transport: %v", err)
		}
	})
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	if _, err := client.Call(initCtx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: t.TempDir(), Session: "program-cancellation"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		code      string
		timeoutMS int
		cancel    bool
	}{
		{name: "caller_cancellation", code: `await tools.wait({})`, cancel: true},
		{name: "caller_cancellation_busy_guest", code: `tools.wait({}); for (;;) {}`, cancel: true},
		{name: "explicit_timeout", code: `await tools.wait({})`, timeoutMS: 1000},
		{name: "unawaited_call_cleanup", code: `tools.wait({}); await tools.ready({}); return 7;`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, stopped := make(chan struct{}), make(chan struct{})
			type outcome struct {
				data json.RawMessage
				err  error
			}
			done := make(chan outcome, 1)
			request := executionenv.CodeRequest{Actor: "a", PermissionMode: "unconfined", TimeoutMS: test.timeoutMS,
				Program: codemode.RunRequest{Code: test.code, Tools: []codemode.ToolDefinition{{Name: "wait"}, {Name: "ready"}}}}
			go func() {
				data, err := client.CallWithHandler(ctx, "run_code", request, func(callCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
					var call providers.ToolCall
					if err := json.Unmarshal(raw, &call); err != nil {
						return nil, err
					}
					switch call.Name {
					case "wait":
						if test.timeoutMS == 0 {
							if _, ok := callCtx.Deadline(); ok {
								t.Error("omitted program timeout imposed a host callback deadline")
							}
						}
						close(entered)
						<-callCtx.Done()
						close(stopped)
						return nil, callCtx.Err()
					case "ready":
						select {
						case <-entered:
							return json.Marshal(toolresult.FromText("ready"))
						case <-callCtx.Done():
							return nil, callCtx.Err()
						}
					default:
						return nil, fmt.Errorf("unexpected callback %q", call.Name)
					}
				})
				done <- outcome{data, err}
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("host callback did not start")
			}
			if test.cancel {
				cancel()
			}
			select {
			case result := <-done:
				if test.cancel {
					if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("caller cancellation: %v", result.err)
					}
				} else {
					if result.err != nil {
						t.Fatal(result.err)
					}
					var program codemode.RunResult
					if err := json.Unmarshal(result.data, &program); err != nil {
						t.Fatal(err)
					}
					if test.timeoutMS > 0 {
						if program.Error != context.DeadlineExceeded.Error() {
							t.Fatalf("explicit timeout: %+v", program)
						}
					} else if program.Error != "" || string(program.Value) != "7" {
						t.Fatalf("normal completion: %+v", program)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("program did not finish")
			}
			// Host cleanup is asynchronous; the scope must still be revoked on
			// every exit path, including a deadline imposed only by the worker.
			select {
			case <-stopped:
			case <-time.After(10 * time.Second):
				t.Fatal("host callback outlived its program scope")
			}
			nextCtx, nextCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer nextCancel()
			request.Program = codemode.RunRequest{Code: `return 9`}
			request.TimeoutMS = 0
			raw, err := client.Call(nextCtx, "run_code", request)
			if err != nil {
				t.Fatalf("transport did not recover after program completion: %v", err)
			}
			var result codemode.RunResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Error != "" || string(result.Value) != "9" {
				t.Fatalf("next program: %+v %v", result, err)
			}
		})
	}
}

func TestWorkerTransportStateIsActorScopedAndVolatile(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
	root := t.TempDir()
	newClient := func() *executionenv.Client {
		client := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, os.Environ())
		t.Cleanup(func() { _ = client.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "state-conversation"}); err != nil {
			t.Fatal(err)
		}
		return client
	}
	run := func(client *executionenv.Client, actor, code, want string, failed bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		raw, err := client.Call(ctx, "run_code", executionenv.CodeRequest{Actor: actor, PermissionMode: "unconfined", Program: codemode.RunRequest{Code: code}})
		var result codemode.RunResult
		if err != nil || json.Unmarshal(raw, &result) != nil || (result.Error != "") != failed || (!failed && string(result.Value) != want) {
			t.Fatalf("worker state=%s err=%v", raw, err)
		}
	}
	client := newClient()
	run(client, "root", `store("checkpoint",{done:2}); return true;`, `true`, false)
	run(client, "child", `return typeof load("checkpoint");`, `"undefined"`, false)
	run(client, "root", `store("checkpoint",3); throw new Error("failed");`, "", true)
	run(client, "root", `return load("checkpoint");`, `{"done":2}`, false)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	run(newClient(), "root", `return typeof load("checkpoint");`, `"undefined"`, false)
}

func TestCanceledStdinWriteStillAllowsStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix PTY")
	}
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
	client := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, os.Environ())
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: t.TempDir(), Session: "stdin-recovery"}); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Call(ctx, "execute", executionenv.ToolRequest{Actor: "a", PermissionMode: "unconfined", Call: providers.ToolCall{Name: "bash", Arguments: `{"command":"stty raw -echo; echo READY; sleep 60","run_in_background":true}`}})
	if err != nil {
		t.Fatal(err)
	}
	var result toolresult.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var proc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(result.TextProjection()), &proc); err != nil || proc.ID == "" {
		t.Fatalf("start %s, %v", raw, err)
	}
	raw, err = client.Call(ctx, "process/read", map[string]any{"id": proc.ID, "options": map[string]any{"OffsetBytes": 0, "Wait": int64(2 * time.Second)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "READY") {
		t.Fatalf("process not ready: %s", raw)
	}
	writeCtx, stopWrite := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = client.Call(writeCtx, "process/write", map[string]any{"id": proc.ID, "input": strings.Repeat("x", 64*1024)})
	stopWrite()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected full stdin pipe: %v", err)
	}
	if client.Failed() {
		t.Fatal("write cancellation interrupted transport setup rather than blocked process input")
	}
	stopCtx, stopStop := context.WithTimeout(ctx, 2*process.DefaultStopGracePeriod+time.Second)
	defer stopStop()
	_, err = client.Call(stopCtx, "process/stop", map[string]string{"id": proc.ID})
	if err != nil {
		t.Fatalf("cancelled stdin write blocks stop recovery: %v", err)
	}
}
