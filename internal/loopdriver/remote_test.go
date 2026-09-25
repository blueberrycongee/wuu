package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type fakeRemoteInvoker struct {
	mu    sync.Mutex
	calls []remoteCall
	serve func(ctx context.Context, executionID string, method string, params json.RawMessage) (json.RawMessage, error)
}

type remoteCall struct {
	executionID string
	method      string
	params      json.RawMessage
}

func (f *fakeRemoteInvoker) InvokeDriver(ctx context.Context, executionID string, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, remoteCall{executionID: executionID, method: method, params: params})
	f.mu.Unlock()
	if f.serve != nil {
		return f.serve(ctx, executionID, method, params)
	}
	return json.RawMessage(`{}`), nil
}

type fakeGatewayRegistry struct {
	mu       sync.Mutex
	gateways map[string]KernelGateway
}

func (r *fakeGatewayRegistry) RegisterGateway(executionID string, gateway KernelGateway) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gateways == nil {
		r.gateways = make(map[string]KernelGateway)
	}
	r.gateways[executionID] = gateway
	return func() {
		r.mu.Lock()
		delete(r.gateways, executionID)
		r.mu.Unlock()
	}, nil
}

type recordingGateway struct {
	mu          sync.Mutex
	checkpoints []Checkpoint
	receipt     ModelLoopReceipt
	err         error
}

func (g *recordingGateway) ExecuteModelLoop(context.Context, PersistedInput, LoopPolicy) (ModelLoopReceipt, error) {
	return g.receipt, g.err
}

func (g *recordingGateway) WriteCheckpoint(_ context.Context, checkpoint Checkpoint) error {
	g.mu.Lock()
	g.checkpoints = append(g.checkpoints, checkpoint)
	g.mu.Unlock()
	return nil
}

func testExecution() ExecutionContext {
	return ExecutionContext{SessionID: "sess-1", ExecutionID: "exec-1"}
}

func TestRemoteDriverCreateRunShutdown(t *testing.T) {
	invoker := &fakeRemoteInvoker{}
	gateways := &fakeGatewayRegistry{}
	driver := &RemoteDriver{Profile: "demo", Invoker: invoker, Gateways: gateways, ServiceID: "driver.demo"}

	invoker.serve = func(_ context.Context, _ string, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case RemoteMethodDescriptor:
			return json.Marshal(Descriptor{ID: "wuu.demo", Version: "0.1.0", Capabilities: []string{"demo_round"}})
		case RemoteMethodCreate:
			return json.Marshal(remoteInstanceResult{
				InstanceID: "inst-1",
				Checkpoint: Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0", State: json.RawMessage(`{"phase":"ready"}`)},
			})
		case RemoteMethodRun:
			return json.Marshal(remoteRunResult{
				Status:     TerminalSucceeded,
				ReceiptID:  "rcpt-1",
				Checkpoint: Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0", State: json.RawMessage(`{"phase":"succeeded"}`)},
			})
		case RemoteMethodShutdown:
			return json.RawMessage(`{}`), nil
		default:
			return nil, errors.New("unexpected method " + method)
		}
	}

	descriptor := driver.Descriptor()
	if descriptor.ID != "wuu.demo" || descriptor.Version != "0.1.0" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if cached := driver.Descriptor(); cached.ID != descriptor.ID {
		t.Fatalf("cached descriptor = %+v", cached)
	}

	instance, err := driver.Create(testExecution(), PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	gateway := &recordingGateway{}
	outcome, err := instance.Run(context.Background(), gateway)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if outcome.Status != TerminalSucceeded || outcome.ReceiptID != "rcpt-1" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if string(outcome.Checkpoint.State) != `{"phase":"succeeded"}` {
		t.Fatalf("terminal checkpoint = %s", outcome.Checkpoint.State)
	}
	if got := instance.Checkpoint(); string(got.State) != `{"phase":"succeeded"}` {
		t.Fatalf("instance.Checkpoint() = %s", got.State)
	}
	instance.Shutdown()

	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	var methods []string
	for _, call := range invoker.calls {
		methods = append(methods, call.method)
		if call.method != RemoteMethodDescriptor && call.executionID != "exec-1" {
			t.Fatalf("call %s execution id = %q, want exec-1", call.method, call.executionID)
		}
	}
	want := []string{RemoteMethodDescriptor, RemoteMethodCreate, RemoteMethodRun, RemoteMethodShutdown}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	gateways.mu.Lock()
	defer gateways.mu.Unlock()
	if len(gateways.gateways) != 0 {
		t.Fatalf("gateway registry not unregistered: %v", gateways.gateways)
	}
}

func TestRemoteDriverRunRegistersGatewayDuringRun(t *testing.T) {
	invoker := &fakeRemoteInvoker{}
	gateways := &fakeGatewayRegistry{}
	driver := &RemoteDriver{Profile: "demo", Invoker: invoker, Gateways: gateways}
	gatewayVisible := make(chan KernelGateway, 1)

	invoker.serve = func(ctx context.Context, _ string, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case RemoteMethodCreate:
			return json.Marshal(remoteInstanceResult{InstanceID: "inst-1", Checkpoint: Checkpoint{ContractVersion: ContractVersion}})
		case RemoteMethodRun:
			gateways.mu.Lock()
			gateway := gateways.gateways["exec-1"]
			gateways.mu.Unlock()
			if gateway == nil {
				return nil, errors.New("gateway not registered during run")
			}
			gatewayVisible <- gateway
			return json.Marshal(remoteRunResult{Status: TerminalSucceeded, Checkpoint: Checkpoint{ContractVersion: ContractVersion}})
		default:
			return json.RawMessage(`{}`), nil
		}
	}

	instance, err := driver.Create(testExecution(), PersistedInput{})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := instance.Run(context.Background(), &recordingGateway{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	select {
	case <-gatewayVisible:
	default:
		t.Fatal("gateway was not visible to the remote run")
	}
}

func TestRemoteDriverCancelPropagatesThroughExecution(t *testing.T) {
	invoker := &fakeRemoteInvoker{}
	driver := &RemoteDriver{Profile: "demo", Invoker: invoker, Gateways: &fakeGatewayRegistry{}}
	runStarted := make(chan struct{})
	runCanceled := make(chan error, 1)

	invoker.serve = func(ctx context.Context, _ string, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case RemoteMethodCreate:
			return json.Marshal(remoteInstanceResult{InstanceID: "inst-1", Checkpoint: Checkpoint{ContractVersion: ContractVersion}})
		case RemoteMethodRun:
			close(runStarted)
			<-ctx.Done()
			runCanceled <- context.Cause(ctx)
			return nil, ctx.Err()
		default:
			return json.RawMessage(`{}`), nil
		}
	}

	instance, err := driver.Create(testExecution(), PersistedInput{})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	type runResult struct {
		outcome TerminalOutcome
		err     error
	}
	done := make(chan runResult, 1)
	go func() {
		outcome, runErr := instance.Run(context.Background(), &recordingGateway{})
		done <- runResult{outcome: outcome, err: runErr}
	}()
	<-runStarted
	instance.Cancel("user interrupt")
	got := <-done
	if got.err == nil || !strings.Contains(got.err.Error(), "remote driver run") {
		t.Fatalf("Run() error = %v", got.err)
	}
	if got.outcome.Status != TerminalCanceled {
		t.Fatalf("outcome status = %q, want canceled", got.outcome.Status)
	}
	if err := <-runCanceled; err == nil || !strings.Contains(err.Error(), "user interrupt") {
		t.Fatalf("remote ctx cause = %v", err)
	}
}

func TestRemoteDriverRequiresExecutionID(t *testing.T) {
	driver := &RemoteDriver{Profile: "demo", Invoker: &fakeRemoteInvoker{}}
	if _, err := driver.Create(ExecutionContext{SessionID: "sess-1"}, PersistedInput{}); err == nil || !strings.Contains(err.Error(), "execution id") {
		t.Fatalf("Create() = %v, want execution id error", err)
	}
	if _, err := driver.Resume(ExecutionContext{SessionID: "sess-1"}, PersistedInput{}, Checkpoint{}); err == nil || !strings.Contains(err.Error(), "execution id") {
		t.Fatalf("Resume() = %v, want execution id error", err)
	}
}

func TestRemoteDriverResumeSendsCheckpoint(t *testing.T) {
	invoker := &fakeRemoteInvoker{}
	driver := &RemoteDriver{Profile: "demo", Invoker: invoker}
	checkpoint := Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0", State: json.RawMessage(`{"phase":"running"}`)}
	invoker.serve = func(_ context.Context, _ string, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != RemoteMethodResume {
			return nil, errors.New("unexpected method " + method)
		}
		var decoded remoteResumeParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, err
		}
		if string(decoded.Checkpoint.State) != `{"phase":"running"}` || decoded.Execution.ExecutionID != "exec-1" {
			t.Fatalf("resume params = %+v", decoded)
		}
		return json.Marshal(remoteInstanceResult{InstanceID: "inst-2", Checkpoint: checkpoint})
	}
	if _, err := driver.Resume(testExecution(), PersistedInput{}, checkpoint); err != nil {
		t.Fatalf("Resume() = %v", err)
	}
}
