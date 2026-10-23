package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
// writes a logical record. Full snapshots here are admission work, not a normal
// bounded-read implementation. Every call observes fresh source data for probes.
func preparedSnapshot(ctx context.Context, dir, kind string) (Snapshot, error) {
	lock, err := lockPreparedStore(ctx, dir)
	if err != nil {
		return Snapshot{}, err
	}
	if lock == nil {
		return Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}, nil
	}
	defer lock.Release()
	selected, err := readBinding(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := requireEmptyDirectory(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, fmt.Errorf("%w: prepared authority missing in nonempty store", ErrCorrupt)
		}
		return Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	if selected.Backend != kind {
		return Snapshot{}, ErrMigrationRequired
	}
	name := "state.sqlite3"
	if kind == "jsonl" {
		name = "state.jsonl"
	}
	path := filepath.Join(dir, selected.Generation, name)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return Snapshot{}, fmt.Errorf("%w: prepared data file is missing or empty", ErrCorrupt)
	}
	var source backend
	if kind == "jsonl" {
		file, err := os.Open(path)
		if err != nil {
			return Snapshot{}, err
		}
		source, err = readJSONLFile(file, true)
		if err != nil {
			return Snapshot{}, err
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
			return Snapshot{}, err
		}
		db.SetMaxOpenConns(1)
		source = &sqliteBackend{db: db, path: path}
	}
	defer source.close()
	snapshot, err := source.export(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current, err := readBinding(dir)
	if err != nil {
		return Snapshot{}, err
	}
	if current != selected {
		return Snapshot{}, ErrBindingChanged
	}
	return snapshot, nil
}
func preparedView(ctx context.Context, dir, kind string, query Query) (View, error) {
	if err := validateQuery(query); err != nil {
		return View{}, err
	}
	snapshot, err := preparedSnapshot(ctx, dir, kind)
	if err != nil {
		return View{}, err
	}
	if query.AtRevision != nil && *query.AtRevision != snapshot.Revision {
		return View{}, ErrConflict
	}
	view := View{Revision: snapshot.Revision, Documents: []Document{}}
	for _, document := range snapshot.Documents {
		if matchesQuery(document, query) {
			view.Documents = append(view.Documents, document)
		}
	}
	if query.Reverse {
		slices.Reverse(view.Documents)
	}
	finishPage(&view, query)
	return view, nil
}
