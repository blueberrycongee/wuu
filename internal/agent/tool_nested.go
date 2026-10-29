package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func toolIsOrchestrator(executor ToolExecutor, call providers.ToolCall) bool {
	provider, ok := executor.(ToolMetadataProvider)
	if !ok {
		return false
	}
	metadata, found := provider.ToolMetadata(call)
	return found && metadata.Orchestrator
}

type nestedToolScope struct {
	runtime *TurnToolRuntime
	parent  *toolRun
	ctx     context.Context
	mu      sync.Mutex
	calls   map[string]*toolRun
	pending map[string]*nestedMetadataLookup
}

type nestedMetadataLookup struct {
	call providers.ToolCall
	done chan struct{}
	run  *toolRun
	err  error
}

func sameNestedCall(a, b providers.ToolCall) bool {
	return a.Name == b.Name && a.Arguments == b.Arguments && a.Kind == b.Kind
}

func (s *nestedToolScope) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
		return toolresult.Result{}, errors.New("nested tool requires a local call ID and name")
	}
	if s.parent.depth >= 8 {
		return toolresult.Result{}, errors.New("nested tool depth limit reached")
	}
	// Both the owner and this invocation can cancel a child; neither can keep
	// it alive after the other has ended. Observation detachment is separate.
	childCtx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if err := s.ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	// Reserve each distinct ID before making any provider call. Pending
	// lookups count toward the same bound as admitted nested calls. Duplicates
	// share the lookup; canceling a duplicate waiter does not cancel its owner.
	// The first caller owns the lookup and execution: its cancellation also
	// cancels duplicates waiting for that lookup.
	localID := call.ID
	s.mu.Lock()
	if existing := s.calls[localID]; existing != nil {
		if !sameNestedCall(existing.call, call) {
			s.mu.Unlock()
			return toolresult.Result{}, errors.New("nested call ID reused with different arguments")
		}
		s.mu.Unlock()
		return s.runtime.awaitRunResult(childCtx, existing)
	}
	if pending := s.pending[localID]; pending != nil {
		if !sameNestedCall(pending.call, call) {
			s.mu.Unlock()
			return toolresult.Result{}, errors.New("nested call ID reused with different arguments")
		}
		s.mu.Unlock()
		select {
		case <-pending.done:
			if pending.err != nil {
				return toolresult.Result{}, pending.err
			}
			return s.runtime.awaitRunResult(childCtx, pending.run)
		case <-ctx.Done():
			return toolresult.Result{}, ctx.Err()
		case <-s.ctx.Done():
			return toolresult.Result{}, s.ctx.Err()
		}
	}
	if len(s.calls)+len(s.pending) >= 1024 {
		s.mu.Unlock()
		return toolresult.Result{}, errors.New("nested tool call limit reached")
	}
	if s.pending == nil {
		s.pending = make(map[string]*nestedMetadataLookup)
	}
	pending := &nestedMetadataLookup{call: call, done: make(chan struct{})}
	s.pending[localID] = pending
	s.mu.Unlock()

	call = providers.ToolCall{ID: "nested-" + rand.Text(), Name: call.Name, Arguments: call.Arguments, Kind: call.Kind}
	var metadata *ToolMetadata
	var lookupErr error
	if provider, ok := s.runtime.executor.(ToolMetadataContextProvider); ok {
		// A serial external provider cannot re-enter itself while its outer
		// callback is waiting. Bound the lookup without holding runtime locks.
		lookupCtx, stopLookup := context.WithTimeout(childCtx, 5*time.Second)
		resolved, found, err := provider.ToolMetadataContext(lookupCtx, call)
		stopLookup()
		if err != nil {
			lookupErr = fmt.Errorf("nested tool metadata unavailable: %w", err)
		}
		if !found {
			resolved = ToolMetadata{}
		}
		metadata = &resolved
	}
	s.mu.Lock()
	r := s.runtime
	r.mu.Lock()
	if lookupErr == nil && (r.canceled || s.ctx.Err() != nil || childCtx.Err() != nil) {
		lookupErr = context.Canceled
	}
	var run *toolRun
	if lookupErr == nil {
		run = r.runForCallWithMetadataLocked(call, metadata)
		run.parent, run.depth = s.parent, s.parent.depth+1
		s.calls[localID] = run
		r.startRunLocked(childCtx, run, false)
	}
	pending.run, pending.err = run, lookupErr
	delete(s.pending, localID)
	close(pending.done)
	r.mu.Unlock()
	s.mu.Unlock()
	if lookupErr != nil {
		return toolresult.Result{}, lookupErr
	}
	return r.awaitRunResult(childCtx, run)
}
