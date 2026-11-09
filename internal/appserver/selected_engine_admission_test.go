package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// An external engine.wuu provider is not the host loop merely because it uses
// that identity. Admission follows its capabilities and keeps opaque options.
func TestSelectedEngineThreadAdmission(t *testing.T) {
	fixture := os.Getenv("WUU_ENGINE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/engine-provider-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root, home := t.TempDir(), t.TempDir()
	plugins := []pluginpkg.Plugin{{Root: fixture, Official: true, Manifest: pluginpkg.Manifest{ID: "external-engine", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"engine.ts", "admission"}}}}}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fixture", Model: "host-default"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{"wuu.engines.native": false, "external-engine": true}}}, Services: []composition.Binding{{Service: "engine.wuu", Major: 1, Provider: "external-engine"}}}}
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, ConfigPath: path, ConfigLoadMode: runtime.ConfigLoadFile, DiscoveredPlugins: &plugins, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	out := &lockedBuffer{}
	server := New(rt, out)
	defer server.Close()
	if err := server.handleLine(context.Background(), []byte(`{"id":"external-start","method":"thread/start","params":{"engine":"wuu","model":"native-choice","effort":"high","model_options":{"detail":"full"}}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "external-start")
	if response["error"] != nil {
		t.Fatalf("selected external admission rejected: %+v", response)
	}
	result := remarshal[ThreadStartResult](t, response["result"])
	thread := server.thread(result.Thread.ID)
	if thread.ModelOptions["detail"] != "full" || thread.Model != "native-choice" || thread.ModelEffort != "high" || thread.ModelVariant != "" {
		t.Fatalf("external options/effort changed: %+v", result.Thread)
	}
	if thread.PermissionMode == config.PermissionModeUnconfined {
		t.Fatal("arbitrary replacement inherited native CLI unconfined default")
	}
	lease, err := server.tryAcquireThreadMutationLease(thread.state, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseThreadMutationLease(thread.ID, lease)
	request, _ := json.Marshal(map[string]any{"id": "external-fork", "method": "thread/fork", "params": map[string]any{"thread_id": thread.ID, "turn_id": "active"}})
	if err := server.handleLine(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	fork := responseByID(t, parseOutput(t, out.String()), "external-fork")
	encoded, _ := json.Marshal(fork["error"])
	if !strings.Contains(string(encoded), "wait for the native turn to finish") {
		t.Fatalf("replacement identity bypassed native history lease: %s", encoded)
	}
}

func TestSelectedEngineMetadataRetainsNativeDefaultModel(t *testing.T) {
	th := &threadState{Model: "host-default"}
	applySessionMetadata(th, session.Session{EngineID: "wuu", Provider: "external-model", Effort: "high", PermissionMode: config.PermissionModeUnconfined})
	if th.Model != "" || th.ModelProvider != "external-model" || th.ModelEffort != "high" || th.PermissionMode != config.PermissionModeUnconfined {
		t.Fatalf("persisted external selection lost: %+v", th)
	}
}

type countingListEngine struct {
	agentengine.Factory
	calls int
	fail  bool
}

func (f *countingListEngine) Descriptor(ctx context.Context) (agentengine.Descriptor, error) {
	f.calls++
	if f.fail {
		return agentengine.Descriptor{}, fmt.Errorf("descriptor unavailable")
	}
	return f.Factory.Descriptor(ctx)
}
func TestLegacyThreadListMemoizesEngineDescriptor(t *testing.T) {
	for _, fail := range []bool{false, true} {
		rt := newTestRuntime(t, &fakeClient{})
		factory := &countingListEngine{Factory: runtime.NewWuuEngine(rt)}
		registry := agentengine.NewRegistry()
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
		rt.SetEnginesForTest(registry)
		for i := 0; i < 8; i++ {
			if _, err := rt.Sessions.CreateWithMetadata(fmt.Sprintf("legacy-%d", i), rt.RootDir); err != nil {
				t.Fatal(err)
			}
		}
		out := &lockedBuffer{}
		srv := New(rt, out)
		factory.calls = 0
		factory.fail = fail
		if err := srv.handleLine(context.Background(), []byte(`{"id":"list","method":"thread/list","params":{}}`)); err != nil {
			t.Fatal(err)
		}
		if factory.calls != 1 {
			t.Errorf("failed=%v descriptor calls=%d for one engine", fail, factory.calls)
		}
		srv.Close()
	}
}
