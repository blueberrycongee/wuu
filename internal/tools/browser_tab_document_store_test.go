package tools

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"path/filepath"
	"sync"
	"testing"
)

func TestBrowserTabDocumentsMigrationIsolationAndFailure(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	legacyPath := filepath.Join(t.TempDir(), "tabs.json")
	legacy := NewBrowserTabStore(legacyPath)
	if err := legacy.Put(BrowserTabRecord{TabID: "tab", URL: "https://example.org"}); err != nil {
		t.Fatal(err)
	}
	store := NewBrowserTabDocumentStore(docs, "thread", filepath.Join(t.TempDir(), "missing"), legacyPath)
	records, err := store.List()
	if err != nil || len(records) != 1 {
		t.Fatalf("migration: %+v %v", records, err)
	}
	if err := store.Delete("tab"); err != nil {
		t.Fatal(err)
	}
	records, err = NewBrowserTabDocumentStore(docs, "thread", legacyPath).List()
	if err != nil || len(records) != 0 {
		t.Fatalf("stale import: %+v %v", records, err)
	}
	if err := store.Put(BrowserTabRecord{TabID: "new"}); err != nil {
		t.Fatal(err)
	}
	records, err = NewBrowserTabDocumentStore(docs, "other").List()
	if err != nil || len(records) != 0 {
		t.Fatalf("scope leak: %+v %v", records, err)
	}
	if err := docs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("unavailable selected provider fell through")
	}
}

func TestBrowserSelectedStateFailureFencesHostEffects(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	store := NewBrowserTabDocumentStore(docs, "thread")
	bridge := &fakeBrowserBridge{}
	tool := NewBrowserTool(&Env{BrowserTabs: store, BrowserBridge: bridge})
	if err := docs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteResult(context.Background(), `{"action":"navigate","tab_id":"tab","url":"https://example.org"}`); err == nil {
		t.Fatal("unavailable provider accepted browser action")
	}
	if len(bridge.openCalls)+len(bridge.cdpCalls) != 0 {
		t.Fatal("browser side effects before state availability")
	}
}

func TestBrowserStateFailureAfterHostActionIsNotSuccess(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store := NewBrowserTabDocumentStore(docs, "thread")
	bridge := &fakeBrowserBridge{onOpen: func(string, string) error { return docs.Close() }}
	tool := NewBrowserTool(&Env{BrowserTabs: store, BrowserBridge: bridge})
	ctx := withBrowserActionContext(context.Background(), browserActionContext{tabID: "tab", newTab: true})
	_, err = tool.ExecuteResult(ctx, `{"action":"navigate","url":"https://example.org"}`)
	if err == nil {
		t.Fatal("reported success despite failed durable registry write")
	}
	if len(bridge.openCalls) != 1 {
		t.Fatalf("replayed host side effect: %d", len(bridge.openCalls))
	}
}

func TestBrowserDocumentConcurrentClientsPreserveTabs(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	const clients = 8
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < clients; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			<-start
			if err := NewBrowserTabDocumentStore(docs, "thread").Put(BrowserTabRecord{TabID: fmt.Sprintf("tab-%d", id)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(start)
	group.Wait()
	tabs, err := NewBrowserTabDocumentStore(docs, "thread").List()
	if err != nil || len(tabs) != clients {
		t.Fatalf("lost concurrent tab updates: %d %v", len(tabs), err)
	}
}
