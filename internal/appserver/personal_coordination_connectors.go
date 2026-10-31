package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type personalConnectorCoordination struct {
	Actor    personalconnectors.Actor `json:"actor"`
	Expected string                   `json:"expected,omitempty"`
	EffectID string                   `json:"effect_id,omitempty"`
}

type personalConnectorSnapshot struct {
	Authority string `json:"authority"`
}

func (s *Server) connectorSnapshot(ctx context.Context, actor personalconnectors.Actor) (string, error) {
	input, err := json.Marshal(personalConnectorCoordination{Actor: actor})
	if err != nil {
		return "", err
	}
	raw, err := s.invokePersonalCoordination(ctx, "connector-snapshot", personalCoordinationRequest{Input: input}, nil)
	if err != nil {
		return "", err
	}
	var result personalConnectorSnapshot
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Authority == "" {
		return "", errors.New("selected connector authority returned no snapshot")
	}
	return result.Authority, nil
}

// The coordinator receives a single-use effect, not permission to later call a
// broker outside its inspection. Releasing the token drains any in-flight use.
func (s *Server) connectorCommit(ctx context.Context, actor personalconnectors.Actor, expected string, use func(context.Context) error) error {
	if s.personalEffects == nil {
		return errors.New("Personal effect mechanisms are unavailable")
	}
	state, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return err
	}
	defer state.Close()
	ctx = s.personalEffects.Context(ctx, s, state, nil)
	var outcomeMu sync.Mutex
	var completed bool
	var outcome error
	id, release, err := s.personalEffects.RegisterConnector(ctx, state, func(effectCtx context.Context) error {
		err := use(effectCtx)
		outcomeMu.Lock()
		completed, outcome = true, err
		outcomeMu.Unlock()
		return err
	})
	if err != nil {
		return err
	}
	defer release()
	input, err := json.Marshal(personalConnectorCoordination{Actor: actor, Expected: expected, EffectID: id})
	if err != nil {
		return err
	}
	_, err = s.invokePersonalCoordination(ctx, "connector-commit", personalCoordinationRequest{Input: input}, nil)
	release()
	outcomeMu.Lock()
	defer outcomeMu.Unlock()
	if err != nil {
		return errors.Join(outcome, err)
	}
	if !completed {
		return errors.New("selected coordinator did not execute the connector effect")
	}
	return outcome
}

func (p *personalActions) coordinateConnector(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	var input personalConnectorCoordination
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if method == "connector-snapshot" {
		authority, err := p.connectorAuthority(ctx, input.Actor)
		if err != nil {
			return nil, err
		}
		return json.Marshal(personalConnectorSnapshot{Authority: authority})
	}
	if input.EffectID == "" {
		return nil, errors.New("connector effect is required")
	}
	err := p.withConnectorAuthorization(ctx, input.Actor, input.Expected, func(effectCtx context.Context) error {
		state, err := runtime.RetainProductInvocationState(effectCtx)
		if err != nil {
			return err
		}
		defer state.Close()
		request, err := json.Marshal(PersonalEffectReference{EffectID: input.EffectID})
		if err != nil {
			return err
		}
		_, callErr := state.Host.CallService(effectCtx, personalActionsConsumerID, PersonalEffectService, "execute", request)
		if callErr != nil {
			return callErr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), nil
}
