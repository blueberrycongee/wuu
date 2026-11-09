package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// These behavior checks protect the distribution trust boundary: immutable
// identity, integrity, safe extraction, script-free loading and failed updates.
func TestDistributionInstallAndLockedReplay(t *testing.T) {
	archive := distributionArchive(t, map[string]string{
		"package/plugin.json":            `{"id":"registry-demo","version":"1.2.3"}`,
		"package/package.json":           `{"name":"@fixture/plugin","version":"1.2.3","scripts":{"postinstall":"touch SHOULD_NOT_EXIST"}}`,
		"package/skills/review/SKILL.md": "review carefully",
	}, nil)
	registry, requests := distributionRegistry(t, archive, "", nil)
	resolved, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL}, registry.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	if resolved.Identity != "npm:"+registry.URL+"/@fixture/plugin" {
		t.Fatalf("identity = %q", resolved.Identity)
	}
	installed, err := InstallPackage(t.TempDir(), resolved.Path)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Package.Distribution == nil || installed.Package.Distribution.Name != "@fixture/plugin" || installed.Package.Distribution.Integrity == "" {
		t.Fatalf("installed package lost pinned provenance: %+v", installed.Package.Distribution)
	}
	lockPath := filepath.Join(installed.Destination, DistributionLockFilename)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed.Destination, "SHOULD_NOT_EXIST")); !os.IsNotExist(err) {
		t.Fatalf("install script executed: %v", err)
	}
	before := requests.Load()
	replayed, err := resolvePackageSource(context.Background(), "lock:"+lockPath, DistributionOptions{}, registry.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer replayed.Close()
	if requests.Load() != before+1 {
		t.Fatalf("lock replay fetched mutable metadata: requests=%d before=%d", requests.Load(), before)
	}
	if replayed.Inspection.Fingerprint != resolved.Inspection.Fingerprint || replayed.Identity != resolved.Identity {
		t.Fatal("locked replay changed the package or source identity")
	}
}

func TestDistributionRejectsUnsafePackagesBeforePublication(t *testing.T) {
	cases := []struct {
		name, want, metadataIntegrity string
		files                         map[string]string
		headers                       []*tar.Header
		change                        func(map[string]any)
	}{
		{name: "tampered artifact", want: "integrity", metadataIntegrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))},
		{name: "traversal", want: "path", files: map[string]string{"package/../escape": "bad"}},
		{name: "symlink", want: "entry", headers: []*tar.Header{{Name: "package/linked", Typeflag: tar.TypeSymlink, Linkname: "/tmp/escape"}}},
		{name: "hardlink", want: "entry", headers: []*tar.Header{{Name: "package/linked", Typeflag: tar.TypeLink, Linkname: "package/plugin.json"}}},
		{name: "case collision", want: "conflicting", files: map[string]string{"package/README": "one", "package/readme": "two"}},
		{name: "outside package", want: "package/", files: map[string]string{"other/README": "outside"}},
		{name: "forged lock", want: "reserved", files: map[string]string{"package/" + DistributionLockFilename: "{}"}},
		{name: "case-folded forged lock", want: "reserved", files: map[string]string{"package/" + strings.ToUpper(DistributionLockFilename): "{}"}},
		{name: "unbundled dependencies", want: "self-contained", files: map[string]string{"package/package.json": `{"name":"@fixture/plugin","version":"1.2.3","dependencies":{"dependency":"1.0.0"}}`}},
		{name: "package identity mismatch", want: "identity", files: map[string]string{"package/package.json": `{"name":"imposter","version":"1.2.3"}`}},
		{name: "plugin version mismatch", want: "version", files: map[string]string{"package/plugin.json": `{"id":"registry-demo","version":"9.9.9"}`}},
		{name: "metadata identity mismatch", want: "identity", change: func(v map[string]any) { v["name"] = "imposter" }},
		{name: "missing integrity", want: "integrity", change: func(v map[string]any) { v["dist"].(map[string]any)["integrity"] = "" }},
		{name: "cross origin", want: "origin", change: func(v map[string]any) { v["dist"].(map[string]any)["tarball"] = "https://other.invalid/package.tgz" }},
		{name: "insecure tarball", want: "HTTPS", change: func(v map[string]any) { v["dist"].(map[string]any)["tarball"] = "http://example.invalid/package.tgz" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"package/plugin.json": `{"id":"registry-demo","version":"1.2.3"}`, "package/package.json": `{"name":"@fixture/plugin","version":"1.2.3"}`}
			for name, content := range tc.files {
				files[name] = content
			}
			registry, _ := distributionRegistry(t, distributionArchive(t, files, tc.headers), tc.metadataIntegrity, tc.change)
			resolved, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL}, registry.Client())
			if resolved != nil {
				resolved.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestDistributionUpdateRetainsPriorGenerationAndSource(t *testing.T) {
	home := t.TempDir()
	old := t.TempDir()
	writeFile(t, filepath.Join(old, ManifestFilename), `{"id":"registry-demo","version":"1.0.0"}`)
	previous, err := InstallPackage(home, old)
	if err != nil {
		t.Fatal(err)
	}
	archive := distributionArchive(t, map[string]string{"package/plugin.json": `{"id":"registry-demo","version":"1.2.3"}`, "package/package.json": `{"name":"@fixture/plugin","version":"1.2.3"}`}, nil)
	registry, _ := distributionRegistry(t, archive, "", nil)
	resolved, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL}, registry.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	pending, err := StagePackageUpdateFromSource(home, resolved.Path, resolved.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if pending.SourceIdentity != resolved.Identity {
		t.Fatalf("pending identity=%q", pending.SourceIdentity)
	}
	active, _, err := installedUserPlugin(home, "registry-demo")
	if err != nil || active.Fingerprint != previous.Plugin.Fingerprint {
		t.Fatalf("staging replaced active generation: %v", err)
	}
	writeFile(t, filepath.Join(pending.Package.SourcePath, "tampered"), "changed")
	if _, err := PromotePendingUpdate(home, "registry-demo", pending.Package.Fingerprint); err == nil {
		t.Fatal("tampered pending update promoted")
	}
	active, _, err = installedUserPlugin(home, "registry-demo")
	if err != nil || active.Fingerprint != previous.Plugin.Fingerprint {
		t.Fatalf("failed promotion lost active generation: %v", err)
	}
}

func TestDistributionRequiresPinnedSafeSources(t *testing.T) {
	for _, source := range []string{"npm:package", "npm:package@latest", "npm:package@^1.0.0", "npm:package@v1.0.0", "npm:../package@1.0.0", "npm:@scope/../package@1.0.0"} {
		if result, err := ResolvePackageSource(context.Background(), source, DistributionOptions{}); err == nil {
			result.Close()
			t.Fatalf("accepted %q", source)
		}
	}
	for _, registry := range []string{"http://registry.example", "https://user:secret@registry.example", "https://registry.example/?token=secret", "https://registry.example/#fragment"} {
		if result, err := ResolvePackageSource(context.Background(), "npm:package@1.0.0", DistributionOptions{Registry: registry}); err == nil {
			result.Close()
			t.Fatalf("accepted registry %q", registry)
		}
	}
}

func TestDistributionDownloadBoundaries(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://other.invalid/secret", http.StatusFound)
		}))
		defer server.Close()
		_, err := resolvePackageSource(context.Background(), "npm:package@1.0.0", DistributionOptions{Registry: server.URL}, server.Client())
		if err == nil || !strings.Contains(err.Error(), "origin") {
			t.Fatalf("redirect error=%v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := ResolvePackageSource(ctx, "npm:package@1.0.0", DistributionOptions{})
		if err == nil {
			t.Fatal("canceled resolution succeeded")
		}
	})
	t.Run("oversized metadata", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(bytes.Repeat([]byte(" "), maxDistributionMetadataBytes+1))
		}))
		defer server.Close()
		_, err := resolvePackageSource(context.Background(), "npm:package@1.0.0", DistributionOptions{Registry: server.URL}, server.Client())
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized metadata error=%v", err)
		}
	})
	t.Run("expanded size", func(t *testing.T) {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "package/huge", Mode: 0o644, Size: maxPackageBytes + 1, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_ = tw.Close()
		_ = gz.Close()
		registry, _ := distributionRegistry(t, buffer.Bytes(), "", nil)
		_, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL}, registry.Client())
		if err == nil || !strings.Contains(err.Error(), "expands") {
			t.Fatalf("expansion error=%v", err)
		}
	})
	t.Run("invalid gzip trailer", func(t *testing.T) {
		archive := distributionArchive(t, map[string]string{"package/plugin.json": `{"id":"registry-demo","version":"1.2.3"}`, "package/package.json": `{"name":"@fixture/plugin","version":"1.2.3"}`}, nil)
		archive[len(archive)-8] ^= 0xff
		registry, _ := distributionRegistry(t, archive, "", nil)
		_, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL}, registry.Client())
		if err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("invalid gzip trailer error=%v", err)
		}
	})
	t.Run("caller integrity pin", func(t *testing.T) {
		archive := distributionArchive(t, map[string]string{"package/plugin.json": `{"id":"registry-demo","version":"1.2.3"}`, "package/package.json": `{"name":"@fixture/plugin","version":"1.2.3"}`}, nil)
		registry, _ := distributionRegistry(t, archive, "", nil)
		_, err := resolvePackageSource(context.Background(), "npm:@fixture/plugin@1.2.3", DistributionOptions{Registry: registry.URL, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}, registry.Client())
		if err == nil || !strings.Contains(err.Error(), "integrity") {
			t.Fatalf("pin error=%v", err)
		}
	})
	t.Run("malformed replay lock", func(t *testing.T) {
		lock := filepath.Join(t.TempDir(), "lock.json")
		writeFile(t, lock, `{"lock_version":2}`)
		_, err := ResolvePackageSource(context.Background(), "lock:"+lock, DistributionOptions{})
		if err == nil || !strings.Contains(err.Error(), "lock version") {
			t.Fatalf("lock error=%v", err)
		}
	})
}

func distributionArchive(t *testing.T, files map[string]string, headers []*tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, header := range headers {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func distributionRegistry(t *testing.T, archive []byte, integrity string, change func(map[string]any)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	var server *httptest.Server
	if integrity == "" {
		digest := sha512.Sum512(archive)
		integrity = "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
	}
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials sent to registry")
		}
		if r.URL.Path == "/package.tgz" {
			_, _ = w.Write(archive)
			return
		}
		if r.URL.Path != "/@fixture/plugin/1.2.3" {
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), 404)
			return
		}
		metadata := map[string]any{"name": "@fixture/plugin", "version": "1.2.3", "dist": map[string]any{"tarball": server.URL + "/package.tgz", "integrity": integrity}}
		if change != nil {
			change(metadata)
		}
		_ = json.NewEncoder(w).Encode(metadata)
	}))
	t.Cleanup(server.Close)
	return server, &count
}
