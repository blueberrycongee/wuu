package siwc

import (
	"errors"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

// NewBoundManager uses an already resolved home and the selected generation's
// credential client. Its owner shares this instance with dependent consumers,
// closes it after their cleanup, then releases the credential generation.
func NewBoundManager(resolvedWuuHome string, credentials credentialstore.Store) (*Manager, error) {
	if !filepath.IsAbs(resolvedWuuHome) {
		return nil, errors.New("ChatGPT requires a resolved absolute Wuu home")
	}
	return New(Config{StateDir: filepath.Join(resolvedWuuHome, "chatgpt"), Credentials: credentials})
}

// Remove this error-only compatibility entry when all application/model
// constructors inject NewBoundManager from their retained service generation.
func DefaultManager() (*Manager, error) {
	return nil, errors.New("ChatGPT requires an explicitly bound credential provider")
}
