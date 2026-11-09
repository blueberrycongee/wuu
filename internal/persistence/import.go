package persistence

import (
	"context"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
)

// Import explicitly installs a verified portable snapshot into an empty selected
// provider. Exact retries succeed without rewriting data. Publishing a new
// physical generation fences every other handle of the former empty target.
// This does not stop writers of the SOURCE: the host must drain and fence those
// before transfer and atomically publish its authority checkpoint afterwards.
func (s *Store) Import(ctx context.Context, snapshot Snapshot) (ImportResult, error) {
	if err := validateSnapshotOwners(ctx, snapshot); err != nil {
		return ImportResult{}, err
	}
	expected, err := SnapshotCheckpoint(snapshot)
	if err != nil {
		return ImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer lock.Release()
	if err := s.checkNamespaceAccess(ctx, nil); err != nil {
		return ImportResult{}, err
	}
	current, err := s.backend.export(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	actual, err := SnapshotCheckpoint(current)
	if err != nil {
		return ImportResult{}, err
	}
	if actual == expected {
		return ImportResult{Revision: actual.Revision}, nil
	}
	if err := inspectionjournal.Check(s.dir, nil); err != nil {
		return ImportResult{}, err
	}
	if current.Revision != 0 || len(current.Documents) != 0 || len(current.Receipts) != 0 {
		return ImportResult{}, fmt.Errorf("%w: snapshot import requires an empty target or an exact retry", ErrConflict)
	}
	next, err := createGeneration(ctx, s.dir, s.binding.Backend, snapshot)
	if err != nil {
		return ImportResult{}, err
	}
	target, err := openBackend(s.dir, next, false)
	if err != nil {
		return ImportResult{}, err
	}
	if err := ctx.Err(); err != nil {
		target.close()
		return ImportResult{}, err
	}
	publicationErr := publishBinding(s.dir, next)
	if publicationErr != nil {
		published, readErr := readBinding(s.dir)
		if readErr != nil || published != next {
			target.close()
			return ImportResult{}, publicationErr
		}
	}
	// A directory-sync error after rename still committed the new authority.
	// Adopt it and report the finalization error instead of retaining a stale handle.
	previous := s.backend
	s.backend = target
	s.binding = next
	closeErr := previous.close()
	if closeErr != nil {
		closeErr = fmt.Errorf("snapshot import committed; close previous backend: %w", closeErr)
	}
	return ImportResult{Revision: expected.Revision}, errors.Join(publicationErr, closeErr)
}
