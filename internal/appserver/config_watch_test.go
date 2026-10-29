package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

func TestAgentFileEditsPersistAndRefreshUserConfig(t *testing.T) {
	for _, mode := range []string{"standard", "read_only", "unconfined"} {
		t.Run(mode, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
			t.Setenv("WUU_HOME", rt.WuuHome)
			rt.ConfigPath = filepath.Join(rt.WuuHome, "config.json")
			if err := os.MkdirAll(rt.WuuHome, 0o700); err != nil {
				t.Fatal(err)
			}
			initial := `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key_env":"WUU_CONFIG_TEST_KEY","model":"fake-model"}},"agent":{"max_steps":10}}`
			t.Setenv("WUU_CONFIG_TEST_KEY", "test-key")
			if err := os.WriteFile(rt.ConfigPath, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}
			rt = newConfigRefreshRuntime(t, rt)
			configured, err := os.ReadFile(rt.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			initial = string(configured)
			kit, err := tools.New(rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			kit.SetBoundary(runtime.BoundaryForMode(mode))
			kit.SetPermissionMode(mode)
			// Exclude shared temp so this fixture exercises the runtime scope
			// instead of granting Wuu-home access through its temporary parent.
			roots := workspaces.BoundaryRoots(rt.RootDir, rt.WuuHome)
			var scoped []string
			for _, root := range roots {
				if filepath.Clean(root) != filepath.Clean(os.TempDir()) {
					scoped = append(scoped, root)
				}
			}
			kit.SetFileScopeRoots(scoped)
			call := func(name string, args map[string]any) (string, error) {
				t.Helper()
				data, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				return kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: string(data)})
			}
			if result, err := call("read_file", map[string]any{"path": rt.ConfigPath}); err != nil || !strings.Contains(result, "fake-model") {
				t.Fatalf("read user config: result=%q err=%v", result, err)
			}
			bindConfigSourceForTest(t, rt)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			added := `"added":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key_env":"WUU_CONFIG_TEST_KEY","model":"added-model"},`
			_, err = call("edit_file", map[string]any{"path": rt.ConfigPath, "old_text": `"providers":{`, "new_text": `"providers":{` + added})
			if mode == "read_only" {
				if err == nil {
					t.Fatal("read-only agent changed user config")
				}
				data, readErr := os.ReadFile(rt.ConfigPath)
				if readErr != nil || string(data) != initial {
					t.Fatalf("read-only refusal changed the file: %s, %v", data, readErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("add provider: %v", err)
			}
			if _, err := call("edit_file", map[string]any{"path": rt.ConfigPath, "old_text": `"default_provider":"fake-provider"`, "new_text": `"default_provider":"added"`}); err != nil {
				t.Fatalf("select added provider: %v", err)
			}
			if _, err := call("edit_file", map[string]any{"path": rt.ConfigPath, "old_text": `"max_steps":10`, "new_text": `"max_steps":20`}); err != nil {
				t.Fatalf("edit settings: %v", err)
			}
			cfg, _, err := rt.LoadEffectiveConfig()
			if err != nil || cfg.DefaultProvider != "added" || cfg.Providers["added"].Model != "added-model" || cfg.Agent.MaxSteps != 20 {
				t.Fatalf("edits did not survive a config reload: %+v, %v", cfg, err)
			}
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			if rt.ProviderName != "added" || rt.Model != "added-model" || len(srv.providerSummaries()) != 2 {
				t.Fatalf("model refresh did not apply file edits: provider=%s model=%s inventory=%+v", rt.ProviderName, rt.Model, srv.providerSummaries())
			}
			notifications := notificationsByMethod(parseOutput(t, out.String()), NotificationConfigChanged)
			if len(notifications) == 0 {
				t.Fatalf("file edit did not publish config/changed: %s", out.String())
			}
			publications := notificationsByMethod(parseOutput(t, out.String()), NotificationPluginInventoryChanged)
			if len(publications) == 0 {
				t.Fatal("config watcher replaced the desktop graph without publishing inventory")
			}
			published := remarshal[PluginInventoryChangedNotification](t, publications[len(publications)-1]["params"])
			if len(published.ExtensionInventory) == 0 {
				t.Fatal("config watcher published empty inventory")
			}
			for _, item := range published.ExtensionInventory {
				if item.Kind == extensions.KindPlugin && item.GraphGeneration != rt.ExtensionSnapshot().GenerationID {
					t.Fatalf("config watcher left desktop routes on stale graph: %+v", item)
				}
			}
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			if got := len(notificationsByMethod(parseOutput(t, out.String()), NotificationPluginInventoryChanged)); got != len(publications) {
				t.Fatal("unchanged configuration churned desktop inventory")
			}
			// The watcher may publish intermediate edits before the explicit refresh.
			// The last notification must describe the final saved selection.
			changed := remarshal[ConfigChangedNotification](t, notifications[len(notifications)-1]["params"])
			if changed.Provider != "added" || changed.Model != "added-model" {
				t.Fatalf("config/changed did not publish the final selection: %+v", changed)
			}
			if mode == "standard" {
				outside := filepath.Join(t.TempDir(), "outside.json")
				if _, err := call("write_file", map[string]any{"path": outside, "content": "{}"}); err == nil {
					t.Fatal("config access also allowed writes outside the file scope")
				}
			}
		})
	}
}

func TestConfigWatchUsesEventsInsteadOfContinuousReloads(t *testing.T) {
	root := t.TempDir()
	wuuHome := t.TempDir()
	refreshes := make(chan struct{}, 8)
	srv := &Server{
		rt: &runtime.Session{
			RootDir:    root,
			WuuHome:    wuuHome,
			ConfigPath: filepath.Join(root, ".wuu.json"),
		},
		out:     &lockedBuffer{},
		threads: map[string]*threadState{},
		refreshConfigForTest: func() error {
			refreshes <- struct{}{}
			return nil
		},
	}
	srv.startConfigWatch()
	t.Cleanup(func() {
		srv.closed.Store(true)
		srv.backgroundWG.Wait()
	})

	select {
	case <-refreshes:
	case <-time.After(3 * time.Second):
		t.Fatal("config watcher did not establish its initial baseline")
	}

	select {
	case <-refreshes:
		t.Fatal("config watcher reloaded an unchanged config")
	case <-time.After(700 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "runtime-state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshes:
		t.Fatal("config watcher reloaded after an unrelated runtime-state change")
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(root, ".wuu.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshes:
	case <-time.After(3 * time.Second):
		t.Fatal("config watcher did not refresh after a filesystem event")
	}
}

func TestConfigRefreshPreservesInventoryUntilInvalidDocumentIsRepaired(t *testing.T) {
	for _, watcherFirst := range []bool{false, true} {
		name := "explicit first"
		if watcherFirst {
			name = "watcher first"
		}
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			valid := `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test-key","model":"fake-model"}}}`
			write := func(contents string) {
				t.Helper()
				if err := os.WriteFile(rt.ConfigPath, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(valid)
			rt = newConfigRefreshRuntime(t, rt)
			configured, err := os.ReadFile(rt.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			valid = string(configured)
			bindConfigSourceForTest(t, rt)
			out := &lockedBuffer{}
			// Prevent an independent watcher from racing this ordering test. The
			// watcher event path is covered by TestConfigWatchUsesEventsInsteadOfContinuousReloads.
			srv := New(rt, nil)
			srv.out = out
			t.Cleanup(srv.Close)
			before := srv.providerSummaries()
			if len(before) != 1 {
				t.Fatalf("expected configured provider: %+v", before)
			}
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}

			// A newer build can add a field unknown to this reader.
			write(strings.Replace(valid, `"default_provider"`, `"future_setting":{},"default_provider"`, 1))
			first, second := srv.refreshConfigIfChanged, srv.refreshObservedConfigChange
			if watcherFirst {
				first, second = second, first
			}
			var configErr *pluginhost.HostServiceError
			if err := first(); !errors.As(err, &configErr) || configErr.Code != "config_invalid" {
				t.Fatalf("first observer must report a strict config error, got %v", err)
			}
			if err := second(); err != nil {
				t.Fatalf("second observer must suppress the already reported error: %v", err)
			}
			messages := parseOutput(t, out.String())
			if len(notificationsByMethod(messages, NotificationConfigError)) != 1 {
				t.Fatalf("expected one visible configuration error: %s", out.String())
			}
			if got := srv.providerSummaries(); !reflect.DeepEqual(got, before) {
				t.Fatalf("invalid refresh erased inventory: %+v", got)
			}
			for i := 0; i < 3; i++ {
				if err := srv.refreshConfigIfChanged(); err != nil {
					t.Fatalf("unchanged rejected document was retried: %v", err)
				}
			}
			if len(notificationsByMethod(parseOutput(t, out.String()), NotificationConfigError)) != 1 {
				t.Fatal("unchanged rejected document emitted repeated errors")
			}
			if err := srv.handleLine(context.Background(), []byte(`{"id":"blocked","method":"config/model/update","params":{"model":"other-model"}}`)); err != nil {
				t.Fatal(err)
			}
			rejection := remarshal[ResponseError](t, responseByID(t, parseOutput(t, out.String()), "blocked")["error"])
			if rejection.Code == "" {
				t.Fatalf("mutation must reject the invalid config, got %+v", rejection)
			}

			// Restoring the exact previous contents must clear the error too.
			write(valid)
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatalf("repair did not recover: %v", err)
			}
			if len(notificationsByMethod(parseOutput(t, out.String()), NotificationConfigChanged)) != 1 {
				t.Fatal("repair did not publish the recovered configuration")
			}
			write(strings.Replace(valid, "fake-model", "repaired-model", 1))
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			if got := srv.providerSummaries(); len(got) != 1 || got[0].Model != "repaired-model" {
				t.Fatalf("inventory did not recover: %+v", got)
			}
		})
	}
}

// Model publication and catalog reload share one lock order. Read-only config
// checks do not fence admission; failed mutation admission releases both locks.
func TestModelConfigurationPublicationAdmission(t *testing.T) {
	srv := &Server{}
	admit, release := srv.lockModelConfiguration()
	if srv.pluginGenerationMutation.Load() {
		t.Fatal("read-only config inspection fenced admission")
	}
	if err := admit(); err != nil {
		t.Fatal(err)
	}
	if !srv.pluginGenerationMutation.Load() {
		t.Fatal("publication did not fence admission")
	}
	started, acquired := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		nextAdmit, nextRelease := srv.lockModelConfiguration()
		defer nextRelease()
		if err := nextAdmit(); err != nil {
			t.Error(err)
		}
		close(acquired)
	}()
	<-started
	select {
	case <-acquired:
		t.Fatal("concurrent publication crossed held generation lock")
	default:
	}
	release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("model publication locks did not release")
	}
	// Wait for the second transaction's release by reacquiring the same locks.
	admit, release = srv.lockModelConfiguration()
	srv.pluginGenerationMutation.Store(true) // Another publisher already reserved admission.
	if err := admit(); err == nil {
		t.Fatal("overlapping publication reservation was accepted")
	}
	release()
	if !srv.pluginGenerationMutation.Load() {
		t.Fatal("failed admission cleared another publisher's fence")
	}
	srv.pluginGenerationMutation.Store(false)
	admit, release = srv.lockModelConfiguration()
	if err := admit(); err != nil {
		t.Fatal(err)
	}
	release()
	if srv.pluginGenerationMutation.Load() {
		t.Fatal("publication leaked admission fence")
	}
}

// A failed activation may already have committed a new graph. Its inventory
// must still revoke the previous desktop handles while preserving the error.
func TestModelConfigurationPublishesCommittedFailure(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	preparePluginRuntimePathsForTest(t, rt)
	var fail atomic.Bool
	rt = newConfigRefreshRuntime(t, rt, runtime.NativeProviderFactory{
		ID: "fixture.config-publication", DefaultEnabled: true,
		Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
			return &configPublicationFailureClient{fail: fail.Load()}, nil
		},
	})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	before := rt.ExtensionSnapshot().GenerationID
	fail.Store(true)
	callPluginPackageRPC(t, srv, "failed-model", MethodConfigModelUpdate, ConfigModelUpdateParams{Model: "new-model"})
	messages := parseOutput(t, out.String())
	if message := responseErrorMessage(t, responseByID(t, messages, "failed-model")); !strings.Contains(message, "fixture activation failed") {
		t.Fatalf("committed activation failure was hidden: %s", message)
	}
	if rt.ExtensionSnapshot().GenerationID == before {
		t.Fatal("fixture did not exercise a committed graph replacement")
	}
	publications := notificationsByMethod(messages, NotificationPluginInventoryChanged)
	if len(publications) == 0 {
		t.Fatal("failed replacement left the previous desktop inventory live")
	}
	published := remarshal[PluginInventoryChangedNotification](t, publications[len(publications)-1]["params"])
	if len(published.ExtensionInventory) == 0 {
		t.Fatal("failed replacement published empty desktop inventory")
	}
	for _, item := range published.ExtensionInventory {
		if item.Kind == extensions.KindPlugin && item.GraphGeneration != rt.ExtensionSnapshot().GenerationID {
			t.Fatalf("failed replacement published stale routes: %+v", item)
		}
	}
}

type configPublicationFailureClient struct {
	fail   bool
	active atomic.Bool
}

func (*configPublicationFailureClient) ID() string { return "fixture.config-publication" }
func (c *configPublicationFailureClient) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if c.active.Load() {
		state = pluginhost.StateActive
		if c.fail {
			state = pluginhost.StateFailed
		}
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (c *configPublicationFailureClient) Activate(context.Context) error {
	c.active.Store(true)
	if c.fail {
		return errors.New("fixture activation failed")
	}
	return nil
}
func (*configPublicationFailureClient) Close(context.Context) error { return nil }
