package providers

import (
	"context"
	"errors"
	"sync"
)

type pendingAttemptTerminal struct {
	attempt InferenceAttempt
	outcome InferenceTerminalOutcome
	failure NormalizedFailure
}
type pendingOperationTerminal struct {
	outcome InferenceTerminalOutcome
	failure NormalizedFailure
}

// RetainPhysicalReceipt keeps durable logical completion behind this exact
// adapter dispatch, not behind the whole receipt scope (which includes its own
// observer). Logical cancellation remains immediate. Only issued submissions
// may settle after a terminal intent; no new attempt may be admitted.
func (a InferenceAttempt) RetainPhysicalReceipt() (func() error, error) {
	if a.remote != nil || !a.Valid() {
		return nil, errors.New("physical receipt ownership belongs to the host attempt")
	}
	e := a.execution
	e.mu.Lock()
	if e.journalErr != nil {
		err := e.journalErr
		e.mu.Unlock()
		return nil, err
	}
	if e.terminal || e.terminalRequested {
		e.mu.Unlock()
		return nil, errors.New("inference operation is already closing")
	}
	e.physicalReceipts++
	e.mu.Unlock()
	var once sync.Once
	var result error
	return func() error {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.physicalReceipts--
			if e.physicalReceipts != 0 {
				return
			}
			if e.journalErr != nil {
				result = e.journalErr
				return
			}
			if e.journal != nil {
				if result = e.checkpointSubmissionsLocked(context.Background(), ""); result != nil {
					return
				}
			}
			pending := e.pendingAttempts
			e.pendingAttempts = nil
			for _, terminal := range pending {
				if result = e.completeAttemptLocked(terminal.attempt, terminal.outcome, terminal.failure); result != nil {
					return
				}
			}
			if terminal := e.pendingTerminal; terminal != nil {
				e.pendingTerminal = nil
				result = e.completeLocked(terminal.outcome, terminal.failure)
			}
		})
		return result
	}, nil
}
