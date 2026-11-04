package appserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Selection must stay scoped to the owning plugin and idle session; a failed
// update cannot alter the active selection or another session.
func TestPluginSessionModelSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	if err := os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test-key","model":"fake-model"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	rt = newSettingsRefreshRuntime(t, rt)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, personalTestOwner(context.Background()))
	owner, err := clientauthority.AdmitDesktop(ctx, rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	p := pluginhost.SessionCreateParams{RequestID: "model-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "main"}
	main, err := srv.createPluginSession(ctx, "personal", p)
	if err != nil {
		t.Fatal(err)
	}
	p.RequestID, p.ParentSessionID = "model-child", main.SessionID
	child, err := srv.createPluginSession(ctx, "personal", p)
	if err != nil {
		t.Fatal(err)
	}
	request := pluginhost.SessionModelParams{SessionID: main.SessionID, Provider: "fake-provider", Model: "new-model"}
	if _, err := srv.pluginSessionModel(ctx, "personal", request); err == nil {
		t.Fatal("model change accepted without owner UI")
	}
	if _, err := srv.pluginSessionModel(owner, "other", request); err == nil {
		t.Fatal("foreign plugin changed session")
	}
	if _, err := srv.pluginSessionModel(owner, "personal", request); err != nil {
		t.Fatal(err)
	}
	got, _, err := rt.Sessions.Find(main.SessionID)
	if err != nil || got.Model != "new-model" {
		t.Fatalf("main selection: %+v %v", got, err)
	}
	old, _, err := rt.Sessions.Find(child.SessionID)
	if err != nil || old.Model != "fake-model" {
		t.Fatalf("existing child changed: %+v %v", old, err)
	}
	if rt.Model != "fake-model" {
		t.Fatal("global default changed")
	}
	request.Provider = "missing-provider"
	if _, err := srv.pluginSessionModel(owner, "personal", request); err == nil {
		t.Fatal("missing provider accepted")
	}
	got, _, _ = rt.Sessions.Find(main.SessionID)
	if got.Model != "new-model" {
		t.Fatal("failed update changed selection")
	}
	th := srv.thread(main.SessionID)
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	request.Provider, request.Model = "fake-provider", "other-model"
	if _, err := srv.pluginSessionModel(owner, "personal", request); err == nil {
		t.Fatal("running session changed")
	}
	th.mu.Lock()
	th.running = false
	th.mu.Unlock()
}
