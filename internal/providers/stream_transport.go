package providers

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultStreamConnectTimeout = 15 * time.Second
	// Response headers arrive only after server-side queueing and, on some
	// providers, prompt prefill. Large-context requests routinely need tens
	// of seconds before the first header byte, so this deadline must be far
	// looser than the dial/TLS one: measured MiniMax TTFB at ~70k input was
	// 8-11s at normal load, with spikes past 15s under pressure.
	defaultStreamHeaderTimeout = 120 * time.Second
	// Longer timeout is needed for models with extended thinking phases.
	defaultStreamIdleTimeout = 300 * time.Second
)

// StreamTransportMode is the caller's preferred transport for providers that
// expose more than one streaming path. Providers without a matching transport
// ignore unsupported modes or reject them at construction time.
type StreamTransportMode string

const (
	StreamTransportAuto            StreamTransportMode = "auto"
	StreamTransportSSE             StreamTransportMode = "sse"
	StreamTransportWebSocket       StreamTransportMode = "websocket"
	StreamTransportWebSocketCached StreamTransportMode = "websocket-cached"
)

func NormalizeStreamTransportMode(value string) (StreamTransportMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", true
	case string(StreamTransportAuto):
		return StreamTransportAuto, true
	case string(StreamTransportSSE):
		return StreamTransportSSE, true
	case string(StreamTransportWebSocket):
		return StreamTransportWebSocket, true
	case string(StreamTransportWebSocketCached), "websocket_cached":
		return StreamTransportWebSocketCached, true
	default:
		return "", false
	}
}

// StreamTransportConfig splits the transport deadlines that govern one live
// streaming response. ConnectTimeout bounds dial and TLS handshake.
// HeaderTimeout bounds the wait from request sent to first response headers,
// which includes server-side queueing and prompt prefill. IdleTimeout bounds
// silence after the stream has started.
type StreamTransportConfig struct {
	ConnectTimeout time.Duration
	HeaderTimeout  time.Duration
	IdleTimeout    time.Duration
}

// ResolveStreamTransportConfig applies defaults and environment overrides.
// Explicit config wins over defaults; env vars remain the last-mile override
// so existing workflows keep working.
func ResolveStreamTransportConfig(cfg *StreamTransportConfig) StreamTransportConfig {
	resolved := StreamTransportConfig{
		ConnectTimeout: defaultStreamConnectTimeout,
		HeaderTimeout:  defaultStreamHeaderTimeout,
		IdleTimeout:    defaultStreamIdleTimeout,
	}
	if cfg != nil {
		if cfg.ConnectTimeout > 0 {
			resolved.ConnectTimeout = cfg.ConnectTimeout
		}
		if cfg.HeaderTimeout > 0 {
			resolved.HeaderTimeout = cfg.HeaderTimeout
		}
		if cfg.IdleTimeout > 0 {
			resolved.IdleTimeout = cfg.IdleTimeout
		}
	}
	if env := durationFromEnv("WUU_STREAM_CONNECT_TIMEOUT_MS"); env > 0 {
		resolved.ConnectTimeout = env
	}
	if env := durationFromEnv("WUU_STREAM_HEADER_TIMEOUT_MS"); env > 0 {
		resolved.HeaderTimeout = env
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
// Build once per provider client: cloning a transport per request discards its
// connection pool. Credential adapters should share the built client while
// setting authentication on each request. Custom RoundTrippers are shared as-is.
func BuildStreamingHTTPClient(base *http.Client, cfg StreamTransportConfig) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	streamClient := *base
	streamClient.Timeout = 0

	transport, ok := cloneStreamingTransport(base.Transport)
	if ok && (cfg.ConnectTimeout > 0 || cfg.HeaderTimeout > 0) {
		if cfg.ConnectTimeout > 0 {
			transport.TLSHandshakeTimeout = cfg.ConnectTimeout
			transport.DialContext = wrapDialContextWithTimeout(transport.DialContext, cfg.ConnectTimeout)
		}
		// Header wait is a separate, much looser deadline than dial/TLS:
		// binding it to ConnectTimeout guillotined every large-context
		// request whose prefill pushed first headers past 15s, and the
		// identical retries then exhausted the replay budget.
		headerTimeout := cfg.HeaderTimeout
		if headerTimeout <= 0 {
			headerTimeout = defaultStreamHeaderTimeout
		}
		transport.ResponseHeaderTimeout = headerTimeout
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
