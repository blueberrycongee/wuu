package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) CreateInitialized(sess Session, records []HistoryRecord) (*Session, error) {
	return s.createInitialized(sess, records, ContextSeed{}, SessionLaunchRecord{}, nil)
}

func (s *Store) CreateInitializedWithLaunch(sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord) (*Session, error) {
	return s.createInitialized(sess, records, seed, launch, nil)
}

func (s *Store) CreateInitializedWithInbox(sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord, input InboxMessage) (*Session, error) {
	return s.createInitialized(sess, records, seed, launch, &input)
}

func (s *Store) createInitialized(sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord, input *InboxMessage) (*Session, error) {
	now := time.Now().UTC()
	sess = normalizeNewSession(sess, now)
	hasSeed := seed.Version != 0 || strings.TrimSpace(seed.Body) != ""
	if hasSeed {
		seed = prepareContextSeed(seed, now)
	}
	var result Session
	err := s.transaction(func(tx *persistence.Tx) error {
		current := sess
		initial := records
		if strings.TrimSpace(launch.RequestID) != "" {
			var existing SessionLaunchRecord
			found, err := tx.Get(documentNamespace("session_launches"), launch.RequestID, &existing)
			if err != nil {
				return err
			}
			if found && existing.Status == SessionLaunchCommitted && existing.TargetSession != "" {
				if existing.Revision != launch.Revision || !existing.Runtime.equal(launch.Runtime) || existing.SourceSession != launch.SourceSession || existing.SourceCutoff != launch.SourceCutoff {
					return fmt.Errorf("%w: request %q", ErrSessionLaunchConflict, launch.RequestID)
				}
				loaded, found, err := getSessionDocument(tx, existing.TargetSession)
				if err != nil {
					return err
				}
				if found {
					result = loaded
					return nil
				}
			}
		}
		if hasSeed {
			written, err := putContextSeedDocument(tx, seed)
			if err != nil {
				return err
			}
			current.SeedID = written.ID
			if strings.TrimSpace(current.ContextSource) == "" {
				current.ContextSource = "seed"
			}
			initial = append([]HistoryRecord{seedHistoryRecord(written)}, records...)
		}
		if _, found, err := getSessionDocument(tx, current.ID); err != nil {
			return err
		} else if found {
			return fmt.Errorf("session %q already exists", current.ID)
		}
		if strings.TrimSpace(current.Owner) != "" && strings.TrimSpace(current.CreationRequestID) != "" {
			sessions, err := readDocumentRows[Session](tx, "sessions", "")
			if err != nil {
				return err
			}
			for _, prior := range sessions {
				if prior.Owner == strings.TrimSpace(current.Owner) && prior.CreationRequestID == strings.TrimSpace(current.CreationRequestID) {
					return fmt.Errorf("%w: owner %q request %q already created session %q", ErrSessionLaunchConflict, prior.Owner, prior.CreationRequestID, prior.ID)
				}
			}
		}
		if err := putSessionDocument(tx, current); err != nil {
			return err
		}
		if current.ForkedFromID != "" {
			var binding json.RawMessage
			found, err := tx.Get(documentNamespace("session_driver_bindings"), current.ForkedFromID, &binding)
			if err != nil {
				return err
			}
			if found {
				if err := tx.Put(documentNamespace("session_driver_bindings"), current.ID, binding); err != nil {
					return err
				}
			}
		}
		if input != nil {
			control := Control{SessionID: current.ID, ManagerID: current.ParentID, Revision: 1, State: ControlActive}
			if control.ManagerID == "" {
				return errors.New("initial dispatch needs a manager")
			}
			if err := tx.Put(documentNamespace("session_controls"), current.ID, control); err != nil {
				return err
			}
			message := *input
			message.SessionID = current.ID
			message.Controls = append(append([]Control(nil), message.Controls...), control)
			if err := insertInboxDocument(tx, message, now); err != nil {
				return err
			}
		}
		for index, record := range initial {
			if err := insertHistoryDocument(tx, current.ID, index+1, record, now); err != nil {
				return err
			}
		}
		if err := tx.Put(documentNamespace("session_history_heads"), current.ID, len(initial)); err != nil {
			return err
		}
		if strings.TrimSpace(launch.RequestID) != "" {
			committed := launch
			committed.TargetSession, committed.SeedID, committed.Status = current.ID, current.SeedID, SessionLaunchCommitted
			if _, err := putSessionLaunchDocument(tx, prepareSessionLaunch(committed, now)); err != nil {
				return err
			}
		}
		// History projection may have updated the completed-turn index.
		var err error
		result, err = requireSessionDocument(tx, current.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (store *Store) Create(id ...string) (*Session, error) {
	sessID := ""
	if len(id) > 0 {
		sessID = id[0]
	}
	return store.CreateWithMetadata(sessID, "")
}

// CreateWithMetadata initializes a new session with thread-level metadata.
// The workspace identity is bound separately via SetWorkspaceID, so this
// signature stays stable.
func (store *Store) CreateWithMetadata(id, cwd string) (*Session, error) {
	return store.createWithMetadata(id, cwd, ForkMetadata{}, ManagedMetadata{})
}

func (store *Store) CreateManagedWithMetadata(id, cwd string, managed ManagedMetadata) (*Session, error) {
	return store.createWithMetadata(id, cwd, ForkMetadata{}, managed)
}

// CreateForkWithMetadata initializes a forked session with source metadata.
func (store *Store) CreateForkWithMetadata(id, cwd string, fork ForkMetadata) (*Session, error) {
	return store.createWithMetadata(id, cwd, fork, ManagedMetadata{})
}

func (store *Store) CreateManagedForkWithMetadata(id, cwd string, fork ForkMetadata, managed ManagedMetadata) (*Session, error) {
	return store.createWithMetadata(id, cwd, fork, managed)
}

// CreateWithWorktree initializes a session bound to an isolated git worktree.
// Fork metadata is empty for a conversation that starts in a new worktree.
func (store *Store) CreateWithWorktree(id, cwd string, fork ForkMetadata, worktree WorktreeInfo) (*Session, error) {
	return store.createWithMetadataAndWorktree(id, cwd, fork, worktree, ManagedMetadata{})
}

// CreateManagedWithWorktree initializes a plugin-owned session in an isolated
// workspace while preserving the same ownership and idempotency metadata as
// every other host.session.create call.
func (store *Store) CreateManagedWithWorktree(id, cwd string, fork ForkMetadata, worktree WorktreeInfo, managed ManagedMetadata) (*Session, error) {
	return store.createWithMetadataAndWorktree(id, cwd, fork, worktree, managed)
}

func (store *Store) createWithMetadata(id, cwd string, fork ForkMetadata, managed ManagedMetadata) (*Session, error) {
	return store.createWithMetadataAndWorktree(id, cwd, fork, WorktreeInfo{}, managed)
}

func (store *Store) createWithMetadataAndWorktree(id, cwd string, fork ForkMetadata, worktree WorktreeInfo, managed ManagedMetadata) (*Session, error) {
	sessID := NewID()
	if strings.TrimSpace(id) != "" {
		sessID = strings.TrimSpace(id)
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:               sessID,
		CreatedAt:        now,
		UpdatedAt:        now,
		CWD:              normalizeCWD(cwd),
		ForkedFromID:     strings.TrimSpace(fork.ForkedFromID),
		ForkedFromTurnID: strings.TrimSpace(fork.ForkedFromTurnID),
		ForkedFromItemID: strings.TrimSpace(fork.ForkedFromItemID),
		WorktreePath:     normalizeCWD(worktree.Path),
		WorktreeBaseHEAD: strings.TrimSpace(worktree.BaseHEAD),
		WorktreeBaseRepo: normalizeCWD(worktree.BaseRepo),
		Owner:            strings.TrimSpace(managed.Owner), Visibility: strings.TrimSpace(managed.Visibility),
		ParentID: strings.TrimSpace(managed.ParentID), ContextSource: strings.TrimSpace(managed.ContextSource),
		CreationRequestID: strings.TrimSpace(managed.CreationRequestID),
	}
	return store.CreateInitialized(*sess, nil)
}
