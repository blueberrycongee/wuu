// Package principal keeps authenticated transport provenance separate from wire
// identity claims. Only a trusted embedding transport or identity adapter creates
// Verified values; app-server parameters never deserialize into this authority.
package principal

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Identity is a namespace-qualified principal, not proof of authentication.
// Tenant is required even for a single-tenant issuer. Subject is opaque.
type Identity struct {
	Kind    string `json:"kind"`
	Issuer  string `json:"issuer"`
	Tenant  string `json:"tenant"`
	Subject string `json:"subject"`
}

func (i Identity) Validate() error {
	if i.Kind != "user" && i.Kind != "device" && i.Kind != "agent" {
		return errors.New("unknown principal kind")
	}
	for _, value := range []string{i.Issuer, i.Tenant, i.Subject} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return errors.New("principal namespace and subject are required")
		}
	}
	return nil
}

type Verified struct {
	durableAuthority string
	identity         Identity
	owner            bool
	valid            bool
	check            func() bool
	expiresAt        time.Time
	revalidate       func(context.Context) error
}

func (v Verified) Identity() Identity { return v.identity }

// DurableAuthority identifies provider-owned validity independent of a GUI
// connection. Empty means unsupported; identity and owner flags cannot infer it.
func (v Verified) DurableAuthority() string { return v.durableAuthority }
func (v Verified) IsOwner() bool            { return v.valid && v.owner }
func (v Verified) Valid() bool {
	return v.valid && v.identity.Validate() == nil && (v.expiresAt.IsZero() || time.Now().Before(v.expiresAt))
}

// Check validates the connection immediately before a protected operation.
// Provider-authenticated connections recheck the original credential; a removed
// provider, revoked credential or changed subject cannot retain cached authority.
func (v Verified) Check(ctx context.Context) error {
	if !v.Valid() {
		return errors.New("authenticated session expired or revoked")
	}
	if v.check != nil && !v.check() {
		return errors.New("authenticated transport was revoked")
	}
	if v.revalidate != nil {
		if err := v.revalidate(ctx); err != nil {
			return err
		}
	}
	if !v.Valid() {
		return errors.New("authenticated session expired during validation")
	}
	return ctx.Err()
}
func (v Verified) ExpiresAt() time.Time { return v.expiresAt }

// Authenticated accepts only the result of a trusted authenticator, never a
// caller-supplied identity object. Authentication does not grant owner authority.
func Authenticated(identity Identity) (Verified, error) {
	if err := identity.Validate(); err != nil {
		return Verified{}, err
	}
	return Verified{identity: identity, valid: true}, nil
}

// AuthenticatedWithValidity binds an authenticated non-owner to a transport's
// live enrollment check. The check is host code, never a wire-supplied callback.
func AuthenticatedWithValidity(identity Identity, valid func() bool) (Verified, error) {
	if valid == nil {
		return Verified{}, errors.New("connection validity check required")
	}
	caller, err := Authenticated(identity)
	if err != nil {
		return Verified{}, err
	}
	caller.check = valid
	return caller, nil
}

// LocalOwner is for an embedding's trusted local pipe, not network ingress.
func LocalOwner() Verified {
	return Verified{identity: Identity{Kind: "user", Issuer: "wuu:local", Tenant: "local", Subject: "owner"}, owner: true, valid: true, durableAuthority: "local-installation"}
}

// OwnerConnection is reserved for trusted transports that already verified an
// owner-controlled enrollment (for example cryptographic device pairing or an
// authenticated account device roster). Identity-provider results use
// Authenticate instead and can never set owner authority.
func OwnerConnection(identity Identity, valid func() bool) (Verified, error) {
	caller, err := AuthenticatedWithValidity(identity, valid)
	if err != nil {
		return Verified{}, err
	}
	caller.owner = true
	return caller, nil
}

// PairedOwner is for a device whose cryptographic key was verified against the
// host's persisted owner-controlled pairing store. Pairing already grants full
// owner control; mere possession of a device id does not.
func PairedOwner(issuer, subject string, stillPaired ...func() bool) (Verified, error) {
	v, err := Authenticated(Identity{Kind: "device", Issuer: issuer, Tenant: "paired", Subject: subject})
	v.owner = err == nil
	if len(stillPaired) > 0 {
		v.check = stillPaired[0]
	}
	return v, err
}

type contextKey struct{}

func WithContext(ctx context.Context, v Verified) context.Context {
	return context.WithValue(ctx, contextKey{}, v)
}
func FromContext(ctx context.Context) (Verified, bool) {
	v, ok := ctx.Value(contextKey{}).(Verified)
	return v, ok
}

// Credential comes only from the transport. Adapters must verify signature,
// issuer, audience, expiry and revocation according to their protocol. The host
// does not treat a decoded OIDC claim or SAML assertion as verified identity.
type Credential struct {
	Scheme   string `json:"scheme"`
	Token    string `json:"token"`
	Audience string `json:"audience"`
}
type Authentication struct {
	Identity  Identity  `json:"identity"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Authenticator interface {
	Authenticate(context.Context, Credential) (Authentication, error)
}

func Authenticate(ctx context.Context, adapter Authenticator, credential Credential) (Verified, error) {
	if adapter == nil {
		return Verified{}, errors.New("identity provider unavailable")
	}
	result, err := adapter.Authenticate(ctx, credential)
	if err != nil {
		return Verified{}, err
	}
	if result.ExpiresAt.IsZero() || !time.Now().Before(result.ExpiresAt) {
		return Verified{}, errors.New("identity provider returned an expired or unbounded session")
	}
	verified, err := Authenticated(result.Identity)
	if err != nil {
		return Verified{}, err
	}
	verified.expiresAt = result.ExpiresAt
	verified.revalidate = func(ctx context.Context) error {
		current, err := adapter.Authenticate(ctx, credential)
		if err != nil {
			return errors.New("identity provider rejected connection")
		}
		if current.Identity != result.Identity || current.ExpiresAt.IsZero() || !time.Now().Before(current.ExpiresAt) {
			return errors.New("authenticated identity changed or expired")
		}
		return nil
	}
	return verified, nil
}
