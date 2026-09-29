package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SSETransport communicates with an MCP server over Server-Sent Events.
type SSETransport struct {
	endpoint        string
	messageEndpoint string
	client          *http.Client
	headers         map[string]string
	authProvider    TokenProvider
	reader          *bufio.Reader
	resp            *http.Response
	ctx             context.Context
	cancel          context.CancelFunc
	closeOnce       sync.Once
	closeErr        error
}

func newSSETransport(ctx context.Context, endpoint string, headers map[string]string) (*SSETransport, error) {
	return newSSETransportWithAuth(ctx, endpoint, headers, nil)
}

func newSSETransportWithAuth(ctx context.Context, endpoint string, headers map[string]string, authProvider TokenProvider) (*SSETransport, error) {
	client := newSSEHTTPClient()
	transportCtx, cancel := context.WithCancel(context.Background())
	stopCallerCancel := context.AfterFunc(ctx, cancel)
	req, err := http.NewRequestWithContext(transportCtx, "GET", endpoint, nil)
	if err != nil {
		stopCallerCancel()
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	if err := applyToken(req, authProvider, ctx, false); err != nil {
		stopCallerCancel()
		cancel()
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		stopCallerCancel()
		cancel()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("sse connect GET %s: %w", endpoint, contextErr)
		}
		return nil, fmt.Errorf("sse connect GET %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		stopCallerCancel()
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("sse connect GET %s: %s", endpoint, resp.Status)
	}
	if !stopCallerCancel() {
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("sse connect GET %s: %w", endpoint, ctx.Err())
	}
	transport := &SSETransport{
		endpoint:     endpoint,
		client:       client,
		headers:      cloneStringMap(headers),
		authProvider: authProvider,
		reader:       bufio.NewReader(resp.Body),
		resp:         resp,
		ctx:          transportCtx,
		cancel:       cancel,
	}
	// Legacy SSE servers announce the POST target in the first endpoint
	// event. Waiting here is important: deriving /message loses session query
	// parameters and breaks servers hosted behind a different route.
	event, err := readSSEEvent(transport.reader)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("sse endpoint event: %w", err)
	}
	if event.event != "endpoint" || strings.TrimSpace(event.data) == "" {
		_ = transport.Close()
		return nil, errors.New("sse stream did not declare an endpoint event")
	}
	messageEndpoint, err := resolveSSEEndpoint(endpoint, event.data)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("sse endpoint event: %w", err)
	}
	transport.messageEndpoint = messageEndpoint
	return transport, nil
}

func newSSEHTTPClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		tr = tr.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	// Bound the connection handshake without imposing a lifetime on the SSE
	// response body, which is expected to remain open indefinitely.
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}

func (t *SSETransport) Send(ctx context.Context, req Request) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	msgURL := t.messageEndpoint
	requestCtx, cancelRequest := context.WithCancel(t.ctx)
	stopCallerCancel := context.AfterFunc(ctx, cancelRequest)
	defer func() {
		stopCallerCancel()
		cancelRequest()
	}()
	for attempt := 0; attempt < 2; attempt++ {
		hreq, err := http.NewRequestWithContext(requestCtx, "POST", msgURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		hreq.Header.Set("Content-Type", "application/json")
		for key, value := range t.headers {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			hreq.Header.Set(key, value)
		}
		if err := applyToken(hreq, t.authProvider, ctx, attempt == 1); err != nil {
			return err
		}
		resp, err := t.client.Do(hreq)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 && t.authProvider != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMCPBodyExcerptBytes))
			_ = resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("sse post %d: %s", resp.StatusCode, readBodyExcerpt(resp.Body))
		}
		return nil
	}
	return errors.New("sse post authentication retry exhausted")
}

func applyToken(req *http.Request, provider TokenProvider, ctx context.Context, forceRefresh bool) error {
	if provider == nil {
		return nil
	}
	token, err := provider(ctx, forceRefresh)
	if err != nil {
		return fmt.Errorf("MCP OAuth token: %w", err)
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (t *SSETransport) Receive(ctx context.Context) (Response, error) {
	for {
		event, err := readSSEEvent(t.reader)
		if err != nil {
			return Response{}, err
		}
		if strings.TrimSpace(event.data) == "" {
			continue
		}
		var resp Response
		if err := json.Unmarshal([]byte(event.data), &resp); err != nil {
			return Response{}, fmt.Errorf("decode sse JSON-RPC event: %w", err)
		}
		return resp, nil
	}
}

func resolveSSEEndpoint(streamURL, announced string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(streamURL))
	if err != nil {
		return "", err
	}
	target, err := url.Parse(strings.TrimSpace(announced))
	if err != nil {
		return "", err
	}
	if !target.IsAbs() && target.Path == "" && target.RawQuery == "" {
		return "", errors.New("endpoint event is empty")
	}
	return base.ResolveReference(target).String(), nil
}

func (t *SSETransport) Close() error {
	t.closeOnce.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.resp != nil {
			t.closeErr = t.resp.Body.Close()
		}
	})
	return t.closeErr
}
