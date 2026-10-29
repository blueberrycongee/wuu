package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type settingsCalendarFixture struct {
	personalhost.Calendar
	connected       bool
	reads           int
	expectedSession string
}

func (f *settingsCalendarFixture) Status(_ context.Context, a personalcalendar.Actor) (personalcalendar.Connection, error) {
	state := "disconnected"
	if f.connected {
		state = "connected"
	}
	return personalcalendar.Connection{ID: "synthetic-connection", UserSpaceID: a.UserSpaceID, ProductDomain: "personal", Provider: "google-calendar", AccountID: "synthetic-account", State: state, SelectedCalendars: []string{"primary"}, SelectionRevision: "1"}, nil
}
func (f *settingsCalendarFixture) Authorize(ctx context.Context, a personalcalendar.Actor) (personalcalendar.Connection, error) {
	return f.Status(ctx, a)
}
func (f *settingsCalendarFixture) EventsPage(_ context.Context, a personalcalendar.Actor, id string, q personalcalendar.EventQuery) (json.RawMessage, error) {
	expectedSession := f.expectedSession
	if expectedSession == "" {
		expectedSession = "synthetic-main"
	}
	if a.UserSpaceID != "local" || a.SessionID != expectedSession || id != "synthetic-connection" || !f.connected {
		return nil, &personalcalendar.Failure{Code: "account_not_connected"}
	}
	f.reads++
	return json.RawMessage(`{"timeZone":"UTC","items":[{"id":"synthetic-event","status":"confirmed","summary":"Synthetic appointment","start":{"dateTime":"2026-10-24T10:00:00Z"},"end":{"dateTime":"2026-10-24T11:00:00Z"}}]}`), nil
}
func (f *settingsCalendarFixture) Disconnect(context.Context, personalcalendar.Actor, string, bool) (personalcalendar.DisconnectResult, error) {
	f.connected = false
	return personalcalendar.DisconnectResult{LocalDeleted: true, Revocation: "not_requested"}, nil
}
func (f *settingsCalendarFixture) Close() error { return nil }

// Real generated plugin processes, execution-context propagation, service
// registry and selected document storage. Only Calendar and the admission bootstrap are
// synthetic. This never reads Keychain, calls Google, or configures a model.
func TestPersonalSettingsGeneratedRuntimeHostEndToEnd(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	home := t.TempDir()
	project := binding.Root(home)
	state, err := userHostWorkspaceStateDir(home, project, binding)
	if err != nil {
		t.Fatal(err)
	}
	router := NewPluginSessionRouter()
	unbindAccess := router.BindSessionResourceAccess(func(ctx context.Context, _ string, id string, _ *pluginhost.ExecutionAuthorizationReference, _ string) (context.Context, error) {
		caller, ok := principal.FromContext(ctx)
		if !ok || !caller.IsOwner() || (id != "synthetic-main" && id != "") {
			return ctx, errors.New("synthetic session policy denied")
		}
		return ctx, caller.Check(ctx)
	})
	defer unbindAccess()
	calendar := &settingsCalendarFixture{connected: true}
	personal := personalhost.New(binding, personalhost.Options{Calendar: calendar, Authorize: func(context.Context, string, string) error { return nil }})
	defer personal.Close()
	unbind := router.BindPersonalHost(personal.Call)
	defer unbind()
	items := []pluginpkg.Plugin{}
	for _, id := range []string{"personal", "personal-calendar"} {
		root, err := filepath.Abs(filepath.Join("..", "plugin", "bundled", id))
		if err != nil {
			t.Fatal(err)
		}
		item := serviceTestPlugin(id, "plugin:user:"+id, "integration-fixture")
		item.Root = root
		item.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.mjs"}, Timeout: 10}
		items = append(items, item)
	}
	selection := composition.DefaultConfig("headless", "personal", "personal-calendar")
	selection.Services = []composition.Binding{{Service: "agent.prompt", Major: 1, Provider: "personal"}}
	host, kernel, err := buildPluginHostForBinding(context.Background(), items, project, "", home, state, nil, func(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		return pluginhost.Start(ctx, cfg)
	}, router, nil, selection, false, "", binding, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := activatePluginHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	_, err = kernel.state.sessions.CreateInitialized(session.Session{ID: "synthetic-main", UserSpaceID: "local", ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", AgentID: "main"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	owner = principal.WithContext(owner, principal.LocalOwner())
	capability, ok := host.Capability("personal", pluginhost.CapabilityPluginClientRequest)
	if !ok {
		t.Fatalf("Personal client capability missing: %+v", host.Statuses())
	}
	invoke := func(ctx context.Context, method string, input any) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		var output pluginhost.PluginClientRequestOutput
		err := host.InvokeCapability(ctx, capability, pluginhost.PluginClientRequestInput{Method: method, Input: raw}, &output)
		return output.Result, err
	}
	if _, err := invoke(context.Background(), "connections.status", map[string]any{"owner": true}); err == nil {
		t.Fatal("payload forged owner authority")
	}
	status, err := invoke(owner, "connections.status", map[string]any{})
	if err != nil || !strings.Contains(string(status), `"connected"`) {
		t.Fatalf("status=%s err=%v", status, err)
	}
	review, err := invoke(owner, "review.list", map[string]any{})
	if err != nil || !strings.Contains(string(review), `"unavailable"`) {
		t.Fatalf("review=%s err=%v", review, err)
	}
	query := json.RawMessage(`{"session_id":"synthetic-main","calendar_id":"primary","time_min":"2026-10-24T00:00:00Z","time_max":"2026-10-25T00:00:00Z","timezone":"UTC"}`)
	read := func(ctx context.Context) (json.RawMessage, error) {
		value, err := host.ServiceRegistry().Call(ctx, "personal", pluginhost.ServiceCallParams{Service: "calendar.personal", Method: "refresh", Params: query})
		if err != nil {
			return nil, err
		}
		return value, nil
	}
	if _, err := read(context.Background()); err == nil {
		t.Fatal("unverified calendar read reached session context")
	}
	agenda, err := read(principal.WithContext(owner, principal.LocalOwner()))
	if err != nil || !strings.Contains(string(agenda), "Synthetic appointment") || calendar.reads != 1 {
		t.Fatalf("agenda=%s err=%v reads=%d", agenda, err, calendar.reads)
	}
	if _, err := invoke(owner, "connections.disconnect", map[string]any{"connection_id": "synthetic-connection", "revoke": false}); err != nil {
		t.Fatal(err)
	}
	agenda, err = read(principal.WithContext(owner, principal.LocalOwner()))
	if err != nil || strings.Contains(string(agenda), "Synthetic appointment") || calendar.connected {
		t.Fatalf("disconnect retained data: %s %v", agenda, err)
	}
	cancel()
	if _, err := invoke(context.WithoutCancel(owner), "connections.status", map[string]any{}); err == nil {
		t.Fatal("revoked connection retained control")
	}
	t.Log("generated Personal → real registry/context → generated Calendar → host broker fixture: owner status, model read, cache clearing and revoked-owner rejection passed; Google/Keychain/model calls=0")
}
