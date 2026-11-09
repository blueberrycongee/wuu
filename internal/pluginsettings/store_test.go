package pluginsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdatePersistsUserValuesAndReturnsCopies(t *testing.T) {
	home := t.TempDir()
	document, err := Update(home, "", "demo.plugin", ScopeUser, "sha256:first", func(values map[string]json.RawMessage) error {
		values["enabled"] = json.RawMessage(`true`)
		values["label"] = json.RawMessage(`"demo"`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	document.Values["enabled"][0] = 'f'

	loaded, err := Read(home, "", "demo.plugin", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != "sha256:first" || string(loaded.Values["enabled"]) != "true" || string(loaded.Values["label"]) != `"demo"` {
		t.Fatalf("loaded document = %+v", loaded)
	}
	info, err := os.Stat(filepath.Join(home, settingsDirectory, string(ScopeUser), "demo.plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWorkspaceValuesAreIsolatedByCanonicalWorkspace(t *testing.T) {
	home := t.TempDir()
	workspaceA := filepath.Join(t.TempDir(), "project")
	workspaceB := filepath.Join(t.TempDir(), "project")
	for _, workspace := range []string{workspaceA, workspaceB} {
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Update(home, workspaceA, "demo", ScopeWorkspace, "one", func(values map[string]json.RawMessage) error {
		values["color"] = json.RawMessage(`"orange"`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	a, err := Read(home, workspaceA, "demo", ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(home, workspaceB, "demo", ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Values["color"]) != `"orange"` || len(b.Values) != 0 {
		t.Fatalf("workspace settings leaked: a=%+v b=%+v", a, b)
	}
}

func TestConcurrentUpdatesPreserveIndependentKeys(t *testing.T) {
	home := t.TempDir()
	start := make(chan struct{})
	errorsByWorker := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for key, value := range map[string]string{"first": `1`, "second": `2`} {
		go func() {
			ready.Done()
			<-start
			_, err := Update(home, "", "concurrent", ScopeUser, "same", func(values map[string]json.RawMessage) error {
				values[key] = json.RawMessage(value)
				return nil
			})
			errorsByWorker <- err
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errorsByWorker; err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := Read(home, "", "concurrent", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Values["first"]) != "1" || string(loaded.Values["second"]) != "2" {
		t.Fatalf("concurrent values = %+v", loaded.Values)
	}
}

func TestUpdateRejectsInvalidValuesWithoutReplacingStoredData(t *testing.T) {
	home := t.TempDir()
	if _, err := Update(home, "", "demo", ScopeUser, "old", func(values map[string]json.RawMessage) error {
		values["valid"] = json.RawMessage(`true`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(home, "", "demo", ScopeUser, "new", func(values map[string]json.RawMessage) error {
		values["broken"] = json.RawMessage(`{"unterminated"`)
		return nil
	}); err == nil {
		t.Fatal("invalid JSON update unexpectedly succeeded")
	}
	loaded, err := Read(home, "", "demo", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != "old" || string(loaded.Values["valid"]) != "true" {
		t.Fatalf("failed update replaced stored data: %+v", loaded)
	}
}

func TestRemoveIsIdempotentAndScoped(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	for _, scope := range []Scope{ScopeUser, ScopeWorkspace} {
		if _, err := Update(home, workspace, "demo", scope, "one", func(values map[string]json.RawMessage) error {
			values["value"] = json.RawMessage(`1`)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(home, workspace, "demo", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := Remove(home, workspace, "demo", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	user, err := Read(home, workspace, "demo", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	workspaceDocument, err := Read(home, workspace, "demo", ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(user.Values) != 1 || len(workspaceDocument.Values) != 0 {
		t.Fatalf("remove crossed scopes: user=%+v workspace=%+v", user, workspaceDocument)
	}
}

func TestReadRejectsMismatchedOrUnsupportedDocuments(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, settingsDirectory, string(ScopeUser))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "demo.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"plugin_id":"other","values":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(home, "", "demo", ScopeUser); err == nil {
		t.Fatal("unsupported document unexpectedly loaded")
	}
}

func TestStateStorageIsolatesPluginsScopesAndWorkspaces(t *testing.T) {
	home := t.TempDir()
	workspaceA, workspaceB := t.TempDir(), t.TempDir()
	if _, err := UpdateState(home, workspaceA, "alpha", ScopeWorkspace, func(values map[string]string) error {
		values["panel.mode"] = "focused"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		workspace, plugin string
		scope             Scope
	}{
		{workspaceA, "beta", ScopeWorkspace},
		{workspaceB, "alpha", ScopeWorkspace},
		{workspaceA, "alpha", ScopeUser},
	} {
		document, err := ReadState(home, test.workspace, test.plugin, test.scope)
		if err != nil {
			t.Fatal(err)
		}
		if len(document.Values) != 0 {
			t.Fatalf("state leaked to %+v: %+v", test, document.Values)
		}
	}
}

func TestStateStorageRejectsInvalidKeysAndOversizeValues(t *testing.T) {
	home := t.TempDir()
	for name, update := range map[string]func(map[string]string){
		"key":   func(values map[string]string) { values["../escape"] = "bad" },
		"value": func(values map[string]string) { values["valid"] = string(make([]byte, MaxStateValueBytes+1)) },
	} {
		if _, err := UpdateState(home, "", "alpha", ScopeUser, func(values map[string]string) error { update(values); return nil }); err == nil {
			t.Fatalf("%s limit unexpectedly accepted", name)
		}
	}
}
