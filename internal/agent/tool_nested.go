package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
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
	s.mu.Lock()
	r := s.runtime
	r.mu.Lock()
	if r.canceled || s.ctx.Err() != nil {
		r.mu.Unlock()
		s.mu.Unlock()
		return toolresult.Result{}, context.Canceled
	}
	run := s.calls[call.ID]
	if run != nil {
		if run.call.Name != call.Name || run.call.Arguments != call.Arguments || run.call.Kind != call.Kind {
			r.mu.Unlock()
			s.mu.Unlock()
			return toolresult.Result{}, errors.New("nested call ID reused with different arguments")
		}
	} else {
		if len(s.calls) >= 1024 {
			r.mu.Unlock()
			s.mu.Unlock()
			return toolresult.Result{}, errors.New("nested tool call limit reached")
		}
		localID := call.ID
		// Never trust a child's local key as a global/provider call identity.
		call = providers.ToolCall{ID: "nested-" + rand.Text(), Name: call.Name, Arguments: call.Arguments, Kind: call.Kind}
		var receipt *toolledger.Invocation
		if s.parent.invocationAttempt > 0 {
			local := call
			local.ID = localID
			admitted, err := r.ledger.PrepareScopedChild(childCtx, s.parent.invocationID, s.parent.invocationAttempt, local, toolReplayPolicy(r.executor, local))
			if err != nil {
				r.mu.Unlock()
				s.mu.Unlock()
				return toolresult.Result{}, toolerrors.Unknown(err)
			}
			call.ID = admitted.ProviderCallID
			receipt = &admitted
		}
		run = r.runForCallLocked(call)
		if receipt != nil {
			run.invocationID, run.invocationAttempt = receipt.ID, receipt.Attempt
			if receipt.State == toolledger.InvocationSucceeded || receipt.State == toolledger.InvocationFailed {
				run.complete(receipt.Result.Clone(), nil)
			}
		}
		run.parent, run.depth = s.parent, s.parent.depth+1
		s.calls[localID] = run
		r.startRunLocked(childCtx, run, false)
	}
	r.mu.Unlock()
	s.mu.Unlock()
	return r.awaitRunResult(childCtx, run)
}

// Only durably admitted parents receive this capability. Keeping it off the
// ordinary nested scope leaves the desktop's lightweight execution unchanged.
type durableNestedToolScope struct{ *nestedToolScope }

func (s *durableNestedToolScope) CodeJournal(ctx context.Context, request toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
	if err := s.ctx.Err(); err != nil {
		return toolctx.CodeJournalResponse{}, err
	}
	return s.runtime.ledger.CodeJournal(ctx, s.parent.invocationID, s.parent.invocationAttempt, request)
}
