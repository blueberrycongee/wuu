package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (store *documentStore) UpdateIndex(id string, entries int, summary string) error {
	now := time.Now().UTC()
	_, err := store.updateMetadata(id, true, func(s *Session) {
		s.Entries = entries
		s.UpdatedAt = now
		if strings.TrimSpace(summary) != "" && strings.TrimSpace(s.Summary) == "" {
			s.Summary = summary
		}
	})
	return err
}

// UpdateGeneratedTitle sets a title only when the session does not already have one.
func (store *documentStore) UpdateGeneratedTitle(id string, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, fmt.Errorf("title is required")
	}
	return store.updateMetadata(id, false, func(s *Session) {
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	})
}

// UpdateTitle overwrites a session title unconditionally. Differs from
// UpdateGeneratedTitle, which only fills an empty title. The right-click
// Rename menu uses UpdateTitle to overwrite both the auto-generated
// preview and any prior user-edited title.
func (store *documentStore) UpdateTitle(id string, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, fmt.Errorf("title is required")
	}
	return store.updateMetadata(id, false, func(s *Session) {
		s.Title = title
	})
}

// UpdateWorkspaceBinding persists the workspace used by a session after the
// main agent explicitly moves the task into a linked worktree.
func (store *documentStore) UpdateWorkspaceBinding(
	id, cwd, worktreePath, worktreeBaseHEAD, worktreeBaseRepo string,
) (Session, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return Session{}, errors.New("workspace cwd is required")
	}
	return store.updateMetadata(id, false, func(s *Session) {
		s.CWD = cwd
		s.WorktreePath = strings.TrimSpace(worktreePath)
		s.WorktreeBaseHEAD = strings.TrimSpace(worktreeBaseHEAD)
		s.WorktreeBaseRepo = strings.TrimSpace(worktreeBaseRepo)
	})
}

// UpdatePinned marks a session as pinned or unpinned.
func (store *documentStore) UpdatePinned(id string, pinned bool) (Session, error) {
	return store.updateSessionOrganization(id, nil, &pinned)
}

// SetWorkspaceID binds a session to a stable, location-independent workspace
// identity (the desktop's registered-project id), so the session's state and
// thread listing follow the workspace across moves/renames. Set once at
// creation for registered-project threads; left empty for location-anchored
// scratch threads, which match by cwd.
func (store *documentStore) SetWorkspaceID(id, workspaceID string) (Session, error) {
	return store.updateMetadata(id, false, func(s *Session) {
		s.WorkspaceID = strings.TrimSpace(workspaceID)
	})
}

// SetProjectMembership moves an ordinary session into a project, or a
// managed session back out: source and parentID carry the relation, and
// instructions replace the session's create-time instructions.
func (store *documentStore) SetProjectMembership(id, source, parentID, instructions string) (Session, error) {
	return store.updateMetadata(id, false, func(s *Session) {
		s.Source = strings.TrimSpace(source)
		s.ParentID = strings.TrimSpace(parentID)
		s.Instructions = instructions
		if source == "" {
			s.ProjectRole = ""
		}
	})
}

func (store *documentStore) SetSource(id, source string) (Session, error) {
	return store.updateMetadata(id, false, func(s *Session) {
		s.Source = strings.TrimSpace(source)
	})
}

// SetRuntimeSelection persists the runtime defaults pinned to one conversation.
func (store *documentStore) SetRuntimeSelection(id string, selection RuntimeSelection) (Session, error) {
	selection.Provider = strings.TrimSpace(selection.Provider)
	selection.Model = strings.TrimSpace(selection.Model)
	if selection.Provider == "" {
		return Session{}, fmt.Errorf("provider is required")
	}
	var result Session
	err := store.transaction(func(tx *persistence.Tx) error {
		current, err := requireSessionDocument(tx, strings.TrimSpace(id))
		if err != nil {
			return err
		}
		// Engine service identity does not imply a model contract. The selected
		// execution provider validates its options before admitting a turn.
		current.ModelSelectionRevision++
		current.ModelSelectionApplyAt = ""
		current.Provider, current.Model = selection.Provider, selection.Model
		current.Variant, current.Effort = strings.TrimSpace(selection.Variant), strings.TrimSpace(selection.Effort)
		current.Speed, current.ModelOptions = selection.Speed, selection.ModelOptions
		current.PermissionMode, current.ApproveForMe = strings.TrimSpace(selection.PermissionMode), selection.ApproveForMe
		result = current
		return putSessionDocument(tx, current)
	})
	return result, err
}

// SetNextStepModelSelection is separate from SetRuntimeSelection so older
// providers reject an unsupported intent before any legacy selection write.
func (store *documentStore) SetNextStepModelSelection(id string, intent NextStepModelSelection) (Session, error) {
	selection := intent.Selection
	selection.Provider, selection.Model = strings.TrimSpace(selection.Provider), strings.TrimSpace(selection.Model)
	if selection.Provider == "" || selection.Model == "" {
		return Session{}, fmt.Errorf("provider and model are required")
	}
	var result Session
	err := store.transaction(func(tx *persistence.Tx) error {
		current, err := requireSessionDocument(tx, strings.TrimSpace(id))
		if err != nil {
			return err
		}
		if current.ModelSelectionRevision != intent.ExpectedRevision || current.EngineID != intent.ExpectedEngineID {
			return ErrModelSelectionChanged
		}
		if current.PermissionMode != strings.TrimSpace(selection.PermissionMode) || current.ApproveForMe != selection.ApproveForMe {
			return fmt.Errorf("next-step model selection cannot change permissions")
		}
		if current.ModelSelectionRevision == ^uint64(0) {
			return fmt.Errorf("model selection revision exhausted")
		}
		current.ModelSelectionRevision++
		current.ModelSelectionApplyAt = "next_step"
		current.Provider, current.Model = selection.Provider, selection.Model
		current.Variant, current.Effort = strings.TrimSpace(selection.Variant), strings.TrimSpace(selection.Effort)
		current.Speed, current.ModelOptions = selection.Speed, selection.ModelOptions
		result = current
		return putSessionDocument(tx, current)
	})
	if err != nil {
		return Session{}, err
	}
	return result, nil
}

// SetEngine explicitly binds the session to a known engine. Protocol engines
// may persist an empty model to select the native default.
func (store *documentStore) SetEngine(id, engineID string) (Session, error) {
	engineID = strings.TrimSpace(engineID)
	if engineID == "" {
		return Session{}, fmt.Errorf("engine id is required")
	}
	return store.updateMetadata(id, false, func(s *Session) {
		s.EngineID = engineID
	})
}

// SetEngineRef stores the engine's native session reference for a thread
// (for example a codex thread id). It is written once the engine creates the
// native session and read back to resume it.
func (store *documentStore) SetEngineRef(id, ref string) (Session, error) {
	return store.updateMetadata(id, false, func(s *Session) {
		s.EngineRef = strings.TrimSpace(ref)
	})
}

// SetModelSelection preserves the pre-runtime-selection API for callers that
// only know about model variants.
func (store *documentStore) SetModelSelection(id, provider, model, variant string) (Session, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return Session{}, fmt.Errorf("provider is required")
	}
	if model == "" {
		return Session{}, fmt.Errorf("model is required")
	}
	return store.updateMetadata(id, false, func(s *Session) {
		s.ModelSelectionRevision++
		s.ModelSelectionApplyAt = ""
		s.Provider = provider
		s.Model = model
		s.Variant = strings.TrimSpace(variant)
	})
}

// UpdateArchived marks a session as archived or active.
func (store *documentStore) UpdateArchived(id string, archived bool) (Session, error) {
	now := time.Now().UTC()
	return store.updateMetadata(id, false, func(s *Session) {
		if archived {
			s.ArchivedAt = &now
			s.PinnedAt = nil
		} else {
			s.ArchivedAt = nil
			s.ArchiveReason = ""
		}
	})
}

func (store *documentStore) BindWorktree(id string, worktree WorktreeInfo) (Session, error) {
	return store.updateMetadata(id, false, func(s *Session) {
		s.WorktreePath = normalizeCWD(worktree.Path)
		s.WorktreeBaseHEAD = strings.TrimSpace(worktree.BaseHEAD)
		s.WorktreeBaseRepo = normalizeCWD(worktree.BaseRepo)
		if s.CWD == "" {
			s.CWD = s.WorktreePath
		}
	})
}

func (store *documentStore) WorktreeInfoForSession(id string) (WorktreeInfo, bool, error) {
	sess, ok, err := store.Find(id)
	if err != nil || !ok {
		return WorktreeInfo{}, false, err
	}
	info, bound := sess.WorktreeInfo()
	return info, bound, nil
}
