package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Unlike shared Server.callClient, this request has exactly one negotiated local
// connection. It is never broadcast, and another connection cannot answer it.
func (h *UserHost) callDesktopSession(ctx context.Context, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, _, err := clientauthority.Connection(ctx, h.runtime.UserBinding())
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	var selected *userHostConnection
	for _, c := range h.connections {
		c.mu.Lock()
		ready := c.desktopSessionReady
		c.mu.Unlock()
		candidate, _, admissionErr := clientauthority.Connection(c.ctx, h.runtime.UserBinding())
		if admissionErr != nil || candidate != connection || !ready || c.ctx.Err() != nil {
			continue
		}
		p, ok := principal.FromContext(c.ctx)
		if !ok || !p.IsOwner() || p.DurableAuthority() != "local-installation" {
			continue
		}
		if selected != nil {
			h.mu.Unlock()
			return nil, errors.New("multiple local desktop coordinators are connected; choose one before sending")
		}
		selected = c
	}
	if selected == nil || h.closed {
		h.mu.Unlock()
		return nil, errors.New("local desktop session coordinator is disconnected")
	}
	h.next++
	id := fmt.Sprintf("desktop-session-%d", h.next)
	h.mu.Unlock()
	c := selected
	if err := h.server.requireDesktopOwner(c.ctx); err != nil {
		return nil, err
	}
	ch := make(chan clientResponse, 1)
	c.mu.Lock()
	if c.desktopSessionCalls == nil {
		c.desktopSessionCalls = make(map[string]chan clientResponse)
	}
	c.desktopSessionCalls[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.desktopSessionCalls, id); c.mu.Unlock() }()
	rawID, _ := json.Marshal(id)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(Request{ID: rawID, Method: desktopSessionDispatchMethod, Params: rawParams})
	if err != nil {
		return nil, err
	}
	c.enqueueAuthorized(append(raw, '\n'), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return h.server.requireDesktopOwner(c.ctx)
	})
	timer := time.NewTimer(browserClientCallTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("desktop session outcome unknown after caller cancellation; reconcile the same request id: %w", ctx.Err())
	case <-c.ctx.Done():
		return nil, errors.New("desktop session outcome unknown after connection loss; reconcile the same request id")
	case <-timer.C:
		return nil, errors.New("desktop session outcome unknown after timeout; reconcile the same request id")
	case response := <-ch:
		if err := h.server.requireDesktopOwner(c.ctx); err != nil {
			return nil, err
		}
		if response.Error != nil {
			return nil, fmt.Errorf("desktop session coordinator: %s", response.Error.Message)
		}
		return response.Result, nil
	}
}
func (c *userHostConnection) deliverDesktopSessionResponse(raw []byte) bool {
	var response clientResponse
	if json.Unmarshal(raw, &response) != nil {
		return false
	}
	var id string
	if json.Unmarshal(response.ID, &id) != nil {
		return false
	}
	c.mu.Lock()
	ready := c.desktopSessionReady
	ch := c.desktopSessionCalls[id]
	delete(c.desktopSessionCalls, id)
	c.mu.Unlock()
	if !ready || ch == nil {
		return false
	}
	select {
	case ch <- response:
	default:
	}
	return true
}
