package pluginsettings

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// CAS must preserve settings written by concurrent shells, fingerprints and
// legacy files, and must not retry uncertain provider outcomes.
func TestSettingsServicePreservesLegacyAndConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	home, project, subject := t.TempDir(), t.TempDir(), "plugin:project:example"
	if _, err := Update(home, project, subject, ScopeWorkspace, "old", func(values map[string]json.RawMessage) error {
		values["legacy"] = json.RawMessage(`{"nested":true}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test.settings", home, project)
	client := settingsServiceClient(service)
	if _, err := client.Read(ctx, subject, ScopeWorkspace); err == nil {
		t.Fatal("prepared provider read")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Update(ctx, subject, ScopeWorkspace, "new", func(values map[string]json.RawMessage) error { values[key] = json.RawMessage(`true`); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := Read(home, project, subject, ScopeWorkspace)
	if err != nil || len(got.Values) != 5 || got.Fingerprint != "new" {
		t.Fatalf("persisted settings: %+v %v", got, err)
	}
	if _, err := client.UpdateState(ctx, subject, ScopeUser, func(values map[string]string) error { values["task"] = "complete"; return nil }); err != nil {
		t.Fatal(err)
	}
	state, err := ReadState(home, project, subject, ScopeUser)
	if err != nil || state.Values["task"] != "complete" {
		t.Fatalf("persisted state: %+v %v", state, err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx, subject, ScopeUser); err == nil {
		t.Fatal("closed provider read")
	}
	reopened := NewService("next.settings", home, project)
	if err := reopened.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = settingsServiceClient(reopened).Read(ctx, subject, ScopeWorkspace)
	if err != nil || len(got.Values) != 5 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
}
func TestSettingsClientDoesNotReplayUncertainUpdate(t *testing.T) {
	attempts := 0
	client := NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "read" {
			return json.Marshal(SettingsView{Document: emptyDocument("plugin:test"), Revision: "old"})
		}
		attempts++
		return nil, errors.New("outcome unknown")
	})
	_, err := client.Update(context.Background(), "plugin:test", ScopeUser, "fingerprint", func(values map[string]json.RawMessage) error { values["a"] = json.RawMessage(`1`); return nil })
	if err == nil || attempts != 1 {
		t.Fatalf("uncertain update attempts=%d error=%v", attempts, err)
	}
}
func settingsServiceClient(service *Service) *Client {
	return NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
}
