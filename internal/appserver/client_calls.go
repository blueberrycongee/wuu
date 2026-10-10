package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// browserClientCallTimeout bounds a single server-initiated request. It is kept
// well under Close's owned-shutdown drain (ownedShutdownDrainTimeout, 1 minute)
// so a wedged desktop peer surfaces as a per-call timeout rather than stalling
// shutdown. The single-threaded stdin scanner delivers replies, so a slow
// synchronous handler ahead of a browser reply in the queue can present as a
// false timeout; bridge callers word timeout errors to reflect that ambiguity.
const browserClientCallTimeout = 30 * time.Second

// callClient issues a negotiated server-initiated request to the connected
// client and blocks until the reply arrives, the context is cancelled, or the
// per-call timeout fires. It runs on turn/tool background goroutines, never on
// the stdin scanner goroutine, so blocking here does not freeze the RPC read loop.
//
// Deadlock discipline (reviewed as the critical correctness surface):
//   - The reply channel is buffered with capacity 1 so deliverClientResponse's
//     send never blocks even if this caller has already left on ctx/timeout.
//   - Every exit path re-acquires clientCallMu and deletes its own pending
//     entry first. That both prevents a map leak and guarantees a late
//     delivery can never find a channel to block on: once we delete, the sole
//     buffered slot belongs to nobody and the non-blocking send falls through.
func (s *Server) callClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s == nil {
		return nil, fmt.Errorf("app-server is required")
	}
	if !s.supportsClientMethod(method) {
		return nil, fmt.Errorf("%s unavailable: connected client did not advertise this reverse-RPC method", method)
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal %s params: %w", method, err)
	}

	s.clientCallMu.Lock()
	if s.closed.Load() {
		s.clientCallMu.Unlock()
		return nil, fmt.Errorf("%s unavailable: %w", method, errServerClosed)
	}
	s.clientCallSeq++
	id := "srv-" + strconv.FormatUint(s.clientCallSeq, 10)
	ch := make(chan clientResponse, 1)
	s.clientCalls[id] = ch
	s.clientCallMu.Unlock()

	// delete removes this call's pending entry under the lock. Safe to call on
	// every exit path even if deliverClientResponse already removed it: a
	// missing key is a no-op.
	delete := func() {
		s.clientCallMu.Lock()
		delete(s.clientCalls, id)
		s.clientCallMu.Unlock()
	}

	if err := s.writeJSONContext(ctx, Request{
		ID:     json.RawMessage(strconv.Quote(id)),
		Method: method,
		Params: json.RawMessage(payload),
	}); err != nil {
		delete()
		return nil, fmt.Errorf("send %s request: %w", method, err)
	}

	select {
	case <-ctx.Done():
		delete()
		return nil, ctx.Err()
	case <-time.After(browserClientCallTimeout):
		delete()
		return nil, fmt.Errorf("%s request timed out after %s (client unresponsive or protocol congestion)", method, browserClientCallTimeout)
	case r := <-ch:
		delete()
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	}
}

func (s *Server) setClientMethods(methods []string) {
	s.clientCallMu.Lock()
	defer s.clientCallMu.Unlock()
	s.clientMethods = make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method = strings.TrimSpace(method); method != "" {
			s.clientMethods[method] = struct{}{}
		}
	}
}

func (s *Server) supportsClientMethod(method string) bool {
	if s == nil {
		return false
	}
	s.clientCallMu.Lock()
	defer s.clientCallMu.Unlock()
	_, ok := s.clientMethods[method]
	return ok
}

func (s *Server) supportsBrowserClient() bool {
	for _, method := range []string{
		MethodBrowserCDP,
		MethodBrowserScreenshot,
		MethodBrowserOpenTab,
		MethodBrowserCloseTab,
		MethodBrowserSetVisibility,
		MethodBrowserListTabs,
	} {
		if !s.supportsClientMethod(method) {
			return false
		}
	}
	return true
}

// deliverClientResponse routes a client Response back to its waiting callClient.
// It runs on the single stdin scanner goroutine, so it must never block:
// lookup+delete happen under the lock, then the send is non-blocking. Unknown or
// late ids (the caller already left) are silently dropped.
func (s *Server) deliverClientResponse(raw []byte) {
	if s == nil {
		return
	}
	var r clientResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return
	}
	var key string
	if err := json.Unmarshal(r.ID, &key); err != nil || key == "" {
		return
	}
	s.clientCallMu.Lock()
	ch := s.clientCalls[key]
	delete(s.clientCalls, key)
	s.clientCallMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- r:
	default:
	}
}

// failPendingClientCalls releases every pending server-initiated call during
// Close. Delivery is non-blocking and entries are deleted as we go, so a caller
// that already left cannot make this stall — Close must never block draining a
// full channel or the process can hang forever.
func (s *Server) failPendingClientCalls() {
	if s == nil {
		return
	}
	s.clientCallMu.Lock()
	defer s.clientCallMu.Unlock()
	for id, ch := range s.clientCalls {
		if ch != nil {
			select {
			case ch <- clientResponse{Error: &ResponseError{Code: "closed", Message: "app-server is shutting down"}}:
			default:
			}
		}
		delete(s.clientCalls, id)
	}
}
