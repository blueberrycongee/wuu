package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	plugingo "github.com/blueberrycongee/wuu/packages/plugin-go"
)

func mustRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestProcessClientLifecycle(t *testing.T) {
	if os.Getenv("WUU_PLUGINHOST_HELPER") == "1" {
		runPluginHelper()
		return
	}
	root := t.TempDir()
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "test-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessClientLifecycle"},
		Env:         map[string]string{"WUU_PLUGINHOST_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Status().State != StateActive {
		t.Fatalf("status = %+v", client.Status())
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.Status().State != StateStopped {
		t.Fatalf("status = %+v", client.Status())
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
			result = InitializeResult{}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
			return
		default:
			_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]string{"message": fmt.Sprintf("unknown method %s", req.Method)}})
			continue
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
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
	client, err := Start(context.Background(), ProcessConfig{
		ID:          "oversize-plugin",
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestProcessOversizeResponse"},
		Env:         map[string]string{"WUU_PLUGINHOST_OVERSIZE_HELPER": "1"},
		PluginRoot:  root,
		ProjectRoot: filepath.Dir(root),
		WuuHome:     t.TempDir(),
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	_, err = client.InvokeCapability(context.Background(), CapabilityInvokeParams{Capability: CapabilityPluginClientRequest, Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected oversize response error")
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
			// Produce one JSON-lines token larger than maxResponseLineSize.
			big := strings.Repeat("x", maxResponseLineSize+1024)
			payload, _ := json.Marshal(map[string]string{"message": big})
			result = CapabilityInvokeResult{Output: payload}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
			return
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
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
	_ = plugingo.Serve(context.Background(), plugingo.Handler{
		Definition: plugingo.Definition{Tools: []plugingo.Tool{
			{ID: "wait", Description: "block until canceled", InputSchema: map[string]any{"type": "object"}},
			{ID: "ping", Description: "answer promptly", InputSchema: map[string]any{"type": "object"}},
		}},
		ExecuteTool: func(ctx context.Context, host plugingo.Host, call plugingo.ToolCall) (plugingo.ToolResult, error) {
			if call.ToolID == "wait" {
				_ = plugingo.ReportExecutionUpdate(ctx, host, plugingo.ExecutionUpdate{ExecutionID: call.ExecutionID, Message: "waiting"})
				<-ctx.Done()
				return plugingo.TextResult("aborted"), nil
			}
			return plugingo.TextResult("pong"), nil
		},
	})
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
