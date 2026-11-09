package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

// Exercise the public CLI through download, policy, pending-generation
// publication and disk inspection, using only a deterministic local registry.
func TestPluginCLINPMPinnedLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	configuration, err := json.Marshal(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := make(map[string][]byte)
	for _, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tarball := tar.NewWriter(gz)
		for name, content := range map[string]string{
			"plugin.json":  fmt.Sprintf(`{"id":"npm-cli","version":%q}`, version),
			"package.json": fmt.Sprintf(`{"name":"fixture-plugin","version":%q,"scripts":{"postinstall":"exit 1"}}`, version),
		} {
			if err := tarball.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0o644, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarball.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tarball.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		artifacts[version] = buffer.Bytes()
	}
	var corrupt atomic.Bool
	var registry *httptest.Server
	registry = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := strings.TrimPrefix(r.URL.Path, "/fixture-plugin/")
		if strings.HasPrefix(r.URL.Path, "/artifact/") {
			version = strings.TrimPrefix(r.URL.Path, "/artifact/")
			if corrupt.Load() {
				_, _ = w.Write([]byte("tampered"))
				return
			}
			_, _ = w.Write(artifacts[version])
			return
		}
		artifact, ok := artifacts[version]
		if !ok {
			http.NotFound(w, r)
			return
		}
		digest := sha512.Sum512(artifact)
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "fixture-plugin", "version": version, "dist": map[string]string{"tarball": registry.URL + "/artifact/" + version, "integrity": "sha512-" + base64.StdEncoding.EncodeToString(digest[:])}})
	}))
	t.Cleanup(registry.Close)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = registry.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	call := func(args ...string) pluginPackageOutput {
		t.Helper()
		output := captureStdout(t, func() {
			if err := run(args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
		})
		var result pluginPackageOutput
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("decode %s: %v", output, err)
		}
		return result
	}
	inspected := call("plugin", "inspect", "--json", "--registry", registry.URL, "npm:fixture-plugin@1.0.0")
	if inspected.Distribution == nil {
		t.Fatal("inspection did not expose integrity pin")
	}
	installed := call("plugin", "install", "--json", "--registry", registry.URL, "--integrity", inspected.Distribution.Integrity, "npm:fixture-plugin@1.0.0")
	if installed.ApprovalRequired || installed.Pending || installed.Distribution == nil {
		t.Fatalf("first install=%+v", installed)
	}
	lockBytes, err := os.ReadFile(filepath.Join(installed.Destination, pluginpkg.DistributionLockFilename))
	if err != nil {
		t.Fatal(err)
	}
	pinned := filepath.Join(t.TempDir(), "old.lock.json")
	if err := os.WriteFile(pinned, lockBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	updated := call("plugin", "update", "--json", "--registry", registry.URL, "npm-cli", "npm:fixture-plugin@1.1.0")
	if updated.ApprovalRequired || updated.Pending || !updated.Replaced || updated.Version != "1.1.0" {
		t.Fatalf("same-source update=%+v", updated)
	}
	corrupt.Store(true)
	var failed error
	captureStdout(t, func() {
		failed = run([]string{"plugin", "update", "--json", "--registry", registry.URL, "npm-cli", "npm:fixture-plugin@1.2.0"})
	})
	if failed == nil || !strings.Contains(failed.Error(), "integrity") {
		t.Fatalf("tampered upgrade error=%v", failed)
	}
	active := userPluginByID(t, home, "npm-cli")
	if active.Fingerprint != updated.Fingerprint {
		t.Fatal("failed upgrade changed the active package")
	}
	corrupt.Store(false)
	restored := call("plugin", "update", "--json", "npm-cli", "lock:"+pinned)
	if restored.ApprovalRequired || restored.Pending || restored.Version != "1.0.0" || restored.Fingerprint != installed.Fingerprint {
		t.Fatalf("explicit locked restore=%+v", restored)
	}
}
