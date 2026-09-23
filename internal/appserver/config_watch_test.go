package appserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestConfigWatchUsesEventsInsteadOfContinuousReloads(t *testing.T) {
	root := t.TempDir()
	wuuHome := t.TempDir()
	refreshes := make(chan struct{}, 8)
	srv := &Server{
		rt: &runtime.Session{
			RootDir:    root,
			WuuHome:    wuuHome,
			ConfigPath: filepath.Join(root, ".wuu.json"),
		},
		out:     &lockedBuffer{},
		threads: map[string]*threadState{},
		refreshConfigForTest: func() error {
			refreshes <- struct{}{}
			return nil
		},
	}
	srv.startConfigWatch()
	t.Cleanup(func() {
		srv.closed.Store(true)
		srv.backgroundWG.Wait()
	})

	select {
	case <-refreshes:
	case <-time.After(3 * time.Second):
		t.Fatal("config watcher did not establish its initial baseline")
	}

	select {
	case <-refreshes:
		t.Fatal("config watcher reloaded an unchanged config")
	case <-time.After(700 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "runtime-state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshes:
		t.Fatal("config watcher reloaded after an unrelated runtime-state change")
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(root, ".wuu.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshes:
	case <-time.After(3 * time.Second):
		t.Fatal("config watcher did not refresh after a filesystem event")
	}
}

func TestConfigRefreshPreservesInventoryUntilInvalidDocumentIsRepaired(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	valid := `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test-key","model":"fake-model"}}}`
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(rt.ConfigPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	before := srv.providerSummaries()
	if len(before) != 1 {
		t.Fatalf("expected configured provider: %+v", before)
	}
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}

	// A newer build can add a field unknown to this reader.
	write(strings.Replace(valid, `"default_provider"`, `"future_setting":{},"default_provider"`, 1))
	if err := srv.refreshConfigIfChanged(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict config error, got %v", err)
	}
	messages := parseOutput(t, out.String())
	if len(notificationsByMethod(messages, NotificationConfigError)) != 1 {
		t.Fatalf("expected one visible configuration error: %s", out.String())
	}
	if got := srv.providerSummaries(); !reflect.DeepEqual(got, before) {
		t.Fatalf("invalid refresh erased inventory: %+v", got)
	}
	for i := 0; i < 3; i++ {
		if err := srv.refreshConfigIfChanged(); err != nil {
			t.Fatalf("unchanged rejected document was retried: %v", err)
		}
	}
	if len(notificationsByMethod(parseOutput(t, out.String()), NotificationConfigError)) != 1 {
		t.Fatal("unchanged rejected document emitted repeated errors")
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"blocked","method":"config/model/update","params":{"model":"other-model"}}`)); err != nil {
		t.Fatal(err)
	}
	rejection := remarshal[ResponseError](t, responseByID(t, parseOutput(t, out.String()), "blocked")["error"])
	if !strings.Contains(rejection.Message, "unknown field") {
		t.Fatalf("mutation must reject the invalid config, got %+v", rejection)
	}

	// Restoring the exact previous contents must clear the error too.
	write(valid)
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatalf("repair did not recover: %v", err)
	}
	if len(notificationsByMethod(parseOutput(t, out.String()), NotificationConfigChanged)) != 1 {
		t.Fatal("repair did not publish the recovered configuration")
	}
	write(strings.Replace(valid, "fake-model", "repaired-model", 1))
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	if got := srv.providerSummaries(); len(got) != 1 || got[0].Model != "repaired-model" {
		t.Fatalf("inventory did not recover: %+v", got)
	}
}
