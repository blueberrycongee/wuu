package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// A real runtime holds its prepare callback until the test releases its socket.
// Sending must complete while the barrier remains held, not merely beat a timer.
func TestAdditivePreparationProcess(t *testing.T) {
	address := os.Getenv("WUU_ADDITIVE_PREPARE_BARRIER")
	if address == "" {
		return
	}
	scanner, encoder := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct{ ID, Method string }
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion}
		case "prepare":
			connection, err := net.Dial("tcp", address)
			if err != nil {
				os.Exit(3)
			}
			_, _ = connection.Write([]byte("ready\n"))
			_, _ = connection.Read(make([]byte, 1))
			_ = connection.Close()
		case "shutdown":
			_ = encoder.Encode(map[string]any{"id": request.ID, "result": result})
			return
		}
		if encoder.Encode(map[string]any{"id": request.ID, "result": result}) != nil {
			os.Exit(4)
		}
	}
}

func TestAdditivePreparationKeepsWarmAndColdConversationsUsable(t *testing.T) {
	testAdditivePreparationAdmission(t, "additive")
}

func TestAdditivePreparationAfterQuarantinedOptionalFailure(t *testing.T) {
	testAdditivePreparationAdmission(t, "quarantined-current")
}

func TestChangedCatalogPreparationStillFencesAdmission(t *testing.T) {
	for _, change := range []string{"config", "trust", "required", "removal", "replacement", "source", "failed-current", "failed-required"} {
		t.Run(change, func(t *testing.T) { testAdditivePreparationAdmission(t, change) })
	}
}

func TestAdditivePreparationCancellationDoesNotPublishCandidate(t *testing.T) {
	for _, change := range []string{"cancel-mutation", "cancel-shutdown"} {
		t.Run(change, func(t *testing.T) { testAdditivePreparationAdmission(t, change) })
	}
}

func TestSupersededAdditivePreparationCannotAdmitOrPublish(t *testing.T) {
	testAdditivePreparationAdmission(t, "superseded-epoch")
}

func testAdditivePreparationAdmission(t *testing.T, change string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		if _, err := bufio.NewReader(connection).ReadString('\n'); err != nil {
			_ = connection.Close()
			return
		}
		entered <- connection
	}()
	var connection net.Conn
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			if connection != nil {
				_ = connection.Close()
			}
			_ = listener.Close()
		})
	}
	t.Cleanup(release)
	manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "additive-barrier", Runtime: &pluginpkg.RuntimeSpec{
		Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestAdditivePreparationProcess$"}, Timeout: 30,
		Env: map[string]string{"WUU_ADDITIVE_PREPARE_BARRIER": listener.Addr().String()},
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "plugin.json")
	writePluginPackageFile(t, source, string(raw))
	item, err := pluginpkg.LoadManifest(source, "user")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{response: providers.ChatResponse{Content: "completed while plugin preparation is held"}}
	rt := newTestRuntime(t, client)
	rt.WuuHome = t.TempDir()
	preparePluginRuntimePathsForTest(t, rt)
	cfg, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Extensions = &extensions.Settings{}
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(rt.WuuHome, "plugins", manifest.ID, "plugin.json")
	prior := filepath.Join(rt.WuuHome, "plugins", "previous-addon", "plugin.json")
	if change == "replacement" {
		writePluginPackageFile(t, installed, `{"schema_version":1,"id":"additive-barrier","version":"1.0.0"}`)
	}
	if change == "removal" || change == "source" || change == "quarantined-current" {
		writePluginPackageFile(t, prior, `{"schema_version":1,"id":"previous-addon"}`)
	}
	if change == "quarantined-current" {
		previous, err := pluginpkg.LoadManifest(prior, "user")
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: previous.SubjectID, Fingerprint: previous.Fingerprint, Permissions: previous.EffectivePermissions}); err != nil {
			t.Fatal(err)
		}
	}
	rawConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, rt.ConfigPath, string(rawConfig))
	rt = newReloadBehaviorRuntime(t, rt, client)
	out := &lockedBuffer{}
	// Trigger the observation explicitly so config and filesystem watchers
	// cannot publish a negative-control edit before its paired catalog change.
	home := rt.WuuHome
	rt.WuuHome = ""
	srv := New(rt, out)
	rt.WuuHome = home
	t.Cleanup(func() { release(); srv.Close() })
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- srv.handleLine(context.Background(), payload) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s waited for the held plugin preparation", method)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	create := call("warm-create", MethodThreadStart, ThreadStartParams{})
	if create["error"] != nil {
		t.Fatalf("create warm thread: %+v", create)
	}
	threadID := remarshal[ThreadStartResult](t, create["result"]).Thread.ID
	baseline := call("baseline", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: "baseline"})
	if baseline["error"] != nil {
		t.Fatalf("baseline turn: %+v", baseline)
	}
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	base := rt.RetainPluginGeneration()
	defer rt.ReleasePluginGeneration(base)
	baseEpoch := srv.pluginGenerationEpoch.Load()
	if change == "quarantined-current" {
		// A completed optional prepare failure is already diagnostic-only,
		// but its retirement mask may await the next catalog observation.
		rt.PluginHost.Add(pluginhost.Failed("previous-addon", fmt.Errorf("optional preparation failed")))
	}
	if change == "failed-required" {
		requireActiveRuntime(t, rt.PluginHost, toolservice.DefaultModule)
		rt.PluginHost.Add(pluginhost.Failed(toolservice.DefaultModule, fmt.Errorf("required preparation failed")))
	}
	if change == "failed-current" {
		rt.PluginHost.Add(pluginhost.FailedWithStatus(pluginhost.Status{
			ID: "failed-existing-runtime", State: pluginhost.StateFailed, Error: "existing runtime crashed", StartedAt: time.Unix(1, 0),
		}))
	}
	if change == "removal" {
		if err := os.RemoveAll(filepath.Dir(prior)); err != nil {
			t.Fatal(err)
		}
	}
	if change == "source" {
		project := filepath.Join(rt.RootDir, ".wuu", "plugins", "previous-addon", "plugin.json")
		data, err := os.ReadFile(prior)
		if err != nil {
			t.Fatal(err)
		}
		writePluginPackageFile(t, project, string(data))
		if err := os.RemoveAll(filepath.Dir(prior)); err != nil {
			t.Fatal(err)
		}
	}
	if change == "config" || change == "trust" || change == "required" {
		changed, _, err := config.LoadPath(rt.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		switch change {
		case "config":
			changed.Agent.MaxSteps++
		case "trust":
			changed.Extensions.SetEnabled("plugin:user:previous-authority", false)
		case "required":
			changed.Composition = composition.DefaultConfig("headless", manifest.ID)
		}
		data, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		writePluginPackageFile(t, rt.ConfigPath, string(data))
	}
	writePluginPackageFile(t, installed, string(raw))
	epoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	refreshed := make(chan error, 1)
	go func() { refreshed <- srv.refreshPluginGenerationIfChanged() }()
	select {
	case connection = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("candidate never entered the real prepare callback")
	}
	if srv.pluginGenerationEpoch.Load() != baseEpoch || !rt.IsCurrentPluginGeneration(base) {
		t.Fatal("candidate became current before its preparation barrier completed")
	}
	additive := change == "additive" || change == "quarantined-current" || change == "cancel-mutation" || change == "cancel-shutdown" || change == "superseded-epoch"
	if additive {
		select {
		case err := <-refreshed:
			if err != nil {
				t.Fatalf("additive observation rejected its initiating caller: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("first additive observation waited for plugin preparation")
		}
	}
	if change == "cancel-mutation" || change == "cancel-shutdown" {
		cancelled := make(chan error, 1)
		go func() {
			if change == "cancel-shutdown" {
				srv.Close()
				cancelled <- nil
				return
			}
			finish, err := srv.beginPluginGenerationMutation("supersede additive preparation", pluginGenerationMutationLive)
			if err == nil {
				err = finish()
			}
			cancelled <- err
		}()
		select {
		case err := <-cancelled:
			if err != nil {
				t.Fatalf("cancel pending preparation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation waited for the plugin's unreleased prepare callback")
		}
		if srv.pluginGenerationEpoch.Load() == epoch || !rt.IsCurrentPluginGeneration(base) {
			t.Fatal("cancelled candidate replaced the committed generation")
		}
		if change == "cancel-mutation" {
			release()
			// The no-op mutation did not load the pending catalog. Its epoch
			// publication must not hide that observation from the next refresh.
			if err := srv.refreshPluginGenerationIfChanged(); err != nil {
				t.Fatalf("observe catalog after cancelled preparation: %v", err)
			}
			waitPluginGenerationWatchTest(t, func() bool { return !rt.IsCurrentPluginGeneration(base) })
			found := false
			for _, item := range rt.ExtensionSnapshot().Plugins {
				found = found || item.ID == manifest.ID
			}
			if !found {
				t.Fatal("mutation publication swallowed the pending addon catalog")
			}
		}
		return
	}
	if change == "superseded-epoch" {
		advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	}
	response := call("during-prepare", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: "while prepare remains held"})
	if change != "additive" && change != "quarantined-current" {
		if response["error"] == nil {
			t.Fatalf("%s transition admitted new input through old graph: %+v", change, response)
		}
		history, err := loadChatMessages(rt.Sessions, threadID)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range history {
			if message.Role == "user" && message.Content == "while prepare remains held" {
				t.Fatal("fenced input was appended before admission")
			}
		}
		if change == "superseded-epoch" {
			drained := make(chan struct{})
			go func() { srv.backgroundWG.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(5 * time.Second):
				t.Fatal("superseded candidate did not cancel while prepare remained held")
			}
			if srv.pluginGenerationEpoch.Load() != baseEpoch || !rt.IsCurrentPluginGeneration(base) {
				t.Fatal("superseded candidate published its stale snapshot")
			}
			return
		}
		release()
		select {
		case <-refreshed:
		case <-time.After(5 * time.Second):
			t.Fatal("fenced preparation did not finish after release")
		}
		return
	}
	if response["error"] != nil {
		t.Fatalf("additive optional preparation blocked unrelated warm conversation: %+v", response)
	}
	waitForTurnCompletedCountForThread(t, out, threadID, 2)
	cold := call("cold-create", MethodThreadStart, ThreadStartParams{})
	if cold["error"] != nil {
		t.Fatalf("cold thread creation blocked by optional preparation: %+v", cold)
	}
	coldID := remarshal[ThreadStartResult](t, cold["result"]).Thread.ID
	response = call("cold-turn", MethodTurnStart, TurnStartParams{ThreadID: coldID, Prompt: "cold turn while prepare remains held"})
	if response["error"] != nil {
		t.Fatalf("cold turn blocked by optional preparation: %+v", response)
	}
	waitForTurnCompletedCountForThread(t, out, coldID, 1)
	for _, id := range []string{threadID, coldID} {
		thread := srv.thread(id)
		thread.mu.Lock()
		generation := thread.execRuntime.PluginGeneration
		thread.mu.Unlock()
		if generation != base {
			t.Fatalf("conversation %s rebuilt from pending disk catalog instead of captured graph", id)
		}
	}
	if !rt.IsCurrentPluginGeneration(base) {
		t.Fatal("old graph was replaced while candidate preparation remained held")
	}
	release()
	waitPluginGenerationWatchTest(t, func() bool { return srv.pluginGenerationEpoch.Load() == epoch && !rt.IsCurrentPluginGeneration(base) })
	requireActiveRuntime(t, rt.PluginHost, manifest.ID)
	t.Log(fmt.Sprintf("warm and cold turns completed while real plugin prepare was held; generation %d published only after release", epoch))
}
