package externalengine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// OpenCode 1.x's synchronous prompt response is the completion authority.
// SSE supplies progress and permission requests, never an idle-based heuristic.
type openCodeClient struct {
	http           *http.Client
	base, password string
	root           string
}

func (s *Session) runOpenCode(ctx context.Context, message providers.ChatMessage, t *turn) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	password := hex.EncodeToString(secret)
	config := map[string]any{}
	// Retain explicitly supplied native configuration without allowing it to
	// override this process's authentication or host-owned MCP endpoints.
	if raw := os.Getenv("OPENCODE_CONFIG_CONTENT"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
			return errors.New("OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	config["share"], config["autoupdate"] = "disabled", false
	mcp, _ := config["mcp"].(map[string]any)
	if mcp == nil {
		mcp = make(map[string]any)
	}
	for _, server := range s.binding.MCPServers {
		mcp[server.Name] = map[string]any{"type": "remote", "url": server.URL, "enabled": true, "oauth": false}
	}
	config["mcp"] = mcp
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	env := engineEnvironment(map[string]string{
		"OPENCODE_SERVER_PASSWORD": password, "OPENCODE_SERVER_USERNAME": "wuu",
		"OPENCODE_CONFIG_CONTENT": string(encoded),
	})
	args := append(append([]string(nil), s.engine.entry.Args...), "serve", "--hostname", "127.0.0.1", "--port", "0")
	p, err := startChild(s.engine.binary, args, s.binding.RootDir, env)
	if err != nil {
		return err
	}
	defer p.close()
	setup, cancelSetup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSetup()
	base, err := openCodeAddress(setup, p.frames)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	c := &openCodeClient{base: base, password: password, root: s.binding.RootDir, http: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OpenCode redirects are not supported") },
	}}
	// Continue draining stdout, and interrupt HTTP waits if the child dies.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		for {
			select {
			case f, ok := <-p.frames:
				if !ok || f.err != nil {
					cancelRun()
					return
				}
			case <-runCtx.Done():
				return
			}
		}
	}()
	defer func() { cancelRun(); <-monitorDone }()
	err = s.openCodeTurn(runCtx, c, message, t)
	if ctx.Err() == nil && runCtx.Err() != nil {
		return errors.New("OpenCode process exited before the turn completed")
	}
	return err
}

func engineEnvironment(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if _, replaced := overrides[key]; !replaced {
			env = append(env, value)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func openCodeAddress(ctx context.Context, frames <-chan frame) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for OpenCode server: %w", ctx.Err())
		case f, ok := <-frames:
			if !ok || f.err != nil {
				return "", errors.New("OpenCode exited before publishing its local server address")
			}
			address, found := strings.CutPrefix(strings.TrimSpace(string(f.data)), "opencode server listening on ")
			if !found {
				continue
			}
			u, err := url.Parse(address)
			if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
				return "", errors.New("OpenCode published an invalid loopback address")
			}
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return "", errors.New("OpenCode published an invalid port")
			}
			return u.String(), nil
		}
	}
}

func (c *openCodeClient) request(ctx context.Context, method, path string, input any) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path+"?directory="+url.QueryEscape(c.root), body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("wuu", c.password)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenCode %s: %w", method, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		// Arbitrary server error bodies can contain native configuration secrets.
		return nil, fmt.Errorf("OpenCode %s %s: HTTP %d", method, path, response.StatusCode)
	}
	return response, nil
}

func (c *openCodeClient) call(ctx context.Context, method, path string, input, output any) error {
	response, err := c.request(ctx, method, path, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxFrameBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxFrameBytes {
		return errors.New("OpenCode response exceeds size limit")
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("invalid OpenCode response: %w", err)
		}
	}
	return nil
}

func (s *Session) openCodeTurn(ctx context.Context, c *openCodeClient, message providers.ChatMessage, t *turn) error {
	setup, cancelSetup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSetup()
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := c.call(setup, "GET", "/global/health", nil, &health); err != nil {
		return err
	}
	if !health.Healthy || !strings.HasPrefix(health.Version, "1.") {
		return fmt.Errorf("unsupported OpenCode server version %q; this integration requires 1.x", health.Version)
	}
	ref := s.binding.ExternalRef
	// Append a last-match native rule on every turn, including resume, so an
	// earlier unconfined selection cannot survive a later permission change.
	permission := map[string]any{"permission": []map[string]string{{"permission": "*", "pattern": "*", "action": "ask"}}}
	var native struct {
		ID string `json:"id"`
	}
	notice := ""
	if ref == "" {
		if err := c.call(setup, "POST", "/session", permission, &native); err != nil {
			return err
		}
		ref = native.ID
	} else if err := c.call(setup, "PATCH", "/session/"+url.PathEscape(ref), permission, &native); err != nil {
		// A session the agent no longer has — deleted in its own TUI, or a
		// reference from another machine — must not strand the thread: Wuu has
		// no reset entry point for the reference, so every later turn would
		// fail the same way. Start a fresh session and say so, because the
		// agent's earlier context is gone.
		notice = engineResumeNotice(s.engine.entry.Name, truncateUTF8(err.Error(), 200))
		if retryErr := c.call(setup, "POST", "/session", permission, &native); retryErr != nil {
			return fmt.Errorf("resume OpenCode session: %w; create session: %w", err, retryErr)
		}
		ref = native.ID
	} else if native.ID != ref {
		return errors.New("OpenCode resumed a different session")
	}
	if ref != strings.TrimSpace(s.binding.ExternalRef) {
		if err := s.persist(ref); err != nil {
			return err
		}
	}
	if notice != "" {
		t.content(notice, false)
	}
	path := "/session/" + url.PathEscape(ref)
	random := make([]byte, 7)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	// Match native chronological IDs: a 48-bit millisecond/counter prefix and
	// a random suffix. Arbitrary UUIDs would sort outside the native transcript.
	userID := fmt.Sprintf("msg_%012x%s", (uint64(time.Now().UnixMilli())<<12|1)&0xffffffffffff, hex.EncodeToString(random))
	parts := []map[string]string{}
	if message.Content != "" {
		parts = append(parts, map[string]string{"type": "text", "text": message.Content})
	}
	for _, image := range message.Images {
		parts = append(parts, map[string]string{"type": "file", "mime": image.MediaType, "url": "data:" + image.MediaType + ";base64," + image.Data})
	}
	prompt := map[string]any{"messageID": userID, "parts": parts}
	if s.binding.Instructions != "" {
		prompt["system"] = s.binding.Instructions
	}
	if model := strings.TrimSpace(s.binding.Model); model != "" {
		provider, modelID, ok := strings.Cut(model, "/")
		if !ok || provider == "" || modelID == "" {
			return errors.New("OpenCode model must use provider/model format")
		}
		prompt["model"] = map[string]string{"providerID": provider, "modelID": modelID}
	}
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	// Bound connection establishment without setting a timeout on user approval
	// or on the agent's native tool loop.
	stopSetup := context.AfterFunc(setup, cancelStream)
	response, err := c.request(streamCtx, "GET", "/event", nil)
	if err != nil {
		stopSetup()
		return err
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		stopSetup()
		return errors.New("OpenCode event endpoint did not return SSE")
	}
	frames := make(chan frame, 32)
	readDone := make(chan struct{})
	go func() { defer close(readDone); readOpenCodeSSE(streamCtx, response.Body, frames) }()
	defer func() { cancelStream(); _ = response.Body.Close(); <-readDone }()
	// The connected frame is emitted after the native listener is registered.
	for {
		select {
		case <-streamCtx.Done():
			stopSetup()
			return fmt.Errorf("wait for OpenCode event subscription: %w", streamCtx.Err())
		case f := <-frames:
			if f.err != nil {
				stopSetup()
				return f.err
			}
			var event openCodeEvent
			if err := json.Unmarshal(f.data, &event); err != nil {
				stopSetup()
				return err
			}
			if event.Type == "server.connected" {
				if !stopSetup() {
					return setup.Err()
				}
				cancelSetup()
				return s.openCodePrompt(ctx, c, path, userID, prompt, frames, t)
			}
		}
	}
}

func (s *Session) openCodePrompt(ctx context.Context, c *openCodeClient, path, userID string, prompt any, frames <-chan frame, t *turn) (err error) {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var final openCodeMessage
	finished := make(chan error, 1)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		finished <- c.call(requestCtx, "POST", path+"/message", prompt, &final)
	}()
	defer func() {
		if err != nil {
			abortCtx, abort := context.WithTimeout(context.Background(), 2*time.Second)
			defer abort()
			_ = c.call(abortCtx, "POST", path+"/abort", nil, nil)
		}
		cancel()
		<-requestDone
	}()
	state := newOpenCodeTurn(s.binding.ExternalRef, userID, t)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-finished:
			if err != nil {
				return err
			}
			if final.Info.ID == "" || final.Info.Role != "assistant" || final.Info.SessionID != state.ref || final.Info.ParentID != userID {
				return errors.New("OpenCode prompt returned an unrelated final message")
			}
			if final.Info.Error != nil {
				return final.Info.Error
			}
			if final.Info.Finish != "stop" || final.Info.Time.Completed == 0 {
				return fmt.Errorf("OpenCode prompt did not complete normally (finish %q)", final.Info.Finish)
			}
			// Reconcile durable snapshots after completion. This also recovers
			// progress frames still in transit when the HTTP response arrives.
			var messages []openCodeMessage
			reconcile, done := context.WithTimeout(ctx, 30*time.Second)
			err = c.call(reconcile, "GET", path+"/message", nil, &messages)
			done()
			if err != nil {
				return err
			}
			for _, message := range messages {
				if err := state.message(message); err != nil {
					return err
				}
			}
			if err := state.message(final); err != nil {
				return err
			}
			if len(t.tools) > 0 {
				return errors.New("OpenCode completed with unfinished tool calls")
			}
			t.stopReason = "end_turn"
			return nil
		case f := <-frames:
			if f.err != nil {
				return f.err
			}
			if err := s.openCodeEvent(ctx, c, state, f.data); err != nil {
				return err
			}
		}
	}
}

func readOpenCodeSSE(ctx context.Context, body io.Reader, frames chan<- frame) {
	send := func(f frame) bool {
		select {
		case frames <- f:
			return true
		case <-ctx.Done():
			return false
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
	var data []byte
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 && !send(frame{data: bytes.TrimSuffix(data, []byte("\n"))}) {
				return
			}
			data = nil
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			value = strings.TrimPrefix(value, " ")
			if len(data)+len(value)+1 > maxFrameBytes {
				send(frame{err: errors.New("OpenCode SSE event exceeds size limit")})
				return
			}
			data = append(append(data, value...), '\n')
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	send(frame{err: fmt.Errorf("OpenCode event stream ended: %w", err)})
}
