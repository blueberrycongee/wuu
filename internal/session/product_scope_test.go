package session

import "testing"

// Scope metadata must survive every read/write path without migrating old scratch sessions.
func TestSessionProductScopePersistence(t *testing.T) {
	dir := t.TempDir()
	personal, err := CreateInitialized(dir, Session{ID: "personal-main", UserSpaceID: "local", ProductDomain: "personal", AgentID: "mosaic"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateTitle(dir, personal.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Find(dir, personal.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.UserSpaceID != "local" || got.ProductDomain != "personal" || got.AgentID != "mosaic" {
		t.Fatalf("binding lost: %+v", got)
	}
	legacy, err := Create(dir, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	all, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || legacy.ProductDomain != "" || legacy.UserSpaceID != "" {
		t.Fatal("legacy changed")
	}
}
