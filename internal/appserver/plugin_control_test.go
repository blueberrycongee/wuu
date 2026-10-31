package appserver

import (
	"context"
	"testing"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincontrol"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPluginControlReportsActualFailedRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "host-fixture"}, SubjectID: "plugin:dev:host-fixture", Fingerprint: "generation"}
	rt.Plugins = []pluginpkg.Plugin{item}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	snapshot := rt.ExtensionSnapshot()
	snapshot.PluginHost.Add(pluginhost.Failed(item.ID, errShutdown))
	reports, err := plugincontrol.Inspect(context.Background(), rt.WuuHome, item.ID)
	if err != nil || len(reports) != 1 {
		t.Fatalf("host reports: %+v %v", reports, err)
	}
	if len(reports[0].Plugins) != 1 || reports[0].Plugins[0].State != "failed" || reports[0].Plugins[0].Error == "" {
		t.Fatalf("failed runtime hidden: %+v", reports)
	}
}
