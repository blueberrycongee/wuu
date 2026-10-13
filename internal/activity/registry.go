package activity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound       = errors.New("activity not found")
	ErrAlreadyExists  = errors.New("activity already exists")
	ErrThreadMismatch = errors.New("activity belongs to another thread")
	ErrControlRevoked = errors.New("activity control revoked")
	ErrStopped        = errors.New("activity is stopped")
	ErrTargetBusy     = errors.New("activity target is controlled by another thread")
)

type registryEntry struct {
	session         Session
	leaseToken      string
	control         context.Context
	revoke          context.CancelCauseFunc
	takeoverVersion uint64
}

type Registry struct {
	mu                 sync.RWMutex
	entries            map[string]*registryEntry
	runs               map[string]*threadRun
	revokedOwners      map[activityOwner]struct{}
	revokedConnections map[activityConnection]struct{}
	listeners          map[uint64]func(Event)
	nextListenerID     uint64
	now                func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]*registryEntry), runs: make(map[string]*threadRun),
		revokedOwners: make(map[activityOwner]struct{}), revokedConnections: make(map[activityConnection]struct{}),
		listeners: make(map[uint64]func(Event)), now: time.Now,
	}
}

func (r *Registry) Subscribe(listener func(Event)) func() {
	if r == nil || listener == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextListenerID++
	id := r.nextListenerID
	r.listeners[id] = listener
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

func (r *Registry) Start(options StartOptions) (Session, Lease, error) {
	if r == nil {
		return Session{}, Lease{}, errors.New("activity registry is unavailable")
	}
	options.ThreadID = strings.TrimSpace(options.ThreadID)
	options.Workdir = strings.TrimSpace(options.Workdir)
	options.PluginID = strings.TrimSpace(options.PluginID)
	options.GenerationID = strings.TrimSpace(options.GenerationID)
	options.ConnectionID = strings.TrimSpace(options.ConnectionID)
	if options.ThreadID == "" || options.Workdir == "" {
		return Session{}, Lease{}, errors.New("activity thread_id and workdir are required")
	}
	if !validKind(options.Kind) {
		return Session{}, Lease{}, fmt.Errorf("unsupported activity kind %q", options.Kind)
	}
	id := strings.TrimSpace(options.ID)
	if id == "" {
		var err error
		id, err = randomID("activity", 12)
		if err != nil {
			return Session{}, Lease{}, err
		}
	}
	token, err := randomID("lease", 24)
	if err != nil {
		return Session{}, Lease{}, err
	}
	now := r.now().UTC()
	session := Session{
		ID:           id,
		Kind:         options.Kind,
		ThreadID:     options.ThreadID,
		Workdir:      options.Workdir,
		PluginID:     strings.TrimSpace(options.PluginID),
		GenerationID: options.GenerationID,
		ConnectionID: options.ConnectionID,
		Target:       strings.TrimSpace(options.Target),
		ProcessID:    options.ProcessID,
		WindowID:     options.WindowID,
		State:        StateStarting,
		Controller:   ControllerAgent,
		Preview:      strings.TrimSpace(options.Preview),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	r.mu.Lock()
	if err := r.checkExecutionLocked(nil, options.ThreadID); err != nil {
		r.mu.Unlock()
		return Session{}, Lease{}, err
	}
	if r.ownerRevokedLocked(options.PluginID, options.GenerationID, options.ConnectionID) {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrControlRevoked
	}
	if _, exists := r.entries[id]; exists {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrAlreadyExists
	}
	if r.targetBusyLocked(session.Target, session.Kind, session.ID) {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrTargetBusy
	}
	if run := r.runs[options.ThreadID]; run != nil && run.execution != nil {
		session.ExecutionID = run.execution.id
	}
	control, revoke := context.WithCancelCause(context.Background())
	r.entries[id] = &registryEntry{session: session, leaseToken: token, control: control, revoke: revoke}
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, Activity: session})
	return session, Lease{ActivityID: id, ThreadID: options.ThreadID, Token: token}, nil
}

// Acquire returns the current agent lease for one plugin Activity or creates
// the Activity on first use. A user takeover and a stopped tombstone are hard
// gates: callers cannot silently create a replacement session and continue
// controlling the UI behind the user's back.
func (r *Registry) Acquire(options StartOptions) (Session, Lease, error) {
	return r.acquire(nil, options)
}

// AcquireForExecution rejects work from an ended or superseded task, even when
// its caller removed context cancellation or attempts another backend.
func (r *Registry) AcquireForExecution(ctx context.Context, options StartOptions) (Session, Lease, error) {
	if err := ControlErr(ctx); err != nil {
		return Session{}, Lease{}, err
	}
	return r.acquire(ctx, options)
}

func (r *Registry) acquire(ctx context.Context, options StartOptions) (Session, Lease, error) {
	if r == nil {
		return Session{}, Lease{}, errors.New("activity registry is unavailable")
	}
	options.ThreadID = strings.TrimSpace(options.ThreadID)
	options.Workdir = strings.TrimSpace(options.Workdir)
	options.PluginID = strings.TrimSpace(options.PluginID)
	options.GenerationID = strings.TrimSpace(options.GenerationID)
	options.ConnectionID = strings.TrimSpace(options.ConnectionID)
	if options.ThreadID == "" || options.Workdir == "" || options.PluginID == "" {
		return Session{}, Lease{}, errors.New("activity thread_id, workdir, and plugin_id are required")
	}
	if !validKind(options.Kind) {
		return Session{}, Lease{}, fmt.Errorf("unsupported activity kind %q", options.Kind)
	}

	r.mu.Lock()
	if err := r.checkExecutionLocked(ctx, options.ThreadID); err != nil {
		r.mu.Unlock()
		return Session{}, Lease{}, err
	}
	if r.ownerRevokedLocked(options.PluginID, options.GenerationID, options.ConnectionID) {
		// A pinned old generation may make its first UI call only after its
		// provider retired. Fence this execution too; stale contexts were
		// rejected above and cannot stop a newer task.
		var events []Event
		if ctx != nil {
			events = r.revokeThreadLocked(options.ThreadID, true)
		}
		r.mu.Unlock()
		for _, event := range events {
			r.emit(event)
		}
		return Session{}, Lease{}, ErrControlRevoked
	}
	requestedTarget := strings.TrimSpace(options.Target)
	var current *registryEntry
	for _, entry := range r.entries {
		if entry.session.ThreadID != options.ThreadID || entry.session.PluginID != options.PluginID || entry.session.Kind != options.Kind || entry.session.GenerationID != options.GenerationID || entry.session.ConnectionID != options.ConnectionID {
			continue
		}
		if current == nil || entry.session.CreatedAt.After(current.session.CreatedAt) {
			current = entry
		}
	}
	if current != nil {
		session := current.session
		if r.targetBusyLocked(requestedTarget, options.Kind, session.ID) {
			r.mu.Unlock()
			return Session{}, Lease{}, ErrTargetBusy
		}
		// Idle activities become agent-owned only when this execution actually
		// uses them. A later text-only turn must not resurrect a prior PiP.
		if run := r.runs[options.ThreadID]; run != nil && run.execution != nil && run.execution.control.Err() == nil && session.Controller == ControllerNone && session.State != StateStopped {
			r.releaseLocked(current, rand.Text())
			current.session.ExecutionID = run.execution.id
			if requestedTarget != "" {
				current.session.Target = requestedTarget
			}
			current.session.State = StateStarting
			current.session.Preview = ""
			current.session.Interaction = nil
			current.session.ProcessID, current.session.WindowID = 0, 0
			current.session.Error = ""
			session = current.session
			lease := Lease{ActivityID: session.ID, ThreadID: session.ThreadID, Token: current.leaseToken}
			r.mu.Unlock()
			r.emit(Event{Type: EventControlChanged, Activity: session})
			return session, lease, nil
		}
		switch {
		case session.State == StateStopped:
			r.mu.Unlock()
			return Session{}, Lease{}, ErrStopped
		case session.Controller != ControllerAgent || current.leaseToken == "":
			r.mu.Unlock()
			return Session{}, Lease{}, ErrControlRevoked
		default:
			lease := Lease{ActivityID: session.ID, ThreadID: session.ThreadID, Token: current.leaseToken}
			r.mu.Unlock()
			return session, lease, nil
		}
	}
	if r.targetBusyLocked(requestedTarget, options.Kind, "") {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrTargetBusy
	}

	id := strings.TrimSpace(options.ID)
	if id == "" {
		var err error
		id, err = randomID("activity", 12)
		if err != nil {
			r.mu.Unlock()
			return Session{}, Lease{}, err
		}
	}
	if _, exists := r.entries[id]; exists {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrAlreadyExists
	}
	token, err := randomID("lease", 24)
	if err != nil {
		r.mu.Unlock()
		return Session{}, Lease{}, err
	}
	now := r.now().UTC()
	session := Session{
		ID:           id,
		Kind:         options.Kind,
		ThreadID:     options.ThreadID,
		Workdir:      options.Workdir,
		PluginID:     options.PluginID,
		GenerationID: options.GenerationID,
		ConnectionID: options.ConnectionID,
		Target:       strings.TrimSpace(options.Target),
		ProcessID:    options.ProcessID,
		WindowID:     options.WindowID,
		State:        StateStarting,
		Controller:   ControllerAgent,
		Preview:      strings.TrimSpace(options.Preview),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if run := r.runs[options.ThreadID]; run != nil && run.execution != nil {
		session.ExecutionID = run.execution.id
	}
	control, revoke := context.WithCancelCause(context.Background())
	r.entries[id] = &registryEntry{session: session, leaseToken: token, control: control, revoke: revoke}
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, Activity: session})
	return session, Lease{ActivityID: id, ThreadID: options.ThreadID, Token: token}, nil
}

func (r *Registry) List(threadID string) []Session {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Session, 0)
	for _, entry := range r.entries {
		if entry.session.ThreadID == threadID {
			out = append(out, entry.session)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// ListByKind returns a snapshot of every live session of the given kind across
// all threads. Shutdown paths use it to stop a whole class of activities (for
// example every embedded-browser tab owned by this process) without knowing the
// owning thread ids in advance; ordinary UI queries stay scoped to List.
func (r *Registry) ListByKind(kind Kind) []Session {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Session, 0)
	for _, entry := range r.entries {
		if entry.session.Kind == kind {
			out = append(out, entry.session)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (r *Registry) Update(threadID, activityID string, options UpdateOptions) (Session, error) {
	return r.update(threadID, activityID, "", options)
}

// UpdateWithLease prevents a completed action from overwriting a concurrent takeover.
func (r *Registry) UpdateWithLease(lease Lease, options UpdateOptions) (Session, error) {
	if lease.Token == "" {
		return Session{}, ErrControlRevoked
	}
	return r.update(lease.ThreadID, lease.ActivityID, lease.Token, options)
}

func (r *Registry) update(threadID, activityID, token string, options UpdateOptions) (Session, error) {
	r.mu.Lock()
	entry, err := r.entryLocked(threadID, activityID)
	if err != nil {
		r.mu.Unlock()
		return Session{}, err
	}
	if token != "" && (entry.leaseToken != token || entry.session.Controller != ControllerAgent) {
		r.mu.Unlock()
		return Session{}, ErrControlRevoked
	}
	if entry.session.State == StateStopped {
		r.mu.Unlock()
		return Session{}, ErrStopped
	}
	if options.State != "" {
		if !validState(options.State) {
			r.mu.Unlock()
			return Session{}, fmt.Errorf("unsupported activity state %q", options.State)
		}
		entry.session.State = options.State
		if options.State == StateStopped {
			entry.revoke(ErrControlRevoked)
			entry.leaseToken = ""
			entry.session.Controller = ControllerNone
		}
	}
	if options.Target != "" {
		target := strings.TrimSpace(options.Target)
		if r.targetBusyLocked(target, entry.session.Kind, entry.session.ID) {
			r.mu.Unlock()
			return Session{}, ErrTargetBusy
		}
		entry.session.Target = target
	}
	if options.ClearWindowIdentity {
		entry.session.ProcessID = 0
		entry.session.WindowID = 0
	}
	if options.ProcessID != 0 {
		entry.session.ProcessID = options.ProcessID
	}
	if options.WindowID != 0 {
		entry.session.WindowID = options.WindowID
	}
	if options.Preview != "" {
		entry.session.Preview = strings.TrimSpace(options.Preview)
	}
	if options.ClearError {
		entry.session.Error = ""
	} else if options.Error != "" {
		entry.session.Error = strings.TrimSpace(options.Error)
	}
	if options.Interaction != nil {
		interaction := *options.Interaction
		entry.session.Interaction = &interaction
	}
	entry.session.UpdatedAt = r.now().UTC()
	session := entry.session
	r.mu.Unlock()
	eventType := EventUpdated
	if session.State == StateStopped {
		eventType = EventStopped
	}
	r.emit(Event{Type: eventType, Activity: session})
	return session, nil
}

func (r *Registry) targetBusyLocked(target string, kind Kind, exceptActivityID string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" || kind != KindCUA {
		return false
	}
	for id, entry := range r.entries {
		if id == exceptActivityID || entry.session.Kind != kind || entry.session.State == StateStopped || entry.session.Controller == ControllerNone {
			continue
		}
		if strings.ToLower(strings.TrimSpace(entry.session.Target)) == target {
			return true
		}
	}
	return false
}

func (r *Registry) Takeover(threadID, activityID string, executionID ...string) (Session, error) {
	r.mu.Lock()
	entry, err := r.executionEntryLocked(threadID, activityID, executionID)
	if err != nil {
		r.mu.Unlock()
		return Session{}, err
	}
	if entry.session.State == StateStopped {
		r.mu.Unlock()
		return Session{}, ErrStopped
	}
	events := r.revokeThreadLocked(threadID, false)
	session := entry.session
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
	return session, nil
}

func (r *Registry) Release(threadID, activityID string, executionID ...string) (Session, Lease, error) {
	r.mu.Lock()
	entry, err := r.executionEntryLocked(threadID, activityID, executionID)
	if err != nil {
		r.mu.Unlock()
		return Session{}, Lease{}, err
	}
	if entry.session.State == StateStopped {
		r.mu.Unlock()
		return Session{}, Lease{}, ErrStopped
	}
	if run := r.runs[threadID]; run != nil && run.execution != nil {
		// Releasing manual ownership is not a new model instruction. Remain
		// idle until explicit admission creates another execution epoch.
		session := r.idleLocked(entry)
		r.mu.Unlock()
		r.emit(Event{Type: EventControlChanged, Activity: session})
		return session, Lease{ActivityID: session.ID, ThreadID: session.ThreadID}, nil
	}
	token, err := randomID("lease", 24)
	if err != nil {
		r.mu.Unlock()
		return Session{}, Lease{}, err
	}
	session := r.releaseLocked(entry, token)
	if run := r.runs[threadID]; run != nil {
		run.paused = false
	}
	r.mu.Unlock()
	r.emit(Event{Type: EventControlChanged, Activity: session})
	return session, Lease{ActivityID: session.ID, ThreadID: session.ThreadID, Token: token}, nil
}

// PrepareResume snapshots user-controlled activities before a user turn is
// admitted. Call the returned function only once admission succeeds. A newer
// takeover or stop wins over this pending resume; old leases stay revoked.
func (r *Registry) PrepareResume(threadID string, kind Kind) (func(), error) {
	if r == nil {
		return func() {}, nil
	}
	type pendingResume struct {
		entry   *registryEntry
		version uint64
		token   string
	}
	r.mu.Lock()
	var pending []pendingResume
	for _, entry := range r.entries {
		if entry.session.ThreadID != threadID || entry.session.Kind != kind || entry.session.Controller != ControllerUser || entry.session.State == StateStopped {
			continue
		}
		token, err := randomID("lease", 24)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		pending = append(pending, pendingResume{entry: entry, version: entry.takeoverVersion, token: token})
	}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		var events []Event
		for _, resume := range pending {
			entry := resume.entry
			if entry.takeoverVersion != resume.version || entry.session.Controller != ControllerUser || entry.session.State == StateStopped {
				continue
			}
			events = append(events, Event{Type: EventControlChanged, Activity: r.releaseLocked(entry, resume.token)})
			if run := r.runs[threadID]; run != nil {
				run.paused = false
			}
		}
		r.mu.Unlock()
		for _, event := range events {
			r.emit(event)
		}
	}, nil
}

func (r *Registry) releaseLocked(entry *registryEntry, token string) Session {
	entry.revoke(ErrControlRevoked)
	entry.control, entry.revoke = context.WithCancelCause(context.Background())
	entry.leaseToken = token
	entry.session.Controller = ControllerAgent
	entry.session.State = StateBackgroundControlled
	entry.session.UpdatedAt = r.now().UTC()
	return entry.session
}

func (r *Registry) idleLocked(entry *registryEntry) Session {
	entry.revoke(ErrControlRevoked)
	entry.leaseToken = ""
	entry.session.Controller = ControllerNone
	if entry.session.State != StateError {
		entry.session.State = StateActive
	}
	entry.session.Interaction = nil
	entry.session.UpdatedAt = r.now().UTC()
	return entry.session
}

func (r *Registry) Stop(threadID, activityID string, executionID ...string) (Session, error) {
	r.mu.Lock()
	entry, err := r.executionEntryLocked(threadID, activityID, executionID)
	if err != nil {
		r.mu.Unlock()
		return Session{}, err
	}
	if entry.session.State == StateStopped {
		session := entry.session
		r.mu.Unlock()
		return session, nil
	}
	events := r.revokeThreadLocked(threadID, true)
	session := entry.session
	r.mu.Unlock()
	for _, event := range events {
		r.emit(event)
	}
	return session, nil
}

// BindControl cancels in-flight work when its lease is revoked. Registration and
// validation share the registry lock so takeover cannot race between them.
func (r *Registry) BindControl(parent context.Context, lease Lease) (context.Context, context.CancelFunc, error) {
	if r == nil {
		return nil, nil, ErrControlRevoked
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, err := r.entryLocked(lease.ThreadID, lease.ActivityID)
	if err != nil {
		return nil, nil, err
	}
	if entry.session.Controller != ControllerAgent || entry.leaseToken == "" || entry.leaseToken != lease.Token {
		return nil, nil, ErrControlRevoked
	}
	ctx, cancel := context.WithCancelCause(parent)
	stop := context.AfterFunc(entry.control, func() { cancel(ErrControlRevoked) })
	// Keep the original authority available at side-effect dispatch boundaries;
	// AfterFunc propagates cancellation asynchronously.
	bound := context.WithValue(ctx, boundControlContextKey{}, entry.control)
	return bound, func() { stop(); cancel(context.Canceled) }, nil
}

type boundControlContextKey struct{}

// ControlErr checks both caller cancellation and the original activity lease.
// It does not wait for the lease's asynchronous cancellation callback to run.
func ControlErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if execution, ok := ctx.Value(executionContextKey{}).(*executionEpoch); ok && execution.control.Err() != nil {
		return ErrControlRevoked
	}
	if control, ok := ctx.Value(boundControlContextKey{}).(context.Context); ok && control.Err() != nil {
		return context.Cause(control)
	}
	return nil
}

// BoundControlErr checks local UI authority at shared transport boundaries.
// Unbound tools retain their caller lifetime when another UI operation's
// execution is revoked; only BindControl opts into the local-control fence.
func BoundControlErr(ctx context.Context) error {
	if _, bound := ctx.Value(boundControlContextKey{}).(context.Context); bound {
		return ControlErr(ctx)
	}
	return ctx.Err()
}

func (r *Registry) CheckControl(threadID, activityID, token string) error {
	if r == nil {
		return ErrControlRevoked
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, err := r.entryLocked(threadID, activityID)
	if err != nil {
		return err
	}
	if entry.session.Controller != ControllerAgent || entry.session.State == StateStopped || entry.leaseToken == "" || entry.leaseToken != strings.TrimSpace(token) {
		return ErrControlRevoked
	}
	return nil
}

// Optional execution identity protects newer runs from delayed desktop controls.
func (r *Registry) executionEntryLocked(threadID, activityID string, executionID []string) (*registryEntry, error) {
	entry, err := r.entryLocked(threadID, activityID)
	if err != nil {
		return nil, err
	}
	if len(executionID) > 0 && executionID[0] != "" {
		if entry.session.ExecutionID != executionID[0] {
			return nil, ErrControlRevoked
		}
		// An idle previous activity keeps its historical identity until first
		// use. Its delayed controls must not interrupt a newer text-only turn.
		if run := r.runs[entry.session.ThreadID]; run != nil && run.execution != nil && run.execution.control.Err() == nil && run.execution.id != executionID[0] {
			return nil, ErrControlRevoked
		}
	}
	return entry, nil
}

func (r *Registry) entryLocked(threadID, activityID string) (*registryEntry, error) {
	entry, ok := r.entries[strings.TrimSpace(activityID)]
	if !ok {
		return nil, ErrNotFound
	}
	if entry.session.ThreadID != strings.TrimSpace(threadID) {
		return nil, ErrThreadMismatch
	}
	return entry, nil
}

func (r *Registry) emit(event Event) {
	if r == nil {
		return
	}
	r.mu.RLock()
	// Mutations publish outside the registry lock. A newer takeover, stop, or
	// execution may already have replaced this snapshot before publication.
	if entry := r.entries[event.Activity.ID]; entry != nil && entry.session != event.Activity {
		r.mu.RUnlock()
		return
	}
	listeners := make([]func(Event), 0, len(r.listeners))
	for _, listener := range r.listeners {
		listeners = append(listeners, listener)
	}
	r.mu.RUnlock()
	for _, listener := range listeners {
		listener(event)
	}
}

func validKind(kind Kind) bool {
	return kind == KindBrowser || kind == KindCUA
}

func validState(state State) bool {
	switch state {
	case StateStarting, StateActive, StateBackgroundControlled, StateForegroundControlled, StateUserControlled, StateWaitingConfirmation, StateStopped, StateError:
		return true
	default:
		return false
	}
}

func randomID(prefix string, size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(data), nil
}
