package appserver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

const maxUserHostConnections = 16
const maxUserHostClientQueuedBytes = 32 * 1024 * 1024

var ErrUserHostClientOverflow = errors.New("user host client fell behind; reconnect and read current state")

// UserHost owns one execution authority independently of client connections.
// The initialized runtime has an immutable logical memory-domain binding; an
// embedding host may separately opt into verified host isolation.
// Closing a connection never closes this host or replaces its session router.
// Headless hosts do not negotiate client computer/browser reverse-RPC tools.
type UserHost struct {
	personal            *personalhost.Host
	unbindPersonal      func()
	runtime             *runtime.Session
	mu                  sync.Mutex
	dispatchMu          sync.Mutex
	server              *Server
	connections         map[string]*userHostConnection
	pending             map[string]userHostResponse
	next                uint64
	closed              bool
	closeDone           chan struct{}
	closeErr            error
	attentionCtx        context.Context
	attentionCancel     context.CancelFunc
	attentionDone       chan struct{}
	attentionJobs       chan userAttentionJob
	attentionEvents     chan userAttentionEvent
	attentionSubscribed bool
	attentionLastTurn   map[string]string
}
type userHostResponse struct {
	connection *userHostConnection
	originalID json.RawMessage
}
type userHostOutput struct {
	data    []byte
	barrier chan struct{}
	guard   func() error
}
type userHostConnection struct {
	id          string
	ctx         context.Context
	cancel      context.CancelFunc
	output      chan userHostOutput
	mu          sync.Mutex
	queuedBytes int
	failure     error
}

func NewUserHost(rt *runtime.Session, personalOptions personalhost.Options, serverOptions ...ServerOption) (*UserHost, error) {
	if rt == nil || !rt.UserBinding().IsUser() {
		return nil, errors.New("persistent user host requires an explicit user binding")
	}
	if err := rt.UserBinding().Validate(); err != nil {
		return nil, err
	}
	h := &UserHost{runtime: rt, connections: map[string]*userHostConnection{}, pending: map[string]userHostResponse{}}
	h.attentionCtx, h.attentionCancel = context.WithCancel(context.Background())
	h.attentionDone = make(chan struct{})
	h.attentionJobs = make(chan userAttentionJob, 32)
	h.attentionEvents = make(chan userAttentionEvent, 128)
	h.attentionLastTurn = make(map[string]string)
	h.server = New(rt, h, serverOptions...)
	if h.server.startupErr != nil {
		err := h.server.startupErr
		h.attentionCancel()
		h.server.Close()
		return nil, err
	}
	if h.server.personalAccountsRouted && (personalOptions.Calendar != nil || personalOptions.Connectors != nil) {
		h.attentionCancel()
		h.server.Close()
		return nil, errors.New("generation-owned Personal accounts require RuntimeBindings.SetPersonalAccountsFactory")
	}
	if h.server.bootOwner && strings.TrimSpace(rt.WuuHome) != "" {
		ctx, cancel := context.WithCancel(context.Background())
		h.server.storageMaintenanceCancel = cancel
		if !h.server.startBackground(func() { h.server.maintainSessionStorage(ctx) }) {
			cancel()
			h.server.storageMaintenanceCancel = nil
		}
	}

	if h.server.personalEffects == nil {
		h.server.personalEffects = NewPersonalEffectBridge()
	}
	if h.server.personalCoordination == nil {
		h.server.personalCoordination = newPersonalCoordinatorBridge()
	}
	if rt.UserBinding().Domain() == "personal" {
		state, err := rt.RetainState()
		if err != nil {
			h.attentionCancel()
			h.server.Close()
			return nil, err
		}
		selected := h.server.personalCoordinationAvailable(state)
		state.Close()
		if selected {
			if _, err = h.server.invokePersonalCoordination(context.Background(), "initialize", personalCoordinationRequest{}, nil); err != nil {
				h.attentionCancel()
				_ = h.server.personalCoordination.Close()
				h.server.Close()
				return nil, err
			}
		}
		personalOptions.Review = h.server.callPersonalCoordination
		personalOptions.Memory = h.server.callPersonalCoordination
		if rt.Toolkit != nil {
			rt.Toolkit.SetFileActionGate(h.server.personalFileGate)
		}
	}

	if h.server.personalAccountsRouted {
		personalOptions.Accounts = h.server.callPersonalAccounts
	}

	additionalAuthorization := personalOptions.Authorize
	personalOptions.Authorize = func(ctx context.Context, operation, verifiedSession string) error {
		if err := h.authorizePersonalOperation(ctx, operation, verifiedSession); err != nil {
			return err
		}
		if additionalAuthorization != nil {
			return additionalAuthorization(ctx, operation, verifiedSession)
		}
		return nil
	}
	h.personal = personalhost.New(rt.UserBinding(), personalOptions)
	if rt.PluginSessionRouter != nil {
		h.unbindPersonal = rt.PluginSessionRouter.BindPersonalHost(h.personal.Call)
	}
	go h.runAttentionPolicy()
	return h, nil
}

func (h *UserHost) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		done := h.closeDone
		h.mu.Unlock()
		<-done
		return h.closeErr
	}
	h.closeDone = make(chan struct{})
	h.closed = true
	h.attentionCancel()
	connections := make([]*userHostConnection, 0, len(h.connections))
	for _, c := range h.connections {
		connections = append(connections, c)
	}
	h.mu.Unlock()
	for _, c := range connections {
		c.cancel()
	}
	if h.unbindPersonal != nil {
		h.unbindPersonal()
	}
	h.closeErr = h.personal.Close()
	h.server.personalEffects.Close()
	h.closeErr = errors.Join(h.closeErr, h.server.personalCoordination.Close())
	h.server.Close()
	<-h.attentionDone
	close(h.closeDone)
	return h.closeErr
}

// Write accepts one JSONL frame from the single server. A slow or disconnected
// client can lose its own transport, never stall task completion or its peers.
// Internal notifications have no ambient transport identity. WriteAuthorized
// checks every recipient and captures its current policy for queued replay.
func (h *UserHost) writeRecipientNotification(notification Notification) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	_, err = h.WriteAuthorized(append(data, '\n'), nil)
	return err
}

func (h *UserHost) Write(p []byte) (int, error) {
	return h.WriteAuthorized(p, nil)
}

func (h *UserHost) WriteAuthorized(p []byte, guard func() error) (int, error) {
	n := len(p)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(p, &envelope); err != nil {
		return 0, err
	}
	var attention *userAttentionJob
	var method string
	_ = json.Unmarshal(envelope["method"], &method)
	if method == NotificationTurnCompleted || method == NotificationTurnError {
		var err error
		envelope["params"], attention, err = h.terminalLocation(method, envelope["params"])
		if err != nil {
			return 0, err
		}
		p, err = json.Marshal(envelope)
		if err != nil {
			return 0, err
		}
		p = append(p, '\n')
	}
	h.mu.Lock()
	if id := envelope["id"]; len(id) > 0 && string(id) != "null" {
		route, ok := h.pending[string(id)]
		delete(h.pending, string(id))
		h.mu.Unlock()
		if ok {
			envelope["id"] = route.originalID
			data, err := json.Marshal(envelope)
			if err != nil {
				return 0, err
			}
			route.connection.enqueueAuthorized(append(data, '\n'), guard)
		}
		return n, nil
	}
	connections := make([]*userHostConnection, 0, len(h.connections))
	for _, c := range h.connections {
		connections = append(connections, c)
	}
	h.mu.Unlock()
	for _, c := range connections {
		var params any
		if err := json.Unmarshal(envelope["params"], &params); err != nil {
			continue
		}
		_, recipientGuard, allowed := h.server.authorizeOutbound(c.ctx, Notification{Method: method, Params: params})
		if !allowed {
			continue
		}
		c.enqueueAuthorized(p, func() error {
			if guard != nil {
				if err := guard(); err != nil {
					return err
				}
			}
			return recipientGuard()
		})
	}
	if attention != nil {
		h.enqueueAttention(*attention)
	}
	return n, nil
}

// The terminal envelope always carries scope, even when no user-facing
// location is available. Downstream push adapters must not fall back to a
// background thread ID. Only persisted main ownership may produce navigation.
func (h *UserHost) terminalLocation(method string, raw json.RawMessage) (json.RawMessage, *userAttentionJob, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, nil, err
	}
	if params == nil {
		params = make(map[string]json.RawMessage)
	}
	binding := h.runtime.UserBinding()
	params["user_space_id"], _ = json.Marshal(binding.UserSpaceID)
	params["product_domain"], _ = json.Marshal(binding.ProductDomain)
	delete(params, "user_location")
	var terminal struct {
		ThreadID string `json:"thread_id"`
		Turn     Turn   `json:"turn"`
		Category string `json:"category"`
		Awaiting bool   `json:"awaiting_auto_continuation"`
	}
	if err := json.Unmarshal(raw, &terminal); err != nil {
		return nil, nil, err
	}
	if terminal.ThreadID == "" || terminal.Awaiting || terminal.Category == "cancelled" {
		data, err := json.Marshal(params)
		return data, nil, err
	}
	state, stateErr := h.runtime.RetainState()
	if stateErr != nil {
		return nil, nil, stateErr
	}
	defer state.Close()
	metadata, found, err := state.Sessions.Find(terminal.ThreadID)
	if err != nil || !found || metadata.UserSpaceID != binding.UserSpaceID || metadata.ProductDomain != binding.ProductDomain || metadata.ParentID != "" || !strings.HasPrefix(metadata.Owner, "plugin:") || metadata.Visibility != "plugin" || metadata.AgentID == "" {
		data, err := json.Marshal(params)
		return data, nil, err
	}
	location := UserLocation{Version: 1, UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, AgentID: metadata.AgentID}
	for _, item := range terminal.Turn.Items {
		if item.Type == ThreadItemAgentMessage && item.Terminal && item.Seq > location.MessageSeq {
			location.MessageSeq = item.Seq
		}
	}
	// A completed tool-only or empty turn is not a user-visible answer. Errors
	// may still navigate to the owning agent without inventing a message address.
	if method == NotificationTurnCompleted && (terminal.Turn.Status != TurnStatusCompleted || location.MessageSeq == 0) {
		data, err := json.Marshal(params)
		return data, nil, err
	}
	params["user_location"], _ = json.Marshal(location)
	data, err := json.Marshal(params)
	if terminal.Turn.ID == "" {
		return data, nil, err
	}
	// Only the host's settled turn projection can name its originating send.
	// Lifecycle receipts settle after terminal publication, so resolving the
	// latest lifecycle here would race and could select another request.
	thread := h.server.threadForState(state, terminal.ThreadID)
	if thread == nil {
		return data, nil, err
	}
	var source ThreadItem
	thread.mu.Lock()
	for _, turn := range thread.Turns {
		if turn.ID != terminal.Turn.ID || turn.Status != terminal.Turn.Status {
			continue
		}
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage {
				source = item
				break
			}
		}
		break
	}
	thread.mu.Unlock()
	owner, requestID, valid := pluginSessionRequestFromClientID(source.SourceID)
	if !valid || metadata.Owner != "plugin:"+owner || source.Seq < 1 {
		return data, nil, err
	}
	outcome := "completed"
	if method == NotificationTurnError {
		outcome = "failed"
	}
	job := &userAttentionJob{owner: owner, sourceSeq: source.Seq, input: pluginhost.SessionAttentionInput{
		UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain,
		SessionID: terminal.ThreadID, AgentID: metadata.AgentID, MessageSeq: location.MessageSeq,
		TurnID: terminal.Turn.ID, RequestID: requestID, Outcome: outcome}, location: location}
	return data, job, err
}

func (c *userHostConnection) enqueue(data []byte) { c.enqueueAuthorized(data, nil) }

func (c *userHostConnection) enqueueAuthorized(data []byte, guard func() error) {
	if guard != nil {
		authorization := guard
		guard = func() error {
			if err := c.ctx.Err(); err != nil {
				return err
			}
			return authorization()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return
	}
	if len(data) > maxUserHostClientQueuedBytes-c.queuedBytes {
		c.failure = ErrUserHostClientOverflow
		c.cancel()
		return
	}
	copy := append([]byte(nil), data...)
	select {
	case c.output <- userHostOutput{data: copy, guard: guard}:
		c.queuedBytes += len(copy)
	default:
		c.failure = ErrUserHostClientOverflow
		c.cancel()
	}
}

// Serve attaches an already authenticated transport. No credentials or remote
// permissions are created here. The input is closed on cancellation when it
// implements io.Closer, so a stalled reader cannot retain a revoked device.
// Device registrations remain bound to this transport's registrar.
func (h *UserHost) Serve(ctx context.Context, in io.Reader, out io.Writer, registrar PushRegistrar) error {
	return h.serve(ctx, in, out, registrar, false)
}

// ServeTrustedLocal enables only registered-session transport on an embedding's
// private local Desktop pipe. Authenticated device/network callers use Serve.
func (h *UserHost) ServeTrustedLocal(ctx context.Context, in io.Reader, out io.Writer) error {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.IsOwner() || caller.Identity() != principal.LocalOwner().Identity() {
		return errors.New("trusted local user host requires its local installation principal")
	}
	return h.serve(ctx, in, out, nil, true)
}

func (h *UserHost) serve(ctx context.Context, in io.Reader, out io.Writer, registrar PushRegistrar, trustedLocal bool) error {
	if ctx == nil || in == nil || out == nil {
		return errors.New("user host connection requires context, input and output")
	}
	caller, authenticated := principal.FromContext(ctx)
	if !authenticated || !caller.Valid() {
		return errors.New("authenticated user host principal is required")
	}
	if err := caller.Check(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = clientauthority.BindConnection(ctx)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("user host is closed")
	}
	if len(h.connections) >= maxUserHostConnections {
		h.mu.Unlock()
		return errors.New("user host connection limit reached")
	}
	h.next++
	id := fmt.Sprintf("user-client-%d", h.next)
	c := &userHostConnection{id: id, ctx: ctx, cancel: cancel, output: make(chan userHostOutput, 128)}
	h.connections[id] = c
	h.mu.Unlock()
	// Reverse transport belongs to this connection, never the shared user host.
	// The facade has no request dispatcher or session router.
	var transport *Server
	if trustedLocal && h.server.desktopSessionTransport != nil {
		transport = &Server{out: userHostConnectionWriter{c}, access: connectionAccess{principal: caller, policy: h.server.access.policy}, clientCalls: make(map[string]chan clientResponse)}
		transport.trustedTransport.enabled = true
		h.server.desktopSessionTransport.bind(transport)
		defer func() {
			transport.closed.Store(true)
			transport.failPendingClientCalls()
			transport.desktopSessionTransport.unbind(transport)
		}()
	}

	stopInput := context.AfterFunc(ctx, func() {
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	defer stopInput()
	writeCtx, stopWriter := context.WithCancel(context.Background())
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-writeCtx.Done():
				return
			case frame := <-c.output:
				if frame.barrier != nil {
					close(frame.barrier)
					continue
				}
				var err error
				if frame.guard != nil {
					err = frame.guard()
				}
				if err == nil {
					if guarded, ok := out.(GuardedWriter); ok && frame.guard != nil {
						_, err = guarded.WriteAuthorized(frame.data, frame.guard)
					} else {
						_, err = out.Write(frame.data)
					}
				} else {
					// Revocation discards protected queued data without leaking it.
					c.mu.Lock()
					c.queuedBytes -= len(frame.data)
					c.mu.Unlock()
					continue
				}
				c.mu.Lock()
				c.queuedBytes -= len(frame.data)
				if err != nil {
					c.failure = err
				}
				c.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		// Revoke live client authority before any best-effort output drain.
		cancel()
		h.mu.Lock()
		delete(h.connections, id)
		for key, route := range h.pending {
			if route.connection == c {
				delete(h.pending, key)
			}
		}
		h.mu.Unlock()
		barrier := make(chan struct{})
		select {
		case c.output <- userHostOutput{barrier: barrier}:
			select {
			case <-barrier:
			case <-writerDone:
			case <-time.After(250 * time.Millisecond):
			}
		default:
		}
		stopWriter()
		if closer, ok := out.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), appServerMaxStdioRequestBytes)
	var implicitRequestID uint64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			break
		}
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			return fmt.Errorf("invalid user host request: %w", err)
		}
		if transport != nil && len(req.ID) > 0 && req.Method == "" {
			transport.deliverClientResponse([]byte(raw))
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" || req.Method == "" {
			return errors.New("user host requires an identified client request; reverse RPC is unavailable")
		}
		switch req.Method {
		case MethodInitialize, MethodPluginClientRequest, MethodPluginClientCancel,
			MethodStateInspectionsList, MethodStateInspectionsReconcile,
			MethodDevicePushRegister, MethodDevicePushUnregister, MethodShutdown,
			MethodUserQuestionList, MethodUserQuestionRespond, MethodUserQuestionCancel, MethodUserQuestionHold:
		default:
			// A user host exposes product-owned operations, never a generic
			// thread editor, storage mutator, workspace shell, or Coding export.
			direct := &Server{out: userHostConnectionWriter{c}, access: connectionAccess{principal: caller, policy: h.server.access.policy}}
			_ = direct.writeResponse(req.ID, nil, errors.New("user host client method is unavailable; use the active plugin client contract"))
			continue
		}
		var requestedBinding struct {
			UserSpaceID   string `json:"user_space_id,omitempty"`
			ProductDomain string `json:"product_domain,omitempty"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &requestedBinding); err != nil {
				return err
			}
		}
		requested := userspace.Binding{UserSpaceID: requestedBinding.UserSpaceID, ProductDomain: requestedBinding.ProductDomain}
		if requested.UserSpaceID != "" || requested.ProductDomain != "" {
			if requested.Validate() != nil || !h.runtime.UserBinding().Matches(requested) {
				direct := &Server{out: userHostConnectionWriter{c}, access: connectionAccess{principal: caller, policy: h.server.access.policy}}
				_ = direct.writeResponse(req.ID, nil, errors.New("request belongs to another user space or product domain"))
				continue
			}
		}
		// Scope is a transport routing envelope. Canonical host methods retain
		// their own DTOs after the immutable user-host binding has been checked.
		if req.Method != MethodPluginClientRequest && (requested.UserSpaceID != "" || requested.ProductDomain != "") {
			var params map[string]json.RawMessage
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			delete(params, "user_space_id")
			delete(params, "product_domain")
			req.Params, _ = json.Marshal(params)
		}
		if req.Method == MethodShutdown {
			if err := h.server.authorizeRequest(ctx, req); err != nil {
				direct := &Server{out: userHostConnectionWriter{c}, access: connectionAccess{principal: caller, policy: h.server.access.policy}}
				_ = direct.writeResponse(req.ID, nil, err)
				continue
			}
			data, _ := json.Marshal(Response{ID: req.ID, Result: OKResult{OK: true}})
			c.enqueue(append(data, '\n'))
			return nil
		}
		if req.Method == MethodDevicePushRegister || req.Method == MethodDevicePushUnregister {
			direct := &Server{rt: h.runtime, out: userHostConnectionWriter{c}, pushRegistrar: registrar, access: connectionAccess{principal: caller, policy: h.server.access.policy}}
			if err := direct.authorizeRequest(ctx, req); err != nil {
				_ = direct.writeResponse(req.ID, nil, err)
				continue
			}
			if err := direct.trackAuthorizedRequest(ctx, req); err != nil {
				return err
			}
			if req.Method == MethodDevicePushRegister {
				_ = direct.handleDevicePushRegister(req)
			} else {
				_ = direct.handleDevicePushUnregister(req)
			}
			continue
		}
		if req.Method == MethodInitialize {
			var params InitializeParams
			if err := decodeParams(req.Params, &params); err != nil {
				return err
			}
			if transport != nil {
				var methods []string
				for _, method := range params.Capabilities.ReverseRPC.Methods {
					if method == desktopSessionDispatch || method == desktopSessionTargets || method == desktopSessionRelease {
						methods = append(methods, method)
					}
				}
				transport.setClientMethods(methods)
			}
			params.Capabilities.ReverseRPC.Methods = nil
			// All user-host consumers use bounded content references. No connection
			// may change another connection's delivery or browser authority.
			params.Capabilities.DeferredNotificationContent = true
			req.Params, _ = json.Marshal(params)
		}
		if req.Method == MethodPluginClientRequest || req.Method == MethodPluginClientCancel {
			var params map[string]json.RawMessage
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			var requestID string
			if value := params["request_id"]; len(value) > 0 {
				if err := json.Unmarshal(value, &requestID); err != nil {
					return err
				}
			}
			if params == nil {
				params = make(map[string]json.RawMessage)
			}
			if requestID != "" && (strings.TrimSpace(requestID) == "" || len(strings.TrimSpace(requestID)) > 128) {
				return errors.New("plugin client request_id must contain 1-128 bytes")
			}
			identity := "explicit\x00" + strings.TrimSpace(requestID)
			if requestID == "" && req.Method == MethodPluginClientRequest {
				// Legacy callers omit the cancellation ID. Still admit their
				// callback asynchronously so EOF can revoke proof and peers run.
				implicitRequestID++
				identity = fmt.Sprintf("implicit\x00%d", implicitRequestID)
			}
			if requestID != "" || req.Method == MethodPluginClientRequest {
				digest := sha256.Sum256([]byte(id + "\x00" + identity))
				params["request_id"], _ = json.Marshal("user-rpc-" + hex.EncodeToString(digest[:]))
				req.Params, _ = json.Marshal(params)
			}
		}
		h.mu.Lock()
		h.next++
		mapped, _ := json.Marshal(fmt.Sprintf("user-request-%d", h.next))
		h.pending[string(mapped)] = userHostResponse{connection: c, originalID: append(json.RawMessage(nil), req.ID...)}
		h.mu.Unlock()
		req.ID = mapped
		frame, err := json.Marshal(req)
		if err != nil {
			return err
		}
		h.dispatchMu.Lock()
		if ctx.Err() == nil {
			err = h.server.handleLine(ctx, frame)
		} else {
			err = ctx.Err()
		}
		h.dispatchMu.Unlock()
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	failure := c.failure
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scanner.Err()
}

type userHostConnectionWriter struct{ connection *userHostConnection }

func (w userHostConnectionWriter) Write(p []byte) (int, error) {
	w.connection.enqueue(p)
	return len(p), nil
}

// Origin=user is assigned only after live client proof validation. PluginTurn
// retains its delivery receipt identity without changing the input's author.
// Do not classify proven client text using legacy synthetic-message envelopes.
func (s *Server) isUserHostClientInput(entry queuedTurn) bool {
	return s.rt != nil && s.rt.UserBinding().IsUser() && entry.snapshot.PluginTurn != nil && entry.msg.Origin == "user" && entry.msg.Role == "user" && !entry.msg.Hidden && !entry.msg.ReadOnly
}

func (w userHostConnectionWriter) WriteAuthorized(p []byte, guard func() error) (int, error) {
	w.connection.enqueueAuthorized(p, guard)
	return len(p), nil
}

// Personal domain services never borrow the shared execution server's ambient
// owner identity. Only the transport/admitted execution can carry its principal.
func (h *UserHost) authorizePersonalOperation(ctx context.Context, operation, verifiedSession string) error {
	return h.server.authorizePersonalOperation(ctx, operation, verifiedSession)
}

func (s *Server) authorizePersonalOperation(ctx context.Context, operation, verifiedSession string) error {
	_, execution, known := personalhost.OperationAccess(operation)
	if !known {
		return errors.New("Personal operation is unavailable")
	}
	caller, authenticated := principal.FromContext(ctx)
	if !authenticated || !caller.Valid() {
		return errors.New("authenticated Personal principal is required")
	}
	resources := []access.Resource{{Kind: "user-space", ID: s.rt.UserBinding().UserSpaceID}}
	if execution {
		if verifiedSession == "" {
			return errors.New("host-resolved execution resource is required")
		}
		resources = append(resources, access.Resource{Kind: "session", ID: verifiedSession})
	}
	if err := s.AuthorizeResource(ctx, "personal/"+operation, resources...); err != nil {
		return err
	}
	s.appendContextResponseScope(ctx, "personal/"+operation, resources...)
	return nil
}
