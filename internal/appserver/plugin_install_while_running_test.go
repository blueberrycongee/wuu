package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPluginPackageInstallAllowedWhileTurnRunning is the install-while-active
// regression: installation publishes a trusted generation while the admitted
// turn retains its own host and model-facing surface.
func TestPluginPackageInstallAllowedWhileTurnRunning(t *testing.T) {
	stream := newBlockingStreamClient("eventually done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	preparePluginRuntimePathsForTest(t, rt)
	bindConfigSourceForTest(t, rt)
	activeHost := rt.PluginHost
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		select {
		case <-stream.release:
		default:
			close(stream.release)
		}
		srv.Close()
	})

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("turn did not start; output:\n%s", out.String())
	}

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"id":"live-install","name":"Live Install","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installReq, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodPluginPackageInstall,
		"params": PluginPackageInstallParams{Path: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), installReq); err != nil {
		t.Fatalf("plugin/package/install: %v", err)
	}
	if result := responseByID(t, parseOutput(t, out.String()), "3")["result"]; result == nil {
		t.Fatalf("install failed while a turn was running; output:\n%s", out.String())
	}
	thread := srv.threads[threadID]
	thread.mu.Lock()
	retainedHost := thread.execRuntime.PluginHost
	thread.mu.Unlock()
	if retainedHost != activeHost {
		t.Fatal("install replaced admitted turn plugin host")
	}

	found := false
	for _, record := range srv.currentExtensionInventory() {
		if record.ID == "plugin:user:live-install" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("installed package missing from catalog: %+v", srv.currentExtensionInventory())
	}
}
