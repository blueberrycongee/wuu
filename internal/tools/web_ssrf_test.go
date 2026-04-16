package tools

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
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
		err = validateFetchURL(u)
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
		out, err := webFetchExecute(context.Background(), args)
		if err != nil {
			t.Errorf("webFetchExecute(%s) err: %v", args, err)
			continue
		}
		if !strings.Contains(out, "blocked") {
			t.Errorf("webFetchExecute(%s) = %s, expected 'blocked'", args, out)
		}
	}
}

func TestWebFetchExecuteBlocksResolvedInternal(t *testing.T) {
	// A hostname that resolves to 127.0.0.1 should be caught at dial time.
	// "localhost" resolves to loopback on every standard system.
	out, err := webFetchExecute(context.Background(), `{"url":"http://localhost/"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "internal address") && !strings.Contains(out, "blocked") {
		t.Errorf("expected blocked result for localhost, got: %s", out)
	}
}
