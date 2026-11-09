package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/blueberrycongee/wuu/internal/remote/conversations"
)

var errSyncConflict = errors.New("conversation sync changed; refresh and retry")

// The host row and requesting device remain locked through the operation, so
// revocation cannot race an upload or a read. The sync lock orders revisions by
// commit, preventing a delta cursor from skipping an uncommitted earlier write.
func (s *Store) syncTransaction(ctx context.Context, d Device, host string) (*sql.Tx, conversations.Settings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, conversations.Settings{}, ErrUnavailable
	}
	var caller string
	err = tx.QueryRowContext(ctx, `SELECT account FROM devices WHERE pub=$1 FOR SHARE`, d.Pub).Scan(&caller)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, conversations.Settings{}, ErrUnavailable
	}
	if err != nil || caller != d.Account {
		tx.Rollback()
		return nil, conversations.Settings{}, ErrUnauthorized
	}
	var owner, role string
	err = tx.QueryRowContext(ctx, `SELECT account,role FROM devices WHERE pub=$1 FOR SHARE`, host).Scan(&owner, &role)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, conversations.Settings{}, ErrUnavailable
	}
	if err != nil || owner != d.Account || role != "host" {
		tx.Rollback()
		return nil, conversations.Settings{}, ErrUnauthorized
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conversation_sync(host,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, host, randomToken()); err != nil {
		tx.Rollback()
		return nil, conversations.Settings{}, ErrUnavailable
	}
	state := conversations.Settings{Host: host}
	err = tx.QueryRowContext(ctx, `SELECT enabled,generation FROM conversation_sync WHERE host=$1 FOR UPDATE`, host).Scan(&state.Enabled, &state.Generation)
	if err != nil {
		tx.Rollback()
		return nil, state, ErrUnavailable
	}
	return tx, state, nil
}

func (s *Store) ConversationSettings(ctx context.Context, d Device, host string, enabled *bool) (conversations.Settings, error) {
	tx, state, err := s.syncTransaction(ctx, d, host)
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	if enabled != nil && *enabled != state.Enabled {
		state.Enabled, state.Generation = *enabled, randomToken()
		if _, err = tx.ExecContext(ctx, `DELETE FROM conversation_copies WHERE host=$1`, host); err != nil {
			return state, ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conversation_sync SET enabled=$2,generation=$3,revision=0 WHERE host=$1`, host, state.Enabled, state.Generation); err != nil {
			return state, ErrUnavailable
		}
	}
	return state, commitConversation(tx)
}

func (s *Store) PutConversation(ctx context.Context, d Device, in conversations.Mutation) (conversations.Entry, error) {
	var result conversations.Entry
	if d.Role != "host" {
		return result, ErrUnauthorized
	}
	if err := in.Thread.Validate(); err != nil {
		return result, err
	}
	tx, state, err := s.syncTransaction(ctx, d, d.Pub)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if !state.Enabled || in.Generation != state.Generation {
		return result, errSyncConflict
	}
	var previous int64
	var previousDigest string
	var previousDeleted bool
	err = tx.QueryRowContext(ctx, `SELECT revision,digest,body IS NULL FROM conversation_copies WHERE host=$1 AND id=$2`, d.Pub, in.Thread.ID).Scan(&previous, &previousDigest, &previousDeleted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, ErrUnavailable
	}
	if in.Expected != strconv.FormatInt(previous, 10) {
		return result, errSyncConflict
	}
	if in.Thread.Messages == nil {
		in.Thread.Messages = []conversations.Message{}
	}
	body, _ := json.Marshal(in.Thread)
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	if in.Deleted {
		digest = ""
		in.Thread.Title = ""
		in.Thread.UpdatedAt = ""
	}
	result = conversations.Entry{ID: in.Thread.ID, Title: in.Thread.Title, UpdatedAt: in.Thread.UpdatedAt, Digest: digest, Deleted: in.Deleted, Revision: strconv.FormatInt(previous, 10)}
	if previous != 0 && previousDigest == digest && previousDeleted == in.Deleted {
		return result, commitConversation(tx)
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `UPDATE conversation_sync SET revision=revision+1 WHERE host=$1 RETURNING revision`, d.Pub).Scan(&revision); err != nil {
		return result, ErrUnavailable
	}
	var stored any = string(body)
	if in.Deleted {
		stored = nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_copies(host,id,revision,title,updated_at,digest,body) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(host,id) DO UPDATE SET revision=excluded.revision,title=excluded.title,updated_at=excluded.updated_at,digest=excluded.digest,body=excluded.body`, d.Pub, in.Thread.ID, revision, in.Thread.Title, in.Thread.UpdatedAt, digest, stored)
	if err != nil {
		return result, ErrUnavailable
	}
	result.Revision = strconv.FormatInt(revision, 10)
	return result, commitConversation(tx)
}

func (s *Store) ConversationChanges(ctx context.Context, d Device, host, generation string, after int64) (conversations.Changes, error) {
	tx, state, err := s.syncTransaction(ctx, d, host)
	result := conversations.Changes{Settings: state, Entries: []conversations.Entry{}, Cursor: "0"}
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if generation != state.Generation {
		after = 0
	}
	result.Cursor = strconv.FormatInt(after, 10)
	rows, err := tx.QueryContext(ctx, `SELECT id,title,updated_at,revision,digest,body IS NULL FROM conversation_copies WHERE host=$1 AND revision>$2 ORDER BY revision LIMIT 101`, host, after)
	if err != nil {
		return result, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var entry conversations.Entry
		var revision int64
		if err := rows.Scan(&entry.ID, &entry.Title, &entry.UpdatedAt, &revision, &entry.Digest, &entry.Deleted); err != nil {
			return result, ErrUnavailable
		}
		if len(result.Entries) == 100 {
			result.More = true
			break
		}
		entry.Revision = strconv.FormatInt(revision, 10)
		result.Entries = append(result.Entries, entry)
		result.Cursor = entry.Revision
	}
	if rows.Err() != nil {
		return result, ErrUnavailable
	}
	rows.Close()
	return result, commitConversation(tx)
}

func (s *Store) Conversation(ctx context.Context, d Device, host, generation, id string) (json.RawMessage, string, error) {
	tx, state, err := s.syncTransaction(ctx, d, host)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	if !state.Enabled || state.Generation != generation {
		return nil, "", errSyncConflict
	}
	var body []byte
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT body,revision FROM conversation_copies WHERE host=$1 AND id=$2 AND body IS NOT NULL`, host, id).Scan(&body, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", sql.ErrNoRows
	}
	if err != nil {
		return nil, "", ErrUnavailable
	}
	return body, strconv.FormatInt(revision, 10), commitConversation(tx)
}

func commitConversation(tx *sql.Tx) error {
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
