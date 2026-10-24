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

// Prepared access never creates a provider generation, repairs a journal or
// writes a logical record. Read and export share the writer lock and binding
// checks, while each call observes fresh source data for authority probes.
func withPreparedBackend(ctx context.Context, dir, kind string, read func(backend) error) error {
	lock, err := lockPreparedStore(ctx, dir)
	if err != nil {
		return err
	}
	if lock == nil {
		return nil
	}
	defer lock.Release()
	selected, err := readBinding(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := requireEmptyDirectory(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: prepared authority missing in nonempty store", ErrCorrupt)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if selected.Backend != kind {
		return ErrMigrationRequired
	}
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
		p := filepath.ToSlash(path)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		uri := url.URL{Scheme: "file", Path: p}
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
	defer source.close()
	if err := read(source); err != nil {
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

func preparedSnapshot(ctx context.Context, dir, kind string) (Snapshot, error) {
	snapshot := Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}
	err := withPreparedBackend(ctx, dir, kind, func(source backend) error {
		var err error
		snapshot, err = source.export(ctx)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func preparedView(ctx context.Context, dir, kind string, query Query) (View, error) {
	if err := validateQuery(query); err != nil {
		return View{}, err
	}
	view := View{Documents: []Document{}}
	err := withPreparedBackend(ctx, dir, kind, func(source backend) error {
		var err error
		view, err = source.read(ctx, query)
		return err
	})
	if err != nil {
		return View{}, err
	}
	// A missing candidate has revision zero without a backend to enforce the pin.
	if query.AtRevision != nil && *query.AtRevision != view.Revision {
		return View{}, ErrConflict
	}
	return view, nil
}
