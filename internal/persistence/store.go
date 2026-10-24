package persistence

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type backend interface {
	read(context.Context, Query) (View, error)
	commit(context.Context, Transaction, string) (Receipt, error)
	export(context.Context) (Snapshot, error)
	exportPage(context.Context, ExportQuery, string) (ExportPage, error)
	compact(context.Context) (Compaction, error)
	restore(context.Context, Snapshot) error
	close() error
}

type binding struct {
	Version    int    `json:"version"`
	Backend    string `json:"backend"`
	Generation string `json:"generation"`
}

// Store pins one durable backend generation. Every operation checks the same
// authority file under the cross-process transaction lock. A migrated writer
// fails visibly even when it still holds an open database or log descriptor.
type Store struct {
	mu      sync.Mutex
	dir     string
	binding binding
	backend backend
}

func Open(ctx context.Context, dir, kind string) (*Store, error) {
	if kind != "sqlite" && kind != "jsonl" {
		return nil, fmt.Errorf("%w: unknown backend %q", ErrInvalid, kind)
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	current, err := readBinding(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := requireEmptyDirectory(dir); err != nil {
			return nil, fmt.Errorf("%w: authority is missing in a nonempty store: %v", ErrCorrupt, err)
		}
		current, err = createGeneration(ctx, dir, kind, Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}})
		if err != nil {
			return nil, err
		}
		if err := publishBinding(dir, current); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if current.Backend != kind {
		return nil, ErrMigrationRequired
	}
	b, err := openBackend(dir, current, false)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, binding: current, backend: b}, nil
}

func (s *Store) acquire(ctx context.Context) (*storelock.Lock, error) {
	if s.backend == nil {
		return nil, ErrClosed
	}
	lock, err := storelock.AcquireContext(ctx, s.dir)
	if err != nil {
		return nil, err
	}
	current, err := readBinding(s.dir)
	if err == nil && current != s.binding {
		err = ErrBindingChanged
	}
	if err != nil {
		lock.Release()
		return nil, err
	}
	return lock, nil
}

func (s *Store) Read(ctx context.Context, q Query) (View, error) {
	if err := validateQuery(q); err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return View{}, err
	}
	defer lock.Release()
	return s.backend.read(ctx, q)
}

// Commit observes cancellation before durable admission. Once the write starts,
// it completes its durability barrier regardless of caller cancellation. An I/O
// error or lost response has an unknown outcome; retry the identical operation
// ID and intent. Reusing an ID with different intent is always an error.
func (s *Store) Commit(ctx context.Context, tx Transaction) (Receipt, error) {
	digest, err := transactionDigest(tx)
	if err != nil {
		return Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer lock.Release()
	view, err := s.backend.read(ctx, Query{Namespace: ServiceName, Keys: []string{}})
	if err != nil {
		return Receipt{}, err
	}
	var after *Checkpoint
	if view.Revision == tx.ExpectedRevision {
		cached, err := readCachedCheckpoint(s.dir, s.binding.Generation, view.Revision)
		if err != nil {
			return Receipt{}, err
		}
		if cached != nil {
			next, err := NextCheckpoint(ctx, backendCheckpointReader{s.backend, s.binding.Generation}, cached.Checkpoint, tx)
			if err != nil {
				return Receipt{}, err
			}
			after = &next
		}
	}
	receipt, err := s.backend.commit(ctx, tx, digest)
	if err != nil {
		return receipt, err
	}
	if after != nil && receipt.Revision == after.Revision {
		// The durable receipt wins if cache publication fails; an exact retry
		// remains safe and a stale cache is rebuilt at the next checkpoint read.
		err = writeCachedCheckpoint(s.dir, cachedCheckpoint{s.binding.Generation, *after})
	}
	return receipt, err
}

func (s *Store) Export(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer lock.Release()
	return s.backend.export(ctx)
}

// Compact serializes physical maintenance with every handle's reads/writes.
// Once journal replacement begins, its durability barrier completes even if
// the caller cancels. Compaction never replays writes or drops their receipts.
func (s *Store) Compact(ctx context.Context) (Compaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return Compaction{}, err
	}
	defer lock.Release()
	return s.backend.compact(ctx)
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend == nil {
		return nil
	}
	err := s.backend.close()
	s.backend = nil
	return err
}

// Migrate excludes all writers while copying and checking a portable snapshot.
// Only a synced, verified target is published. Before publication the source
// remains authoritative; afterwards old handles fail with ErrBindingChanged.
// The source generation remains available for inspection, never for dual writes.
// Callers must quiesce business consumers before changing their provider binding.
func Migrate(ctx context.Context, dir, from, to string) error {
	if from == to {
		return fmt.Errorf("%w: source and target are identical", ErrInvalid)
	}
	if to != "sqlite" && to != "jsonl" {
		return fmt.Errorf("%w: unknown backend %q", ErrInvalid, to)
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	current, err := readBinding(dir)
	if err != nil {
		return err
	}
	if current.Backend != from {
		return ErrBindingChanged
	}
	source, err := openBackend(dir, current, false)
	if err != nil {
		return err
	}
	defer source.close()
	snapshot, err := source.export(ctx)
	if err != nil {
		return err
	}
	next, err := createGeneration(ctx, dir, to, snapshot)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishBinding(dir, next)
}

// Import creates a new store from a portable export. It never replaces an
// existing authority or guesses which orphaned generation was last committed.
// Restore into a separate empty directory when recovering damaged metadata.
func Import(ctx context.Context, dir, kind string, snapshot Snapshot) error {
	if kind != "sqlite" && kind != "jsonl" {
		return fmt.Errorf("%w: unknown backend %q", ErrInvalid, kind)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := requireEmptyDirectory(dir); err != nil {
		return err
	}
	next, err := createGeneration(ctx, dir, kind, snapshot)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishBinding(dir, next)
}

// Inspection contains no document values. Inspect checks the complete snapshot
// and its durable receipt sequence under the same lock used by writers.
type Inspection struct {
	Backend    string   `json:"backend"`
	Generation string   `json:"generation"`
	Revision   Revision `json:"revision,string"`
	Documents  int      `json:"documents"`
	Receipts   int      `json:"receipts"`
}

func Inspect(ctx context.Context, dir string) (Inspection, Snapshot, error) {
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return Inspection{}, Snapshot{}, err
	}
	defer lock.Release()
	current, err := readBinding(dir)
	if err != nil {
		return Inspection{}, Snapshot{}, err
	}
	b, err := openBackend(dir, current, false)
	if err != nil {
		return Inspection{}, Snapshot{}, err
	}
	defer b.close()
	snapshot, err := b.export(ctx)
	if err != nil {
		return Inspection{}, Snapshot{}, err
	}
	return Inspection{Backend: current.Backend, Generation: current.Generation, Revision: snapshot.Revision, Documents: len(snapshot.Documents), Receipts: len(snapshot.Receipts)}, snapshot, nil
}

func requireEmptyDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// The authority lock is created before checking the directory. It is
		// retained so all processes continue locking the same inode.
		if entry.Name() != storelock.FileName {
			return fmt.Errorf("%w: destination must be empty", ErrConflict)
		}
	}
	return nil
}

func createGeneration(ctx context.Context, dir, kind string, snapshot Snapshot) (binding, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return binding{}, err
	}
	snapshot.Documents = append([]Document{}, snapshot.Documents...)
	snapshot.Receipts = append([]CommitRecord{}, snapshot.Receipts...)
	sortDocuments(snapshot.Documents)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return binding{}, err
	}
	b := binding{Version: 1, Backend: kind, Generation: hex.EncodeToString(random[:])}
	if err := securefs.Mkdir(filepath.Join(dir, b.Generation)); err != nil {
		return binding{}, err
	}
	target, err := openBackend(dir, b, true)
	if err != nil {
		return binding{}, err
	}
	defer target.close()
	if err := target.restore(ctx, snapshot); err != nil {
		return binding{}, err
	}
	verified, err := target.export(ctx)
	if err != nil {
		return binding{}, err
	}
	expected, err := json.Marshal(snapshot)
	if err != nil {
		return binding{}, err
	}
	actual, err := json.Marshal(verified)
	if err != nil {
		return binding{}, err
	}
	if string(expected) != string(actual) {
		return binding{}, fmt.Errorf("%w: migration verification mismatch", ErrCorrupt)
	}
	if err := syncDirectory(filepath.Join(dir, b.Generation)); err != nil {
		return binding{}, err
	}
	return b, nil
}

func readBinding(dir string) (binding, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "authority.json"))
	if err != nil {
		return binding{}, err
	}
	var b binding
	if err := json.Unmarshal(raw, &b); err != nil {
		return binding{}, fmt.Errorf("%w: invalid authority file", ErrCorrupt)
	}
	generation, err := hex.DecodeString(b.Generation)
	if err != nil || len(generation) != 16 || b.Version != 1 || (b.Backend != "sqlite" && b.Backend != "jsonl") {
		return binding{}, fmt.Errorf("%w: invalid authority binding", ErrCorrupt)
	}
	return b, nil
}

// A directory-sync failure after rename is explicitly a committed publication
// with uncertain crash durability, not a claim that the source was restored.
func publishBinding(dir string, b binding) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if err := securefs.WriteFileAtomic(filepath.Join(dir, "authority.json"), raw); err != nil {
		if current, readErr := readBinding(dir); readErr == nil && current == b {
			return fmt.Errorf("binding committed to %s/%s; publication finalization failed: %w", b.Backend, b.Generation, err)
		}
		return err
	}
	if err := syncAncestors(dir); err != nil {
		return fmt.Errorf("binding committed to %s/%s; directory durability failed: %w", b.Backend, b.Generation, err)
	}
	return nil
}

func openBackend(dir string, b binding, create bool) (backend, error) {
	root := filepath.Join(dir, b.Generation)
	name := "state.sqlite3"
	if b.Backend == "jsonl" {
		name = "state.jsonl"
	}
	path := filepath.Join(root, name)
	if !create {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: authoritative data file: %v", ErrCorrupt, err)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("%w: empty authoritative data file", ErrCorrupt)
		}
	}
	switch b.Backend {
	case "sqlite":
		return openSQLite(path, create)
	case "jsonl":
		log, err := openJSONL(path)
		if err != nil {
			return nil, err
		}
		if !create && !log.initialized {
			log.close()
			return nil, fmt.Errorf("%w: authoritative journal has no snapshot", ErrCorrupt)
		}
		return log, nil
	default:
		return nil, fmt.Errorf("%w: unknown backend", ErrCorrupt)
	}
}
