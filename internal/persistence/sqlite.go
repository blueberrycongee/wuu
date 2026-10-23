package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
	_ "modernc.org/sqlite"
)

type sqliteBackend struct{ db *sql.DB }

func openSQLite(path string, create bool) (*sqliteBackend, error) {
	if err := securefs.PreCreateFile(path); err != nil {
		return nil, err
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if !create {
		var revision Revision
		if err := db.QueryRow(`SELECT revision FROM state_metadata WHERE id = 1`).Scan(&revision); err != nil {
			db.Close()
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		return &sqliteBackend{db: db}, nil
	}
	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS state_metadata (id INTEGER PRIMARY KEY CHECK (id = 1), revision INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO state_metadata VALUES (1, 0)`,
		`CREATE TABLE IF NOT EXISTS state_documents (namespace TEXT NOT NULL, key TEXT NOT NULL, value BLOB NOT NULL, PRIMARY KEY (namespace, key))`,
		`CREATE TABLE IF NOT EXISTS state_receipts (id TEXT PRIMARY KEY, revision INTEGER NOT NULL UNIQUE, digest TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &sqliteBackend{db: db}, nil
}

func (b *sqliteBackend) close() error { return b.db.Close() }

func (b *sqliteBackend) read(ctx context.Context, q Query) (View, error) {
	// The caller holds the authority lock across revision and document reads.
	view := View{Documents: []Document{}}
	if err := b.db.QueryRowContext(ctx, `SELECT revision FROM state_metadata WHERE id = 1`).Scan(&view.Revision); err != nil {
		return View{}, err
	}
	if q.AtRevision != nil && *q.AtRevision != view.Revision {
		return View{}, ErrConflict
	}
	if q.Keys != nil && len(q.Keys) == 0 {
		return view, nil
	}
	statement := `SELECT key, value FROM state_documents WHERE namespace = ?`
	args := []any{q.Namespace}
	start, end := keyBounds(q)
	if start != "" {
		statement += ` AND key >= ?`
		args = append(args, start)
	}
	if end != "" {
		statement += ` AND key < ?`
		args = append(args, end)
	}
	if q.Cursor != "" {
		if q.Reverse {
			statement += ` AND key < ?`
		} else {
			statement += ` AND key > ?`
		}
		args = append(args, q.Cursor)
	}
	if q.Keys != nil {
		keys, err := json.Marshal(q.Keys)
		if err != nil {
			return View{}, err
		}
		statement += ` AND key IN (SELECT value FROM json_each(?))`
		args = append(args, string(keys))
	}
	statement += ` ORDER BY key`
	if q.Reverse {
		statement += ` DESC`
	}
	if q.Limit > 0 {
		statement += ` LIMIT ?`
		args = append(args, q.Limit+1)
	}
	rows, err := b.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return View{}, err
	}
	defer rows.Close()
	for rows.Next() {
		d := Document{Namespace: q.Namespace}
		if err := rows.Scan(&d.Key, &d.Value); err != nil {
			return View{}, err
		}
		view.Documents = append(view.Documents, d)
	}
	finishPage(&view, q)
	return view, rows.Err()
}

func (b *sqliteBackend) commit(ctx context.Context, request Transaction, digest string) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	// Cancellation must not cause database/sql to asynchronously roll back a
	// write after durable admission. We check it ourselves before Commit.
	tx, err := b.db.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return Receipt{}, err
	}
	defer tx.Rollback()
	var prior CommitRecord
	err = tx.QueryRow(`SELECT id, revision, digest FROM state_receipts WHERE id = ?`, request.ID).Scan(&prior.ID, &prior.Revision, &prior.Digest)
	if err == nil {
		if prior.Digest != digest {
			return Receipt{}, ErrOperationConflict
		}
		return prior.Receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, err
	}
	var revision Revision
	if err := tx.QueryRow(`SELECT revision FROM state_metadata WHERE id = 1`).Scan(&revision); err != nil {
		return Receipt{}, err
	}
	if revision != request.ExpectedRevision {
		return Receipt{}, ErrConflict
	}
	for _, w := range request.Writes {
		if w.Delete {
			_, err = tx.Exec(`DELETE FROM state_documents WHERE namespace = ? AND key = ?`, w.Namespace, w.Key)
		} else {
			_, err = tx.Exec(`INSERT INTO state_documents (namespace, key, value) VALUES (?, ?, ?) ON CONFLICT(namespace, key) DO UPDATE SET value = excluded.value`, w.Namespace, w.Key, []byte(w.Value))
		}
		if err != nil {
			return Receipt{}, err
		}
	}
	receipt := Receipt{ID: request.ID, Revision: revision + 1}
	if _, err := tx.Exec(`INSERT INTO state_receipts (id, revision, digest) VALUES (?, ?, ?)`, receipt.ID, receipt.Revision, digest); err != nil {
		return Receipt{}, err
	}
	if _, err := tx.Exec(`UPDATE state_metadata SET revision = ? WHERE id = 1`, receipt.Revision); err != nil {
		return Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (b *sqliteBackend) export(ctx context.Context) (Snapshot, error) {
	var integrity string
	if err := b.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if integrity != "ok" {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrCorrupt, integrity)
	}
	s := Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}
	if err := b.db.QueryRowContext(ctx, `SELECT revision FROM state_metadata WHERE id = 1`).Scan(&s.Revision); err != nil {
		return Snapshot{}, err
	}
	rows, err := b.db.QueryContext(ctx, `SELECT namespace, key, value FROM state_documents ORDER BY namespace, key`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.Namespace, &d.Key, &d.Value); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		s.Documents = append(s.Documents, d)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return Snapshot{}, err
	}
	rows, err = b.db.QueryContext(ctx, `SELECT id, revision, digest FROM state_receipts ORDER BY revision`)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r CommitRecord
		if err := rows.Scan(&r.ID, &r.Revision, &r.Digest); err != nil {
			return Snapshot{}, err
		}
		s.Receipts = append(s.Receipts, r)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}
	return s, validateSnapshot(s)
}

func (b *sqliteBackend) restore(ctx context.Context, snapshot Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision Revision
	if err := tx.QueryRow(`SELECT revision FROM state_metadata WHERE id = 1`).Scan(&revision); err != nil {
		return err
	}
	if revision != 0 {
		return fmt.Errorf("%w: import target is not empty", ErrConflict)
	}
	for _, d := range snapshot.Documents {
		if !json.Valid(d.Value) {
			return ErrCorrupt
		}
		if _, err := tx.Exec(`INSERT INTO state_documents (namespace, key, value) VALUES (?, ?, ?)`, d.Namespace, d.Key, []byte(d.Value)); err != nil {
			return err
		}
	}
	for _, r := range snapshot.Receipts {
		if _, err := tx.Exec(`INSERT INTO state_receipts (id, revision, digest) VALUES (?, ?, ?)`, r.ID, r.Revision, r.Digest); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE state_metadata SET revision = ? WHERE id = 1`, snapshot.Revision); err != nil {
		return err
	}
	return tx.Commit()
}
