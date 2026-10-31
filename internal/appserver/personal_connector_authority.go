package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"strconv"
)

// connectorAuthority fingerprints host-owned task state, not tool arguments.
// Confirmation is invalid after task cancellation, a new requirement revision,
// session archival, plugin disablement, or a newer human turn in the main chat.
func (p *personalActions) connectorAuthority(ctx context.Context, actor personalconnectors.Actor) (string, error) {
	if err := p.server.authorizePersonalOperation(ctx, "connectors.call", actor.SessionID); err != nil {
		return "", err
	}
	binding := p.server.rt.UserBinding()
	if actor.ProductDomain != "personal" || actor.UserSpaceID != binding.UserSpaceID || actor.SessionID == "" || p.server.rt.SafeMode {
		return "", errors.New("Personal connector authority is unavailable")
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return "", err
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	selected, _, err := p.selectedTaskProvider(state)
	if err != nil {
		return "", err
	}
	return p.connectorAuthorityForState(ctx, state, selected, actor)
}
func (p *personalActions) connectorAuthorityForState(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin, actor personalconnectors.Actor) (string, error) {
	binding := p.server.rt.UserBinding()
	if actor.ProductDomain != binding.ProductDomain || actor.UserSpaceID != binding.UserSpaceID || actor.SessionID == "" || p.server.rt.SafeMode {
		return "", errors.New("Personal connector authority is unavailable")
	}
	metadata, found, err := state.Sessions.Find(actor.SessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("Personal session is unavailable")
	}
	taskID := ""
	if metadata.ParentID == "" {
		// Select the latest physical human source, then require its exact active
		// projection in authority below. Falling back to an older active source
		// after an edit could revive an earlier connector confirmation.
		head, err := state.Sessions.WithContext(ctx).ReadHistoryPage(actor.SessionID, 1, 1)
		if err != nil {
			return "", err
		}
		start := max(1, head.HeadSeq-255)
		page, err := state.Sessions.WithContext(ctx).ReadHistoryPage(actor.SessionID, start, 256)
		if err != nil {
			return "", err
		}
		seq := 0
		for _, record := range page.Records {
			if record.Role == "user" && record.Origin == "user" && !record.Hidden && !record.ReadOnly {
				seq = record.Seq
			}
		}
		if seq == 0 {
			return "", errors.New("human source turn is unavailable")
		}
		taskID = "conversation:" + actor.SessionID + ":" + strconv.Itoa(seq)
	}
	authority, err := p.authority(ctx, state, selected, actor.SessionID, taskID)
	if err != nil {
		return "", err
	}
	if !authority.Active || authority.BoundaryReason != "" {
		return "", errors.New("Personal task authority changed")
	}
	if _, _, err := p.selectedTaskProvider(state); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Scope  any
		Plugin string
	}{authority.Scope, selected.Fingerprint})
	if err != nil {
		return "", err
	}
	return personalHash(raw), nil
}
