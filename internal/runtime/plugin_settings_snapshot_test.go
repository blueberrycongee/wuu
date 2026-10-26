package runtime

import (
	"context"
	"encoding/json"
	"testing"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// Contract failures: torn keys in one provider document; partial publication of
// rejected candidates; restart leakage; next-operation drift; leaked secrets or
// scopes; stale reads after retirement. Exercise the selected provider boundary.
func TestPluginSettingsSnapshotEffectiveness(t *testing.T) {
	ctx := context.Background()
	home, root := t.TempDir(), t.TempDir()
	item := serviceTestPlugin("configured", "plugin:user:configured", "one")
	item.Settings = map[string]pluginpkg.SettingDefinition{}
	for key, apply := range map[string]pluginpkg.SettingApplyMode{"live": pluginpkg.SettingApplyLive, "operation": pluginpkg.SettingApplyNextOperation, "resource": pluginpkg.SettingApplyRestart} {
		item.Settings[item.ID+"."+key] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeNumber, Default: float64(1), Scope: pluginpkg.SettingScopeUser, Apply: apply}
	}
	item.Settings[item.ID+".workspace"] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeString, Default: "local", Scope: pluginpkg.SettingScopeWorkspace, Apply: pluginpkg.SettingApplyLive}
	item.Settings[item.ID+".secret"] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeSecret, Default: "", Scope: pluginpkg.SettingScopeUser, Apply: pluginpkg.SettingApplyLive}
	provider := pluginsettings.NewService(pluginsettings.Module, home, root)
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(ctx) })
	reads := 0
	client := pluginsettings.NewClient(func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method == "read" {
			reads++
		}
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: pluginsettings.ServiceName, Method: method, Params: input})
	})
	makeGeneration := func() *pluginHostServices {
		s := newPluginHostServices(item, root, home, nil)
		s.settingsClient = client
		if err := s.prepareSettings(ctx); err != nil {
			t.Fatal(err)
		}
		s.activate()
		t.Cleanup(s.close)
		return s
	}
	s := makeGeneration()
	read := func(s *pluginHostServices, ctx context.Context, params string) pluginhost.SettingsListResult {
		t.Helper()
		raw, err := s.invoke(ctx, pluginhost.HostServiceSettingsList, json.RawMessage(params))
		if err != nil {
			t.Fatal(err)
		}
		var view pluginhost.SettingsListResult
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	change := func(value json.RawMessage) {
		t.Helper()
		_, err := client.Update(ctx, item.SubjectID, pluginsettings.ScopeUser, item.Fingerprint, func(values map[string]json.RawMessage) error {
			for _, key := range []string{"live", "operation", "resource"} {
				values[key] = value
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	opctx := context.WithValue(ctx, pluginSettingsOperationKey{}, &pluginhost.ExecutionScope{ID: "operation-one", PluginID: item.ID, Context: operation})
	beforeReads := reads
	before := read(s, opctx, `{}`)
	if reads-beforeReads != 2 {
		t.Fatalf("snapshot reread individual keys: %d provider reads", reads-beforeReads)
	}
	change(json.RawMessage(`2`))
	after := read(s, opctx, `{}`)
	if string(after.Entries["live"]) != "2" || string(after.Entries["operation"]) != "1" || string(after.Entries["resource"]) != "1" {
		t.Fatalf("effectiveness = %+v", after)
	}
	if before.Revision == after.Revision || len(after.RestartRequired) != 1 || after.RestartRequired[0] != "resource" {
		t.Fatalf("change metadata = %+v", after)
	}
	if string(before.Entries["live"]) != "1" {
		t.Fatal("previous snapshot mutated")
	}
	next := read(s, ctx, `{"scope":"user"}`)
	if string(next.Entries["operation"]) != "2" || next.Entries["workspace"] != nil || string(next.Entries["secret"]) != `""` {
		t.Fatalf("scope or secret leakage: %+v", next)
	}
	// An invalid persisted candidate must fail as a whole, not replace the last
	// accepted snapshot or poison the restart baseline.
	change(json.RawMessage(`"invalid"`))
	if _, err := s.invoke(ctx, pluginhost.HostServiceSettingsList, json.RawMessage(`{}`)); err == nil {
		t.Fatal("invalid candidate published")
	}
	change(json.RawMessage(`3`))
	recovered := read(s, ctx, `{}`)
	if string(recovered.Entries["resource"]) != "1" || string(recovered.Entries["live"]) != "3" {
		t.Fatalf("recovery = %+v", recovered)
	}
	replacement := makeGeneration()
	reloaded := read(replacement, ctx, `{}`)
	if string(reloaded.Entries["resource"]) != "3" || len(reloaded.RestartRequired) != 0 {
		t.Fatalf("reload did not adopt restart settings: %+v", reloaded)
	}
	// Retaining the old generation does not make it adopt structural settings.
	retained := read(s, opctx, `{}`)
	if string(retained.Entries["resource"]) != "1" || string(retained.Entries["operation"]) != "1" {
		t.Fatal("retained generation drifted")
	}
	cancel()
	if _, err := s.invoke(opctx, pluginhost.HostServiceSettingsList, json.RawMessage(`{}`)); err == nil {
		t.Fatal("ended operation admitted")
	}
	s.close()
	if _, err := s.invoke(ctx, pluginhost.HostServiceSettingsList, json.RawMessage(`{}`)); err == nil {
		t.Fatal("retired generation admitted")
	}
	t.Log("selected settings provider: consistent snapshot, live/operation/restart, invalid candidate, reload, scope and retirement verified")
}
