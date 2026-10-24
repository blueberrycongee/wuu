package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func mustRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

// pauseUntilReaderDone forwards Close unchanged so transport retirement can
// finish while the caller is paused after delivering its complete request.
type pauseUntilReaderDone struct {
	io.WriteCloser
	done <-chan struct{}
}

func (w pauseUntilReaderDone) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	if err == nil {
		<-w.done
	}
	return n, err
}

func TestProcessClientLifecycle(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_HELPER") == "1" {
		runPluginHelper()
		return
	}
	for _, mode := range []string{"normal", "stderr-tail", "descendant", "blocked-write", "cancel-delivery", "shutdown-failure", "shutdown-rejection", "shutdown-rejection-exit", "shutdown-no-response", "shutdown-timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			env := map[string]string{"WUU_PLUGINHOST_HELPER": "1", "WUU_PLUGINHOST_MODE": mode}
			var listener *net.TCPListener
			if mode == "descendant" || mode == "blocked-write" || mode == "cancel-delivery" {
				var err error
				listener, err = net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
				env["WUU_PLUGINHOST_CHILD_ADDR"] = listener.Addr().String()
			}
			timeout := 2 * time.Second
			if mode == "cancel-delivery" {
				timeout = 5 * time.Second
			}
			var closed atomic.Int32
			client, err := Start(context.Background(), ProcessConfig{
				ID: "test-plugin", Command: os.Args[0],
				Args: []string{"-test.run=^TestProcessClientLifecycle$"}, Env: env,
				PluginRoot: root, ProjectRoot: filepath.Dir(root), WuuHome: t.TempDir(),
				Timeout: timeout, OnClose: func() { closed.Add(1) },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(context.Background())
			if client.Status().State != StateActive {
				t.Fatalf("status = %+v", client.Status())
			}
			var conn net.Conn
			if listener != nil {
				conn, err = listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
			}
			if mode == "cancel-delivery" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() {
					_, _ = client.ExecuteTool(ctx, ToolExecuteParams{
						ToolID: "wait", ToolExecuteInput: ToolExecuteInput{ExecutionID: "running"},
					})
				}()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var ready [1]byte
				if _, err := io.ReadFull(conn, ready[:]); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, _ = client.ExecuteTool(context.Background(), ToolExecuteParams{
						ToolID: "blocked", ToolExecuteInput: ToolExecuteInput{Arguments: mustRaw(strings.Repeat("x", 2<<20))},
					})
				}()
				if _, err := io.ReadFull(conn, ready[:]); err != nil {
					t.Fatal(err)
				}
				// An ordinary request that never acquires the writer may be
				// canceled without retiring other, already-running work.
				queuedCtx, queuedCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				_, queuedErr := client.ExecuteTool(queuedCtx, ToolExecuteParams{ToolID: "queued"})
				queuedCancel()
				if !errors.Is(queuedErr, context.DeadlineExceeded) || client.Status().State != StateActive {
					t.Fatalf("queued request cancellation = %v, status = %+v", queuedErr, client.Status())
				}
				cancel()
				select {
				case <-client.readerDone:
				case <-time.After(2 * time.Second):
					t.Fatal("undelivered execution cancellation left the runtime running")
				}
				if status := client.Status(); status.State != StateFailed || !strings.Contains(status.Error, "running") {
					t.Fatalf("lost cancellation diagnostic = %+v", status)
				}
			}
			if mode == "blocked-write" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					_, err := client.ExecuteTool(ctx, ToolExecuteParams{
						ToolID: "blocked", ToolExecuteInput: ToolExecuteInput{
							ExecutionID: "blocked", Arguments: mustRaw(strings.Repeat("x", 2<<20)),
						},
					})
					done <- err
				}()
				// The runtime acknowledges one byte of the next frame and then
				// stops reading, so cancellation always exercises an active write.
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var ready [1]byte
				if _, err := io.ReadFull(conn, ready[:]); err != nil {
					t.Fatal(err)
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("blocked write = %v, want canceled", err)
					}
				case <-time.After(3 * time.Second):
					_ = conn.Close()
					<-done
					t.Fatal("cancellation did not interrupt the blocked plugin write")
				}
				if client.Status().State != StateFailed {
					t.Fatalf("partially written transport remains usable: %+v", client.Status())
				}
			}
			if mode == "shutdown-rejection-exit" {
				// A caller may be descheduled after sending shutdown. Wait for
				// real process EOF so the complete reply and readerDone are both
				// ready when call resumes, without injecting a response or sleep.
				client.stopMu.Lock()
				client.stdin = pauseUntilReaderDone{WriteCloser: client.stdin, done: client.readerDone}
				client.stopMu.Unlock()
			}
			var closeErr error
			done := make(chan error, 1)
			go func() { done <- client.Close(context.Background()) }()
			select {
			case err := <-done:
				closeErr = err
				if mode == "shutdown-rejection" || mode == "shutdown-rejection-exit" {
					var remoteErr *remoteCallError
					if !errors.As(err, &remoteErr) || remoteErr.message != "synthetic shutdown probe failed" {
						t.Errorf("shutdown rejection was lost: %T %v", err, err)
					}
				} else if mode == "shutdown-failure" {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
						t.Fatalf("shutdown failure = %v, want exit status 42", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				if conn != nil {
					_ = conn.Close()
				}
				<-done
				t.Fatal("plugin shutdown did not finish within its retirement budget")
			}
			if client.Status().State != StateStopped {
				t.Fatalf("status = %+v", client.Status())
			}
			if client.cmd.ProcessState == nil || client.processCtx.Err() == nil {
				t.Fatal("shutdown returned without retiring the runtime")
			}
			if err := client.Close(context.Background()); err != nil && (closeErr == nil || err.Error() != closeErr.Error()) {
				t.Fatalf("repeated close introduced a new failure: %v (first: %v)", err, closeErr)
			}
			if got := closed.Load(); got != 1 {
				t.Fatalf("runtime cleanup called %d times, want exactly once", got)
			}
			if mode == "stderr-tail" {
				stderr := client.stderr.String()
				if len(stderr) > (64<<10)+100 || !strings.Contains(stderr, "truncated") || !strings.HasSuffix(stderr, "final plugin diagnostic") {
					t.Fatalf("stderr tail lost its bound or final diagnostic: length=%d", len(stderr))
				}
			}
			if mode == "descendant" {
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				var b [1]byte
				if _, err := conn.Read(b[:]); err == nil {
					t.Fatal("unexpected descendant output")
				} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					t.Fatal("plugin shutdown left its descendant running")
				}
			}
		})
	}
}

func TestProcessClientNegotiatesAndInvokesCapability(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_CAPABILITY_HELPER") == "1" {
		runCapabilityHelper()
		return
	}
	root := t.TempDir()
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "capability-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessClientNegotiatesAndInvokesCapability"},
		Env:         map[string]string{"WUU_PLUGINHOST_CAPABILITY_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if client.ProtocolVersion() != CapabilityProtocolVersion {
		t.Fatalf("protocol = %d", client.ProtocolVersion())
	}
	capabilities := client.Capabilities()
	if len(capabilities) != 1 || capabilities[0].ID != CapabilityAgentRequestTransform {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	output, _ := json.Marshal(RequestTransformOutput{})
	result, err := client.InvokeCapability(context.Background(), CapabilityInvokeParams{
		Capability: CapabilityAgentRequestTransform,
		Input:      json.RawMessage(`{"provider":"test"}`),
		Output:     output,
	})
	if err != nil {
		t.Fatal(err)
	}
	var transformed RequestTransformOutput
	if err := json.Unmarshal(result.Output, &transformed); err != nil {
		t.Fatal(err)
	}
	if len(transformed.PrependSystemMessages) != 1 || transformed.PrependSystemMessages[0] != "after" {
		t.Fatalf("output = %+v", transformed)
	}
}

func runCapabilityHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var result any
		switch req.Method {
		case "initialize":
			var params CapabilityInitializeParams
			_ = json.Unmarshal(req.Params, &params)
			if params.ProtocolVersion != ProtocolVersion || params.CapabilityProtocolVersion != CapabilityProtocolVersion {
				os.Exit(3)
			}
			result = CapabilityInitializeResult{
				ProtocolVersion: CapabilityProtocolVersion,
				Capabilities: []CapabilityDescriptor{{
					ID: CapabilityAgentRequestTransform, Kind: "transform", Version: 1,
				}},
			}
		case "capability.invoke":
			var params CapabilityInvokeParams
			_ = json.Unmarshal(req.Params, &params)
			if params.Capability != CapabilityAgentRequestTransform {
				os.Exit(4)
			}
			var output RequestTransformOutput
			_ = json.Unmarshal(params.Output, &output)
			output.PrependSystemMessages = []string{"after"}
			data, _ := json.Marshal(output)
			result = CapabilityInvokeResult{Output: data}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
			return
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
	}
}

func runPluginHelper() {
	if address := os.Getenv("WUU_PLUGINHOST_DESCENDANT_ADDR"); address != "" {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			os.Exit(1)
		}
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
		return
	}
	mode := os.Getenv("WUU_PLUGINHOST_MODE")
	if mode == "descendant" {
		child := exec.Command(os.Args[0], "-test.run=^TestProcessClientLifecycle$")
		child.Env = append(os.Environ(), "WUU_PLUGINHOST_DESCENDANT_ADDR="+os.Getenv("WUU_PLUGINHOST_CHILD_ADDR"))
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			var params InitializeParams
			_ = json.Unmarshal(req.Params, &params)
			if params.ProtocolVersion != ProtocolVersion {
				os.Exit(3)
			}
			if mode == "stderr-tail" {
				_, _ = io.WriteString(os.Stderr, strings.Repeat("x", 128<<10)+"final plugin diagnostic")
			}
			result = InitializeResult{}
		case "shutdown":
			switch mode {
			case "shutdown-failure":
				os.Exit(42)
			case "shutdown-rejection", "shutdown-rejection-exit":
				_ = enc.Encode(rpcResponse{ID: req.ID, Error: &rpcError{Message: "synthetic shutdown probe failed"}})
				if mode == "shutdown-rejection-exit" {
					os.Exit(0)
				}
				continue
			case "shutdown-no-response":
				os.Exit(0)
			case "shutdown-timeout":
				// Keep the transport open without acknowledging shutdown. The
				// host must still force retirement within its existing budget.
				_, _ = io.Copy(io.Discard, os.Stdin)
				return
			}
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
			return
		default:
			_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]string{"message": fmt.Sprintf("unknown method %s", req.Method)}})
			continue
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
		if (mode == "blocked-write" || mode == "cancel-delivery") && req.Method == "initialize" {
			conn, err := net.Dial("tcp", os.Getenv("WUU_PLUGINHOST_CHILD_ADDR"))
			if err != nil {
				os.Exit(1)
			}
			if mode == "cancel-delivery" {
				if !scanner.Scan() {
					os.Exit(1)
				}
				_, _ = conn.Write([]byte{1})
			}
			var first [1]byte
			if _, err := io.ReadFull(os.Stdin, first[:]); err != nil {
				os.Exit(1)
			}
			_, _ = conn.Write([]byte{1})
			_, _ = io.Copy(io.Discard, conn)
			_ = conn.Close()
			return
		}
	}
}

func TestBuildEnvUsesBaselineAndExplicitOverrides(t *testing.T) {
	// Set baseline variables that should be inherited.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("LANG", "en_US.UTF-8")

	// A parent secret must not leak into the plugin environment.
	t.Setenv("WUU_PARENT_SECRET_TOKEN", "super-secret")

	got := buildEnv(map[string]string{"EXPLICIT": "yes", "PATH": "/override"})
	m := envSliceToMap(got)

	if m["PATH"] != "/override" {
		t.Fatalf("explicit override lost: PATH=%q", m["PATH"])
	}
	if m["HOME"] != "/home/test" {
		t.Fatalf("baseline HOME missing: %q", m["HOME"])
	}
	if m["LANG"] != "en_US.UTF-8" {
		t.Fatalf("baseline LANG missing: %q", m["LANG"])
	}
	if m["EXPLICIT"] != "yes" {
		t.Fatalf("explicit env missing: %q", m["EXPLICIT"])
	}
	if _, ok := m["WUU_PARENT_SECRET_TOKEN"]; ok {
		t.Fatalf("parent secret inherited: %v", m)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("env slice not sorted: %v", got)
	}
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		key, value, _ := strings.Cut(e, "=")
		m[key] = value
	}
	return m
}

func TestProcessEnvDoesNotInheritSecrets(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_ENV_HELPER") == "1" {
		runEnvHelper()
		return
	}
	t.Setenv("WUU_PARENT_SECRET_TOKEN", "super-secret")

	root := t.TempDir()
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "env-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessEnvDoesNotInheritSecrets"},
		Env:         map[string]string{"WUU_PLUGINHOST_ENV_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	result, err := client.InvokeCapability(context.Background(), CapabilityInvokeParams{
		Capability: CapabilityPluginClientRequest,
		Input:      mustRaw(map[string]string{"name": "WUU_PARENT_SECRET_TOKEN"}),
		Output:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out["value"] != "" {
		t.Fatalf("parent secret token was inherited: %q", out["value"])
	}
}

func runEnvHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion, Capabilities: []CapabilityDescriptor{{ID: CapabilityPluginClientRequest, Kind: SeamDecision, Version: 1}}}
		case "capability.invoke":
			var params CapabilityInvokeParams
			_ = json.Unmarshal(req.Params, &params)
			var query map[string]string
			_ = json.Unmarshal(params.Input, &query)
			value := os.Getenv(query["name"])
			data, _ := json.Marshal(map[string]string{"value": value})
			result = CapabilityInvokeResult{Output: data}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
			return
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
	}
}

func TestProcessOversizeResponse(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_OVERSIZE_HELPER") == "1" {
		runOversizeHelper()
		return
	}
	root := t.TempDir()
	var closed atomic.Int32
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "oversize-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessOversizeResponse"},
		Env:         map[string]string{"WUU_PLUGINHOST_OVERSIZE_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     5 * time.Second,
		OnClose:     func() { closed.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Logf("close: %v", err)
		}
		if got := closed.Load(); got != 1 {
			t.Errorf("runtime cleanup called %d times, want exactly once", got)
		}
	}()

	_, err = client.InvokeCapability(context.Background(), CapabilityInvokeParams{Capability: CapabilityPluginClientRequest, Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected oversize response error")
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("protocol failure left runtime resources open: cleanup calls = %d", got)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func runOversizeHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion, Capabilities: []CapabilityDescriptor{{ID: CapabilityPluginClientRequest, Kind: SeamDecision, Version: 1}}}
		case "capability.invoke":
			// Stream the same oversized JSONL response directly. Repeatedly
			// marshaling a 64 MiB RawMessage makes the race-instrumented fixture
			// hit its call deadline before it exercises the scanner's bound.
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%q,"result":{"output":{"message":"`, req.ID)
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", maxResponseLineSize+1024))
			_, _ = io.WriteString(os.Stdout, "\"}}}\n")
			continue
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
			return
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
	}
}

func TestProcessClientReceiptCompletion(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_RECEIPT_HELPER") == "1" {
		runReceiptCompletionHelper()
		return
	}
	for _, mode := range []string{"ordinary cancellation", "response", "rejection", "transport exit", "timeout", "closed scope"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
			root := t.TempDir()
			client, err := Start(context.Background(), ProcessConfig{
				ID: "receipt-plugin", Command: os.Args[0],
				Args: []string{"-test.run=^TestProcessClientReceiptCompletion$"},
				Env: map[string]string{
					"WUU_PLUGINHOST_RECEIPT_HELPER": "1",
					"WUU_PLUGINHOST_RECEIPT_ADDR":   listener.Addr().String(),
				},
				PluginRoot: root, ProjectRoot: filepath.Dir(root), WuuHome: t.TempDir(),
				Timeout: 2 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(context.Background())
			control, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			_ = control.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(control)
			expect := func(want string) {
				t.Helper()
				got, err := reader.ReadString('\n')
				if err != nil || got != want+"\n" {
					t.Fatalf("process gate = %q, %v; want %q", got, err, want)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var receipts *providers.ModelReceiptScope
			if mode != "ordinary cancellation" {
				ctx, receipts = providers.NewModelReceiptScope(ctx)
				defer receipts.Seal()
			}
			if mode == "closed scope" {
				receipts.Seal()
				closedCtx, closedCancel := context.WithTimeout(ctx, time.Second)
				defer closedCancel()
				_, err := client.InvokeCapability(closedCtx, CapabilityInvokeParams{
					Capability: CapabilityModelMiddleware, ExecutionID: "receipt-execution", Input: mustRaw("hold"),
				})
				if !errors.Is(err, providers.ErrModelReceiptScopeClosed) {
					t.Fatalf("closed scope invocation = %v, want admission rejection", err)
				}
				if client.Status().State != StateActive {
					t.Fatalf("local receipt rejection retired the process: %+v", client.Status())
				}
				client.pendingMu.Lock()
				pending := len(client.pending)
				client.pendingMu.Unlock()
				if pending != 0 {
					t.Fatalf("rejected scope left %d pending calls", pending)
				}
				return
			}
			returned := make(chan error, 1)
			go func() {
				_, err := client.InvokeCapability(ctx, CapabilityInvokeParams{
					Capability: CapabilityModelMiddleware, ExecutionID: "receipt-execution", Input: mustRaw("hold"),
				})
				returned <- err
			}()
			expect("invoked")
			cancel()
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("consumer error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("consumer waited for the gated process response after cancellation")
			}
			expect("canceled")
			if receipts != nil {
				receipts.Seal()
				select {
				case <-receipts.Done():
					t.Fatal("receipt scope completed while the original RPC response is still gated")
				default:
				}
			}
			client.pendingMu.Lock()
			pending := len(client.pending)
			client.pendingMu.Unlock()
			wantPending := 0
			if receipts != nil {
				wantPending = 1
			}
			if pending != wantPending {
				t.Fatalf("pending calls after cancellation = %d, want %d", pending, wantPending)
			}
			if mode == "timeout" {
				waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				err := receipts.Wait(waitCtx)
				waitCancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("gated receipt wait = %v, want deadline exceeded", err)
				}
				select {
				case <-receipts.Done():
					t.Fatal("wait timeout claimed the process producer had finished")
				default:
				}
				receipts.Abort(err)
				select {
				case <-receipts.Done():
				case <-time.After(time.Second):
					t.Fatal("owner abort did not release the pending RPC observer")
				}
				if err := receipts.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("abandoned receipt = %v, want the owner's deadline cause", err)
				}
				client.pendingMu.Lock()
				pending = len(client.pending)
				client.pendingMu.Unlock()
				if pending != 0 {
					t.Fatalf("owner abort left %d pending calls", pending)
				}
				if client.Status().State != StateActive {
					t.Fatalf("observation abort changed process lifecycle: %+v", client.Status())
				}
			}
			if mode == "transport exit" {
				if _, err := io.WriteString(control, "exit\n"); err != nil {
					t.Fatal(err)
				}
				waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer waitCancel()
				if err := receipts.Wait(waitCtx); err == nil || errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("receipt after process exit = %v, want transport failure", err)
				}
				client.pendingMu.Lock()
				pending = len(client.pending)
				client.pendingMu.Unlock()
				if pending != 0 {
					t.Fatalf("process exit left %d pending calls", pending)
				}
				return
			}
			command := "release\n"
			if mode == "rejection" {
				command = "reject\n"
			}
			if _, err := io.WriteString(control, command); err != nil {
				t.Fatal(err)
			}
			expect("released")
			if receipts != nil && mode != "timeout" {
				waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
				defer waitCancel()
				if err := receipts.Wait(waitCtx); err != nil {
					t.Fatalf("completed callback receipt = %v", err)
				}
				client.pendingMu.Lock()
				pending = len(client.pending)
				client.pendingMu.Unlock()
				if pending != 0 {
					t.Fatalf("completed callback left %d pending calls", pending)
				}
			}
			// A subsequent reply proves the discarded late response did not block
			// the real JSONL reader or replay the canceled model invocation.
			pingCtx, pingCancel := context.WithTimeout(context.Background(), time.Second)
			defer pingCancel()
			if _, err := client.InvokeCapability(pingCtx, CapabilityInvokeParams{
				Capability: CapabilityModelMiddleware, ExecutionID: "ping", Input: mustRaw("ping"),
			}); err != nil {
				t.Fatalf("ping after late reply: %v", err)
			}
		})
	}
}

func runReceiptCompletionHelper() {
	control, err := net.Dial("tcp", os.Getenv("WUU_PLUGINHOST_RECEIPT_ADDR"))
	if err != nil {
		os.Exit(2)
	}
	defer control.Close()
	type request struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	requests := make(chan request)
	commands := make(chan string)
	go func() {
		defer close(requests)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var req request
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				os.Exit(3)
			}
			requests <- req
		}
	}()
	go func() {
		defer close(commands)
		scanner := bufio.NewScanner(control)
		for scanner.Scan() {
			commands <- scanner.Text()
		}
	}()
	enc := json.NewEncoder(os.Stdout)
	waitingID, executionID := "", ""
	respond := func(id string) {
		_ = enc.Encode(map[string]any{"id": id, "result": CapabilityInvokeResult{Output: json.RawMessage(`{}`)}})
	}
	for {
		select {
		case req, open := <-requests:
			if !open {
				return
			}
			switch req.Method {
			case "initialize":
				_ = enc.Encode(map[string]any{"id": req.ID, "result": CapabilityInitializeResult{
					ProtocolVersion: CapabilityProtocolVersion,
					Capabilities:    []CapabilityDescriptor{{ID: CapabilityModelMiddleware, Kind: SeamDecision, Version: 1}},
				}})
			case "capability.invoke":
				var params CapabilityInvokeParams
				_ = json.Unmarshal(req.Params, &params)
				if string(params.Input) == `"hold"` {
					if waitingID != "" {
						os.Exit(4)
					}
					waitingID, executionID = req.ID, params.ExecutionID
					_, _ = io.WriteString(control, "invoked\n")
				} else {
					respond(req.ID)
				}
			case ExecutionCancelMethod:
				var params ExecutionCancelParams
				_ = json.Unmarshal(req.Params, &params)
				if waitingID != "" && params.ExecutionID == executionID {
					_, _ = io.WriteString(control, "canceled\n")
				}
			case "shutdown":
				respond(req.ID)
				return
			}
		case command, open := <-commands:
			if !open {
				return
			}
			switch command {
			case "release":
				respond(waitingID)
				waitingID, executionID = "", ""
				_, _ = io.WriteString(control, "released\n")
			case "reject":
				_ = enc.Encode(map[string]any{"id": waitingID, "error": rpcError{Code: "canceled", Message: "callback canceled"}})
				waitingID, executionID = "", ""
				_, _ = io.WriteString(control, "released\n")
			case "exit":
				os.Exit(23)
			}
		}
	}
}

func TestProcessClientCancelReachesPluginExecution(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_EXECUTION_HELPER") == "1" {
		runExecutionHelper()
		return
	}
	root := t.TempDir()
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "exec-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessClientCancelReachesPluginExecution"},
		Env:         map[string]string{"WUU_PLUGINHOST_EXECUTION_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if client.Status().State != StateActive {
		t.Fatalf("status = %+v", client.Status())
	}

	callCtx, cancel := context.WithCancel(context.Background())
	// The manifest timeout protects process lifecycle calls, not the tool's
	// execution. Keep this below the wait so the test catches accidental reuse.
	client.config.Timeout = 50 * time.Millisecond
	first := make(chan error, 1)
	go func() {
		_, callErr := client.ExecuteTool(callCtx, ToolExecuteParams{
			ToolExecuteInput: ToolExecuteInput{ExecutionID: "exec-1", CallID: "c1", CWD: root, Tool: "wait", Arguments: json.RawMessage(`{}`)},
			ToolID:           "wait",
		})
		first <- callErr
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case callErr := <-first:
		t.Fatalf("tool execution inherited the process timeout: %v", callErr)
	default:
	}
	cancel()
	if callErr := <-first; callErr == nil || !errors.Is(callErr, context.Canceled) {
		t.Fatalf("first call error = %v, want context.Canceled", callErr)
	}

	// The plugin runtime dispatches serially: ping can only be answered after
	// the canceled wait execution actually aborted inside the plugin.
	ping, err := client.ExecuteTool(context.Background(), ToolExecuteParams{
		ToolExecuteInput: ToolExecuteInput{ExecutionID: "exec-2", CallID: "c2", CWD: root, Tool: "ping", Arguments: json.RawMessage(`{}`)},
		ToolID:           "ping",
	})
	if err != nil {
		t.Fatalf("ping after cancel: %v (the canceled execution did not abort plugin-side)", err)
	}
	if len(ping.Result.Content) != 1 || ping.Result.Content[0].Text != "pong" {
		t.Fatalf("ping result = %+v", ping.Result)
	}
}

func runExecutionHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	waitingID, executionID := "", ""
	var queuedPings []string
	respond := func(id, text string) {
		_ = enc.Encode(map[string]any{"id": id, "result": map[string]any{"result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}}})
	}
	for scanner.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion, InitializeResult: InitializeResult{Tools: []ToolRegistration{
				{ID: "wait", Description: "block until canceled", InputSchema: map[string]any{"type": "object"}},
				{ID: "ping", Description: "answer promptly", InputSchema: map[string]any{"type": "object"}},
			}}}})
		case "tool.execute":
			var call ToolExecuteParams
			_ = json.Unmarshal(req.Params, &call)
			if call.ToolID == "wait" {
				waitingID, executionID = req.ID, call.ExecutionID
				_ = enc.Encode(map[string]any{"id": "fixture-progress", "method": "host.service.call", "params": map[string]any{"service": "execution.update", "method": "call", "execution_id": executionID, "params": map[string]string{"execution_id": executionID, "message": "waiting"}}})
			} else if waitingID != "" {
				queuedPings = append(queuedPings, req.ID)
			} else {
				respond(req.ID, "pong")
			}
		case "execution.cancel":
			var call struct {
				ExecutionID string `json:"execution_id"`
			}
			_ = json.Unmarshal(req.Params, &call)
			if waitingID != "" && call.ExecutionID == executionID {
				respond(waitingID, "aborted")
				waitingID, executionID = "", ""
				for _, id := range queuedPings {
					respond(id, "pong")
				}
				queuedPings = nil
			}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
			return
		}
	}
}

type recordingServiceRouter struct {
	seen chan ServiceCallParams
}

func (r *recordingServiceRouter) RouteServiceCall(_ context.Context, _ string, params ServiceCallParams) (json.RawMessage, *HostServiceError) {
	r.seen <- params
	return json.RawMessage(`{}`), nil
}

func TestProcessClientExecutionUpdateReachesHost(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_EXECUTION_HELPER") == "1" {
		runExecutionHelper()
		return
	}
	root := t.TempDir()
	router := &recordingServiceRouter{seen: make(chan ServiceCallParams, 1)}
	client, err := Start(context.Background(), ProcessConfig{
		ID:            "exec-plugin",
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestProcessClientExecutionUpdateReachesHost"},
		Env:           map[string]string{"WUU_PLUGINHOST_EXECUTION_HELPER": "1"},
		PluginRoot:    root,
		ProjectRoot:   filepath.Dir(root),
		WuuHome:       t.TempDir(),
		Timeout:       2 * time.Second,
		ServiceRouter: router,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if client.Status().State != StateActive {
		t.Fatalf("status = %+v", client.Status())
	}

	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, callErr := client.ExecuteTool(callCtx, ToolExecuteParams{
			ToolExecuteInput: ToolExecuteInput{ExecutionID: "exec-progress", CallID: "c9", CWD: root, Tool: "wait", Arguments: json.RawMessage(`{}`)},
			ToolID:           "wait",
		})
		done <- callErr
	}()

	var update ServiceCallParams
	select {
	case update = <-router.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin progress update did not reach the host")
	}
	cancel()
	<-done
	if update.Service != "execution.update" || update.Method != KernelServiceMethod {
		t.Fatalf("routed service call = %+v", update)
	}
	var payload struct {
		ExecutionID string `json:"execution_id"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(update.Params, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExecutionID != "exec-progress" || payload.Message != "waiting" {
		t.Fatalf("progress payload = %+v", payload)
	}
}
