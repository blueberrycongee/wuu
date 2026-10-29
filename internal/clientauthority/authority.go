// Package clientauthority carries host-admitted UI authority in Go contexts.
// It is not a plugin protocol, user-message resolver, or human-click attestation.
package clientauthority

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/blueberrycongee/wuu/internal/userspace"
)

type ownerKey struct{}
type owner struct {
	id       string
	binding  userspace.Binding
	lifetime context.Context
}

// AdmitDesktop consumes the dedicated inherited desktop-parent channel. Callers
// must never pass the application JSONL stream or plugin-controlled bytes here.
// The desktop and installed extensions share the existing trusted OS-user model;
// this does not authenticate an arbitrary process as a physical human.
func AdmitDesktop(ctx context.Context, binding userspace.Binding, bootstrap io.Reader) (context.Context, error) {
	if ctx == nil || bootstrap == nil || binding.Validate() != nil || !binding.IsUser() {
		return nil, errors.New("desktop owner admission requires an explicit user binding")
	}
	var input struct {
		Version       int    `json:"version"`
		UserSpaceID   string `json:"user_space_id"`
		ProductDomain string `json:"product_domain"`
	}
	decoder := json.NewDecoder(io.LimitReader(bootstrap, 1025))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, errors.New("invalid desktop owner admission")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || input.Version != 1 || input.UserSpaceID != binding.UserSpaceID || input.ProductDomain != binding.ProductDomain {
		return nil, errors.New("desktop owner admission binding mismatch")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, ownerKey{}, &owner{id: newConnectionID(), binding: binding, lifetime: ctx}), nil
}

// BindConnection shortens inherited authority to one admitted transport. A
// detached plugin callback cannot outlive that transport by dropping cancellation.
func BindConnection(ctx context.Context) context.Context {
	proof, _ := ctx.Value(ownerKey{}).(*owner)
	if proof == nil {
		return ctx
	}
	if RequireOwner(ctx, proof.binding) != nil {
		return context.WithValue(ctx, ownerKey{}, (*owner)(nil))
	}
	return context.WithValue(ctx, ownerKey{}, &owner{id: newConnectionID(), binding: proof.binding, lifetime: ctx})
}

func RequireOwner(ctx context.Context, binding userspace.Binding) error {
	if ctx == nil {
		return errors.New("owner UI transport is required")
	}
	proof, _ := ctx.Value(ownerKey{}).(*owner)
	if proof == nil || ctx.Err() != nil || proof.lifetime.Err() != nil || binding.Validate() != nil || !binding.IsUser() || !proof.binding.Matches(binding) {
		return errors.New("live owner UI transport for this user binding is required")
	}
	return nil
}

// Connection returns only the host-minted identity and its revocation lifetime.
func Connection(ctx context.Context, binding userspace.Binding) (string, context.Context, error) {
	if err := RequireOwner(ctx, binding); err != nil {
		return "", nil, err
	}
	proof := ctx.Value(ownerKey{}).(*owner)
	return proof.id, proof.lifetime, nil
}
func newConnectionID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}
