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
// regression: installation and validation are catalog-only, so they must
// succeed while a turn is running and must not touch the active plugin
// generation. The newly installed package appears in the inventory but adds
// no model-facing surface to the existing Session.
func TestPluginPackageInstallAllowedWhileTurnRunning(t *testing.T) {
	stream := newBlockingStreamClient("eventually done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachTestModelGeneration(t, rt)
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
	if rt.PluginHost != activeHost {
		t.Fatal("install swapped the active plugin host")
	}
	if len(rt.ActivePlugins) != 0 {
		t.Fatalf("install changed the active plugin set: %+v", rt.ActivePlugins)
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
