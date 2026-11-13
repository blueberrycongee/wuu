package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Signal physical work before touching the ledger: a running effect retains
// task and policy inspections until it exits. Waiting on those writes first
// would prevent cancellation from reaching the process that holds them.
func (p *personalActions) cancelExecution(ctx context.Context, sessionID string, exact ...personalreview.Scope) error {
	spaceID := p.server.rt.UserBinding().UserSpaceID
	if sessionID == "" || len(exact) > 1 || len(exact) == 1 && (exact[0].SpaceID != spaceID || exact[0].SessionID != sessionID || exact[0].TaskID == "" || exact[0].RequirementsRevision < 1 || exact[0].ContextDigest == "") {
		return errors.New("cancellation requires a matching execution scope")
	}
	records, err := p.engine.Records(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Action.Scope.SessionID != sessionID || len(exact) == 1 && record.Action.Scope != exact[0] {
			continue
		}
		p.kitsMu.Lock()
		token := p.effects[record.Action.ID]
		p.kitsMu.Unlock()
		if token == "" {
			continue
		}
		p.admissionsMu.Lock()
		admission, found := p.admissions[record.Action.Key()]
		var state *runtime.State
		if found {
			state = admission.state.Retain()
		}
		p.admissionsMu.Unlock()
		if state == nil {
			continue
		}
		err := p.callEffect(runtime.WithInvocationState(ctx, state), nil, "release", PersonalEffectReference{EffectID: token}, nil)
		closeErr := state.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	return p.engine.CancelExecution(ctx, spaceID, sessionID, exact...)
}
