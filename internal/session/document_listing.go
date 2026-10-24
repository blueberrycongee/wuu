package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

const sessionListingIndexVersion = 1

func checkSessionIndexVersion(version int) error {
	if version != sessionListingIndexVersion {
		return fmt.Errorf("%w: unsupported session listing index version %d", persistence.ErrMigrationRequired, version)
	}
	return nil
}

// SessionListCursor pins a page to the selected provider revision. Any write
// invalidates it; callers restart the list rather than silently mixing pages.
type SessionListCursor struct {
	Key      string               `json:"key"`
	Revision persistence.Revision `json:"revision,string"`
}
type SessionListQuery struct {
	CWD         string
	WorkspaceID string
	Limit       int
	Cursor      *SessionListCursor
}
type SessionListPage struct {
	Sessions []Session
	Next     *SessionListCursor
}

func sessionOrderKey(sess Session) string {
	pinned := "0"
	if sess.PinnedAt != nil {
		pinned = "1"
	}
	return pinned + "/" + sessionActivityAt(sess).UTC().Format("2006-01-02T15:04:05.000000000Z") + "/" + sess.ID
}

// RebuildSessionIndexes explicitly upgrades pre-index document stores. The
// index and its version marker publish atomically with the captured revision.
// Ordinary reads never fall back to a complete session scan.
func (s *Store) RebuildSessionIndexes() error {
	return s.transaction(func(tx *persistence.Tx) error {
		var version int
		found, err := tx.Get(documentNamespace("session_indexes"), "listing", &version)
		if err != nil {
			return err
		}
		if found {
			return checkSessionIndexVersion(version)
		}
		if err := deleteDocumentPrefix(tx, "session_order", ""); err != nil {
			return err
		}
		rows, err := readDocumentRows[Session](tx, "sessions", "")
		if err != nil {
			return err
		}
		for _, sess := range rows {
			if err := tx.Put(documentNamespace("session_order"), sessionOrderKey(sess), sess.ID); err != nil {
				return err
			}
		}
		return tx.Put(documentNamespace("session_indexes"), "listing", sessionListingIndexVersion)
	})
}

func sessionListMatches(cwd, workspaceID string) func(Session) bool {
	target, workspaceID := normalizeCWD(cwd), strings.TrimSpace(workspaceID)
	return func(sess Session) bool {
		if target == "" && workspaceID == "" {
			return true
		}
		if workspaceID != "" {
			return sess.WorkspaceID == workspaceID || (sess.WorkspaceID == "" && target != "" && (sess.CWD == target || sess.WorktreeBaseRepo == target))
		}
		return sess.CWD == target || sess.WorktreeBaseRepo == target
	}
}

func (s *Store) ListPage(query SessionListQuery) (SessionListPage, error) {
	if query.Cursor != nil && query.Cursor.Key == "" {
		return SessionListPage{}, fmt.Errorf("%w: session page cursor key is required", persistence.ErrInvalid)
	}
	if query.Limit < 1 || query.Limit > persistence.MaxReadLimit {
		return SessionListPage{}, fmt.Errorf("%w: session page limit must be 1..%d", persistence.ErrInvalid, persistence.MaxReadLimit)
	}
	for {
		page, err := s.readSessionListPage(query.Limit, query.Cursor, sessionListMatches(query.CWD, query.WorkspaceID))
		if query.Cursor != nil || !errors.Is(err, persistence.ErrConflict) {
			return page, err
		}
	}
}

func (s *Store) readSessionListPage(limit int, cursor *SessionListCursor, matches func(Session) bool) (SessionListPage, error) {
	var result SessionListPage
	versionQuery := persistence.Query{Namespace: documentNamespace("session_indexes"), Keys: []string{"listing"}}
	if cursor != nil {
		versionQuery.AtRevision = &cursor.Revision
	}
	version, err := s.documents.Read(s.ctx, versionQuery)
	if err != nil {
		return result, err
	}
	if len(version.Documents) == 0 {
		existing, err := s.documents.Read(s.ctx, persistence.Query{Namespace: documentNamespace("sessions"), Limit: 1, AtRevision: &version.Revision})
		if err != nil {
			return result, err
		}
		if len(existing.Documents) != 0 {
			return result, fmt.Errorf("%w: rebuild session listing indexes before reading", persistence.ErrMigrationRequired)
		}
		return result, nil
	}
	var indexVersion int
	if err := json.Unmarshal(version.Documents[0].Value, &indexVersion); err != nil {
		return result, err
	}
	if err := checkSessionIndexVersion(indexVersion); err != nil {
		return result, err
	}
	query := persistence.Query{Namespace: documentNamespace("session_order"), Reverse: true, Limit: min(limit, 256), AtRevision: &version.Revision}
	if cursor != nil {
		query.Cursor = cursor.Key
	}
	for {
		view, err := s.documents.Read(s.ctx, query)
		if err != nil {
			return result, err
		}
		keys := make([]string, 0, len(view.Documents))
		for _, document := range view.Documents {
			var id string
			if err := json.Unmarshal(document.Value, &id); err != nil {
				return result, err
			}
			keys = append(keys, id)
		}
		rows, err := s.documents.Read(s.ctx, persistence.Query{Namespace: documentNamespace("sessions"), Keys: keys, AtRevision: &version.Revision})
		if err != nil {
			return result, err
		}
		sessions := make(map[string]Session, len(rows.Documents))
		for _, document := range rows.Documents {
			var sess Session
			if err := json.Unmarshal(document.Value, &sess); err != nil {
				return result, err
			}
			sessions[sess.ID] = sess
		}
		for i, id := range keys {
			sess, found := sessions[id]
			if !found {
				return result, fmt.Errorf("%w: session listing index references missing session", persistence.ErrCorrupt)
			}
			if !matches(sess) {
				continue
			}
			result.Sessions = append(result.Sessions, sess)
			if len(result.Sessions) == limit {
				if i+1 < len(keys) || view.NextCursor != "" {
					result.Next = &SessionListCursor{Key: view.Documents[i].Key, Revision: version.Revision}
				}
				return result, nil
			}
		}
		if view.NextCursor == "" {
			return result, nil
		}
		query.Cursor = view.NextCursor
		query.Limit = min(limit-len(result.Sessions), 256)
	}
}
