package userspace

import (
	"path/filepath"
	"testing"
)

// Protect the process identity boundary before wiring it into session creation.
func TestBindingRejectsTraversalAndPartialIdentity(t *testing.T) {
	for _, input := range []Binding{{UserSpaceID: "../other", ProductDomain: "personal"}, {UserSpaceID: "local"}, {ProductDomain: "personal"}, {UserSpaceID: "local", ProductDomain: "other"}} {
		if err := input.Validate(); err == nil {
			t.Fatalf("accepted invalid binding: %+v", input)
		}
	}
	if err := (Binding{}).Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestBindingStablePathsAndLegacyDefault(t *testing.T) {
	home := t.TempDir()
	b := Binding{UserSpaceID: "local", ProductDomain: "personal"}
	if got := b.Root(home); got != filepath.Join(home, "user-spaces", "local", "personal", "files") {
		t.Fatal(got)
	}
	if (Binding{}).Domain() != "coding" {
		t.Fatal("legacy must remain coding")
	}
	if b.Matches(Binding{UserSpaceID: "other", ProductDomain: "personal"}) || b.Matches(Binding{}) {
		t.Fatal("cross-space/domain match")
	}
}
