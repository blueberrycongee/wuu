package toolctx

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

var ErrSettlementOutcomeUnknown = errors.New("tool settlement outcome unknown")

type SettlementIdentity struct{ OperationID, WorkerInstance, Actor, EnvironmentIdentity string }
type PreparedSettlement struct {
	Identity *SettlementIdentity
	Commit   func(context.Context) error
	Abort    func(context.Context) error
	Release  func(context.Context) error
}
type settlementKey struct{}
type settlementAction struct {
	group   any
	value   any
	prepare func(context.Context, []any) (*PreparedSettlement, error)
	abort   func()
}
type settlement struct {
	mu             sync.Mutex
	parent         *settlement
	finished       bool
	durable        bool
	children       int
	remoteExposure bool
	hadActions     bool
	actions        []settlementAction
}

// FailedSettlementOutput discards all output that could have been derived
// from a remote read whose settlement did not commit.
func FailedSettlementOutput(ctx context.Context, after toolresult.Result) toolresult.Result {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return after
	}
	s.mu.Lock()
	exposed := s.remoteExposure || s.hadActions || len(s.actions) != 0
	s.mu.Unlock()
	if !exposed {
		return after
	}
	return toolresult.FromErrorText("The tool failed; pending remote read output was discarded.")
}

// WithSettlement binds host-only deferred work to one invocation. A child
// transfers successful participants to the parent, not to a public payload.
func WithSettlement(ctx context.Context, durable ...bool) context.Context {
	parent, _ := ctx.Value(settlementKey{}).(*settlement)
	enabled := true
	if len(durable) > 0 {
		enabled = durable[0]
	}
	return context.WithValue(ctx, settlementKey{}, &settlement{parent: parent, durable: enabled})
}
func HasSettlement(ctx context.Context) bool {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	return s != nil && s.durable
}

// ReserveSettlementChild closes the gap between nested-call admission and
// its eventual registration. It is released only on physical completion.
func ReserveSettlementChild(ctx context.Context) (func(), error) {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil || !s.durable {
		return func() {}, nil
	}
	var chain []*settlement
	for current := s; current != nil; current = current.parent {
		chain = append(chain, current)
	}
	// Lock from root to leaf. Closing any ancestor and admitting a descendant
	// are mutually exclusive, including when an intermediate child has ended.
	for i := len(chain) - 1; i >= 0; i-- {
		chain[i].mu.Lock()
	}
	closed := false
	for _, current := range chain {
		closed = closed || current.finished
	}
	if !closed {
		for _, current := range chain {
			current.children++
		}
	}
	for _, current := range chain {
		current.mu.Unlock()
	}
	if closed {
		return nil, errors.New("parent settlement scope closed")
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, current := range chain {
				current.mu.Lock()
				current.children--
				current.mu.Unlock()
			}
		})
	}, nil
}

// MarkRemoteReadExposure records that a nested invocation dispatched a remote
// read whose contents may reach its orchestrator. Propagate immediately to all
// ancestors even if the read, plugin, cancellation or registration fails.
func MarkRemoteReadExposure(ctx context.Context) {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	for s != nil {
		s.mu.Lock()
		s.remoteExposure = true
		parent := s.parent
		s.mu.Unlock()
		s = parent
	}
}

// HasRemoteReadExposure remains true after settlement closes or aborts.
func HasRemoteReadExposure(ctx context.Context) bool {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remoteExposure
}

// SettlementHasParticipants reports participants captured when the scope
// closed, including those already aborted on an unsuccessful finalization.
func SettlementHasParticipants(ctx context.Context) bool {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hadActions || len(s.actions) != 0
}

// SettlementHasUnfinishedChildren is read after the scope is closed, so no new
// child can be admitted while deciding whether nested projection is safe.
func SettlementHasUnfinishedChildren(ctx context.Context) bool {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.children != 0
}

func RegisterSettlement(ctx context.Context, commit func(context.Context) error, abort func()) error {
	if commit == nil {
		return errors.New("host settlement scope unavailable")
	}
	return RegisterGroupedSettlement(ctx, new(byte), nil, func(_ context.Context, _ []any) (*PreparedSettlement, error) {
		cleanup := func(context.Context) error { abort(); return nil }
		return &PreparedSettlement{Commit: commit, Abort: cleanup, Release: cleanup}, nil
	}, abort)
}
func RegisterGroupedSettlement(ctx context.Context, group, value any, prepare func(context.Context, []any) (*PreparedSettlement, error), abort func()) error {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil || !s.durable || group == nil || !reflect.ValueOf(group).Comparable() || prepare == nil || abort == nil {
		return errors.New("host settlement scope unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || len(s.actions) >= 128 {
		return errors.New("host settlement scope is closed or full")
	}
	s.actions = append(s.actions, settlementAction{group: group, value: value, prepare: prepare, abort: abort})
	return nil
}
func abortActions(actions []settlementAction) {
	var wg sync.WaitGroup
	for _, action := range actions {
		wg.Add(1)
		go func(abort func()) { defer wg.Done(); abort() }(action.abort)
	}
	wg.Wait()
}

// PrepareSettlement closes the owner. A successful child transfers to its
// parent; a successful root prepares but does not yet commit its worker batch.
func PrepareSettlement(ctx context.Context, success bool) (*PreparedSettlement, error) {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil, nil
	}
	s.finished = true
	actions := s.actions
	s.hadActions = len(actions) != 0
	children := s.children
	s.actions = nil
	s.mu.Unlock()
	if success && children != 0 && len(actions) != 0 {
		abortActions(actions)
		return nil, errors.New("parent settlement has unfinished child calls")
	}
	if len(actions) == 0 {
		return nil, nil
	}
	if !success {
		abortActions(actions)
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		abortActions(actions)
		return nil, err
	}
	if s.parent != nil {
		s.parent.mu.Lock()
		if s.parent.finished || len(s.parent.actions)+len(actions) > 128 {
			s.parent.mu.Unlock()
			abortActions(actions)
			return nil, errors.New("parent settlement unavailable or full")
		}
		s.parent.actions = append(s.parent.actions, actions...)
		s.parent.mu.Unlock()
		return nil, nil
	}
	values := make([]any, 0, len(actions))
	key := actions[0].group
	for _, action := range actions {
		if action.group != key {
			abortActions(actions)
			return nil, errors.New("remote settlement requires a single worker and actor")
		}
		values = append(values, action.value)
	}
	prepared, err := actions[0].prepare(ctx, values)
	if err != nil {
		abortActions(actions)
		return nil, err
	}
	if prepared == nil || prepared.Commit == nil || prepared.Abort == nil || prepared.Release == nil {
		abortActions(actions)
		return nil, errors.New("invalid prepared settlement")
	}
	return prepared, nil
}

// FinishSettlement retains a one-shot helper for non-durable callers and tests.
func FinishSettlement(ctx context.Context, success bool) (bool, error) {
	p, err := PrepareSettlement(ctx, success)
	if err != nil || p == nil {
		return false, err
	}
	defer func() { _ = p.Abort(context.Background()) }()
	return true, p.Commit(ctx)
}
