package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserTabStoreCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "browser_tabs.json")
	store := NewBrowserTabStore(path)

	if _, ok, err := store.Get("tab-1"); err != nil || ok {
		t.Fatalf("empty get = ok:%v err:%v", ok, err)
	}
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty list = %v err:%v", list, err)
	}

	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://a.example", Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(BrowserTabRecord{TabID: "tab-2", URL: "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := store.Get("tab-1")
	if err != nil || !ok || rec.URL != "https://a.example" || rec.Title != "A" {
		t.Fatalf("get tab-1 = %+v ok:%v err:%v", rec, ok, err)
	}
	if rec.UpdatedAt.IsZero() {
		t.Fatal("Put did not stamp UpdatedAt")
	}

	// Upsert replaces in place.
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://a2.example", Status: "deliverable"}); err != nil {
		t.Fatal(err)
	}
	rec, _, _ = store.Get("tab-1")
	if rec.URL != "https://a2.example" || rec.Status != "deliverable" {
		t.Fatalf("upsert tab-1 = %+v", rec)
	}
	list, _ := store.List()
	if len(list) != 2 || list[0].TabID != "tab-1" || list[1].TabID != "tab-2" {
		t.Fatalf("list = %+v", list)
	}

	if err := store.Delete("tab-2"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("tab-2"); ok {
		t.Fatal("tab-2 still present after delete")
	}
	// Deleting a missing tab is a no-op.
	if err := store.Delete("tab-missing"); err != nil {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestBrowserTabStorePersistsAtomicValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser_tabs.json")
	store := NewBrowserTabStore(path)
	store.SetClock(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://a.example"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file browserTabsFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("on-disk file is not valid JSON: %v", err)
	}
	if file.SchemaVersion != browserTabsSchemaVersion || len(file.Tabs) != 1 || file.Tabs[0].TabID != "tab-1" {
		t.Fatalf("persisted file = %+v", file)
	}
	// No temp files leak beside the target.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Fatalf("unexpected leftover file %q", e.Name())
		}
	}

	// A fresh store reads the same records back (survives a process boundary).
	reopened := NewBrowserTabStore(path)
	rec, ok, _ := reopened.Get("tab-1")
	if !ok || rec.URL != "https://a.example" {
		t.Fatalf("reopened get = %+v ok:%v", rec, ok)
	}
}

func TestBrowserTabStorePersistsDead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser_tabs.json")
	store := NewBrowserTabStore(path)

	if err := store.Put(BrowserTabRecord{TabID: "tab-1", URL: "https://a.example", Dead: true}); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := store.Get("tab-1"); !rec.Dead {
		t.Fatalf("dead flag lost: %+v", rec)
	}
}
