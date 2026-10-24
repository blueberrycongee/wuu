package sidethread

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type legacyRecord struct {
	key, namespace, digest string
	raw                    json.RawMessage
}
type legacyReceipt struct {
	Digest string `json:"digest"`
	Staged bool   `json:"staged"`
}

// ImportLegacy explicitly transfers legacy JSON, including staged deletions,
// into captured Documents without modifying source files or domain metadata.
// Only the exclusive boot owner may write; live peers verify prior receipts.
// Receipts travel with snapshots and survive deletion to prevent resurrection.
func (s *Store) ImportLegacy(dir string, bootOwner bool) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := storelock.Acquire(dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	var records []legacyRecord
	seen := map[string]bool{}
	for _, source := range []struct{ dir, namespace string }{{dir, recordNamespace}, {filepath.Join(dir, ".deleting"), deletingNamespace}} {
		entries, err := os.ReadDir(source.dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			key := strings.TrimSuffix(entry.Name(), ".json")
			if normalizeKey(key) != key || key == "" {
				return fmt.Errorf("invalid legacy side thread key %q", key)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("legacy side thread is not a regular file: %s", entry.Name())
			}
			if seen[key] {
				return fmt.Errorf("legacy side thread %s has both live and staged records", key)
			}
			seen[key] = true
			raw, err := os.ReadFile(filepath.Join(source.dir, entry.Name()))
			if err != nil {
				return err
			}
			var st SideThread
			if err := json.Unmarshal(raw, &st); err != nil {
				return fmt.Errorf("decode legacy side thread %s: %w", key, err)
			}
			if st.MainThreadID != key || strings.TrimSpace(st.SideThreadID) == "" {
				return fmt.Errorf("legacy side thread identity does not match %s", key)
			}
			digest, err := legacyDigest(raw)
			if err != nil {
				return err
			}
			records = append(records, legacyRecord{key, source.namespace, digest, raw})
		}
	}
	if len(records) == 0 {
		return nil
	}
	return s.update(func(tx *persistence.Tx) error {
		for _, source := range records {
			expected := legacyReceipt{Digest: source.digest, Staged: source.namespace == deletingNamespace}
			var receipt legacyReceipt
			found, err := tx.Get(legacyNamespace, source.key, &receipt)
			if err != nil {
				return err
			}
			if found {
				if receipt != expected {
					return fmt.Errorf("legacy side thread %s changed after import; reconcile source before retrying", source.key)
				}
				continue
			}
			if !bootOwner {
				return errors.New("legacy side threads require exclusive boot recovery; stop other app-servers before import")
			}
			raw, namespace, err := record(tx, source.key)
			if err != nil {
				return err
			}
			if raw != nil {
				digest, err := legacyDigest(raw)
				if err != nil {
					return err
				}
				if digest != source.digest || namespace != source.namespace {
					return fmt.Errorf("legacy side thread %s conflicts with selected history", source.key)
				}
			} else if err := tx.Put(source.namespace, source.key, source.raw); err != nil {
				return err
			}
			if err := tx.Put(legacyNamespace, source.key, expected); err != nil {
				return err
			}
		}
		return nil
	})
}

func legacyDigest(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
