package cualinux

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
)

func (b *backend) perform(ctx context.Context, cmd command) (result, error) {
	if cmd.Action == "permission_status" {
		err := b.connect()
		data := map[string]any{"platform": "linux", "backend": "x11", "supported": err == nil, "accessibility_supported": false, "background_input_supported": false, "wayland_supported": false}
		if err != nil {
			data["reason"] = err.Error()
			return result{Text: err.Error(), Data: data}, nil
		}
		data["capture_supported"] = b.composite
		data["input_supported"] = b.xtest
		data["keyboard_supported"] = b.xtest && b.xkb != 0
		return result{Text: "Connected to X11. Target capture requires XComposite; input requires XTEST and explicit foreground permission.", Data: data}, nil
	}
	if err := b.connect(); err != nil {
		return result{}, err
	}
	if cmd.Action == "list_apps" {
		target := strings.TrimSpace(cmd.App)
		if strings.HasPrefix(target, "linux:") && len(strings.Split(target, ":")) == 4 {
			// The host resolves its lifetime-bound target before every action.
			// Validate that window directly instead of scanning unrelated apps.
			w, e := b.resolve(target)
			if e != nil {
				return result{}, e
			}
			return result{Text: "Resolved the selected X11 window.", Data: map[string]any{"apps": []window{w}, "resolved_app": w}}, nil
		}
		apps, e := b.list()
		if e != nil {
			return result{}, e
		}
		data := map[string]any{"apps": apps}
		if strings.TrimSpace(cmd.App) != "" {
			w, e := b.resolve(cmd.App)
			if e != nil {
				return result{}, e
			}
			data["resolved_app"] = w
		}
		return result{Text: fmt.Sprintf("%d running X11 windows. Use an exact linux:PID:XID target.", len(apps)), Data: data}, nil
	}
	switch cmd.Action {
	case "observe", "click", "drag", "type_text", "press_key", "press_keys", "scroll":
	default:
		return result{}, fail("unsupported_action", "unsupported native action %q; sequences require Wuu host orchestration", cmd.Action)
	}
	if strings.TrimSpace(cmd.App) == "" || strings.TrimSpace(cmd.Epoch) == "" {
		return result{}, fail("invalid_arguments", "app and runtime control_epoch are required")
	}
	if cmd.Mode != "" && cmd.Mode != "vision" && cmd.Mode != "both" && cmd.Mode != "ax" || cmd.After != "" && cmd.After != "vision" && cmd.After != "both" && cmd.After != "ax" {
		return result{}, fail("invalid_arguments", "invalid observation mode")
	}
	if cmd.Mode == "ax" || cmd.After == "ax" {
		return result{}, fail("unsupported_action", "Linux X11 helper has no accessibility-tree mode; use vision-capable models")
	}
	if cmd.Foreground == "" {
		cmd.Foreground = "avoid"
	}
	if cmd.Foreground != "avoid" && cmd.Foreground != "allow" && cmd.Foreground != "require" {
		return result{}, fail("invalid_arguments", "invalid foreground_policy")
	}
	w, err := b.resolve(cmd.App)
	if err != nil {
		return result{}, err
	}
	if cmd.Action != "observe" && strings.TrimSpace(cmd.App) != w.ID {
		return result{}, fail("invalid_arguments", "input requires the exact lifetime-bound app ID returned by observe or list_apps; resolve the target first")
	}
	if w.Hidden {
		return result{}, fail("target_hidden", "target is minimized or hidden; show it before observing or controlling it")
	}
	if cmd.Action != "observe" && cmd.Foreground == "avoid" {
		return result{}, fail("requires_foreground", "XTEST input needs foreground_policy=allow or require; background control is unsupported")
	}
	if math.IsNaN(cmd.Duration) || math.IsInf(cmd.Duration, 0) || cmd.Duration < 0 || cmd.Duration > 10 {
		return result{}, fail("invalid_arguments", "duration must be between 0 and 10 seconds")
	}
	var snap snapshot
	pointer := cmd.Action == "click" || cmd.Action == "drag" || cmd.X != nil || cmd.Y != nil
	if cmd.Action != "observe" && (pointer || cmd.SnapshotID != "") {
		var ok bool
		snap, ok = b.snapshots[w.ID]
		if !ok || cmd.SnapshotID == "" || snap.id != cmd.SnapshotID || snap.epoch != cmd.Epoch || !sameIdentity(snap.target, w) || !sameGeometry(snap.target, w) || time.Since(snap.created) > 2*time.Minute {
			return result{}, fail("stale_snapshot", "snapshot is missing, consumed, replaced, expired, or target/control ownership changed; observe again")
		}
	}
	if cmd.Action == "observe" {
		changed := false
		if cmd.Foreground == "require" {
			changed, err = b.activate(ctx, w)
			if err != nil {
				return result{}, err
			}
		}
		r, e := b.observe(ctx, w, cmd)
		if e == nil && changed {
			r.Data["foreground_changed"] = true
			r.Data["mechanism"] = "foreground_native"
		}
		return r, e
	}
	if !b.xtest {
		return result{}, fail("unsupported_input", "XTEST extension is unavailable")
	}
	// Validate and prepare all input before changing focus or consuming a ticket.
	plan, err := b.plan(w, snap, cmd)
	if err != nil {
		return result{}, err
	}
	if err = checkpoint(ctx); err != nil {
		return result{}, err
	}
	delete(b.snapshots, w.ID)
	changed, err := b.activate(ctx, w)
	if err != nil {
		return result{}, err
	}
	data := map[string]any{"app": w.ID, "process_id": w.PID, "window_id": uint32(w.XID), "mechanism": "foreground_native", "foreground_changed": changed, "delivery": "not_delivered", "verification": "not_requested"}
	delivered, err := b.execute(ctx, w, plan)
	if delivered > 0 {
		if err != nil {
			data["delivery"] = "unknown"
		} else {
			data["delivery"] = "delivered"
		}
	}
	data["input_units_attempted"] = delivered
	if err != nil {
		return result{Data: data}, err
	}
	if plan.interaction != nil {
		data["interaction"] = plan.interaction
	}
	if cmd.After != "" {
		latest, e := b.validate(w)
		if e != nil {
			return result{Data: data}, e
		}
		observed, e := b.observe(ctx, latest, cmd)
		if e != nil {
			return result{Data: data}, e
		}
		for k, v := range data {
			observed.Data[k] = v
		}
		return observed, nil
	}
	return result{Text: "Input delivered to the verified foreground window. Observe to verify the outcome.", Data: data}, nil
}

func (b *backend) hasFocus(w window) bool {
	focus, e := xproto.GetInputFocus(b.conn).Reply()
	return e == nil && b.descendant(focus.Focus, w.XID)
}
func (b *backend) activate(ctx context.Context, w window) (bool, error) {
	if _, err := b.validate(w); err != nil {
		return false, err
	}
	if b.hasFocus(w) {
		return false, nil
	}
	if err := checkpoint(ctx); err != nil {
		return false, err
	}
	wm := b.propertyWords(b.screen.Root, "_NET_SUPPORTING_WM_CHECK")
	if len(wm) > 0 {
		event := xproto.ClientMessageEvent{Format: 32, Window: w.XID, Type: b.atom("_NET_ACTIVE_WINDOW"), Data: xproto.ClientMessageDataUnionData32New([]uint32{2, 0, 0, 0, 0})}
		if err := xproto.SendEventChecked(b.conn, false, b.screen.Root, xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify, string(event.Bytes())).Check(); err != nil {
			return false, err
		}
	} else {
		if err := xproto.ConfigureWindowChecked(b.conn, w.XID, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove}).Check(); err != nil {
			return false, err
		}
		if err := xproto.SetInputFocusChecked(b.conn, xproto.InputFocusPointerRoot, w.XID, xproto.TimeCurrentTime).Check(); err != nil {
			return false, err
		}
	}
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if b.hasFocus(w) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, checkpoint(ctx)
		case <-deadline.C:
			return true, fail("requires_foreground", "window manager did not focus the exact target; no input delivered")
		case <-poll.C:
		}
	}
}
