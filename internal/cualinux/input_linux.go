package cualinux

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type keyStroke struct{ codes []byte }
type inputPlan struct {
	action         string
	x, y, toX, toY int
	button         byte
	count          int
	duration       time.Duration
	keys           []keyStroke
	modifiers      []byte
	mapping        []xproto.Keysym
	modifierMasks  map[byte]byte
	numLockMask    byte
	interaction    map[string]any
}

func (b *backend) keyboardMapping() (*xproto.GetKeyboardMappingReply, error) {
	setup := xproto.Setup(b.conn)
	return xproto.GetKeyboardMapping(b.conn, setup.MinKeycode, byte(int(setup.MaxKeycode)-int(setup.MinKeycode)+1)).Reply()
}
func (b *backend) plan(w window, s snapshot, cmd command) (inputPlan, error) {
	p := inputPlan{action: cmd.Action, button: 1, count: 1, duration: time.Duration(cmd.Duration * float64(time.Second))}
	if cmd.Button != "" {
		switch cmd.Button {
		case "left":
			p.button = 1
		case "middle":
			p.button = 2
		case "right":
			p.button = 3
		default:
			return p, fail("invalid_arguments", "invalid mouse_button")
		}
	}
	if cmd.Count != 0 {
		p.count = cmd.Count
	}
	if p.count < 1 || p.count > 3 {
		return p, fail("invalid_arguments", "click_count must be between 1 and 3")
	}
	pointer := cmd.Action == "click" || cmd.Action == "drag" || cmd.Action == "scroll"
	if pointer {
		x, y := cmd.X, cmd.Y
		if cmd.Action == "drag" {
			x, y = cmd.FromX, cmd.FromY
			if x == nil {
				x = cmd.X
			}
			if y == nil {
				y = cmd.Y
			}
		}
		if cmd.Action == "scroll" && x == nil && y == nil {
			p.x, p.y = w.x+w.Width/2, w.y+w.Height/2
		} else {
			var err error
			p.x, p.y, err = point(w, s, cmd.CoordinateSpace, x, y)
			if err != nil {
				return p, err
			}
		}
		p.interaction = map[string]any{"kind": cmd.Action, "x": float64(p.x-w.x) / float64(w.Width), "y": float64(p.y-w.y) / float64(w.Height)}
		if cmd.Action == "drag" {
			var err error
			p.toX, p.toY, err = point(w, s, cmd.CoordinateSpace, cmd.ToX, cmd.ToY)
			if err != nil {
				return p, err
			}
			p.interaction["to_x"] = float64(p.toX-w.x) / float64(w.Width)
			p.interaction["to_y"] = float64(p.toY-w.y) / float64(w.Height)
			if p.duration == 0 {
				p.duration = 400 * time.Millisecond
			}
		}
		if cmd.Action == "scroll" {
			switch cmd.Direction {
			case "up":
				p.button = 4
			case "down":
				p.button = 5
			case "left":
				p.button = 6
			case "right":
				p.button = 7
			default:
				return p, fail("invalid_arguments", "scroll requires direction up, down, left, or right")
			}
			pages := cmd.Pages
			if pages == 0 {
				pages = 1
			}
			if pages < 1 || pages > 20 {
				return p, fail("invalid_arguments", "pages must be between 1 and 20")
			}
			p.count = pages * 5
		}
	}
	needsKeys := cmd.Action == "type_text" || cmd.Action == "press_key" || cmd.Action == "press_keys" || len(cmd.Modifiers) > 0
	if needsKeys {
		mapping, err := b.keyboardMapping()
		if err != nil {
			return p, err
		}
		p.mapping = append([]xproto.Keysym(nil), mapping.Keysyms...)
		mods, err := xproto.GetModifierMapping(b.conn).Reply()
		if err != nil {
			return p, err
		}
		p.modifierMasks = make(map[byte]byte)
		for i, code := range mods.Keycodes {
			if code == 0 {
				continue
			}
			mask := byte(1 << uint(i/int(mods.KeycodesPerModifier)))
			p.modifierMasks[byte(code)] |= mask
			offset := (int(code) - int(xproto.Setup(b.conn).MinKeycode)) * int(mapping.KeysymsPerKeycode)
			if offset >= 0 && offset < len(mapping.Keysyms) && mapping.Keysyms[offset] == 0xff7f {
				p.numLockMask |= mask
			}
		}
		if err = b.keyboardState(p, nil); err != nil {
			return p, err
		}

		find := func(sym uint32) ([]byte, error) {
			n := int(mapping.KeysymsPerKeycode)
			minCode := int(xproto.Setup(b.conn).MinKeycode)
			for i := 0; i < len(mapping.Keysyms); i += n {
				for col := 0; col < min(n, 2); col++ {
					candidate := uint32(mapping.Keysyms[i+col])
					if col == 1 && candidate == 0 && mapping.Keysyms[i] >= 0x61 && mapping.Keysyms[i] <= 0x7a {
						candidate = uint32(unicode.ToUpper(rune(mapping.Keysyms[i])))
					}
					if candidate == sym {
						key := byte(minCode + i/n)
						if col == 0 {
							return []byte{key}, nil
						}
						for j := 0; j < len(mapping.Keysyms); j += n {
							if mapping.Keysyms[j] == 0xffe1 {
								return []byte{byte(minCode + j/n), key}, nil
							}
						}
					}
				}
			}
			return nil, fail("unsupported_input", "keysym U+%04X is unavailable in the active primary keyboard layout; no text was typed", sym)
		}
		for _, mod := range cmd.Modifiers {
			sym, ok := modifierKeysym(mod)
			if !ok {
				return p, fail("invalid_arguments", "unsupported modifier %q", mod)
			}
			codes, err := find(sym)
			if err != nil {
				return p, err
			}
			p.modifiers = appendUnique(p.modifiers, codes...)
		}
		chord := func(key string) (keyStroke, error) {
			parts := strings.Split(key, "+")
			codes := []byte{}
			for i, part := range parts {
				var sym uint32
				var ok bool
				if i < len(parts)-1 {
					sym, ok = modifierKeysym(part)
				} else {
					sym, ok = keySymbol(part)
				}
				if !ok {
					return keyStroke{}, fail("invalid_arguments", "unsupported key name %q", part)
				}
				found, err := find(sym)
				if err != nil {
					return keyStroke{}, err
				}
				codes = appendUnique(codes, found...)
			}
			return keyStroke{codes: codes}, nil
		}
		switch cmd.Action {
		case "type_text":
			if cmd.Text == "" || len([]rune(cmd.Text)) > 4096 {
				return p, fail("invalid_arguments", "type_text requires 1–4096 characters")
			}
			for _, r := range cmd.Text {
				sym := uint32(r)
				switch r {
				case '\n':
					sym = 0xff0d
				case '\t':
					sym = 0xff09
				default:
					if r < 32 || r == 127 {
						return p, fail("invalid_arguments", "text contains unsupported control characters")
					}
					if r > 255 {
						sym = 0x01000000 | uint32(r)
					}
				}
				codes, e := find(sym)
				if e != nil {
					return p, e
				}
				p.keys = append(p.keys, keyStroke{codes: codes})
			}
			p.interaction = map[string]any{"kind": "type", "x": 0.5, "y": 0.5}
		case "press_key":
			if cmd.Key == "" {
				return p, fail("invalid_arguments", "press_key requires key")
			}
			key, e := chord(cmd.Key)
			if e != nil {
				return p, e
			}
			p.keys = []keyStroke{key}
		case "press_keys":
			if len(cmd.Keys) == 0 || len(cmd.Keys) > 64 {
				return p, fail("invalid_arguments", "press_keys requires 1–64 keys")
			}
			for _, key := range cmd.Keys {
				stroke, e := chord(key)
				if e != nil {
					return p, e
				}
				p.keys = append(p.keys, stroke)
			}
		}
	}
	return p, nil
}
func appendUnique(codes []byte, more ...byte) []byte {
	for _, code := range more {
		if !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	return codes
}
func modifierKeysym(name string) (uint32, bool) {
	switch strings.ToLower(name) {
	case "shift":
		return 0xffe1, true
	case "ctrl", "control":
		return 0xffe3, true
	case "alt", "option":
		return 0xffe9, true
	case "super", "meta", "win", "command":
		return 0xffeb, true
	}
	return 0, false
}
func keySymbol(name string) (uint32, bool) {
	if sym, ok := modifierKeysym(name); ok {
		return sym, true
	}
	names := map[string]uint32{"return": 0xff0d, "enter": 0xff0d, "tab": 0xff09, "escape": 0xff1b, "esc": 0xff1b, "backspace": 0xff08, "delete": 0xffff, "space": 0x20, "plus": 0x2b, "minus": 0x2d, "left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54, "home": 0xff50, "end": 0xff57, "pageup": 0xff55, "page_up": 0xff55, "pagedown": 0xff56, "page_down": 0xff56, "insert": 0xff63}
	if sym, ok := names[strings.ToLower(name)]; ok {
		return sym, true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "f") {
		n, e := strconv.Atoi(lower[1:])
		if e == nil && n >= 1 && n <= 24 {
			return uint32(0xffbd + n), true
		}
	}
	runes := []rune(name)
	if len(runes) == 1 {
		r := runes[0]
		if r >= 32 && r <= 255 {
			return uint32(r), true
		}
		if r > 255 {
			return 0x01000000 | uint32(r), true
		}
	}
	return 0, false
}
func point(w window, s snapshot, space string, x, y *float64) (int, int, error) {
	if x == nil || y == nil || math.IsNaN(*x) || math.IsNaN(*y) || math.IsInf(*x, 0) || math.IsInf(*y, 0) {
		return 0, 0, fail("invalid_arguments", "finite x/y coordinates are required")
	}
	var px, py float64
	switch space {
	case "normalized":
		if *x < 0 || *x > 1000 || *y < 0 || *y > 1000 {
			return 0, 0, fail("invalid_arguments", "normalized coordinates must be within 0–1000")
		}
		px = *x / 1000 * float64(w.Width-1)
		py = *y / 1000 * float64(w.Height-1)
	case "screenshot":
		if s.width <= 0 || s.height <= 0 || *x < 0 || *x >= float64(s.width) || *y < 0 || *y >= float64(s.height) {
			return 0, 0, fail("invalid_arguments", "coordinates are outside the snapshot image")
		}
		px = *x / float64(s.width) * float64(w.Width)
		py = *y / float64(s.height) * float64(w.Height)
	default:
		return 0, 0, fail("invalid_arguments", "coordinate_space must be normalized or screenshot")
	}
	rx, ry := w.x+int(px), w.y+int(py)
	if rx < 0 || ry < 0 || rx > 32767 || ry > 32767 {
		return 0, 0, fail("unsupported_input", "target point is outside the addressable X11 root")
	}
	return rx, ry, nil
}

func (b *backend) fake(kind, detail byte, x, y int) error {
	return xtest.FakeInputChecked(b.conn, kind, detail, 0, b.screen.Root, int16(x), int16(y), 0).Check()
}
func (b *backend) pointerOn(w window) bool {
	current := b.screen.Root
	for i := 0; i < 64; i++ {
		r, err := xproto.QueryPointer(b.conn, current).Reply()
		if err != nil || !r.SameScreen {
			return false
		}
		if r.Child == 0 {
			return b.descendant(current, w.XID)
		}
		current = r.Child
	}
	return false
}
func (b *backend) guard(ctx context.Context, w window, p inputPlan, heldKeys []byte, heldButton byte) error {
	if err := checkpoint(ctx); err != nil {
		return err
	}
	fresh, err := b.validate(w)
	if err != nil {
		return err
	}
	if !sameGeometry(w, fresh) {
		return fail("stale_snapshot", "target moved or resized during input; observe before retrying")
	}
	if !b.hasFocus(w) {
		return fail("control_lost", "focus left the exact target; remaining input stopped")
	}
	keys, err := xproto.QueryKeymap(b.conn).Reply()
	if err != nil {
		return err
	}
	for code := 0; code < 256; code++ {
		if keys.Keys[code/8]&(1<<uint(code%8)) != 0 && !slices.Contains(heldKeys, byte(code)) {
			return fail("user_input_active", "another key is held; remaining input stopped")
		}
	}
	pointer, err := xproto.QueryPointer(b.conn, b.screen.Root).Reply()
	if err != nil {
		return err
	}
	mask := uint16(0x1f00)
	if heldButton >= 1 && heldButton <= 5 {
		mask &^= 1 << uint(heldButton+7)
	}
	if pointer.Mask&mask != 0 {
		return fail("user_input_active", "another mouse button is held; remaining input stopped")
	}
	if len(p.mapping) > 0 {
		if err := b.keyboardState(p, heldKeys); err != nil {
			return err
		}
		mapping, e := b.keyboardMapping()
		if e != nil {
			return e
		}
		if !slices.Equal(p.mapping, mapping.Keysyms) {
			return fail("control_lost", "keyboard mapping changed; observe before retrying")
		}
	}
	return nil
}
func (b *backend) atomic(ctx context.Context, w window, p inputPlan, held []byte, button byte, body func() error) error {
	if err := xproto.GrabServerChecked(b.conn).Check(); err != nil {
		return err
	}
	defer xproto.UngrabServer(b.conn)
	if err := b.guard(ctx, w, p, held, button); err != nil {
		return err
	}
	return body()
}
func (b *backend) execute(ctx context.Context, w window, p inputPlan) (int, error) {
	delivered := 0
	held := []byte{}
	var button byte
	release := func() {
		if button != 0 {
			_ = b.fake(xproto.ButtonRelease, button, 0, 0)
			button = 0
		}
		for i := len(held) - 1; i >= 0; i-- {
			_ = b.fake(xproto.KeyRelease, held[i], 0, 0)
		}
		held = nil
	}
	defer release()
	pressModifiers := func(codes []byte) error {
		for _, key := range codes {
			if slices.Contains(held, key) {
				continue
			}
			// A lost acknowledgement does not prove the press was undelivered.
			// Register cleanup before sending so every attempted press is released.
			held = append(held, key)
			if err := b.fake(xproto.KeyPress, key, 0, 0); err != nil {
				return err
			}
		}
		return nil
	}
	hold := func(duration time.Duration) error {
		deadline := time.Now().Add(duration)
		for time.Now().Before(deadline) {
			timer := time.NewTimer(min(20*time.Millisecond, time.Until(deadline)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return checkpoint(ctx)
			case <-timer.C:
			}
			if err := b.guard(ctx, w, p, held, button); err != nil {
				return err
			}
		}
		return nil
	}
	if p.action == "type_text" || p.action == "press_key" || p.action == "press_keys" {
		for _, stroke := range p.keys {
			err := b.atomic(ctx, w, p, nil, 0, func() error {
				delivered++
				if err := pressModifiers(appendUnique(slices.Clone(p.modifiers), stroke.codes...)); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return delivered, err
			}
			if err = hold(p.duration); err != nil {
				return delivered, err
			}
			release()
			if err = checkpoint(ctx); err != nil {
				return delivered, err
			}
		}
		return delivered, nil
	}
	// Focus and pointer routing are checked together under a short server grab:
	// another X11 client cannot change focus/stacking between validation and press.
	for n := 0; n < p.count; n++ {
		err := b.atomic(ctx, w, p, nil, 0, func() error {
			if err := b.fake(xproto.MotionNotify, 0, p.x, p.y); err != nil {
				return err
			}
			if !b.pointerOn(w) {
				return fail("target_occluded", "another window covers the target point; no button was pressed")
			}
			delivered++
			if err := pressModifiers(p.modifiers); err != nil {
				return err
			}
			button = p.button
			if err := b.fake(xproto.ButtonPress, p.button, 0, 0); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return delivered, err
		}
		if p.action == "drag" {
			steps := max(2, int(p.duration/(16*time.Millisecond)))
			for i := 1; i <= steps; i++ {
				if err = hold(p.duration / time.Duration(steps)); err != nil {
					return delivered, err
				}
				x := p.x + (p.toX-p.x)*i/steps
				y := p.y + (p.toY-p.y)*i/steps
				err = b.atomic(ctx, w, p, held, button, func() error {
					if err := b.fake(xproto.MotionNotify, 0, x, y); err != nil {
						return err
					}
					if !b.pointerOn(w) {
						return fail("target_occluded", "drag path left the visible target; gesture stopped")
					}
					return nil
				})
				if err != nil {
					return delivered, err
				}
			}
		} else if err = hold(p.duration); err != nil {
			return delivered, err
		}
		release()
		if p.count > 1 {
			if err = hold(25 * time.Millisecond); err != nil {
				return delivered, err
			}
		}
	}
	return delivered, nil
}
