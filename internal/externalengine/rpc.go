package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("engine RPC error %d: %s", e.Code, e.Message) }

type rpcMessage struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpc runs one request at a time, processing notifications in wire order.
// In particular the prompt response cannot overtake its final text update.
type rpc struct {
	child  *child
	nextID int
	handle func(context.Context, string, json.RawMessage, bool) (any, error)
}

func (r *rpc) notify(ctx context.Context, method string, params any) error {
	return r.child.write(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (r *rpc) call(ctx context.Context, method string, params, result any) error {
	r.nextID++
	id := fmt.Sprintf("wuu-%d", r.nextID)
	if err := r.child.write(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f, ok := <-r.child.frames:
			if !ok {
				return errors.New("engine connection closed")
			}
			if f.err != nil {
				return f.err
			}
			var msg rpcMessage
			if err := json.Unmarshal(f.data, &msg); err != nil {
				return fmt.Errorf("invalid engine JSON: %w", err)
			}
			if msg.Version != "2.0" {
				return errors.New("engine did not send JSON-RPC 2.0")
			}
			if msg.Method != "" {
				isRequest := len(msg.ID) > 0 && string(msg.ID) != "null"
				value, err := r.handle(ctx, msg.Method, msg.Params, isRequest)
				if !isRequest {
					var settled *acpTurnSettled
					if errors.As(err, &settled) {
						return applyACPTurnSettled(result, settled)
					}
					if err != nil {
						return err
					}
					continue
				}
				reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
				if err != nil {
					rpcErr := &rpcError{Code: -32603, Message: err.Error()}
					_ = errors.As(err, &rpcErr)
					reply["error"] = rpcErr
				} else {
					reply["result"] = value
				}
				if err := r.child.write(ctx, reply); err != nil {
					return err
				}
				continue
			}
			if !rpcResponseIDMatches(msg.ID, id) {
				// A prompt can still be running when a stale or unsolicited
				// result arrives. Handshake calls must keep failing on a
				// desynced stream; the prompt wait must not.
				if method == "session/prompt" {
					continue
				}
				return errors.New("engine replied with an unexpected request ID")
			}
			if msg.Error != nil {
				return msg.Error
			}
			if len(msg.Result) == 0 {
				return errors.New("engine response has no result")
			}
			if result == nil {
				return nil
			}
			if err := json.Unmarshal(msg.Result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
			return nil
		}
	}
}

func rpcResponseIDMatches(raw json.RawMessage, want string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var responseID string
	if json.Unmarshal(raw, &responseID) != nil {
		return false
	}
	return responseID == want
}
