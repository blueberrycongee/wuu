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

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
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
	actions             *personalActions
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

func NewUserHost(rt *runtime.Session, options ...personalhost.Options) (*UserHost, error) {
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
	h.server = New(rt, h)
	if h.server.startupErr != nil {
		err := h.server.startupErr
		h.attentionCancel()
		h.server.Close()
		return nil, err
	}
	if h.server.bootOwner && strings.TrimSpace(rt.WuuHome) != "" {
		ctx, cancel := context.WithCancel(context.Background())
		h.server.storageMaintenanceCancel = cancel
		if !h.server.startBackground(func() { h.server.maintainSessionStorage(ctx) }) {
			cancel()
			h.server.storageMaintenanceCancel = nil
		}
	}
	personalOptions := personalhost.Options{}
	if len(options) > 0 {
		personalOptions = options[0]
	}

	if rt.UserBinding().Domain() == "personal" {
		var err error
		h.actions, err = newPersonalActions(h.server)
		if err != nil {
			h.attentionCancel()
			h.server.Close()
			return nil, err
		}
		h.server.personalActions = h.actions
		if broker, ok := personalOptions.Connectors.(*personalconnectors.Broker); ok {
			broker.SetAuthorization(h.actions.connectorAuthority)
		}
		personalOptions.Review = h.actions.Call
		if rt.Toolkit != nil {
			rt.Toolkit.SetFileActionGate(h.actions.gate)
		}
	}
	h.personal = personalhost.New(rt.UserBinding(), personalOptions)
	if rt.PluginSessionRouter != nil {
		h.unbindPersonal = rt.PluginSessionRouter.BindPersonalHost(h.personal.Call)
	}
	if h.actions != nil {
		h.actions.start()
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
	if h.actions != nil {
		h.closeErr = errors.Join(h.closeErr, h.actions.close())
	}
	h.server.Close()
	<-h.attentionDone
	close(h.closeDone)
	return h.closeErr
}

// Write accepts one JSONL frame from the single server. A slow or disconnected
// client can lose its own transport, never stall task completion or its peers.
func (h *UserHost) Write(p []byte) (int, error) {
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
			route.connection.enqueue(append(data, '\n'))
		}
		return n, nil
	}
	connections := make([]*userHostConnection, 0, len(h.connections))
	for _, c := range h.connections {
		connections = append(connections, c)
	}
	h.mu.Unlock()
	for _, c := range connections {
		c.enqueue(p)
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
	metadata, found, err := session.Find(h.runtime.SessionDir, terminal.ThreadID)
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
	outcome := "completed"
	if method == NotificationTurnError {
		outcome = "failed"
	}
	job := &userAttentionJob{owner: strings.TrimPrefix(metadata.Owner, "plugin:"), input: pluginhost.SessionAttentionInput{
		UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain,
		SessionID: terminal.ThreadID, AgentID: metadata.AgentID, MessageSeq: location.MessageSeq,
		TurnID: terminal.Turn.ID, Outcome: outcome}, location: location}
	return data, job, err
}

func (c *userHostConnection) enqueue(data []byte) {
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
	case c.output <- userHostOutput{data: copy}:
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
	if ctx == nil || in == nil || out == nil {
		return errors.New("user host connection requires context, input and output")
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
				_, err := out.Write(frame.data)
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
		if len(req.ID) == 0 || string(req.ID) == "null" || req.Method == "" {
			return errors.New("user host requires an identified client request; reverse RPC is unavailable")
		}
		switch req.Method {
		case MethodInitialize, MethodPluginClientRequest, MethodPluginClientCancel,
			MethodDevicePushRegister, MethodDevicePushUnregister, MethodShutdown,
			MethodUserQuestionList, MethodUserQuestionRespond, MethodUserQuestionCancel, MethodUserQuestionHold:
		default:
			// A user host exposes product-owned operations, never a generic
			// thread editor, storage mutator, workspace shell, or Coding export.
			direct := &Server{out: userHostConnectionWriter{c}}
			_ = direct.writeResponse(req.ID, nil, errors.New("user host client method is unavailable; use the active plugin client contract"))
			continue
		}
		if req.Method == MethodShutdown {
			data, _ := json.Marshal(Response{ID: req.ID, Result: OKResult{OK: true}})
			c.enqueue(append(data, '\n'))
			return nil
		}
		if req.Method == MethodDevicePushRegister || req.Method == MethodDevicePushUnregister {
			direct := &Server{out: userHostConnectionWriter{c}, pushRegistrar: registrar}
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
