package pluginsettings

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type preparationRouter struct {
	mu       sync.RWMutex
	registry *pluginhost.ServiceRegistry
}

func (r *preparationRouter) RouteServiceCall(ctx context.Context, caller string, params pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	r.mu.RLock()
	registry := r.registry
	r.mu.RUnlock()
	return registry.Call(ctx, caller, params)
}

// A real process reads its persisted state in prepare through service.call.
// The same graph must reject state/config writes until activation.
func TestStateReadOnlyPublicPreparation(t *testing.T) {
	if os.Getenv("WUU_STATE_PREPARATION_FIXTURE") == "1" {
		runStatePreparationFixture()
		return
	}
	ctx := context.Background()
	home, root, subject := t.TempDir(), t.TempDir(), "plugin:prepare"
	if _, err := Update(home, root, subject, ScopeUser, "fingerprint", func(values map[string]json.RawMessage) error {
		values["saved"] = json.RawMessage(`"settings-before"`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateState(home, root, subject, ScopeUser, func(values map[string]string) error { values["saved"] = "state-before"; return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.json")
	source := []byte(`{"default_provider":"embedded","providers":{"embedded":{"type":"openai-compatible","model":"model","base_url":"https://example.test/v1"}}}`)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	router := &preparationRouter{}
	process, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "test.prepare", Command: os.Args[0], Args: []string{"-test.run=^TestStateReadOnlyPublicPreparation$"}, Env: map[string]string{"WUU_STATE_PREPARATION_FIXTURE": "1"}, PluginRoot: root, ProjectRoot: root, WuuHome: home, PrepareOnly: true, ServiceRouter: router, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	settings, configuration := NewService(Module, home, root), config.NewService(config.Module, path, "")
	registry, conflicts := pluginhost.BuildServiceRegistry(process, settings, configuration)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	router.mu.Lock()
	router.registry = registry
	router.mu.Unlock()
	host := pluginhost.New(process, settings, configuration)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(ctx)
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if settings.Status().State != pluginhost.StatePrepared || configuration.Status().State != pluginhost.StatePrepared {
		t.Fatal("prepare activated a provider")
	}
	if _, err := host.CallService(ctx, process.ID(), ServiceName, "read", json.RawMessage(`{"subject":"plugin:prepare","scope":"user"}`)); err == nil {
		t.Fatal("autonomous read escaped prepare admission")
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := ReadState(home, root, subject, ScopeUser)
	if err != nil || state.Values["saved"] != "state-before" {
		t.Fatalf("preparation changed business state: %v", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(source) {
		t.Fatalf("preparation changed config: %v", err)
	}
}

func runStatePreparationFixture() {
	input := bufio.NewScanner(os.Stdin)
	output := json.NewEncoder(os.Stdout)
	type envelope struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	call := func(service, method, params string, denied bool, expected string) error {
		if err := output.Encode(map[string]any{"id": "fixture-call", "method": pluginhost.ServiceCallMethod, "params": pluginhost.ServiceCallParams{Service: service, Method: method, Params: json.RawMessage(params)}}); err != nil {
			return err
		}
		if !input.Scan() {
			return fmt.Errorf("missing service reply")
		}
		var reply envelope
		if err := json.Unmarshal(input.Bytes(), &reply); err != nil {
			return err
		}
		failed := len(reply.Error) != 0 && string(reply.Error) != "null"
		if reply.ID != "fixture-call" || failed != denied || (!denied && !strings.Contains(string(reply.Result), expected)) {
			return fmt.Errorf("unexpected preparation reply for %s/%s: %s", service, method, input.Bytes())
		}
		return nil
	}
	for input.Scan() {
		var request envelope
		if json.Unmarshal(input.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = struct{}{}
		var err error
		switch request.Method {
		case "initialize":
			result = pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, RequiredServices: []pluginhost.ServiceRequirement{{Name: ServiceName, MajorVersion: 1, Required: true}, {Name: config.ServiceName, MajorVersion: 1, Required: true}}}
		case "prepare":
			for _, step := range []struct {
				service, method, params, expected string
				denied                            bool
			}{
				{ServiceName, "read", `{"subject":"plugin:prepare","scope":"user"}`, "settings-before", false},
				{ServiceName, "read-state", `{"subject":"plugin:prepare","scope":"user"}`, "state-before", false},
				{config.ServiceName, "read", `{}`, "embedded", false},
				{ServiceName, "update", `{}`, "", true},
				{ServiceName, "update-state", `{}`, "", true},
				{config.ServiceName, "compare-exchange", `{}`, "", true},
			} {
				if err = call(step.service, step.method, step.params, step.denied, step.expected); err != nil {
					break
				}
			}
		case "shutdown":
			_ = output.Encode(map[string]any{"id": request.ID, "result": result})
			return
		}
		if err != nil {
			_ = output.Encode(map[string]any{"id": request.ID, "error": map[string]string{"code": "fixture_failed", "message": err.Error()}})
		} else {
			_ = output.Encode(map[string]any{"id": request.ID, "result": result})
		}
	}
}
