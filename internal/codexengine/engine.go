package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Engine is the codex agent engine: the Codex CLI app-server hosted as an
// external process. It implements agentengine.Factory and
// agentengine.ThreadBoundFactory so the app-server can route threads bound
// to engine_id "codex" through it.
type Engine struct {
	host *Host
	// RequestApproval bridges Codex's reverse-RPC approval requests to Wuu's
	// host-owned interaction broker. Nil declines every request.
	RequestApproval agentengine.ApprovalHandler
}

// NewEngine builds the codex engine around a host (binary discovery +
// app-server lifecycle).
func NewEngine(host *Host) *Engine {
	return &Engine{host: host}
}

// Descriptor describes the codex engine.
func (e *Engine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{
		ID:      agentengine.EngineID("codex"),
		Version: "1",
		Capabilities: []string{
			"native-tool-loop",
			"native-session-resume",
			"approval-requests",
		},
	}, nil
}

// Open starts a codex thread for a new conversation.
func (e *Engine) Open(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID: req.ThreadID,
		rootDir:  req.RootDir,
	})
}

// Resume reopens an existing codex thread by its native reference.
func (e *Engine) Resume(ctx context.Context, req agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID:    req.ThreadID,
		rootDir:     req.RootDir,
		externalRef: req.ExternalSessionRef,
	})
}

// SessionForThread binds the codex engine to an existing thread runtime (the
// live app-server path). The binding carries the persisted codex thread id;
// an empty ref means the first turn creates the codex thread lazily.
func (e *Engine) SessionForThread(ctx context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID:       binding.ThreadID,
		rootDir:        binding.RootDir,
		model:          binding.Model,
		effort:         ReasoningEffort(binding.Effort),
		permissionMode: binding.PermissionMode,
		instructions:   binding.Instructions,
		mcpServers:     binding.MCPServers,
		externalRef:    binding.ExternalRef,
		persistRef:     binding.PersistRef,
		approval:       binding.RequestApproval,
	})
}

type sessionOptions struct {
	threadID       string
	rootDir        string
	model          string
	effort         ReasoningEffort
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer
	externalRef    string
	persistRef     func(string) error
	approval       agentengine.ApprovalHandler
}

func (e *Engine) newSession(ctx context.Context, opts sessionOptions) (agentengine.Session, error) {
	if e == nil || e.host == nil {
		return nil, errors.New("codex engine is not configured")
	}
	client, err := e.host.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	approval := opts.approval
	if approval == nil {
		approval = e.RequestApproval
	}
	return &Session{
		engine:         e,
		client:         client,
		threadID:       opts.threadID,
		rootDir:        opts.rootDir,
		model:          opts.model,
		effort:         opts.effort,
		permissionMode: opts.permissionMode,
		instructions:   opts.instructions,
		mcpServers:     append([]agentengine.MCPServer(nil), opts.mcpServers...),
		ref:            opts.externalRef,
		persist:        opts.persistRef,
		approval:       approval,
	}, nil
}

// Session is one wuu thread's handle on a codex thread. The codex thread is
// created lazily on the first turn (thread/start) and its id is persisted via
// the binding's PersistRef; later turns run turn/start directly on the shared
// app-server, or thread/resume first if the app-server restarted.
type Session struct {
	engine *Engine
	client *Client

	threadID       string
	rootDir        string
	model          string
	effort         ReasoningEffort
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer

	mu          sync.Mutex
	ref         string
	persist     func(string) error
	approval    agentengine.ApprovalHandler
	approvalCtx context.Context
}

// RunTurn starts a codex turn and translates its notifications into Wuu
// stream events. It blocks until turn/completed or the context is canceled.
func (s *Session) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	if s == nil || s.client == nil {
		return agentengine.TurnResult{}, errors.New("codex session has no app-server client")
	}
	if err := ctx.Err(); err != nil {
		return agentengine.TurnResult{}, err
	}
	s.mu.Lock()
	s.approvalCtx = ctx
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.approvalCtx = nil
		s.mu.Unlock()
	}()
	// First turn: create the codex thread and persist its id so later turns
	// (and later app-server processes) resume the same native session.
	if err := s.ensureThread(ctx); err != nil {
		return agentengine.TurnResult{}, err
	}
	unregister := s.registerApprovalHandlers()
	defer unregister()
	prompt := turnPrompt(input.History)
	// Subscribe before turn/start: the app-server streams notifications
	// immediately after responding, so a late subscription would drop them.
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(s.client, sink, done)
	defer sub.close()
	turnResp, err := s.startTurn(ctx, prompt)
	if err != nil {
		return agentengine.TurnResult{}, err
	}
	turnID := turnResp.Turn.ID
	sub.setTurnID(turnID)

	select {
	case out := <-done:
		return out.result, out.err
	case <-ctx.Done():
		_ = s.interrupt(context.Background(), turnID)
		select {
		case out := <-done:
			return out.result, out.err
		default:
			return agentengine.TurnResult{}, ctx.Err()
		}
	}
}

func (s *Session) registerApprovalHandlers() func() {
	if s == nil || s.client == nil {
		return func() {}
	}
	remove := []func(){
		s.client.OnRequest(MethodCommandExecApproval, s.handleCommandApproval),
		s.client.OnRequest(MethodFileChangeApproval, s.handleFileChangeApproval),
		s.client.OnRequest(MethodPermissionsApproval, s.handlePermissionsApproval),
	}
	return func() {
		for _, fn := range remove {
			fn()
		}
	}
}

func (s *Session) approvalDecision(ctx context.Context, request agentengine.ApprovalRequest) string {
	if s == nil || s.approval == nil {
		return string(DecisionDecline)
	}
	s.mu.Lock()
	if s.approvalCtx != nil {
		ctx = s.approvalCtx
	}
	s.mu.Unlock()
	decision, err := s.approval(ctx, request)
	if err != nil {
		return string(DecisionDecline)
	}
	switch decision {
	case agentengine.ApprovalAccept:
		return string(DecisionAccept)
	case agentengine.ApprovalAcceptForSession:
		return string(DecisionAcceptForSession)
	case agentengine.ApprovalCancel:
		return string(DecisionCancel)
	default:
		return string(DecisionDecline)
	}
}

func (s *Session) handleCommandApproval(raw json.RawMessage) (any, error) {
	var params CommandExecutionApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if !s.matchesThread(params.ThreadID) {
		return nil, errRequestNotForSession
	}
	return ApprovalDecisionResponse{Decision: s.approvalDecision(context.Background(), agentengine.ApprovalRequest{
		Kind: agentengine.ApprovalCommandExecution, EngineID: agentengine.EngineID("codex"),
		ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID,
		Command: params.Command, CWD: params.CWD, Reason: params.Reason,
	})}, nil
}

func (s *Session) handleFileChangeApproval(raw json.RawMessage) (any, error) {
	var params FileChangeApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if !s.matchesThread(params.ThreadID) {
		return nil, errRequestNotForSession
	}
	return ApprovalDecisionResponse{Decision: s.approvalDecision(context.Background(), agentengine.ApprovalRequest{
		Kind: agentengine.ApprovalFileChange, EngineID: agentengine.EngineID("codex"),
		ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID,
		FilePath: params.FilePath, Reason: params.Reason,
	})}, nil
}

func (s *Session) handlePermissionsApproval(raw json.RawMessage) (any, error) {
	var params PermissionsApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if !s.matchesThread(params.ThreadID) {
		return nil, errRequestNotForSession
	}
	request := agentengine.ApprovalRequest{Kind: agentengine.ApprovalPermissions, EngineID: agentengine.EngineID("codex"), ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID, Reason: params.Reason, Permissions: params.Permissions}
	decision := s.approvalDecision(context.Background(), request)
	scope := ""
	switch decision {
	case string(DecisionAccept):
		scope = "turn"
	case string(DecisionAcceptForSession):
		scope = "session"
	default:
		return PermissionsApprovalResponse{Permissions: nil, Scope: ""}, nil
	}
	return PermissionsApprovalResponse{Permissions: params.Permissions, Scope: scope}, nil
}

func (s *Session) matchesThread(threadID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	ref := s.ref
	s.mu.Unlock()
	return strings.TrimSpace(ref) != "" && strings.TrimSpace(ref) == strings.TrimSpace(threadID)
}

// ensureThread runs thread/start once and persists the native thread id.
func (s *Session) ensureThread(ctx context.Context) error {
	s.mu.Lock()
	ref := s.ref
	s.mu.Unlock()
	sandbox, approval, _ := codexPermissionSettings(s.permissionMode)
	config := s.threadConfig()
	if ref != "" {
		var resp ThreadResumeResponse
		if err := s.client.Request(ctx, MethodThreadResume, ThreadResumeParams{
			ThreadID: ref, Model: s.model, CWD: s.rootDir,
			ApprovalPolicy: approval, Sandbox: sandbox, Config: config,
			DeveloperInstructs: strings.TrimSpace(s.instructions),
		}, &resp); err != nil {
			return fmt.Errorf("codex thread/resume: %w", err)
		}
		return nil
	}
	var resp ThreadStartResponse
	err := s.client.Request(ctx, MethodThreadStart, ThreadStartParams{
		Model:              s.model,
		CWD:                s.rootDir,
		DeveloperInstructs: strings.TrimSpace(s.instructions),
		ApprovalPolicy:     approval,
		Sandbox:            sandbox,
		Config:             config,
	}, &resp)
	if err != nil {
		return fmt.Errorf("codex thread/start: %w", err)
	}
	ref = resp.Thread.ID
	if strings.TrimSpace(ref) == "" {
		return errors.New("codex thread/start returned an empty thread id")
	}
	if s.persist != nil {
		if err := s.persist(ref); err != nil {
			// The native thread exists but we could not record it; fail the
			// turn rather than orphan it silently.
			return fmt.Errorf("persist codex thread reference: %w", err)
		}
	}
	s.mu.Lock()
	s.ref = ref
	s.mu.Unlock()
	return nil
}

func (s *Session) threadConfig() map[string]any {
	config := map[string]any{}
	if len(s.mcpServers) > 0 {
		servers := make(map[string]any, len(s.mcpServers))
		for _, server := range s.mcpServers {
			if strings.TrimSpace(server.Name) != "" && strings.TrimSpace(server.URL) != "" {
				servers[server.Name] = map[string]any{"url": server.URL}
			}
		}
		if len(servers) > 0 {
			config["mcp_servers"] = servers
		}
	}
	return config
}

func (s *Session) startTurn(ctx context.Context, prompt string) (TurnStartResponse, error) {
	var resp TurnStartResponse
	_, approval, sandboxPolicy := codexPermissionSettings(s.permissionMode)
	if sandboxPolicy.Type == SandboxPolicyWorkspaceWrite && strings.TrimSpace(s.rootDir) != "" {
		sandboxPolicy.WritableRoots = []string{s.rootDir}
	}
	err := s.client.Request(ctx, MethodTurnStart, TurnStartParams{
		ThreadID:        s.ref,
		Input:           []UserInput{{Type: "text", Text: prompt}},
		Model:           s.model,
		ReasoningEffort: s.effort,
		ApprovalPolicy:  approval,
		SandboxPolicy:   &sandboxPolicy,
	}, &resp)
	return resp, err
}

func codexPermissionSettings(mode string) (SandboxMode, ApprovalPolicy, SandboxPolicy) {
	switch strings.TrimSpace(mode) {
	case "read_only":
		return SandboxReadOnly, ApprovalNever, SandboxPolicy{Type: SandboxPolicyReadOnly}
	case "unconfined":
		return SandboxDangerFull, ApprovalNever, SandboxPolicy{Type: SandboxPolicyDangerFull}
	default:
		return SandboxWorkspaceWrite, ApprovalOnRequest, SandboxPolicy{Type: SandboxPolicyWorkspaceWrite}
	}
}

func (s *Session) interrupt(ctx context.Context, turnID string) error {
	if s.ref == "" {
		return agentengine.ErrNoActiveTurn
	}
	var out map[string]any
	return s.client.Request(ctx, MethodTurnInterrupt, TurnInterruptParams{
		ThreadID: s.ref,
		TurnID:   turnID,
	}, &out)
}

// Interrupt cancels the in-flight turn via turn/interrupt.
func (s *Session) Interrupt(ctx context.Context, reason string) error {
	if s == nil {
		return errors.New("codex session is nil")
	}
	s.mu.Lock()
	ref := s.ref
	s.mu.Unlock()
	if ref == "" {
		return agentengine.ErrNoActiveTurn
	}
	return s.client.Request(ctx, MethodTurnInterrupt, TurnInterruptParams{
		ThreadID: ref,
		TurnID:   "",
	}, &map[string]any{})
}

// Close releases the client reference held by this session. The shared
// app-server stays alive while other threads use it.
func (s *Session) Close(context.Context) error {
	if s == nil || s.engine == nil || s.engine.host == nil {
		return nil
	}
	s.engine.host.Release()
	return nil
}

// turnPrompt extracts the user's latest message as the turn input.
func turnPrompt(history []providers.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			return msg.Content
		}
	}
	return ""
}

// turnOutcome carries the converted loop result and terminal error.
type turnOutcome struct {
	result agentengine.TurnResult
	err    error
}

// turnNotification is one queued notification with its method name (the
// client dispatches by method; the subscription needs it to translate).
type turnNotification struct {
	method string
	params json.RawMessage
}

type codexAgentItem struct {
	Type              string                         `json:"type"`
	Tool              string                         `json:"tool"`
	Status            string                         `json:"status"`
	ReceiverThreadIDs []string                       `json:"receiverThreadIds"`
	AgentsStates      map[string]codexAgentItemState `json:"agentsStates"`
	Kind              string                         `json:"kind"`
	AgentThreadID     string                         `json:"agentThreadId"`
	AgentPath         string                         `json:"agentPath"`
}

// codexStreamItem is the provider-facing subset of Codex v2 ThreadItem. Codex
// executes these tools itself; translating their item lifecycle into Wuu's
// ordinary stream events lets the existing conversation UI render them without
// a Codex-specific message path.
type codexStreamItem struct {
	ID                string          `json:"id"`
	Type              string          `json:"type"`
	Status            string          `json:"status"`
	Text              string          `json:"text"`
	Phase             string          `json:"phase"`
	Command           string          `json:"command"`
	CWD               string          `json:"cwd"`
	CommandActions    json.RawMessage `json:"commandActions"`
	AggregatedOutput  *string         `json:"aggregatedOutput"`
	ExitCode          *int            `json:"exitCode"`
	Server            string          `json:"server"`
	Tool              string          `json:"tool"`
	Namespace         string          `json:"namespace"`
	Arguments         json.RawMessage `json:"arguments"`
	Result            json.RawMessage `json:"result"`
	Error             json.RawMessage `json:"error"`
	Query             string          `json:"query"`
	Action            json.RawMessage `json:"action"`
	Changes           json.RawMessage `json:"changes"`
	ContentItems      json.RawMessage `json:"contentItems"`
	Success           *bool           `json:"success"`
	Summary           []string        `json:"summary"`
	Content           []string        `json:"content"`
	ReceiverThreadIDs []string        `json:"receiverThreadIds"`
	Prompt            string          `json:"prompt"`
	Model             string          `json:"model"`
	ReasoningEffort   string          `json:"reasoningEffort"`
}

type codexAgentItemState struct {
	Status string `json:"status"`
}

// turnSubscription translates the notifications of one codex turn into Wuu
// stream events and signals completion.
type turnSubscription struct {
	client *Client
	turnID string
	sink   agentengine.EventSink
	done   chan turnOutcome

	events          chan turnNotification
	mu              sync.Mutex
	closed          bool
	unsubs          []func()
	labels          map[string]string
	tools           map[string]struct{}
	reasoning       map[string]struct{}
	lastAgentText   string
	lastAgentPhase  providers.MessagePhase
	lastAgentItemID string
}

func newTurnSubscription(client *Client, sink agentengine.EventSink, done chan turnOutcome) *turnSubscription {
	sub := &turnSubscription{
		client:    client,
		sink:      sink,
		done:      done,
		events:    make(chan turnNotification, 512),
		labels:    make(map[string]string),
		tools:     make(map[string]struct{}),
		reasoning: make(map[string]struct{}),
	}
	handlers := map[string]struct{}{
		NotifyAgentMessageDelta:     {},
		NotifyReasoningTextDelta:    {},
		NotifyReasoningSummaryAdded: {},
		NotifyReasoningSummaryDelta: {},
		NotifyItemStarted:           {},
		NotifyItemCompleted:         {},
		NotifyTokenUsageUpdated:     {},
		NotifyTurnCompleted:         {},
		NotifyError:                 {},
	}
	for method := range handlers {
		sub.unsubs = append(sub.unsubs, client.OnNotification(method, func(params json.RawMessage) {
			sub.enqueue(turnNotification{method: method, params: params})
		}))
	}
	return sub
}

// setTurnID fixes the turn the subscription filters for and starts the
// consume loop. Notifications that arrived before the id was known (between
// subscription and turn/start response) stay buffered and are filtered once
// the id is set.
func (sub *turnSubscription) setTurnID(turnID string) {
	sub.turnID = turnID
	go sub.run()
}

func (sub *turnSubscription) enqueue(notification turnNotification) {
	select {
	case sub.events <- notification:
	default:
		// A slow sink must not wedge the app-server; drop the notification
		// and let turn/completed reconcile the final state.
	}
}

func (sub *turnSubscription) close() {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return
	}
	sub.closed = true
	unsubs := sub.unsubs
	sub.unsubs = nil
	sub.mu.Unlock()
	for _, unsub := range unsubs {
		unsub()
	}
}

// run consumes notifications until turn/completed or a terminal error, then
// reports the outcome. Events from other turns on the shared app-server are
// filtered by turn id.
func (sub *turnSubscription) run() {
	var (
		text         strings.Builder
		reasoning    strings.Builder
		usage        providers.TokenUsage
		reconnecting bool
	)
	for notification := range sub.events {
		if sub.turnID == "" {
			// No turn id (should not happen); drop everything.
			continue
		}
		var envelope struct {
			TurnID       string `json:"turnId"`
			ItemID       string `json:"itemId"`
			Delta        string `json:"delta"`
			SummaryIndex int    `json:"summaryIndex"`
			Turn         struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
			Message   string `json:"message"`
			WillRetry bool   `json:"willRetry"`
			Error     *struct {
				Message string `json:"message"`
			} `json:"error"`
			TokenUsage *struct {
				Total TokenUsageBreakdown `json:"total"`
				Last  TokenUsageBreakdown `json:"last"`
			} `json:"tokenUsage"`
			Item json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(notification.params, &envelope); err != nil {
			continue
		}
		// turn/completed carries the turn id nested; all other notifications
		// carry it top-level.
		turnID := envelope.TurnID
		if turnID == "" && envelope.Turn.ID != "" {
			turnID = envelope.Turn.ID
		}
		if turnID != sub.turnID {
			continue
		}
		if reconnecting {
			switch notification.method {
			case NotifyAgentMessageDelta, NotifyReasoningTextDelta, NotifyReasoningSummaryDelta, NotifyItemStarted, NotifyItemCompleted:
				sub.emit(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseConnected}})
				reconnecting = false
			}
		}
		switch notification.method {
		case NotifyAgentMessageDelta:
			if envelope.Delta != "" {
				text.WriteString(envelope.Delta)
				sub.emit(providers.StreamEvent{
					Type:           providers.EventContentDelta,
					Content:        envelope.Delta,
					ProviderItemID: envelope.ItemID,
				})
			}
		case NotifyReasoningTextDelta, NotifyReasoningSummaryDelta:
			if envelope.Delta != "" {
				reasoning.WriteString(envelope.Delta)
				sub.reasoning[envelope.ItemID] = struct{}{}
				sub.emit(providers.StreamEvent{
					Type:           providers.EventThinkingDelta,
					Content:        envelope.Delta,
					ProviderItemID: envelope.ItemID,
				})
			}
		case NotifyReasoningSummaryAdded:
			if envelope.SummaryIndex > 0 {
				reasoning.WriteString("\n\n")
				sub.reasoning[envelope.ItemID] = struct{}{}
				sub.emit(providers.StreamEvent{
					Type:           providers.EventThinkingDelta,
					Content:        "\n\n",
					ProviderItemID: envelope.ItemID,
				})
			}
		case NotifyItemStarted, NotifyItemCompleted:
			sub.emitAgentActivities(envelope.Item)
			sub.emitItem(notification.method, envelope.Item)
		case NotifyTokenUsageUpdated:
			if envelope.TokenUsage != nil {
				// "last" is this turn's increment; "total" is the thread
				// lifetime cumulative. Convert to wuu's TokenUsage shape:
				// uncached input, output incl. reasoning, cached read.
				last := envelope.TokenUsage.Last
				usage.InputTokens += last.InputTokens - last.CachedInputTokens
				usage.OutputTokens += last.OutputTokens + last.ReasoningOutputTokens
				usage.CacheReadTokens += last.CachedInputTokens
				sub.emit(providers.StreamEvent{Type: providers.EventUsage, Usage: &usage})
			}
		case NotifyError:
			lastErr := errors.New("codex app-server reported an error")
			if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
				lastErr = errors.New(strings.TrimSpace(envelope.Error.Message))
			}
			if envelope.WillRetry {
				// Codex owns recovery and may keep executing this turn. Ending
				// our subscription here would lose its eventual output.
				reconnecting = true
				sub.emit(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{
					Phase: providers.StreamPhaseReconnecting, Reason: lastErr.Error(),
				}})
				continue
			}
			sub.emit(providers.StreamEvent{Type: providers.EventError, Error: lastErr})
			if reconnecting {
				sub.emit(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseFailed, Reason: lastErr.Error()}})
			}
			sub.finish(agent.LoopResult{}, lastErr)
			return
		case NotifyTurnCompleted:
			status := envelope.Turn.Status
			finishReason := providers.FinishReasonStop
			if status != "" && status != "completed" {
				finishReason = providers.FinishReasonError
			}
			if reconnecting {
				phase := providers.StreamPhaseConnected
				if finishReason == providers.FinishReasonError {
					phase = providers.StreamPhaseFailed
				}
				sub.emit(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{Phase: phase}})
			}
			sub.emit(providers.StreamEvent{
				Type:         providers.EventDone,
				FinishReason: finishReason,
				StopReason:   status,
			})
			content := text.String()
			if strings.TrimSpace(sub.lastAgentText) != "" {
				content = sub.lastAgentText
			}
			result := agent.LoopResult{
				Content:         strings.TrimSpace(content),
				FinishReason:    finishReason,
				StopReason:      status,
				InputTokens:     usage.InputTokens,
				OutputTokens:    usage.OutputTokens,
				CacheReadTokens: usage.CacheReadTokens,
				NewMessages: []providers.ChatMessage{{
					Role:             "assistant",
					Content:          content,
					ReasoningContent: reasoning.String(),
					Phase:            sub.lastAgentPhase,
					ProviderItemID:   sub.lastAgentItemID,
				}},
			}
			sub.finish(result, nil)
			return
		}
	}
	// Stream closed without a terminal notification.
	sub.finish(agent.LoopResult{}, errors.New("codex turn stream closed before completion"))
}

func (sub *turnSubscription) emitItem(method string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var item codexStreamItem
	if err := json.Unmarshal(raw, &item); err != nil || strings.TrimSpace(item.ID) == "" {
		return
	}
	if item.Type == "agentMessage" {
		if method != NotifyItemCompleted || strings.TrimSpace(item.Text) == "" {
			return
		}
		phase := providers.NormalizeMessagePhase(item.Phase)
		sub.lastAgentText = item.Text
		sub.lastAgentPhase = phase
		sub.lastAgentItemID = item.ID
		// Older Codex builds can omit phase. Without that semantic signal an
		// agentMessage may be commentary followed by more tools, so leave the live
		// item open and reconcile it at turn/completed rather than guessing.
		if phase == "" {
			return
		}
		sub.emit(providers.StreamEvent{
			Type: providers.EventMessage,
			Message: &providers.ChatMessage{
				Role:           "assistant",
				Content:        item.Text,
				Phase:          phase,
				ProviderItemID: item.ID,
			},
		})
		return
	}

	if item.Type == "reasoning" {
		text := strings.Join(item.Summary, "\n\n")
		if text == "" {
			text = strings.Join(item.Content, "\n\n")
		}
		if text != "" {
			sub.reasoning[item.ID] = struct{}{}
			sub.emit(providers.StreamEvent{
				Type:           providers.EventThinkingReplace,
				Content:        text,
				ProviderItemID: item.ID,
			})
		}
		if method == NotifyItemCompleted {
			if _, ok := sub.reasoning[item.ID]; ok {
				sub.emit(providers.StreamEvent{Type: providers.EventThinkingDone, ProviderItemID: item.ID})
				delete(sub.reasoning, item.ID)
			}
		}
		return
	}

	call, ok := codexToolCall(item)
	if !ok {
		return
	}
	_, started := sub.tools[item.ID]
	if !started {
		sub.tools[item.ID] = struct{}{}
		sub.emit(providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call})
	}
	if method != NotifyItemCompleted {
		return
	}
	delete(sub.tools, item.ID)
	sub.emit(providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &call,
		ToolResult: codexToolResult(item),
	})
}

func codexToolCall(item codexStreamItem) (providers.ToolCall, bool) {
	call := providers.ToolCall{ID: item.ID, ProviderItemID: item.ID}
	var arguments any
	switch item.Type {
	case "commandExecution":
		call.Name = "exec"
		arguments = map[string]any{
			"command":        item.Command,
			"cwd":            item.CWD,
			"commandActions": rawJSONValue(item.CommandActions),
		}
	case "mcpToolCall":
		call.Name = "mcp:" + item.Server + ":" + item.Tool
		arguments = rawJSONValue(item.Arguments)
	case "dynamicToolCall":
		call.Name = "dynamic:" + item.Tool
		if strings.TrimSpace(item.Namespace) != "" {
			call.Name = "dynamic:" + item.Namespace + ":" + item.Tool
		}
		arguments = rawJSONValue(item.Arguments)
	case "webSearch":
		call.Name = "web_search"
		arguments = map[string]any{"query": item.Query, "action": rawJSONValue(item.Action)}
	case "fileChange":
		call.Name = "apply_patch"
		arguments = map[string]any{"changes": rawJSONValue(item.Changes)}
	case "collabAgentToolCall":
		call.Name = "codex_collab_" + item.Tool
		arguments = map[string]any{
			"receiverThreadIds": item.ReceiverThreadIDs,
			"prompt":            item.Prompt,
			"model":             item.Model,
			"reasoningEffort":   item.ReasoningEffort,
		}
	default:
		return providers.ToolCall{}, false
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		encoded = []byte("{}")
	}
	call.Arguments = string(encoded)
	return call, true
}

func codexToolResult(item codexStreamItem) string {
	var result string
	switch item.Type {
	case "commandExecution":
		if item.AggregatedOutput != nil {
			result = *item.AggregatedOutput
		}
		if result == "" && item.ExitCode != nil {
			result = fmt.Sprintf("Exit %d", *item.ExitCode)
		}
	case "mcpToolCall":
		if rawJSONPresent(item.Error) {
			result = compactJSON(item.Error)
		} else {
			result = compactJSON(item.Result)
		}
	case "dynamicToolCall":
		result = compactJSON(item.ContentItems)
	case "fileChange":
		result = compactJSON(item.Changes)
	case "webSearch":
		result = "done"
	case "collabAgentToolCall":
		result = item.Status
	}
	if strings.TrimSpace(result) == "" {
		result = item.Status
	}
	if strings.TrimSpace(result) == "" {
		result = "completed"
	}
	return result
}

func rawJSONValue(raw json.RawMessage) any {
	if !rawJSONPresent(raw) {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func rawJSONPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}" && trimmed != "[]"
}

func compactJSON(raw json.RawMessage) string {
	if !rawJSONPresent(raw) {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func (sub *turnSubscription) emitAgentActivities(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var item codexAgentItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}
	switch item.Type {
	case "subAgentActivity":
		id := strings.TrimSpace(item.AgentThreadID)
		if id == "" {
			return
		}
		state := providers.AgentActivityRunning
		if item.Kind == "interrupted" {
			state = providers.AgentActivityWaiting
		}
		sub.emitAgentActivity(id, codexAgentLabel(item.AgentPath), state)
	case "collabAgentToolCall":
		if len(item.AgentsStates) > 0 {
			ids := make([]string, 0, len(item.AgentsStates))
			for id := range item.AgentsStates {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				sub.emitAgentActivity(id, "Codex agent", codexAgentState(item.AgentsStates[id].Status))
			}
			return
		}
		state, ok := codexCollabFallbackState(item.Tool, item.Status)
		if !ok {
			return
		}
		for _, id := range item.ReceiverThreadIDs {
			if strings.TrimSpace(id) != "" {
				sub.emitAgentActivity(id, "Codex agent", state)
			}
		}
	}
}

func (sub *turnSubscription) emitAgentActivity(id, label string, state providers.AgentActivityState) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	label = strings.TrimSpace(label)
	if existing := sub.labels[id]; existing != "" && (label == "" || label == "Codex agent") {
		label = existing
	}
	if label == "" {
		label = "Codex agent"
	}
	if label != "Codex agent" {
		sub.labels[id] = label
	}
	sub.emit(providers.StreamEvent{
		Type: providers.EventAgentActivity,
		AgentActivity: &providers.AgentActivity{
			ID:     id,
			Engine: "codex",
			Label:  label,
			State:  state,
		},
	})
}

func codexAgentState(status string) providers.AgentActivityState {
	switch status {
	case "pendingInit":
		return providers.AgentActivityQueued
	case "interrupted":
		return providers.AgentActivityWaiting
	case "completed", "shutdown":
		return providers.AgentActivityCompleted
	case "errored", "notFound":
		return providers.AgentActivityFailed
	default:
		return providers.AgentActivityRunning
	}
}

func codexCollabFallbackState(tool, status string) (providers.AgentActivityState, bool) {
	if status == "failed" {
		return providers.AgentActivityFailed, true
	}
	if tool == "closeAgent" && status == "completed" {
		return providers.AgentActivityCompleted, true
	}
	switch tool {
	case "spawnAgent", "resumeAgent", "sendInput":
		return providers.AgentActivityRunning, true
	default:
		return "", false
	}
}

func codexAgentLabel(path string) string {
	label := strings.Trim(strings.TrimSpace(path), "/\\")
	if index := strings.LastIndexAny(label, "/\\"); index >= 0 {
		label = label[index+1:]
	}
	if label == "" {
		return "Codex agent"
	}
	return label
}

func (sub *turnSubscription) emit(ev providers.StreamEvent) {
	if sub.sink != nil {
		sub.sink(ev)
	}
}

func (sub *turnSubscription) finish(result agent.LoopResult, err error) {
	select {
	case sub.done <- turnOutcome{result: agentengine.TurnResult{Result: result}, err: err}:
	default:
	}
}
