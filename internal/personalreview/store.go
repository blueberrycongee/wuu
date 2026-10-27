package personalreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Store is an isolated host-owned ledger. Never put it in plugin/model-writable
// storage. Each state transition and its audit event commit in one transaction.
type Store struct {
	db               *sql.DB
	documents        persistence.Documents
	closed           atomic.Bool
	ownershipMu      sync.Mutex
	ownershipClaimed bool
}

func Open(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
CREATE TABLE IF NOT EXISTS personal_review_actions (space TEXT NOT NULL,id TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(space,id));
CREATE TABLE IF NOT EXISTS personal_review_events (space TEXT NOT NULL,id TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(space,id,revision));
CREATE TABLE IF NOT EXISTS personal_review_settings (space TEXT PRIMARY KEY,revision INTEGER NOT NULL,policy_version TEXT NOT NULL,auto_review_enabled INTEGER NOT NULL CHECK(auto_review_enabled IN (0,1)));
CREATE TABLE IF NOT EXISTS personal_review_settings_events (space TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(space,revision));`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error {
	if s.documents != nil {
		s.closed.Store(true)
		return nil
	}
	return s.db.Close()
}
func (s *Store) get(ctx context.Context, key Key) (Record, error) {
	if s.documents != nil {
		return s.documentGet(ctx, key)
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM personal_review_actions WHERE space=? AND id=?`, key.SpaceID, key.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err = json.Unmarshal(raw, &r); err != nil {
		return Record{}, err
	}
	if r.Action.Key() != key || r.Revision < 1 {
		return Record{}, errors.New("invalid review ledger identity")
	}
	return r, nil
}
func (s *Store) save(ctx context.Context, r Record, expected int64, actor string, at time.Time) (Record, error) {
	if s.documents != nil {
		return s.documentSave(ctx, r, expected, actor, at)
	}
	r.Revision = expected + 1
	raw, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	event, err := json.Marshal(Event{Revision: r.Revision, Status: r.Status, Outcome: r.Outcome, Reason: r.Reason, Actor: actor, At: at})
	if err != nil {
		return Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO personal_review_actions(space,id,revision,body) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, r.Action.Scope.SpaceID, r.Action.ID, r.Revision, raw)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE personal_review_actions SET revision=?,body=? WHERE space=? AND id=? AND revision=?`, r.Revision, raw, r.Action.Scope.SpaceID, r.Action.ID, expected)
	}
	if err != nil {
		return Record{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Record{}, err
	}
	if n != 1 {
		return Record{}, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_review_events(space,id,revision,body) VALUES(?,?,?,?)`, r.Action.Scope.SpaceID, r.Action.ID, r.Revision, event)
	if err != nil {
		return Record{}, err
	}
	if err = tx.Commit(); err != nil {
		return Record{}, err
	}
	return r, nil
}
func (s *Store) Events(ctx context.Context, key Key) ([]Event, error) {
	if s.documents != nil {
		return documentRows[Event](ctx, s, reviewEvents, reviewKey(key)+"/")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM personal_review_events WHERE space=? AND id=? ORDER BY revision`, key.SpaceID, key.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var raw []byte
		var event Event
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
func (s *Store) keys(ctx context.Context) ([]Key, error) {
	if s.documents != nil {
		records, err := documentRows[Record](ctx, s, reviewActions, "")
		keys := make([]Key, 0, len(records))
		for _, r := range records {
			keys = append(keys, r.Action.Key())
		}
		return keys, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT space,id FROM personal_review_actions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Key{}
	for rows.Next() {
		var key Key
		if err = rows.Scan(&key.SpaceID, &key.ID); err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, rows.Err()
}
