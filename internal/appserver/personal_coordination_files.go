package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func (p *personalActions) callEffect(ctx context.Context, kit *tools.Toolkit, method string, input any, output any) error {
	state, err := p.retainState(ctx)
	if err != nil {
		return err
	}
	defer state.Close()
	if p.server.personalEffects == nil {
		return errors.New("selected Personal effects unavailable")
	}
	ctx = p.server.personalEffects.Context(runtime.WithInvocationState(ctx, state), p.server, state, kit)
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, callErr := state.Host.CallService(ctx, personalActionsConsumerID, PersonalEffectService, method, raw)
	if callErr != nil {
		return callErr
	}
	if output != nil {
		return json.Unmarshal(result, output)
	}
	return nil
}
func (p *personalActions) prepareFileEffect(ctx context.Context, kit *tools.Toolkit, call providers.ToolCall) (PersonalPreparedFile, error) {
	var prepared PersonalPreparedFile
	err := p.callEffect(ctx, kit, "prepare-file", call, &prepared)
	return prepared, err
}
func (p *personalActions) executeFileEffect(ctx context.Context, id string) (toolresult.Result, error) {
	p.kitsMu.Lock()
	token := p.effects[id]
	p.kitsMu.Unlock()
	if token == "" {
		return toolresult.Result{}, errors.New("reviewed file effect is unavailable")
	}
	var result PersonalEffectResult
	err := p.callEffect(ctx, nil, "execute", PersonalEffectReference{EffectID: token}, &result)
	return result.ToolResult, err
}
