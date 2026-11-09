package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStageAndRejectPackageUpdateKeepInstalledGeneration(t *testing.T) {
	home := t.TempDir()
	installedSource := writeUpdatePackage(t, "update-demo", "1.0.0", "installed")
	installed, err := InstallPackage(home, installedSource)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "update.zip")
	writeZip(t, archive, []zipTestEntry{
		{name: "update-demo/plugin.json", body: `{"id":"update-demo","version":"2.0.0","skills":["skills"]}`, mode: 0o644},
		{name: "update-demo/skills/demo/SKILL.md", body: "pending", mode: 0o644},
	})
	pending, err := StagePackageUpdate(home, archive)
	if err != nil {
		t.Fatalf("StagePackageUpdate: %v", err)
	}
	if pending.Package.ID != "update-demo" || pending.Package.SourceKind != PackageSourceZip || pending.Package.ArchiveRoot != "update-demo" {
		t.Fatalf("pending = %+v", pending)
	}
	if pending.Package.ManifestPath != "plugin.json" {
		t.Fatalf("pending manifest path = %q, want package-relative plugin.json", pending.Package.ManifestPath)
	}
	if _, err := os.Stat(filepath.Join(pending.Package.SourcePath, filepath.FromSlash(pending.Package.ManifestPath))); err != nil {
		t.Fatalf("pending manifest is not addressable from package root: %v", err)
	}
	if pending.ActiveFingerprint != installed.Plugin.Fingerprint || pending.Package.Fingerprint == installed.Plugin.Fingerprint {
		t.Fatalf("pending fingerprints = active %q candidate %q, installed %q", pending.ActiveFingerprint, pending.Package.Fingerprint, installed.Plugin.Fingerprint)
	}
	assertInstalledUpdateContent(t, home, "update-demo", "installed")

	discovered := Discover("", home)
	installedGeneration := -1
	for index := range discovered {
		if discovered[index].ID == "update-demo" {
			installedGeneration = index
			break
		}
	}
	if installedGeneration < 0 || discovered[installedGeneration].Version != "1.0.0" {
		t.Fatalf("discovered plugins while update is pending = %+v", discovered)
	}
	listed, err := ListPendingUpdates(home)
	if err != nil {
		t.Fatalf("ListPendingUpdates: %v", err)
	}
	if len(listed) != 1 || listed[0].Package.Fingerprint != pending.Package.Fingerprint {
		t.Fatalf("listed pending updates = %+v", listed)
	}
	read, err := InspectPendingUpdate(home, "update-demo")
	if err != nil {
		t.Fatalf("InspectPendingUpdate: %v", err)
	}
	if read.Path != pending.Path || read.Package.Fingerprint != pending.Package.Fingerprint {
		t.Fatalf("read pending update = %+v, staged = %+v", read, pending)
	}

	if err := RejectPendingUpdate(home, "update-demo", installed.Plugin.Fingerprint); !errors.Is(err, ErrPendingUpdateFingerprintMismatch) {
		t.Fatalf("stale RejectPendingUpdate error = %v", err)
	}
	if _, err := ReadPendingUpdate(home, "update-demo"); err != nil {
		t.Fatalf("pending update changed after stale rejection: %v", err)
	}
	assertInstalledUpdateContent(t, home, "update-demo", "installed")

	if err := RejectPendingUpdate(home, "update-demo", pending.Package.Fingerprint); err != nil {
		t.Fatalf("RejectPendingUpdate: %v", err)
	}
	if _, err := ReadPendingUpdate(home, "update-demo"); !errors.Is(err, ErrPendingUpdateNotFound) {
		t.Fatalf("ReadPendingUpdate after rejection error = %v", err)
	}
	assertInstalledUpdateContent(t, home, "update-demo", "installed")
}

func TestPromotePendingUpdateRequiresExactFingerprint(t *testing.T) {
	home := t.TempDir()
	installedSource := writeUpdatePackage(t, "promote-demo", "1.0.0", "old generation")
	installed, err := InstallPackage(home, installedSource)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	pendingSource := writeUpdatePackage(t, "promote-demo", "2.0.0", "new generation")
	pending, err := StagePackageUpdate(home, pendingSource)
	if err != nil {
		t.Fatalf("StagePackageUpdate: %v", err)
	}

	if _, err := PromotePendingUpdate(home, "promote-demo", installed.Plugin.Fingerprint); !errors.Is(err, ErrPendingUpdateFingerprintMismatch) {
		t.Fatalf("stale PromotePendingUpdate error = %v", err)
	}
	assertInstalledUpdateContent(t, home, "promote-demo", "old generation")
	stillPending, err := ReadPendingUpdate(home, "promote-demo")
	if err != nil || stillPending.Package.Fingerprint != pending.Package.Fingerprint {
		t.Fatalf("pending update after stale promotion = %+v, %v", stillPending, err)
	}

	promoted, err := PromotePendingUpdate(home, "promote-demo", pending.Package.Fingerprint)
	if err != nil {
		t.Fatalf("PromotePendingUpdate: %v", err)
	}
	if !promoted.Replaced || promoted.Plugin.Fingerprint != pending.Package.Fingerprint || promoted.Package.Fingerprint != pending.Package.Fingerprint {
		t.Fatalf("promoted result = %+v, pending = %+v", promoted, pending)
	}
	assertInstalledUpdateContent(t, home, "promote-demo", "new generation")
	if _, err := ReadPendingUpdate(home, "promote-demo"); !errors.Is(err, ErrPendingUpdateNotFound) {
		t.Fatalf("ReadPendingUpdate after promotion error = %v", err)
	}
}

func TestPackageUpdateFingerprintIncludesTransitiveFiles(t *testing.T) {
	home := t.TempDir()
	installedSource := writeUpdatePackage(t, "fingerprint-demo", "1.0.0", "first chunk")
	installed, err := InstallPackage(home, installedSource)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}

	changedSource := writeUpdatePackage(t, "fingerprint-demo", "1.0.0", "changed transitive chunk")
	pending, err := StagePackageUpdate(home, changedSource)
	if err != nil {
		t.Fatalf("StagePackageUpdate: %v", err)
	}
	if pending.Package.Fingerprint == installed.Plugin.Fingerprint {
		t.Fatal("transitive file change did not change pending fingerprint")
	}

	identicalSource := writeUpdatePackage(t, "identical-demo", "1.0.0", "same chunk")
	if _, err := InstallPackage(home, identicalSource); err != nil {
		t.Fatalf("InstallPackage identical-demo: %v", err)
	}
	identicalCandidate := writeUpdatePackage(t, "identical-demo", "1.0.0", "same chunk")
	if _, err := StagePackageUpdate(home, identicalCandidate); err == nil {
		t.Fatal("StagePackageUpdate accepted the installed fingerprint")
	}
}

func TestStagePackageUpdateReplacesPreviousPendingGeneration(t *testing.T) {
	home := t.TempDir()
	installedSource := writeUpdatePackage(t, "replace-demo", "1.0.0", "installed")
	if _, err := InstallPackage(home, installedSource); err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}

	firstSource := writeUpdatePackage(t, "replace-demo", "2.0.0", "first pending")
	first, err := StagePackageUpdate(home, firstSource)
	if err != nil {
		t.Fatalf("first StagePackageUpdate: %v", err)
	}
	secondSource := writeUpdatePackage(t, "replace-demo", "3.0.0", "second pending")
	second, err := StagePackageUpdate(home, secondSource)
	if err != nil {
		t.Fatalf("second StagePackageUpdate: %v", err)
	}
	if first.Path != second.Path || first.Package.Fingerprint == second.Package.Fingerprint {
		t.Fatalf("pending replacements = first %+v, second %+v", first, second)
	}

	listed, err := ListPendingUpdates(home)
	if err != nil {
		t.Fatalf("ListPendingUpdates: %v", err)
	}
	if len(listed) != 1 || listed[0].Package.Version != "3.0.0" || listed[0].Package.Fingerprint != second.Package.Fingerprint {
		t.Fatalf("listed pending replacements = %+v", listed)
	}
	contents, err := os.ReadFile(filepath.Join(second.Path, "package", "skills", "demo", "SKILL.md"))
	if err != nil || string(contents) != "second pending" {
		t.Fatalf("replacement pending content = %q, %v", contents, err)
	}

	if _, err := PromotePendingUpdate(home, "replace-demo", first.Package.Fingerprint); !errors.Is(err, ErrPendingUpdateFingerprintMismatch) {
		t.Fatalf("superseded PromotePendingUpdate error = %v", err)
	}
	current, err := ReadPendingUpdate(home, "replace-demo")
	if err != nil || current.Package.Fingerprint != second.Package.Fingerprint {
		t.Fatalf("current pending update after superseded approval = %+v, %v", current, err)
	}
	assertInstalledUpdateContent(t, home, "replace-demo", "installed")
}

func writeUpdatePackage(t *testing.T, id, version, content string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ManifestFilename), `{"id":"`+id+`","version":"`+version+`","skills":["skills"]}`)
	writeFile(t, filepath.Join(root, "skills", "demo", "SKILL.md"), content)
	return root
}

func assertInstalledUpdateContent(t *testing.T, home, id, want string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(home, "plugins", id, "skills", "demo", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed plugin %s: %v", id, err)
	}
	if string(contents) != want {
		t.Fatalf("installed plugin %s content = %q, want %q", id, contents, want)
	}
}
