package toolctx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	media          [][32]byte
}

func settlementMediaKey(part toolresult.ContentPart) [32]byte {
	encoded, _ := json.Marshal(part)
	return sha256.Sum256(encoded)
}

// TrackSettlementMedia associates a leaf's final media with its provisional
// settlement. Counts matter when an ordinary call returns the same image.
func TrackSettlementMedia(ctx context.Context, parts []toolresult.ContentPart) {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || len(s.actions) == 0 {
		return
	}
	s.media = nil
	for _, part := range parts {
		if part.Type == toolresult.ContentTypeImage || part.Type == toolresult.ContentTypeAudio {
			s.media = append(s.media, settlementMediaKey(part))
		}
	}
}

// FailedSettlementOutput discards uncommitted free text and media after the
// settlement has failed. Ordinary media already present in the input survives,
// including copies byte-identical to a provisional image. A finalizer cannot
// introduce new media whose provenance is unknown on the failed path.
func FailedSettlementOutput(ctx context.Context, before, after toolresult.Result) toolresult.Result {
	s, _ := ctx.Value(settlementKey{}).(*settlement)
	if s == nil {
		return after
	}
	s.mu.Lock()
	participated := s.remoteExposure || s.hadActions || len(s.actions) != 0
	media := append([][32]byte(nil), s.media...)
	s.mu.Unlock()
	if !participated {
		return after
	}
	provisional := make(map[[32]byte]int, len(media))
	for _, key := range media {
		provisional[key]++
	}
	allowed := make(map[[32]byte]int)
	for _, part := range before.Content {
		if part.Type != toolresult.ContentTypeImage && part.Type != toolresult.ContentTypeAudio {
			continue
		}
		key := settlementMediaKey(part)
		if provisional[key] > 0 {
			provisional[key]--
			continue
		}
		allowed[key]++
	}
	result := toolresult.FromErrorText("The tool failed; pending remote read output was discarded.")
	for _, part := range after.Content {
		if part.Type != toolresult.ContentTypeImage && part.Type != toolresult.ContentTypeAudio {
			continue
		}
		key := settlementMediaKey(part)
		if allowed[key] > 0 {
			result.Content = append(result.Content, part)
			allowed[key]--
		}
	}
	return result
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
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil, errors.New("parent settlement scope closed")
	}
	s.children++
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); s.children--; s.mu.Unlock() }) }, nil
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
	media := append([][32]byte(nil), s.media...)
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
		s.parent.media = append(s.parent.media, media...)
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
