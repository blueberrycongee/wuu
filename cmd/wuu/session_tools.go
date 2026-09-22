package main

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
	"os"
	"strings"
	"time"
)

// This command only transports MCP frames to the owning app-server. It must
// never initialize a runtime or discover a different server from local state.
func runSessionTools(args []string) error {
	if len(args) != 1 || args[0] != "--stdio" {
		return errors.New("usage: wuu session-tools --stdio (requires WUU_SESSION_TOOLS_URL from the host)")
	}
	return proxySessionTools(context.Background(), os.Getenv("WUU_SESSION_TOOLS_URL"), os.Stdin, os.Stdout)
}

func proxySessionTools(ctx context.Context, endpoint string, input io.Reader, output io.Writer) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("session tools require a host-provided loopback endpoint")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not supported") },
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return errors.New("invalid session tool JSON-RPC frame")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			return errors.New("invalid session tool request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		response, err := client.Do(req)
		if err != nil {
			// net/http errors include the URL, whose path is a capability secret.
			return errors.New("session tool connection failed; the owning turn may have ended")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		_ = response.Body.Close()
		if readErr != nil || len(body) > 1<<20 {
			return errors.New("invalid session tool response")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("session tools unavailable (HTTP %d)", response.StatusCode)
		}
		if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent {
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, body); err != nil {
			return errors.New("session tools returned invalid JSON")
		}
		if _, err := io.WriteString(output, strings.TrimSpace(compact.String())+"\n"); err != nil {
			return err
		}
	}
	return scanner.Err()
}
