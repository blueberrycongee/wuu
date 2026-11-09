package providers

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResolveStreamTransportConfig_EnvOverrides(t *testing.T) {
	t.Setenv("WUU_STREAM_CONNECT_TIMEOUT_MS", "1500")
	t.Setenv("WUU_STREAM_IDLE_TIMEOUT_MS", "2500")

	cfg := ResolveStreamTransportConfig(&StreamTransportConfig{
		ConnectTimeout: 5 * time.Second,
		IdleTimeout:    6 * time.Second,
	})
	if cfg.ConnectTimeout != 1500*time.Millisecond {
		t.Fatalf("expected env connect timeout, got %s", cfg.ConnectTimeout)
	}
	if cfg.IdleTimeout != 2500*time.Millisecond {
		t.Fatalf("expected env idle timeout, got %s", cfg.IdleTimeout)
	}
}

func TestBuildStreamingHTTPClient_SetsConnectStageDeadlines(t *testing.T) {
	base := &http.Client{Transport: http.DefaultTransport}
	cfg := StreamTransportConfig{ConnectTimeout: 1234 * time.Millisecond}

	streamClient := BuildStreamingHTTPClient(base, cfg)
	if streamClient == base {
		t.Fatal("expected cloned client")
	}
	if streamClient.Timeout != 0 {
		t.Fatalf("expected streaming client timeout disabled, got %s", streamClient.Timeout)
	}

	transport, ok := streamClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", streamClient.Transport)
	}
	// Header wait is decoupled from the connect stage: unset HeaderTimeout
	// falls back to the loose default instead of inheriting ConnectTimeout,
	// which guillotined large-context requests whose prefill pushed first
	// headers past the dial deadline.
	if transport.ResponseHeaderTimeout != defaultStreamHeaderTimeout {
		t.Fatalf("expected response header timeout %s, got %s", defaultStreamHeaderTimeout, transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != cfg.ConnectTimeout {
		t.Fatalf("expected TLS handshake timeout %s, got %s", cfg.ConnectTimeout, transport.TLSHandshakeTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be configured")
	}
}

func TestBuildStreamingHTTPClient_SeparatesHeaderDeadline(t *testing.T) {
	base := &http.Client{Transport: http.DefaultTransport}
	cfg := StreamTransportConfig{
		ConnectTimeout: 1 * time.Second,
		HeaderTimeout:  90 * time.Second,
	}

	streamClient := BuildStreamingHTTPClient(base, cfg)
	transport, ok := streamClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", streamClient.Transport)
	}
	if transport.ResponseHeaderTimeout != 90*time.Second {
		t.Fatalf("expected 90s response header timeout, got %s", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 1*time.Second {
		t.Fatalf("expected 1s TLS handshake timeout, got %s", transport.TLSHandshakeTimeout)
	}
}

// Compression is an HTTP wire contract: opting in must not change JSON bytes,
// replay bodies, cancellation, redirect boundaries, or the caller's client.
func TestPrepareRequestCompression(t *testing.T) {
	large := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("synthetic 界🙂 ", 10000) + `"}]}`)
	incompressible := make([]byte, 128<<10)
	_, _ = rand.New(rand.NewSource(1)).Read(incompressible)
	for _, tc := range []struct {
		name, endpoint, optIn, encoding string
		body                            []byte
		compressed                      bool
	}{
		{name: "default off", body: large},
		{name: "exact endpoint", body: large, optIn: "https://example.test/v1/responses", compressed: true},
		{name: "list", body: large, optIn: " https://other.test/v1/messages , https://example.test/v1/responses ", compressed: true},
		{name: "other host", body: large, optIn: "https://other.test/v1/responses"},
		{name: "base URL is not endpoint", body: large, optIn: "https://example.test/v1"},
		{name: "path prefix", body: large, optIn: "https://example.test/v1/responses/extra"},
		{name: "query mismatch", body: large, endpoint: "https://example.test/v1/responses?mode=test", optIn: "https://example.test/v1/responses"},
		{name: "exact query", body: large, endpoint: "https://example.test/v1/responses?mode=test", optIn: "https://example.test/v1/responses?mode=test", compressed: true},
		{name: "userinfo", body: large, endpoint: "https://user@example.test/v1/responses", optIn: "https://user@example.test/v1/responses"},
		{name: "fragment", body: large, endpoint: "https://example.test/v1/responses#test", optIn: "https://example.test/v1/responses#test"},
		{name: "small", body: []byte(`{"messages":[]}`), optIn: "https://example.test/v1/responses"},
		{name: "below threshold", body: bytes.Repeat([]byte("a"), (64<<10)-1), optIn: "https://example.test/v1/responses"},
		{name: "at threshold", body: bytes.Repeat([]byte("a"), 64<<10), optIn: "https://example.test/v1/responses", compressed: true},
		{name: "no savings", body: incompressible, optIn: "https://example.test/v1/responses"},
		{name: "existing encoding", body: large, encoding: "br", optIn: "https://example.test/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", tc.optIn)
			endpoint := tc.endpoint
			if endpoint == "" {
				endpoint = "https://example.test/v1/responses"
			}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Length", strconv.Itoa(len(tc.body)))
			if tc.encoding != "" {
				req.Header.Set("Content-Encoding", tc.encoding)
			}
			original := &http.Client{Timeout: time.Second}
			client, err := PrepareRequestCompression(req, tc.body, original)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := req.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := io.ReadAll(replay)
			_ = replay.Close()
			if err != nil || !bytes.Equal(wire, replayed) || req.ContentLength != int64(len(wire)) {
				t.Fatalf("wire/replay/content length differ: replay error=%v", err)
			}
			if client.Timeout != original.Timeout || original.CheckRedirect != nil {
				t.Fatal("caller timeout or redirect policy changed")
			}
			decoded := wire
			if tc.compressed {
				if req.Header.Get("Content-Encoding") != "gzip" || req.Header.Get("Content-Length") != "" || len(wire) >= len(tc.body) {
					t.Fatal("compressed request has invalid headers or no savings")
				}
				reader, err := gzip.NewReader(bytes.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err = io.ReadAll(reader)
				_ = reader.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if req.Header.Get("Content-Encoding") != tc.encoding || client != original {
				t.Fatal("ineligible request changed")
			}
			if !bytes.Equal(decoded, tc.body) {
				t.Fatal("request bytes changed")
			}
		})
	}
}

func TestRequestCompressionCancellationAndRedirects(t *testing.T) {
	body := bytes.Repeat([]byte("synthetic"), 20000)
	endpoint := "https://example.test/v1/responses"
	t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", endpoint)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if _, err := PrepareRequestCompression(req, body, &http.Client{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation error = %v", err)
	}
	if req.Header.Get("Content-Encoding") != "" || req.ContentLength != int64(len(body)) {
		t.Fatal("cancelled preparation mutated request")
	}

	redirectErr := errors.New("caller redirect policy")
	for _, custom := range []bool{false, true} {
		req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		original := &http.Client{}
		if custom {
			original.CheckRedirect = func(*http.Request, []*http.Request) error { return redirectErr }
		}
		client, err := PrepareRequestCompression(req, body, original)
		if err != nil {
			t.Fatal(err)
		}
		other, _ := http.NewRequest(http.MethodPost, "https://example.test/v1/other", nil)
		if err := client.CheckRedirect(other, []*http.Request{req}); err == nil {
			t.Fatal("compressed redirect to an unlisted endpoint was allowed")
		}
		err = client.CheckRedirect(req, []*http.Request{req})
		if custom && !errors.Is(err, redirectErr) || !custom && err != nil {
			t.Fatalf("caller redirect policy was not preserved: %v", err)
		}
		if !custom && client.CheckRedirect(req, make([]*http.Request, 10)) == nil {
			t.Fatal("default redirect limit was lost")
		}
	}
}

func TestRequestCompressionHonorsRedirectCallback(t *testing.T) {
	endpoint := "https://example.test/v1/responses"
	t.Setenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS", endpoint)
	body := bytes.Repeat([]byte("synthetic"), 20000)
	for _, stop := range []bool{false, true} {
		req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		original := &http.Client{CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if stop {
				return http.ErrUseLastResponse
			}
			next.URL.Path = "/unlisted"
			return nil
		}}
		client, err := PrepareRequestCompression(req, body, original)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := http.NewRequest(http.MethodPost, endpoint, nil)
		if stop {
			next.URL.Path = "/unlisted"
		}
		err = client.CheckRedirect(next, []*http.Request{req})
		if stop && err != http.ErrUseLastResponse {
			t.Fatalf("caller stop policy changed: %v", err)
		}
		if !stop && err == nil {
			t.Fatal("callback redirected gzip to an unlisted endpoint")
		}
	}
}
