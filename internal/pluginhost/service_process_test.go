package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
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

	start := func(id, role string, router ServiceRouter) *ProcessClient {
		t.Helper()
		root := t.TempDir()
		client, err := Start(context.Background(), ProcessConfig{
			ID: id, Command: os.Args[0], Args: []string{"-test.run=TestServiceRegistryRoutesBetweenProcessesAndConvergesAfterClose"},
			Env: map[string]string{serviceProcessHelperEnv: role}, PluginRoot: root,
			ProjectRoot: filepath.Dir(root), WuuHome: t.TempDir(), Timeout: 2 * time.Second,
			ServiceRouter: router, PrepareOnly: true,
		})
		if err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
		return client
	}

	provider := start("search-provider", "provider", nil)
	defer provider.Close(context.Background())
	router := &serviceRegistryRouter{}
	consumer := start("search-consumer", "consumer", router)
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

type serviceConsumerResult struct {
	Caller        string `json:"caller,omitempty"`
	Query         string `json:"query,omitempty"`
	Code          string `json:"code,omitempty"`
	ChangedReason string `json:"changed_reason,omitempty"`
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
			var params ServiceInvokeParams
			if role != "provider" || json.Unmarshal(request.Params, &params) != nil || params.Service != "search.provider" || params.Method != "query" {
				os.Exit(3)
			}
			var input struct {
				Query string `json:"query"`
			}
			if json.Unmarshal(params.Params, &input) != nil {
				os.Exit(4)
			}
			writeServiceHelperResult(encoder, request.ID, serviceConsumerResult{Caller: params.Caller, Query: input.Query})
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
