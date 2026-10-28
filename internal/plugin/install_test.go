package plugin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallPackageFromDirectory(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, ManifestFilename), `{"id":"directory-demo","name":"Directory Demo","version":"1.2.3","skills":["skills"]}`)
	writeFile(t, filepath.Join(source, "skills", "review", "SKILL.md"), "review this")
	home := t.TempDir()

	inspected, err := InspectPackage(source)
	if err != nil {
		t.Fatalf("InspectPackage: %v", err)
	}
	if inspected.ID != "directory-demo" || inspected.SourceKind != PackageSourceDirectory || inspected.FileCount != 2 {
		t.Fatalf("inspection = %+v", inspected)
	}

	result, err := InstallPackage(home, source)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	wantDestination := filepath.Join(home, "plugins", "directory-demo")
	if result.Destination != wantDestination || result.Replaced || result.Plugin.Root != wantDestination {
		t.Fatalf("result = %+v", result)
	}
	contents, err := os.ReadFile(filepath.Join(wantDestination, "skills", "review", "SKILL.md"))
	if err != nil || string(contents) != "review this" {
		t.Fatalf("installed skill = %q, %v", contents, err)
	}
}

func TestInstallPackageFromZip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	writeZip(t, archive, []zipTestEntry{
		{name: "zip-demo/plugin.json", body: `{"id":"zip-demo","version":"2.0.0"}`, mode: 0o644},
		{name: "zip-demo/assets/data.txt", body: "zip data", mode: 0o644},
	})
	home := t.TempDir()

	inspected, err := InspectPackage(archive)
	if err != nil {
		t.Fatalf("InspectPackage: %v", err)
	}
	if inspected.ID != "zip-demo" || inspected.SourceKind != PackageSourceZip || inspected.ArchiveRoot != "zip-demo" || inspected.ManifestPath != "zip-demo/plugin.json" {
		t.Fatalf("inspection = %+v", inspected)
	}

	result, err := InstallPackage(home, archive)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	if result.Package.ArchiveRoot != "zip-demo" || result.Plugin.Version != "2.0.0" {
		t.Fatalf("result = %+v", result)
	}
	contents, err := os.ReadFile(filepath.Join(result.Destination, "assets", "data.txt"))
	if err != nil || string(contents) != "zip data" {
		t.Fatalf("installed zip data = %q, %v", contents, err)
	}
}

func TestInstallPackageRejectsTraversal(t *testing.T) {
	t.Run("archive entry", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "plugin.zip")
		writeZip(t, archive, []zipTestEntry{
			{name: "plugin.json", body: `{"id":"safe"}`, mode: 0o644},
			{name: "../outside", body: "escape", mode: 0o644},
		})
		_, err := InstallPackage(t.TempDir(), archive)
		if err == nil || !strings.Contains(err.Error(), "traversal") {
			t.Fatalf("InstallPackage error = %v, want traversal rejection", err)
		}
	})

	t.Run("manifest id", func(t *testing.T) {
		source := t.TempDir()
		writeFile(t, filepath.Join(source, ManifestFilename), `{"id":"../outside"}`)
		_, err := InstallPackage(t.TempDir(), source)
		if err == nil || !strings.Contains(err.Error(), "portable path component") {
			t.Fatalf("InstallPackage error = %v, want id rejection", err)
		}
	})
}

func TestInspectPackageRejectsUnsafeArchiveStructure(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "plugin.zip")
		writeZip(t, archive, []zipTestEntry{
			{name: "plugin.json", body: `{"id":"unsafe-link"}`, mode: 0o644},
			{name: "linked", body: "target", mode: os.ModeSymlink | 0o777},
		})
		if _, err := InspectPackage(archive); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("InspectPackage error = %v, want symlink rejection", err)
		}
	})

	t.Run("conflicting roots", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "plugin.zip")
		writeZip(t, archive, []zipTestEntry{
			{name: "one/plugin.json", body: `{"id":"one"}`, mode: 0o644},
			{name: "two/plugin.json", body: `{"id":"two"}`, mode: 0o644},
		})
		if _, err := InspectPackage(archive); err == nil || !strings.Contains(err.Error(), "conflicting package roots") {
			t.Fatalf("InspectPackage error = %v, want root conflict rejection", err)
		}
	})

	t.Run("missing manifest", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "plugin.zip")
		writeZip(t, archive, []zipTestEntry{{name: "README.md", body: "no manifest", mode: 0o644}})
		if _, err := InspectPackage(archive); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("InspectPackage error = %v, want missing manifest rejection", err)
		}
	})
}

func TestInstallPackageReplacementRollback(t *testing.T) {
	home := t.TempDir()
	oldSource := t.TempDir()
	writeFile(t, filepath.Join(oldSource, ManifestFilename), `{"id":"rollback-demo","version":"1"}`)
	writeFile(t, filepath.Join(oldSource, "generation.txt"), "old")
	first, err := InstallPackage(home, oldSource)
	if err != nil {
		t.Fatalf("initial InstallPackage: %v", err)
	}

	invalidSource := t.TempDir()
	writeFile(t, filepath.Join(invalidSource, ManifestFilename), `{"id":"rollback-demo","version":"2","skills":["../escape"]}`)
	if _, err := InstallPackage(home, invalidSource); err == nil {
		t.Fatal("replacement unexpectedly succeeded")
	}
	contents, err := os.ReadFile(filepath.Join(first.Destination, "generation.txt"))
	if err != nil || string(contents) != "old" {
		t.Fatalf("old generation after failed replacement = %q, %v", contents, err)
	}
	installed, err := LoadManifest(filepath.Join(first.Destination, ManifestFilename), "user")
	if err != nil || installed.Version != "1" {
		t.Fatalf("installed generation = %+v, %v", installed, err)
	}
}

func TestInstallPackagePreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not represent Unix executable mode bits")
	}
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	writeZip(t, archive, []zipTestEntry{
		{name: "plugin.json", body: `{"id":"executable-demo"}`, mode: 0o644},
		{name: "bin/plugin", body: "#!/bin/sh\n", mode: 0o755},
	})
	result, err := InstallPackage(t.TempDir(), archive)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	info, err := os.Stat(filepath.Join(result.Destination, "bin", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0o111 {
		t.Fatalf("installed executable mode = %o", info.Mode().Perm())
	}
}

func TestUninstallPackage(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeFile(t, filepath.Join(source, ManifestFilename), `{"id":"remove-demo"}`)
	installed, err := InstallPackage(home, source)
	if err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}

	result, err := UninstallPackage(home, "remove-demo")
	if err != nil {
		t.Fatalf("UninstallPackage: %v", err)
	}
	if !result.Removed || result.Destination != installed.Destination {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Lstat(installed.Destination); !os.IsNotExist(err) {
		t.Fatalf("installed package still exists: %v", err)
	}

	again, err := UninstallPackage(home, "remove-demo")
	if err != nil || again.Removed {
		t.Fatalf("second uninstall = %+v, %v", again, err)
	}
	if _, err := UninstallPackage(home, "../outside"); err == nil {
		t.Fatal("unsafe uninstall id unexpectedly accepted")
	}
}

type zipTestEntry struct {
	name string
	body string
	mode os.FileMode
}

func writeZip(t *testing.T, destination string, entries []zipTestEntry) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPackageSourceIdentityFollowsDistributionLocation(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first.zip"), filepath.Join(root, "second.zip")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("first generation"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "source.zip")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	original, err := PackageSourceIdentity(link)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := PackageSourceIdentity(first)
	if err != nil || original != direct {
		t.Fatalf("alias identity = %q, direct %q: %v", original, direct, err)
	}
	if err := os.WriteFile(first, []byte("next generation"), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := PackageSourceIdentity(link)
	if err != nil || updated != original {
		t.Fatalf("same zip location changed source: %q %v", updated, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	retargeted, err := PackageSourceIdentity(link)
	if err != nil || retargeted == original {
		t.Fatalf("retargeted alias retained old source: %q %v", retargeted, err)
	}
}
