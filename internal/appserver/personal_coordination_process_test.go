package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const personalCoordinatorHelperEnv = "WUU_PERSONAL_COORDINATOR_HELPER"

// A real process selects its own immediate/deferred review policy and executes
// frozen host effects using only the declared JSON protocol.
func TestPersonalCoordinationExternalProcessEffects(t *testing.T) {
	if os.Getenv(personalCoordinatorHelperEnv) == "1" {
		runPersonalCoordinatorHelper()
		return
	}
	const externalID = "independent.personal-coordinator"
	home := t.TempDir()
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{PersonalCoordinationConsumer: true, PersonalEffectModule: true, personalActionsConsumerID: false, externalID: true}}}}
	bindings := NewRuntimeBindings()
	factories := append(bindings.NativeProviders(), runtime.NativeProviderFactory{ID: externalID, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		return pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: externalID, Command: os.Args[0], Args: []string{"-test.run=^TestPersonalCoordinationExternalProcessEffects$"}, Env: map[string]string{personalCoordinatorHelperEnv: "1"}, PluginRoot: home, ProjectRoot: home, WuuHome: home, Timeout: 5 * time.Second, ServiceRouter: input.Registry, PrepareOnly: true})
	}})
	plugins := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: home, HomeDir: home, UserSpaceID: "process-space", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NativeProviders: factories, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if host.server.personalCoordination.nativeActions() != nil {
		t.Fatal("replacement constructed native coordinator")
	}
	const sessionID = "external-main"
	if _, err := rt.Sessions.CreateInitialized(session.Session{ID: sessionID, UserSpaceID: rt.UserSpaceID, ProductDomain: "personal", CWD: rt.RootDir}, nil); err != nil {
		t.Fatal(err)
	}
	kit, err := rt.Toolkit.CloneForRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID(sessionID)
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"process-space","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	owner = clientauthority.BindConnection(owner)
	call := func(id, path string) error {
		args, _ := json.Marshal(map[string]string{"path": path, "content": "external effect"})
		_, err := host.server.personalFileGate(owner, kit, providers.ToolCall{ID: id, Name: "write_file", Arguments: string(args)})
		return err
	}
	if err := call("immediate", "immediate.txt"); err != nil {
		t.Fatal(err)
	}
	check := func(path string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(rt.RootDir, path))
		if err != nil || string(data) != "external effect" {
			t.Fatalf("%s=%q err=%v", path, data, err)
		}
	}
	check("immediate.txt")
	if err := call("deferred", "deferred.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rt.RootDir, "deferred.txt")); !os.IsNotExist(err) {
		t.Fatalf("deferred effect ran before later owner call: %v", err)
	}
	if _, err := host.server.callPersonalCoordination(owner, "review.resolve", json.RawMessage(`{}`), sessionID); err != nil {
		t.Fatal(err)
	}
	check("deferred.txt")
	if _, err := host.server.callPersonalCoordination(owner, "review.resolve", json.RawMessage(`{}`), sessionID); err == nil {
		t.Fatal("process replayed consumed effect")
	}
	if err := call("deferred", "abandoned.txt"); err != nil {
		t.Fatal(err)
	}
	state, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	connectorCtx := runtime.WithInvocationState(owner, state)
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: rt.UserSpaceID, SessionID: sessionID}
	snapshot, err := host.server.connectorSnapshot(connectorCtx, actor)
	if err != nil || snapshot != "independent-snapshot" {
		t.Fatalf("external connector snapshot=%q err=%v", snapshot, err)
	}
	connectorCalls := 0
	if err := host.server.connectorCommit(connectorCtx, actor, snapshot, func(context.Context) error { connectorCalls++; return nil }); err != nil || connectorCalls != 1 {
		t.Fatalf("external connector effect calls=%d err=%v", connectorCalls, err)
	}
	if err := host.server.connectorCommit(connectorCtx, actor, "skip", func(context.Context) error { connectorCalls++; return nil }); err == nil || connectorCalls != 1 {
		t.Fatalf("coordinator success without effect accepted: calls=%d err=%v", connectorCalls, err)
	}
	if _, ok := state.Host.RetirePlugin(context.Background(), externalID, nil); !ok {
		t.Fatal("external provider was not retired")
	}
	bindings.personalEffects.mu.Lock()
	remaining := len(bindings.personalEffects.resources)
	bindings.personalEffects.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("retired process retained %d deferred host effects", remaining)
	}
	if err := call("disabled", "disabled.txt"); err == nil {
		t.Fatal("disabled replacement fell back to native file execution")
	}
	if host.server.personalCoordination.nativeActions() != nil {
		t.Fatal("disable constructed native fallback")
	}
}

func runPersonalCoordinatorHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	write := func(id string, result any) {
		if encoder.Encode(map[string]any{"id": id, "result": result}) != nil {
			os.Exit(2)
		}
	}
	var deferred PersonalPreparedFile
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		switch request.Method {
		case "initialize":
			write(request.ID, pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{PersonalCoordinationDescriptor()}, RequiredServices: []pluginhost.ServiceRequirement{{Name: PersonalEffectService, MajorVersion: 1, Required: true}}})
		case "prepare", "activate", "service.changed", "execution.cancel":
			write(request.ID, struct{}{})
		case "shutdown":
			write(request.ID, struct{}{})
			return
		case pluginhost.ServiceInvokeMethod:
			var invoke pluginhost.ServiceInvokeParams
			var input personalCoordinationRequest
			if json.Unmarshal(request.Params, &invoke) != nil || json.Unmarshal(invoke.Params, &input) != nil {
				os.Exit(4)
			}
			call := func(method string, value, result any) *pluginhost.HostServiceError {
				params, _ := json.Marshal(value)
				raw, _ := json.Marshal(pluginhost.ServiceCallParams{Service: PersonalEffectService, Method: method, ExecutionID: invoke.ExecutionID, Params: params})
				id := "effect-" + request.ID + "-" + method
				if encoder.Encode(pluginhost.HostServiceCall{ID: id, Method: pluginhost.ServiceCallMethod, Params: raw}) != nil {
					os.Exit(5)
				}
				for scanner.Scan() {
					var frame struct {
						ID     string `json:"id"`
						Method string `json:"method"`
					}
					if json.Unmarshal(scanner.Bytes(), &frame) != nil {
						os.Exit(6)
					}
					if frame.Method == "execution.cancel" || frame.Method == "service.changed" {
						write(frame.ID, struct{}{})
						continue
					}
					var response pluginhost.HostServiceResult
					if frame.ID != id || frame.Method != "" || json.Unmarshal(scanner.Bytes(), &response) != nil {
						os.Exit(7)
					}
					if response.Error != nil {
						return response.Error
					}
					if result != nil && json.Unmarshal(response.Result, result) != nil {
						os.Exit(8)
					}
					return nil
				}
				os.Exit(9)
				return nil
			}
			var err *pluginhost.HostServiceError
			switch invoke.Method {
			case "initialize":
				write(request.ID, struct{}{})
				continue
			case "file-gate":
				var prepared PersonalPreparedFile
				err = call("prepare-file", input.Call, &prepared)
				if err == nil && input.Call.ID == "deferred" {
					deferred = prepared
				} else if err == nil {
					err = call("execute", PersonalEffectReference{EffectID: prepared.EffectID}, nil)
				}
			case "connector-snapshot":
				write(request.ID, personalConnectorSnapshot{Authority: "independent-snapshot"})
				continue
			case "connector-commit":
				var connector personalConnectorCoordination
				if json.Unmarshal(input.Input, &connector) != nil {
					os.Exit(13)
				}
				if connector.Expected != "skip" {
					err = call("execute", PersonalEffectReference{EffectID: connector.EffectID}, nil)
				}
			case "call":
				err = call("execute", PersonalEffectReference{EffectID: deferred.EffectID}, nil)
			default:
				os.Exit(10)
			}
			if err != nil {
				if encoder.Encode(pluginhost.HostServiceResult{ID: request.ID, Error: err}) != nil {
					os.Exit(11)
				}
			} else {
				write(request.ID, toolresult.Result{})
			}
		default:
			os.Exit(12)
		}
	}
}
