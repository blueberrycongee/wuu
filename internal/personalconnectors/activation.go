package personalconnectors

import (
	"bytes"
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"os"
	"path/filepath"
)

const activationProof = "personal-connectors-completed-consent-v1\n"

func (b *Broker) activationPath(a Actor, g Grant) string {
	return filepath.Join(b.stateDir, "activation-v1", hash(a.UserSpaceID+"\x00"+g.Connection.ID+"\x00"+g.Connection.Revision+"\x00"+g.ActivationID))
}
func (b *Broker) activated(a Actor, g Grant) bool {
	if g.OAuthProvider == "" {
		return true
	}
	if !filepath.IsAbs(b.stateDir) || g.ActivationID == "" {
		return false
	}
	path := b.activationPath(a, g)
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != securefs.FileMode {
		return false
	}
	raw, e := os.ReadFile(path)
	return e == nil && bytes.Equal(raw, []byte(activationProof))
}

// Credential persistence is preparation, not completed consent. Only this
// atomic rename activates an OAuth grant. All fallible preparation and the
// last cancellation check precede it; missing proof always denies after restart.
func (b *Broker) activate(ctx context.Context, a Actor, g Grant) error {
	if !filepath.IsAbs(b.stateDir) {
		return fault("activation_record_unavailable")
	}
	dir := filepath.Dir(b.activationPath(a, g))
	if e := securefs.Mkdir(dir); e != nil {
		return fault("activation_record_unavailable")
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm() != securefs.DirMode {
		return fault("activation_record_unavailable")
	}
	f, e := os.CreateTemp(dir, ".pending-*")
	if e != nil {
		return fault("activation_record_unavailable")
	}
	defer os.Remove(f.Name())
	_, e = f.WriteString(activationProof)
	if e == nil {
		e = f.Chmod(securefs.FileMode)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return fault("activation_record_unavailable")
	}
	if ctx.Err() != nil || b.lifetime.Err() != nil {
		return fault("authorization_cancelled")
	}
	if e = os.Rename(f.Name(), b.activationPath(a, g)); e != nil {
		return fault("activation_record_unavailable")
	}
	return nil
}
