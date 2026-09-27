// Package workingnotes persists session working memory independently of extensions.
package workingnotes

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type Store struct{ Home string }

func (s Store) path(sessionID string) (string, error) {
	if strings.TrimSpace(s.Home) == "" || strings.TrimSpace(sessionID) == "" {
		return "", errors.New("working notes require a Wuu home and session")
	}
	return filepath.Join(s.Home, "working-notes", fmt.Sprintf("%x.json", sha256.Sum256([]byte(sessionID)))), nil
}

func (s Store) Read(sessionID string) (*string, error) {
	path, err := s.path(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		value := string(data)
		return &value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Read-only compatibility for the retired bundled extension. Reads preserve
	// the old revision; the first edit writes native storage without deleting the
	// old data. Remove after legacy storage is migrated.
	document, err := pluginsettings.ReadState(s.Home, "", "plugin:bundled:note-compaction", pluginsettings.ScopeUser)
	if err != nil {
		return nil, err
	}
	value, found := document.Values[fmt.Sprintf("notes.v1.%x", sha256.Sum256([]byte(sessionID)))]
	if !found {
		return nil, nil
	}
	return &value, nil
}

func (s Store) CompareAndSwap(sessionID string, expected *string, value string) error {
	path, err := s.path(sessionID)
	if err != nil {
		return err
	}
	lock, err := storelock.Acquire(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer lock.Release() //nolint:errcheck -- the write result is authoritative
	current, err := s.Read(sessionID)
	if err != nil {
		return err
	}
	if (current == nil) != (expected == nil) || (current != nil && *current != *expected) {
		return errors.New("notes changed during write; reread before retrying")
	}
	return securefs.WriteFileAtomic(path, []byte(value))
}
