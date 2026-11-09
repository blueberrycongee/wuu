package host

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"time"
)

// devicePrincipal runs only after the handshake verified the device key. Account
// enrollment and local pairing are distinct authorities; one cannot silently
// substitute for the other after configuration or enrollment changes.
func (h *Host) devicePrincipal(pub string) (principal.Verified, error) {
	if h.store == nil {
		return principal.Verified{}, errors.New("remote identity store unavailable")
	}
	if account := h.store.Account(); account != nil {
		identity := principal.Identity{Kind: "device", Issuer: account.Server, Tenant: account.Username, Subject: pub}
		return principal.OwnerConnection(identity, func() bool {
			current := h.store.Account()
			if current == nil || current.Server != account.Server || current.Username != account.Username {
				return false
			}
			ctx, cancel := context.WithTimeout(h.baseCtx, 5*time.Second)
			defer cancel()
			return current.Allows(ctx, secure.EncodeKey(h.store.Identity().Public()), pub)
		})
	}
	paired, err := h.store.DevicePrincipal(pub)
	if err != nil {
		return principal.Verified{}, err
	}
	live := func() bool { return h.store.Account() == nil && paired.Check(h.baseCtx) == nil }
	if paired.IsOwner() {
		return principal.OwnerConnection(paired.Identity(), live)
	}
	return principal.AuthenticatedWithValidity(paired.Identity(), live)
}
