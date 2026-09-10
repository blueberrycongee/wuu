package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// fingerprintedAssetRe matches Vite's default content-hashed build output
// under assets/: the basename ends with "-<hash>.<ext>" where the hash is an
// 8+ character base64url value (for example index-CxEm1P_7.js).
var fingerprintedAssetRe = regexp.MustCompile(`^[^/]+-[A-Za-z0-9_-]{8,}\.[^./]+$`)

// cachePolicy returns the Cache-Control value for a resolved URL path, or ""
// to leave the default behavior. Content-hashed assets are immutable; HTML
// must stay revalidatable so fresh builds are picked up without a new URL.
func cachePolicy(name string) string {
	// HTML always revalidates, even when a hashed name appears under assets/,
	// so fresh builds are picked up without changing the entry URL.
	if path.Ext(name) == ".html" {
		return "no-cache"
	}
	if strings.HasPrefix(name, "/assets/") && fingerprintedAssetRe.MatchString(path.Base(name)) {
		return "public, max-age=31536000, immutable"
	}
	return ""
}

// Compress public web assets before they cross a potentially slow relay link.
// Keep the standard file server for ranges, directories, and non-text assets.
func remoteWebHandler(root string) http.Handler {
	fs := http.Dir(root)
	fallback := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		name := path.Clean("/" + r.URL.Path)
		if name == "/" {
			name = "/index.html"
		}

		// Advertise the cache policy only for GET/HEAD requests that resolve
		// to a real file, so 404s, directory listings, and method errors are
		// never marked immutable. The same policy must apply to the gzip and
		// fallback paths because fonts, images, ranges, and non-gzip clients
		// all skip the compression branch.
		if r.Method == "GET" || r.Method == "HEAD" {
			if policy := cachePolicy(name); policy != "" {
				if f, err := fs.Open(name); err == nil {
					if info, statErr := f.Stat(); statErr == nil && !info.IsDir() {
						w.Header().Set("Cache-Control", policy)
					}
					f.Close()
				}
			}
		}

		ext := path.Ext(name)
		text := ext == ".js" || ext == ".css" || ext == ".html" || ext == ".svg" || ext == ".json"
		if (r.Method != "GET" && r.Method != "HEAD") || r.Header.Get("Range") != "" || !text || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			fallback.ServeHTTP(w, r)
			return
		}
		f, err := fs.Open(name)
		if err != nil {
			fallback.ServeHTTP(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() || info.Size() < 1024 || info.Size() > 16<<20 {
			fallback.ServeHTTP(w, r)
			return
		}

		// Answer conditional revalidations before gzipping so an unchanged
		// asset is not recompressed on every If-Modified-Since request. Only
		// take this shortcut when no other preconditions are present:
		// If-None-Match takes precedence over If-Modified-Since, and If-Match
		// / If-Unmodified-Since must be evaluated by http.ServeContent.
		if r.Header.Get("If-Match") == "" && r.Header.Get("If-Unmodified-Since") == "" && r.Header.Get("If-None-Match") == "" {
			if ims := r.Header.Get("If-Modified-Since"); ims != "" {
				if t, err := http.ParseTime(ims); err == nil && info.ModTime().Truncate(time.Second).Before(t.Add(time.Second)) {
					w.Header().Set("Content-Type", mime.TypeByExtension(ext))
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		_, err = io.Copy(z, io.LimitReader(f, (16<<20)+1))
		closeErr := z.Close()
		if err != nil || closeErr != nil {
			w.Header().Del("Cache-Control")
			http.Error(w, "cannot read web asset", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(ext))
		w.Header().Set("Content-Encoding", "gzip")
		http.ServeContent(w, r, info.Name(), info.ModTime(), bytes.NewReader(compressed.Bytes()))
	})
}

func acceptsGzip(value string) bool {
	wildcard := false
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, param := range parts[1:] {
			key, val, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && strings.EqualFold(key, "q") {
				quality, _ = strconv.ParseFloat(val, 64)
			}
		}
		if coding == "gzip" {
			return quality > 0 && quality <= 1
		}
		if coding == "*" {
			wildcard = quality > 0 && quality <= 1
		}
	}
	return wildcard
}
