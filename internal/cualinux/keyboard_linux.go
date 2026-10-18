package cualinux

import (
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// XGB has no generated XKB binding. These two read-only requests implement the
// fixed wire layouts in the XKB 1.0 protocol (UseExtension and GetState):
// https://www.x.org/releases/current/doc/kbproto/xkbproto.html
func (b *backend) initKeyboardState() bool {
	ext, err := xproto.QueryExtension(b.conn, 9, "XKEYBOARD").Reply()
	if err != nil || !ext.Present {
		return false
	}
	request := make([]byte, 8)
	request[0] = ext.MajorOpcode
	xgb.Put16(request[2:], 2)
	xgb.Put16(request[4:], 1)
	cookie := b.conn.NewCookie(true, true)
	b.conn.NewRequest(request, cookie)
	reply, err := cookie.Reply()
	if err != nil || len(reply) < 32 || reply[1] == 0 {
		return false
	}
	b.xkb = ext.MajorOpcode
	return true
}
func (b *backend) keyboardState(p inputPlan, held []byte) error {
	if b.xkb == 0 {
		return fail("unsupported_input", "XKB state inspection is required for safe keyboard input")
	}
	request := make([]byte, 8)
	request[0] = b.xkb
	request[1] = 4
	xgb.Put16(request[2:], 2)
	xgb.Put16(request[4:], 256)
	cookie := b.conn.NewCookie(true, true)
	b.conn.NewRequest(request, cookie)
	state, err := cookie.Reply()
	if err != nil {
		return err
	}
	if len(state) < 32 {
		return fail("unsupported_input", "incomplete XKB state reply")
	}
	if state[12] != 0 || state[13] != 0 || xgb.Get16(state[14:]) != 0 || xgb.Get16(state[16:]) != 0 {
		return fail("unsupported_input", "keyboard input supports the primary layout group only; select it before retrying")
	}
	var heldMask byte
	for _, code := range held {
		heldMask |= p.modifierMasks[code]
	}
	if state[10] != 0 || state[11]&^p.numLockMask != 0 || state[9]&^heldMask != 0 || state[8]&^(heldMask|p.numLockMask) != 0 {
		return fail("user_input_active", "latched, locked, or unowned keyboard modifiers are active; clear them before retrying")
	}
	return nil
}
