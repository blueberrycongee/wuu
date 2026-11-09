// Package inspectionjournal stores non-expiring read fences under a provider's
// transaction lock. It stores authority metadata only, never business values.
package inspectionjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

var ErrActive = errors.New("inspection fences a concurrent write")
var ErrReleased = errors.New("inspection was already released")
var ErrConflict = errors.New("inspection identity has different intent or owner")

type Record struct {
	ID           string   `json:"id"`
	Owner        string   `json:"owner"`
	Scopes       []string `json:"scopes"`
	ReconciledBy string   `json:"reconciled_by,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

func Validate(id string, scopes []string) error {
	if strings.TrimSpace(id) != id || id == "" || len(id) > 1024 || len(scopes) == 0 || len(scopes) > 128 {
		return errors.New("inspection requires a bounded id and scopes")
	}
	for _, scope := range scopes {
		if scope == "" || strings.TrimSpace(scope) != scope || len(scope) > 1024 {
			return errors.New("invalid inspection scope")
		}
	}
	return nil
}
func normalized(id, owner string, scopes []string) Record {
	scopes = slices.Clone(scopes)
	slices.Sort(scopes)
	return Record{ID: id, Owner: owner, Scopes: slices.Compact(scopes)}
}
func path(dir, kind, id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(dir, ".inspection-"+kind, hex.EncodeToString(sum[:])+".json")
}
func read(path string) (Record, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, false, err
	}
	if err := Validate(record.ID, record.Scopes); err != nil {
		return record, false, err
	}
	return record, true, nil
}
func same(a, b Record) bool {
	return a.ID == b.ID && a.Owner == b.Owner && slices.Equal(a.Scopes, b.Scopes)
}
func write(path string, record Record) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := securefs.WriteFileAtomic(path, raw); err != nil {
		return err
	}
	return syncParents(filepath.Dir(path))
}
func syncParents(dir string) error {
	for {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// Acquire and Release require the caller's provider transaction lock. A release
// receipt is durable before removing its active fence: a lost or delayed acquire
// can never resurrect authority after cleanup, even after a provider restart.
func Acquire(dir, owner, id string, scopes []string) error {
	if err := Validate(id, scopes); err != nil {
		return err
	}
	want := normalized(id, owner, scopes)
	if old, found, err := read(path(dir, "released", id)); err != nil {
		return err
	} else if found {
		if !same(old, want) {
			return ErrConflict
		}
		return ErrReleased
	}
	if old, found, err := read(path(dir, "active", id)); err != nil {
		return err
	} else if found && !same(old, want) {
		return ErrConflict
	}
	return write(path(dir, "active", id), want)
}
func Release(dir, owner, id string, scopes []string) error {
	if err := Validate(id, scopes); err != nil {
		return err
	}
	want := normalized(id, owner, scopes)
	for _, kind := range []string{"active", "released"} {
		if old, found, err := read(path(dir, kind, id)); err != nil {
			return err
		} else if found && !same(old, want) {
			return ErrConflict
		}
	}
	if previous, found, err := read(path(dir, "released", id)); err != nil {
		return err
	} else if found {
		want = previous
	}
	if err := write(path(dir, "released", id), want); err != nil {
		return err
	}
	active := path(dir, "active", id)
	if err := os.Remove(active); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncParents(filepath.Dir(active))
}

// Check rejects writes touching any active scope; nil checks the entire store.
// Corrupt or unreadable metadata fails closed. Released receipts are never scanned.
func Check(dir string, scopes []string) error {
	entries, err := os.ReadDir(filepath.Join(dir, ".inspection-active"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("invalid inspection journal entry")
		}
		record, found, err := read(filepath.Join(dir, ".inspection-active", entry.Name()))
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if scopes == nil {
			return fmt.Errorf("%w: %s", ErrActive, record.ID)
		}
		for _, scope := range scopes {
			if slices.Contains(record.Scopes, scope) {
				return fmt.Errorf("%w: %s", ErrActive, record.ID)
			}
		}
	}
	return nil
}

// List reports only outstanding fences; callers use IDs, owners and scopes for
// explicit crash reconciliation, never infer expiry from process liveness.
func List(dir string) ([]Record, error) {
	entries, err := os.ReadDir(filepath.Join(dir, ".inspection-active"))
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for _, entry := range entries {
		record, found, err := read(filepath.Join(dir, ".inspection-active", entry.Name()))
		if err != nil {
			return nil, err
		}
		if found {
			records = append(records, record)
		}
	}
	return records, nil
}

// Reconcile requires exact ownership and explicit evidence of a terminal
// effect. An unknown outcome cannot release a possibly live external effect.
func Reconcile(dir, reconciler, id, owner string, scopes []string, outcome, reason string) (Record, error) {
	if err := Validate(id, scopes); err != nil {
		return Record{}, err
	}
	if reconciler == "" || owner == "" || strings.TrimSpace(reason) == "" || len(reason) > 4096 || (outcome != "completed" && outcome != "not-dispatched") {
		return Record{}, errors.New("reconciliation requires terminal outcome, owner and evidence")
	}
	want := normalized(id, owner, scopes)
	previous, found, err := read(path(dir, "released", id))
	if err != nil {
		return Record{}, err
	}
	if found {
		if !same(previous, want) || previous.ReconciledBy != reconciler || previous.Outcome != outcome || previous.Reason != reason {
			return Record{}, ErrConflict
		}
		if err := Release(dir, owner, id, scopes); err != nil {
			return Record{}, err
		}
		return previous, nil
	}
	active, found, err := read(path(dir, "active", id))
	if err != nil {
		return Record{}, err
	}
	if !found || !same(active, want) {
		return Record{}, ErrConflict
	}
	want.ReconciledBy, want.Outcome, want.Reason = reconciler, outcome, reason
	if err := write(path(dir, "released", id), want); err != nil {
		return Record{}, err
	}
	if err := Release(dir, owner, id, scopes); err != nil {
		return Record{}, err
	}
	return want, nil
}
