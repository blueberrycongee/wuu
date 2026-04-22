package providers

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	// Aligned with CC's SDK timeout (API_TIMEOUT_MS default: 600s).
	// Covers relay overhead + large context + Opus thinking warm-up.
	defaultStreamConnectTimeout = 600 * time.Second
	// Aligned with Codex's DEFAULT_STREAM_IDLE_TIMEOUT_MS (300s).
	// Longer timeout is needed for models with extended thinking phases.
	defaultStreamIdleTimeout = 300 * time.Second
)

// StreamTransportConfig splits the transport deadlines that govern one live
// streaming response. ConnectTimeout bounds dial/TLS/response-header wait.
// IdleTimeout bounds silence after the stream has started.
type StreamTransportConfig struct {
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
}

// ResolveStreamTransportConfig applies defaults and environment overrides.
// Explicit config wins over defaults; env vars remain the last-mile override
// so existing workflows keep working.
func ResolveStreamTransportConfig(cfg *StreamTransportConfig) StreamTransportConfig {
	resolved := StreamTransportConfig{
		ConnectTimeout: defaultStreamConnectTimeout,
		IdleTimeout:    defaultStreamIdleTimeout,
	}
	if cfg != nil {
		if cfg.ConnectTimeout > 0 {
			resolved.ConnectTimeout = cfg.ConnectTimeout
		}
		if cfg.IdleTimeout > 0 {
			resolved.IdleTimeout = cfg.IdleTimeout
		}
	}
	if env := durationFromEnv("WUU_STREAM_CONNECT_TIMEOUT_MS"); env > 0 {
		resolved.ConnectTimeout = env
	}
	if env := durationFromEnv("WUU_STREAM_IDLE_TIMEOUT_MS"); env > 0 {
		resolved.IdleTimeout = env
	}
	return resolved
}

func durationFromEnv(name string) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return 0
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// BuildStreamingHTTPClient clones base into a long-lived streaming client and
// attaches connect-stage deadlines without imposing a total request timeout.
func BuildStreamingHTTPClient(base *http.Client, cfg StreamTransportConfig) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	streamClient := *base
	streamClient.Timeout = 0

	transport, ok := cloneStreamingTransport(base.Transport)
	if ok && cfg.ConnectTimeout > 0 {
		transport.ResponseHeaderTimeout = cfg.ConnectTimeout
		transport.TLSHandshakeTimeout = cfg.ConnectTimeout
		transport.DialContext = wrapDialContextWithTimeout(transport.DialContext, cfg.ConnectTimeout)
		streamClient.Transport = transport
	}
	return &streamClient
}

func cloneStreamingTransport(base http.RoundTripper) (*http.Transport, bool) {
	if base == nil {
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, false
		}
		return defaultTransport.Clone(), true
	}
	transport, ok := base.(*http.Transport)
	if !ok {
		return nil, false
	}
	return transport.Clone(), true
}

func wrapDialContextWithTimeout(
	next func(context.Context, string, string) (net.Conn, error),
	timeout time.Duration,
) func(context.Context, string, string) (net.Conn, error) {
	if timeout <= 0 {
		return next
	}
	if next == nil {
		dialer := &net.Dialer{Timeout: timeout}
		return dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return next(dialCtx, network, addr)
	}
}
