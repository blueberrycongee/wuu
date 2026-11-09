package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
)

type selectedToolBatch struct{ host *pluginhost.Host }

func newSelectedToolBatch(host *pluginhost.Host) toolbatch.Executor {
	return &selectedToolBatch{host: host}
}
func (e *selectedToolBatch) SupportsStreaming() bool {
	if e.host == nil || e.host.ServiceRegistry() == nil {
		return false
	}
	service, found := e.host.ServiceRegistry().ServiceDescriptorFor(toolExecutionConsumer, toolbatch.ServiceName, 1)
	if found {
		for _, method := range service.Methods {
			if method.Name == "stream_admit" {
				return true
			}
		}
	}
	return false
}
func (e *selectedToolBatch) AdmitStream(ctx context.Context, request toolbatch.StreamRequest) (bool, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	response, callErr := e.host.CallService(ctx, toolExecutionConsumer, toolbatch.ServiceName, "stream_admit", raw)
	if callErr != nil {
		return false, errors.Join(callErr, ctx.Err())
	}
	var admission toolbatch.StreamAdmission
	if err := toolbatch.Decode(response, &admission); err != nil {
		return false, err
	}
	return admission.Admit, nil
}
func (e *selectedToolBatch) ExecuteBatch(ctx context.Context, request toolbatch.Request, resources toolbatch.Resources) error {
	host := e.host
	if host == nil || host.ServiceRegistry() == nil {
		return errors.New("tool batch service registry is unavailable")
	}
	owner, ok := host.ServiceRegistry().ProviderPluginIDFor(toolExecutionConsumer, toolbatch.ServiceName, 1)
	if !ok {
		return errors.New("selected tool batch provider is unavailable")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx = toolbatch.WithResources(ctx, owner, request.ID, resources)
	result, callErr := host.CallService(ctx, toolExecutionConsumer, toolbatch.ServiceName, "execute", raw)
	if callErr != nil {
		return errors.Join(callErr, ctx.Err())
	}
	var completed toolbatch.Completion
	if err := toolbatch.Decode(result, &completed); err != nil {
		return err
	}
	if !completed.Completed {
		return errors.New("tool batch provider did not return a completion receipt")
	}
	return nil
}

type toolBatchResourceInvoker struct{ parent *kernelHostServices }

func (i *toolBatchResourceInvoker) ID() string                { return i.parent.ID() }
func (i *toolBatchResourceInvoker) Status() pluginhost.Status { return i.parent.Status() }
func (*toolBatchResourceInvoker) Close(context.Context) error { return nil }
func (*toolBatchResourceInvoker) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return toolbatch.InvokeResources(ctx, p)
}
