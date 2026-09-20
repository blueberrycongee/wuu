package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

const (
	defaultResponsesWebSocketCacheTTL = 30 * time.Minute
	responsesWebSocketPrewarmTTL      = 2 * time.Minute
	// Fallback pins prevent every request in a hot session from immediately
	// retrying a broken websocket path. Transient failures re-probe quickly,
	// connection pressure backs off longer, and stable compatibility/auth
	// failures retain the former long pin.
	responsesWebSocketTransientFallbackTTL = 30 * time.Second
	responsesWebSocketPressureFallbackTTL  = 2 * time.Minute
	responsesWebSocketLongFallbackTTL      = 10 * time.Minute

	responsesWebSocketFallbackPinCreated  = "created"
	responsesWebSocketFallbackPinExtended = "extended"
	responsesWebSocketFallbackPinReused   = "reused"

	responsesWebSocketConnectionLimitCode = "websocket_connection_limit_reached"
)

// ResponsesWebSocketCache stores per-session Responses WebSocket state.
// Each session keeps a reusable connection plus the last full request/response
// pair needed to build previous_response_id deltas.
type ResponsesWebSocketCache struct {
	mu       sync.Mutex
	sessions map[string]*responsesWebSocketSession
	idleTTL  time.Duration
}

func NewResponsesWebSocketCache() *ResponsesWebSocketCache {
	return NewResponsesWebSocketCacheWithTTL(defaultResponsesWebSocketCacheTTL)
}

func NewResponsesWebSocketCacheWithTTL(ttl time.Duration) *ResponsesWebSocketCache {
	return &ResponsesWebSocketCache{
		sessions: make(map[string]*responsesWebSocketSession),
		idleTTL:  ttl,
	}
}

type responsesWebSocketSession struct {
	id            string
	cache         *ResponsesWebSocketCache
	mu            sync.Mutex
	conn          *websocket.Conn
	wsURL         string
	generation    uint64
	busy          bool
	active        chan responsesWebSocketReadEvent
	activeDone    chan struct{}
	activeCtxDone <-chan struct{}
	activeErr     error
	continuation  *responsesWebSocketContinuation
	fallback      responsesWebSocketFallbackState
	idleTimer     *time.Timer
	prewarmDone   chan struct{}
	warmOnly      bool
}

type responsesWebSocketContinuation struct {
	generation        uint64
	lastRequestRest   []byte
	lastRequestInput  []responsesInputItem
	lastResponseID    string
	lastResponseItems []responsesInputItem
}

type responsesWebSocketReadEvent struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

type responsesWebSocketFallbackState struct {
	active bool
	reason string
	until  time.Time
	ttl    time.Duration
}

// fallbackActiveLocked reports whether the SSE pin is still in force and
// clears it once expired. Callers must hold session.mu.
func (s *responsesWebSocketSession) fallbackActiveLocked(now time.Time) bool {
	if !s.fallback.active {
		return false
	}
	if !s.fallback.until.IsZero() && !now.Before(s.fallback.until) {
		s.fallback = responsesWebSocketFallbackState{}
		return false
	}
	return true
}

type responsesWebSocketRequestMeta struct {
	connectionReused bool
}

type responsesWebSocketFallbackMeta struct {
	pinStatus  string
	retryAfter time.Duration
	ttl        time.Duration
}

type responsesWebSocketFallbackError struct {
	reason   string
	err      error
	fallback responsesWebSocketFallbackMeta
}

func (e *responsesWebSocketFallbackError) Error() string {
	if e == nil {
		return ""
	}
	if e.err != nil {
		return "responses websocket fallback (" + e.reason + "): " + e.err.Error()
	}
	return "responses websocket fallback (" + e.reason + ")"
}

func (e *responsesWebSocketFallbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *responsesWebSocketFallbackError) InferenceRecoveryAction() providers.RecoveryActionKind {
	return providers.RecoverySwitchTransport
}

func (c *Client) responsesStreamChatWebSocket(ctx context.Context, payload responsesRequest, transport providers.StreamTransportMode) (<-chan providers.StreamEvent, error) {
	return c.responsesStreamChatWebSocketAttempt(ctx, payload, transport)
}

// PrewarmResponsesWebSocket establishes and retains a session-scoped
// connection without submitting a response.create request. Authentication,
// DNS, TLS, and the WebSocket upgrade can therefore finish while the user is
// composing the first message. The first real request remains the only model
// submission and owns all journal/coordinator accounting.
func (c *Client) PrewarmResponsesWebSocket(ctx context.Context, sessionID string) error {
	if c == nil || c.responsesWSCache == nil || !responsesTransportUsesWebSocket(c.responsesTransport) {
		return nil
	}
	if ctx == nil {
		return errors.New("responses websocket prewarm context is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("responses websocket prewarm session id is empty")
	}
	wsURL, err := resolveCodexWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	session := c.responsesWSCache.session(sessionID)
	session.mu.Lock()
	if session.fallbackActiveLocked(time.Now()) || session.busy || session.prewarmDone != nil {
		session.mu.Unlock()
		return nil
	}
	c.responsesWebSocketCancelIdleTimerLocked(session)
	if session.conn != nil && session.wsURL == wsURL {
		c.responsesWebSocketScheduleIdleExpiryLocked(session)
		session.mu.Unlock()
		return nil
	}
	if session.conn != nil {
		c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusNormalClosure, "prewarm_reconnect")
	}
	prewarmDone := make(chan struct{})
	session.prewarmDone = prewarmDone
	session.mu.Unlock()

	headers := map[string]string{
		"session-id":          sessionID,
		"x-client-request-id": sessionID,
	}
	conn, dialErr := c.responsesWebSocketDial(ctx, wsURL, headers)
	session.mu.Lock()
	if session.prewarmDone == prewarmDone {
		session.prewarmDone = nil
	}
	close(prewarmDone)
	if dialErr != nil {
		// Prewarm is the session's first WebSocket setup attempt. Preserve its
		// result so the first real request does not repeat the same failed dial
		// before falling back to SSE. Caller cancellation is different: it aborts
		// prewarm without making a claim about transport availability.
		if ctx.Err() == nil {
			c.responsesWebSocketActivateFallbackLocked(session, "websocket_setup_failed", dialErr)
		}
		session.mu.Unlock()
		return dialErr
	}
	// A concurrent real request uses a transient connection while prewarm is
	// dialing. If it changed canonical session state, do not overwrite it.
	if session.conn != nil || session.fallbackActiveLocked(time.Now()) {
		session.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "prewarm_superseded")
		return nil
	}
	session.conn = conn
	session.wsURL = wsURL
	session.generation++
	session.warmOnly = true
	generation := session.generation
	c.responsesWebSocketScheduleIdleExpiryLocked(session)
	session.mu.Unlock()
	go c.responsesWebSocketReadPump(session, conn, generation)
	return nil
}

func (c *Client) responsesStreamChatWebSocketAttempt(ctx context.Context, payload responsesRequest, transport providers.StreamTransportMode) (<-chan providers.StreamEvent, error) {
	if c.responsesWSCache == nil {
		return nil, errors.New("responses websocket cache is nil")
	}
	sessionID := responsesWebSocketSessionID(payload)
	if sessionID == "" {
		return nil, errors.New("responses websocket session id is empty")
	}
	wsURL, err := resolveCodexWebSocketURL(c.baseURL)
	if err != nil {
		return nil, err
	}
	session := c.responsesWSCache.session(sessionID)
	session.mu.Lock()
	prewarmDone := session.prewarmDone
	session.mu.Unlock()
	if prewarmDone != nil {
		select {
		case <-prewarmDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Admission may wait on a shared cooldown. Do it before taking the
	// session mutex so one rate-limited account cannot freeze connection
	// cleanup, fallback state, or another session's cancellation.
	lease, err := c.coordinator.AcquireForAttempt(ctx, c.providerScope, payload.Attempt)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	now := time.Now()
	if session.fallbackActiveLocked(now) {
		reason := session.fallback.reason
		fallback := session.responsesWebSocketFallbackMetaLocked(now, responsesWebSocketFallbackPinReused)
		session.mu.Unlock()
		lease.Release()
		return nil, newResponsesWebSocketFallbackError(reason, nil, fallback)
	}
	c.responsesWebSocketCancelIdleTimerLocked(session)
	if session.busy {
		session.mu.Unlock()
		transient := &responsesWebSocketSession{id: sessionID}
		transient.mu.Lock()
		return c.responsesStreamChatWebSocketWithSessionLocked(ctx, transient, session, wsURL, payload, transport, false, lease)
	}

	return c.responsesStreamChatWebSocketWithSessionLocked(ctx, session, session, wsURL, payload, transport, true, lease)
}

func (c *Client) responsesStreamChatWebSocketWithSessionLocked(ctx context.Context, session, fallbackSession *responsesWebSocketSession, wsURL string, payload responsesRequest, transport providers.StreamTransportMode, cacheConnection bool, lease *providers.ProviderLease) (<-chan providers.StreamEvent, error) {
	// Reserve the session before dropping the lock for network IO. busy=true
	// diverts concurrent same-session requests to transient connections and
	// keeps idle expiry away; holding session.mu across a dial or a write
	// would instead block every one of those waiters (each already holding a
	// coordinator lease) for as long as the network stalls.
	session.busy = true
	reused := session.conn != nil && session.wsURL == wsURL
	var conn *websocket.Conn
	var generation uint64
	if reused {
		conn = session.conn
		generation = session.generation
		session.warmOnly = false
	} else if session.conn != nil {
		c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusNormalClosure, "reconnect")
	}
	session.mu.Unlock()

	if !reused {
		newConn, dialErr := c.responsesWebSocketDial(ctx, wsURL, payload.ExtraHeaders)
		session.mu.Lock()
		if dialErr != nil {
			session.busy = false
			if ctx.Err() != nil {
				cache := session.cache
				sessionID := session.id
				session.mu.Unlock()
				if cacheConnection && cache != nil {
					cache.expireIdleSession(sessionID, session)
				}
				lease.FailError(ctx.Err())
				return nil, ctx.Err()
			}
			var fallback responsesWebSocketFallbackMeta
			if cacheConnection {
				fallback = c.responsesWebSocketActivateFallbackLocked(session, "websocket_setup_failed", dialErr)
			}
			session.mu.Unlock()
			if !cacheConnection {
				fallback = c.responsesWebSocketMarkFallback(fallbackSession, "websocket_setup_failed", dialErr)
			}
			fallbackErr := newResponsesWebSocketFallbackError("websocket_setup_failed", dialErr, fallback)
			// This transport attempt is being replaced by SSE. Do not open the
			// account-level circuit for a WebSocket-only compatibility failure.
			lease.Release()
			return nil, fallbackErr
		}
		session.conn = newConn
		session.wsURL = wsURL
		session.generation++
		conn = newConn
		generation = session.generation
		go c.responsesWebSocketReadPump(session, newConn, generation)
	} else {
		session.mu.Lock()
	}

	fullPayload := payload
	useCachedContext := cacheConnection && responsesTransportUsesCachedContext(transport)
	requestPayload := fullPayload
	if useCachedContext {
		requestPayload = responsesCachedWebSocketRequest(session, generation, fullPayload)
	}
	providers.DebugLogf("Responses websocket request: session=%q previous_response_id=%v input_items=%d full_input_items=%d",
		session.id, strings.TrimSpace(requestPayload.PreviousResponseID) != "", len(requestPayload.Input), len(fullPayload.Input))
	body, err := marshalResponsesWebSocketCreate(requestPayload)
	if err != nil {
		session.busy = false
		session.mu.Unlock()
		lease.FailError(err)
		return nil, err
	}
	readCh := make(chan responsesWebSocketReadEvent, 64)
	session.active = readCh
	session.activeDone = make(chan struct{})
	session.activeCtxDone = ctx.Done()
	session.activeErr = nil
	session.mu.Unlock()

	mode := "stream"
	if strings.TrimSpace(requestPayload.SubmissionReason) != "" {
		mode = "fallback"
	}
	if _, err := lease.RecordSubmission(requestPayload.Attempt, providers.InferenceSubmissionMeta{
		Provider:     "openai",
		Protocol:     "responses",
		Transport:    "websocket",
		Mode:         mode,
		Reason:       requestPayload.SubmissionReason,
		RequestBytes: len(body),
	}); err != nil {
		session.mu.Lock()
		c.responsesWebSocketReleaseLocked(session, readCh)
		session.mu.Unlock()
		lease.Release()
		return nil, fmt.Errorf("journal websocket submission: %w", err)
	}
	// The write happens unlocked: the pump's generation guard and the active
	// channel installed above already route any early frames correctly, and a
	// zero-window peer must not wedge the whole session.
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		session.mu.Lock()
		c.responsesWebSocketReleaseLocked(session, readCh)
		if ctx.Err() != nil {
			c.responsesWebSocketAbortConnectionLocked(session)
			session.mu.Unlock()
			lease.FailError(ctx.Err())
			return nil, ctx.Err()
		}
		var fallback responsesWebSocketFallbackMeta
		if cacheConnection {
			fallback = c.responsesWebSocketActivateFallbackLocked(session, "websocket_write_failed", err)
		} else {
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "websocket_write_failed")
		}
		session.mu.Unlock()
		if !cacheConnection {
			fallback = c.responsesWebSocketMarkFallback(fallbackSession, "websocket_write_failed", err)
		}
		fallbackErr := newResponsesWebSocketFallbackError("websocket_write_failed", err, fallback)
		lease.FallbackError(fallbackErr)
		return nil, fallbackErr
	}
	state := responsesWebSocketProviderState(fullPayload, requestPayload, transport, responsesWebSocketRequestMeta{
		connectionReused: reused,
	})

	ch := make(chan providers.StreamEvent, 64)
	go c.readResponsesWebSocket(ctx, session, fallbackSession, generation, readCh, fullPayload, requestPayload, transport, useCachedContext, cacheConnection, lease, state, ch)
	return ch, nil
}

func (c *Client) responsesWebSocketDial(ctx context.Context, wsURL string, extraHeaders map[string]string) (*websocket.Conn, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.apiKey)
	for k, v := range c.headers {
		headers.Set(k, v)
	}
	for k, v := range extraHeaders {
		headers.Set(k, v)
	}
	headers.Set("OpenAI-Beta", CodexWebSocketBetaTag)
	return (CodexWebSocketDialer{
		ConnectTimeout: c.streamConfig.ConnectTimeout,
		HTTPClient:     newStreamingHTTPClient(c.httpClient, c.streamConfig),
	}).dialCodexWebSocket(ctx, wsURL, headers)
}

func (c *Client) responsesWebSocketReadPump(session *responsesWebSocketSession, conn *websocket.Conn, generation uint64) {
	for {
		typ, reader, err := conn.Reader(context.Background())
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(reader, providers.MaxStreamEventBytes+1))
			if len(data) > providers.MaxStreamEventBytes {
				err = &providers.StreamEventTooLargeError{Transport: "WebSocket", LimitBytes: providers.MaxStreamEventBytes}
			}
		}

		session.mu.Lock()
		if session.conn != conn || session.generation != generation {
			session.mu.Unlock()
			return
		}
		target := session.active
		targetDone := session.activeDone
		targetCtxDone := session.activeCtxDone
		if err != nil {
			if target != nil {
				session.activeErr = err
			}
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "read_error")
			session.mu.Unlock()
			if target != nil {
				close(target)
			}
			return
		}
		if typ != websocket.MessageText {
			session.mu.Unlock()
			continue
		}
		if target == nil {
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusPolicyViolation, "unexpected_idle_message")
			session.mu.Unlock()
			return
		}
		frame := responsesWebSocketReadEvent{typ: typ, data: append([]byte(nil), data...)}
		session.mu.Unlock()

		select {
		case target <- frame:
		case <-targetDone:
			// The request completed or was released while the reader was
			// waiting. The next read observes either the next active request or
			// the connection shutdown.
			continue
		case <-targetCtxDone:
			session.mu.Lock()
			active := session.active == target && session.conn == conn && session.generation == generation
			if active {
				session.activeErr = context.Canceled
				c.responsesWebSocketAbortConnectionLocked(session)
			}
			session.mu.Unlock()
			if !active {
				continue
			}
			close(target)
			return
		}
	}
}

func (c *Client) readResponsesWebSocket(ctx context.Context, session, fallbackSession *responsesWebSocketSession, generation uint64, readCh chan responsesWebSocketReadEvent, fullPayload, requestPayload responsesRequest, transport providers.StreamTransportMode, useCachedContext bool, cacheConnection bool, lease *providers.ProviderLease, state *providers.ProviderStateSummary, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer lease.Release()

	emit := providers.NewStreamEmitter(ctx, ch)

	if !emit.Send(providers.StreamEvent{
		Type:          providers.EventProviderState,
		ProviderState: state,
	}) {
		return
	}

	pending := newResponsesPendingTools()
	pendingReasoning := newResponsesPendingReasoning()
	var sawToolCall bool
	var sawProviderEvent bool
	var currentTextPhase providers.MessagePhase
	var currentTextItemID string
	var responseID string
	var responseItems []responsesInputItem

	// Idle watchdog, matching the SSE readers: the pump reads the connection
	// with no deadline, so a half-open connection (NAT expiry, silently killed
	// cached connection) would otherwise hang this request forever. The timer
	// also bounds the wait for the first frame after a write that landed in a
	// dead connection's kernel buffer. Firing is routed through the regular
	// frame.err path, which closes the connection (unblocking the pump) and
	// falls back to SSE before the first provider event.
	idleTimeout := c.streamConfig.IdleTimeout
	var idleC <-chan time.Time
	var idleTimer *time.Timer
	if idleTimeout > 0 {
		idleTimer = time.NewTimer(idleTimeout)
		defer idleTimer.Stop()
		idleC = idleTimer.C
	}
	var finalAnswerTailC <-chan time.Time
	var finalAnswerTailTimer *time.Timer
	defer func() {
		if finalAnswerTailTimer != nil {
			finalAnswerTailTimer.Stop()
		}
	}()
	armFinalAnswerTail := func() {
		if finalAnswerTailTimer != nil {
			return
		}
		finalAnswerTailTimer = time.NewTimer(responsesFinalAnswerTailTimeout(idleTimeout))
		finalAnswerTailC = finalAnswerTailTimer.C
	}
	disarmFinalAnswerTail := func() {
		if finalAnswerTailTimer != nil {
			finalAnswerTailTimer.Stop()
			finalAnswerTailC = nil
		}
	}

	for {
		var frame responsesWebSocketReadEvent
		var ok bool
		select {
		case <-ctx.Done():
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			c.responsesWebSocketAbortConnectionLocked(session)
			session.mu.Unlock()
			lease.FailError(ctx.Err())
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()})
			return
		case frame, ok = <-readCh:
			if idleTimer != nil {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idleTimeout)
			}
		case <-idleC:
			frame.err = fmt.Errorf("websocket stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
			ok = true
		case <-finalAnswerTailC:
			finalAnswerTailC = nil
			if sawToolCall {
				continue
			}
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusNormalClosure, "final_answer_tail_timeout")
			session.mu.Unlock()
			providers.DebugLogf("Responses WebSocket inferred completion after final-answer tail grace")
			lease.Succeed()
			emit.Send(responsesInferredFinalAnswerDoneEvent())
			return
		}
		if !ok && frame.err == nil {
			session.mu.Lock()
			frame.err = session.activeErr
			session.activeErr = nil
			session.mu.Unlock()
			if frame.err == nil {
				frame.err = providers.NewIncompleteStreamError("websocket stream closed before response.completed")
			}
		}
		if frame.err != nil {
			if ctx.Err() != nil {
				session.mu.Lock()
				c.responsesWebSocketReleaseLocked(session, readCh)
				c.responsesWebSocketAbortConnectionLocked(session)
				session.mu.Unlock()
				lease.FailError(ctx.Err())
				emit.Send(providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()})
				return
			}
			failure := providers.NormalizeFailure(frame.err)
			if failure.Category == providers.FailureLocalBackpressure || failure.Category == providers.FailureResponseTooLarge {
				session.mu.Lock()
				c.responsesWebSocketReleaseLocked(session, readCh)
				session.mu.Unlock()
				lease.FailError(frame.err)
				emit.Send(providers.StreamEvent{Type: providers.EventError, Error: frame.err})
				return
			}
			if !sawProviderEvent {
				const reason = "websocket_failed_before_first_event"
				providers.DebugLogf("Responses websocket failed before first provider event; falling back to SSE: %v", frame.err)
				session.mu.Lock()
				c.responsesWebSocketReleaseLocked(session, readCh)
				var fallback responsesWebSocketFallbackMeta
				if cacheConnection {
					fallback = c.responsesWebSocketActivateFallbackLocked(session, reason, frame.err)
				} else {
					c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, reason)
				}
				session.mu.Unlock()
				if !cacheConnection {
					fallback = c.responsesWebSocketMarkFallback(fallbackSession, reason, frame.err)
				}
				fallbackErr := newResponsesWebSocketFallbackError(reason, frame.err, fallback)
				lease.FallbackError(fallbackErr)
				emit.Send(providers.StreamEvent{
					Type:          providers.EventProviderState,
					ProviderState: responsesWebSocketTransportFailureState(state, reason, fallback),
				})
				emit.Send(providers.StreamEvent{Type: providers.EventError, Error: fallbackErr})
				return
			}
			streamCause := providers.NewIncompleteStreamError(fmt.Sprintf("websocket stream closed after provider event: %v", frame.err))
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			var fallback responsesWebSocketFallbackMeta
			if cacheConnection {
				fallback = c.responsesWebSocketActivateFallbackLocked(session, "stream_error_after_provider_event", streamCause)
			} else {
				c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "stream_error_after_provider_event")
			}
			session.mu.Unlock()
			if !cacheConnection {
				fallback = c.responsesWebSocketMarkFallback(fallbackSession, "stream_error_after_provider_event", streamCause)
			}
			streamErr := newResponsesWebSocketFallbackError(
				"stream_error_after_provider_event",
				streamCause,
				fallback,
			)
			// The session is pinned to SSE for the next engine attempt. Keep the
			// replay decision and billable ambiguity in the attempt outcome, but
			// do not let a WS-only disconnect open the cross-transport circuit.
			lease.FallbackError(streamErr)
			emit.Send(providers.StreamEvent{
				Type:          providers.EventProviderState,
				ProviderState: responsesWebSocketTransportFailureState(state, "stream_error_after_provider_event", fallback),
			})
			emit.Send(providers.StreamEvent{
				Type:  providers.EventError,
				Error: streamErr,
			})
			return
		}
		if frame.typ != websocket.MessageText {
			continue
		}
		providers.DebugLogfWire("Responses websocket raw: %s", string(frame.data))
		var event responsesStreamEvent
		if err := json.Unmarshal(frame.data, &event); err != nil {
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "parse_error")
			session.mu.Unlock()
			err = fmt.Errorf("parse websocket event: %w", err)
			lease.FailError(err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}
		if responsesWebSocketConnectionLimitReached(event) && !sawProviderEvent {
			// Connection capacity is transport-specific. The retry/fallback owns
			// a new lease, while account-wide rate limits remain shared.
			limitErr := errors.New("websocket connection limit reached")
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			var fallback responsesWebSocketFallbackMeta
			if cacheConnection {
				fallback = c.responsesWebSocketActivateFallbackLocked(session, responsesWebSocketConnectionLimitCode, limitErr)
			} else {
				c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, responsesWebSocketConnectionLimitCode)
			}
			session.mu.Unlock()
			if !cacheConnection {
				fallback = c.responsesWebSocketMarkFallback(fallbackSession, responsesWebSocketConnectionLimitCode, limitErr)
			}
			fallbackErr := newResponsesWebSocketFallbackError(responsesWebSocketConnectionLimitCode, limitErr, fallback)
			lease.FallbackError(fallbackErr)
			emit.Send(providers.StreamEvent{
				Type:          providers.EventProviderState,
				ProviderState: responsesWebSocketTransportFailureState(state, responsesWebSocketConnectionLimitCode, fallback),
			})
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: fallbackErr})
			return
		}
		if event.Type != "error" {
			sawProviderEvent = true
		}

		switch event.Type {
		case "response.created":
			if event.Response != nil {
				responseID = event.Response.ID
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			pendingReasoning.appendDelta(event, event.Delta, emit)

		case "response.reasoning_summary_part.done":
			pendingReasoning.appendDelta(event, "\n\n", emit)

		case "response.output_text.delta":
			if event.Delta != "" {
				if event.ItemID != "" {
					currentTextItemID = event.ItemID
				}
				emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Content: event.Delta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
			}

		case "response.output_item.added":
			switch event.Item.Type {
			case "reasoning":
				pendingReasoning.start(event.Item, event.outputIndex())
			case "message":
				if event.Item.ID != "" {
					currentTextItemID = event.Item.ID
				}
				if phase := providers.NormalizeMessagePhase(event.Item.Phase); phase != "" {
					currentTextPhase = phase
					emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
				}
			case "function_call", "tool_search_call":
				sawToolCall = true
				disarmFinalAnswerTail()
				pending.start(event.Item, event.outputIndex(), emit)
			}

		case "response.function_call_arguments.delta", "response.tool_search_call.arguments.delta":
			if event.Delta != "" {
				pending.appendDelta(event, emit)
			}

		case "response.function_call_arguments.done", "response.tool_search_call.arguments.done":
			pending.setArguments(event)

		case "response.output_item.done":
			if item, ok := responsesOutputItemReplayInput(event.Item); ok {
				responseItems = append(responseItems, item)
			}
			switch event.Item.Type {
			case "reasoning":
				pendingReasoning.emitDone(event, emit)
			case "message":
				if event.Item.ID != "" {
					currentTextItemID = event.Item.ID
				}
				if phase := providers.NormalizeMessagePhase(event.Item.Phase); phase != "" {
					currentTextPhase = phase
					emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
				}
				if responsesFinalAnswerItemDone(event, sawToolCall) {
					armFinalAnswerTail()
				}
			case "function_call", "tool_search_call":
				sawToolCall = true
				disarmFinalAnswerTail()
				pt := pending.start(event.Item, event.outputIndex(), emit)
				pending.emitEnd(pt, event.Item.argumentsString(), emit)
			}

		case "response.completed", "response.done", "response.incomplete":
			if event.Response != nil && event.Response.ID != "" {
				responseID = event.Response.ID
			}
			pending.emitEnds(emit)
			usage, stopReason, finishReason, truncated := responsesDoneMetadata(event.Response, sawToolCall)
			session.mu.Lock()
			if useCachedContext {
				responsesWebSocketStoreContinuation(session, generation, fullPayload, responseID, responseItems)
			} else {
				session.continuation = nil
			}
			c.responsesWebSocketReleaseLocked(session, readCh)
			if cacheConnection {
				c.responsesWebSocketScheduleIdleExpiryLocked(session)
			} else {
				c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusNormalClosure, "done")
			}
			session.mu.Unlock()
			lease.SucceedWithUsage(usage)
			emit.Send(providers.StreamEvent{
				Type:              providers.EventDone,
				Usage:             usage,
				StopReason:        stopReason,
				FinishReason:      finishReason,
				Truncated:         truncated,
				ProviderEventType: event.Type,
			})
			return

		case "response.failed":
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "response_failed")
			session.mu.Unlock()
			err := event.asError()
			lease.FailError(err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return

		case "error":
			session.mu.Lock()
			c.responsesWebSocketReleaseLocked(session, readCh)
			c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, "response_error")
			session.mu.Unlock()
			err := event.asError()
			lease.FailError(err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}
	}
}

func responsesWebSocketProviderState(fullPayload, requestPayload responsesRequest, configuredTransport providers.StreamTransportMode, meta responsesWebSocketRequestMeta) *providers.ProviderStateSummary {
	previous := strings.TrimSpace(requestPayload.PreviousResponseID) != ""
	replayMode := "full_request"
	deltaItems := 0
	if previous {
		replayMode = "previous_response_id"
		deltaItems = len(requestPayload.Input)
	}
	return &providers.ProviderStateSummary{
		Provider:               "openai",
		Protocol:               "responses_websocket",
		Transport:              "websocket",
		ConfiguredTransport:    string(configuredTransport),
		ReplayMode:             replayMode,
		PreviousResponseIDUsed: previous,
		ConnectionReused:       meta.connectionReused,
		InputItems:             len(requestPayload.Input),
		FullInputItems:         len(fullPayload.Input),
		DeltaInputItems:        deltaItems,
	}
}

func responsesWebSocketTransportFailureState(state *providers.ProviderStateSummary, reason string, fallback responsesWebSocketFallbackMeta) *providers.ProviderStateSummary {
	if state == nil {
		return nil
	}
	diagnostic := *state
	diagnostic.Diagnostic = "provider_transport_failure"
	diagnostic.TransportFailurePhase = responsesWebSocketFallbackPhase(reason)
	diagnostic.FailedTransport = strings.TrimSpace(state.Transport)
	if diagnostic.TransportFailurePhase == "before_message_stream_start" {
		diagnostic.FallbackTransport = "http"
	} else {
		diagnostic.FallbackTransport = ""
	}
	diagnostic.EventsEmitted = diagnostic.TransportFailurePhase == "after_message_stream_start"
	diagnostic.FallbackActive = true
	diagnostic.FallbackReason = strings.TrimSpace(reason)
	applyResponsesWebSocketFallbackMeta(&diagnostic, fallback)
	return &diagnostic
}

func responsesWebSocketFallbackPhase(reason string) string {
	if strings.TrimSpace(reason) == "stream_error_after_provider_event" {
		return "after_message_stream_start"
	}
	return "before_message_stream_start"
}

func responsesWebSocketConnectionLimitReached(event responsesStreamEvent) bool {
	return responsesErrorCode(event) == responsesWebSocketConnectionLimitCode
}

func responsesErrorCode(event responsesStreamEvent) string {
	return strings.TrimSpace(event.errorDetail().Code)
}

func newResponsesWebSocketFallbackError(reason string, err error, fallback responsesWebSocketFallbackMeta) *responsesWebSocketFallbackError {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "websocket_unavailable_before_start"
	}
	return &responsesWebSocketFallbackError{reason: reason, err: err, fallback: fallback}
}

func responsesWebSocketFallbackReason(err error) string {
	var fallbackErr *responsesWebSocketFallbackError
	if errors.As(err, &fallbackErr) && strings.TrimSpace(fallbackErr.reason) != "" {
		return fallbackErr.reason
	}
	return "websocket_unavailable_before_start"
}

func responsesWebSocketFallbackMetaFromError(err error) responsesWebSocketFallbackMeta {
	var fallbackErr *responsesWebSocketFallbackError
	if errors.As(err, &fallbackErr) {
		return fallbackErr.fallback
	}
	return responsesWebSocketFallbackMeta{}
}

func applyResponsesWebSocketFallbackMeta(state *providers.ProviderStateSummary, fallback responsesWebSocketFallbackMeta) {
	if state == nil {
		return
	}
	state.FallbackPinStatus = fallback.pinStatus
	state.FallbackRetryAfterMS = durationMillis(fallback.retryAfter)
	state.FallbackTTLMS = durationMillis(fallback.ttl)
}

func durationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	if millis := d.Milliseconds(); millis > 0 {
		return millis
	}
	return 1
}

func (c *Client) responsesWebSocketReleaseLocked(session *responsesWebSocketSession, readCh chan responsesWebSocketReadEvent) {
	if session.active == readCh {
		if session.activeDone != nil {
			close(session.activeDone)
		}
		session.active = nil
		session.activeDone = nil
		session.activeCtxDone = nil
	}
	session.busy = false
}

func (c *Client) responsesWebSocketCancelIdleTimerLocked(session *responsesWebSocketSession) {
	if session.idleTimer == nil {
		return
	}
	session.idleTimer.Stop()
	session.idleTimer = nil
}

func (c *Client) responsesWebSocketScheduleIdleExpiryLocked(session *responsesWebSocketSession) {
	if session == nil || session.cache == nil || session.cache.idleTTL <= 0 || session.conn == nil || session.busy || session.active != nil {
		return
	}
	c.responsesWebSocketCancelIdleTimerLocked(session)
	cache := session.cache
	sessionID := session.id
	ttl := cache.idleTTL
	if session.warmOnly && (ttl <= 0 || ttl > responsesWebSocketPrewarmTTL) {
		ttl = responsesWebSocketPrewarmTTL
	}
	session.idleTimer = time.AfterFunc(ttl, func() {
		cache.expireIdleSession(sessionID, session)
	})
}

func (c *Client) responsesWebSocketActivateFallbackLocked(session *responsesWebSocketSession, reason string, err error) responsesWebSocketFallbackMeta {
	// Invalidate first: it cancels the idle timer, and marking afterwards
	// installs the pin-expiry timer that reclaims this session.
	c.responsesWebSocketInvalidateConnectionLocked(session, websocket.StatusInternalError, reason)
	return c.responsesWebSocketMarkFallbackLocked(session, reason, err)
}

func (c *Client) responsesWebSocketMarkFallback(session *responsesWebSocketSession, reason string, err error) responsesWebSocketFallbackMeta {
	if session == nil {
		return responsesWebSocketFallbackMeta{}
	}
	session.mu.Lock()
	fallback := c.responsesWebSocketMarkFallbackLocked(session, reason, err)
	session.mu.Unlock()
	return fallback
}

func (c *Client) responsesWebSocketMarkFallbackLocked(session *responsesWebSocketSession, reason string, err error) responsesWebSocketFallbackMeta {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "websocket_unavailable_before_start"
	}
	ttl := responsesWebSocketFallbackTTL(reason, err)
	now := time.Now()
	until := now.Add(ttl)
	pinStatus := responsesWebSocketFallbackPinCreated
	if session.fallbackActiveLocked(now) {
		if !until.After(session.fallback.until) {
			fallback := session.responsesWebSocketFallbackMetaLocked(now, responsesWebSocketFallbackPinReused)
			c.responsesWebSocketScheduleFallbackExpiryLocked(session, fallback.retryAfter)
			return fallback
		}
		pinStatus = responsesWebSocketFallbackPinExtended
	}
	session.fallback = responsesWebSocketFallbackState{
		active: true,
		reason: reason,
		until:  until,
		ttl:    ttl,
	}
	// A pinned session holds no connection, so the regular idle expiry never
	// runs for it. Arm a timer so the entry is reclaimed (and the websocket
	// retried) once the pin lapses instead of accumulating forever.
	c.responsesWebSocketScheduleFallbackExpiryLocked(session, ttl)
	return session.responsesWebSocketFallbackMetaLocked(now, pinStatus)
}

func (c *Client) responsesWebSocketScheduleFallbackExpiryLocked(session *responsesWebSocketSession, after time.Duration) {
	if session == nil || session.cache == nil {
		return
	}
	if after <= 0 {
		after = time.Millisecond
	}
	c.responsesWebSocketCancelIdleTimerLocked(session)
	cache := session.cache
	sessionID := session.id
	session.idleTimer = time.AfterFunc(after, func() {
		cache.expireIdleSession(sessionID, session)
	})
}

func (s *responsesWebSocketSession) responsesWebSocketFallbackMetaLocked(now time.Time, pinStatus string) responsesWebSocketFallbackMeta {
	retryAfter := time.Duration(0)
	if now.Before(s.fallback.until) {
		retryAfter = s.fallback.until.Sub(now)
	}
	return responsesWebSocketFallbackMeta{
		pinStatus:  pinStatus,
		retryAfter: retryAfter,
		ttl:        s.fallback.ttl,
	}
}

func responsesWebSocketFallbackTTL(reason string, err error) time.Duration {
	if strings.TrimSpace(reason) == responsesWebSocketConnectionLimitCode {
		return responsesWebSocketPressureFallbackTTL
	}

	var dialErr *CodexWebSocketDialError
	if errors.As(err, &dialErr) {
		switch dialErr.StatusCode {
		case http.StatusTooManyRequests:
			return responsesWebSocketPressureFallbackTTL
		case http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusPaymentRequired,
			http.StatusForbidden,
			http.StatusNotFound,
			http.StatusMethodNotAllowed,
			http.StatusNotAcceptable,
			http.StatusProxyAuthRequired,
			http.StatusGone,
			http.StatusUnsupportedMediaType,
			http.StatusUpgradeRequired,
			http.StatusNotImplemented:
			return responsesWebSocketLongFallbackTTL
		}
	}
	return responsesWebSocketTransientFallbackTTL
}

func (c *Client) responsesWebSocketInvalidateConnectionLocked(session *responsesWebSocketSession, status websocket.StatusCode, reason string) {
	c.responsesWebSocketCancelIdleTimerLocked(session)
	if session.conn != nil {
		_ = session.conn.Close(status, reason)
	}
	session.conn = nil
	session.wsURL = ""
	if session.activeDone != nil {
		close(session.activeDone)
	}
	session.active = nil
	session.activeDone = nil
	session.activeCtxDone = nil
	session.continuation = nil
	session.generation++
}

func (c *Client) responsesWebSocketAbortConnectionLocked(session *responsesWebSocketSession) {
	c.responsesWebSocketCancelIdleTimerLocked(session)
	if session.conn != nil {
		_ = session.conn.CloseNow()
	}
	session.conn = nil
	session.wsURL = ""
	if session.activeDone != nil {
		close(session.activeDone)
	}
	session.active = nil
	session.activeDone = nil
	session.activeCtxDone = nil
	session.continuation = nil
	session.generation++
}

func (c *ResponsesWebSocketCache) expireIdleSession(sessionID string, session *responsesWebSocketSession) {
	session.mu.Lock()
	if session.busy || session.active != nil {
		session.idleTimer = nil
		session.mu.Unlock()
		return
	}
	if session.conn != nil {
		_ = session.conn.Close(websocket.StatusNormalClosure, "idle_timeout")
	}
	session.conn = nil
	session.wsURL = ""
	session.active = nil
	session.continuation = nil
	session.generation++
	now := time.Now()
	keepSession := session.fallbackActiveLocked(now)
	session.idleTimer = nil
	if keepSession && !session.fallback.until.IsZero() {
		// Fired before the pin lapsed (e.g. a connection idle expiry).
		// Re-arm so the entry is still reclaimed at pin expiry.
		sessionID := sessionID
		session.idleTimer = time.AfterFunc(session.fallback.until.Sub(now)+time.Second, func() {
			c.expireIdleSession(sessionID, session)
		})
	}
	session.mu.Unlock()

	if keepSession {
		return
	}
	c.mu.Lock()
	if c.sessions[sessionID] == session {
		delete(c.sessions, sessionID)
	}
	c.mu.Unlock()
}

func (c *ResponsesWebSocketCache) session(sessionID string) *responsesWebSocketSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessions == nil {
		c.sessions = make(map[string]*responsesWebSocketSession)
	}
	session := c.sessions[sessionID]
	if session == nil {
		session = &responsesWebSocketSession{id: sessionID, cache: c}
		c.sessions[sessionID] = session
	}
	return session
}

func responsesWebSocketSessionID(payload responsesRequest) string {
	if payload.ExtraHeaders != nil {
		if id := strings.TrimSpace(payload.ExtraHeaders["session-id"]); id != "" {
			return id
		}
	}
	return strings.TrimSpace(payload.PromptCacheKey)
}

func responsesCachedWebSocketRequest(session *responsesWebSocketSession, generation uint64, payload responsesRequest) responsesRequest {
	continuation := session.continuation
	if continuation == nil || continuation.lastResponseID == "" {
		return payload
	}
	if continuation.generation != generation {
		session.continuation = nil
		return payload
	}
	rest, err := responsesRequestRestJSON(payload)
	if err != nil || string(rest) != string(continuation.lastRequestRest) {
		session.continuation = nil
		return payload
	}
	delta, ok := responsesCachedInputDelta(payload.Input, continuation)
	if !ok {
		session.continuation = nil
		return payload
	}
	payload.PreviousResponseID = continuation.lastResponseID
	payload.Input = delta
	return payload
}

func responsesCachedInputDelta(current []responsesInputItem, continuation *responsesWebSocketContinuation) ([]responsesInputItem, bool) {
	baseline := make([]responsesInputItem, 0, len(continuation.lastRequestInput)+len(continuation.lastResponseItems))
	baseline = append(baseline, continuation.lastRequestInput...)
	baseline = append(baseline, continuation.lastResponseItems...)
	return responsesCachedInputDeltaFromBaseline(current, baseline)
}

func responsesCachedInputDeltaFromBaseline(current, baseline []responsesInputItem) ([]responsesInputItem, bool) {
	if len(current) < len(baseline) {
		if !responsesInputCanMatchBaselineWithRefreshableContext(current, baseline) {
			return nil, false
		}
	}
	var delta []responsesInputItem
	i := 0
	for j := 0; j < len(baseline); j++ {
		base := baseline[j]
		if isRefreshableResponsesInputItem(base) {
			if i < len(current) && responsesInputItemEqual(current[i], base) {
				i++
			}
			continue
		}
		for i < len(current) && isRefreshableResponsesInputItem(current[i]) {
			delta = append(delta, current[i])
			i++
		}
		if i >= len(current) || !responsesInputItemEqual(current[i], base) {
			return nil, false
		}
		i++
	}
	if i < len(current) {
		delta = append(delta, current[i:]...)
	}
	if delta == nil {
		delta = make([]responsesInputItem, 0)
	}
	return delta, true
}

func responsesWebSocketStoreContinuation(session *responsesWebSocketSession, generation uint64, payload responsesRequest, responseID string, responseItems []responsesInputItem) {
	if strings.TrimSpace(responseID) == "" {
		session.continuation = nil
		return
	}
	if session.generation != generation || session.conn == nil {
		session.continuation = nil
		return
	}
	rest, err := responsesRequestRestJSON(payload)
	if err != nil {
		session.continuation = nil
		return
	}
	session.continuation = &responsesWebSocketContinuation{
		generation:        generation,
		lastRequestRest:   rest,
		lastRequestInput:  append([]responsesInputItem(nil), payload.Input...),
		lastResponseID:    responseID,
		lastResponseItems: append([]responsesInputItem(nil), responseItems...),
	}
}

func responsesRequestRestJSON(payload responsesRequest) ([]byte, error) {
	body, err := marshalResponsesRequest(payload)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	delete(object, "input")
	delete(object, "previous_response_id")
	return json.Marshal(object)
}

func responsesInputItemsEqual(a, b []responsesInputItem) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return jsonBytesEqual(aj, bj)
}

func responsesInputItemEqual(a, b responsesInputItem) bool {
	return responsesInputItemsEqual([]responsesInputItem{a}, []responsesInputItem{b})
}

func jsonBytesEqual(a, b []byte) bool {
	var av any
	var bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return string(a) == string(b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}

func responsesInputCanMatchBaselineWithRefreshableContext(current, baseline []responsesInputItem) bool {
	nonRefreshable := 0
	for _, item := range baseline {
		if !isRefreshableResponsesInputItem(item) {
			nonRefreshable++
		}
	}
	return len(current) >= nonRefreshable
}

func isRefreshableResponsesInputItem(item responsesInputItem) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Role), "user") {
		return false
	}
	text := strings.TrimSpace(responsesInputItemText(item))
	return wuucontext.IsSystemReminder("", text)
}

func responsesInputItemText(item responsesInputItem) string {
	switch content := item.Content.(type) {
	case string:
		return content
	case []responsesInputContentPart:
		parts := make([]string, 0, len(content))
		for _, part := range content {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(content))
		for _, raw := range content {
			if part, ok := raw.(map[string]any); ok {
				if text, ok := part["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func marshalResponsesWebSocketCreate(payload responsesRequest) ([]byte, error) {
	body, err := marshalResponsesRequest(payload)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	object["type"] = json.RawMessage(`"response.create"`)
	return json.Marshal(object)
}

func responsesOutputItemReplayInput(item responsesOutputItem) (responsesInputItem, bool) {
	switch item.Type {
	case "reasoning":
		if len(item.Raw) == 0 {
			return responsesInputItem{}, false
		}
		return responsesInputItem{Raw: append(json.RawMessage(nil), stripResponsesReasoningStatus(item.Raw)...)}, true
	case "message":
		content, err := parseResponsesContent(item.Content)
		if err != nil || content == "" {
			return responsesInputItem{}, false
		}
		return responsesInputItem{
			Type:    "message",
			ID:      clampResponsesItemID(item.ID),
			Role:    "assistant",
			Phase:   string(providers.NormalizeMessagePhase(item.Phase)),
			Content: []responsesInputContentPart{{Type: "output_text", Text: content}},
		}, true
	case "function_call":
		return responsesInputItem{
			Type:      "function_call",
			ID:        responsesFunctionCallItemID(item.ID),
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.argumentsString(),
		}, true
	case "tool_search_call":
		return responsesInputItem{
			Type:      "tool_search_call",
			ID:        clampResponsesItemID(item.ID),
			CallID:    item.CallID,
			Status:    "completed",
			Execution: "client",
			Arguments: responsesToolSearchArguments(item.argumentsString()),
		}, true
	default:
		return responsesInputItem{}, false
	}
}
