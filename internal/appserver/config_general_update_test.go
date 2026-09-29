package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestAttributionUpdateDuringTurnDefersRuntimeChange(t *testing.T) {
	stream := newBlockingStreamClient("finished normally")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "fake-model"}},
  "agent": {"git_attribution_enabled": true}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	srv.mu.Lock()
	th := srv.threads[threadID]
	srv.mu.Unlock()
	th.mu.Lock()
	activeRuntime := th.execRuntime
	idle := th.addIdleWaiterLocked()
	th.mu.Unlock()

	// A mixed update must still be rejected atomically while MCP is in use.
	if err := srv.handleLine(context.Background(), []byte(`{"id":"mixed","method":"config/general/update","params":{"git_attribution_enabled":false,"mcp_enabled_toggles":{"docs":false}}}`)); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "mixed")["error"] == nil {
		t.Fatal("MCP update should be rejected during a turn")
	}
	cfg, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil || !cfg.Agent.GitAttributionEnabledValue() {
		t.Fatalf("rejected update changed attribution: %v", err)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"ptc","method":"config/general/update","params":{"ptc":{"enabled":true}}}`)); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "ptc")["error"] == nil {
		t.Fatal("PTC update should be rejected during a turn")
	}
	cfg, _, err = config.LoadPath(rt.ConfigPath)
	if err != nil || cfg.PTC.Enabled {
		t.Fatalf("rejected PTC update changed configuration: %v", err)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"attribution","method":"config/general/update","params":{"git_attribution_enabled":false}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "attribution")
	if response["error"] != nil || response["result"] == nil {
		t.Fatalf("attribution update rejected: %+v", response)
	}
	result := remarshal[ConfigGeneralUpdateResult](t, response["result"])
	if result.GeneralSettings.GitAttributionEnabled {
		t.Fatal("response did not reflect saved attribution")
	}
	cfg, _, err = config.LoadPath(rt.ConfigPath)
	if err != nil || cfg.Agent.GitAttributionEnabledValue() {
		t.Fatalf("attribution was not persisted: %v", err)
	}
	th.mu.Lock()
	keptActive := th.running && th.execRuntime == activeRuntime && th.pendingRuntimeReset
	th.mu.Unlock()
	if !keptActive || !activeRuntime.Toolkit.GitAttributionEnabled() {
		t.Fatal("saving attribution changed the active turn's runtime")
	}
	close(stream.release)
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("active turn did not finish after settings save")
	}
	th.mu.Lock()
	status := th.Turns[len(th.Turns)-1].Status
	th.mu.Unlock()
	if status != TurnStatusCompleted {
		t.Fatalf("active turn did not complete normally: %s", status)
	}
	nextRuntime, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if nextRuntime == activeRuntime || nextRuntime.Toolkit.GitAttributionEnabled() {
		t.Fatal("next runtime did not adopt saved attribution")
	}
}

// Exercise configuration persistence, creation and fork through the public RPC
// path without provisioning resources or requiring an inference service.
func TestExecutionEnvironmentSelectionSurvivesSettingsChanges(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	send := func(id, method, params string) map[string]any {
		t.Helper()
		raw := fmt.Sprintf(`{"id":%q,"method":%q,"params":%s}`, id, method, params)
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s: %v", method, response["error"])
		}
		return response
	}
	original := remarshal[ThreadStartResult](t, send("original", "thread/start", `{}`)["result"]).Thread.ID
	send("remote", "config/general/update", `{"execution_environments":{"default":"remote","profiles":{"remote":{"backend":"command","command":["unused-test-adapter"]}}}}`)
	remote := remarshal[ThreadStartResult](t, send("new", "thread/start", `{}`)["result"]).Thread.ID
	send("local", "config/general/update", `{"execution_environments":{"default":"local"}}`)
	for _, item := range []struct {
		id     string
		remote bool
	}{{original, false}, {remote, true}} {
		manager, err := rt.RemoteProcesses(item.id)
		if err != nil || (manager != nil) != item.remote {
			t.Fatalf("pinned choice: %v, remote=%v", err, manager != nil)
		}
	}
	fork := remarshal[ThreadForkResult](t, send("fork", "thread/fork", fmt.Sprintf(`{"thread_id":%q,"mode":"local"}`, remote))["result"]).Thread.ID
	if manager, err := rt.RemoteProcesses(fork); err != nil || manager == nil {
		t.Fatalf("fork lost selected profile: %v", err)
	}
	saved, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil || saved.ExecutionEnvironments.Default != "local" {
		t.Fatalf("saved default: %v %+v", err, saved.ExecutionEnvironments)
	}
}
