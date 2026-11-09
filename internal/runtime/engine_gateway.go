package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type engineSessionGateway struct {
	owner       string
	binding     agentengine.ThreadBinding
	runtime     *ThreadRuntime
	mu          sync.Mutex
	sink        agentengine.EventSink
	runCtx      context.Context
	loopRunning bool
}
type engineGatewayTable struct {
	mu      sync.Mutex
	entries map[string]*engineSessionGateway
	closed  bool
}

func newEngineGatewayTable() *engineGatewayTable {
	return &engineGatewayTable{entries: map[string]*engineSessionGateway{}}
}
func (t *engineGatewayTable) register(g *engineSessionGateway) (string, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return "", func() {}
	}
	id := rand.Text()
	t.entries[id] = g
	return id, func() { t.mu.Lock(); delete(t.entries, id); t.mu.Unlock() }
}
func (t *engineGatewayTable) close() { t.mu.Lock(); t.closed = true; clear(t.entries); t.mu.Unlock() }
func engineGatewayDescriptor() pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"event", "persist_reference", "persist_turn", "settle_reference", "settle_turn", "approval", "input", "run_loop"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, Cleanup: name == "settle_turn" || name == "settle_reference", InputSchema: "engine.gateway.input.v1", OutputSchema: "engine.gateway.output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: pluginprotocol.EngineGatewayService, Version: "1.0.0", Methods: methods}
}

// Authentication is independent of the opaque handle. Every callback stays
// attached to the provider and thread that created this session.
func (t *engineGatewayTable) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var in pluginprotocol.EngineGatewayParams
	if err := decodeServiceParams(p.Params, &in); err != nil {
		return nil, err
	}
	t.mu.Lock()
	g := t.entries[in.GatewayID]
	t.mu.Unlock()
	if g == nil || g.owner != p.Caller {
		return nil, serviceError("execution_not_found", "engine callback is not owned by this provider")
	}
	switch p.Method {
	case "persist_reference", "settle_reference":
		if g.binding.PersistRef == nil {
			return nil, errors.New("engine reference persistence is unavailable")
		}
		if err := g.binding.PersistRef(in.Reference); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "persist_turn", "settle_turn":
		if p.Method == "settle_turn" && in.Pending {
			return nil, errors.New("terminal settlement cannot admit a pending turn")
		}
		if g.binding.PersistTurnRef == nil {
			return nil, errors.New("engine turn persistence is unavailable")
		}
		if err := g.binding.PersistTurnRef(in.Reference, in.Pending); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "approval":
		if in.Approval == nil {
			return nil, errors.New("engine approval request is required")
		}
		request := engineApprovalFromWire(*in.Approval)
		if g.binding.RequestApproval == nil {
			return json.Marshal(agentengine.ApprovalDecline)
		}
		decision, err := g.binding.RequestApproval(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(decision)
	case "input":
		if in.Input == nil {
			return nil, errors.New("engine user input request is required")
		}
		request := engineUserInputRequestFromWire(*in.Input)
		if g.binding.RequestUserInput == nil {
			return nil, errors.New("engine user input is unavailable")
		}
		result, err := g.binding.RequestUserInput(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "event":
		if in.Event == nil {
			return nil, errors.New("engine event is required")
		}
		event, err := engineEventFromWire(*in.Event)
		if err != nil {
			return nil, err
		}
		g.mu.Lock()
		sink, runCtx := g.sink, g.runCtx
		g.mu.Unlock()
		if runCtx == nil || runCtx.Err() != nil {
			return nil, errors.New("engine turn is not active")
		}
		if sink != nil {
			sink(event)
		}
		return json.RawMessage(`{}`), nil
	case "run_loop":
		g.mu.Lock()
		runCtx, sink := g.runCtx, g.sink
		if runCtx == nil || g.loopRunning || g.runtime == nil || g.runtime.StreamRunner == nil {
			g.mu.Unlock()
			return nil, errors.New("selected loop resources are unavailable")
		}
		g.loopRunning = true
		g.mu.Unlock()
		defer func() { g.mu.Lock(); g.loopRunning = false; g.mu.Unlock() }()
		callCtx, cancel := context.WithCancel(runCtx)
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		result, err := g.runtime.StreamRunner.RunWithCallback(callCtx, modelwire.DriverMessagesFromWire(in.Messages), agent.StreamCallback(sink))
		return json.Marshal(pluginprotocol.EngineRunResult{Result: engineResultToWire(result), Failure: loopdriver.FailureToWire(err)})
	default:
		return nil, serviceError("method_not_found", "unknown engine callback")
	}
}

type engineGatewayInvoker struct{ parent *kernelHostServices }

func (i *engineGatewayInvoker) ID() string                { return i.parent.ID() }
func (i *engineGatewayInvoker) Status() pluginhost.Status { return i.parent.Status() }
func (i *engineGatewayInvoker) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return i.parent.engineGateways.InvokeService(ctx, p)
}
