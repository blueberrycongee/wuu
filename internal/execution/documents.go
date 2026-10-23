package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	runsNamespace          = "wuu.execution_runs.v1"
	activeThreadsNamespace = "wuu.execution_active_threads.v1"
	turnOwnersNamespace    = "wuu.execution_turn_owners.v1"
	runOrderNamespace      = "wuu.execution_run_order.v1"
)

// Store retains the selected authority supplied by its runtime owner.
// Ephemeral runs remain local; durable runs and their admission indexes commit
// together, including all canonical turn references.
type Store struct {
	documents persistence.Documents
	mu        sync.Mutex
	memory    map[string]Run
}

func NewStore(documents persistence.Documents) (*Store, error) {
	if documents == nil {
		return nil, errors.New("execution document authority is required")
	}
	return &Store{documents: documents, memory: make(map[string]Run)}, nil
}

func (s *Store) Create(ctx context.Context, params CreateParams) (Run, error) {
	if s == nil {
		return Run{}, errors.New("execution store is required")
	}
	params.RuntimeID, params.ThreadID = strings.TrimSpace(params.RuntimeID), strings.TrimSpace(params.ThreadID)
	params.Request, params.Runtime, params.Workspace = normalizeRequest(params.Request), normalizeRuntime(params.Runtime), normalizeWorkspace(params.Workspace)
	if err := validateManifest(params); err != nil {
		return Run{}, err
	}
	now := time.Now().UTC()
	run := Run{ID: "run-" + session.NewID(), RuntimeID: params.RuntimeID, Status: StatusAccepted, Request: params.Request, Runtime: params.Runtime,
		Workspace: params.Workspace, ThreadID: params.ThreadID, Turns: []TurnRef{}, Ephemeral: params.Ephemeral, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.Ephemeral {
		for _, existing := range s.memory {
			if existing.ThreadID == run.ThreadID && !existing.Status.Terminal() {
				return Run{}, fmt.Errorf("%w: thread %q already has active run %q", ErrConflict, run.ThreadID, existing.ID)
			}
		}
		s.memory[run.ID] = cloneRun(run)
		return run, nil
	}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		if _, err := session.ReadInferenceRuntime(tx, run.RuntimeID); err != nil {
			return err
		}
		return persistRunDocument(tx, run)
	})
	return run, err
}

func getRunDocument(tx *persistence.Tx, id string) (Run, error) {
	var run Run
	found, err := tx.Get(runsNamespace, id, &run)
	if err != nil {
		return Run{}, err
	}
	if !found {
		return Run{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return run, nil
}

func persistRunDocument(tx *persistence.Tx, run Run) error {
	var previous Run
	found, err := tx.Get(runsNamespace, run.ID, &previous)
	if err != nil {
		return err
	}
	if found {
		for _, key := range runOrderKeys(previous) {
			tx.Delete(runOrderNamespace, key)
		}
	}
	for _, key := range runOrderKeys(run) {
		if err := tx.Put(runOrderNamespace, key, run.ID); err != nil {
			return err
		}
	}
	var activeID string
	_, err = tx.Get(activeThreadsNamespace, run.ThreadID, &activeID)
	if err != nil {
		return err
	}
	if !run.Status.Terminal() {
		if activeID != "" && activeID != run.ID {
			return fmt.Errorf("%w: thread %q already has active run %q", ErrConflict, run.ThreadID, activeID)
		}
		if activeID == "" {
			if err := tx.Put(activeThreadsNamespace, run.ThreadID, run.ID); err != nil {
				return err
			}
		}
	} else if activeID == run.ID {
		tx.Delete(activeThreadsNamespace, run.ThreadID)
	}
	for _, turn := range run.Turns {
		key := base64.RawURLEncoding.EncodeToString([]byte(turn.ThreadID)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(turn.TurnID))
		var owner string
		found, err := tx.Get(turnOwnersNamespace, key, &owner)
		if err != nil {
			return err
		}
		if found && owner != run.ID {
			return fmt.Errorf("%w: turn %q is already attached to another run", ErrConflict, turn.TurnID)
		}
		if !found {
			if err := tx.Put(turnOwnersNamespace, key, run.ID); err != nil {
				return err
			}
		}
	}
	return tx.Put(runsNamespace, run.ID, run)
}

func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	if s == nil {
		return Run{}, errors.New("execution store is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Run{}, errors.New("execution run id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if run, ok := s.memory[id]; ok {
		return cloneRun(run), nil
	}
	var run Run
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var err error
		run, err = getRunDocument(tx, id)
		return err
	})
	return run, err
}

func runOrderKeys(run Run) []string {
	suffix := run.UpdatedAt.UTC().Format("20060102150405.000000000") + "/" + run.ID
	keys := []string{"all/" + suffix}
	for _, scope := range []struct{ name, value string }{{"thread", run.ThreadID}, {"workspace", run.Workspace.ID}, {"root", run.Workspace.Root}, {"status", string(run.Status)}} {
		if scope.value != "" {
			keys = append(keys, scope.name+"/"+base64.RawURLEncoding.EncodeToString([]byte(scope.value))+"/"+suffix)
		}
	}
	return keys
}

func listRunDocuments(tx *persistence.Tx, opts ListOptions) ([]Run, error) {
	prefix := "all/"
	for _, scope := range []struct{ name, value string }{{"thread", opts.ThreadID}, {"workspace", opts.WorkspaceID}, {"root", opts.WorkspaceRoot}, {"status", string(opts.Status)}} {
		if scope.value != "" {
			prefix = scope.name + "/" + base64.RawURLEncoding.EncodeToString([]byte(scope.value)) + "/"
			break
		}
	}
	query := persistence.Query{Namespace: runOrderNamespace, Prefix: prefix, Reverse: true, Limit: opts.Limit}
	runs := make([]Run, 0, opts.Limit)
	for {
		page, err := tx.ReadView(query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Documents {
			var id string
			if err := json.Unmarshal(row.Value, &id); err != nil {
				return nil, err
			}
			run, err := getRunDocument(tx, id)
			if err != nil {
				return nil, err
			}
			if matchesList(run, opts) {
				runs = append(runs, run)
				if len(runs) == opts.Limit {
					return runs, nil
				}
			}
		}
		if page.NextCursor == "" {
			return runs, nil
		}
		query.Cursor = page.NextCursor
		query.AtRevision = &page.Revision
	}
}

func activeRunDocuments(tx *persistence.Tx) ([]Run, error) {
	query := persistence.Query{Namespace: activeThreadsNamespace, Limit: 256}
	var runs []Run
	for {
		page, err := tx.ReadView(query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Documents {
			var id string
			if err := json.Unmarshal(row.Value, &id); err != nil {
				return nil, err
			}
			run, err := getRunDocument(tx, id)
			if err != nil {
				return nil, err
			}
			runs = append(runs, run)
		}
		if page.NextCursor == "" {
			return runs, nil
		}
		query.Cursor = page.NextCursor
		query.AtRevision = &page.Revision
	}
}

func (s *Store) List(ctx context.Context, opts ListOptions) ([]Run, error) {
	if s == nil {
		return nil, errors.New("execution store is required")
	}
	opts.WorkspaceID, opts.WorkspaceRoot, opts.ThreadID = strings.TrimSpace(opts.WorkspaceID), strings.TrimSpace(opts.WorkspaceRoot), strings.TrimSpace(opts.ThreadID)
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 500 {
		opts.Limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []Run
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		all = nil
		runs, err := listRunDocuments(tx, opts)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if matchesList(run, opts) {
				all = append(all, run)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, run := range s.memory {
		if matchesList(run, opts) {
			all = append(all, cloneRun(run))
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].UpdatedAt.Equal(all[j].UpdatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	if len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	return all, nil
}

func (s *Store) update(ctx context.Context, id string, mutate func(*Run) error) (Run, error) {
	if s == nil {
		return Run{}, errors.New("execution store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.memory[id]; found {
		run := cloneRun(existing)
		if err := mutate(&run); err != nil {
			return Run{}, err
		}
		s.memory[id] = cloneRun(run)
		return run, nil
	}
	var run Run
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var err error
		run, err = getRunDocument(tx, id)
		if err != nil {
			return err
		}
		if err := mutate(&run); err != nil {
			return err
		}
		return persistRunDocument(tx, run)
	})
	return run, err
}

func (s *Store) ReconcileOrphans(ctx context.Context, currentRuntimeID string, at time.Time) ([]Run, error) {
	if s == nil {
		return nil, errors.New("execution store is required")
	}
	currentRuntimeID = strings.TrimSpace(currentRuntimeID)
	if currentRuntimeID == "" {
		return nil, errors.New("current execution runtime id is required")
	}
	at = normalizedTime(at)
	cutoff := at.Add(-session.InferenceJournalRecoveryInterval()).UnixMilli()
	var recovered []Run
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		recovered = nil
		runs, err := activeRunDocuments(tx)
		if err != nil {
			return err
		}
		sort.Slice(runs, func(i, j int) bool {
			if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
				return runs[i].ID < runs[j].ID
			}
			return runs[i].CreatedAt.Before(runs[j].CreatedAt)
		})
		for _, run := range runs {
			if run.RuntimeID == currentRuntimeID || run.Status != StatusAccepted && run.Status != StatusRunning {
				continue
			}
			runtime, err := session.ReadInferenceRuntime(tx, run.RuntimeID)
			if err != nil {
				return err
			}
			if runtime.ClosedAt == 0 && (runtime.HeartbeatAt >= cutoff || session.InferenceRuntimeProcessAlive(runtime.PID)) {
				continue
			}
			run.Status, run.UpdatedAt, run.CompletedAt = StatusInterrupted, at, timePtr(at)
			run.Error = &Error{Code: "process_restarted", Category: "cancelled", Message: "app-server stopped before the run reached a terminal state"}
			if err := persistRunDocument(tx, run); err != nil {
				return err
			}
			recovered = append(recovered, run)
		}
		return nil
	})
	return recovered, err
}

func (s *Store) AttachTurn(ctx context.Context, runID, threadID, turnID string, at time.Time) (Run, error) {
	runID = strings.TrimSpace(runID)
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if runID == "" || threadID == "" || turnID == "" {
		return Run{}, errors.New("execution run, thread, and turn ids are required")
	}
	at = normalizedTime(at)
	return s.update(ctx, runID, func(run *Run) error {
		if run.Status.Terminal() {
			return stateConflict(run.ID, run.Status, "attach turn")
		}
		if run.ThreadID == "" {
			run.ThreadID = threadID
		} else if run.ThreadID != threadID {
			return fmt.Errorf("%w: run %q belongs to thread %q", ErrConflict, run.ID, run.ThreadID)
		}
		for _, ref := range run.Turns {
			if ref.TurnID == turnID {
				return nil
			}
		}
		run.Turns = append(run.Turns, TurnRef{
			TurnID:     turnID,
			ThreadID:   threadID,
			Ordinal:    len(run.Turns) + 1,
			AttachedAt: at,
		})
		run.Status = StatusRunning
		if run.StartedAt == nil {
			run.StartedAt = timePtr(at)
		}
		run.UpdatedAt = at
		return nil
	})
}

func (s *Store) Resolve(ctx context.Context, runID string, runtime RuntimeManifest, workspace WorkspaceRef, at time.Time) (Run, error) {
	runtime = normalizeRuntime(runtime)
	workspace = normalizeWorkspace(workspace)
	if runtime.Resolved.Provider == "" || runtime.Resolved.Model == "" || runtime.ProtocolVersion == "" {
		return Run{}, errors.New("resolved execution runtime is incomplete")
	}
	if workspace.Root == "" {
		return Run{}, errors.New("resolved execution workspace root is required")
	}
	at = normalizedTime(at)
	return s.update(ctx, runID, func(run *Run) error {
		if run.Status != StatusAccepted || len(run.Turns) != 0 {
			return stateConflict(run.ID, run.Status, "resolve runtime")
		}
		run.Runtime = runtime
		run.Workspace = workspace
		run.UpdatedAt = at
		return nil
	})
}

func (s *Store) FinishTurn(ctx context.Context, runID, turnID string, terminal TurnTerminal) (Run, error) {
	runID = strings.TrimSpace(runID)
	turnID = strings.TrimSpace(turnID)
	if runID == "" || turnID == "" {
		return Run{}, errors.New("execution run and turn ids are required")
	}
	terminal.TracePath = strings.TrimSpace(terminal.TracePath)
	terminal.At = normalizedTime(terminal.At)
	return s.update(ctx, runID, func(run *Run) error {
		for i := range run.Turns {
			if run.Turns[i].TurnID != turnID {
				continue
			}
			if run.Status.Terminal() {
				if run.Turns[i].TracePath == terminal.TracePath {
					return nil
				}
				return stateConflict(run.ID, run.Status, "change turn trace")
			}
			run.Turns[i].TracePath = terminal.TracePath
			run.UpdatedAt = terminal.At
			return nil
		}
		return fmt.Errorf("%w: turn %q is not attached to run %q", ErrConflict, turnID, run.ID)
	})
}

func (s *Store) Complete(ctx context.Context, runID string, result Result, at time.Time) (Run, error) {
	return s.finish(ctx, runID, StatusCompleted, result, nil, at)
}

func (s *Store) Fail(ctx context.Context, runID string, status Status, result Result, runErr Error, at time.Time) (Run, error) {
	if status == StatusCompleted || !status.Terminal() {
		return Run{}, fmt.Errorf("invalid failure status %q", status)
	}
	runErr = sanitizeError(runErr)
	if runErr.Message == "" {
		return Run{}, errors.New("execution failure message is required")
	}
	return s.finish(ctx, runID, status, result, &runErr, at)
}

func (s *Store) finish(ctx context.Context, runID string, status Status, result Result, runErr *Error, at time.Time) (Run, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Run{}, errors.New("execution run id is required")
	}
	at = normalizedTime(at)
	return s.update(ctx, runID, func(run *Run) error {
		if run.Status.Terminal() {
			resultCopy := cloneResult(result)
			if run.Status == status && reflect.DeepEqual(run.Result, &resultCopy) && reflect.DeepEqual(run.Error, runErr) {
				return nil
			}
			return stateConflict(run.ID, run.Status, "finish")
		}
		run.Status = status
		resultCopy := cloneResult(result)
		run.Result = &resultCopy
		if runErr != nil {
			errorCopy := *runErr
			run.Error = &errorCopy
		}
		run.UpdatedAt = at
		run.CompletedAt = timePtr(at)
		return nil
	})
}
