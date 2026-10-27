package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// documentStore is the session domain bound to one selected document provider. It does
// not open a database or choose a backend. Its owner retains the generation and
// supplies an operation context; a retired binding cannot reopen local files.
type documentStore struct {
	ctx       context.Context
	documents persistence.Documents
}

func newDocumentStore(ctx context.Context, documents persistence.Documents) *documentStore {
	return &documentStore{ctx: ctx, documents: documents}
}

func (s *documentStore) WithContext(ctx context.Context) *documentStore {
	return &documentStore{ctx: ctx, documents: s.documents}
}

func documentNamespace(table string) string { return "wuu." + table + ".v1" }

// Each component is encoded independently so prefix scans never confuse a
// caller-supplied ID containing a slash with a child of a different ID.
func documentPrefix(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id)) + "/"
}

func (s *documentStore) transaction(body func(*persistence.Tx) error) error {
	return persistence.Update(s.ctx, s.documents, body)
}

func readDocumentRows[T any](tx *persistence.Tx, table, prefix string) ([]T, error) {
	query := persistence.Query{Namespace: documentNamespace(table), Prefix: prefix, Limit: 256}
	var rows []T
	for {
		view, err := tx.ReadView(query)
		if err != nil {
			return nil, err
		}
		for _, document := range view.Documents {
			var row T
			if err := json.Unmarshal(document.Value, &row); err != nil {
				return nil, fmt.Errorf("%w: decode %s/%s: %v", persistence.ErrCorrupt, table, document.Key, err)
			}
			rows = append(rows, row)
		}
		if view.NextCursor == "" {
			break
		}
		query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
	}

	return rows, nil
}

func getSessionDocument(tx *persistence.Tx, id string) (Session, bool, error) {
	var sess Session
	found, err := tx.Get(documentNamespace("sessions"), id, &sess)
	return sess, found, err
}

func requireSessionDocument(tx *persistence.Tx, id string) (Session, error) {
	sess, found, err := getSessionDocument(tx, id)
	if err != nil {
		return Session{}, err
	}
	if !found {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	return sess, nil
}

func putSessionDocument(tx *persistence.Tx, sess Session) error {
	sess.CWD = normalizeCWD(sess.CWD)
	sess.WorktreePath = normalizeCWD(sess.WorktreePath)
	sess.WorktreeBaseRepo = normalizeCWD(sess.WorktreeBaseRepo)
	for _, field := range []*string{&sess.FolderID, &sess.WorkspaceID, &sess.Source, &sess.Owner, &sess.Visibility,
		&sess.ParentID, &sess.ContextSource, &sess.CreationRequestID, &sess.Provider, &sess.Model, &sess.Variant,
		&sess.Effort, &sess.Speed, &sess.PermissionMode, &sess.EngineID, &sess.EngineRef, &sess.ToolPolicyJSON} {
		*field = strings.TrimSpace(*field)
	}
	var indexVersion int
	indexExists, err := tx.Get(documentNamespace("session_indexes"), "listing", &indexVersion)
	if err != nil {
		return err
	}
	if indexExists {
		if err := checkSessionIndexVersion(indexVersion); err != nil {
			return err
		}
	}
	prior, found, err := getSessionDocument(tx, sess.ID)
	if err != nil {
		return err
	}
	if found {
		if err := tx.Delete(documentNamespace("session_order"), sessionOrderKey(prior)); err != nil {
			return err
		}
	} else {
		existing, err := tx.Read(persistence.Query{Namespace: documentNamespace("sessions"), Limit: 1})
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			if err := tx.Put(documentNamespace("session_indexes"), "listing", sessionListingIndexVersion); err != nil {
				return err
			}
		}
	}
	if err := tx.Put(documentNamespace("session_order"), sessionOrderKey(sess), sess.ID); err != nil {
		return err
	}
	return tx.Put(documentNamespace("sessions"), sess.ID, sess)
}

func (s *documentStore) Find(id string) (Session, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, false, nil
	}
	var result Session
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		result, found, err = getSessionDocument(tx, id)
		return err
	})
	return result, found, err
}

func (s *documentStore) List(limit int) ([]Session, error) {
	return s.listSessions(limit, func(Session) bool { return true })
}

func (s *documentStore) ListForCWD(cwd, workspaceID string, limit int) ([]Session, error) {
	return s.listSessions(limit, sessionListMatches(cwd, workspaceID))
}

func (s *documentStore) listSessions(limit int, matches func(Session) bool) ([]Session, error) {
	for {
		var result []Session
		var cursor *SessionListCursor
		for {
			count := 256
			if limit > 0 {
				count = min(limit-len(result), count)
			}
			page, err := s.readSessionListPage(count, cursor, matches)
			if errors.Is(err, persistence.ErrConflict) {
				break
			}
			if err != nil {
				return nil, err
			}
			result = append(result, page.Sessions...)
			if page.Next == nil || limit > 0 && len(result) == limit {
				return result, nil
			}
			cursor = page.Next
		}
	}
}

func (s *documentStore) FindPinnedArchived(ids []string) (map[string]PinnedArchived, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			keys = append(keys, id)
		}
	}
	view, err := s.documents.Read(s.ctx, persistence.Query{Namespace: documentNamespace("sessions"), Keys: keys})
	if err != nil {
		return nil, err
	}
	result := make(map[string]PinnedArchived, len(view.Documents))
	for _, document := range view.Documents {
		var sess Session
		if err := json.Unmarshal(document.Value, &sess); err != nil {
			return nil, fmt.Errorf("%w: decode session %s: %v", persistence.ErrCorrupt, document.Key, err)
		}
		result[sess.ID] = PinnedArchived{Pinned: sess.PinnedAt != nil, Archived: sess.ArchivedAt != nil}
	}
	return result, nil
}

func (s *documentStore) FindManagedByRequest(owner, requestID string) (Session, bool, error) {
	owner, requestID = strings.TrimSpace(owner), strings.TrimSpace(requestID)
	if owner == "" || requestID == "" {
		return Session{}, false, nil
	}
	rows, err := s.listSessions(1, func(sess Session) bool { return sess.Owner == owner && sess.CreationRequestID == requestID })
	if err != nil || len(rows) == 0 {
		return Session{}, false, err
	}
	return rows[0], true, nil
}

func (s *documentStore) updateMetadata(id string, missingOK bool, update func(*Session)) (Session, error) {
	id = strings.TrimSpace(id)
	var result Session
	err := s.transaction(func(tx *persistence.Tx) error {
		current, found, err := getSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if !found {
			result = Session{}
			if missingOK {
				return nil
			}
			return fmt.Errorf("%w: %q", ErrSessionNotFound, id)
		}
		previousTitle := current.Title
		update(&current)
		if current.Title != previousTitle {
			current.TitleRevision++
		}
		if err := putSessionDocument(tx, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}

func (s *documentStore) MostRecent() (string, error) {
	rows, err := s.List(1)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].ID, nil
}

func (s *documentStore) MostRecentForCWD(cwd, workspaceID string) (string, error) {
	rows, err := s.ListForCWD(cwd, workspaceID, 1)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].ID, nil
}

func normalizeNewSession(sess Session, now time.Time) Session {
	sess.ID = strings.TrimSpace(sess.ID)
	if sess.ID == "" {
		sess.ID = NewID()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = now
	}
	return sess
}
