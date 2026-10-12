package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type backgroundInvoker struct{ parent *kernelHostServices }

func (k *backgroundInvoker) ID() string                { return k.parent.ID() }
func (k *backgroundInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *backgroundInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k.parent.mu.RLock()
	active := k.parent.active
	host, _ := k.parent.executions.(*pluginhost.Host)
	k.parent.mu.RUnlock()
	if !active || host == nil {
		return nil, serviceError("service_unavailable", "background service is unavailable")
	}
	switch params.Method {
	case "start":
		if pluginhost.InShutdownCleanup(ctx) {
			return nil, serviceError("generation_closed", "shutdown cleanup cannot start background work")
		}
		var input pluginhost.BackgroundStartParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		// Retain the initiating operation's attribution, not its foreground
		// cancellation, journal or workflow budget. Timers have no parent scope.
		provenance := context.Background()
		if scope, err := host.ResolveExecution(params.Caller, params.ExecutionID); err == nil {
			origin, ok := scope.Context.Value(modelRequestOriginKey{}).(pluginhost.ModelInvocationOriginV1)
			if !ok {
				origin = pluginhost.ModelInvocationOriginV1{Kind: "plugin", PluginID: params.Caller}
			}
			if origin.SessionID == "" {
				origin.SessionID = scope.SessionID
			}
			if origin.ThreadID == "" {
				origin.ThreadID = scope.ThreadID
			}
			provenance = context.WithValue(provenance, modelRequestOriginKey{}, origin)
		}
		result, err := host.StartBackground(provenance, params.Caller, input)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case "inspect", "cancel":
		var input pluginhost.BackgroundJobParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if params.Method == "inspect" {
			result, err := host.InspectBackground(params.Caller, input.JobID)
			if err != nil {
				return nil, err
			}
			return marshalServiceResult(result)
		}
		cancelled, err := host.CancelBackground(params.Caller, input.JobID)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(struct {
			Cancelled bool `json:"cancelled"`
		}{Cancelled: cancelled})
	default:
		return nil, serviceError("method_not_found", "background service method is unavailable")
	}
}
