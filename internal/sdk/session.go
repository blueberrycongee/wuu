package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

// SessionOptions controls creation of one Wuu session.
type SessionOptions struct {
	Ephemeral bool
}

// ListSessionsOptions optionally narrows persisted sessions to a working tree.
type ListSessionsOptions struct {
	WorkDir string
}

// SessionSnapshot is the server-authoritative summary of an app-server thread.
// Raw contains the complete versioned thread payload.
type SessionSnapshot struct {
	ID             string
	ParentID       string
	Preview        string
	Title          string
	Provider       string
	Model          string
	Variant        string
	Effort         string
	PermissionMode string
	WorkDir        string
	Status         string
	ReadOnly       bool
	Ephemeral      bool
	Pinned         bool
	Archived       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Raw            json.RawMessage
}

// Session is a lightweight handle to an app-server-owned conversation. Session
// is the SDK term for the protocol's canonical thread record.
type Session struct {
	client       *Client
	id           string
	mode         string
	sourceThread string
}

// ID returns the durable thread/session identity.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Snapshot returns the latest authoritative snapshot observed on this client.
func (s *Session) Snapshot() (SessionSnapshot, bool) {
	if s == nil || s.client == nil {
		return SessionSnapshot{}, false
	}
	s.client.mu.RLock()
	defer s.client.mu.RUnlock()
	snapshot, ok := s.client.sessions[s.id]
	snapshot.Raw = cloneRaw(snapshot.Raw)
	return snapshot, ok
}

// Subscribe returns authoritative events scoped to this session.
func (s *Session) Subscribe(ctx context.Context, opts SubscriptionOptions) *Subscription {
	if s == nil || s.client == nil {
		return closedSubscription()
	}
	return s.client.subscribe(ctx, s.id, opts)
}

// NewSession creates and acquires a session on this connection.
func (c *Client) NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := c.rpc.call(ctx, appserver.MethodThreadStart, struct {
		Ephemeral bool `json:"ephemeral,omitempty"`
	}{Ephemeral: opts.Ephemeral}, &result); err != nil {
		return nil, err
	}
	return c.sessionFromRaw(result.Thread, "start", "")
}

// ResumeSession acquires an existing session. An empty id resumes the most
// recently used session according to the app-server contract.
func (c *Client) ResumeSession(ctx context.Context, id string) (*Session, error) {
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := c.rpc.call(ctx, appserver.MethodThreadResume, struct {
		SessionID string `json:"session_id,omitempty"`
	}{SessionID: strings.TrimSpace(id)}, &result); err != nil {
		return nil, err
	}
	return c.sessionFromRaw(result.Thread, "resume", strings.TrimSpace(id))
}

// ForkSession creates a new durable session from an existing session.
func (c *Client) ForkSession(ctx context.Context, id string) (*Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("source session id is required")
	}
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := c.rpc.call(ctx, appserver.MethodThreadFork, struct {
		ThreadID string `json:"thread_id"`
	}{ThreadID: id}, &result); err != nil {
		return nil, err
	}
	return c.sessionFromRaw(result.Thread, "fork", id)
}

// ListSessions returns server-authoritative snapshots without acquiring them.
func (c *Client) ListSessions(ctx context.Context, opts ListSessionsOptions) ([]SessionSnapshot, error) {
	var result struct {
		Threads []json.RawMessage `json:"threads"`
	}
	if err := c.rpc.call(ctx, appserver.MethodThreadList, struct {
		CWD string `json:"cwd,omitempty"`
	}{CWD: strings.TrimSpace(opts.WorkDir)}, &result); err != nil {
		return nil, err
	}
	snapshots := make([]SessionSnapshot, 0, len(result.Threads))
	for _, raw := range result.Threads {
		snapshot, err := decodeSessionSnapshot(raw)
		if err != nil {
			return nil, err
		}
		c.rememberSession(snapshot)
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (c *Client) sessionFromRaw(raw json.RawMessage, mode, sourceThread string) (*Session, error) {
	snapshot, err := decodeSessionSnapshot(raw)
	if err != nil {
		return nil, err
	}
	if snapshot.ID == "" {
		return nil, errors.New("app-server returned a session without an id")
	}
	c.rememberSession(snapshot)
	if mode == "resume" && sourceThread == "" {
		sourceThread = snapshot.ID
	}
	return &Session{client: c, id: snapshot.ID, mode: mode, sourceThread: sourceThread}, nil
}

// Image is a base64-encoded image attachment.
type Image struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Original  bool   `json:"original,omitempty"`
}

// File is a base64-encoded file attachment.
type File struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Filename  string `json:"filename,omitempty"`
}

// ModelSelection changes only this session's model selection, not workspace
// defaults. Empty provider/model and nil variant/effort preserve the selection.
type ModelSelection struct {
	Provider string
	Model    string
	Variant  *string
	Effort   *string
}

// SelectModel applies a persistent session selection before the next Run. The
// server rejects changes while the session is busy or its selection is pinned.
func (s *Session) SelectModel(ctx context.Context, selection ModelSelection) error {
	if s == nil || s.client == nil {
		return errors.New("session is required")
	}
	provider, model := strings.TrimSpace(selection.Provider), strings.TrimSpace(selection.Model)
	if provider == "" && model == "" && selection.Variant == nil && selection.Effort == nil {
		return nil
	}
	if err := s.client.rpc.call(ctx, appserver.MethodConfigModelUpdate, appserver.ConfigModelUpdateParams{
		ThreadID: s.id, Provider: provider, Model: model,
		Variant: selection.Variant, Effort: selection.Effort,
	}, nil); err != nil {
		return err
	}
	// config/model/update returns workspace defaults, not the target thread.
	// Read the authoritative selection without waiting on asynchronous events.
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := s.client.rpc.call(ctx, appserver.MethodThreadResume, appserver.ThreadResumeParams{
		SessionID: s.id, ResponseOnly: true, HistoryPage: true,
	}, &result); err != nil {
		return err
	}
	snapshot, err := decodeSessionSnapshot(result.Thread)
	if err != nil {
		return err
	}
	s.client.rememberSession(snapshot)
	return nil
}

// SendOptions describes one agent invocation. Selection and limit fields below
// record invocation intent; configure the Runtime and call SelectModel before
// Send to apply them. PermissionMode and OutputSchema are enforced by the Run.
type SendOptions struct {
	Prompt         string
	Images         []Image
	Files          []File
	PermissionMode string
	OutputSchema   json.RawMessage

	Provider     string
	Model        string
	Variant      string
	Effort       string
	AgentProfile string
	MaxTurns     int
	Timeout      time.Duration
	NoTools      bool
}

// RunStatus is the durable state of one agent invocation.
type RunStatus string

const (
	RunAccepted    RunStatus = "accepted"
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted"
	RunTimedOut    RunStatus = "timed_out"
	RunCancelled   RunStatus = "cancelled"
)

// Terminal reports whether no more work will be attached to this Run.
func (s RunStatus) Terminal() bool {
	switch s {
	case RunCompleted, RunFailed, RunInterrupted, RunTimedOut, RunCancelled:
		return true
	default:
		return false
	}
}

// RunError is the server's structured terminal failure.
type RunError struct {
	Code       string `json:"code,omitempty"`
	Category   string `json:"category,omitempty"`
	Message    string `json:"message"`
	Provider   string `json:"provider,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

// RunSnapshot is the server-authoritative execution state. Raw contains the
// complete versioned run payload.
type RunSnapshot struct {
	ID          string
	SessionID   string
	Status      RunStatus
	FinalTurnID string
	FinalItemID string
	TracePath   string
	ExitCode    int
	Error       *RunError
	CreatedAt   time.Time
	StartedAt   *time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	Raw         json.RawMessage
}

// Run is a control handle for one invocation.
type Run struct {
	client    *Client
	sessionID string
	id        string
}

func (r *Run) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// Snapshot returns the latest authoritative run state observed by the client.
func (r *Run) Snapshot() (RunSnapshot, bool) {
	if r == nil || r.client == nil {
		return RunSnapshot{}, false
	}
	r.client.mu.RLock()
	defer r.client.mu.RUnlock()
	snapshot, ok := r.client.runs[r.id]
	snapshot = cloneRunSnapshot(snapshot)
	return snapshot, ok
}

// Send starts one app-server Run in this session.
func (s *Session) Send(ctx context.Context, opts SendOptions) (*Run, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("session is required")
	}
	if strings.TrimSpace(opts.Prompt) == "" && len(opts.Images) == 0 && len(opts.Files) == 0 {
		return nil, errors.New("prompt or attachment is required")
	}
	if opts.MaxTurns < 0 {
		return nil, errors.New("max turns must be non-negative")
	}
	mode := s.mode
	if mode == "" {
		mode = "start"
	}
	var permissionMode *string
	if value := strings.TrimSpace(opts.PermissionMode); value != "" {
		permissionMode = &value
	}
	params := struct {
		ThreadID       string          `json:"thread_id"`
		Prompt         string          `json:"prompt"`
		Images         []Image         `json:"images,omitempty"`
		Files          []File          `json:"files,omitempty"`
		PermissionMode *string         `json:"permission_mode,omitempty"`
		Request        runRequest      `json:"request"`
		OutputSchema   json.RawMessage `json:"output_schema,omitempty"`
	}{
		ThreadID: s.id, Prompt: opts.Prompt, Images: opts.Images, Files: opts.Files,
		PermissionMode: permissionMode, OutputSchema: cloneRaw(opts.OutputSchema),
		Request: runRequest{
			Mode: mode, SourceThreadID: s.sourceThread,
			Requested:    runSelection{Provider: strings.TrimSpace(opts.Provider), Model: strings.TrimSpace(opts.Model), Variant: strings.TrimSpace(opts.Variant), Effort: strings.TrimSpace(opts.Effort), PermissionMode: strings.TrimSpace(opts.PermissionMode)},
			AgentProfile: strings.TrimSpace(opts.AgentProfile), MaxTurns: opts.MaxTurns,
			TimeoutMS: opts.Timeout.Milliseconds(), NoTools: opts.NoTools,
		},
	}
	var result struct {
		Run json.RawMessage `json:"run"`
	}
	if err := s.client.rpc.call(ctx, appserver.MethodRunStart, params, &result); err != nil {
		return nil, err
	}
	snapshot, err := decodeRunSnapshot(result.Run)
	if err != nil {
		return nil, err
	}
	s.client.rememberRun(snapshot)
	return &Run{client: s.client, sessionID: s.id, id: snapshot.ID}, nil
}

type runSelection struct {
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Variant        string `json:"variant,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

type runRequest struct {
	Mode           string       `json:"mode"`
	SourceThreadID string       `json:"source_thread_id,omitempty"`
	Requested      runSelection `json:"requested,omitempty"`
	AgentProfile   string       `json:"agent_profile,omitempty"`
	MaxTurns       int          `json:"max_turns,omitempty"`
	TimeoutMS      int64        `json:"timeout_ms,omitempty"`
	NoTools        bool         `json:"no_tools,omitempty"`
}

// Cancel requests interruption and returns the resulting durable run state.
func (r *Run) Cancel(ctx context.Context, reason string) (RunSnapshot, error) {
	if r == nil || r.client == nil {
		return RunSnapshot{}, errors.New("run is required")
	}
	var result struct {
		Run json.RawMessage `json:"run"`
	}
	if err := r.client.rpc.call(ctx, appserver.MethodRunInterrupt, struct {
		RunID  string `json:"run_id"`
		Reason string `json:"reason,omitempty"`
	}{RunID: r.id, Reason: strings.TrimSpace(reason)}, &result); err != nil {
		return RunSnapshot{}, err
	}
	snapshot, err := decodeRunSnapshot(result.Run)
	if err == nil {
		r.client.rememberRun(snapshot)
	}
	return snapshot, err
}

// RunResult combines the terminal execution record with the final message
// carried by the session event stream.
type RunResult struct {
	Run          RunSnapshot
	FinalMessage string
}

// Wait blocks until the Run reaches a terminal state or ctx ends.
func (r *Run) Wait(ctx context.Context) (RunResult, error) {
	if r == nil || r.client == nil {
		return RunResult{}, errors.New("run is required")
	}
	if ctx == nil {
		return RunResult{}, errors.New("context is required")
	}
	subscription := r.client.subscribe(ctx, r.sessionID, SubscriptionOptions{Buffer: 16})
	defer subscription.Close()
	if snapshot, ok := r.Snapshot(); ok && snapshot.Status.Terminal() {
		if result, ready := r.terminalResult(snapshot); ready {
			return result, nil
		}
	}
	for {
		select {
		case event, ok := <-subscription.Events:
			if !ok {
				if ctx.Err() != nil {
					return RunResult{}, ctx.Err()
				}
				if err := subscription.Err(); err != nil {
					return RunResult{}, err
				}
				if snapshot, found := r.Snapshot(); found && snapshot.Status.Terminal() {
					if result, ready := r.terminalResult(snapshot); ready {
						return result, nil
					}
					return RunResult{}, errors.New("app-server event stream closed before final message was recorded")
				}
				return RunResult{}, errors.New("app-server event stream closed before run completed")
			}
			switch event.Method {
			case appserver.NotificationTurnCompleted:
				// rememberEvent records the message before dispatching this event.
				// Re-check the terminal snapshot so a turn/completed notification
				// that arrives after run/updated can finish the wait.
				if snapshot, found := r.Snapshot(); found && snapshot.Status.Terminal() {
					if result, ready := r.terminalResult(snapshot); ready {
						return result, nil
					}
				}
			case appserver.NotificationRunUpdated:
				var payload struct {
					Run json.RawMessage `json:"run"`
				}
				if event.Decode(&payload) != nil {
					continue
				}
				snapshot, err := decodeRunSnapshot(payload.Run)
				if err != nil || snapshot.ID != r.id || !snapshot.Status.Terminal() {
					continue
				}
				if result, ready := r.terminalResult(snapshot); ready {
					return result, nil
				}
			}
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		}
	}
}

func (r *Run) terminalResult(snapshot RunSnapshot) (RunResult, bool) {
	message, messageReady := r.finalMessage(snapshot)
	if snapshot.Status == RunCompleted && snapshot.FinalTurnID != "" && !messageReady {
		return RunResult{}, false
	}
	return RunResult{Run: snapshot, FinalMessage: message}, true
}

func runSnapshotContainsTurn(snapshot RunSnapshot, turnID string) bool {
	var wire struct {
		Turns []struct {
			TurnID string `json:"turn_id"`
		} `json:"turns"`
	}
	if json.Unmarshal(snapshot.Raw, &wire) != nil {
		return false
	}
	for _, turn := range wire.Turns {
		if turn.TurnID == turnID {
			return true
		}
	}
	return false
}

func (r *Run) finalMessage(run RunSnapshot) (string, bool) {
	r.client.mu.RLock()
	if content, ok := r.client.runTexts[r.id]; ok {
		r.client.mu.RUnlock()
		return content, true
	}
	session := r.client.sessions[r.sessionID]
	r.client.mu.RUnlock()
	var wire struct {
		Turns []struct {
			ID    string `json:"id"`
			Items []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"items"`
		} `json:"turns"`
	}
	if json.Unmarshal(session.Raw, &wire) != nil {
		return "", false
	}
	for turnIndex := len(wire.Turns) - 1; turnIndex >= 0; turnIndex-- {
		turn := wire.Turns[turnIndex]
		if run.FinalTurnID != "" && turn.ID != run.FinalTurnID {
			continue
		}
		for itemIndex := len(turn.Items) - 1; itemIndex >= 0; itemIndex-- {
			item := turn.Items[itemIndex]
			if run.FinalItemID != "" && item.ID == run.FinalItemID {
				return item.Text, true
			}
			if run.FinalItemID == "" && item.Type == "agent_message" && item.Text != "" {
				return item.Text, true
			}
		}
	}
	return "", false
}

func decodeSessionSnapshot(raw json.RawMessage) (SessionSnapshot, error) {
	var wire struct {
		ID             string    `json:"id"`
		ParentID       string    `json:"parent_id,omitempty"`
		Preview        string    `json:"preview"`
		Title          string    `json:"title,omitempty"`
		ModelProvider  string    `json:"model_provider"`
		Model          string    `json:"model"`
		ModelVariant   string    `json:"model_variant"`
		ModelEffort    string    `json:"model_effort"`
		PermissionMode string    `json:"permission_mode"`
		CWD            string    `json:"cwd"`
		Status         string    `json:"status"`
		ReadOnly       bool      `json:"read_only,omitempty"`
		Ephemeral      bool      `json:"ephemeral,omitempty"`
		Pinned         bool      `json:"pinned,omitempty"`
		Archived       bool      `json:"archived,omitempty"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return SessionSnapshot{}, fmt.Errorf("decode session snapshot: %w", err)
	}
	return SessionSnapshot{
		ID: wire.ID, ParentID: wire.ParentID, Preview: wire.Preview, Title: wire.Title,
		Provider: wire.ModelProvider, Model: wire.Model, Variant: wire.ModelVariant, Effort: wire.ModelEffort,
		PermissionMode: wire.PermissionMode, WorkDir: wire.CWD, Status: wire.Status,
		ReadOnly: wire.ReadOnly, Ephemeral: wire.Ephemeral, Pinned: wire.Pinned, Archived: wire.Archived,
		CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt, Raw: cloneRaw(raw),
	}, nil
}

func decodeRunSnapshot(raw json.RawMessage) (RunSnapshot, error) {
	var wire struct {
		ID       string    `json:"id"`
		ThreadID string    `json:"thread_id"`
		Status   RunStatus `json:"status"`
		Result   *struct {
			FinalTurnID string `json:"final_turn_id,omitempty"`
			FinalItemID string `json:"final_item_id,omitempty"`
			TracePath   string `json:"trace_path,omitempty"`
			ExitCode    int    `json:"exit_code,omitempty"`
		} `json:"result,omitempty"`
		Error       *RunError  `json:"error,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
		StartedAt   *time.Time `json:"started_at,omitempty"`
		UpdatedAt   time.Time  `json:"updated_at"`
		CompletedAt *time.Time `json:"completed_at,omitempty"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return RunSnapshot{}, fmt.Errorf("decode run snapshot: %w", err)
	}
	snapshot := RunSnapshot{
		ID: wire.ID, SessionID: wire.ThreadID, Status: wire.Status, Error: wire.Error,
		CreatedAt: wire.CreatedAt, StartedAt: wire.StartedAt, UpdatedAt: wire.UpdatedAt,
		CompletedAt: wire.CompletedAt, Raw: cloneRaw(raw),
	}
	if wire.Result != nil {
		snapshot.FinalTurnID = wire.Result.FinalTurnID
		snapshot.FinalItemID = wire.Result.FinalItemID
		snapshot.TracePath = wire.Result.TracePath
		snapshot.ExitCode = wire.Result.ExitCode
	}
	return snapshot, nil
}

func cloneRunSnapshot(snapshot RunSnapshot) RunSnapshot {
	snapshot.Raw = cloneRaw(snapshot.Raw)
	if snapshot.StartedAt != nil {
		startedAt := *snapshot.StartedAt
		snapshot.StartedAt = &startedAt
	}
	if snapshot.CompletedAt != nil {
		completedAt := *snapshot.CompletedAt
		snapshot.CompletedAt = &completedAt
	}
	if snapshot.Error != nil {
		runError := *snapshot.Error
		snapshot.Error = &runError
	}
	return snapshot
}
