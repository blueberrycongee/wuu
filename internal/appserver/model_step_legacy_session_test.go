package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The original independent provider implements the public v1 setter, but not
// next-step intent. A new host must reject before invoking its legacy mutation.
func TestNextStepSelectionRejectsLegacySessionProviderWithoutMutation(t *testing.T) {
	ctx := context.Background()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sdk := os.Getenv("WUU_PLUGIN_SDK_ROOT")
	if sdk == "" {
		sdk = filepath.Join(repo, "packages", "plugin-sdk")
	}
	esbuild := filepath.Join(sdk, "node_modules", "esbuild", "bin", "esbuild")
	if _, err := os.Stat(esbuild); err != nil {
		t.Skip("requires installed plugin SDK dependencies")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	entry := filepath.Join(fixture, "legacy-session.mjs")
	source := fmt.Sprintf("import {runJSONLRuntime} from '@wuu/plugin-sdk';\nimport {createSessionProvider} from %q;\nawait runJSONLRuntime(createSessionProvider('legacy-session'), {input:process.stdin,output:process.stdout});\n", filepath.Join(repo, "examples", "plugins", "independent-providers", "sessions.ts"))
	wrapper := filepath.Join(fixture, "entry.mjs")
	if err := os.WriteFile(wrapper, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	// Bundle the public SDK source just like the provider source, so every Go
	// test entry point works without an earlier npm build or stale dist output.
	command := exec.Command(esbuild, wrapper, "--bundle", "--platform=node", "--format=esm", "--alias:@wuu/plugin-sdk="+filepath.Join(sdk, "src", "index.ts"), "--outfile="+entry)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bundle unchanged independent session provider: %s: %v", output, err)
	}
	process, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "legacy-session", Command: node, Args: []string{entry}, PluginRoot: fixture, ProjectRoot: fixture, WuuHome: t.TempDir(), WorkspaceStateDir: t.TempDir(), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close(context.Background()) })
	for _, service := range process.ProvidedServices() {
		for _, method := range service.Methods {
			if method.Name == "SetNextStepModelSelection" {
				t.Fatal("legacy fixture unexpectedly implements next-step intent")
			}
		}
	}
	var legacyWrites atomic.Int32
	var unsupportedCalls atomic.Int32
	store := session.NewClient(ctx, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "SetRuntimeSelection" {
			legacyWrites.Add(1)
		}
		result, err := process.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: session.ServiceName, Caller: "legacy-session-contract", Method: method, Params: raw})
		var remote *pluginhost.HostServiceError
		if method == "SetNextStepModelSelection" && errors.As(err, &remote) && remote.Code == "method_not_found" {
			unsupportedCalls.Add(1)
		}
		return result, err
	})
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	bindConfigSourceForTest(t, rt)
	rt.Sessions = store
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(ctx, []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "start")
	if response["error"] != nil {
		t.Fatalf("legacy thread start: %+v", response)
	}
	id := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	th := srv.thread(id)
	active, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	// This embedded driver is already selected. Its checkpoint persistence is
	// independent of whether the session provider accepts the new mutation.
	active.StreamRunner.DriverBindingStore = nil
	selected := session.RuntimeSelection{Provider: "alt-provider", Model: "alt-model", PermissionMode: th.PermissionMode, ApproveForMe: th.ApproveForMe}
	if err := active.PrepareNextStepModel(ctx, selected); err != nil {
		t.Fatalf("engine/model preflight must pass before storage rejection: %v", err)
	}
	before, found, err := store.Find(id)
	if err != nil || !found {
		t.Fatalf("legacy session before selection: %+v %v", before, err)
	}
	writesBefore := legacyWrites.Load()
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
	raw, _ := json.Marshal(map[string]any{"id": "next", "method": "config/model/update", "params": map[string]any{"thread_id": id, "provider": selected.Provider, "model": selected.Model, "apply_at": "next_step"}})
	if err := srv.handleLine(ctx, raw); err != nil {
		t.Fatal(err)
	}
	response = responseByID(t, parseOutput(t, out.String()), "next")
	if response["error"] == nil {
		t.Error("legacy provider accepted next-step intent")
	}
	if unsupportedCalls.Load() != 1 {
		t.Error("next-step intent did not reach the provider's explicit missing-method rejection")
	}
	if writes := legacyWrites.Load() - writesBefore; writes != 0 {
		t.Errorf("unsupported next-step intent invoked legacy setter %d time(s)", writes)
	}
	after, found, err := store.Find(id)
	if err != nil || !found || !reflect.DeepEqual(before, after) {
		t.Errorf("rejected next-step intent changed provider-owned state: before=%+v after=%+v err=%v", before, after, err)
	}
	th.mu.Lock()
	unchanged := th.execRuntime == active && th.ModelProvider == before.Provider && th.Model == before.Model && th.PermissionMode == before.PermissionMode && th.ApproveForMe == before.ApproveForMe
	th.mu.Unlock()
	if !unchanged {
		t.Error("rejected next-step intent changed in-memory selection or execution resources")
	}
}
