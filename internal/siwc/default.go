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
