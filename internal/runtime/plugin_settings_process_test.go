package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// Run through the packed SDK, real JSONL child, registry scopes, and selected
// settings provider. The held operation gives deterministic reload/write races.
func TestPluginSettingsSDKProcess(t *testing.T) {
	fixture := os.Getenv("WUU_SETTINGS_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/plugin-settings-e2e.mjs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	item := serviceTestPlugin("configured-worker", "plugin:user:configured-worker", "one")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	item.Root = fixture
	item.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.ts", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)}, Timeout: 10}
	item.Settings = map[string]pluginpkg.SettingDefinition{}
	for key, apply := range map[string]pluginpkg.SettingApplyMode{"live": pluginpkg.SettingApplyLive, "operation": pluginpkg.SettingApplyNextOperation, "resource": pluginpkg.SettingApplyRestart} {
		item.Settings[item.ID+"."+key] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeNumber, Default: float64(1), Scope: pluginpkg.SettingScopeUser, Apply: apply}
	}
	modules := append(testStateModules(), kernelHostModule, item.ID)
	build := func() (*pluginhost.Host, error) {
		h, _, err := buildPluginHost([]pluginpkg.Plugin{item}, root, "settings", home, state, nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
		return h, err
	}
	start := func() *pluginhost.Host {
		t.Helper()
		h, err := build()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(context.Background()) })
		if err := h.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		return h
	}
	change := func(value json.RawMessage) {
		t.Helper()
		_, err := pluginsettings.Update(home, root, item.SubjectID, pluginsettings.ScopeUser, item.Fingerprint, func(values map[string]json.RawMessage) error {
			for _, key := range []string{"live", "operation", "resource"} {
				values[key] = value
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	type snapshot struct {
		Revision string         `json:"revision"`
		Values   map[string]int `json:"values"`
	}
	type output struct {
		Captured  snapshot `json:"captured"`
		Refreshed snapshot `json:"refreshed"`
		Resource  struct {
			Size int `json:"size"`
		} `json:"resource"`
		Builds        int `json:"builds"`
		PID           int `json:"pid"`
		Legacy        int `json:"legacy"`
		Notifications int `json:"notifications"`
	}
	call := func(h *pluginhost.Host, hold bool) (output, error) {
		var out output
		raw, _ := json.Marshal(map[string]bool{"hold": hold})
		tools := h.ToolDefinitions()
		if len(tools) != 1 {
			return out, fmt.Errorf("fixture has %d active tools", len(tools))
		}
		result, err := h.ExecuteTool(ctx, tools[0].Name, pluginhost.ToolExecuteInput{SessionID: "settings-fixture", ThreadID: "settings-fixture", TurnID: "configuration-turn", CallID: "configuration-read", CWD: root, Arguments: raw})
		if err != nil {
			return out, err
		}
		err = json.Unmarshal([]byte(result.Content[0].Text), &out)
		return out, err
	}
	old := start()
	baseline, err := call(old, false)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan output, 1)
	errors := make(chan error, 1)
	go func() { result, err := call(old, true); held <- result; errors <- err }()
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if line, err := bufio.NewReader(connection).ReadString('\n'); err != nil || line != "captured\n" {
		t.Fatalf("capture barrier: %q %v", line, err)
	}
	change(json.RawMessage(`2`))
	replacement := start()
	fresh, err := call(replacement, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("continue\n")); err != nil {
		t.Fatal(err)
	}
	retained := <-held
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if retained.Refreshed.Values["live"] != 2 || retained.Refreshed.Values["operation"] != 1 || retained.Refreshed.Values["resource"] != 1 || retained.Captured.Values["live"] != 1 {
		t.Fatalf("in-flight configuration drift: %+v", retained)
	}
	if retained.PID != baseline.PID || retained.Builds != 1 || retained.Resource.Size != 1 || retained.Legacy != 1 || retained.Notifications != 2 {
		t.Fatalf("live edit rebuilt resources or skipped notifications: %+v", retained)
	}
	if fresh.Refreshed.Values["resource"] != 2 || fresh.Resource.Size != 2 || fresh.PID == baseline.PID {
		t.Fatalf("reload failed: %+v", fresh)
	}
	next, err := call(old, false)
	if err != nil || next.Refreshed.Values["operation"] != 2 || next.Refreshed.Values["resource"] != 1 {
		t.Fatalf("next operation: %+v %v", next, err)
	}
	change(json.RawMessage(`"invalid"`))
	rejected, err := build()
	if rejected != nil {
		_ = rejected.Close(ctx)
	}
	if err == nil {
		t.Fatal("invalid candidate generation was admitted")
	}
	change(json.RawMessage(`3`))
	recovered, err := call(old, false)
	if err != nil || recovered.PID != baseline.PID || recovered.Resource.Size != 1 || recovered.Refreshed.Values["live"] != 3 {
		t.Fatalf("candidate failure replaced old resource: %+v %v", recovered, err)
	}
	if err := old.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := call(old, false); err == nil {
		t.Fatal("retired generation accepted work")
	}
	t.Log("packed public SDK: in-flight/reload, live publication, operation capture, restart resources, rejected candidate and retirement verified")
}
