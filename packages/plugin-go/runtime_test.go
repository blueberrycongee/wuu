package pluginapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeNegotiatesAndInvokesCapability(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":2,"plugin_id":"test"}}`,
		`{"id":"2","method":"capability.invoke","params":{"capability":"test.decision","input":{"value":1}}}`,
		`{"id":"3","method":"shutdown"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	err := ServeIO(context.Background(), strings.NewReader(input), &output, Handler{
		Definition: Definition{Capabilities: []Capability{{ID: "test.decision", Kind: "decision", Version: 1}}},
		InvokeCapability: func(_ context.Context, _ Host, call CapabilityCall) (json.RawMessage, error) {
			if call.Capability != "test.decision" {
				t.Fatalf("capability = %q", call.Capability)
			}
			return json.RawMessage(`{"accepted":true}`), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"protocol_version":3`) || !strings.Contains(lines[1], `"accepted":true`) {
		t.Fatalf("responses = %s", output.String())
	}
}

func TestServeAcceptsTranscriptBearingCapabilityRequestOverThirtyTwoMiB(t *testing.T) {
	largeValue := strings.Repeat("x", 33<<20)
	input := strings.Join([]string{
		`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":2,"plugin_id":"test"}}`,
		fmt.Sprintf(`{"id":"2","method":"capability.invoke","params":{"capability":"test.decision","input":{"transcript":%q}}}`, largeValue),
		`{"id":"3","method":"shutdown"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	err := ServeIO(context.Background(), strings.NewReader(input), &output, Handler{
		Definition: Definition{Capabilities: []Capability{{ID: "test.decision", Kind: "decision", Version: 1}}},
		InvokeCapability: func(_ context.Context, _ Host, call CapabilityCall) (json.RawMessage, error) {
			var payload struct {
				Transcript string `json:"transcript"`
			}
			if err := json.Unmarshal(call.Input, &payload); err != nil {
				return nil, err
			}
			if len(payload.Transcript) != len(largeValue) {
				t.Fatalf("transcript bytes = %d, want %d", len(payload.Transcript), len(largeValue))
			}
			return json.RawMessage(`{"accepted":true}`), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"accepted":true`) {
		t.Fatal("response did not contain accepted result")
	}
}

func TestServeNegotiatesAndInvokesService(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":3,"plugin_id":"search"}}`,
		`{"id":"2","method":"service.invoke","params":{"service":"search.provider","method":"query","caller":"notes","params":{"q":"x"}}}`,
		`{"id":"3","method":"service.changed","params":{"service":"search.provider","reason":"provider_closed"}}`,
		`{"id":"4","method":"shutdown"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	var invoke ServiceCall
	var notice ServiceChangedNotice
	err := ServeIO(context.Background(), strings.NewReader(input), &output, Handler{
		Definition: Definition{
			ProvidedServices: []Service{{
				Name:    "search.provider",
				Version: "1.0.0",
				Methods: []ServiceMethod{{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"}},
			}},
			RequiredServices: []ServiceRequirement{{Name: "memory.index", MajorVersion: 1}},
		},
		InvokeService: func(_ context.Context, _ Host, call ServiceCall) (json.RawMessage, error) {
			invoke = call
			return json.RawMessage(`{"hits":["a"]}`), nil
		},
		ServiceChanged: func(_ context.Context, got ServiceChangedNotice) error {
			notice = got
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if invoke.Caller != "notes" || invoke.Method != "query" {
		t.Fatalf("invoke = %+v", invoke)
	}
	if notice.Reason != "provider_closed" {
		t.Fatalf("notice = %+v", notice)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("responses = %s", output.String())
	}
	if !strings.Contains(lines[0], `"protocol_version":3`) || !strings.Contains(lines[0], `"provided_services"`) || !strings.Contains(lines[0], `"required_services"`) {
		t.Fatalf("initialize response = %s", lines[0])
	}
	if !strings.Contains(lines[1], `"hits":["a"]`) {
		t.Fatalf("service.invoke response = %s", lines[1])
	}
}

func TestCallServiceUsesGatewayFrame(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	defer requestReader.Close()
	client := newClient(requestWriter)
	requestSeen := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(requestReader)
		if !scanner.Scan() {
			return
		}
		requestSeen <- scanner.Text()
		client.routeResponse(rpcResponse{ID: "plugin-1", Result: json.RawMessage(`{"hits":[]}`)})
	}()
	var result struct {
		Hits []string `json:"hits"`
	}
	if err := CallService(context.Background(), client, "search.provider", "query", map[string]string{"q": "x"}, &result); err != nil {
		t.Fatal(err)
	}
	request := <-requestSeen
	if !strings.Contains(request, `"method":"host.service.call"`) || !strings.Contains(request, `"service":"search.provider"`) || !strings.Contains(request, `"method":"query"`) {
		t.Fatalf("gateway request = %s", request)
	}
}

func TestServiceHandlerPropagatesExecutionToNestedCall(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeIO(context.Background(), inputReader, outputWriter, Handler{
			Definition: Definition{ProvidedServices: []Service{{Name: "outer", Version: "1.0.0"}}},
			InvokeService: func(ctx context.Context, host Host, _ ServiceCall) (json.RawMessage, error) {
				var nested json.RawMessage
				if err := CallService(ctx, host, "inner", "run", nil, &nested); err != nil {
					return nil, err
				}
				return nested, nil
			},
		})
	}()
	writeHost := func(line string) { _, _ = io.WriteString(inputWriter, line+"\n") }
	scanner := bufio.NewScanner(outputReader)
	writeHost(`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":3,"plugin_id":"outer","supported_host_services":["host.service.call"]}}`)
	if !scanner.Scan() {
		t.Fatal("missing initialize response")
	}
	writeHost(`{"id":"2","method":"service.invoke","params":{"service":"outer","method":"run","caller":"kernel","execution_id":"exec-nested"}}`)
	if !scanner.Scan() {
		t.Fatal("missing nested service call")
	}
	var nested rpcRequest
	if err := json.Unmarshal(scanner.Bytes(), &nested); err != nil {
		t.Fatal(err)
	}
	if nested.Method != HostServiceCallMethod || !strings.Contains(string(nested.Params), `"execution_id":"exec-nested"`) {
		t.Fatalf("nested service call = %s", scanner.Text())
	}
	writeHost(`{"id":"` + nested.ID + `","result":{"ok":true}}`)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"id":"2"`) {
		t.Fatalf("service response = %s", scanner.Text())
	}
	_ = inputWriter.Close()
	_ = outputReader.Close()
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestServeRunsShutdownCleanupBeforeAcknowledging(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":2,"plugin_id":"test"}}`,
		`{"id":"2","method":"shutdown"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	cleaned := false
	err := ServeIO(context.Background(), strings.NewReader(input), &output, Handler{
		Shutdown: func(context.Context) error {
			cleaned = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("shutdown cleanup was not called")
	}
	if lines := strings.Split(strings.TrimSpace(output.String()), "\n"); len(lines) != 2 || !strings.Contains(lines[1], `"id":"2"`) {
		t.Fatalf("responses = %s", output.String())
	}
}

func TestClientCallsHostServiceOnSameChannel(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	defer requestReader.Close()
	client := newClient(requestWriter)
	requestSeen := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(requestReader)
		if !scanner.Scan() {
			return
		}
		requestSeen <- scanner.Text()
		client.routeResponse(rpcResponse{ID: "plugin-1", Result: json.RawMessage(`{"value":"stored"}`)})
	}()
	var result struct {
		Value string `json:"value"`
	}
	if err := client.CallHost(context.Background(), "host.storage.get", map[string]string{"scope": "workspace", "key": "state"}, &result); err != nil {
		t.Fatal(err)
	}
	request := <-requestSeen
	if result.Value != "stored" || !strings.Contains(request, `"method":"host.service.call"`) || !strings.Contains(request, `"service":"host.storage.get"`) {
		t.Fatalf("result = %+v request = %s", result, request)
	}
}

func TestServeAllowsBackgroundHostCallAfterCapabilityReturns(t *testing.T) {
	hostReader, pluginWriter := io.Pipe()
	pluginReader, hostWriter := io.Pipe()
	defer hostReader.Close()
	defer pluginReader.Close()

	release := make(chan struct{})
	backgroundResult := make(chan error, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeIO(context.Background(), pluginReader, pluginWriter, Handler{
			Definition: Definition{
				Capabilities:         []Capability{{ID: "test.background", Kind: "observe", Version: 1}},
				RequiredHostServices: []HostService{{ID: "host.storage.get", Required: true}},
			},
			InvokeCapability: func(_ context.Context, host Host, _ CapabilityCall) (json.RawMessage, error) {
				go func() {
					<-release
					var result struct {
						Value string `json:"value"`
					}
					err := host.CallHost(context.Background(), "host.storage.get", map[string]string{"key": "background"}, &result)
					if err == nil && result.Value != "awake" {
						err = fmt.Errorf("value = %q", result.Value)
					}
					backgroundResult <- err
				}()
				return json.RawMessage(`{}`), nil
			},
		})
	}()

	scanner := bufio.NewScanner(hostReader)
	writeHost := func(line string) {
		t.Helper()
		if _, err := io.WriteString(hostWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	readPlugin := func() string {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("plugin output ended: %v", scanner.Err())
		}
		return scanner.Text()
	}

	writeHost(`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":2,"plugin_id":"test","supported_host_services":["host.storage.get"]}}`)
	if response := readPlugin(); !strings.Contains(response, `"id":"1"`) {
		t.Fatalf("initialize response = %s", response)
	}
	writeHost(`{"id":"2","method":"capability.invoke","params":{"capability":"test.background","input":{}}}`)
	if response := readPlugin(); !strings.Contains(response, `"id":"2"`) {
		t.Fatalf("capability response = %s", response)
	}
	close(release)
	request := readPlugin()
	var hostCall rpcRequest
	if err := json.Unmarshal([]byte(request), &hostCall); err != nil || hostCall.Method != "host.service.call" {
		t.Fatalf("background request = %s err=%v", request, err)
	}
	writeHost(fmt.Sprintf(`{"id":%q,"result":{"value":"awake"}}`, hostCall.ID))
	if err := <-backgroundResult; err != nil {
		t.Fatal(err)
	}
	writeHost(`{"id":"3","method":"shutdown"}`)
	if response := readPlugin(); !strings.Contains(response, `"id":"3"`) {
		t.Fatalf("shutdown response = %s", response)
	}
	_ = hostWriter.Close()
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestExecutionCancelPreemptsRunning(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    string
		wantResult any
	}{
		{
			name:       "Tool",
			request:    `{"id":"invoke","method":"tool.execute","params":{"tool_id":"wait","execution_id":"exec-1","call_id":"c1","tool":"wait","arguments":{}}}`,
			wantResult: map[string]any{"result": TextResult("canceled")},
		},
		{
			name:       "Service",
			request:    `{"id":"invoke","method":"service.invoke","params":{"service":"search.provider","method":"query","caller":"notes","execution_id":"exec-1"}}`,
			wantResult: map[string]string{"state": "cancelled"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			responses := make(responseSink, 4)
			started := make(chan error, 1)
			observedCancel := make(chan error, 1)
			done := make(chan error, 1)
			waitForCancel := func(ctx context.Context, executionID string) {
				err := ctx.Err()
				if executionID != "exec-1" {
					err = fmt.Errorf("execution ID = %q, want exec-1", executionID)
				}
				started <- err
				<-ctx.Done()
				observedCancel <- ctx.Err()
			}
			go func() {
				done <- ServeIO(ctx, reader, responses, Handler{
					ExecuteTool: func(ctx context.Context, _ Host, call ToolCall) (ToolResult, error) {
						waitForCancel(ctx, call.ExecutionID)
						return TextResult("canceled"), nil
					},
					InvokeService: func(ctx context.Context, _ Host, call ServiceCall) (json.RawMessage, error) {
						waitForCancel(ctx, call.ExecutionID)
						return json.RawMessage(`{"state":"cancelled"}`), nil
					},
				})
			}()
			write := func(line string) {
				t.Helper()
				if _, err := io.WriteString(writer, line+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			await := func(ch <-chan error, stage string) error {
				t.Helper()
				select {
				case err := <-ch:
					return err
				case <-time.After(3 * time.Second):
					t.Fatalf("timed out waiting for %s", stage)
					return nil
				}
			}
			next := func() rpcResponse {
				t.Helper()
				select {
				case r := <-responses:
					if r.Error != nil {
						t.Fatalf("response %q: %+v", r.ID, r.Error)
					}
					return r
				case <-time.After(3 * time.Second):
					t.Fatal("missing runtime response")
					return rpcResponse{}
				}
			}
			write(`{"id":"init","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":3,"plugin_id":"slow"}}`)
			if r := next(); r.ID != "init" {
				t.Fatalf("initialize response = %+v", r)
			}
			write(test.request)
			if err := await(started, "handler start"); err != nil {
				t.Fatalf("handler started with invalid execution: %v", err)
			}
			// Keep the input open and the parent context live: only this frame
			// can cancel the already-running handler, bypassing serial dispatch.
			write(`{"id":"cancel","method":"execution.cancel","params":{"execution_id":"exec-1"}}`)
			if err := await(observedCancel, "handler cancellation"); err != context.Canceled {
				t.Fatalf("handler context error = %v, want context.Canceled", err)
			}
			// The host discards cancellation acknowledgements. Their order
			// relative to the invocation result is not a protocol guarantee.
			wantResult, err := json.Marshal(test.wantResult)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"invoke": string(wantResult), "cancel": `{}`}
			for range 2 {
				r := next()
				result, ok := want[r.ID]
				if !ok || string(r.Result) != result {
					t.Fatalf("unexpected response: id=%q result=%s, remaining=%v", r.ID, r.Result, want)
				}
				delete(want, r.ID)
			}
			write(`{"id":"shutdown","method":"shutdown"}`)
			if r := next(); r.ID != "shutdown" {
				t.Fatalf("shutdown response = %+v", r)
			}
			writer.Close()
			if err := await(done, "runtime shutdown"); err != nil {
				t.Fatal(err)
			}
			if len(responses) != 0 {
				t.Fatalf("unexpected extra response: %+v", <-responses)
			}
		})
	}
}

func TestExecutionCancelForUnknownExecutionIsNoop(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"1","method":"initialize","params":{"protocol_version":1,"capability_protocol_version":3,"plugin_id":"slow"}}`,
		`{"id":"2","method":"execution.cancel","params":{"execution_id":"exec-gone"}}`,
		`{"id":"3","method":"shutdown"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := ServeIO(context.Background(), strings.NewReader(input), &output, Handler{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("responses = %s", output.String())
	}
	joined := output.String()
	for _, id := range []string{`"id":"1"`, `"id":"2"`, `"id":"3"`} {
		if !strings.Contains(joined, id) {
			t.Fatalf("missing ack for %s in %s", id, joined)
		}
	}
}

func TestCallHostPreservesTypedErrorCode(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	defer requestReader.Close()
	client := newClient(requestWriter)
	go func() {
		scanner := bufio.NewScanner(requestReader)
		if !scanner.Scan() {
			return
		}
		client.routeResponse(rpcResponse{ID: "plugin-1", Error: &rpcError{Code: "service_unavailable", Message: "no provider for service memory.session"}})
	}()
	err := client.CallHost(context.Background(), HostServiceCallMethod, map[string]string{"service": "memory.session", "method": "read"}, nil)
	var hostErr *HostCallError
	if !errors.As(err, &hostErr) || hostErr.Code != "service_unavailable" || hostErr.Message != "no provider for service memory.session" {
		t.Fatalf("typed error = %#v", err)
	}
}

// Client serializes writes, so this sink preserves complete response frames.
type responseSink chan rpcResponse

func (s responseSink) Write(p []byte) (int, error) {
	var r rpcResponse
	if err := json.Unmarshal(p, &r); err != nil {
		return 0, err
	}
	s <- r
	return len(p), nil
}

func TestConcurrentCapabilityCancellationAndShutdown(t *testing.T) {
	reader, writer := io.Pipe()
	responses := make(responseSink, 16)
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	var active atomic.Bool
	go func() {
		done <- ServeIO(context.Background(), reader, responses, Handler{
			ConcurrentCapabilities: []string{"read.wait"},
			InvokeCapability: func(ctx context.Context, _ Host, _ CapabilityCall) (json.RawMessage, error) {
				active.Store(true)
				defer active.Store(false)
				started <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			},
			ExecuteTool: func(context.Context, Host, ToolCall) (ToolResult, error) { return TextResult("pong"), nil },
			Shutdown: func(context.Context) error {
				if active.Load() {
					return errors.New("shutdown overlapped capability")
				}
				return nil
			},
		})
	}()
	defer reader.Close()
	defer writer.Close()
	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	next := func() rpcResponse {
		t.Helper()
		select {
		case r := <-responses:
			return r
		case <-time.After(3 * time.Second):
			t.Fatal("missing runtime response")
			return rpcResponse{}
		}
	}
	write(`{"id":"init","method":"initialize","params":{"protocol_version":1,"plugin_id":"test"}}`)
	if r := next(); r.ID != "init" || r.Error != nil {
		t.Fatalf("initialize: %+v", r)
	}
	write(`{"id":"read","method":"capability.invoke","params":{"capability":"read.wait","execution_id":"read-1","input":{}}}`)
	<-started
	write(`{"id":"ping","method":"tool.execute","params":{"tool_id":"ping","arguments":{}}}`)
	if r := next(); r.ID != "ping" || r.Error != nil {
		t.Fatalf("concurrent query blocked tool: %+v", r)
	}
	write(`{"id":"shutdown","method":"shutdown"}`)
	// Cancellation must remain readable while shutdown waits for the query.
	write(`{"id":"cancel","method":"execution.cancel","params":{"execution_id":"read-1"}}`)
	seen := map[string]rpcResponse{}
	for len(seen) < 3 {
		r := next()
		seen[r.ID] = r
	}
	if seen["read"].Error == nil || seen["shutdown"].Error != nil {
		t.Fatalf("cancel/shutdown results: %+v", seen)
	}
	writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not stop")
	}
}
