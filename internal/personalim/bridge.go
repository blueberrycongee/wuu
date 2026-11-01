package personalim

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

var ErrNotEnrolled = errors.New("provider sender is not the enrolled owner for this conversation")

const ledgerNamespace = "wuu.personal_im.v1"

type Binding struct {
	Platform       string `json:"platform,omitempty"`
	EnrollmentID   string `json:"enrollment_id,omitempty"`
	TenantID       string `json:"tenant_id"`
	SenderID       string `json:"sender_id"`
	ConversationID string `json:"conversation_id"`
}
type Client interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
	Close() error
}
type Driver interface {
	Run(context.Context, func(context.Context, Incoming) error) error
	Post(context.Context, Outgoing) (Receipt, error)
	Validate(context.Context, Binding) error
}
type Options struct {
	Documents       persistence.Documents
	CredentialError string
	Current         func() bool
	Client          func(context.Context, Binding) (Client, error)
	Drivers         map[string]Driver
	Enroll          func(context.Context, Binding) (Binding, error)
	Revoke          func(context.Context, Binding) error
}
type connection struct {
	Platform        string         `json:"platform"`
	Binding         Binding        `json:"binding"`
	Enabled         bool           `json:"enabled"`
	Configured      bool           `json:"configured"`
	CredentialReady bool           `json:"credential_ready"`
	State           string         `json:"state"`
	LastError       string         `json:"last_error,omitempty"`
	ProcessingError string         `json:"processing_error,omitempty"`
	PendingOutbound int            `json:"pending_outbound"`
	UnknownDelivery int            `json:"unknown_delivery"`
	AgentID         string         `json:"agent_id,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	Cursor          int            `json:"cursor"`
	Initialized     bool           `json:"initialized"`
	LastThread      string         `json:"last_thread,omitempty"`
	Routes          map[int]string `json:"routes"`
	ThreadReplies   map[string]int `json:"thread_replies"`
}
type intake struct {
	Event       Incoming          `json:"event"`
	RequestID   string            `json:"request_id"`
	State       string            `json:"state"`
	Error       string            `json:"error,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Attachments []InputAttachment `json:"attachments,omitempty"`
	Prepared    bool              `json:"prepared"`
	ReplySeq    int               `json:"reply_seq,omitempty"`
	RetryAt     time.Time         `json:"retry_at,omitempty"`
}
type delivery struct {
	Group      string             `json:"group,omitempty"`
	Part       int                `json:"part"`
	Platform   string             `json:"platform"`
	Output     Outgoing           `json:"output"`
	State      string             `json:"state"`
	Receipt    *Receipt           `json:"receipt,omitempty"`
	Error      string             `json:"error,omitempty"`
	Sequence   int                `json:"sequence"`
	Attachment *HistoryAttachment `json:"attachment,omitempty"`
	Uploaded   *Attachment        `json:"uploaded,omitempty"`
	RetryAt    time.Time          `json:"retry_at,omitempty"`
}
type sourceReceipt struct {
	Digest string   `json:"digest"`
	Source Incoming `json:"source"`
}

type ledger struct {
	Connections map[string]*connection   `json:"connections"`
	Inbox       map[string]*intake       `json:"inbox"`
	Outbox      map[string]*delivery     `json:"outbox"`
	Seen        map[string]sourceReceipt `json:"seen"`
}
type runner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Bridge struct {
	options  Options
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	runners  map[string]*runner
	lifetime context.Context
	cancel   context.CancelFunc
	jobs     sync.WaitGroup
	closed   bool
}

func New(o Options) (*Bridge, error) {
	if o.Documents == nil || o.Client == nil {
		return nil, errors.New("Personal messaging requires selected storage and authenticated client factory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Bridge{options: o, locks: map[string]*sync.Mutex{"slack": {}, "feishu": {}}, runners: map[string]*runner{}, lifetime: ctx, cancel: cancel}, nil
}
func freshLedger() *ledger {
	s := &ledger{Connections: map[string]*connection{}, Inbox: map[string]*intake{}, Outbox: map[string]*delivery{}, Seen: map[string]sourceReceipt{}}
	for _, p := range []string{"slack", "feishu"} {
		s.Connections[p] = &connection{Platform: p, State: "unconfigured", Routes: map[int]string{}, ThreadReplies: map[string]int{}}
	}
	return s
}
func (b *Bridge) update(ctx context.Context, f func(*ledger) error) error {
	if err := persistence.ClaimNamespaces(ctx, b.options.Documents, []string{ledgerNamespace}); err != nil {
		return err
	}
	return persistence.Update(ctx, b.options.Documents, func(tx *persistence.Tx) error {
		s := freshLedger()
		if _, err := tx.Get(ledgerNamespace, "state", s); err != nil {
			return err
		}
		before, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if err := f(s); err != nil {
			return err
		}
		encoded, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if bytes.Equal(before, encoded) {
			return nil
		}
		if len(encoded) > 32<<20 {
			return errors.New("messaging backlog exceeds 32 MiB; resolve pending inputs or deliveries before accepting more")
		}
		return tx.Put(ledgerNamespace, "state", s)
	})
}
func (b *Bridge) snapshot(ctx context.Context) (*ledger, error) {
	if err := persistence.ClaimNamespaces(ctx, b.options.Documents, []string{ledgerNamespace}); err != nil {
		return nil, err
	}
	view, err := b.options.Documents.Read(ctx, persistence.Query{Namespace: ledgerNamespace, Keys: []string{"state"}})
	if err != nil {
		return nil, err
	}
	out := freshLedger()
	if len(view.Documents) > 0 {
		if err = json.Unmarshal(view.Documents[0].Value, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func key(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}
func validBinding(v Binding) bool {
	return strings.TrimSpace(v.TenantID) != "" && strings.TrimSpace(v.SenderID) != "" && strings.TrimSpace(v.ConversationID) != ""
}
func (b *Bridge) Call(ctx context.Context, operation string, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Platform string  `json:"platform"`
		Binding  Binding `json:"binding"`
		Kind     string  `json:"kind"`
		ID       string  `json:"id"`
		Decision string  `json:"decision"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if operation == "im.status" {
		s, err := b.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		rows := []*connection{}
		for _, p := range []string{"slack", "feishu"} {
			c := s.Connections[p]
			c.CredentialReady = b.options.Drivers[p] != nil
			if c.ProcessingError != "" {
				c.LastError = c.ProcessingError
			}
			if !c.CredentialReady && b.options.CredentialError != "" {
				c.LastError = b.options.CredentialError
				if c.Enabled {
					c.State = "error"
				}
			}
			c.PendingOutbound = 0
			c.UnknownDelivery = 0
			for _, d := range s.Outbox {
				if d.Platform == p {
					if d.State == "pending" {
						c.PendingOutbound++
					}
					if d.State == "uncertain" || d.State == "sending" || d.State == "preparing" {
						c.UnknownDelivery++
					}
				}
			}
			rows = append(rows, c)
		}
		return json.Marshal(map[string]any{"connections": rows})
	}
	if operation == "im.pending" && input.Platform == "" {
		combined := struct {
			Inputs  []json.RawMessage `json:"inputs"`
			Outputs []json.RawMessage `json:"outputs"`
		}{Inputs: []json.RawMessage{}, Outputs: []json.RawMessage{}}
		for _, platform := range []string{"slack", "feishu"} {
			params, _ := json.Marshal(map[string]string{"platform": platform})
			raw, err := b.Call(ctx, operation, params)
			if err != nil {
				return nil, err
			}
			var part struct {
				Inputs  []json.RawMessage `json:"inputs"`
				Outputs []json.RawMessage `json:"outputs"`
			}
			if err = json.Unmarshal(raw, &part); err != nil {
				return nil, err
			}
			combined.Inputs = append(combined.Inputs, part.Inputs...)
			combined.Outputs = append(combined.Outputs, part.Outputs...)
		}
		return json.Marshal(combined)
	}
	lock, ok := b.locks[input.Platform]
	if !ok {
		return nil, errors.New("unsupported messaging platform")
	}
	lock.Lock()
	defer lock.Unlock()
	s, err := b.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	c := s.Connections[input.Platform]
	driver := b.options.Drivers[input.Platform]
	switch operation {
	case "im.pending":
		inputs, outputs := []map[string]any{}, []map[string]any{}
		for id, i := range s.Inbox {
			if i.Event.Platform == input.Platform && i.State != "submitted" && i.State != "discarded" {
				inputs = append(inputs, map[string]any{"id": id, "platform": input.Platform, "state": i.State, "error": i.Error, "preview": preview(i.Event.Text)})
			}
		}
		for id, o := range s.Outbox {
			if o.Platform == input.Platform && o.State != "delivered" && o.State != "discarded" {
				state := o.State
				if state == "preparing" {
					state = "sending"
				}
				outputs = append(outputs, map[string]any{"id": id, "platform": input.Platform, "state": state, "error": o.Error, "preview": preview(o.Output.Text), "receipt": o.Receipt})
			}
		}
		sort.Slice(inputs, func(i, j int) bool { return inputs[i]["id"].(string) < inputs[j]["id"].(string) })
		sort.Slice(outputs, func(i, j int) bool { return outputs[i]["id"].(string) < outputs[j]["id"].(string) })
		if len(inputs) > 100 {
			inputs = inputs[:100]
		}
		if len(outputs) > 100 {
			outputs = outputs[:100]
		}
		return json.Marshal(map[string]any{"inputs": inputs, "outputs": outputs})
	case "im.resolve":
		if input.Decision != "discard" {
			return nil, errors.New("only explicit discard is supported; unknown effects cannot be retried")
		}
		err = b.update(ctx, func(s *ledger) error {
			switch input.Kind {
			case "input":
				i := s.Inbox[input.ID]
				if i == nil || i.Event.Platform != input.Platform || i.State != "pending" {
					return errors.New("input is no longer pending")
				}
				i.State = "discarded"
				i.Attachments = nil
			case "output":
				o := s.Outbox[input.ID]
				if o == nil || o.Platform != input.Platform || (o.State != "pending" && o.State != "uncertain" && o.State != "failed") {
					return errors.New("output is not safely discardable")
				}
				o.State = "discarded"
			default:
				return errors.New("unknown pending item kind")
			}
			return nil
		})
	case "im.configure":
		if c.Enabled {
			return nil, errors.New("disable this connection before changing its owner binding")
		}
		input.Binding.Platform = input.Platform
		input.Binding.EnrollmentID = ""
		if !validBinding(input.Binding) {
			return nil, errors.New("tenant, sender and conversation identities are required")
		}
		err = b.update(ctx, func(s *ledger) error {
			old := s.Connections[input.Platform]
			if old.Enabled {
				return errors.New("connection became enabled")
			}
			if old.Configured && old.Binding != input.Binding {
				for _, i := range s.Inbox {
					if i.Event.Platform == input.Platform && i.State != "submitted" && i.State != "discarded" {
						return errors.New("pending inputs must be resolved before changing the binding")
					}
				}
				for _, o := range s.Outbox {
					if o.Platform == input.Platform && o.State != "delivered" && o.State != "failed" && o.State != "discarded" {
						return errors.New("pending or uncertain replies must be resolved before changing the binding")
					}
				}
			}
			s.Connections[input.Platform] = &connection{Platform: input.Platform, Binding: input.Binding, Configured: true, State: "disabled", Routes: map[int]string{}, ThreadReplies: map[string]int{}}
			return nil
		})
	case "im.test", "im.enable", "im.retry":
		if !c.Configured || driver == nil {
			return nil, errors.New("configure the owner binding and host credentials first")
		}
		if err = driver.Validate(ctx, c.Binding); err != nil {
			return nil, err
		}
		if operation != "im.test" {
			if b.options.Enroll == nil {
				return nil, errors.New("durable owner enrollment is unavailable")
			}
			candidate := c.Binding
			if candidate.EnrollmentID == "" {
				var nonce [16]byte
				if _, err = rand.Read(nonce[:]); err != nil {
					return nil, err
				}
				candidate.EnrollmentID = hex.EncodeToString(nonce[:])
				if err = b.update(ctx, func(s *ledger) error { s.Connections[input.Platform].Binding = candidate; return nil }); err != nil {
					return nil, err
				}
			}
			enrolled, enrollErr := b.options.Enroll(ctx, candidate)
			if enrollErr != nil {
				return nil, enrollErr
			}
			if enrolled != candidate {
				return nil, errors.New("owner enrollment identity changed")
			}
			enrollment := enrolled.EnrollmentID
			if operation == "im.retry" {
				b.mu.Lock()
				old := b.runners[input.Platform]
				delete(b.runners, input.Platform)
				b.mu.Unlock()
				if old != nil {
					old.cancel()
					<-old.done
				}
			}
			err = b.update(ctx, func(s *ledger) error {
				c := s.Connections[input.Platform]
				c.Binding.EnrollmentID = enrollment
				c.Enabled = true
				c.State = "connecting"
				c.LastError = ""
				return nil
			})
			if err == nil {
				b.start(input.Platform)
			}
		}
	case "im.disable":
		if c.Binding.EnrollmentID != "" {
			if b.options.Revoke == nil {
				return nil, errors.New("owner enrollment revocation unavailable")
			}
			if err = b.options.Revoke(ctx, c.Binding); err != nil {
				return nil, err
			}
		}
		b.mu.Lock()
		cancel := b.runners[input.Platform]
		delete(b.runners, input.Platform)
		b.mu.Unlock()
		if cancel != nil {
			cancel.cancel()
			<-cancel.done
		}
		err = b.update(ctx, func(s *ledger) error {
			c := s.Connections[input.Platform]
			c.Enabled = false
			c.Binding.EnrollmentID = ""
			c.State = "disabled"
			return nil
		})
	default:
		return nil, errors.New("unknown messaging control")
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}

// Start resumes only explicitly enabled connections; construction is side-effect free.
func (b *Bridge) Start(ctx context.Context) error {
	s, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	for p, c := range s.Connections {
		if c.Enabled {
			b.start(p)
		}
	}
	return nil
}
func (b *Bridge) start(platform string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if old := b.runners[platform]; old != nil {
		return
	}
	ctx, cancel := context.WithCancel(b.lifetime)
	run := &runner{cancel: cancel, done: make(chan struct{})}
	b.runners[platform] = run
	b.jobs.Add(1)
	go func() {
		defer close(run.done)
		defer b.jobs.Done()
		driver := b.options.Drivers[platform]
		if driver == nil {
			b.setState(ctx, platform, "error", "host credentials are unavailable")
			return
		}
		snapshot, validationErr := b.snapshot(ctx)
		if validationErr == nil {
			validationErr = driver.Validate(ctx, snapshot.Connections[platform].Binding)
		}
		if validationErr != nil {
			b.setState(ctx, platform, "error", validationErr.Error())
			return
		}
		// Recovery and incoming transport share one lifetime, not the desktop window.
		done := make(chan error, 1)
		go func() {
			done <- driver.Run(ctx, func(ctx context.Context, e Incoming) error {
				err := b.Accept(ctx, e)
				if errors.Is(err, ErrNotEnrolled) {
					return nil
				}
				return err
			})
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				<-done
				return
			case err := <-done:
				if err != nil && ctx.Err() == nil {
					b.setState(ctx, platform, "error", err.Error())
				}
				return
			case <-ticker.C:
				if b.options.Current != nil && !b.options.Current() {
					cancel()
					<-done
					return
				}
				diagnostic := ""
				if err := b.Tick(ctx, platform); err != nil {
					diagnostic = err.Error()
				}
				if ctx.Err() == nil {
					_ = b.update(ctx, func(s *ledger) error { s.Connections[platform].ProcessingError = diagnostic; return nil })
				}
			}
		}
	}()
}
func (b *Bridge) setState(ctx context.Context, p, state, diagnostic string) {
	_ = b.update(ctx, func(s *ledger) error {
		c := s.Connections[p]
		if c.Enabled {
			c.State = state
			c.LastError = diagnostic
		}
		return nil
	})
}
func (b *Bridge) Close() error {
	b.mu.Lock()
	b.closed = true
	b.cancel()
	b.mu.Unlock()
	b.jobs.Wait()
	return nil
}

// Accept runs only from a verified provider transport. It acknowledges only after
// identity matching and durable insertion; replay never changes the original text.
func (b *Bridge) Accept(ctx context.Context, e Incoming) error {
	if b.options.Current != nil && !b.options.Current() {
		return errors.New("messaging provider generation is no longer selected")
	}
	if e.MessageID == "" || e.EventID == "" || e.Platform == "" {
		return errors.New("provider message identity is missing")
	}
	if e.ThreadID == "" {
		e.ThreadID = e.MessageID
	}
	k := key(e.Platform, e.TenantID, e.ConversationID, e.MessageID)
	at := time.Now().UTC()
	canonical := e
	canonical.EventID = ""
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	digest := key(string(encoded))
	return b.update(ctx, func(s *ledger) error {
		c := s.Connections[e.Platform]
		if c == nil || !c.Enabled || c.Binding.TenantID != e.TenantID || c.Binding.SenderID != e.SenderID || c.Binding.ConversationID != e.ConversationID {
			return ErrNotEnrolled
		}
		if prior, ok := s.Seen[k]; ok {
			if prior.Digest != digest {
				return errors.New("provider message identity was reused with different content")
			}
			return nil
		}
		source := e
		source.Text = ""
		source.Attachments = nil
		s.Seen[k] = sourceReceipt{Digest: digest, Source: source}

		s.Inbox[k] = &intake{Event: e, RequestID: "im-" + k, State: "pending", CreatedAt: at}
		return nil
	})
}
func invoke(ctx context.Context, c Client, m string, p any, out any) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	raw, err = c.Call(ctx, m, raw)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Tick reconciles accepted inputs and durable output in order. It never repeats
// a provider write whose outcome is uncertain, including a crash during send.
func (b *Bridge) Tick(ctx context.Context, p string) error {
	if b.options.Current != nil && !b.options.Current() {
		return errors.New("messaging provider generation is no longer selected")
	}
	lock := b.locks[p]
	if lock == nil {
		return errors.New("unsupported messaging platform")
	}
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	s, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	c := s.Connections[p]
	if !c.Enabled {
		return nil
	}
	driver := b.options.Drivers[p]
	if driver == nil {
		return errors.New("host credentials unavailable")
	}
	client, err := b.options.Client(ctx, c.Binding)
	if err != nil {
		return err
	}
	defer client.Close()
	var main struct {
		Agent struct {
			AgentID   string `json:"agent_id"`
			SessionID string `json:"main_session_id"`
		} `json:"agent"`
	}
	if err = invoke(ctx, client, "agents.ensure_default", map[string]any{}, &main); err != nil {
		return err
	}
	if !c.Initialized {
		var head struct {
			Snapshot int `json:"snapshot_seq"`
		}
		if err = invoke(ctx, client, "agents.channel_history", map[string]any{"agent_id": main.Agent.AgentID, "start_seq": 1, "limit": 1}, &head); err != nil {
			return err
		}
		if err = b.update(ctx, func(s *ledger) error {
			c := s.Connections[p]
			c.Initialized = true
			c.Cursor = head.Snapshot
			c.AgentID = main.Agent.AgentID
			c.SessionID = main.Agent.SessionID
			return nil
		}); err != nil {
			return err
		}
		c.Initialized = true
		c.Cursor = head.Snapshot
		c.AgentID = main.Agent.AgentID
		c.SessionID = main.Agent.SessionID
	}
	for _, row := range s.Inbox {
		if row.Event.Platform != p || row.State != "submitted" {
			continue
		}
		var status struct {
			Receipt *struct {
				ID    string `json:"receipt_id"`
				State string `json:"receipt_state"`
			} `json:"receipt"`
		}
		if err = invoke(ctx, client, "agents.messages.status", map[string]any{"agent_id": c.AgentID, "request_id": row.RequestID}, &status); err != nil {
			return err
		}
		if status.Receipt != nil && status.Receipt.State == "accepted" {
			if err = invoke(ctx, client, "agents.messages.resume", map[string]any{"agent_id": c.AgentID, "request_id": row.RequestID, "receipt_id": status.Receipt.ID, "channel_source": row.Event}, nil); err != nil {
				return err
			}
		}
	}
	ids := []string{}
	for id, i := range s.Inbox {
		if i.Event.Platform == p && i.State == "pending" && !time.Now().Before(i.RetryAt) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return s.Inbox[ids[i]].CreatedAt.Before(s.Inbox[ids[j]].CreatedAt) })
	for _, id := range ids {
		row := s.Inbox[id]
		handled, reply, commandErr := channelCommand(ctx, sourceBoundClient{Client: client, Source: row.Event}, row.Event.Text, c.AgentID)
		if handled {
			if commandErr != nil {
				reply = "Command not completed: " + commandErr.Error()
			}
			if err = b.update(ctx, func(s *ledger) error {
				delete(s.Inbox, id)
				for part, text := range splitMessage(reply) {
					outputID := key(p, row.RequestID, "command", fmt.Sprint(part))
					if s.Outbox[outputID] == nil {
						s.Outbox[outputID] = &delivery{Platform: p, State: "pending", Part: part, Group: row.RequestID, Output: Outgoing{ConversationID: row.Event.ConversationID, ThreadID: row.Event.ThreadID, Text: text, ClientMessageID: providerMessageID(outputID)}}
					}
				}

				return nil
			}); err != nil {
				return err
			}
			continue
		}
		if !row.Prepared {
			seq := c.ThreadReplies[row.Event.ThreadID]
			if err = b.update(ctx, func(s *ledger) error { s.Inbox[id].Prepared = true; s.Inbox[id].ReplySeq = seq; return nil }); err != nil {
				return err
			}
			row.Prepared = true
			row.ReplySeq = seq
		}
		params := map[string]any{"agent_id": c.AgentID, "request_id": row.RequestID, "prompt": row.Event.Text, "channel_source": row.Event}
		if seq := row.ReplySeq; seq > 0 {
			params["relation"] = map[string]any{"reply_to": map[string]any{"session_id": c.SessionID, "seq": seq}}
		}
		if len(row.Event.Attachments) > 0 {
			if len(row.Attachments) == 0 {
				downloader, ok := driver.(MediaDownloader)
				if !ok {
					return errors.New("messaging attachment download unavailable")
				}
				files, downloadErr := DownloadInputAttachments(ctx, downloader, row.Event.Attachments)
				if downloadErr != nil {
					diagnostic := downloadErr.Error()
					retry := time.Now().Add(30 * time.Second)
					if err = b.update(ctx, func(s *ledger) error { s.Inbox[id].Error = diagnostic; s.Inbox[id].RetryAt = retry; return nil }); err != nil {
						return err
					}
					continue
				}
				if err = b.update(ctx, func(s *ledger) error { s.Inbox[id].Attachments = files; return nil }); err != nil {
					return err
				}
				row.Attachments = files
			}
			params["attachments"] = row.Attachments
		}
		if err = invoke(ctx, client, "agents.send", params, nil); err != nil {
			diagnostic := err.Error()
			retry := time.Now().Add(5 * time.Second)
			if err = b.update(ctx, func(s *ledger) error { s.Inbox[id].Error = diagnostic; s.Inbox[id].RetryAt = retry; return nil }); err != nil {
				return err
			}
			continue
		}
		if err = b.update(ctx, func(s *ledger) error { s.Inbox[id].State = "submitted"; s.Inbox[id].Attachments = nil; return nil }); err != nil {
			return err
		}
	}
	if err = b.collect(ctx, p, client, c); err != nil {
		return err
	}
	return b.sendPending(ctx, p, client, c, driver)
}

type historyRecord struct {
	Seq         int                 `json:"seq"`
	Role        string              `json:"role"`
	Hidden      bool                `json:"hidden"`
	ReadOnly    bool                `json:"read_only"`
	Origin      string              `json:"origin"`
	Content     string              `json:"content"`
	Attachments []HistoryAttachment `json:"attachments,omitempty"`
	Receipt     *struct {
		RequestID string `json:"request_id"`
	} `json:"receipt"`
	Relation *struct {
		Metadata struct {
			TaskLinks []struct {
				TaskID string `json:"task_id"`
			} `json:"task_links"`
		} `json:"metadata"`
		ReplyTo *struct {
			SessionID string `json:"session_id"`
			Seq       int    `json:"seq"`
		} `json:"reply_to"`
	} `json:"relation"`
}

func (b *Bridge) collect(ctx context.Context, p string, client Client, c *connection) error {
	for {
		var page struct {
			Records []historyRecord `json:"records"`
			HasMore bool            `json:"has_more"`
		}
		if err := invoke(ctx, client, "agents.channel_history", map[string]any{"agent_id": c.AgentID, "start_seq": c.Cursor + 1, "limit": 100}, &page); err != nil {
			return err
		}
		taskSources := map[string]int{}
		for _, record := range page.Records {
			if record.Relation == nil {
				continue
			}
			for _, link := range record.Relation.Metadata.TaskLinks {
				if _, seen := taskSources[link.TaskID]; seen {
					continue
				}
				var result struct {
					Task struct {
						Source struct {
							SessionID  string `json:"session_id"`
							MessageSeq int    `json:"message_seq"`
							Binding    string `json:"message_binding"`
						} `json:"source"`
					} `json:"task"`
				}
				if err := invoke(ctx, client, "tasks.get", map[string]any{"task_id": link.TaskID}, &result); err != nil {
					return err
				}
				source := result.Task.Source
				if source.SessionID == c.SessionID && source.Binding == "host_turn" {
					taskSources[link.TaskID] = source.MessageSeq
				} else {
					taskSources[link.TaskID] = 0
				}
			}
		}
		last := c.Cursor
		if err := b.update(ctx, func(s *ledger) error {
			current := s.Connections[p]
			requests := map[string]string{}
			requestRows := map[string]string{}
			for entryID, i := range s.Inbox {
				if i.Event.Platform == p {
					requestRows[i.RequestID] = entryID
					requests[i.RequestID] = i.Event.ThreadID
				}
			}
			for _, r := range page.Records {
				if r.Seq <= current.Cursor {
					continue
				}
				last = r.Seq
				if r.Role == "user" {
					thread := ""
					if r.Receipt != nil {
						thread = requests[r.Receipt.RequestID]
						if entryID := requestRows[r.Receipt.RequestID]; entryID != "" {
							delete(s.Inbox, entryID)
						}
					}
					if thread == "" && r.Relation != nil && r.Relation.ReplyTo != nil && r.Relation.ReplyTo.SessionID == current.SessionID {
						thread = current.Routes[r.Relation.ReplyTo.Seq]
					}
					if thread == "" && r.Relation != nil {
						for _, link := range r.Relation.Metadata.TaskLinks {
							candidate := current.Routes[taskSources[link.TaskID]]
							if candidate != "" {
								if thread != "" && thread != candidate {
									return errors.New("task update spans different messaging threads")
								}
								thread = candidate
							}
						}
					}
					// Plugin-injected context belongs to the current turn, not a new
					// desktop request. Causal task updates still establish their own route.
					if !(r.Hidden && r.ReadOnly && r.Origin == "plugin" && r.Receipt == nil && r.Relation == nil) {
						current.LastThread = thread
					}
					if thread != "" {
						current.Routes[r.Seq] = thread
					}
				}
				if r.Role == "assistant" && !r.Hidden && (strings.TrimSpace(r.Content) != "" || len(r.Attachments) > 0) && current.LastThread != "" {
					chunks := splitMessage(r.Content)
					for part, text := range chunks {
						id := key(p, current.SessionID, fmt.Sprint(r.Seq), "text", fmt.Sprint(part))
						if s.Outbox[id] == nil {
							s.Outbox[id] = &delivery{Platform: p, State: "pending", Sequence: r.Seq, Group: key(p, current.SessionID, fmt.Sprint(r.Seq)), Part: part, Output: Outgoing{ConversationID: current.Binding.ConversationID, ThreadID: current.LastThread, Text: text, ClientMessageID: providerMessageID(id)}}
						}
					}

					for index, ref := range r.Attachments {
						attachmentID := key(p, current.SessionID, fmt.Sprint(r.Seq), fmt.Sprint(index), ref.SHA256)
						if s.Outbox[attachmentID] == nil {
							copy := ref
							s.Outbox[attachmentID] = &delivery{Platform: p, State: "pending", Sequence: r.Seq, Group: key(p, current.SessionID, fmt.Sprint(r.Seq)), Part: len(chunks) + index, Attachment: &copy, Output: Outgoing{ConversationID: current.Binding.ConversationID, ThreadID: current.LastThread, ClientMessageID: providerMessageID(attachmentID)}}
						}
					}
					current.Routes[r.Seq] = current.LastThread
					current.ThreadReplies[current.LastThread] = r.Seq
				}
				current.Cursor = r.Seq
			}
			return nil
		}); err != nil {
			return err
		}
		if last <= c.Cursor || !page.HasMore {
			return nil
		}
		c.Cursor = last
	}
}

// ObserveConnection receives diagnostics from the owned transport; it does not
// enable a connection or confer input authority.
func (b *Bridge) ObserveConnection(platform string, state ConnectionState) {
	switch state.State {
	case "connected", "connecting", "reconnecting", "error":
	default:
		state.State = "error"
	}
	b.setState(b.lifetime, platform, state.State, state.LastError)
}

func providerMessageID(id string) string {
	// Both providers accept a UUID-shaped stable client identity.
	return id[:8] + "-" + id[8:12] + "-4" + id[13:16] + "-8" + id[17:20] + "-" + id[20:32]
}

func (b *Bridge) sendPending(ctx context.Context, p string, client Client, c *connection, driver Driver) error {
	s, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	ids := []string{}
	for id, d := range s.Outbox {
		if d.Platform == p && (d.State == "pending" || d.State == "sending" || d.State == "preparing" || d.State == "uncertain") {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, z := s.Outbox[ids[i]], s.Outbox[ids[j]]
		if a.Sequence == z.Sequence {
			if a.Part != z.Part {
				return a.Part < z.Part
			}
			return ids[i] < ids[j]
		}
		return a.Sequence < z.Sequence
	})
	blocked := map[string]bool{}
	for _, id := range ids {
		if b.options.Current != nil && !b.options.Current() {
			return errors.New("messaging provider generation is no longer selected")
		}
		d := s.Outbox[id]
		group := d.Group
		if group == "" {
			group = fmt.Sprint(d.Sequence)
		}
		if blocked[group] {
			continue
		}
		if time.Now().Before(d.RetryAt) {
			blocked[group] = true
			continue
		}
		if d.State == "sending" || d.State == "preparing" || d.State == "uncertain" {
			// A matching provider receipt can settle an ambiguous send; absence cannot
			// prove the original effect did not happen.
			var receipt Receipt
			var found bool
			if finder, ok := driver.(interface {
				FindReceipt(context.Context, Outgoing) (Receipt, bool, error)
			}); ok && d.Attachment == nil {
				receipt, found, err = finder.FindReceipt(ctx, d.Output)
				if err != nil {
					found = false
				}
			}
			next := time.Now().Add(30 * time.Second)
			if err = b.update(ctx, func(s *ledger) error {
				v := s.Outbox[id]
				v.State = "uncertain"
				v.RetryAt = next
				if found {
					v.State = "delivered"
					v.Receipt = &receipt
					v.Output.Text = ""
					v.Uploaded = nil
					v.Attachment = nil
					v.Error = ""
				}
				return nil
			}); err != nil {
				return err
			}
			if !found {
				blocked[group] = true
			}
			continue
		}
		var attachment Attachment
		var data []byte
		if d.Attachment != nil {
			if d.Uploaded != nil {
				attachment = *d.Uploaded
			} else {
				_, uploadsResource := driver.(*feishuDriver)
				if uploadsResource {
					if err = b.update(ctx, func(s *ledger) error { s.Outbox[id].State = "preparing"; return nil }); err != nil {
						return err
					}
				}
				attachment, data, err = PrepareOutputAttachment(ctx, driver, client, c.AgentID, d.Sequence, *d.Attachment)
				if err != nil {
					state, retry := outputFailure(err)
					if !uploadsResource {
						state = "pending"
						retry = 30 * time.Second
					}
					diagnostic := err.Error()
					next := time.Now().Add(retry)
					if err = b.update(ctx, func(s *ledger) error {
						v := s.Outbox[id]
						v.State = state
						v.Error = diagnostic
						v.RetryAt = next
						return nil
					}); err != nil {
						return err
					}
					continue
				}
				if attachment.ID != "" {
					saved := attachment
					if err = b.update(ctx, func(s *ledger) error { s.Outbox[id].Uploaded = &saved; s.Outbox[id].State = "pending"; return nil }); err != nil {
						return err
					}
				}
			}
		}
		if err = b.update(ctx, func(s *ledger) error { s.Outbox[id].State = "sending"; return nil }); err != nil {
			return err
		}
		if b.options.Current != nil && !b.options.Current() {
			return errors.New("messaging provider generation is no longer selected")
		}
		var receipt Receipt
		var sendErr error
		if d.Attachment == nil {
			receipt, sendErr = driver.Post(ctx, d.Output)
		} else {
			receipt, sendErr = SendOutputAttachment(ctx, driver, d.Output, attachment, data)
		}
		state, retry := outputFailure(sendErr)
		if state != "delivered" {
			blocked[group] = true
		}
		at := time.Now().Add(retry)
		if err = b.update(ctx, func(s *ledger) error {
			v := s.Outbox[id]
			v.State = state
			v.RetryAt = at
			if receipt.MessageID != "" || receipt.AttachmentID != "" {
				v.Receipt = &receipt
			}
			if sendErr != nil {
				v.Error = sendErr.Error()
			} else {
				v.Error = ""
				v.Output.Text = ""
				v.Uploaded = nil
				v.Attachment = nil
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
func outputFailure(err error) (string, time.Duration) {
	if err == nil {
		return "delivered", 0
	}
	var slack *SlackError
	if errors.As(err, &slack) {
		if slack.Uncertain {
			return "uncertain", 30 * time.Second
		}
		if slack.RetryAfter > 0 {
			return "pending", slack.RetryAfter
		}
		return "failed", 0
	}
	var feishu *FeishuError
	if errors.As(err, &feishu) {
		if feishu.Uncertain {
			return "uncertain", 30 * time.Second
		}
		if feishu.Retryable {
			delay := feishu.RetryAfter
			if delay < time.Second {
				delay = 5 * time.Second
			}
			return "pending", delay
		}
		return "failed", 0
	}
	return "uncertain", 30 * time.Second
}

func preview(text string) string {
	runes := []rune(text)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return text
}

func splitMessage(text string) []string {
	runes := []rune(text)
	chunks := []string{}
	for len(runes) > 0 {
		n := min(3000, len(runes))
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

// sourceBoundClient carries the immutable native event only to the host channel
// admission boundary. The host strips it before invoking the Personal plugin.
type sourceBoundClient struct {
	Client
	Source Incoming
}

func (c sourceBoundClient) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	input["channel_source"] = c.Source
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return c.Client.Call(ctx, method, encoded)
}
