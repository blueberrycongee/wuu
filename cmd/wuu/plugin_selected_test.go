package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A separate provider process owns configuration bytes independently from the
// bootstrap file. Lost replies may follow a durable write and must not replay.
func TestPluginCLISelectedConfig(t *testing.T) {
	if dir := os.Getenv("WUU_PLUGIN_CONFIG_FIXTURE"); dir != "" {
		runPluginConfigFixture(dir)
		return
	}
	for _, scenario := range []struct{ action, mode string }{
		{"approve", "success"}, {"disable", "success"}, {"remove", "success"},
		{"approve-update", "success"}, {"reject-update", "read-failure"},
		{"approve", "read-failure"}, {"remove", "read-failure"},
		{"approve-update", "conflict"}, {"remove", "conflict"},
		{"approve-update", "unknown"}, {"remove", "unknown"},
		{"approve-update", "activation-failure"}, {"remove", "activation-failure"},
	} {
		t.Run(scenario.action+"/"+scenario.mode, func(t *testing.T) {
			home, root, external := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("WUU_HOME", home)
			t.Chdir(root)
			providerDir := filepath.Join(home, "plugins", "external-config")
			if err := os.MkdirAll(providerDir, 0700); err != nil {
				t.Fatal(err)
			}
			manifest, err := json.Marshal(pluginpkg.Manifest{SchemaVersion: 1, ID: "external-config", Runtime: &pluginpkg.RuntimeSpec{
				Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginCLISelectedConfig$"},
				Env: map[string]string{"WUU_PLUGIN_CONFIG_FIXTURE": external},
			}})
			if err != nil {
				t.Fatal(err)
			}
			writePluginConfigFile(t, filepath.Join(providerDir, "plugin.json"), manifest)
			source := t.TempDir()
			writePluginConfigFile(t, filepath.Join(source, "plugin.json"), []byte(`{"id":"selected-demo","version":"1.0.0"}`))
			installed, err := pluginpkg.InstallPackage(home, source)
			if err != nil {
				t.Fatal(err)
			}
			provider := userPluginByID(t, home, "external-config")
			cfg := config.Default()
			modules := []string{"wuu.session-state", persistence.SQLiteModule, runtime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, runtime.ArtifactModule, runtime.DataQueryModule, provider.ID}
			cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: config.ServiceName, Major: 1, Provider: provider.ID}}}
			cfg.Extensions = &extensions.Settings{}
			for _, item := range []pluginpkg.Plugin{provider, installed.Plugin} {
				if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions}); err != nil {
					t.Fatal(err)
				}
			}
			bootstrap, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, "config.json")
			writePluginConfigFile(t, configPath, bootstrap)
			// Preserve provider-owned source fields that are not in effective Config.
			var raw map[string]any
			if err := json.Unmarshal(bootstrap, &raw); err != nil {
				t.Fatal(err)
			}
			raw["external_marker"] = external
			initial, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			writePluginConfigFile(t, filepath.Join(external, "source.json"), initial)
			writePluginConfigFile(t, filepath.Join(external, "mode"), []byte(scenario.mode))
			var pending pluginpkg.PendingUpdate
			if strings.HasSuffix(scenario.action, "-update") || scenario.action == "remove" {
				writePluginConfigFile(t, filepath.Join(source, "plugin.json"), []byte(`{"id":"selected-demo","version":"2.0.0"}`))
				pending, err = pluginpkg.StagePackageUpdate(home, source)
				if err != nil {
					t.Fatal(err)
				}
			}
			action := strings.TrimSuffix(scenario.action, "-update")
			var commandErr error
			captureStdout(t, func() { commandErr = run([]string{"plugin", action, "selected-demo"}) })
			wantErr := scenario.mode != "success" && scenario.action != "reject-update"
			if (commandErr != nil) != wantErr {
				t.Fatalf("command error = %v, want error %v", commandErr, wantErr)
			}
			if scenario.mode == "unknown" && (commandErr == nil || !strings.Contains(commandErr.Error(), "unknown")) {
				t.Fatalf("uncertain write was hidden: %v", commandErr)
			}
			if scenario.mode == "conflict" {
				var serviceErr *pluginhost.HostServiceError
				if !errors.As(commandErr, &serviceErr) || serviceErr.Code != "config_conflict" || strings.Contains(commandErr.Error(), "unknown") {
					t.Fatalf("definite conflict was not preserved: %v", commandErr)
				}
			}
			if scenario.mode == "activation-failure" && (commandErr == nil || !strings.Contains(commandErr.Error(), "committed")) {
				t.Fatalf("durable write was hidden: %v", commandErr)
			}
			if actual, err := os.ReadFile(configPath); err != nil || !bytes.Equal(actual, bootstrap) {
				t.Fatalf("native bootstrap was changed: %v", err)
			}
			calls, err := os.ReadFile(filepath.Join(external, "calls"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if scenario.action != "reject-update" && !strings.Contains(string(calls), "read\n") {
				t.Fatal("command never reached selected provider")
			}
			wantCAS := 1
			if scenario.mode == "read-failure" || scenario.action == "reject-update" {
				wantCAS = 0
			}
			if got := strings.Count(string(calls), "compare-exchange\n"); got != wantCAS {
				t.Fatalf("CAS attempts = %d, want %d; calls=%s", got, wantCAS, calls)
			}
			persisted, err := os.ReadFile(filepath.Join(external, "source.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(persisted, []byte(external)) {
				t.Fatal("provider-owned source field was lost")
			}
			written := scenario.mode == "success" || scenario.mode == "unknown" || scenario.mode == "activation-failure"
			if !written && !bytes.Equal(persisted, initial) {
				t.Fatal("rejected policy write changed source")
			}
			var selected config.Config
			if err := json.Unmarshal(persisted, &selected); err != nil {
				t.Fatal(err)
			}
			if written {
				switch action {
				case "approve":
					fingerprint := installed.Plugin.Fingerprint
					if pending.Path != "" {
						fingerprint = pending.Package.Fingerprint
					}
					if _, ok := selected.Extensions.FindGrant(installed.Plugin.SubjectID, fingerprint); !ok {
						t.Fatal("selected policy did not grant the exact fingerprint")
					}
				case "disable":
					if !selected.Extensions.IsDisabled(installed.Plugin.SubjectID) {
						t.Fatal("selected policy did not disable plugin")
					}
				case "remove":
					if _, ok := selected.Extensions.Grants[installed.Plugin.SubjectID]; ok {
						t.Fatal("selected policy still grants removed plugin")
					}
				}
			}
			finalized := scenario.mode == "success" || scenario.mode == "activation-failure"
			if action == "remove" && finalized {
				if _, err := os.Stat(installed.Destination); !os.IsNotExist(err) {
					t.Fatalf("removed package remains: %v", err)
				}
			} else {
				item := userPluginByID(t, home, "selected-demo")
				wantVersion := "1.0.0"
				if scenario.action == "approve-update" && finalized {
					wantVersion = "2.0.0"
				}
				if item.Version != wantVersion {
					t.Fatalf("package version = %s, want %s", item.Version, wantVersion)
				}
			}
			if pending.Path != "" {
				_, err := pluginpkg.ReadPendingUpdate(home, "selected-demo")
				removed := finalized || scenario.action == "reject-update"
				if errors.Is(err, pluginpkg.ErrPendingUpdateNotFound) != removed {
					t.Fatalf("pending update: %v, want removed %v", err, removed)
				}
			}
		})
	}
}

func TestPluginCLIFirstPolicyWithoutModel(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Chdir(root)
	source := t.TempDir()
	writePluginConfigFile(t, filepath.Join(source, "plugin.json"), []byte(`{"id":"first-policy"}`))
	captureStdout(t, func() {
		if err := run([]string{"plugin", "install", source}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("installation created configuration: %v", err)
	}
	captureStdout(t, func() {
		if err := run([]string{"plugin", "approve", "first-policy"}); err != nil {
			t.Fatal(err)
		}
	})
	loaded, _, err := config.LoadFrom(root, "")
	if err != nil {
		t.Fatal(err)
	}
	item := userPluginByID(t, home, "first-policy")
	if _, ok := loaded.Extensions.FindGrant(item.SubjectID, item.Fingerprint); !ok {
		t.Fatal("first approval did not persist")
	}
}

func writePluginConfigFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func runPluginConfigFixture(dir string) {
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = struct{}{}
		var failure any
		switch request.Method {
		case "initialize":
			result = pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{{Name: config.ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
				{Name: "read", InputSchema: "state.config.read.input.v1", OutputSchema: "state.config.read.output.v1", Preparation: true},
				{Name: "compare-exchange", InputSchema: "state.config.compare-exchange.input.v1", OutputSchema: "state.config.compare-exchange.output.v1"},
			}}}}
		case "service.invoke":
			var params pluginhost.ServiceInvokeParams
			if json.Unmarshal(request.Params, &params) != nil || params.Service != config.ServiceName {
				os.Exit(3)
			}
			log, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(4)
			}
			if _, err := log.WriteString(params.Method + "\n"); err != nil {
				os.Exit(5)
			}
			log.Close()
			source, err := os.ReadFile(filepath.Join(dir, "source.json"))
			if err != nil {
				os.Exit(6)
			}
			mode, err := os.ReadFile(filepath.Join(dir, "mode"))
			if err != nil {
				os.Exit(7)
			}
			digest := sha256.Sum256(source)
			revision := hex.EncodeToString(digest[:])
			if params.Method == "read" && string(mode) == "read-failure" {
				failure = map[string]string{"code": "config_unavailable", "message": "selected read failed"}
			} else if params.Method == "compare-exchange" {
				var input config.CompareExchangeSource
				if json.Unmarshal(params.Params, &input) != nil {
					os.Exit(8)
				}
				if input.ExpectedRevision != revision || string(mode) == "conflict" {
					failure = map[string]string{"code": "config_conflict", "message": "selected revision conflict"}
				} else {
					source = input.Source
					if os.WriteFile(filepath.Join(dir, "source.json"), source, 0600) != nil {
						os.Exit(9)
					}
					if string(mode) == "unknown" {
						failure = map[string]string{"code": "config_unavailable", "message": "write applied but acknowledgment lost"}
					}
				}
			}
			var cfg config.Config
			if json.Unmarshal(source, &cfg) != nil {
				os.Exit(10)
			}
			digest = sha256.Sum256(source)
			view := config.SourceView{Source: source, Revision: hex.EncodeToString(digest[:]), Config: cfg, Committed: params.Method == "compare-exchange" && failure == nil}
			if string(mode) == "activation-failure" && view.Committed {
				view.ActivationError = "fixture_activation_failed"
			}
			result = view
		case "shutdown":
			encoder.Encode(map[string]any{"id": request.ID, "result": result})
			os.Exit(0)
		case "prepare", "activate", "service.changed", "execution.cancel":
		default:
			os.Exit(11)
		}
		reply := map[string]any{"id": request.ID, "result": result}
		if failure != nil {
			reply = map[string]any{"id": request.ID, "error": failure}
		}
		if encoder.Encode(reply) != nil {
			os.Exit(12)
		}
	}
	os.Exit(0)
}
