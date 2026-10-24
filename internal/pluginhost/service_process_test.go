package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	Caller        string `json:"caller,omitempty"`
	Query         string `json:"query,omitempty"`
	Code          string `json:"code,omitempty"`
	ChangedReason string `json:"changed_reason,omitempty"`
	Calls         int    `json:"calls,omitempty"`
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
			if role == "provider" {
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
			writeServiceHelperResult(encoder, request.ID, serviceConsumerResult{Caller: params.Caller, Query: input.Query, Calls: serviceCalls})
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
