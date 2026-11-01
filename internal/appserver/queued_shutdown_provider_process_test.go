package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A public SDK provider runs in its own process and talks to a separate HTTP
// authority. The queue cancellation is verified in that authority's durable
// file after the app-server and its Session owner both close.
func TestQueuedShutdownRetainsExternalProviderUntilCancellation(t *testing.T) {
	fixture := os.Getenv("WUU_PROVIDER_AUTHORITY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/state-authority-acceptance/run.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	backend := exec.CommandContext(ctx, node, "backend.ts", stateFile)
	backend.Dir = fixture
	stdout, err := backend.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	backend.Stderr = &stderr
	if err := backend.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Process.Kill(); _ = backend.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("authority readiness: %v %s", scanner.Err(), stderr.String())
	}
	var ready struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil || ready.URL == "" {
		t.Fatalf("authority readiness: %s %v", scanner.Bytes(), err)
	}
	provider := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "queue-close.http-documents", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"provider.ts"}, Env: map[string]string{"DOCUMENTS_ENDPOINT": ready.URL}}}, Root: fixture, Official: true}
	plugins := []pluginpkg.Plugin{provider}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true}, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: provider.ID}}}}}}
	owner, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	sess, err := owner.Sessions.CreateWithMetadata("external-queue-close", owner.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	seedRecoveryQueue(t, owner, sess.ID)
	srv := New(owner, &lockedBuffer{})
	srv.shutdownDrainTimeoutForTest = 50 * time.Millisecond
	released := make(chan struct{})
	srv.afterShutdownThreadReleaseForTest = func() { close(released) }
	defer srv.Close()
	binding, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	th, err := srv.ensureThreadLoaded(binding, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	srv.beforeRuntimeInstallForTest = func(thread *runtime.ThreadRuntime) {
		thread.AgentControl.SetQueuedManagerSpawnHookForTest(func(string) { close(entered); <-resume })
	}
	thread, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("queue did not reach spawn barrier")
	}
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close exceeded bound")
	}
	if _, err := owner.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := th.state.Sessions.Find(sess.ID); err != nil || !found {
		t.Fatalf("retained provider unavailable: %v %v", found, err)
	}
	close(resume)
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer drainCancel()
	if err := thread.AgentControl.WaitOwnedWorkerExecutions(drainCtx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatal(err)
		}
		var saved struct {
			Documents []struct {
				Namespace string          `json:"namespace"`
				Key       string          `json:"key"`
				Value     json.RawMessage `json:"value"`
			} `json:"documents"`
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		var items []harness.QueueItem
		found := false
		for _, doc := range saved.Documents {
			if doc.Namespace == harness.DocumentNamespace && doc.Key == sess.ID {
				found = true
				var image persistence.DocumentImage
				if err := json.Unmarshal(doc.Value, &image); err != nil {
					t.Fatal(err)
				}
				if raw, ok := image.Values["queue"]; ok {
					if err := json.Unmarshal(raw, &items); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if found && len(items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable queue not cancelled: found=%v items=%+v", found, items)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-released:
	case <-time.After(15 * time.Second):
		t.Fatal("deferred thread cleanup did not finish")
	}
}
