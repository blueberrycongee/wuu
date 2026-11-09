package activity

import (
	"context"
	"strings"
)

type activityOwner struct {
	pluginID     string
	generationID string
}

type activityConnection struct {
	owner activityOwner
	id    string
}

type executionEpoch struct {
	id       string
	threadID string
	control  context.Context
	revoke   context.CancelCauseFunc
}

type executionContextKey struct{}

type threadRun struct {
	revision  uint64
	paused    bool
	stopped   bool
	execution *executionEpoch
}

// PrepareRun snapshots the user's control revision before admission. Only an
// admitted explicit input may lift a pause or stop, and a newer user action wins
// over queued input. The returned grant must be applied before starting the turn.
func (r *Registry) PrepareRun(threadID string) (func(), error) {
	if r == nil {
		return func() {}, nil
	}
	threadID = strings.TrimSpace(threadID)
	r.mu.Lock()
	run := r.threadRunLocked(threadID)
	revision := run.revision
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if run.revision != revision {
			r.mu.Unlock()
			return
		}
		run.paused, run.stopped = false, false
		var events []Event
		for _, entry := range r.entries {
			if entry.session.ThreadID != threadID {
				continue
			}
			if r.ownerRevokedLocked(entry.session.PluginID, entry.session.GenerationID, entry.session.ConnectionID) {
				continue
			}
			if entry.session.Controller != ControllerAgent || entry.session.State == StateStopped {
				events = append(events, Event{Type: EventControlChanged, Activity: r.idleLocked(entry)})
			}
		}
		r.mu.Unlock()
		for _, event := range events {
			r.emit(event)
		}
	}, nil
}

// BeginExecution binds every local UI tool to one turn. Inherited contexts keep
// this exact epoch, including context.WithoutCancel; no delayed worker can borrow
// the next turn's authority. Background turns cannot lift a user pause or stop.
func (r *Registry) BeginExecution(ctx context.Context, threadID, executionID string) context.Context {
	if r == nil {
		return ctx
	}
	control, revoke := context.WithCancelCause(context.Background())
	epoch := &executionEpoch{id: executionID, threadID: threadID, control: control, revoke: revoke}
	r.mu.Lock()
	run := r.threadRunLocked(threadID)
	if run.execution != nil {
		run.execution.revoke(ErrControlRevoked)
	}
	run.execution = epoch
	var events []Event
	if run.paused || run.stopped {
		revoke(ErrControlRevoked)
	} else {
		for _, entry := range r.entries {
			if entry.session.ThreadID == threadID && entry.session.Controller == ControllerAgent {
				events = append(events, Event{Type: EventControlChanged, Activity: r.idleLocked(entry)})
			}
		}
	}
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
	return context.WithValue(ctx, executionContextKey{}, epoch)
}

// EndExecution releases both browser and native control without closing the
// user's applications or erasing retained browser pages. Repeated/stale cleanup
// cannot affect a successor, and user takeover remains authoritative.
func (r *Registry) EndExecution(threadID, executionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	run := r.runs[threadID]
	if run == nil || run.execution == nil || run.execution.id != executionID {
		r.mu.Unlock()
		return
	}
	run.execution.revoke(ErrControlRevoked)
	var events []Event
	for _, entry := range r.entries {
		if entry.session.ThreadID != threadID || entry.session.ExecutionID != executionID || entry.session.Controller != ControllerAgent {
			continue
		}
		events = append(events, Event{Type: EventControlChanged, Activity: r.idleLocked(entry)})
	}
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
}

// PauseThread fences local UI authority before asynchronous turn cancellation.
// It also covers Stop controls outside the activity window.
func (r *Registry) PauseThread(threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	run := r.threadRunLocked(threadID)
	run.revision++
	run.paused = true
	if run.execution != nil {
		run.execution.revoke(ErrControlRevoked)
	}
	var events []Event
	for _, entry := range r.entries {
		if entry.session.ThreadID != threadID || entry.session.Controller != ControllerAgent {
			continue
		}
		// Interrupting work relinquishes agent control; it does not claim a
		// user takeover. In particular, temporary browser pages must still be
		// eligible for turn-end cleanup. Existing manual ownership is retained.
		events = append(events, Event{Type: EventControlChanged, Activity: r.idleLocked(entry)})
	}
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
}

// RevokeOwner permanently fences one plugin generation. A late retirement from
// an old helper cannot revoke its replacement's activities or execution.
func (r *Registry) RevokeOwner(pluginID, generationID string) {
	r.revokeOwner(pluginID, generationID, "")
}

// RevokeConnection retires only one native helper connection. A successful
// reconnect in the same plugin generation may serve a new explicit user run;
// neither stale calls nor late callbacks from the old connection can follow it.
func (r *Registry) RevokeConnection(pluginID, generationID, connectionID string) {
	if strings.TrimSpace(connectionID) == "" {
		return
	}
	r.revokeOwner(pluginID, generationID, connectionID)
}

func (r *Registry) ownerRevokedLocked(pluginID, generationID, connectionID string) bool {
	owner := activityOwner{pluginID, generationID}
	if _, retired := r.revokedOwners[owner]; retired {
		return true
	}
	_, retired := r.revokedConnections[activityConnection{owner, connectionID}]
	return retired
}

func (r *Registry) revokeOwner(pluginID, generationID, connectionID string) {
	if r == nil {
		return
	}
	owner := activityOwner{strings.TrimSpace(pluginID), strings.TrimSpace(generationID)}
	connectionID = strings.TrimSpace(connectionID)
	r.mu.Lock()
	if connectionID == "" {
		r.revokedOwners[owner] = struct{}{}
	} else {
		r.revokedConnections[activityConnection{owner, connectionID}] = struct{}{}
	}
	threads := make(map[string]struct{})
	for _, entry := range r.entries {
		if entry.session.PluginID != owner.pluginID || entry.session.GenerationID != owner.generationID || (connectionID != "" && entry.session.ConnectionID != connectionID) || entry.session.State == StateStopped {
			continue
		}
		// Only current execution ownership can stop sibling backends. An idle
		// historical activity still loses its lease, but owns no running task.
		run := r.runs[entry.session.ThreadID]
		if run == nil || run.execution == nil || (run.execution.id == entry.session.ExecutionID && run.execution.control.Err() == nil) {
			threads[entry.session.ThreadID] = struct{}{}
		}
	}
	var events []Event
	for threadID := range threads {
		events = append(events, r.revokeThreadLocked(threadID, true)...)
	}
	for _, entry := range r.entries {
		if entry.session.PluginID != owner.pluginID || entry.session.GenerationID != owner.generationID || (connectionID != "" && entry.session.ConnectionID != connectionID) || entry.session.State == StateStopped {
			continue
		}
		entry.revoke(ErrControlRevoked)
		entry.leaseToken = ""
		entry.session.State, entry.session.Controller = StateStopped, ControllerNone
		entry.session.UpdatedAt = r.now().UTC()
		events = append(events, Event{Type: EventStopped, Activity: entry.session})
	}
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
}

func (r *Registry) threadRunLocked(threadID string) *threadRun {
	run := r.runs[threadID]
	if run == nil {
		run = &threadRun{}
		r.runs[threadID] = run
	}
	return run
}

func (r *Registry) checkExecutionLocked(ctx context.Context, threadID string) error {
	run := r.runs[threadID]
	if run == nil {
		return nil
	}
	if run.stopped {
		return ErrStopped
	}
	if run.paused {
		return ErrControlRevoked
	}
	if ctx != nil && run.execution != nil {
		epoch, _ := ctx.Value(executionContextKey{}).(*executionEpoch)
		if epoch == nil || epoch != run.execution || epoch.threadID != threadID || epoch.control.Err() != nil {
			return ErrControlRevoked
		}
	}
	return nil
}

func (r *Registry) revokeThreadLocked(threadID string, stopped bool) []Event {
	run := r.threadRunLocked(threadID)
	run.revision++
	run.paused = true
	run.stopped = stopped
	if run.execution != nil {
		run.execution.revoke(ErrControlRevoked)
	}
	var events []Event
	for _, entry := range r.entries {
		if entry.session.ThreadID != threadID || entry.session.State == StateStopped {
			continue
		}
		entry.revoke(ErrControlRevoked)
		entry.leaseToken = ""
		entry.takeoverVersion++
		entry.session.Interaction = nil
		entry.session.UpdatedAt = r.now().UTC()
		typeOfEvent := EventControlChanged
		entry.session.State, entry.session.Controller = StateUserControlled, ControllerUser
		if stopped {
			entry.session.State, entry.session.Controller = StateStopped, ControllerNone
			typeOfEvent = EventStopped
		}
		events = append(events, Event{Type: typeOfEvent, Activity: entry.session})
	}
	return events
}
