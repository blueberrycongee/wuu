package pluginhost

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
)

const serviceProcessHelperEnv = "WUU_PLUGINHOST_SERVICE_PROCESS_HELPER"

type serviceRegistryRouter struct {
	mu       sync.RWMutex
	registry *ServiceRegistry
}

func (r *serviceRegistryRouter) set(registry *ServiceRegistry) {
	r.mu.Lock()
	r.registry = registry
	r.mu.Unlock()
}

func (r *serviceRegistryRouter) RouteServiceCall(ctx context.Context, pluginID string, params ServiceCallParams) (json.RawMessage, *HostServiceError) {
	r.mu.RLock()
	registry := r.registry
	r.mu.RUnlock()
	if registry == nil {
		return nil, &HostServiceError{Code: "service_unavailable", Message: "service registry is unavailable"}
	}
	return registry.Call(ctx, pluginID, params)
}

func TestServiceRegistryRoutesBetweenProcessesAndConvergesAfterClose(t *testing.T) {
	if role := os.Getenv(serviceProcessHelperEnv); role != "" {
		runServiceProcessHelper(role)
		return
	}

	provider := startServiceProcess(t, "search-provider", "provider", nil)
	defer provider.Close(context.Background())
	router := &serviceRegistryRouter{}
	consumer := startServiceProcess(t, "search-consumer", "consumer", router)
	defer consumer.Close(context.Background())

	registry, conflicts := BuildServiceRegistry(provider, consumer)
	if len(conflicts) != 0 {
		t.Fatalf("service conflicts = %+v", conflicts)
	}
	router.set(registry)
	host := New(consumer, provider)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatalf("prepare and activate generation: %v", err)
	}

	first := invokeServiceConsumer(t, consumer)
	if first.Code != "" || first.Caller != "search-consumer" || first.Query != "wuu" {
		t.Fatalf("routed result = %+v", first)
	}

	registry.Close(context.Background())
	second := invokeServiceConsumer(t, consumer)
	if second.Code != "service_unavailable" || second.ChangedReason != "provider_closed" {
		t.Fatalf("closed result = %+v", second)
	}
}

// Only the trusted host can attach admission metadata. It survives a real
// process boundary but cannot be replayed by a different caller or payload.
func TestServiceAdmissionCrossProcessBindsExactInvocation(t *testing.T) {
	provider := startServiceProcess(t, "search-provider", "provider", nil)
	consumer := &fakeServiceProcess{id: "admission-consumer", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	other := &fakeServiceProcess{id: "other-consumer", state: StateActive, required: consumer.required}
	host := New(provider, consumer, other)
	registry, conflicts := BuildServiceRegistry(provider, consumer, other)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"query":"admitted"}`)
	digest := sha256.Sum256(input)
	proof := ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Action: "test/issue", InputDigest: hex.EncodeToString(digest[:]), Authority: "local-installation", InputJSON: string(input)}
	admission, cancel := context.WithCancel(context.Background())
	ctx := WithServiceAdmission(admission, consumer.id, "search.provider", "query", proof)
	invoke := func(ctx context.Context, caller string, raw json.RawMessage) *ServiceAdmission {
		t.Helper()
		out, err := registry.Call(ctx, caller, ServiceCallParams{Service: "search.provider", Method: "query", Params: raw})
		if err != nil {
			t.Fatal(err)
		}
		var result serviceConsumerResult
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal(err)
		}
		return result.Admission
	}
	if got := invoke(ctx, consumer.id, input); got == nil || !reflect.DeepEqual(*got, proof) {
		t.Fatalf("host admission lost over process boundary: %+v", got)
	}
	if invoke(ctx, other.id, input) != nil || invoke(ctx, consumer.id, json.RawMessage(`{"query":"changed"}`)) != nil {
		t.Fatal("admission replay widened caller or payload")
	}
	cancel()
	if invoke(context.WithoutCancel(ctx), consumer.id, input) != nil {
		t.Fatal("detached callback revived an ended admission")
	}
	var spoof ServiceCallParams
	if err := json.Unmarshal(mustRaw(map[string]any{"service": "search.provider", "method": "query", "params": json.RawMessage(`{"query":"forged"}`), "admission": proof}), &spoof); err != nil {
		t.Fatal(err)
	}
	if invoke(context.Background(), consumer.id, spoof.Params) != nil {
		t.Fatal("caller JSON manufactured host admission")
	}
}

func TestProcessNegotiatesCaseSensitiveSessionDomainMethods(t *testing.T) {
	provider := startServiceProcess(t, "session-provider", "session-provider", nil)
	consumer := &fakeServiceProcess{id: "session-consumer", state: StateActive, required: []ServiceRequirement{{Name: "session.domain", MajorVersion: 1, Required: true}}}
	host := New(provider, consumer)
	registry, conflicts := BuildServiceRegistry(provider, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(context.Background())
	if len(conflicts) != 0 {
		t.Fatalf("registration rejected descriptor: %+v", conflicts)
	}
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"ReadControl", "Journal.Close"} {
		raw, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "session.domain", Method: method, Params: json.RawMessage(`{"args":[]}`)})
		if err != nil {
			t.Fatalf("negotiated %s: %v", method, err)
		}
		var got struct {
			Method string `json:"method"`
			Caller string `json:"caller"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Method != method || got.Caller != consumer.id {
			t.Fatalf("identity changed across process: %+v", got)
		}
	}
	if _, err := registry.Call(context.Background(), consumer.id, ServiceCallParams{Service: "session.domain", Method: "readControl"}); err == nil {
		t.Fatal("method routing silently normalized case")
	}
}

func TestProcessServicePreservesProviderErrorsWithoutRegistry(t *testing.T) {
	for _, code := range []string{"invalid_request", "state_conflict", "state_operation_conflict", "state_io_error", "provider_specific_failure", ""} {
		name := code
		if name == "" {
			name = "missing-code"
		}
		t.Run(name, func(t *testing.T) {
			provider := startServiceProcess(t, "search-provider", "provider", nil)
			defer provider.Close(context.Background())
			if err := provider.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			message := "provider rejection (" + code + ")"
			result, err := provider.InvokeService(context.Background(), ServiceInvokeParams{
				Service: "search.provider", Method: "query", Caller: "direct-consumer", ExecutionID: "rejected-call",
				Params: mustRaw(map[string]any{"error": HostServiceError{Code: code, Message: message}}),
			})
			if err == nil || err.Error() != message || result != nil {
				t.Fatalf("provider failure = %s, %v", result, err)
			}
			var serviceErr *HostServiceError
			if code == "" {
				if errors.As(err, &serviceErr) {
					t.Fatalf("unclassified remote failure became a service rejection: %+v", serviceErr)
				}
			} else if !errors.As(err, &serviceErr) || serviceErr.Code != code || serviceErr.Message != message {
				t.Fatalf("structured provider failure was lost: %T %v, service=%+v", err, err, serviceErr)
			}
			if provider.Status().State != StateActive {
				t.Fatalf("domain failure retired provider: %+v", provider.Status())
			}
			result, err = provider.InvokeService(context.Background(), ServiceInvokeParams{
				Service: "search.provider", Method: "query", Caller: "direct-consumer", ExecutionID: "next-call",
				Params: json.RawMessage(`{"query":"after rejection"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			var next serviceConsumerResult
			if err := json.Unmarshal(result, &next); err != nil || next.Query != "after rejection" || next.Calls != 2 {
				t.Fatalf("provider did not resume without replay: %s, %v", result, err)
			}
		})
	}
}

func TestProcessServiceTransportLossDoesNotImplyRejection(t *testing.T) {
	provider := startServiceProcess(t, "search-provider", "provider", nil)
	defer provider.Close(context.Background())
	if err := provider.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	effect := filepath.Join(t.TempDir(), "applied.json")
	result, err := provider.InvokeService(context.Background(), ServiceInvokeParams{
		Service: "search.provider", Method: "query", Caller: "direct-consumer", ExecutionID: "lost-response",
		Params: mustRaw(map[string]any{"drop_response_after_write": effect}),
	})
	var serviceErr *HostServiceError
	if err == nil || result != nil || errors.As(err, &serviceErr) {
		t.Fatalf("transport loss was classified as a provider response: %s, %v", result, err)
	}
	applied, readErr := os.ReadFile(effect)
	if readErr != nil || string(applied) != "1" {
		t.Fatalf("side effect was not retained exactly once: %s, %v", applied, readErr)
	}
}

func startServiceProcess(t *testing.T, id, role string, router ServiceRouter) *ProcessClient {
	t.Helper()
	root := t.TempDir()
	client, err := Start(context.Background(), ProcessConfig{
		ID: id, Command: os.Args[0], Args: []string{"-test.run=^TestServiceRegistryRoutesBetweenProcessesAndConvergesAfterClose$"},
		Env: map[string]string{serviceProcessHelperEnv: role}, PluginRoot: root,
		ProjectRoot: filepath.Dir(root), WuuHome: t.TempDir(), Timeout: 2 * time.Second,
		ServiceRouter: router, PrepareOnly: true,
	})
	if err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
	return client
}

type serviceConsumerResult struct {
	Admission     *ServiceAdmission `json:"admission,omitempty"`
	Caller        string            `json:"caller,omitempty"`
	Query         string            `json:"query,omitempty"`
	Code          string            `json:"code,omitempty"`
	ChangedReason string            `json:"changed_reason,omitempty"`
	Calls         int               `json:"calls,omitempty"`
}

func invokeServiceConsumer(t *testing.T, consumer *ProcessClient) serviceConsumerResult {
	t.Helper()
	result, err := consumer.InvokeCapability(context.Background(), CapabilityInvokeParams{
		Capability: CapabilityPluginClientRequest,
		Input:      json.RawMessage(`{}`),
		Output:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke consumer: %v", err)
	}
	var output serviceConsumerResult
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode consumer result: %v", err)
	}
	return output
}

func runServiceProcessHelper(role string) {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	changedReason := ""
	serviceCalls := 0
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			result := CapabilityInitializeResult{
				ProtocolVersion:  CapabilityProtocolVersion,
				LifecycleVersion: RuntimeLifecycleVersion,
			}
			if role == "session-provider" {
				result.ProvidedServices = []ServiceDescriptor{{Name: "session.domain", Version: "1.0.0", Methods: []ServiceMethodDescriptor{
					{Name: "ReadControl", InputSchema: "session.domain.ReadControl.input.v1", OutputSchema: "session.domain.ReadControl.output.v1"},
					{Name: "Journal.Close", InputSchema: "session.domain.Journal.Close.input.v1", OutputSchema: "session.domain.Journal.Close.output.v1", Cleanup: true},
				}}}
			} else if role == "provider" {
				result.ProvidedServices = []ServiceDescriptor{{
					Name: "search.provider", Version: "1.0.0",
					Methods: []ServiceMethodDescriptor{{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"}},
				}}
			} else {
				result.Capabilities = []CapabilityDescriptor{{ID: CapabilityPluginClientRequest, Kind: SeamDecision, Version: 1}}
				result.RequiredServices = []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
			}
			writeServiceHelperResult(encoder, request.ID, result)
		case "prepare", "activate":
			writeServiceHelperResult(encoder, request.ID, struct{}{})
		case ServiceInvokeMethod:
			if role == "session-provider" {
				var params ServiceInvokeParams
				if json.Unmarshal(request.Params, &params) != nil {
					os.Exit(13)
				}
				writeServiceHelperResult(encoder, request.ID, map[string]string{"method": params.Method, "caller": params.Caller})
				continue
			}
			serviceCalls++
			var params ServiceInvokeParams
			if role != "provider" || json.Unmarshal(request.Params, &params) != nil || params.Service != "search.provider" || params.Method != "query" {
				os.Exit(3)
			}
			var input struct {
				Query                  string            `json:"query"`
				Error                  *HostServiceError `json:"error"`
				DropResponseAfterWrite string            `json:"drop_response_after_write"`
			}
			if json.Unmarshal(params.Params, &input) != nil {
				os.Exit(4)
			}
			if input.DropResponseAfterWrite != "" {
				if os.WriteFile(input.DropResponseAfterWrite, mustRaw(serviceCalls), 0600) != nil {
					os.Exit(11)
				}
				os.Exit(0)
			}
			if input.Error != nil {
				if encoder.Encode(rpcResponse{ID: request.ID, Error: &rpcError{Code: input.Error.Code, Message: input.Error.Message}}) != nil {
					os.Exit(12)
				}
				continue
			}
			writeServiceHelperResult(encoder, request.ID, serviceConsumerResult{Admission: params.Admission, Caller: params.Caller, Query: input.Query, Calls: serviceCalls})
		case ServiceChangedMethod:
			var params ServiceChangedParams
			if role != "consumer" || json.Unmarshal(request.Params, &params) != nil || params.Service != "search.provider" {
				os.Exit(5)
			}
			changedReason = params.Reason
			writeServiceHelperResult(encoder, request.ID, struct{}{})
		case "capability.invoke":
			if role != "consumer" {
				os.Exit(6)
			}
			_ = encoder.Encode(HostServiceCall{
				ID: "service-call", Method: ServiceCallMethod,
				Params: mustRaw(ServiceCallParams{Service: "search.provider", Method: "query", Params: json.RawMessage(`{"query":"wuu"}`)}),
			})
			if !scanner.Scan() {
				os.Exit(7)
			}
			var serviceResult HostServiceResult
			if json.Unmarshal(scanner.Bytes(), &serviceResult) != nil || serviceResult.ID != "service-call" {
				os.Exit(8)
			}
			output := serviceResult.Result
			if serviceResult.Error != nil {
				output = mustRaw(serviceConsumerResult{Code: serviceResult.Error.Code, ChangedReason: changedReason})
			}
			writeServiceHelperResult(encoder, request.ID, CapabilityInvokeResult{Output: output})
		case "shutdown":
			writeServiceHelperResult(encoder, request.ID, struct{}{})
			return
		default:
			os.Exit(9)
		}
	}
}

func writeServiceHelperResult(encoder *json.Encoder, id string, result any) {
	if encoder.Encode(rpcResponse{ID: id, Result: mustRaw(result)}) != nil {
		os.Exit(10)
	}
}
