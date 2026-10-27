package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Real CLI and JSONL subprocesses protect recovery, trust, and publication.
func TestPluginCLIRecoversFailedAddon(t *testing.T) {
	if mode := os.Getenv("WUU_RECOVERY_FIXTURE"); mode != "" {
		runPluginRecoveryFixture(mode)
		return
	}
	if raw := os.Getenv("WUU_RECOVERY_COMMAND"); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			panic(err)
		}
		var commandErr error
		if args[0] == "test-session" {
			root, err := os.Getwd()
			if err != nil {
				panic(err)
			}
			cfg, path, err := config.LoadPath(filepath.Join(os.Getenv("WUU_HOME"), "config.json"))
			commandErr = err
			if err == nil {
				owner, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: os.Getenv("HOME"), ConfigPath: path, Config: cfg, NoTools: true, NonInteractive: true})
				commandErr = err
				if owner != nil {
					_, useErr := owner.Sessions.Create("independent state remains usable")
					_, closeErr := owner.Cleanup()
					commandErr = errors.Join(commandErr, useErr, closeErr)
				}
			}
		} else {
			commandErr = run(args)
		}
		if commandErr != nil {
			fmt.Fprintln(os.Stderr, commandErr)
			os.Exit(1)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"old-lifecycle", "malformed", "prepare-failure", "activate-failure"} {
		for _, action := range []string{"disable", "remove"} {
			for _, target := range []string{"target", "broken"} {
				t.Run(mode+"/"+action+"/"+target, func(t *testing.T) {
					home, root, logs, cfg := pluginRecoveryHome(t, mode)
					before, err := os.ReadFile(filepath.Join(home, "config.json"))
					if err != nil {
						t.Fatal(err)
					}
					// Policy publication may coexist with a running generation.
					lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(home)
					if err != nil || !acquired {
						t.Fatalf("execution lease: %v, %v", acquired, err)
					}
					defer lease.Release()
					output, commandErr := pluginRecoveryCommand(t, home, root, "plugin", action, "--json", target)
					after, err := os.ReadFile(filepath.Join(home, "config.json"))
					if err != nil {
						t.Fatal(err)
					}
					if commandErr != nil {
						t.Fatalf("plugin %s --json %s: %v; config unchanged=%v; output=%s", action, target, commandErr, bytes.Equal(before, after), output)
					}
					var actual config.Config
					if err := json.Unmarshal(after, &actual); err != nil {
						t.Fatal(err)
					}
					subject := extensions.SubjectID("user", target)
					if action == "disable" && !actual.Extensions.IsDisabled(subject) {
						t.Fatal("disable did not persist")
					}
					if action == "remove" {
						if _, ok := actual.Extensions.Grants[subject]; ok {
							t.Fatal("removed plugin retained trust")
						}
						if _, err := os.Stat(filepath.Join(home, "plugins", target)); !os.IsNotExist(err) {
							t.Fatalf("removed package remains: %v", err)
						}
					}
					for _, id := range []string{"healthy", "target", "broken"} {
						if id == target {
							continue
						}
						subject := extensions.SubjectID("user", id)
						grant := cfg.Extensions.Grants[subject]
						if _, ok := actual.Extensions.FindGrant(subject, grant.Fingerprint); !ok || actual.Extensions.IsDisabled(subject) {
							t.Fatalf("unrelated %s policy changed", id)
						}
					}
					healthy, err := os.ReadFile(filepath.Join(logs, "healthy"))
					if err != nil || !strings.Contains(string(healthy), "activate\n") {
						t.Fatalf("healthy provider not active: %s, %v", healthy, err)
					}
					broken, err := os.ReadFile(filepath.Join(logs, "broken"))
					if err != nil {
						t.Fatal(err)
					}
					if mode != "activate-failure" && strings.Contains(string(broken), "activate\n") {
						t.Fatalf("invalid plugin activated: %s", broken)
					}
					epoch, err := session.ReadPluginGenerationEpoch(home)
					if err != nil || epoch != 1 {
						t.Fatalf("published epoch=%d: %v", epoch, err)
					}
					t.Logf("plugin %s --json %s committed; unrelated healthy plugin remained active", action, target)
				})
			}
		}
	}
}

func TestPluginCLIRecoveryInventory(t *testing.T) {
	for _, mode := range []string{"old-lifecycle", "malformed", "prepare-failure", "activate-failure"} {
		for _, action := range []string{"list", "describe"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				home, root, logs, _ := pluginRecoveryHome(t, mode)
				args := []string{"plugin", action}
				if action == "list" {
					args = append(args, "--json")
				}
				output, err := pluginRecoveryCommand(t, home, root, args...)
				if err != nil {
					t.Fatalf("%s: %v: %s", action, err, output)
				}
				if !bytes.Contains(output, []byte(`"id": "broken"`)) {
					t.Fatalf("broken plugin absent: %s", output)
				}
				if action == "list" {
					entries, err := os.ReadDir(logs)
					if err != nil || len(entries) != 0 {
						t.Fatalf("inventory started plugins: %v, %v", entries, err)
					}
				} else if !bytes.Contains(output, []byte(`"state": "failed"`)) || !bytes.Contains(output, []byte(`"error":`)) {
					t.Fatalf("failed runtime diagnostic lost: %s", output)
				}
			})
		}
	}
}

func TestPluginCLIRecoveryBoundaries(t *testing.T) {
	for _, boundary := range []string{"explicit-selection", "explicit-binding", "invalid-config", "catalog-writer", "generation-writer", "untrusted"} {
		t.Run(boundary, func(t *testing.T) {
			home, root, logs, cfg := pluginRecoveryHome(t, "old-lifecycle")
			switch boundary {
			case "explicit-selection", "explicit-binding":
				cfg.Composition = composition.DefaultConfig("headless", "broken", "healthy", "target")
				if boundary == "explicit-binding" {
					cfg.Composition.Services = []composition.Binding{{Service: config.ServiceName, Major: 1, Provider: "broken"}}
				}
			case "untrusted":
				delete(cfg.Extensions.Grants, extensions.SubjectID("user", "broken"))
			case "catalog-writer":
				lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(home)
				if err != nil || !acquired {
					t.Fatalf("catalog lock: %v, %v", acquired, err)
				}
				defer lease.Release()
			case "generation-writer":
				lease, acquired, err := session.TryAcquirePluginGenerationMutationLease(home)
				if err != nil || !acquired {
					t.Fatalf("generation lock: %v, %v", acquired, err)
				}
				defer lease.Release()
			}
			before, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "invalid-config" {
				before = []byte(`{"extensions":`)
			}
			writePluginConfigFile(t, filepath.Join(home, "config.json"), before)
			output, commandErr := pluginRecoveryCommand(t, home, root, "plugin", "disable", "--json", "broken")
			after, err := os.ReadFile(filepath.Join(home, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "untrusted" {
				if commandErr == nil || !bytes.Equal(before, after) {
					t.Fatalf("boundary %s bypassed: error=%v unchanged=%v output=%s", boundary, commandErr, bytes.Equal(before, after), output)
				}
			} else {
				if commandErr != nil {
					t.Fatalf("disable untrusted: %v: %s", commandErr, output)
				}
				if _, err := os.Stat(filepath.Join(logs, "broken")); !os.IsNotExist(err) {
					t.Fatalf("untrusted plugin started: %v", err)
				}
			}
		})
	}
}

// A services-only composition has no module list. Its explicit provider and
// transitive dependencies are still mandatory after successful preparation.
func TestPluginCLIRecoveryPreservesServicesOnlyBindings(t *testing.T) {
	for _, scenario := range []struct {
		mode, provider string
		required       bool
	}{
		{"bound-activate-failure", "broken", true},
		{"dependency-activate-failure", "healthy", true},
		{"unbound-activate-failure", "healthy", false},
	} {
		for _, constructor := range []string{"CLI", "Session"} {
			t.Run(scenario.mode+"/"+constructor, func(t *testing.T) {
				home, root, logs, cfg := pluginRecoveryHome(t, scenario.mode)
				cfg.Composition = &composition.Config{Version: composition.Version, Services: []composition.Binding{{Service: "example.recovery", Major: 1, Provider: scenario.provider}}}
				before, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				writePluginConfigFile(t, filepath.Join(home, "config.json"), before)
				args := []string{"plugin", "disable", "--json", "target"}
				if constructor == "Session" {
					args = []string{"test-session"}
				}
				output, commandErr := pluginRecoveryCommand(t, home, root, args...)
				after, err := os.ReadFile(filepath.Join(home, "config.json"))
				if err != nil {
					t.Fatal(err)
				}
				calls, err := os.ReadFile(filepath.Join(logs, "broken"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(calls), "initialize\nprepare\nactivate\n") {
					t.Fatalf("fixture did not reach post-preparation activation failure: %s; output=%s", calls, output)
				}
				if !scenario.required {
					if commandErr != nil {
						t.Fatalf("unrelated failure blocked selected service: %v: %s", commandErr, output)
					}
					if constructor == "CLI" {
						var actual config.Config
						if err := json.Unmarshal(after, &actual); err != nil {
							t.Fatal(err)
						}
						if !actual.Extensions.IsDisabled(extensions.SubjectID("user", "target")) {
							t.Fatal("independent disable did not persist")
						}
					} else if !bytes.Equal(before, after) {
						t.Fatal("session startup changed configuration")
					}
					return
				}
				if commandErr == nil || !bytes.Equal(before, after) {
					t.Fatalf("explicit service binding bypassed: error=%v unchanged=%v output=%s", commandErr, bytes.Equal(before, after), output)
				}
				if !bytes.Contains(output, []byte("example.recovery")) || !bytes.Contains(output, []byte(scenario.provider)) {
					t.Fatalf("lost failed binding diagnostic: %s", output)
				}
			})
		}
	}
}

func pluginRecoveryHome(t *testing.T, mode string) (string, string, string, config.Config) {
	t.Helper()
	home, root, logs := t.TempDir(), t.TempDir(), t.TempDir()
	cfg := config.Default()
	cfg.Extensions = &extensions.Settings{}
	for _, id := range []string{"broken", "healthy", "target"} {
		dir := filepath.Join(home, "plugins", id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		fixtureMode := "healthy"
		if id == "broken" {
			fixtureMode = mode
		} else if (mode == "bound-activate-failure" && id == "healthy") || mode == "unbound-activate-failure" {
			fixtureMode = "recovery-alternative"
		} else if mode == "dependency-activate-failure" {
			fixtureMode = "recovery-alternative"
			if id == "healthy" {
				fixtureMode = "bound-consumer"
			}
		}
		manifest, err := json.Marshal(pluginpkg.Manifest{SchemaVersion: 1, ID: id, Runtime: &pluginpkg.RuntimeSpec{
			Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginCLIRecoversFailedAddon$"},
			Env: map[string]string{"WUU_RECOVERY_FIXTURE": fixtureMode, "WUU_RECOVERY_LOG": filepath.Join(logs, id)},
		}})
		if err != nil {
			t.Fatal(err)
		}
		writePluginConfigFile(t, filepath.Join(dir, "plugin.json"), manifest)
		item := userPluginByID(t, home, id)
		if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions}); err != nil {
			t.Fatal(err)
		}
	}
	source, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writePluginConfigFile(t, filepath.Join(home, "config.json"), source)
	return home, root, logs, cfg
}

func pluginRecoveryCommand(t *testing.T, home, root string, args ...string) ([]byte, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestPluginCLIRecoversFailedAddon$")
	command.Dir = root
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "WUU_HOME=") && !strings.HasPrefix(value, "HOME=") && !strings.HasPrefix(value, "WUU_RECOVERY_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "WUU_HOME="+home, "HOME="+root, "WUU_RECOVERY_COMMAND="+string(raw))
	return command.CombinedOutput()
}

func runPluginRecoveryFixture(mode string) {
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		log, err := os.OpenFile(os.Getenv("WUU_RECOVERY_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(3)
		}
		if _, err := fmt.Fprintln(log, request.Method); err != nil {
			os.Exit(4)
		}
		if err := log.Close(); err != nil {
			os.Exit(5)
		}
		var result any = struct{}{}
		if request.Method == "initialize" {
			version := pluginhost.RuntimeLifecycleVersion
			if mode == "old-lifecycle" {
				version = 1
			}
			initialized := pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: version}
			switch mode {
			case "bound-activate-failure", "bound-consumer", "recovery-alternative", "dependency-activate-failure":
				service := "example.recovery"
				if mode == "dependency-activate-failure" {
					service = "example.dependency"
				}
				initialized.ProvidedServices = []pluginhost.ServiceDescriptor{{Name: service, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "read", InputSchema: service + ".read.input.v1", OutputSchema: service + ".read.output.v1", Preparation: true}}}}
				if mode == "bound-consumer" {
					initialized.RequiredServices = []pluginhost.ServiceRequirement{{Name: "example.dependency", MajorVersion: 1, Required: true}}
				}
			}
			result = initialized
			if mode == "malformed" {
				result = "not an initialize result"
			}
		}
		reply := map[string]any{"id": request.ID, "result": result}
		if (request.Method == "prepare" && mode == "prepare-failure") || (request.Method == "activate" && strings.HasSuffix(mode, "activate-failure")) {
			reply = map[string]any{"id": request.ID, "error": map[string]any{"code": "fixture_failure", "message": "deliberate " + mode}}
		}
		if err := encoder.Encode(reply); err != nil {
			os.Exit(6)
		}
		if request.Method == "shutdown" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}
