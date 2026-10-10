package appserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// browserBridge implements tools.BrowserBridge by translating each call into a
// server-initiated browser/* request over Server.callClient. One bridge is
// bound per thread so its workdir + threadID scope every request to the tabs
// owned by that (workdir, thread) — the desktop host keys its WebContentsView
// registry by (workdir, tab_id).
type browserBridge struct {
	srv      *Server
	workdir  string
	threadID string
}

// browserBridgeForThread returns the per-thread bridge injected into the
// thread's toolkit. It is wired at thread-runtime creation (ensureThreadRuntime)
// alongside the other per-thread toolkit dependencies.
func (s *Server) browserBridgeForThread(workdir, threadID string) tools.BrowserBridge {
	return &browserBridge{srv: s, workdir: workdir, threadID: threadID}
}

func (b *browserBridge) unavailable() error {
	if b == nil || b.srv == nil {
		return errors.New("embedded browser backend is unavailable")
	}
	return nil
}

// Call stamps the bridge-owned task identity on every browser request, including
// optional actions invoked through the generic bridge rather than typed helpers.
func (b *browserBridge) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := b.unavailable(); err != nil {
		return nil, err
	}
	if err := activity.ControlErr(ctx); err != nil {
		return nil, err
	}
	if b.threadID == "" {
		return nil, errors.New("browser request requires a thread context")
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var scoped map[string]json.RawMessage
	if err := json.Unmarshal(payload, &scoped); err != nil || scoped == nil {
		return nil, errors.New("browser request params must be an object")
	}
	requestID := rand.Text()
	scoped["workdir"], _ = json.Marshal(b.workdir)
	scoped["thread_id"], _ = json.Marshal(b.threadID)
	scoped["request_id"], _ = json.Marshal(requestID)
	result, err := b.srv.callClient(ctx, method, scoped)
	if err != nil {
		// A late desktop command must not outlive a cancelled or timed-out
		// reverse RPC. The opaque ID targets only this request, not a new turn.
		_ = b.srv.writeNotification(NotificationBrowserRequestCancelled, BrowserRequestCancelledParams{
			Workdir: b.workdir, ThreadID: b.threadID, RequestID: requestID,
		})
	}
	return result, err
}

func (b *browserBridge) Screenshot(ctx context.Context, tabID, destPath, format string) (tools.BrowserScreenshotResult, error) {
	if err := b.unavailable(); err != nil {
		return tools.BrowserScreenshotResult{}, err
	}
	raw, err := b.Call(ctx, MethodBrowserScreenshot, BrowserScreenshotParams{
		Workdir:  b.workdir,
		TabID:    tabID,
		DestPath: destPath,
		Format:   format,
	})
	if err != nil {
		return tools.BrowserScreenshotResult{}, err
	}
	var res BrowserScreenshotResult
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &res); err != nil {
			return tools.BrowserScreenshotResult{}, fmt.Errorf("decode screenshot result: %w", err)
		}
	}
	return tools.BrowserScreenshotResult{Width: res.Width, Height: res.Height, Path: res.Path, ViewportWidth: res.ViewportWidth, ViewportHeight: res.ViewportHeight}, nil
}

func (b *browserBridge) OpenTab(ctx context.Context, tabID, url string) error {
	if err := b.unavailable(); err != nil {
		return err
	}
	_, err := b.Call(ctx, MethodBrowserOpenTab, BrowserOpenTabParams{
		Workdir:    b.workdir,
		TabID:      tabID,
		InitialURL: url,
	})
	return err
}

func (b *browserBridge) CloseTab(ctx context.Context, tabID string) error {
	if err := b.unavailable(); err != nil {
		return err
	}
	_, err := b.Call(ctx, MethodBrowserCloseTab, BrowserCloseTabParams{
		Workdir: b.workdir,
		TabID:   tabID,
	})
	return err
}

func (b *browserBridge) SetVisibility(ctx context.Context, tabID string, visible bool) error {
	if err := b.unavailable(); err != nil {
		return err
	}
	_, err := b.Call(ctx, MethodBrowserSetVisibility, BrowserSetVisibilityParams{
		Workdir: b.workdir,
		TabID:   tabID,
		Visible: visible,
	})
	return err
}

func (b *browserBridge) ListTabs(ctx context.Context) ([]tools.BrowserLiveTab, error) {
	if err := b.unavailable(); err != nil {
		return nil, err
	}
	raw, err := b.Call(ctx, MethodBrowserListTabs, BrowserListTabsParams{Workdir: b.workdir})
	if err != nil {
		return nil, err
	}
	var res BrowserListTabsResult
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("decode list_tabs result: %w", err)
		}
	}
	if len(res.Tabs) > 0 {
		out := make([]tools.BrowserLiveTab, 0, len(res.Tabs))
		for _, tab := range res.Tabs {
			if tab.TabID == "" {
				continue
			}
			out = append(out, tools.BrowserLiveTab{ID: tab.TabID, URL: tab.URL, Title: tab.Title})
		}
		return out, nil
	}
	out := make([]tools.BrowserLiveTab, 0, len(res.TabIDs))
	for _, id := range res.TabIDs {
		if id == "" {
			continue
		}
		out = append(out, tools.BrowserLiveTab{ID: id})
	}
	return out, nil
}

// stopBrowserActivitiesAndEmit stops every browser-kind activity this process
// owns. Close calls it while the activity subscription is still attached so the
// EventStopped for each tab reaches the desktop and its hidden view is torn
// down; the errors here (a session that raced to StateStopped) are benign during
// shutdown.
func (s *Server) stopBrowserActivitiesAndEmit() {
	if s == nil || s.rt == nil || s.rt.ActivityRegistry == nil {
		return
	}
	registry := s.rt.ActivityRegistry
	for _, sess := range registry.ListByKind(activity.KindBrowser) {
		_, _ = registry.Stop(sess.ThreadID, sess.ID)
	}
}
