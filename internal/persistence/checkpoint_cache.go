package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const CheckpointInputSchema = "state.documents.checkpoint.input.v1"
const CheckpointOutputSchema = "state.documents.checkpoint.output.v1"

type cachedCheckpoint struct {
	Generation string     `json:"generation"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

// A checkpoint is derived metadata, fenced by the physical generation and
// durable revision. A crash between commit and publication leaves a stale
// cache, which is rebuilt. Full exports remain the explicit integrity audit.
func readCachedCheckpoint(dir, generation string, revision Revision) (*cachedCheckpoint, error) {
	raw, err := os.ReadFile(filepath.Join(dir, generation, "checkpoint.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cached cachedCheckpoint
	if err := json.Unmarshal(raw, &cached); err != nil {
		return nil, nil
	}
	if cached.Generation != generation || cached.Checkpoint.Revision != revision {
		return nil, nil
	}
	if _, _, err := decodeCheckpoint(cached.Checkpoint); err != nil {
		return nil, nil
	}
	return &cached, nil
}

func writeCachedCheckpoint(dir string, cached cachedCheckpoint) error {
	raw, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return securefs.WriteFileAtomic(filepath.Join(dir, cached.Generation, "checkpoint.json"), raw)
}

type backendCheckpointReader struct {
	backend
	generation string
}

func (r backendCheckpointReader) Read(ctx context.Context, q Query) (View, error) {
	return r.backend.read(ctx, q)
}
func (r backendCheckpointReader) ExportPage(ctx context.Context, q ExportQuery) (ExportPage, error) {
	return r.backend.exportPage(ctx, q, r.generation)
}

// Checkpoint returns the current committed content/receipt digest. It is not a
// physical scrub: unchanged records are not reread on each process admission.
func (s *Store) Checkpoint(ctx context.Context) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	defer lock.Release()
	view, err := s.backend.read(ctx, Query{Namespace: ServiceName, Keys: []string{}})
	if err != nil {
		return Checkpoint{}, err
	}
	cached, err := readCachedCheckpoint(s.dir, s.binding.Generation, view.Revision)
	if err != nil {
		return Checkpoint{}, err
	}
	if cached != nil {
		return cached.Checkpoint, nil
	}
	checkpoint, err := CaptureCheckpoint(ctx, backendCheckpointReader{s.backend, s.binding.Generation})
	if err != nil {
		return Checkpoint{}, err
	}
	err = writeCachedCheckpoint(s.dir, cachedCheckpoint{s.binding.Generation, checkpoint})
	return checkpoint, err
}

func (p *preparedStore) checkpoint(ctx context.Context, dir, kind string) (Checkpoint, error) {
	var result Checkpoint
	err := p.withBackend(ctx, dir, kind, func(source backend, selected binding) error {
		if source == nil {
			var err error
			result, err = SnapshotCheckpoint(Snapshot{Version: 1})
			return err
		}
		view, err := source.read(ctx, Query{Namespace: ServiceName, Keys: []string{}})
		if err != nil {
			return err
		}
		cached, err := readCachedCheckpoint(dir, selected.Generation, view.Revision)
		if err != nil {
			return err
		}
		if cached == nil && p.verified != nil && p.verified.Generation == selected.Generation && p.verified.Checkpoint.Revision == view.Revision {
			cached = p.verified
		}
		if cached != nil {
			result = cached.Checkpoint
			return nil
		}
		result, err = CaptureCheckpoint(ctx, backendCheckpointReader{source, selected.Generation})
		if err == nil {
			p.verified = &cachedCheckpoint{selected.Generation, result}
		}
		return err
	})
	return result, err
}

// Preparation cannot write files. Activation may retain its verified digest
// only if neither the physical binding nor the durable revision has changed.
func (s *Store) retainPreparedCheckpoint(ctx context.Context, cached *cachedCheckpoint) error {
	if cached == nil || cached.Generation != s.binding.Generation {
		return nil
	}
	lock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	view, err := s.backend.read(ctx, Query{Namespace: ServiceName, Keys: []string{}})
	if err != nil {
		return err
	}
	if view.Revision != cached.Checkpoint.Revision {
		return nil
	}
	return writeCachedCheckpoint(s.dir, *cached)
}

func (c *Client) Checkpoint(ctx context.Context) (Checkpoint, error) {
	var result Checkpoint
	if err := c.invoke(ctx, "checkpoint", struct{}{}, &result); err != nil {
		return Checkpoint{}, err
	}
	if _, _, err := decodeCheckpoint(result); err != nil {
		return Checkpoint{}, err
	}
	return result, nil
}
