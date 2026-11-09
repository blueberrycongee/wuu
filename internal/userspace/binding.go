// Package userspace defines immutable host-selected product data boundaries.
package userspace

import (
	"errors"
	"path/filepath"
	"regexp"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// Binding is supplied by the embedding host, never inferred from a working directory.
// Its zero value preserves the legacy Coding and scratch behavior.
type Binding struct {
	UserSpaceID   string `json:"user_space_id,omitempty"`
	ProductDomain string `json:"product_domain,omitempty"`
}

func (b Binding) Validate() error {
	if b.UserSpaceID == "" && b.ProductDomain == "" {
		return nil
	}
	if !identifier.MatchString(b.UserSpaceID) {
		return errors.New("user_space_id must be a stable 1-64 character identifier")
	}
	if b.ProductDomain != "personal" && b.ProductDomain != "coding" {
		return errors.New("product_domain must be personal or coding")
	}
	return nil
}
func (b Binding) Domain() string {
	if b.ProductDomain == "" {
		return "coding"
	}
	return b.ProductDomain
}
func (b Binding) IsUser() bool { return b.UserSpaceID != "" }
func (b Binding) Matches(other Binding) bool {
	return b.UserSpaceID == other.UserSpaceID && b.Domain() == other.Domain()
}
func (b Binding) Base(home string) string {
	return filepath.Join(home, "user-spaces", b.UserSpaceID, b.ProductDomain)
}
func (b Binding) Root(home string) string  { return filepath.Join(b.Base(home), "files") }
func (b Binding) State(home string) string { return filepath.Join(b.Base(home), "state") }
