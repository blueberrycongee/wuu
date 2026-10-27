package session

import (
	"context"
	"testing"
)

// Scope metadata must survive every read/write path without migrating old scratch sessions.
func TestSessionProductScopePersistence(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		store := NewStore(context.Background(), native.documents)
		personal, err := store.CreateInitialized(Session{ID: "personal-main", UserSpaceID: "local", ProductDomain: "personal", AgentID: "mosaic"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateTitle(personal.ID, "Renamed"); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.Find(personal.ID)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if got.UserSpaceID != "local" || got.ProductDomain != "personal" || got.AgentID != "mosaic" {
			t.Fatalf("binding lost: %+v", got)
		}
		legacy, err := store.Create("legacy")
		if err != nil {
			t.Fatal(err)
		}
		all, err := store.List(0)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 || legacy.ProductDomain != "" || legacy.UserSpaceID != "" {
			t.Fatal("legacy changed")
		}
	})
}
