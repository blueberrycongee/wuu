package personalcalendar

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const activationProof = "personal-calendar-completed-consent-v1\n"

// This record contains no tokens, subjects, event data or copied Personal CAS
// state. Missing proof is a deny, including credentials left by a crashed or
// cancelled OAuth attempt whose compensating Keychain deletion failed.
func (b *Broker) activationPath(r stored) string {
	return filepath.Join(b.activationDir, key(b.config.ClientID+"\x00"+r.Connection.UserSpaceID+"\x00"+r.Connection.ID))
}
func (b *Broker) hasActivation(r stored) bool {
	if b.activationDir == "" {
		return false
	}
	path := b.activationPath(r)
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != securefs.FileMode {
		return false
	}
	data, e := os.ReadFile(path)
	return e == nil && bytes.Equal(data, []byte(activationProof))
}

// The rename is the completion boundary. All fallible preparation and the final
// cancellation check precede it; there is no later failing step that could
// report cancellation after durable completion. Losing the proof in a crash
// can only disable a completed connection, never enable an incomplete one.
func (b *Broker) writeActivation(ctx context.Context, r stored) error {
	if b.activationDir == "" {
		return fault("activation_record_unavailable")
	}
	if e := securefs.Mkdir(b.activationDir); e != nil {
		return fault("activation_record_unavailable")
	}
	info, e := os.Lstat(b.activationDir)
	if e != nil || !info.IsDir() || info.Mode().Perm() != securefs.DirMode {
		return fault("activation_record_unavailable")
	}
	tmp, e := os.CreateTemp(b.activationDir, ".pending-*")
	if e != nil {
		return fault("activation_record_unavailable")
	}
	defer os.Remove(tmp.Name())
	_, e = tmp.WriteString(activationProof)
	if e == nil {
		e = tmp.Chmod(securefs.FileMode)
	}
	if e == nil {
		e = tmp.Sync()
	}
	e = errors.Join(e, tmp.Close())
	if e != nil {
		return fault("activation_record_unavailable")
	}
	if ctx.Err() != nil || b.lifetime.Err() != nil {
		return fault("authorization_cancelled")
	}
	if e = os.Rename(tmp.Name(), b.activationPath(r)); e != nil {
		return fault("activation_record_unavailable")
	}
	return nil
}
