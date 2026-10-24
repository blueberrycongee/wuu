package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/skills"
)

// A failed rebuild must not republish the provider's or consumer's last-good UI
// and skills, and must retain the original crash for recovery diagnostics.
func TestFailedPluginRecoveryPublishesRevokedConsumerState(t *testing.T) {
	model := &fakeClient{}
	rt := newTestRuntime(t, model)
	rt.WuuHome = t.TempDir()
	provider := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "provider"}, SubjectID: "plugin:user:provider", Fingerprint: "provider-one", Source: "user"}
	consumer := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "consumer", Requires: []string{"provider"}, Desktop: &pluginpkg.DesktopSpec{Entry: "desktop.js"}}, SubjectID: "plugin:user:consumer", Fingerprint: "consumer-one", Source: "user"}
	rt.Plugins = []pluginpkg.Plugin{provider, consumer}
	rt.ActivePlugins = append([]pluginpkg.Plugin(nil), rt.Plugins...)
	rt.Skills = []skills.Skill{{Name: "provider-guide", Source: "plugin:provider"}, {Name: "consumer-guide", Source: "plugin:consumer"}, {Name: "local-guide", Source: "project"}}
	rt.ExtensionSettings = &extensions.Settings{}
	for _, item := range rt.Plugins {
		if err := rt.ExtensionSettings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint}); err != nil {
			t.Fatal(err)
		}
	}
	client := &dependencyInventoryClient{id: provider.ID}
	preparePluginRuntimePathsForTest(t, rt)
	bindConfigSourceForTest(t, rt)
	rt.PluginHost.Add(client)
	rt.PluginHost.SetPackageRequirements(map[string][]string{provider.ID: nil, consumer.ID: {provider.ID}})
	out := &lockedBuffer{}
	srv := &Server{rt: rt, out: out, threads: make(map[string]*threadState)}
	_, before := srv.currentExtensionState()
	if len(before) != 3 {
		t.Fatalf("initial skills = %+v", before)
	}
	client.failed = true
	sess, err := rt.Sessions.CreateWithMetadata("retirement-admission", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	retirementCalls := 0
	client.onClose = func() {
		retirementCalls++
		// Host has removed the failed client, but runtime has not restored its
		// failed diagnostic yet. Health alone cannot identify this transition.
		if rt.PluginGenerationNeedsRecovery() {
			t.Error("fixture did not enter the failed-client retirement gap")
		}
		if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, errPluginGenerationRefreshBusy) {
			t.Errorf("retirement callback bypassed refresh admission: %v", err)
		}
		th.mu.Lock()
		acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
		if acquired {
			th.releaseThreadExecutionLeaseLocked()
		}
		th.mu.Unlock()
		if err != nil || acquired {
			t.Errorf("retirement callback admitted execution: acquired=%t err=%v", acquired, err)
		}
	}
	recoveryErr := errors.New("recovery build failed")
	srv.refreshExtensionsForTest = func(config.Config) error { return recoveryErr }
	if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, recoveryErr) {
		t.Fatalf("recovery result = %v", err)
	}
	if retirementCalls != 1 {
		t.Fatalf("failed client closed %d times, want one", retirementCalls)
	}
	assertFakeClientRequestCount(t, model, 0)
	if records, err := rt.Sessions.LoadHistoryRecords(sess.ID, true); err != nil || len(records) != 0 {
		t.Fatalf("retirement admission changed user history: records=%+v err=%v", records, err)
	}
	catalog, _, err := srv.skillCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 {
		t.Fatalf("revoked skill remains in catalog: %+v", catalog)
	}
	for _, item := range catalog {
		if item.Source == "plugin:consumer" || item.Source == "plugin:provider" {
			t.Fatal("failed provider or dependent skill remains visible")
		}
	}
	trust := srv.currentExtensionTrustSummary().MainSession
	if trust.Skills.Count != 1 || trust.Plugins.Count != 0 {
		t.Fatalf("stale trust summary: %+v", trust)
	}
	if _, _, err := srv.requireActiveDesktopPluginHost(consumer.SubjectID, consumer.Fingerprint); err == nil {
		t.Fatal("revoked desktop consumer can still call state APIs")
	}
	if len(rt.Skills) != 3 || len(rt.ActivePlugins) != 2 {
		t.Fatal("invalidation mutated the published generation slices")
	}
	messages := parseOutput(t, out.String())
	var notified bool
	for _, message := range messages {
		if message["method"] != NotificationPluginInventoryChanged {
			continue
		}
		notified = true
		notice := remarshal[PluginInventoryChangedNotification](t, message["params"])
		if len(notice.Skills) != 1 {
			t.Fatalf("failure notification leaked skills: %+v", notice.Skills)
		}
		record := findPluginExtensionRecord(t, notice.ExtensionInventory, "consumer")
		if record.RuntimeState != ExtensionRuntimeFailed && record.RuntimeState != ExtensionRuntimeInactive {
			t.Fatalf("consumer still loadable after crash: %+v", record)
		}
	}
	if !notified {
		t.Fatal("failed recovery did not publish inventory invalidation")
	}
	if !rt.PluginGenerationNeedsRecovery() {
		t.Fatal("cleanup erased the crash that still requires recovery")
	}
}

type dependencyInventoryClient struct {
	id      string
	failed  bool
	onClose func()
}

func (c *dependencyInventoryClient) ID() string { return c.id }
func (c *dependencyInventoryClient) Status() pluginhost.Status {
	state := pluginhost.StateActive
	if c.failed {
		state = pluginhost.StateFailed
	}
	return pluginhost.Status{ID: c.id, State: state, StartedAt: time.Unix(1, 0)}
}
func (c *dependencyInventoryClient) Close(context.Context) error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

func TestFailedDeclarativeConsumerCannotLoadDesktopModule(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		return pluginhost.Failed(id, errors.New("required provider exited"))
	})
	callPluginPackageRPC(t, srv, "load-failed", MethodPluginDesktopModuleRead, PluginDesktopModuleReadParams{ID: item.SubjectID, Fingerprint: item.Fingerprint})
	response := responseByID(t, parseOutput(t, out.String()), "load-failed")
	failure, _ := json.Marshal(response["error"])
	if !strings.Contains(string(failure), "desktop plugin runtime is no longer active") {
		t.Fatalf("desktop load was not rejected for failed runtime: %s", failure)
	}
	if _, _, err := srv.requireActiveDesktopPluginHost(item.SubjectID, item.Fingerprint); err == nil {
		t.Fatal("failed declarative consumer retained state access")
	}
}
