package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteWebCompressedAsset(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("export const workspace = 'ready';\n", 1000)
	if err := os.WriteFile(filepath.Join(root, "workbench.js"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	h := remoteWebHandler(root)
	get := func(method, accept, byteRange string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/workbench.js", nil)
		r.Header.Set("Accept-Encoding", accept)
		r.Header.Set("Range", byteRange)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := get("GET", "br, gzip", "")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(body)/2 {
		t.Fatalf("not compressed: %d %v bytes=%d", w.Code, w.Header(), w.Body.Len())
	}
	z, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(z)
	if err != nil || string(decoded) != body {
		t.Fatal("compressed body did not round trip", err)
	}
	for _, accept := range []string{"", "gzip;q=0, *;q=1", "br"} {
		w = get("GET", accept, "")
		if w.Header().Get("Content-Encoding") != "" || w.Body.String() != body {
			t.Fatalf("invalid fallback for %q", accept)
		}
	}
	w = get("GET", "gzip", "bytes=0-5")
	if w.Code != 206 || w.Body.String() != body[:6] {
		t.Fatalf("range failed: %d %q", w.Code, w.Body.String())
	}
	w = get("HEAD", "gzip", "")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("invalid HEAD response", w)
	}
	r := httptest.NewRequest("GET", "/workbench.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-Modified-Since", w.Header().Get("Last-Modified"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal("conditional request failed", w.Code)
	}
}

func TestRemoteWebCacheControl(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("export const asset = 1;\n", 1000)
	if err := os.WriteFile(filepath.Join(root, "assets", "index-CxEm1P_7.js"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "plain.js"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>wuu</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "page-CxEm1P_7.html"), []byte("<!doctype html><title>asset</title>"), 0600); err != nil {
		t.Fatal(err)
	}

	h := remoteWebHandler(root)
	serve := func(method, target, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	const immutable = "public, max-age=31536000, immutable"

	if w := serve("GET", "/assets/index-CxEm1P_7.js", "gzip"); w.Code != 200 || w.Header().Get("Cache-Control") != immutable || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("hashed asset: code=%d cache=%q encoding=%q", w.Code, w.Header().Get("Cache-Control"), w.Header().Get("Content-Encoding"))
	}
	if w := serve("GET", "/assets/index-CxEm1P_7.js", "br"); w.Code != 200 || w.Header().Get("Cache-Control") != immutable {
		t.Fatalf("hashed asset without gzip: code=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := serve("GET", "/assets/plain.js", "gzip"); w.Header().Get("Cache-Control") == immutable {
		t.Fatalf("non-fingerprinted asset marked immutable: %q", w.Header().Get("Cache-Control"))
	}
	if w := serve("GET", "/", ""); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("html cache policy = %q", w.Header().Get("Cache-Control"))
	}
	if w := serve("GET", "/assets/page-CxEm1P_7.html", "gzip"); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("hashed html cache policy = %q", w.Header().Get("Cache-Control"))
	}
	if w := serve("GET", "/assets/missing-a1b2c3d4.js", "gzip"); w.Code != 404 || w.Header().Get("Cache-Control") == immutable {
		t.Fatalf("404 cache policy: code=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := serve("POST", "/assets/index-CxEm1P_7.js", "gzip"); w.Header().Get("Cache-Control") == immutable {
		t.Fatalf("non-GET marked immutable: %q", w.Header().Get("Cache-Control"))
	}

	first := serve("GET", "/assets/index-CxEm1P_7.js", "gzip")
	r := httptest.NewRequest("GET", "/assets/index-CxEm1P_7.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-Modified-Since", first.Header().Get("Last-Modified"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != immutable || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("hashed asset revalidation: code=%d cache=%q encoding=%q", w.Code, w.Header().Get("Cache-Control"), w.Header().Get("Content-Encoding"))
	}
}

func TestRemoteWebConditionalPreconditions(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("export const x = 1;\n", 1000)
	if err := os.WriteFile(filepath.Join(root, "workbench.js"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	h := remoteWebHandler(root)

	first := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/workbench.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(first, r)
	lastModified := first.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("no Last-Modified on first response")
	}

	// A matching If-Modified-Since must be ignored when If-None-Match is
	// present: If-None-Match takes precedence, and a mismatch means the full
	// 200 response, not the IMS 304 shortcut.
	r = httptest.NewRequest("GET", "/workbench.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-Modified-Since", lastModified)
	r.Header.Set("If-None-Match", `"does-not-match"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("If-None-Match mismatch: code=%d encoding=%q", w.Code, w.Header().Get("Content-Encoding"))
	}

	// If-Match mismatch is a failed precondition and must return 412, not be
	// short-circuited by the If-Modified-Since path.
	r = httptest.NewRequest("GET", "/workbench.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-Match", `"does-not-match"`)
	r.Header.Set("If-Modified-Since", lastModified)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match mismatch: code=%d", w.Code)
	}
}
