package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

func lockPreparedStore(ctx context.Context, dir string) (*storelock.Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A missing candidate is empty at this observation; preparation must not
	// create it. Existing stores share the writer's generation-publication lock.
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return storelock.AcquireContext(ctx, dir)
}

// A prepared store retains only its read-only backend/index. Every access
// checks the captured physical binding under the writer lock; it never repairs
// a journal, creates a generation, or silently follows a replacement generation.
type preparedStore struct {
	observed bool
	verified *cachedCheckpoint
	binding  binding
	source   backend
}

func (p *preparedStore) close() error {
	if p.source == nil {
		return nil
	}
	err := p.source.close()
	p.source = nil
	return err
}

func (p *preparedStore) withBackend(ctx context.Context, dir, kind string, read func(backend, binding) error) error {
	lock, err := lockPreparedStore(ctx, dir)
	if err != nil {
		return err
	}
	if lock == nil {
		if p.observed && p.binding.Generation != "" {
			return fmt.Errorf("%w: prepared store disappeared", ErrCorrupt)
		}
		p.observed = true
		return read(nil, binding{})
	}
	defer lock.Release()
	if namespaceIdentityFrom(ctx).migration {
		if err := inspectionjournal.Check(dir, nil); err != nil {
			return err
		}
	}
	selected, err := readBinding(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := requireEmptyDirectory(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: prepared authority missing in nonempty store", ErrCorrupt)
		}
		if p.observed && p.binding.Generation != "" {
			return fmt.Errorf("%w: prepared authority disappeared", ErrCorrupt)
		}
		p.observed = true
		return read(nil, binding{})
	}
	if err != nil {
		return err
	}
	if p.observed && selected != p.binding {
		return ErrBindingChanged
	}
	if selected.Backend != kind {
		return ErrMigrationRequired
	}
	if p.source == nil {
		name := "state.sqlite3"
		if kind == "jsonl" {
			name = "state.jsonl"
		}
		path := filepath.Join(dir, selected.Generation, name)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return fmt.Errorf("%w: prepared data file is missing or empty", ErrCorrupt)
		}
		var source backend
		if kind == "jsonl" {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			source, err = readJSONLFile(file, true)
			if err != nil {
				return err
			}
		} else {
			name := filepath.ToSlash(path)
			if !strings.HasPrefix(name, "/") {
				name = "/" + name
			}
			uri := url.URL{Scheme: "file", Path: name}
			query := uri.Query()
			query.Set("mode", "ro")
			query.Add("_pragma", "busy_timeout(5000)")
			uri.RawQuery = query.Encode()
			db, err := sql.Open("sqlite", uri.String())
			if err != nil {
				return err
			}
			db.SetMaxOpenConns(1)
			source = &sqliteBackend{db: db, path: path}
		}
		p.source = source
		p.binding = selected
		p.observed = true
	}
	if err := read(p.source, selected); err != nil {
		return err
	}
	current, err := readBinding(dir)
	if err != nil {
		return err
	}
	if current != selected {
		return ErrBindingChanged
	}
	return nil
}

func (p *preparedStore) snapshot(ctx context.Context, dir, kind string) (Snapshot, error) {
	snapshot := Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}
	err := p.withBackend(ctx, dir, kind, func(source backend, _ binding) error {
		if source == nil {
			return nil
		}
		var err error
		if err := (&Store{backend: source}).checkNamespaceAccess(ctx, nil); err != nil {
			return err
		}
		snapshot, err = source.export(ctx)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
func (p *preparedStore) view(ctx context.Context, dir, kind string, q Query) (View, error) {
	if err := validateQuery(q); err != nil {
		return View{}, err
	}
	view := View{Documents: []Document{}}
	err := p.withBackend(ctx, dir, kind, func(source backend, _ binding) error {
		if source == nil {
			return nil
		}
		var err error
		if err := (&Store{backend: source}).checkNamespaceAccess(ctx, []string{q.Namespace}); err != nil {
			return err
		}
		view, err = source.read(ctx, q)
		return err
	})
	if err != nil {
		return View{}, err
	}
	if q.AtRevision != nil && *q.AtRevision != view.Revision {
		return View{}, ErrConflict
	}
	return view, nil
}
func (p *preparedStore) exportPage(ctx context.Context, dir, kind string, q ExportQuery) (ExportPage, error) {
	if err := validateExportQuery(q); err != nil {
		return ExportPage{}, err
	}
	var page ExportPage
	err := p.withBackend(ctx, dir, kind, func(source backend, b binding) error {
		if source == nil {
			builder, err := newExportPage(q, 0, "empty")
			if err != nil {
				return err
			}
			builder.page.Done = true
			page = builder.page
			return nil
		}
		var err error
		if err := (&Store{backend: source}).checkNamespaceAccess(ctx, nil); err != nil {
			return err
		}
		page, err = source.exportPage(ctx, q, b.Generation)
		return err
	})
	if err != nil {
		return ExportPage{}, err
	}
	return page, nil
}
