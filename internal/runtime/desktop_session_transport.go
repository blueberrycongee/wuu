package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const DesktopSessionTransportModule = "wuu.desktop-session-transport"
const DesktopSessionGatewayService = "host.desktop.session"

type DesktopSessionPrepareHandler func(context.Context, string, string, pluginhost.SessionRouteRequest) (pluginhost.SessionRouteRequest, func(), error)
type DesktopSessionDispatchHandler func(context.Context, string, string) (json.RawMessage, error)

// BindDesktopSessionTransport binds a host-owned ticket gateway, not an arbitrary
// reverse-RPC proxy. Old hosts cannot unbind a replacement host's authority.
func (r *PluginSessionRouter) BindDesktopSessionTransport(prepare DesktopSessionPrepareHandler, dispatch DesktopSessionDispatchHandler) func() {
	r.mu.Lock()
	r.desktopBinding++
	id := r.desktopBinding
	r.desktopPrepare = prepare
	r.desktopDispatch = dispatch
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.desktopBinding == id {
				r.desktopPrepare = nil
				r.desktopDispatch = nil
			}
		})
	}
}
func (r *PluginSessionRouter) prepareDesktopRoute(ctx context.Context, caller, provider string, request pluginhost.SessionRouteRequest) (pluginhost.SessionRouteRequest, func(), error) {
	r.mu.RLock()
	handler := r.desktopPrepare
	r.mu.RUnlock()
	if handler == nil {
		return request, nil, errors.New("local desktop session bridge is unavailable")
	}
	return handler(ctx, caller, provider, request)
}
func (r *PluginSessionRouter) dispatchDesktopTicket(ctx context.Context, provider, ticket string) (json.RawMessage, error) {
	r.mu.RLock()
	handler := r.desktopDispatch
	r.mu.RUnlock()
	if handler == nil {
		return nil, errors.New("local desktop session bridge is unavailable")
	}
	return handler(ctx, provider, ticket)
}
func desktopGatewayDescriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: DesktopSessionGatewayService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "dispatch", InputSchema: "desktop.session.ticket.v1", OutputSchema: "session.transport.dispatch.output.v1"}}}
}

type desktopSessionGateway struct{ parent *kernelHostServices }

func (i *desktopSessionGateway) ID() string                { return kernelHostModule }
func (i *desktopSessionGateway) Status() pluginhost.Status { return i.parent.Status() }

func (i *desktopSessionGateway) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method != "dispatch" || p.Service != DesktopSessionGatewayService {
		return nil, errors.New("unknown desktop session gateway method")
	}
	var input struct {
		TicketID string `json:"ticket_id"`
	}
	if err := decodeServiceParams(p.Params, &input); err != nil {
		return nil, err
	}
	if input.TicketID == "" || i.parent.sessionRouter == nil {
		return nil, errors.New("host-issued desktop session ticket is required")
	}
	return i.parent.sessionRouter.dispatchDesktopTicket(ctx, p.Caller, input.TicketID)
}

type desktopSessionProvider struct {
	mu       sync.Mutex
	state    pluginhost.State
	registry *pluginhost.ServiceRegistry
}

func desktopSessionProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: DesktopSessionTransportModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		return &desktopSessionProvider{state: pluginhost.StatePrepared, registry: input.Registry}, nil
	}}
}
func (p *desktopSessionProvider) ID() string { return DesktopSessionTransportModule }
func (p *desktopSessionProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pluginhost.Status{ID: p.ID(), State: p.state}
}
func (p *desktopSessionProvider) Prepare(ctx context.Context) error { return ctx.Err() }
func (p *desktopSessionProvider) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = pluginhost.StateActive
	return nil
}
func (p *desktopSessionProvider) Close(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = pluginhost.StateStopped
	return nil
}
func (p *desktopSessionProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.SessionTransportDescriptor()}
}
func (p *desktopSessionProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: DesktopSessionGatewayService, MajorVersion: 1, Required: true}}
}
func (p *desktopSessionProvider) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Status().State != pluginhost.StateActive || call.Service != pluginhost.SessionTransportService || call.Method != "dispatch" {
		return nil, errors.New("desktop session transport unavailable")
	}
	var request pluginhost.SessionRouteRequest
	if err := decodeServiceParams(call.Params, &request); err != nil {
		return nil, err
	}
	if request.TransportTicket == nil {
		return nil, errors.New("desktop session transport requires a host-issued ticket")
	}
	raw, _ := json.Marshal(map[string]string{"ticket_id": request.TransportTicket.ID})
	result, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: DesktopSessionGatewayService, Method: "dispatch", Params: raw})
	if err != nil {
		return nil, err
	}
	return result, nil
}
