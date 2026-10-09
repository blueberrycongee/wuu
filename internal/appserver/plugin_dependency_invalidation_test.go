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

// A failed rebuild must not republish the consumer's last-good UI and skills.
func TestFailedPluginRecoveryPublishesRevokedConsumerState(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
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
	rt.PluginHost = pluginhost.New(client)
	rt.PluginHost.SetPackageRequirements(map[string][]string{provider.ID: nil, consumer.ID: {provider.ID}})
	out := &lockedBuffer{}
	srv := &Server{rt: rt, out: out}
	_, before := srv.currentExtensionState()
	if len(before) != 3 {
		t.Fatalf("initial skills = %+v", before)
	}
	client.failed = true
	recoveryErr := errors.New("recovery build failed")
	srv.refreshExtensionsForTest = func(config.Config) error { return recoveryErr }
	if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, recoveryErr) {
		t.Fatalf("recovery result = %v", err)
	}
	catalog, _, err := srv.skillCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 {
		t.Fatalf("revoked skill remains in catalog: %+v", catalog)
	}
	for _, item := range catalog {
		if item.Source == "plugin:consumer" {
			t.Fatal("consumer skill remains visible")
		}
	}
	trust := srv.currentExtensionTrustSummary().MainSession
	if trust.Skills.Count != 2 || trust.Plugins.Count != 1 {
		t.Fatalf("stale trust summary: %+v", trust)
	}
	if _, err := srv.requireActiveDesktopPlugin(consumer.SubjectID, consumer.Fingerprint); err == nil {
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
		if len(notice.Skills) != 2 {
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
}

type dependencyInventoryClient struct {
	id     string
	failed bool
}

func (c *dependencyInventoryClient) ID() string { return c.id }
func (c *dependencyInventoryClient) Status() pluginhost.Status {
	state := pluginhost.StateActive
	if c.failed {
		state = pluginhost.StateFailed
	}
	return pluginhost.Status{ID: c.id, State: state, StartedAt: time.Unix(1, 0)}
}
func (c *dependencyInventoryClient) Close(context.Context) error { return nil }

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
	if _, err := srv.requireActiveDesktopPlugin(item.SubjectID, item.Fingerprint); err == nil {
		t.Fatal("failed declarative consumer retained state access")
	}
}
