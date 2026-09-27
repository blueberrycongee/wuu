package tools

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"fe80::1", true},
		{"0.0.0.0", true},
		{"::", true},
		{"224.0.0.1", true}, // multicast
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("parse %q", c.ip)
		}
		if got := isBlockedIP(ip); got != c.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

func TestValidateFetchURL(t *testing.T) {
	cases := []struct {
		url       string
		wantBlock bool
	}{
		{"http://example.com", false},
		{"https://example.com/path", false},
		{"file:///etc/passwd", true},
		{"ftp://example.com", true},
		{"http://127.0.0.1/", true},
		{"http://localhost/", false}, // literal "localhost" not IP; caught at dial
		{"http://169.254.169.254/latest/meta-data/", true},
		{"http://[::1]/", true},
		{"http://10.0.0.1/", true},
		{"http://", true},
	}
	for _, c := range cases {
		u, err := url.Parse(c.url)
		if err != nil {
			if !c.wantBlock {
				t.Errorf("parse %q: %v", c.url, err)
			}
			continue
		}
		err = validateFetchURL(u, false)
		if (err != nil) != c.wantBlock {
			t.Errorf("validateFetchURL(%q) = %v, wantBlock=%v", c.url, err, c.wantBlock)
		}
	}
}

func TestWebFetchExecuteBlocksInternal(t *testing.T) {
	cases := []string{
		`{"url":"file:///etc/passwd"}`,
		`{"url":"http://127.0.0.1/"}`,
		`{"url":"http://169.254.169.254/latest/meta-data/"}`,
		`{"url":"http://10.0.0.1/"}`,
		`{"url":"ftp://example.com/"}`,
	}
	for _, args := range cases {
		out, err := webFetchExecute(context.Background(), args, false)
		if err != nil {
			t.Errorf("webFetchExecute(%s) err: %v", args, err)
			continue
		}
		if !strings.Contains(out, "blocked") {
			t.Errorf("webFetchExecute(%s) = %s, expected 'blocked'", args, out)
		}
	}
}

func TestWebEvidenceVersionContext(t *testing.T) {
	ev := newWebEvidence("fetch", "https://example.com/docs", "web_page", time.Now())
	applyWebEvidenceVersionContext(&ev, "", webPackageContext{
		Name:      "next",
		Version:   "15.2.1",
		Ecosystem: "npm",
	})
	if ev.VersionMatchedToRepo != "npm next@15.2.1" {
		t.Fatalf("VersionMatchedToRepo = %q", ev.VersionMatchedToRepo)
	}

	applyWebEvidenceVersionContext(&ev, "repo uses react@19.0.0", webPackageContext{
		Name:      "react",
		Version:   "18.3.1",
		Ecosystem: "npm",
	})
	if ev.VersionMatchedToRepo != "repo uses react@19.0.0" {
		t.Fatalf("version_hint should win, got %q", ev.VersionMatchedToRepo)
	}
}

func TestWebFetchBlockedIncludesEvidence(t *testing.T) {
	out, err := webFetchExecute(context.Background(), `{"url":"http://127.0.0.1/","package_context":{"name":"next","version":"15.2.1","ecosystem":"npm"}}`, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var got struct {
		Action   string      `json:"action"`
		URL      string      `json:"url"`
		Evidence webEvidence `json:"evidence"`
		Error    string      `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, out)
	}
	if got.Action != "web_fetch" {
		t.Fatalf("action = %q, want web_fetch", got.Action)
	}
	if got.Error == "" {
		t.Fatalf("expected error in result: %s", out)
	}
	if got.Evidence.Kind != "fetch" {
		t.Fatalf("evidence kind = %q, want fetch", got.Evidence.Kind)
	}
	if got.Evidence.Source != "http://127.0.0.1/" {
		t.Fatalf("evidence source = %q", got.Evidence.Source)
	}
	if got.Evidence.SourceTier != "web_page" {
		t.Fatalf("evidence source tier = %q, want web_page", got.Evidence.SourceTier)
	}
	if got.Evidence.RetrievedAt == "" {
		t.Fatalf("expected retrieved_at evidence metadata")
	}
	if got.Evidence.VersionMatchedToRepo != "npm next@15.2.1" {
		t.Fatalf("version_matched_to_repo = %q", got.Evidence.VersionMatchedToRepo)
	}
}

func TestToolkitWebEvidenceContextBlockTracksMetadataOnly(t *testing.T) {
	t.Setenv(wuucontext.DerivedContextLedgersEnvVar, "on")
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "fetch-localhost",
		Name:      "web_fetch",
		Arguments: `{"url":"http://127.0.0.1/"}`,
	})
	if err != nil {
		t.Fatalf("web_fetch: %v", err)
	}
	if !strings.Contains(resp, "blocked") {
		t.Fatalf("fixture should be blocked: %s", resp)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || records[0].ResultAction != "web_fetch" {
		t.Fatalf("web_fetch telemetry missing result action: %+v", records)
	}

	block, ok := kit.WebEvidenceContextBlock()
	if !ok {
		t.Fatal("expected web evidence context block")
	}
	if block.Kind != wuucontext.BlockWebEvidence || block.Source != "web_tools" {
		t.Fatalf("unexpected context block metadata: %+v", block)
	}
	for _, want := range []string{
		"recent_web_evidence:",
		"tool=web_fetch",
		"kind=fetch",
		"status=error",
		"source_tier=web_page",
		"source=http://127.0.0.1/",
		"version_matched_to_repo=unknown",
		"blocked",
	} {
		if !strings.Contains(block.Content, want) {
			t.Fatalf("web evidence context missing %q:\n%s", want, block.Content)
		}
	}
	if strings.Contains(block.Content, `"content"`) || strings.Contains(block.Content, "Content-Type") {
		t.Fatalf("web evidence context should not expose fetched bodies or raw payloads:\n%s", block.Content)
	}

	found := false
	for _, candidate := range kit.ContextBlocks() {
		if candidate.Kind == wuucontext.BlockWebEvidence {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ContextBlocks missing WEB_EVIDENCE block")
	}

	clone, err := kit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("CloneForRoot: %v", err)
	}
	if block, ok := clone.WebEvidenceContextBlock(); ok {
		t.Fatalf("clone should not inherit web evidence state: %+v", block)
	}
}

func TestWebFetchExecuteBlocksResolvedInternal(t *testing.T) {
	// A hostname that resolves to 127.0.0.1 should be caught at dial time.
	// "localhost" resolves to loopback on every standard system.
	out, err := webFetchExecute(context.Background(), `{"url":"http://localhost/"}`, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "internal address") && !strings.Contains(out, "blocked") {
		t.Errorf("expected blocked result for localhost, got: %s", out)
	}
}

func TestWebFetchUnconfinedAllowsInternal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("full-access-local"))
	}))
	defer server.Close()

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(UnconfinedBoundary())

	out, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "fetch-local",
		Name:      "web_fetch",
		Arguments: `{"url":"` + server.URL + `"}`,
	})
	if err != nil {
		t.Fatalf("web_fetch: %v", err)
	}
	if strings.Contains(out, "blocked") || !strings.Contains(out, "full-access-local") {
		t.Fatalf("unconfined should fetch local URL, got: %s", out)
	}
}
