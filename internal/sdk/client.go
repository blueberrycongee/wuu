package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

// ClientOptions identifies an in-process SDK client to the embedded runtime.
// The connection remains alive until its context ends or Close is called.
type ClientOptions struct {
	Name    string
	Version string
}

// BuildInfo identifies the embedded core build.
type BuildInfo struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
}

// Initialization is the stable subset of host state most SDK clients need.
// Raw contains the complete versioned app-server result for advanced clients.
type Initialization struct {
	Status          string
	ProtocolVersion string
	Core            BuildInfo
	Provider        string
	Model           string
	Effort          string
	Variant         string
	MaxParallel     int
	RuntimeHost     Host
	WorkspaceRoot   string
	Raw             json.RawMessage
}

// ProtocolError is a typed error returned by the versioned app-server contract.
type ProtocolError struct {
	Code    string
	Message string
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Code) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Event is one authoritative app-server notification. Decode unmarshals Params
// into an application-owned type without exposing Wuu's internal Go packages.
type Event struct {
	Method string
	Params json.RawMessage
}

func (e Event) Decode(value any) error {
	if value == nil {
		return errors.New("event destination is required")
	}
	if err := json.Unmarshal(e.Params, value); err != nil {
		return fmt.Errorf("decode %s event: %w", e.Method, err)
	}
	return nil
}

// SessionID returns the thread/session identity carried by the event, if any.
func (e Event) SessionID() string {
	var envelope struct {
		ThreadID string `json:"thread_id"`
		Thread   *struct {
			ID string `json:"id"`
		} `json:"thread"`
		Run *struct {
			ThreadID string `json:"thread_id"`
		} `json:"run"`
		Queued *struct {
			ThreadID string `json:"thread_id"`
		} `json:"queued"`
	}
	if json.Unmarshal(e.Params, &envelope) != nil {
		return ""
	}
	if envelope.ThreadID != "" {
		return envelope.ThreadID
	}
	if envelope.Thread != nil && envelope.Thread.ID != "" {
		return envelope.Thread.ID
	}
	if envelope.Run != nil && envelope.Run.ThreadID != "" {
		return envelope.Run.ThreadID
	}
	if envelope.Queued != nil {
		return envelope.Queued.ThreadID
	}
	return ""
}

type eventSubscriber struct {
	ctx       context.Context
	cancel    context.CancelFunc
	filterID  string
	out       chan Event
	clientEnd <-chan struct{}

	mu        sync.Mutex
	queue     []Event
	bytes     int
	maxEvents int
	maxBytes  int
	err       error
	wake      chan struct{}
}

// ErrSubscriptionOverflow means a consumer fell behind its pending-event limit.
// The subscription closes instead of silently dropping events or blocking RPCs.
var ErrSubscriptionOverflow = errors.New("SDK event subscription overflow")

// Subscription delivers events in order. Consumers must drain Events or call
// Close and check Err when Events closes. Pending events are bounded separately
// for each subscription; a slow consumer does not block RPCs or other subscribers.
type Subscription struct {
	Events <-chan Event
	close  func()
	err    func() error
	once   sync.Once
}

// SubscriptionOptions controls local event delivery. Buffer changes only the
// application-side queue; events are never silently dropped.
type SubscriptionOptions struct {
	// Buffer is the number of events immediately available to the consumer.
	// Values less than one use an unbuffered consumer channel; additional pending
	// events remain in the subscription's private queue.
	Buffer int
	// MaxPendingEvents and MaxPendingBytes bound the private queue, excluding
	// Buffer and the event being delivered. Nonpositive values use 1024 events
	// and 16 MiB of encoded payload respectively. Overflow closes the stream.
	MaxPendingEvents int
	MaxPendingBytes  int
}

// Err reports why event delivery stopped. Explicit Close is not an error.
func (s *Subscription) Err() error {
	if s == nil || s.err == nil {
		return nil
	}
	return s.err()
}

// Close stops the subscription. It does not close the Client or Session.
func (s *Subscription) Close() {
	if s == nil || s.close == nil {
		return
	}
	s.once.Do(s.close)
}

// Client is an initialized in-process connection to a Runtime. It provides the
// session facade while all durable state and execution remain app-server owned.
type Client struct {
	rpc       *protocolClient
	cancel    context.CancelFunc
	pipes     []io.Closer
	serveDone chan error
	eventDone chan struct{}

	mu        sync.RWMutex
	init      Initialization
	sessions  map[string]SessionSnapshot
	runs      map[string]RunSnapshot
	runTexts  map[string]string
	turnTexts map[string]string
	subs      map[*eventSubscriber]struct{}

	closeMu   sync.Mutex
	closing   bool
	closeDone chan struct{}
	closeErr  error
}

// Connect creates and initializes an in-process app-server connection. The
// supplied context owns the connection lifetime; use a longer-lived context
// than any individual Send or Wait operation.
func (r *Runtime) Connect(ctx context.Context, opts ClientOptions) (*Client, error) {
	if r == nil {
		return nil, errors.New("runtime is required")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	serverCtx, cancel := context.WithCancel(ctx)
	c := &Client{
		rpc:       newProtocolClient(serverOutR, serverInW),
		cancel:    cancel,
		pipes:     []io.Closer{serverInR, serverInW, serverOutR, serverOutW},
		serveDone: make(chan error, 1),
		eventDone: make(chan struct{}),
		sessions:  make(map[string]SessionSnapshot),
		runs:      make(map[string]RunSnapshot),
		runTexts:  make(map[string]string),
		turnTexts: make(map[string]string),
		subs:      make(map[*eventSubscriber]struct{}),
		closeDone: make(chan struct{}),
	}
	go func() {
		err := r.Serve(serverCtx, serverInR, serverOutW)
		_ = serverInR.CloseWithError(err)
		_ = serverOutW.CloseWithError(err)
		c.serveDone <- err
	}()
	go c.forwardEvents()

	var raw json.RawMessage
	err := c.rpc.call(ctx, appserver.MethodInitialize, struct {
		ProtocolVersion string `json:"protocol_version"`
		Client          struct {
			Name    string `json:"name,omitempty"`
			Version string `json:"version,omitempty"`
		} `json:"client,omitempty"`
	}{
		ProtocolVersion: ProtocolVersion,
		Client: struct {
			Name    string `json:"name,omitempty"`
			Version string `json:"version,omitempty"`
		}{Name: strings.TrimSpace(opts.Name), Version: strings.TrimSpace(opts.Version)},
	}, &raw)
	if err != nil {
		c.abort()
		return nil, err
	}
	initialization, err := decodeInitialization(raw)
	if err != nil {
		c.abort()
		return nil, err
	}
	c.mu.Lock()
	c.init = initialization
	c.mu.Unlock()
	return c, nil
}

// Initialization returns the server-authoritative state captured at connect.
func (c *Client) Initialization() Initialization {
	if c == nil {
		return Initialization{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := c.init
	result.Raw = cloneRaw(result.Raw)
	return result
}

// Subscribe returns all app-server events for this connection.
func (c *Client) Subscribe(ctx context.Context, opts SubscriptionOptions) *Subscription {
	if c == nil {
		return closedSubscription()
	}
	return c.subscribe(ctx, "", opts)
}

func (c *Client) subscribe(ctx context.Context, sessionID string, opts SubscriptionOptions) *Subscription {
	if ctx == nil {
		ctx = context.Background()
	}
	buffer := opts.Buffer
	if buffer < 0 {
		buffer = 0
	}
	subscriberCtx, cancel := context.WithCancel(ctx)
	maxEvents, maxBytes := opts.MaxPendingEvents, opts.MaxPendingBytes
	if maxEvents <= 0 {
		maxEvents = 1024
	}
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	subscriber := &eventSubscriber{
		ctx: subscriberCtx, cancel: cancel, filterID: strings.TrimSpace(sessionID),
		out: make(chan Event, buffer), clientEnd: c.eventDone, wake: make(chan struct{}, 1),
		maxEvents: maxEvents, maxBytes: maxBytes,
	}
	c.mu.Lock()
	c.subs[subscriber] = struct{}{}
	c.mu.Unlock()
	go func() {
		subscriber.run()
		c.removeSubscriber(subscriber)
	}()
	return &Subscription{
		Events: subscriber.out,
		err: func() error {
			subscriber.mu.Lock()
			defer subscriber.mu.Unlock()
			return subscriber.err
		},
		close: func() {
			cancel()
			c.removeSubscriber(subscriber)
		},
	}
}

func closedSubscription() *Subscription {
	out := make(chan Event)
	close(out)
	return &Subscription{Events: out, close: func() {}}
}

func (s *eventSubscriber) run() {
	defer close(s.out)
	for {
		if s.ctx.Err() != nil {
			return
		}
		event, ok := s.next()
		if ok {
			select {
			case s.out <- event:
				continue
			case <-s.ctx.Done():
				return
			}
		}
		select {
		case <-s.wake:
		case <-s.ctx.Done():
			return
		case <-s.clientEnd:
			if !s.hasPending() {
				return
			}
		}
	}
}

func (s *eventSubscriber) next() (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return Event{}, false
	}
	event := s.queue[0]
	s.bytes -= len(event.Method) + len(event.Params)
	s.queue[0] = Event{}
	if len(s.queue) == 1 {
		s.queue = nil
	} else {
		s.queue = s.queue[1:]
	}
	return event, true
}

func (s *eventSubscriber) hasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue) != 0
}

func (s *eventSubscriber) enqueue(event Event) {
	select {
	case <-s.ctx.Done():
		return
	case <-s.clientEnd:
		return
	default:
	}
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return
	}
	size := len(event.Method) + len(event.Params)
	if len(s.queue) >= s.maxEvents || size > s.maxBytes-s.bytes {
		s.err = ErrSubscriptionOverflow
		s.queue = nil
		s.bytes = 0
		s.cancel()
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, event)
	s.bytes += size
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (c *Client) forwardEvents() {
	defer close(c.eventDone)
	for event := range c.rpc.events {
		c.rememberEvent(event)
		c.mu.RLock()
		subscribers := make([]*eventSubscriber, 0, len(c.subs))
		for subscriber := range c.subs {
			subscribers = append(subscribers, subscriber)
		}
		c.mu.RUnlock()
		for _, subscriber := range subscribers {
			if subscriber.filterID != "" && event.SessionID() != subscriber.filterID {
				continue
			}
			delivered := Event{Method: event.Method, Params: cloneRaw(event.Params)}
			subscriber.enqueue(delivered)
		}
	}
}

func (c *Client) removeSubscriber(subscriber *eventSubscriber) {
	c.mu.Lock()
	delete(c.subs, subscriber)
	c.mu.Unlock()
}

// Done closes after the app-server event stream has ended and all events have
// been recorded for snapshots and dispatched to subscriptions.
func (c *Client) Done() <-chan struct{} {
	if c == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return c.eventDone
}

// Err reports the protocol read failure after Done closes. A nil error means
// the connection ended without a transport or framing error.
func (c *Client) Err() error {
	if c == nil {
		return nil
	}
	select {
	case <-c.eventDone:
		return c.rpc.err()
	default:
		return nil
	}
}

func (c *Client) rememberEvent(event Event) {
	switch event.Method {
	case appserver.NotificationThreadStarted, appserver.NotificationThreadResumed, appserver.NotificationThreadUpdated:
		var payload struct {
			Thread json.RawMessage `json:"thread"`
		}
		if event.Decode(&payload) == nil {
			if snapshot, err := decodeSessionSnapshot(payload.Thread); err == nil {
				c.rememberSession(snapshot)
			}
		}
	case appserver.NotificationRunStarted, appserver.NotificationRunUpdated:
		var payload struct {
			Run json.RawMessage `json:"run"`
		}
		if event.Decode(&payload) == nil {
			if snapshot, err := decodeRunSnapshot(payload.Run); err == nil {
				c.rememberRun(snapshot)
			}
		}
	case appserver.NotificationTurnCompleted:
		var payload struct {
			ThreadID string `json:"thread_id"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
			Content string `json:"content"`
		}
		if event.Decode(&payload) == nil {
			c.rememberRunText(payload.ThreadID, payload.Turn.ID, payload.Content)
		}
	}
}

func (c *Client) rememberSession(snapshot SessionSnapshot) {
	if snapshot.ID == "" {
		return
	}
	c.mu.Lock()
	snapshot.Raw = cloneRaw(snapshot.Raw)
	c.sessions[snapshot.ID] = snapshot
	c.mu.Unlock()
}

func (c *Client) rememberRun(snapshot RunSnapshot) {
	if snapshot.ID == "" {
		return
	}
	c.mu.Lock()
	snapshot = cloneRunSnapshot(snapshot)
	c.runs[snapshot.ID] = snapshot
	if snapshot.FinalTurnID != "" {
		if content, ok := c.turnTexts[turnTextKey(snapshot.SessionID, snapshot.FinalTurnID)]; ok {
			c.runTexts[snapshot.ID] = content
		} else {
			// Do not let output from an earlier turn satisfy a terminal run
			// whose final turn notification has not arrived yet.
			delete(c.runTexts, snapshot.ID)
		}
	}
	c.mu.Unlock()
}

func (c *Client) rememberRunText(sessionID, turnID, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turnTexts[turnTextKey(sessionID, turnID)] = content
	for runID, run := range c.runs {
		if run.SessionID != sessionID {
			continue
		}
		if run.FinalTurnID != "" {
			if run.FinalTurnID == turnID {
				c.runTexts[runID] = content
			}
			continue
		}
		if runSnapshotContainsTurn(run, turnID) {
			c.runTexts[runID] = content
		}
	}
}

func turnTextKey(sessionID, turnID string) string {
	return sessionID + "\x00" + turnID
}

// Close requests protocol shutdown and waits for the in-process server to
// finish draining. The Runtime remains owned by the caller and must be closed
// separately after all clients have closed.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	c.closeMu.Lock()
	if c.closing {
		done := c.closeDone
		c.closeMu.Unlock()
		select {
		case <-done:
			c.closeMu.Lock()
			err := c.closeErr
			c.closeMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.closing = true
	c.closeMu.Unlock()
	go c.finishClose()

	select {
	case <-c.closeDone:
		c.closeMu.Lock()
		err := c.closeErr
		c.closeMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) finishClose() {
	c.mu.RLock()
	for subscriber := range c.subs {
		subscriber.cancel()
	}
	c.mu.RUnlock()

	var result struct {
		OK bool `json:"ok"`
	}
	err := c.rpc.call(context.Background(), appserver.MethodShutdown, nil, &result)
	for _, pipe := range c.pipes {
		_ = pipe.Close()
	}
	c.cancel()
	serveErr := <-c.serveDone
	if err == nil && serveErr != nil && !errors.Is(serveErr, io.ErrClosedPipe) {
		err = serveErr
	}

	c.closeMu.Lock()
	c.closeErr = err
	close(c.closeDone)
	c.closeMu.Unlock()
}

func (c *Client) abort() {
	for _, pipe := range c.pipes {
		_ = pipe.Close()
	}
	c.cancel()
	select {
	case <-c.serveDone:
	case <-time.After(2 * time.Second):
	}
}

func decodeInitialization(raw json.RawMessage) (Initialization, error) {
	var wire struct {
		Status          string    `json:"status"`
		ProtocolVersion string    `json:"protocol_version"`
		Core            BuildInfo `json:"core"`
		Provider        string    `json:"provider"`
		Model           string    `json:"model"`
		Effort          string    `json:"effort,omitempty"`
		Variant         string    `json:"variant,omitempty"`
		MaxParallel     int       `json:"max_parallel"`
		RuntimeHost     struct {
			Kind       HostKind `json:"kind"`
			InstanceID string   `json:"instance_id,omitempty"`
		} `json:"runtime_host"`
		WorkspaceRoot string `json:"workspace_root"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Initialization{}, fmt.Errorf("decode initialize result: %w", err)
	}
	if wire.ProtocolVersion != ProtocolVersion {
		return Initialization{}, fmt.Errorf("app-server protocol mismatch: got %q, want %q", wire.ProtocolVersion, ProtocolVersion)
	}
	return Initialization{
		Status: wire.Status, ProtocolVersion: wire.ProtocolVersion, Core: wire.Core,
		Provider: wire.Provider, Model: wire.Model, Effort: wire.Effort, Variant: wire.Variant,
		MaxParallel: wire.MaxParallel, RuntimeHost: Host{Kind: wire.RuntimeHost.Kind, InstanceID: wire.RuntimeHost.InstanceID},
		WorkspaceRoot: wire.WorkspaceRoot, Raw: cloneRaw(raw),
	}, nil
}
