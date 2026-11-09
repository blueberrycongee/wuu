package cualinux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/res"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type window struct {
	ID              string        `json:"id"`
	Title           string        `json:"displayName"`
	Class           string        `json:"class"`
	PID             int           `json:"process_id"`
	XID             xproto.Window `json:"window_id"`
	Width           int           `json:"width"`
	Height          int           `json:"height"`
	Hidden          bool          `json:"hidden"`
	start, identity string
	x, y, border    int
	visual          xproto.Visualid
	depth           byte
}

type snapshot struct {
	id, epoch     string
	target        window
	width, height int
	created       time.Time
}
type backend struct {
	conn                  *xgb.Conn
	screen                *xproto.ScreenInfo
	atoms                 map[string]xproto.Atom
	composite, xtest, res bool
	xkb                   byte
	snapshots             map[string]snapshot
}

func sessionError() error {
	if strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || os.Getenv("WAYLAND_DISPLAY") != "" {
		return fail("unsupported_session", "native Wayland and XWayland are not supported; use an X11 desktop session. No root/uinput fallback is used")
	}
	if os.Getenv("DISPLAY") == "" {
		return fail("unsupported_session", "DISPLAY is unset; a local X11 desktop session is required")
	}
	return nil
}
func (b *backend) connect() error {
	if err := sessionError(); err != nil {
		return err
	}
	if b.conn != nil {
		return nil
	}
	c, err := xgb.NewConn()
	if err != nil {
		return fail("permission_denied", "cannot connect to the X11 display: %v", err)
	}
	b.conn = c
	b.screen = xproto.Setup(c).DefaultScreen(c)
	b.atoms = make(map[string]xproto.Atom)
	if composite.Init(c) == nil {
		v, e := composite.QueryVersion(c, 0, 4).Reply()
		b.composite = e == nil && (v.MajorVersion > 0 || v.MinorVersion >= 2)
	}
	b.xtest = xtest.Init(c) == nil
	b.initKeyboardState()
	if res.Init(c) == nil {
		v, e := res.QueryVersion(c, 1, 2).Reply()
		b.res = e == nil && (v.ServerMajor > 1 || v.ServerMajor == 1 && v.ServerMinor >= 2)
	}
	return nil
}
func (b *backend) close() {
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
}
func (b *backend) atom(name string) xproto.Atom {
	if a, ok := b.atoms[name]; ok {
		return a
	}
	r, err := xproto.InternAtom(b.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	b.atoms[name] = r.Atom
	return r.Atom
}
func (b *backend) property(w xproto.Window, name string) (*xproto.GetPropertyReply, error) {
	return xproto.GetProperty(b.conn, false, w, b.atom(name), xproto.GetPropertyTypeAny, 0, 65536).Reply()
}
func (b *backend) propertyText(w xproto.Window, name string) string {
	r, e := b.property(w, name)
	if e != nil || r.Format != 8 {
		return ""
	}
	return strings.TrimRight(string(r.Value), "\x00")
}
func (b *backend) propertyWords(w xproto.Window, name string) []uint32 {
	r, e := b.property(w, name)
	if e != nil || r.Format != 32 {
		return nil
	}
	out := make([]uint32, len(r.Value)/4)
	for i := range out {
		out[i] = xgb.Get32(r.Value[i*4:])
	}
	return out
}
func processStart(pid int) (string, error) {
	data, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	// comm can contain spaces and parentheses; starttime is field 22.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return "", fmt.Errorf("incomplete process stat")
	}
	return fields[19], nil
}
func (b *backend) window(w xproto.Window) (window, error) {
	attrs, e := xproto.GetWindowAttributes(b.conn, w).Reply()
	if e != nil {
		return window{}, fail("target_gone", "window %d is unavailable", w)
	}
	geom, e := xproto.GetGeometry(b.conn, xproto.Drawable(w)).Reply()
	if e != nil {
		return window{}, e
	}
	pid := 0
	if words := b.propertyWords(w, "_NET_WM_PID"); len(words) > 0 {
		pid = int(words[0])
	}
	if b.res {
		reply, err := res.QueryClientIds(b.conn, 1, []res.ClientIdSpec{{Client: uint32(w), Mask: res.ClientIdMaskLocalClientPID}}).Reply()
		if err == nil {
			for _, id := range reply.Ids {
				if id.Spec.Mask&res.ClientIdMaskLocalClientPID != 0 && len(id.Value) > 0 && id.Value[0] > 0 {
					actual := int(id.Value[0])
					if pid != 0 && pid != actual {
						return window{}, fail("target_identity_mismatch", "window PID property does not match its local X11 client")
					}
					pid = actual
				}
			}
		}
	}
	if pid <= 0 {
		return window{}, fail("unsupported_target", "window %d has no verifiable local process identity", w)
	}
	start, e := processStart(pid)
	if e != nil {
		return window{}, fail("target_gone", "window process %d is unavailable locally", pid)
	}
	pos, e := xproto.TranslateCoordinates(b.conn, w, b.screen.Root, 0, 0).Reply()
	if e != nil {
		return window{}, e
	}
	class := b.propertyText(w, "WM_CLASS")
	title := b.propertyText(w, "_NET_WM_NAME")
	if title == "" {
		title = b.propertyText(w, "WM_NAME")
	}
	hidden := attrs.MapState != xproto.MapStateViewable
	for _, state := range b.propertyWords(w, "_NET_WM_STATE") {
		if xproto.Atom(state) == b.atom("_NET_WM_STATE_HIDDEN") {
			hidden = true
		}
	}
	return window{ID: fmt.Sprintf("linux:%d:%d:%s", pid, w, start), Title: title, Class: strings.ReplaceAll(class, "\x00", " / "), PID: pid, XID: w, Width: int(geom.Width), Height: int(geom.Height), Hidden: hidden, start: start, identity: class, x: int(pos.DstX), y: int(pos.DstY), border: int(geom.BorderWidth), visual: attrs.Visual, depth: geom.Depth}, nil
}
func sameIdentity(a, b window) bool {
	return a.XID == b.XID && a.PID == b.PID && a.start == b.start && a.identity == b.identity
}
func sameGeometry(a, b window) bool {
	return a.Width == b.Width && a.Height == b.Height && a.x == b.x && a.y == b.y && a.border == b.border
}
func (b *backend) validate(w window) (window, error) {
	fresh, err := b.window(w.XID)
	if err != nil {
		return window{}, err
	}
	if !sameIdentity(w, fresh) {
		return window{}, fail("target_identity_mismatch", "window identity changed; list apps and observe again")
	}
	if fresh.Hidden {
		return fresh, fail("target_hidden", "target window is hidden or minimized")
	}
	return fresh, nil
}
func (b *backend) list() ([]window, error) {
	ids := b.propertyWords(b.screen.Root, "_NET_CLIENT_LIST_STACKING")
	if len(ids) == 0 {
		ids = b.propertyWords(b.screen.Root, "_NET_CLIENT_LIST")
	}
	if len(ids) == 0 {
		// Non-EWMH window managers and bare Xvfb still expose ordinary client windows.
		todo := []xproto.Window{b.screen.Root}
		for len(todo) > 0 && len(ids) < 2048 {
			w := todo[0]
			todo = todo[1:]
			tree, e := xproto.QueryTree(b.conn, w).Reply()
			if e != nil {
				continue
			}
			for _, child := range tree.Children {
				if len(b.propertyWords(child, "_NET_WM_PID")) > 0 || b.propertyText(child, "WM_CLASS") != "" {
					ids = append(ids, uint32(child))
				} else if len(todo) < 2048 {
					todo = append(todo, child)
				}
			}
		}
	}
	out := make([]window, 0, len(ids))
	seen := make(map[uint32]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if w, e := b.window(xproto.Window(id)); e == nil {
			out = append(out, w)
		}
	}
	return out, nil
}
func (b *backend) resolve(target string) (window, error) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "linux:") {
		parts := strings.Split(target, ":")
		if len(parts) != 3 && len(parts) != 4 {
			return window{}, fail("invalid_arguments", "invalid linux:PID:XID target")
		}
		pid, e := strconv.Atoi(parts[1])
		id, e2 := strconv.ParseUint(parts[2], 10, 32)
		if e != nil || e2 != nil || pid <= 0 || id == 0 {
			return window{}, fail("invalid_arguments", "invalid linux:PID:XID target")
		}
		w, e := b.window(xproto.Window(id))
		if e != nil {
			return window{}, e
		}
		if w.PID != pid || len(parts) == 4 && w.start != parts[3] {
			return window{}, fail("target_identity_mismatch", "target PID does not own the requested X11 window")
		}
		return w, nil
	}
	apps, e := b.list()
	if e != nil {
		return window{}, e
	}
	matches := []window{}
	for _, w := range apps {
		match := strings.EqualFold(target, w.Title) || strings.EqualFold(target, w.Class)
		for _, class := range strings.Split(w.identity, "\x00") {
			match = match || strings.EqualFold(target, class)
		}
		if match {
			matches = append(matches, w)
		}
	}
	if len(matches) > 1 {
		return window{}, fail("ambiguous_target", "%q matches multiple windows; choose an exact linux:PID:XID from list_apps", target)
	}
	if len(matches) == 0 {
		return window{}, fail("app_not_found", "no running X11 window matches %q", target)
	}
	return matches[0], nil
}
func (b *backend) descendant(child, parent xproto.Window) bool {
	for i := 0; i < 64 && child != 0 && child != b.screen.Root; i++ {
		if child == parent {
			return true
		}
		tree, e := xproto.QueryTree(b.conn, child).Reply()
		if e != nil {
			return false
		}
		child = tree.Parent
	}
	return false
}

func runWatcher(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if (len(args) != 3 && len(args) != 4) || args[0] != "--watch-window" && args[0] != "--inspect-window" {
		return fmt.Errorf("usage: wuu-cua-linux [--watch-window|--inspect-window XID PID [STARTTIME]]")
	}
	id, e := strconv.ParseUint(args[1], 10, 32)
	pid, e2 := strconv.Atoi(args[2])
	if e != nil || e2 != nil || id == 0 || pid <= 0 {
		return fmt.Errorf("invalid window/PID identity")
	}
	b := &backend{}
	defer b.close()
	if e := b.connect(); e != nil {
		return e
	}
	encoder := json.NewEncoder(output)

	if args[0] == "--watch-window" {
		if e := xproto.ChangeWindowAttributesChecked(b.conn, xproto.Window(id), xproto.CwEventMask, []uint32{xproto.EventMaskStructureNotify | xproto.EventMaskPropertyChange}).Check(); e != nil {
			return encoder.Encode(map[string]any{"status": "gone", "message": "window disappeared before watch"})
		}
	}
	initial, e := b.window(xproto.Window(id))
	if e != nil || initial.PID != pid || len(args) == 4 && initial.start != args[3] {
		return encoder.Encode(map[string]any{"status": "gone", "message": "window/PID is unavailable or mismatched"})
	}
	inspect := func() (string, error) {
		w, e := b.window(initial.XID)
		status := "ready"
		message := ""
		if e != nil || !sameIdentity(w, initial) {
			status = "gone"
			message = "window closed or identity changed"
		} else if w.Hidden {
			status = "hidden"
		}
		return status, encoder.Encode(map[string]any{"status": status, "width": w.Width, "height": w.Height, "window_id": uint32(initial.XID), "process_id": initial.PID, "message": message})
	}
	status, e := inspect()
	if e != nil || status == "gone" || args[0] == "--inspect-window" {
		return e
	}
	eof := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, input); close(eof) }()
	// Metadata-only polling supplements events: /proc lifetimes may change without
	// an event. Pixel capture remains a continuous Electron desktop stream.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := initial
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-eof:
			return nil
		case <-ticker.C:
		}
		for {
			event, err := b.conn.PollForEvent()
			if err != nil {
				return err
			}
			if event == nil {
				break
			}
			if _, gone := event.(xproto.DestroyNotifyEvent); gone {
				return encoder.Encode(map[string]any{"status": "gone", "message": "window destroyed"})
			}
		}
		w, err := b.window(initial.XID)
		if err != nil || !sameIdentity(initial, w) {
			_, err = inspect()
			return err
		}
		if w.Hidden != last.Hidden || !sameGeometry(w, last) {
			if _, err := inspect(); err != nil {
				return err
			}
			last = w
		}
	}
}
