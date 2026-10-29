package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPluginClientInvalidationRejectsStaleAndForeignRoutes(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client { return &pluginClientRequestTestRuntime{id: id} })
	owner := srv.rt.ExtensionSnapshot().PluginHost
	event := pluginhost.ClientInvalidation{PluginID: item.SubjectID, Fingerprint: item.Fingerprint, ProductDomain: "coding", Topic: "reactions"}
	for name, change := range map[string]func(*pluginhost.ClientInvalidation){
		"foreign user":      func(e *pluginhost.ClientInvalidation) { e.UserSpaceID = "other" },
		"foreign domain":    func(e *pluginhost.ClientInvalidation) { e.ProductDomain = "personal" },
		"foreign plugin":    func(e *pluginhost.ClientInvalidation) { e.PluginID = "plugin:user:other" },
		"stale fingerprint": func(e *pluginhost.ClientInvalidation) { e.Fingerprint = "old" },
	} {
		bad := event
		change(&bad)
		if err := srv.invalidatePluginClient(context.Background(), owner, bad); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := srv.invalidatePluginClient(context.Background(), pluginhost.New(), event); err == nil {
		t.Fatal("old same-fingerprint host emitted")
	}
	if err := srv.invalidatePluginClient(context.Background(), owner, event); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, msg := range parseOutput(t, out.String()) {
		if msg["method"] == "plugin/client/invalidated" {
			count++
			got := remarshal[pluginhost.ClientInvalidation](t, msg["params"])
			if got != event {
				t.Fatalf("notification = %+v", got)
			}
		}
	}
	if count != 1 {
		t.Fatalf("invalidation count = %d", count)
	}
	srv.rt.ExtensionSettings.SetDisabled(item.SubjectID, true)
	if err := srv.invalidatePluginClient(context.Background(), owner, event); err == nil {
		t.Fatal("disabled plugin emitted")
	}
	srv.Close()
	if err := srv.invalidatePluginClient(context.Background(), owner, event); err == nil {
		t.Fatal("closed server emitted")
	}
}
