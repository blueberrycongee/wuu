package pluginhost

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type modelEventSinkKey struct{}

type modelEventSink struct {
	pluginID    string
	executionID string
	emit        func(context.Context, pluginprotocol.ModelStreamEventV1) error
}

// WithModelEventSink binds a stream callback to the next exact capability
// execution. Descendant calls cannot use their own IDs to emit into it.
func WithModelEventSink(ctx context.Context, pluginID string, emit func(context.Context, pluginprotocol.ModelStreamEventV1) error) context.Context {
	return context.WithValue(ctx, modelEventSinkKey{}, modelEventSink{pluginID: pluginID, emit: emit})
}

func bindModelEventExecution(ctx context.Context, pluginID, executionID string) context.Context {
	if binding, ok := ctx.Value(modelContinuationBindingKey{}).(modelContinuationBinding); ok && binding.executionID == "" && binding.pluginID == pluginID {
		binding.executionID = executionID
		ctx = context.WithValue(ctx, modelContinuationBindingKey{}, binding)
	}
	sink, ok := ctx.Value(modelEventSinkKey{}).(modelEventSink)
	if !ok || sink.executionID != "" || sink.pluginID != pluginID {
		return ctx
	}
	sink.executionID = executionID
	return context.WithValue(ctx, modelEventSinkKey{}, sink)
}

// EmitModelEvent requires a live, exact streaming adapter or middleware
// execution. It does not confer model, tool, or credential authority.
func (h *Host) EmitModelEvent(ctx context.Context, pluginID, executionID string, event pluginprotocol.ModelStreamEventV1) error {
	scope, err := h.ResolveExecution(pluginID, executionID)
	if err != nil {
		return err
	}
	sink, ok := scope.Context.Value(modelEventSinkKey{}).(modelEventSink)
	if !ok || sink.pluginID != pluginID || sink.executionID != executionID || sink.emit == nil {
		return &HostServiceError{Code: "invalid_execution_scope", Message: "model events require the owning streaming model execution"}
	}
	if err := scope.Context.Err(); err != nil {
		return err
	}
	emitCtx, cancel := context.WithCancel(scope.Context)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	return sink.emit(emitCtx, event)
}

// A continuation authorizes only the immediately bound middleware execution.
// Descendant callbacks inherit context but never inherit its authority.
type modelContinuationBindingKey struct{}
type modelContinuationBinding struct{ pluginID, executionID, token string }

func WithModelContinuation(ctx context.Context, pluginID, token string) context.Context {
	return context.WithValue(ctx, modelContinuationBindingKey{}, modelContinuationBinding{pluginID: pluginID, token: token})
}
func ModelContinuationAuthorized(scope ExecutionScope, token string) bool {
	binding, ok := scope.Context.Value(modelContinuationBindingKey{}).(modelContinuationBinding)
	return ok && binding.pluginID == scope.PluginID && binding.executionID == scope.ID && binding.token == token
}
