package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SSETransport communicates with an MCP server over Server-Sent Events.
type SSETransport struct {
	endpoint string
	client   *http.Client
	mu       sync.Mutex
	reader   *bufio.Reader
	resp     *http.Response
}

// NewSSETransport connects to an MCP SSE endpoint.
func NewSSETransport(endpoint string) (*SSETransport, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sse connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("sse connect: %s", resp.Status)
	}
	return &SSETransport{
		endpoint: endpoint,
		client:   client,
		reader:   bufio.NewReader(resp.Body),
		resp:     resp,
	}, nil
}

func (t *SSETransport) Send(ctx context.Context, req Request) error {
	// SSE transport typically POSTs to a message endpoint.
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	// Derive message endpoint from SSE endpoint: replace /sse with /message.
	msgURL := strings.TrimSuffix(t.endpoint, "/sse") + "/message"
	hreq, err := http.NewRequestWithContext(ctx, "POST", msgURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(hreq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sse post %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (t *SSETransport) Receive(ctx context.Context) (Response, error) {
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return Response{}, err
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}
		var resp Response
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			// Some SSE endpoints wrap notifications differently.
			// Try parsing as raw JSON-RPC response.
			continue
		}
		return resp, nil
	}
}

func (t *SSETransport) Close() error {
	if t.resp != nil {
		return t.resp.Body.Close()
	}
	return nil
}
