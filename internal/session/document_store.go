package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Store is the session domain bound to one selected document provider. It does
// not open a database or choose a backend. Its owner retains the generation and
// supplies an operation context; a retired binding cannot reopen local files.
type Store struct {
	ctx       context.Context
	documents persistence.Documents
}

func NewStore(ctx context.Context, documents persistence.Documents) *Store {
	return &Store{ctx: ctx, documents: documents}
}

func (s *Store) WithContext(ctx context.Context) *Store {
	return &Store{ctx: ctx, documents: s.documents}
}

func documentNamespace(table string) string { return "wuu." + table + ".v1" }

// Each component is encoded independently so prefix scans never confuse a
// caller-supplied ID containing a slash with a child of a different ID.
func documentPrefix(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id)) + "/"
}

func (s *Store) transaction(body func(*persistence.Tx) error) error {
	return persistence.Update(s.ctx, s.documents, body)
}

func readDocumentRows[T any](tx *persistence.Tx, table, prefix string) ([]T, error) {
	documents, err := tx.Read(persistence.Query{Namespace: documentNamespace(table), Prefix: prefix})
	if err != nil {
		return nil, err
	}
	rows := make([]T, 0, len(documents))
	for _, document := range documents {
		var row T
		if err := json.Unmarshal(document.Value, &row); err != nil {
			return nil, fmt.Errorf("%w: decode %s/%s: %v", persistence.ErrCorrupt, table, document.Key, err)
		}
		rows = append(rows, row)
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
	return tx.Put(documentNamespace("sessions"), sess.ID, sess)
}

func (s *Store) Find(id string) (Session, bool, error) {
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

func (s *Store) List(limit int) ([]Session, error) {
	return s.listSessions(limit, func(Session) bool { return true })
}

func (s *Store) ListForCWD(cwd, workspaceID string, limit int) ([]Session, error) {
	target, workspaceID := normalizeCWD(cwd), strings.TrimSpace(workspaceID)
	return s.listSessions(limit, func(sess Session) bool {
		if target == "" && workspaceID == "" {
			return true
		}
		if workspaceID != "" {
			return sess.WorkspaceID == workspaceID || (sess.WorkspaceID == "" && target != "" && (sess.CWD == target || sess.WorktreeBaseRepo == target))
		}
		return sess.CWD == target || sess.WorktreeBaseRepo == target
	})
}

func (s *Store) listSessions(limit int, matches func(Session) bool) ([]Session, error) {
	var result []Session
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[Session](tx, "sessions", "")
		if err != nil {
			return err
		}
		result = make([]Session, 0, len(rows))
		for _, sess := range rows {
			if matches(sess) {
				result = append(result, sess)
			}
		}
		return nil
	})
	sortSessions(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, err
}

func (s *Store) FindPinnedArchived(ids []string) (map[string]PinnedArchived, error) {
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

func (s *Store) FindManagedByRequest(owner, requestID string) (Session, bool, error) {
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

func (s *Store) updateMetadata(id string, missingOK bool, update func(*Session)) (Session, error) {
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

func (s *Store) MostRecent() (string, error) {
	rows, err := s.List(1)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].ID, nil
}

func (s *Store) MostRecentForCWD(cwd, workspaceID string) (string, error) {
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
