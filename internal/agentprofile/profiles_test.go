package agentprofile

import "testing"

func TestEnsureAndListProfiles(t *testing.T) {
	wuuHome := t.TempDir()
	profile, created, err := Ensure(EnsureOptions{
		WuuHome:     wuuHome,
		Name:        "qa_laowang",
		Role:        "QA reviewer",
		Description: "Remembers recurring release QA checks.",
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !created {
		t.Fatal("expected profile to be created")
	}
	if profile.Name != "qa_laowang" || profile.Role != "QA reviewer" {
		t.Fatalf("unexpected profile summary: %+v", profile)
	}

	profile, created, err = Ensure(EnsureOptions{
		WuuHome:     wuuHome,
		Name:        "qa_laowang",
		Description: "Updated description",
	})
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if created {
		t.Fatal("existing profile should not be marked created")
	}
	if profile.Description != "Updated description" || profile.Role != "QA reviewer" {
		t.Fatalf("existing profile metadata should update without clearing role: %+v", profile)
	}

	profiles, err := List(wuuHome)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "qa_laowang" {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestEnsureProfileRejectsDefaultProfile(t *testing.T) {
	if _, _, err := Ensure(EnsureOptions{WuuHome: t.TempDir(), Name: "default"}); err == nil {
		t.Fatal("expected default profile to be rejected")
	}
}
