// Package cualinux implements window-bound Computer Use on an X11 desktop.
package cualinux

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type command struct {
	Action          string   `json:"action"`
	App             string   `json:"app"`
	Epoch           string   `json:"control_epoch"`
	SnapshotID      string   `json:"snapshot_id"`
	Mode            string   `json:"mode"`
	After           string   `json:"after"`
	Foreground      string   `json:"foreground_policy"`
	X               *float64 `json:"x"`
	Y               *float64 `json:"y"`
	FromX           *float64 `json:"from_x"`
	FromY           *float64 `json:"from_y"`
	ToX             *float64 `json:"to_x"`
	ToY             *float64 `json:"to_y"`
	CoordinateSpace string   `json:"coordinate_space"`
	Button          string   `json:"mouse_button"`
	Count           int      `json:"click_count"`
	Duration        float64  `json:"duration"`
	Modifiers       []string `json:"modifiers"`
	Key             string   `json:"key"`
	Keys            []string `json:"keys"`
	Text            string   `json:"text"`
	Direction       string   `json:"direction"`
	Pages           int      `json:"pages"`
}

type result struct {
	Text string
	PNG  []byte
	Data map[string]any
}

type failure struct{ code, message string }

func (e *failure) Error() string { return e.code + ": " + e.message }
func fail(code, format string, args ...any) error {
	return &failure{code, fmt.Sprintf(format, args...)}
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type pending struct {
	request request
	args    command
	ctx     context.Context
	cancel  context.CancelFunc
}

// Run serves newline-delimited MCP, or a read-only window metadata watcher.
func Run(args []string, input io.Reader, output io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if len(args) > 0 {
		return runWatcher(ctx, args, input, output)
	}
	b := &backend{snapshots: make(map[string]snapshot)}
	defer b.close()
	var mu, outputMu sync.Mutex
	closing := false
	calls := make(map[string]*pending)
	revoked := make(map[string]bool)
	queue := make(chan *pending, 64)
	write := func(id json.RawMessage, value any, rpcErr bool) {
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr {
			response["error"] = value
		} else {
			response["result"] = value
		}
		outputMu.Lock()
		defer outputMu.Unlock()
		_ = json.NewEncoder(output).Encode(response)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for p := range queue {
			r, err := handle(b, p)
			if err != nil {
				code := "operation_failed"
				var f *failure
				if errors.As(err, &f) {
					code = f.code
				}
				data := map[string]any{"error": err.Error(), "error_code": code, "delivery": "not_delivered"}
				if r.Data != nil {
					for k, v := range r.Data {
						data[k] = v
					}
				}
				r = result{Text: err.Error(), Data: data}
			}
			if err != nil || p.request.Method == "tools/call" {
				// The host's structured index is intentionally bounded. Keep every
				// target and snapshot ID in producer-owned text so the model can
				// discover windows and continue after input without guessing IDs.
				metadata, encodeErr := json.Marshal(r.Data)
				if encodeErr != nil {
					write(p.request.ID, map[string]any{"code": -32603, "message": "encode computer result: " + encodeErr.Error()}, true)
				} else {
					content := []any{map[string]any{"type": "text", "text": strings.TrimSpace(r.Text + "\n" + string(metadata))}}
					if len(r.PNG) > 0 {
						content = append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(r.PNG)})
					}
					write(p.request.ID, map[string]any{"content": content, "structuredContent": r.Data, "isError": err != nil}, false)
				}
			} else {
				write(p.request.ID, r.Data, false)
			}
			p.cancel()
			mu.Lock()
			delete(calls, string(p.request.ID))
			mu.Unlock()
		}
	}()
	// Read independently so revocation interrupts held input and queued calls.
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 2*1024*1024)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "" {
				continue
			}
			var req request
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.Method == "" {
				write(nil, map[string]any{"code": -32700, "message": "request must be a JSON-RPC object"}, true)
				continue
			}
			if len(req.ID) == 0 {
				var n struct {
					RequestID json.RawMessage `json:"requestId"`
					Epoch     string          `json:"control_epoch"`
				}
				_ = json.Unmarshal(req.Params, &n)
				mu.Lock()
				if req.Method == "notifications/cancelled" {
					if p := calls[string(n.RequestID)]; p != nil {
						p.cancel()
					}
				}
				if req.Method == "notifications/wuu/control_revoked" && n.Epoch != "" {
					revoked[n.Epoch] = true
					for _, p := range calls {
						if p.args.Epoch == n.Epoch {
							p.cancel()
						}
					}
				}
				mu.Unlock()
				continue
			}
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			var cmd command
			if req.Method == "tools/call" && (params.Name != "computer" || json.Unmarshal(params.Arguments, &cmd) != nil) {
				write(req.ID, map[string]any{"code": -32602, "message": "expected computer tool arguments"}, true)
				continue
			}
			if req.Method != "tools/call" && req.Method != "initialize" && req.Method != "tools/list" && req.Method != "ping" {
				write(req.ID, map[string]any{"code": -32601, "message": "method not found"}, true)
				continue
			}
			callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			p := &pending{req, cmd, callCtx, cancel}
			mu.Lock()
			if revoked[cmd.Epoch] {
				cancel()
			}
			if closing || calls[string(req.ID)] != nil || len(calls) >= 64 {
				mu.Unlock()
				cancel()
				write(req.ID, map[string]any{"code": -32000, "message": "request queue unavailable or duplicate ID"}, true)
				continue
			}
			calls[string(req.ID)] = p
			queue <- p
			mu.Unlock()
		}
		readDone <- scanner.Err()
	}()
	var readErr error
	select {
	case readErr = <-readDone:
	case <-ctx.Done():
	}
	mu.Lock()
	closing = true
	for _, p := range calls {
		p.cancel()
	}
	// Closing and admission share the lock, so a late reader cannot enqueue
	// after shutdown even if stdin remains open after SIGTERM.
	close(queue)
	mu.Unlock()
	<-done
	return readErr
}

func handle(b *backend, p *pending) (result, error) {
	switch p.request.Method {
	case "initialize":
		var v struct {
			Version string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(p.request.Params, &v)
		switch v.Version {
		case "2026-06-30", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05":
		default:
			v.Version = "2025-11-25"
		}
		return result{Data: map[string]any{"protocolVersion": v.Version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "wuu-cua-linux", "version": "0.1.0"}}}, nil
	case "ping":
		return result{Data: map[string]any{}}, nil
	case "tools/list":
		return result{Data: map[string]any{"tools": []any{toolDefinition()}}}, nil
	default:
		if err := checkpoint(p.ctx); err != nil {
			return result{}, err
		}
		return b.perform(p.ctx, p.args)
	}
}

func checkpoint(ctx context.Context) error {
	if ctx.Err() != nil {
		return fail("cancelled", "control revoked, cancelled, or deadline exceeded; observe before retrying")
	}
	return nil
}

func toolDefinition() map[string]any {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	number := map[string]any{"type": "number"}
	props := map[string]any{
		"action":        enum("permission_status", "list_apps", "observe", "click", "drag", "type_text", "press_key", "press_keys", "scroll", "sequence"),
		"app":           str("Input requires the exact lifetime-bound linux:PID:XID:STARTTIME returned by observe/list_apps. Read-only observation and discovery also accept an unambiguous title, WM_CLASS, or linux:PID:XID selector. No application is launched."),
		"control_epoch": str("Runtime-owned control generation supplied by Wuu."),
		"snapshot_id":   str("Latest unconsumed observation ID; required for pointer coordinates. Every input consumes the target snapshot."),
		"mode":          enum("vision", "both", "ax"), "after": enum("vision", "both", "ax"),
		"foreground_policy": map[string]any{"type": "string", "enum": []string{"avoid", "allow", "require"}, "default": "avoid", "description": "Input requires allow or require: XTEST moves the real pointer and keyboard focus. avoid refuses all input. Observation is background unless require."},
		"coordinate_space":  enum("normalized", "screenshot"), "x": number, "y": number, "from_x": number, "from_y": number, "to_x": number, "to_y": number,
		"mouse_button": enum("left", "middle", "right"), "click_count": map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
		"duration":  map[string]any{"type": "number", "minimum": 0, "maximum": 10},
		"modifiers": map[string]any{"type": "array", "items": enum("shift", "control", "alt", "super")}, "key": str("X11 key or chord, such as ctrl+a, Return, Tab, Escape, Left, F5."),
		"keys":      map[string]any{"type": "array", "minItems": 1, "maxItems": 64, "items": str("Key or chord.")},
		"text":      str("Text representable by the active X11 keyboard layout. Unsupported characters fail before input; clipboard and keymap are never overwritten."),
		"direction": enum("up", "down", "left", "right"), "pages": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		"steps": map[string]any{"type": "array", "minItems": 1, "maxItems": 64, "items": map[string]any{"type": "object", "required": []string{"action", "risk"}, "properties": map[string]any{"action": str("Action"), "risk": enum("safe", "external_side_effect", "destructive"), "confirmed": map[string]any{"type": "boolean"}}, "additionalProperties": true}},
	}
	return map[string]any{"name": "computer", "title": "Computer Use for Linux (X11)", "description": "Observe exact X11 client windows using XComposite and control them with foreground XTEST input. Requires a local X11 session; Wayland/XWayland, background input, accessibility trees, arbitrary Unicode composition, app launch, and concealment are unsupported. list_apps returns PID/XID targets. observe returns target-only PNG pixels and snapshot_id; coordinates are screenshot pixels or a normalized 0–1000 grid. Input delivery does not establish its outcome. Use observe or after=vision to verify. sequence is orchestrated by Wuu's risk-aware host, not by the native helper.", "inputSchema": map[string]any{"type": "object", "required": []string{"action"}, "additionalProperties": false, "properties": props}, "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}}
}
