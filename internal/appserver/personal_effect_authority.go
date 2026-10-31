package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Each client keeps the exact consumer's selected binding. Cleanup participates
// in the same retained service scope even after ordinary admission is revoked.
func inspectionServiceCall(host *pluginhost.Host, scope *pluginhost.ServiceScope, consumer, service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "release-inspection" || method == "ReleaseInspection" {
			return scope.CallCleanup(ctx, service, method, raw)
		}
		result, err := host.CallService(ctx, consumer, service, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

// No runtime/global catalog mutex spans provider RPC. The ordinary service
// execution scopes cancel on retirement and remain retained through physical
// cleanup. Durable provider guards serialize task, session and policy writers.
func (p *personalActions) withTaskInspection(ctx context.Context, use func(context.Context, *runtime.State, pluginpkg.Plugin) error) (err error) {
	select {
	case p.effectGate <- struct{}{}:
		defer func() { <-p.effectGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return err
	}
	defer state.Close()
	selected, _, err := p.selectedTaskProvider(state)
	if err != nil {
		return err
	}
	ownerScope, err := state.Host.OpenServiceScope(ctx, personalActionsConsumerID, "personal.agents")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ownerScope.Close()) }()
	ctx = runtime.WithInvocationState(ownerScope.Context, state)
	// GetSettings may create the default policy row; bootstrap before fencing
	// its namespace. Inside the guard the same selected ledger is read-only.
	if _, err = p.store.GetSettings(ctx, p.server.rt.UserBinding().UserSpaceID); err != nil {
		return err
	}
	settingsScope, err := state.Host.OpenServiceScope(ctx, runtime.StateServicesConsumer, pluginsettings.ServiceName)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, settingsScope.Close()) }()
	settings := pluginsettings.NewClient(inspectionServiceCall(state.Host, settingsScope, runtime.StateServicesConsumer, pluginsettings.ServiceName))
	return settings.WithStateInspection(settingsScope.Context, selected.StorageSubject(), pluginsettings.ScopeUser, func(_ pluginsettings.StateDocument) (err error) {
		sessionScope, err := state.Host.OpenServiceScope(settingsScope.Context, runtime.SessionStateConsumer, session.ServiceName)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, sessionScope.Close()) }()
		sessions := session.NewClient(sessionScope.Context, inspectionServiceCall(state.Host, sessionScope, runtime.SessionStateConsumer, session.ServiceName))
		return sessions.WithInspection(sessionScope.Context, func() (err error) {
			// Fence the same captured ledger used by this operation, including an
			// admitted action whose generation predates the current publication.
			reviewScope, err := state.Host.OpenServiceScope(sessionScope.Context, personalActionsConsumerID, persistence.ServiceName)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, reviewScope.Close()) }()
			guardStore, err := personalreview.NewDocumentStore(persistence.NewClient(inspectionServiceCall(state.Host, reviewScope, personalActionsConsumerID, persistence.ServiceName)))
			if err != nil {
				return err
			}
			defer guardStore.Close()
			return guardStore.WithPolicyInspection(reviewScope.Context, func() error {
				effectCtx := runtime.WithInvocationState(reviewScope.Context, state)
				if _, _, err := p.selectedTaskProvider(state); err != nil {
					return err
				}
				if err := effectCtx.Err(); err != nil {
					return err
				}
				return use(effectCtx, state, selected)
			})
		})
	})
}

func (p *personalActions) withExecutionAuthority(ctx context.Context, scope personalreview.Scope, use func(context.Context, personalreview.Authority) error) error {
	if err := p.server.authorizePersonalOperation(ctx, "review.execution.status", scope.SessionID); err != nil {
		return err
	}
	return p.withTaskInspection(ctx, func(effectCtx context.Context, state *runtime.State, selected pluginpkg.Plugin) error {
		authority, err := p.authority(effectCtx, state, selected, scope.SessionID, scope.TaskID)
		if err != nil {
			return err
		}
		if err := p.server.AuthorizeResource(effectCtx, "personal/file.execute", access.Resource{Kind: "user-space", ID: scope.SpaceID}, access.Resource{Kind: "session", ID: scope.SessionID}); err != nil {
			return err
		}
		return use(effectCtx, authority)
	})
}

func (p *personalActions) withConnectorAuthorization(ctx context.Context, actor personalconnectors.Actor, expected string, use func(context.Context) error) error {
	if expected == "" {
		return errors.New("connector execution authority is required")
	}
	if err := p.server.authorizePersonalOperation(ctx, "connectors.call", actor.SessionID); err != nil {
		return err
	}
	return p.withTaskInspection(ctx, func(effectCtx context.Context, state *runtime.State, selected pluginpkg.Plugin) error {
		current, err := p.connectorAuthorityForState(effectCtx, state, selected, actor)
		if err != nil {
			return err
		}
		if current != expected {
			return errors.New("Personal connector authority changed")
		}
		if err := p.server.AuthorizeResource(effectCtx, "personal/connectors.execute", access.Resource{Kind: "user-space", ID: actor.UserSpaceID}, access.Resource{Kind: "session", ID: actor.SessionID}); err != nil {
			return err
		}
		return use(effectCtx)
	})
}
