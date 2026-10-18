package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserSpaceSessionBindingRecoveryAndCrossDomainDenial(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.UserSpaceID = "local"
	rt.ProductDomain = "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	p := pluginhost.SessionCreateParams{RequestID: "stable-main", Name: "Mosaic", Visibility: "plugin", ContextSource: "fresh", UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent-main"}
	one, err := srv.createPluginSession(context.Background(), "personal", p)
	if err != nil {
		t.Fatal(err)
	}
	two, err := srv.createPluginSession(context.Background(), "personal", p)
	if err != nil || one.SessionID != two.SessionID || two.Created {
		t.Fatalf("duplicate: %+v %v", two, err)
	}
	metadata, _, err := session.Find(rt.SessionDir, one.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.UserSpaceID != "local" || metadata.ProductDomain != "personal" || metadata.AgentID != "agent-main" || metadata.WorkspaceID != "" {
		t.Fatalf("wrong scope: %+v", metadata)
	}
	for _, foreign := range []pluginhost.SessionCreateParams{
		{RequestID: "other-domain", Visibility: "plugin", ContextSource: "fresh", UserSpaceID: "local", ProductDomain: "coding"},
		{RequestID: "other-space", Visibility: "plugin", ContextSource: "fresh", UserSpaceID: "other", ProductDomain: "personal"},
		{RequestID: "project", Visibility: "plugin", ContextSource: "fresh", WorkspaceID: "project"},
	} {
		if _, err := srv.createPluginSession(context.Background(), "personal", foreign); err == nil {
			t.Fatalf("accepted foreign binding %+v", foreign)
		}
	}
	p.RequestID = "child"
	p.ParentSessionID = one.SessionID
	p.AgentID = "spoofed"
	if _, err := srv.createPluginSession(context.Background(), "personal", p); err == nil {
		t.Fatal("child changed responsible agent")
	}
	p.AgentID = ""
	child, err := srv.createPluginSession(context.Background(), "personal", p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := session.Find(rt.SessionDir, child.SessionID)
	if got.AgentID != "agent-main" {
		t.Fatal("child lost responsible agent")
	}
}

func TestPluginClientRequestRejectsMismatchedHostScopeBeforePluginAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.UserSpaceID = "local"
	rt.ProductDomain = "personal"
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"scope","method":"plugin/client/request","params":{"id":"personal","fingerprint":"x","method":"agents.list","user_space_id":"other","product_domain":"personal"}}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "another user space or product domain") {
		t.Fatalf("scope was not checked before plugin lookup: %s", out.String())
	}
}

func TestUserHostRejectsAdHocThreadCreationAndFork(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.UserSpaceID = "local"
	rt.ProductDomain = "personal"
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	for _, method := range []string{"thread/start", "thread/fork"} {
		raw := []byte(fmt.Sprintf(`{"id":%q,"method":%q,"params":{}}`, method, method))
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		row := responseByID(t, parseOutput(t, out.String()), method)
		encoded, _ := json.Marshal(row)
		if !strings.Contains(string(encoded), "named-agent session service") {
			t.Errorf("%s bypassed named-agent lifecycle: %s", method, encoded)
		}
	}
}

func TestUserHostClientStorageUsesSameScopedAuthorityAsRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID = "local"
	rt.ProductDomain = "personal"
	rt.DataRoot = filepath.Join(t.TempDir(), "personal-state")
	rt.WuuHome = t.TempDir()
	rt.RootDir = t.TempDir()
	manifest := filepath.Join(t.TempDir(), "plugin.json")
	writePluginPackageFile(t, manifest, `{"id":"scope-demo","desktop":{"entry":"desktop.js"}}`)
	writePluginPackageFile(t, filepath.Join(filepath.Dir(manifest), "desktop.js"), `export const activate=()=>{};`)
	item, err := pluginpkg.LoadManifest(manifest, "user")
	if err != nil {
		t.Fatal(err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}
	rt.ExtensionSettings = &extensions.Settings{}
	if err := rt.ExtensionSettings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	callPluginPackageRPC(t, srv, "scoped-storage", MethodPluginStorageSet, PluginStorageSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: PluginValueScopeUser, Key: "scope-test", Value: "Personal only"})
	if responseByID(t, parseOutput(t, out.String()), "scoped-storage")["error"] != nil {
		t.Fatal(out.String())
	}
	stored, err := pluginsettings.ReadState(srv.rt.DataRoot, srv.rt.RootDir, item.SubjectID, pluginsettings.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Values["scope-test"] != "Personal only" {
		t.Fatal("client state bypassed Personal authority root")
	}
	coding, err := pluginsettings.ReadState(srv.rt.WuuHome, srv.rt.RootDir, item.SubjectID, pluginsettings.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if coding.Values["scope-test"] != "" {
		t.Fatal("Personal state leaked to Coding namespace")
	}
}

func TestInitializeReportsImmutableUserHostScopeAndLegacyAbsence(t *testing.T) {
	for _, userHost := range []bool{false, true} {
		rt := newTestRuntime(t, &fakeClient{})
		if userHost {
			rt.UserSpaceID = "local"
			rt.ProductDomain = "personal"
		}
		out := &lockedBuffer{}
		srv := New(rt, out)
		if err := srv.handleLine(context.Background(), []byte(`{"id":"scope-init","method":"initialize"}`)); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), "scope-init")
		result, _ := response["result"].(map[string]any)
		if userHost {
			if result["user_space_id"] != "local" || result["product_domain"] != "personal" {
				t.Fatalf("scope missing: %+v", result)
			}
		} else if result["user_space_id"] != nil || result["product_domain"] != nil {
			t.Fatal("legacy Coding gained a false user scope")
		}
		srv.Close()
	}
}
