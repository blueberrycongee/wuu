package credentialstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// The second implementation speaks only the public JSONL protocol and stores
// entries in its own JSON file. It never invokes the native service or store.
func TestCredentialsReplaceNativeWithExternalProcess(t *testing.T) {
	if path := os.Getenv("WUU_CREDENTIAL_SERVICE_FIXTURE"); path != "" {
		runExternalCredentialFixture(path)
		return
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "external.json")
	start := func() (*Client, *pluginhost.Host) {
		t.Helper()
		process, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "external.credentials", Command: os.Args[0], Args: []string{"-test.run=^TestCredentialsReplaceNativeWithExternalProcess$"}, Env: map[string]string{"WUU_CREDENTIAL_SERVICE_FIXTURE": path}, PluginRoot: t.TempDir(), ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), Timeout: 5 * time.Second, PrepareOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		native := NewService(OSModule, func() (Store, error) { t.Error("unselected native backend was opened"); return nil, nil })
		registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: ServiceName, Major: 1, Provider: process.ID()}})
		if err != nil {
			t.Fatal(err)
		}
		conflicts := registry.RegisterClients(process, native, &credentialConsumer{})
		if len(conflicts) != 0 {
			t.Fatal(conflicts)
		}
		host := pluginhost.New(process, &credentialConsumer{})
		host.AttachServiceRegistry(registry, conflicts)
		if err := host.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { host.Close(ctx) })
		client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := host.CallService(ctx, "test.consumer", ServiceName, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		})
		return client, host
	}
	client, host := start()
	if err := client.Set(ctx, "oauth", "test", []byte("initial")); err != nil {
		t.Fatal(err)
	}
	if err := client.Update(ctx, "oauth", "test", func(value []byte) ([]byte, error) { return append(value, []byte("-updated")...), nil }); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(ctx, "oauth", "test"); err == nil {
		t.Fatal("closed process binding served local credentials")
	}
	reopened, next := start()
	value, err := reopened.Get(ctx, "oauth", "test")
	if err != nil || string(value) != "initial-updated" {
		t.Fatalf("external persistence lost: %q %v", value, err)
	}
	next.ServiceRegistry().RevokePlugin(ctx, "external.credentials")
	if _, err := reopened.Get(ctx, "oauth", "test"); err == nil {
		t.Fatal("revoked external provider fell back")
	}
}

func runExternalCredentialFixture(path string) {
	values := map[string][]byte{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &values) != nil {
			os.Exit(2)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	reply := func(id string, result any) {
		if encoder.Encode(map[string]any{"id": id, "result": result}) != nil {
			os.Exit(3)
		}
	}
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(4)
		}
		switch request.Method {
		case "initialize":
			var methods []pluginhost.ServiceMethodDescriptor
			for _, method := range []string{"get", "set", "delete", "compare-exchange"} {
				methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: "state.credentials." + method + ".input.v1", OutputSchema: "state.credentials." + method + ".output.v1"})
			}
			reply(request.ID, pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{{Name: "state.credentials", Version: "1.0.0", Methods: methods}}})
		case "prepare", "activate", "service.changed", "execution.cancel":
			reply(request.ID, struct{}{})
		case "service.invoke":
			var params pluginhost.ServiceInvokeParams
			if json.Unmarshal(request.Params, &params) != nil || params.Service != "state.credentials" {
				os.Exit(5)
			}
			var input struct {
				Namespace string `json:"namespace"`
				Key       string `json:"key"`
				Value     []byte `json:"value"`
				Expected  []byte `json:"expected"`
			}
			if json.Unmarshal(params.Params, &input) != nil {
				os.Exit(6)
			}
			key := input.Namespace + ":" + input.Key
			var result any = struct{}{}
			switch params.Method {
			case "get":
				result = map[string]any{"value": values[key]}
			case "set":
				values[key] = input.Value
			case "delete":
				delete(values, key)
			case "compare-exchange":
				current := values[key]
				matches := (current == nil) == (input.Expected == nil) && bytes.Equal(current, input.Expected)
				if matches {
					if input.Value == nil {
						delete(values, key)
					} else {
						values[key] = input.Value
					}
				}
				result = map[string]any{"exchanged": matches}
			default:
				os.Exit(7)
			}
			if params.Method != "get" {
				raw, err := json.Marshal(values)
				if err != nil || os.WriteFile(path, raw, 0600) != nil {
					os.Exit(8)
				}
			}
			reply(request.ID, result)
		case "shutdown":
			reply(request.ID, struct{}{})
			return
		default:
			os.Exit(9)
		}
	}
}
