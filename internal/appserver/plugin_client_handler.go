package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const maxPluginClientPayloadBytes = 1 << 20

// PluginClientRequestParams is a generation-bound opaque request from a Wuu
// client to one plugin runtime. Method and input are plugin-owned contracts.
type PluginClientRequestParams struct {
	ID          string          `json:"id"`
	Fingerprint string          `json:"fingerprint"`
	Method      string          `json:"method"`
	Input       json.RawMessage `json:"input,omitempty"`
	// RequestID opts into independently cancellable, asynchronous admission.
	// It is scoped to this connection and exact plugin/fingerprint pair.
	RequestID string `json:"request_id,omitempty"`
}

type PluginClientRequestResult struct {
	Result json.RawMessage `json:"result,omitempty"`
}

type PluginClientCancelParams struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	RequestID   string `json:"request_id"`
}

type PluginClientCancelResult struct {
	Cancelled bool `json:"cancelled"`
}

type pluginClientRequestKey struct {
	pluginID    string
	fingerprint string
	requestID   string
}

type pluginClientRequest struct {
	cancel context.CancelFunc
}

func (s *Server) handlePluginClientRequest(ctx context.Context, req Request) error {
	var params PluginClientRequestParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	params.ID = strings.TrimSpace(params.ID)
	params.Fingerprint = strings.TrimSpace(params.Fingerprint)
	method := strings.TrimSpace(params.Method)
	if method == "" {
		return s.writeResponse(req.ID, nil, errors.New("plugin client method is required"))
	}
	if len(method) > 128 {
		return s.writeResponse(req.ID, nil, errors.New("plugin client method exceeds 128 bytes"))
	}
	if len(params.Input) > maxPluginClientPayloadBytes {
		return s.writeResponse(req.ID, nil, errors.New("plugin client input exceeds 1048576 bytes"))
	}
	if len(params.Input) != 0 && !json.Valid(params.Input) {
		return s.writeResponse(req.ID, nil, errors.New("plugin client input must be valid JSON"))
	}
	requestID := strings.TrimSpace(params.RequestID)
	if params.RequestID != "" && (requestID == "" || len(requestID) > 128) {
		return s.writeResponse(req.ID, nil, errors.New("plugin client request_id must contain 1-128 bytes"))
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("plugin runtime is unavailable"))
	}
	if s.closed.Load() {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	generation := s.rt.RetainPluginGeneration()
	releaseGeneration := func() { s.rt.ReleasePluginGeneration(generation) }
	plugin, host, err := s.requireActiveDesktopPluginHost(params.ID, params.Fingerprint)
	if err == nil && generation != nil && !s.rt.IsCurrentPluginGeneration(generation) {
		err = errors.New("plugin generation changed during request admission")
	}
	if err != nil {
		releaseGeneration()
		return s.writeResponse(req.ID, nil, err)
	}
	if host == nil {
		releaseGeneration()
		return s.writeResponse(req.ID, nil, errors.New("plugin runtime is unavailable"))
	}
	capability, ok := host.Capability(plugin.ID, pluginhost.CapabilityPluginClientRequest)
	if !ok {
		releaseGeneration()
		return s.writeResponse(req.ID, nil, errors.New("plugin does not accept client requests"))
	}
	invoke := func(callCtx context.Context) error {
		defer releaseGeneration()
		return s.invokePluginClientRequest(callCtx, req.ID, host, capability, method, params.Input)
	}
	if requestID == "" {
		return invoke(ctx)
	}

	key := pluginClientRequestKey{pluginID: params.ID, fingerprint: params.Fingerprint, requestID: requestID}
	callCtx, cancel := context.WithCancel(ctx)
	request := &pluginClientRequest{cancel: cancel}
	s.pluginClientMu.Lock()
	if s.closed.Load() {
		s.pluginClientMu.Unlock()
		cancel()
		releaseGeneration()
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	if s.pluginClientRequests[key] != nil {
		s.pluginClientMu.Unlock()
		cancel()
		releaseGeneration()
		return s.writeResponse(req.ID, nil, errors.New("plugin client request_id is already active"))
	}
	if s.pluginClientRequests == nil {
		s.pluginClientRequests = make(map[pluginClientRequestKey]*pluginClientRequest)
	}
	// Publish ownership before returning to the stdio scanner. A cancellation
	// in the very next frame must find this request even before its goroutine runs.
	s.pluginClientRequests[key] = request
	s.pluginClientMu.Unlock()
	cleanup := func() {
		cancel()
		s.pluginClientMu.Lock()
		if s.pluginClientRequests[key] == request {
			delete(s.pluginClientRequests, key)
		}
		s.pluginClientMu.Unlock()
	}
	if !s.startBackground(func() {
		defer cleanup()
		if err := invoke(callCtx); err != nil {
			providers.DebugLogf("write plugin client response: %v", err)
		}
	}) {
		cleanup()
		releaseGeneration()
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	return nil
}

func (s *Server) invokePluginClientRequest(ctx context.Context, id json.RawMessage, host *pluginhost.Host, capability pluginhost.RegisteredCapability, method string, input json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return s.writeResponse(id, nil, err)
	}
	output := pluginhost.PluginClientRequestOutput{}
	if err := host.InvokeCapability(ctx, capability, pluginhost.PluginClientRequestInput{
		Method: method,
		Input:  append(json.RawMessage(nil), input...),
	}, &output); err != nil {
		if cancelled := ctx.Err(); cancelled != nil {
			return s.writeResponse(id, nil, cancelled)
		}
		if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
			return s.writeResponse(id, nil, policyErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return s.writeResponse(id, nil, err)
	}
	if len(output.Result) > maxPluginClientPayloadBytes {
		return s.writeResponse(id, nil, errors.New("plugin client result exceeds 1048576 bytes"))
	}
	if len(output.Result) != 0 && !json.Valid(output.Result) {
		return s.writeResponse(id, nil, errors.New("plugin client result must be valid JSON"))
	}
	return s.writeResponse(id, PluginClientRequestResult{Result: output.Result}, nil)
}

func (s *Server) handlePluginClientCancel(req Request) error {
	var params PluginClientCancelParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	key := pluginClientRequestKey{
		pluginID: strings.TrimSpace(params.ID), fingerprint: strings.TrimSpace(params.Fingerprint), requestID: strings.TrimSpace(params.RequestID),
	}
	if key.pluginID == "" || key.fingerprint == "" || key.requestID == "" || len(key.requestID) > 128 {
		return s.writeResponse(req.ID, nil, errors.New("plugin id, fingerprint and request_id (1-128 bytes) are required"))
	}
	// Do not require the fingerprint to remain current: callers must be able
	// to cancel their old generation after a reload or disable has started.
	s.pluginClientMu.Lock()
	request := s.pluginClientRequests[key]
	s.pluginClientMu.Unlock()
	if request != nil {
		request.cancel()
	}
	return s.writeResponse(req.ID, PluginClientCancelResult{Cancelled: request != nil}, nil)
}

func (s *Server) cancelPluginClientRequests() {
	s.pluginClientMu.Lock()
	requests := s.pluginClientRequests
	s.pluginClientRequests = nil
	s.pluginClientMu.Unlock()
	for _, request := range requests {
		request.cancel()
	}
}
