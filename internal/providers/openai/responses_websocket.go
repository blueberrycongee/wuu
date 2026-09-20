package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

// Beta header value required for the Responses-over-WebSocket surface.
// Without it the upgrade request is rejected as a non-Responses endpoint.
const CodexWebSocketBetaTag = "responses_websockets=2026-02-06"

// Allow one lookahead byte so the read pump can report a typed local limit
// error before the WebSocket library replaces it with an untyped read error.
const codexWebSocketReadLimitBytes = providers.MaxStreamEventBytes + 1

// Converts an HTTP Responses base URL into its WebSocket-equivalent endpoint.
// The regular Responses client stores a base URL and appends /responses at
// dispatch time; the WebSocket path sends the upgrade directly, so it must
// resolve to the concrete /responses endpoint.
func resolveCodexWebSocketURL(baseURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return "", errors.New("codex websocket: empty base URL")
	}
	if !strings.HasSuffix(trimmed, "/responses") && !strings.Contains(trimmed, "/responses/") {
		trimmed += "/responses"
	}
	switch {
	case strings.HasPrefix(trimmed, "https://"):
		return "wss://" + strings.TrimPrefix(trimmed, "https://"), nil
	case strings.HasPrefix(trimmed, "http://"):
		return "ws://" + strings.TrimPrefix(trimmed, "http://"), nil
	case strings.HasPrefix(trimmed, "wss://"), strings.HasPrefix(trimmed, "ws://"):
		return trimmed, nil
	default:
		return "", fmt.Errorf("codex websocket: unrecognized scheme in %q", baseURL)
	}
}

// Bundles the connect-time options that are independent of any single request.
// Request-specific headers are passed per-call so callers can keep them
// consistent with the existing static client.headers path.
type CodexWebSocketDialer struct {
	// ConnectTimeout bounds dial + TLS + upgrade handshake. Zero defers
	// to the caller's context deadline only.
	ConnectTimeout time.Duration
	// HTTPClient performs the upgrade handshake, so the connection honors the
	// configured proxy, custom CA, and other transport settings instead of
	// silently dialing through http.DefaultClient. Nil falls back to the
	// library default.
	HTTPClient *http.Client
}

// CodexWebSocketDialError preserves the HTTP upgrade status, when one was
// returned, without requiring callers to recover it from formatted text.
type CodexWebSocketDialError struct {
	URL        string
	StatusCode int
	Err        error
}

func (e *CodexWebSocketDialError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("codex websocket dial %q: status=%d: %v", e.URL, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("codex websocket dial %q: %v", e.URL, e.Err)
}

func (e *CodexWebSocketDialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Opens a Responses-over-WebSocket connection using the standard upgrade
// flow, forwarding request-specific headers and adding the required beta tag
// when absent. The returned connection must be closed by the caller; this
// function does not retain ownership. On upgrade failure the underlying
// http.Response status is included in the wrapped error so callers can
// distinguish 401 / 403 / 404 / 4xx from network-level failures.
func (d CodexWebSocketDialer) dialCodexWebSocket(
	ctx context.Context,
	wsURL string,
	headers http.Header,
) (*websocket.Conn, error) {
	if ctx == nil {
		return nil, errors.New("codex websocket: nil context")
	}
	if wsURL == "" {
		return nil, errors.New("codex websocket: empty URL")
	}
	if headers == nil {
		headers = http.Header{}
	}
	// The endpoint rejects upgrades without this beta tag. The SSE Responses
	// path uses a different tag; only the WS Responses path requires this one.
	// Callers can still override by setting the header before passing it in.
	if headers.Get("OpenAI-Beta") == "" {
		headers.Set("OpenAI-Beta", CodexWebSocketBetaTag)
	}

	dialCtx := ctx
	if d.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, d.ConnectTimeout)
		defer cancel()
	}

	conn, resp, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
		HTTPClient: d.HTTPClient,
	})
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
			_ = resp.Body.Close()
		}
		return nil, &CodexWebSocketDialError{URL: wsURL, StatusCode: statusCode, Err: err}
	}
	conn.SetReadLimit(codexWebSocketReadLimitBytes)
	return conn, nil
}
