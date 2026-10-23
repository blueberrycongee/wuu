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
	serve func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

type remoteCall struct {
	method string
	params json.RawMessage
}

func (f *fakeRemoteInvoker) InvokeDriver(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, remoteCall{method: method, params: params})
	f.mu.Unlock()
	if f.serve != nil {
		return f.serve(ctx, method, params)
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

	invoker.serve = func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
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

	descriptor, err := driver.Descriptor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ID != "wuu.demo" || descriptor.Version != "0.1.0" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if cached, err := driver.Descriptor(context.Background()); err != nil || cached.ID != descriptor.ID {
		t.Fatalf("cached descriptor = %+v", cached)
	}

	instance, err := driver.Create(context.Background(), testExecution(), PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}})
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
		if call.method == RemoteMethodCreate {
			var input remoteCreateParams
			if err := json.Unmarshal(call.params, &input); err != nil || input.Execution.ExecutionID != "exec-1" {
				t.Fatalf("create execution = %+v, err = %v", input.Execution, err)
			}
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

	invoker.serve = func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case RemoteMethodDescriptor:
			return json.Marshal(Descriptor{ID: "wuu.demo", Version: "0.1.0"})
		case RemoteMethodCreate:
			return json.Marshal(remoteInstanceResult{InstanceID: "inst-1", Checkpoint: Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0"}})
		case RemoteMethodRun:
			gateways.mu.Lock()
			gateway := gateways.gateways["exec-1"]
			gateways.mu.Unlock()
			if gateway == nil {
				return nil, errors.New("gateway not registered during run")
			}
			gatewayVisible <- gateway
			return json.Marshal(remoteRunResult{Status: TerminalSucceeded, Checkpoint: Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0"}})
		default:
			return json.RawMessage(`{}`), nil
		}
	}

	instance, err := driver.Create(context.Background(), testExecution(), PersistedInput{})
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

	invoker.serve = func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case RemoteMethodDescriptor:
			return json.Marshal(Descriptor{ID: "wuu.demo", Version: "0.1.0"})
		case RemoteMethodCreate:
			return json.Marshal(remoteInstanceResult{InstanceID: "inst-1", Checkpoint: Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0"}})
		case RemoteMethodRun:
			close(runStarted)
			<-ctx.Done()
			runCanceled <- context.Cause(ctx)
			return nil, ctx.Err()
		default:
			return json.RawMessage(`{}`), nil
		}
	}

	instance, err := driver.Create(context.Background(), testExecution(), PersistedInput{})
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
	if _, err := driver.Create(context.Background(), ExecutionContext{SessionID: "sess-1"}, PersistedInput{}); err == nil || !strings.Contains(err.Error(), "execution id") {
		t.Fatalf("Create() = %v, want execution id error", err)
	}
	if _, err := driver.Resume(context.Background(), ExecutionContext{SessionID: "sess-1"}, PersistedInput{}, Checkpoint{}); err == nil || !strings.Contains(err.Error(), "execution id") {
		t.Fatalf("Resume() = %v, want execution id error", err)
	}
}

func TestRemoteDriverRejectsInvalidIdentityBeforeCreatingInstance(t *testing.T) {
	for _, descriptor := range []string{`{}`, `{"id":"demo"}`, `{"id":" demo ","version":"1.0.0"}`, `{"id":"demo","version":"broken"}`, `unavailable`} {
		t.Run(descriptor, func(t *testing.T) {
			created := false
			invoker := &fakeRemoteInvoker{serve: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				if method == RemoteMethodDescriptor {
					if descriptor == "unavailable" {
						return nil, errors.New("provider crashed")
					}
					return json.RawMessage(descriptor), nil
				}
				created = true
				return json.RawMessage(`{"instance_id":"instance"}`), nil
			}}
			driver := &RemoteDriver{Profile: "demo", Invoker: invoker}
			if _, err := driver.Create(context.Background(), testExecution(), PersistedInput{}); err == nil || created {
				t.Fatalf("invalid descriptor reached create: created=%t, error=%v", created, err)
			}
		})
	}
}

func TestRemoteDriverRejectsForeignStateAndInvalidTerminal(t *testing.T) {
	for _, terminal := range []string{
		`{"status":"succeeded","checkpoint":{"contract_version":1,"driver_id":"other","driver_version":"1.0.0"}}`,
		`{"status":"succeeded","checkpoint":{"contract_version":999,"driver_id":"demo","driver_version":"1.0.0"}}`,
		`{"status":"invented","checkpoint":{"contract_version":1,"driver_id":"demo","driver_version":"1.0.0"}}`,
	} {
		t.Run(terminal, func(t *testing.T) {
			invoker := &fakeRemoteInvoker{serve: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				switch method {
				case RemoteMethodDescriptor:
					return json.RawMessage(`{"id":"demo","version":"1.0.0"}`), nil
				case RemoteMethodCreate:
					return json.RawMessage(`{"instance_id":"instance","checkpoint":{"contract_version":1,"driver_id":"demo","driver_version":"1.0.0","state":{"phase":"ready"}}}`), nil
				case RemoteMethodRun:
					return json.RawMessage(terminal), nil
				}
				return nil, errors.New("unexpected method")
			}}
			driver := &RemoteDriver{Profile: "demo", Invoker: invoker}
			instance, err := driver.Create(context.Background(), testExecution(), PersistedInput{})
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := instance.Run(context.Background(), &recordingGateway{})
			if err == nil || outcome.Status != TerminalFailed || string(instance.Checkpoint().State) != `{"phase":"ready"}` {
				t.Fatalf("invalid terminal replaced accepted state: outcome=%+v, error=%v, checkpoint=%+v", outcome, err, instance.Checkpoint())
			}
		})
	}
}

func TestRemoteDriverCanceledPreparationReleasesLateInstance(t *testing.T) {
	for _, method := range []string{RemoteMethodCreate, RemoteMethodResume} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closed := false
			checkpoint := Checkpoint{ContractVersion: ContractVersion, DriverID: "demo", DriverVersion: "1.0.0"}
			invoker := &fakeRemoteInvoker{serve: func(_ context.Context, invoked string, _ json.RawMessage) (json.RawMessage, error) {
				switch invoked {
				case RemoteMethodDescriptor:
					return json.Marshal(Descriptor{ID: "demo", Version: "1.0.0"})
				case method:
					cancel()
					return json.Marshal(remoteInstanceResult{InstanceID: "late", Checkpoint: checkpoint})
				case RemoteMethodShutdown:
					closed = true
					return json.RawMessage(`{}`), nil
				}
				return nil, errors.New("unexpected call")
			}}
			driver := &RemoteDriver{Profile: "demo", Invoker: invoker}
			var instance Instance
			var err error
			if method == RemoteMethodCreate {
				instance, err = driver.Create(ctx, testExecution(), PersistedInput{})
			} else {
				instance, err = driver.Resume(ctx, testExecution(), PersistedInput{}, checkpoint)
			}
			if !errors.Is(err, context.Canceled) || instance != nil || !closed {
				t.Fatalf("late preparation admitted: error=%v, instance=%v, closed=%t", err, instance, closed)
			}
		})
	}
}

func TestRemoteDriverResumeSendsCheckpoint(t *testing.T) {
	invoker := &fakeRemoteInvoker{}
	driver := &RemoteDriver{Profile: "demo", Invoker: invoker}
	checkpoint := Checkpoint{ContractVersion: ContractVersion, DriverID: "wuu.demo", DriverVersion: "0.1.0", State: json.RawMessage(`{"phase":"running"}`)}
	invoker.serve = func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method == RemoteMethodDescriptor {
			return json.Marshal(Descriptor{ID: "wuu.demo", Version: "0.1.0"})
		}
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
	if _, err := driver.Resume(context.Background(), testExecution(), PersistedInput{}, checkpoint); err != nil {
		t.Fatalf("Resume() = %v", err)
	}
}
