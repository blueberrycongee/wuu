package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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
	RequestApproval  agentengine.ApprovalHandler
	RequestUserInput agentengine.UserInputHandler
}

// NewEngine builds the codex engine around a host (binary discovery +
// app-server lifecycle).
func NewEngine(host *Host) *Engine {
	return &Engine{host: host}
}

// Host returns the shared process host used by this engine's bindings.
func (e *Engine) Host() *Host {
	return e.host
}

// Descriptor describes the codex engine.
func (e *Engine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{
		ID:      agentengine.EngineID("codex"),
		Version: "1",
		Capabilities: []string{
			"native-tool-loop",
			"native-session-resume",
			"active-turn-steering",
			"approval-requests",
			"user-input-requests",
			// engine/list with include_quota reads this engine's account
			// allowance, so clients can reserve room for it beforehand.
			"account-quota",
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
		speed:          binding.Speed,
		permissionMode: binding.PermissionMode,
		instructions:   binding.Instructions,
		mcpServers:     binding.MCPServers,
		externalRef:    binding.ExternalRef,
		persistRef:     binding.PersistRef,
		persistTurnRef: binding.PersistTurnRef,
		approval:       binding.RequestApproval,
		userInput:      binding.RequestUserInput,
	})
}

type sessionOptions struct {
	threadID       string
	rootDir        string
	model          string
	effort         ReasoningEffort
	speed          string
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer
	externalRef    string
	persistRef     func(string) error
	persistTurnRef func(string, bool) error
	approval       agentengine.ApprovalHandler
	userInput      agentengine.UserInputHandler
}

func (e *Engine) newSession(ctx context.Context, opts sessionOptions) (agentengine.Session, error) {
	if e == nil || e.host == nil {
		return nil, errors.New("codex engine is not configured")
	}
	client, release, err := e.host.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	approval := opts.approval
	if approval == nil {
		approval = e.RequestApproval
	}
	userInput := opts.userInput
	if userInput == nil {
		userInput = e.RequestUserInput
	}
	return &Session{
		client:         client,
		release:        release,
		threadID:       opts.threadID,
		rootDir:        opts.rootDir,
		model:          opts.model,
		effort:         opts.effort,
		speed:          opts.speed,
		permissionMode: opts.permissionMode,
		instructions:   opts.instructions,
		mcpServers:     append([]agentengine.MCPServer(nil), opts.mcpServers...),
		ref:            opts.externalRef,
		persist:        opts.persistRef,
		persistTurnRef: opts.persistTurnRef,
		approval:       approval,
		steerReady:     make(chan struct{}),
		userInput:      userInput,
	}, nil
}

// Session is one wuu thread's handle on a codex thread. The codex thread is
// created lazily on the first turn (thread/start) and its id is persisted via
// the binding's PersistRef; later turns run turn/start directly on the shared
// app-server, or thread/resume first if the app-server restarted.
type Session struct {
	client  *Client
	release func()

	threadID       string
	rootDir        string
	model          string
	effort         ReasoningEffort
	speed          string
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer

	mu                   sync.Mutex
	ref                  string
	persist              func(string) error
	persistTurnRef       func(string, bool) error
	approval             agentengine.ApprovalHandler
	approvalCtx          context.Context
	activeTurnID         string
	steerReady           chan struct{}
	steerReadyOnce       sync.Once
	userInput            agentengine.UserInputHandler
	userInputDefaultMode bool
	cancelTurn           context.CancelFunc
}

// RunTurn starts a codex turn and translates its notifications into Wuu
// stream events. It blocks until turn/completed or the context is canceled.
func (s *Session) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	if s == nil || s.client == nil {
		return agentengine.TurnResult{}, errors.New("codex session has no app-server client")
	}
	defer s.signalSteerReady()
	if err := ctx.Err(); err != nil {
		return agentengine.TurnResult{}, err
	}
	// Pending is written only by the native admission protocol below. Missing
	// IDs in older conversations do not imply uncertain work and stay usable.
	if s.persistTurnRef != nil {
		var native *historyThread
		for _, message := range input.History {
			if !message.EngineTurnPending {
				continue
			}
			if message.EngineTurnID == "" {
				return agentengine.TurnResult{}, errors.New("an earlier Codex admission has no verified native turn ID; fork an earlier completed turn before continuing")
			}
			if native == nil {
				var response struct {
					Thread historyThread `json:"thread"`
				}
				if err := s.client.Request(ctx, "thread/read", map[string]any{"threadId": s.ref, "includeTurns": true}, &response); err != nil {
					return agentengine.TurnResult{}, fmt.Errorf("verify pending Codex turn: %w", err)
				}
				if response.Thread.ID != s.ref || response.Thread.Turns == nil {
					return agentengine.TurnResult{}, errors.New("Codex did not return the pending native history; no new input was sent")
				}
				native = &response.Thread
			}
			matches := 0
			terminal := false
			for _, turn := range native.Turns {
				if turn.ID == message.EngineTurnID {
					matches++
					terminal = turn.Status == "completed" || turn.Status == "interrupted" || turn.Status == "failed"
				}
			}
			if matches != 1 || !terminal {
				return agentengine.TurnResult{}, errors.New("an earlier Codex turn still has an unconfirmed native outcome; no new input was sent")
			}
			if err := s.persistTurnRef(message.EngineTurnID, false); err != nil {
				return agentengine.TurnResult{}, fmt.Errorf("settle recovered native turn: %w", err)
			}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.approvalCtx = ctx
	s.cancelTurn = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.approvalCtx = nil
		s.activeTurnID = ""
		s.cancelTurn = nil
		s.mu.Unlock()
	}()
	// First turn: create the codex thread and persist its id so later turns
	// (and later app-server processes) resume the same native session.
	if err := s.ensureThread(ctx); err != nil {
		return agentengine.TurnResult{}, err
	}
	unregister := s.registerApprovalHandlers()
	defer unregister()
	inputs := turnInputs(input.History)
	// Subscribe before turn/start: the app-server streams notifications
	// immediately after responding, so a late subscription would drop them.
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(s.client, s.ref, sink, done)
	defer sub.close()
	// Questions use the same authenticated native identity as output. A
	// turn/started notification may precede a delayed or lost start response.
	questionCtx, cancelQuestions := context.WithCancel(ctx)
	defer cancelQuestions()
	go func() {
		select {
		case <-sub.nativeStopped:
		case <-s.client.done:
		case <-questionCtx.Done():
		}
		cancelQuestions()
	}()
	removeInput := s.client.OnRequest(MethodToolRequestUserInput, func(raw json.RawMessage) (any, error) {
		return s.handleUserInput(questionCtx, sub, raw)
	})
	defer removeInput()
	if s.persistTurnRef != nil {
		if err := s.persistTurnRef("", true); err != nil {
			return agentengine.TurnResult{}, fmt.Errorf("persist native turn admission: %w", err)
		}
	}
	settle := func(out turnOutcome) (agentengine.TurnResult, error) {
		// Stream failure and transport shutdown are not proof of native completion.
		// Read the independently retained terminal notification even when partial
		// output failed before its event could be delivered to the stream consumer.
		sub.mu.Lock()
		status, turnID, nativeDone := sub.nativeStatus, sub.turnID, sub.nativeDone
		sub.mu.Unlock()
		if s.persistTurnRef != nil && nativeDone {
			switch status {
			case "completed", "interrupted", "failed":
				if err := s.persistTurnRef(turnID, false); err != nil {
					out.err = errors.Join(out.err, fmt.Errorf("persist native turn settlement: %w", err))
				}
			}
		}
		return out.result, out.err
	}
	turnResp, err := s.startTurn(ctx, inputs)
	if err == nil {
		if strings.TrimSpace(turnResp.Turn.ID) == "" {
			err = errors.New("codex turn/start returned an empty turn id")
		} else {
			sub.setTurnID(turnResp.Turn.ID)
		}
	}
	sub.mu.Lock()
	turnID := sub.turnID
	sub.mu.Unlock()
	if turnID != "" && s.persistTurnRef != nil {
		if persistErr := s.persistTurnRef(turnID, true); persistErr != nil {
			err = errors.Join(err, fmt.Errorf("persist native turn boundary: %w", persistErr))
		}
	}
	if err != nil {
		var rejected *RPCError
		definitelyRejected := errors.As(err, &rejected) &&
			(rejected.Code == -32600 || rejected.Code == -32601 || rejected.Code == -32602)
		if definitelyRejected && turnID == "" {
			if s.persistTurnRef != nil {
				err = errors.Join(err, s.persistTurnRef("", false))
			}
		} else {
			// Internal errors and invalid/lost responses may follow acceptance.
			// Retain partial output and require bounded native stop confirmation.
			s.stopNativeTurn(sub)
		}
		sub.fail(err)
		out := <-done
		out.err = errors.Join(err, out.err)
		return settle(out)
	}
	s.mu.Lock()
	s.activeTurnID = turnID
	s.mu.Unlock()
	s.signalSteerReady()

	select {
	case out := <-done:
		sub.mu.Lock()
		streamFailed := sub.streamErr != nil
		sub.mu.Unlock()
		if streamFailed {
			s.stopNativeTurn(sub)
		}
		return settle(out)
	case <-ctx.Done():
		s.stopNativeTurn(sub)
		sub.fail(ctx.Err())
		out := <-done
		out.err = errors.Join(ctx.Err(), out.err)
		return settle(out)
	}
}

// Acknowledging turn/interrupt is not a terminal event. If the process cannot
// confirm that the turn stopped, retire the shared connection; peer turns fail
// visibly as well rather than continue executing without an observer.
func (s *Session) stopNativeTurn(sub *turnSubscription) {
	select {
	case <-sub.nativeStopped:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeCleanupTimeout)
	defer cancel()
	// turn/started can identify an accepted turn even when the start response
	// is lost. Do not send an empty ID or discard its eventual stop proof.
	select {
	case <-sub.ready:
		sub.mu.Lock()
		turnID := sub.turnID
		sub.mu.Unlock()
		_ = s.interrupt(ctx, turnID)
	case <-ctx.Done():
	case <-s.client.done:
	}
	select {
	case <-sub.nativeStopped:
		return
	case <-ctx.Done():
	case <-s.client.done:
	}
	select {
	case <-sub.nativeStopped:
		return
	default:
	}
	_ = s.client.Close()
}

// signalSteerReady also unblocks waiting input when native startup fails.
func (s *Session) signalSteerReady() {
	s.steerReadyOnce.Do(func() {
		if s.steerReady != nil {
			close(s.steerReady)
		}
	})
}

// Steer preserves native ownership of the loop. A stable client ID identifies
// the human message, while expectedTurnId fences it against a successor turn.
func (s *Session) Steer(ctx context.Context, message providers.ChatMessage) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("%w: no native session", agentengine.ErrSteerRejected)
	}
	if s.steerReady != nil {
		select {
		case <-s.steerReady:
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %v", agentengine.ErrSteerRejected, ctx.Err())
		}
	}
	s.mu.Lock()
	ref, turnID := s.ref, s.activeTurnID
	s.mu.Unlock()
	if ref == "" || turnID == "" || ctx.Err() != nil {
		return "", fmt.Errorf("%w: no active native turn", agentengine.ErrSteerRejected)
	}
	if message.Role != "user" || message.Hidden || message.ReadOnly || (message.Origin != "" && message.Origin != "user") || strings.TrimSpace(message.ClientID) == "" {
		return "", fmt.Errorf("%w: explicit human input is required", agentengine.ErrSteerRejected)
	}
	var response TurnSteerResponse
	err := s.client.Request(ctx, MethodTurnSteer, TurnSteerParams{
		ThreadID: ref, ExpectedTurnID: turnID, ClientUserMessageID: message.ClientID,
		Input: turnInputs([]providers.ChatMessage{message}),
	}, &response)
	if err != nil {
		var rejected *RPCError
		if errors.As(err, &rejected) {
			if rejected.Code == -32601 {
				return "", fmt.Errorf("%w: %v", agentengine.ErrSteerUnsupported, err)
			}
			if rejected.Code == -32600 || rejected.Code == -32602 {
				return "", fmt.Errorf("%w: %v", agentengine.ErrSteerRejected, err)
			}
		}
		return "", err
	}
	if response.TurnID != turnID {
		return "", fmt.Errorf("native steering acknowledgement returned turn %q, expected %q; delivery is unconfirmed", response.TurnID, turnID)
	}
	return response.TurnID, nil
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
	if s.userInput != nil || s.speed == "" || s.model == "" || s.effort == "" {
		// Omitting fields on a warm native thread inherits its previous
		// overrides. Resolve the native configuration before every new binding.
		var response struct {
			Config struct {
				Model       string          `json:"model"`
				Effort      ReasoningEffort `json:"model_reasoning_effort"`
				ServiceTier *string         `json:"service_tier"`
				Features    struct {
					DefaultModeUserInput *bool `json:"default_mode_request_user_input"`
				} `json:"features"`
			} `json:"config"`
		}
		if err := s.client.Request(ctx, "config/read", map[string]any{"cwd": s.rootDir, "includeLayers": false}, &response); err != nil {
			return fmt.Errorf("codex config/read: %w", err)
		}
		if s.userInput != nil {
			configured := response.Config.Features.DefaultModeUserInput
			s.userInputDefaultMode = configured == nil || *configured
		}
		if s.model == "" {
			s.model = response.Config.Model
		}
		if s.effort == "" {
			s.effort = response.Config.Effort
		}
		if s.model == "" || s.effort == "" {
			models, err := listModels(ctx, s.client)
			if err != nil {
				return err
			}
			for _, model := range models {
				id := model.Model
				if id == "" {
					id = model.ID
				}
				if s.model == "" && model.IsDefault {
					s.model = id
				}
				if id == s.model && s.effort == "" {
					s.effort = model.DefaultReasoningEffort
				}
			}
		}
		if s.speed == "" {
			s.speed = "standard"
			if response.Config.ServiceTier != nil && *response.Config.ServiceTier != "" {
				s.speed = *response.Config.ServiceTier
			}
		}
	}
	s.mu.Lock()
	ref := s.ref
	s.mu.Unlock()
	sandbox, approval, _ := codexPermissionSettings(s.permissionMode)
	config := s.threadConfig()
	if ref != "" {
		if s.model == "" {
			return errors.New("codex did not expose its configured default model; select a model before resuming")
		}
		var resp ThreadResumeResponse
		if err := s.client.Request(ctx, MethodThreadResume, ThreadResumeParams{
			ThreadID: ref, Model: s.model, CWD: s.rootDir,
			ApprovalPolicy: approval, Sandbox: sandbox, Config: config, ServiceTier: s.serviceTier(),
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
		ServiceTier:        s.serviceTier(),
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
	if s.userInput != nil {
		// Enable the host question bridge in ordinary native turns while
		// preserving explicit opt-outs. The native tool-level gate is untouched.
		config["features.default_mode_request_user_input"] = s.userInputDefaultMode
	}
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

func (s *Session) startTurn(ctx context.Context, inputs []UserInput) (TurnStartResponse, error) {
	var resp TurnStartResponse
	_, approval, sandboxPolicy := codexPermissionSettings(s.permissionMode)
	if sandboxPolicy.Type == SandboxPolicyWorkspaceWrite && strings.TrimSpace(s.rootDir) != "" {
		sandboxPolicy.WritableRoots = []string{s.rootDir}
	}
	// Loaded native threads can ignore resume overrides. Set CWD together
	// with the sandbox so its implicit writable CWD cannot remain on the old
	// worktree while WritableRoots points at the newly selected workspace.
	err := s.client.Request(ctx, MethodTurnStart, TurnStartParams{
		CWD:            s.rootDir,
		ThreadID:       s.ref,
		Input:          inputs,
		Model:          s.model,
		Effort:         s.effort,
		ServiceTier:    s.serviceTier(),
		ApprovalPolicy: approval,
		SandboxPolicy:  &sandboxPolicy,
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

const nativeCleanupTimeout = 2 * time.Second

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
// app-server stays alive while other threads or app-server owners use it.
func (s *Session) Close(context.Context) error {
	if s != nil {
		s.mu.Lock()
		cancel := s.cancelTurn
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if s.release != nil {
			s.release()
		}
	}
	return nil
}

// turnInputs selects the latest user message, including image-only turns.
// Earlier turns already belong to the native Codex thread.
func turnInputs(history []providers.ChatMessage) []UserInput {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != "user" {
			continue
		}
		inputs := make([]UserInput, 0, 1+len(msg.Images))
		if strings.TrimSpace(msg.Content) != "" {
			inputs = append(inputs, UserInput{Type: "text", Text: msg.Content})
		}
		for _, img := range msg.Images {
			inputs = append(inputs, UserInput{Type: "image", URL: "data:" + img.MediaType + ";base64," + img.Data})
		}
		return inputs
	}
	return nil
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
	client   *Client
	threadID string
	turnID   string
	sink     agentengine.EventSink
	done     chan turnOutcome

	events          chan turnNotification
	mu              sync.Mutex
	closed          bool
	stopped         chan struct{}
	ready           chan struct{}
	wake            chan struct{}
	streamErr       error
	terminal        *turnNotification
	terminalTurnID  string
	nativeStopped   chan struct{}
	nativeStatus    string
	nativeDone      bool
	unsubs          []func()
	labels          map[string]string
	tools           map[string]struct{}
	reasoning       map[string]struct{}
	lastAgentText   string
	lastAgentPhase  providers.MessagePhase
	lastAgentItemID string
}

var errTurnStreamOverflow = errors.New("codex turn stream exceeded its buffer and cannot continue safely")

func newTurnSubscription(client *Client, threadID string, sink agentengine.EventSink, done chan turnOutcome) *turnSubscription {
	sub := &turnSubscription{
		client:        client,
		threadID:      threadID,
		sink:          sink,
		done:          done,
		events:        make(chan turnNotification, 512),
		stopped:       make(chan struct{}),
		ready:         make(chan struct{}),
		wake:          make(chan struct{}, 1),
		nativeStopped: make(chan struct{}),
		labels:        make(map[string]string),
		tools:         make(map[string]struct{}),
		reasoning:     make(map[string]struct{}),
	}
	handlers := map[string]struct{}{
		NotifyTurnStarted:           {},
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
	go sub.run()
	return sub
}

// setTurnID binds the subscription to the accepted native turn. turn/started
// may arrive before its RPC response, including when that response is lost.
func (sub *turnSubscription) setTurnID(turnID string) {
	sub.mu.Lock()
	if sub.turnID == "" {
		sub.turnID = turnID
		close(sub.ready)
	}
	if sub.terminalTurnID == sub.turnID && !sub.nativeDone {
		sub.nativeDone = true
		close(sub.nativeStopped)
	}
	sub.signalLocked()
	sub.mu.Unlock()
}

func (sub *turnSubscription) enqueue(notification turnNotification) {
	var target struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(notification.params, &target); err != nil {
		sub.fail(fmt.Errorf("decode codex notification target: %w", err))
		return
	}
	if target.ThreadID != sub.threadID {
		return
	}
	var envelope struct {
		TurnID string            `json:"turnId"`
		Turn   TurnCompletedInfo `json:"turn"`
	}
	if err := json.Unmarshal(notification.params, &envelope); err != nil {
		sub.fail(fmt.Errorf("decode codex turn notification: %w", err))
		return
	}
	if envelope.TurnID == "" {
		envelope.TurnID = envelope.Turn.ID
	}
	if notification.method == NotifyTurnStarted {
		if strings.TrimSpace(envelope.TurnID) != "" {
			sub.setTurnID(envelope.TurnID)
		}
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || (sub.turnID != "" && envelope.TurnID != sub.turnID) {
		return
	}
	if notification.method == NotifyTurnCompleted {
		switch envelope.Turn.Status {
		case "completed", "interrupted", "failed":
		default:
			if sub.streamErr == nil {
				sub.streamErr = fmt.Errorf("codex turn/completed returned nonterminal status %q", envelope.Turn.Status)
			}
			sub.signalLocked()
			return
		}
		// Terminal delivery has its own slot. Ordinary output backpressure must
		// never hide proof that the native turn has stopped.
		sub.terminal = &notification
		sub.terminalTurnID = envelope.TurnID
		sub.nativeStatus = envelope.Turn.Status
		if sub.turnID != "" && !sub.nativeDone {
			sub.nativeDone = true
			close(sub.nativeStopped)
		}
		sub.signalLocked()
		return
	}
	if sub.streamErr != nil {
		return
	}
	select {
	case sub.events <- notification:
	default:
		sub.streamErr = errTurnStreamOverflow
		sub.signalLocked()
	}
}

func (sub *turnSubscription) signalLocked() {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (sub *turnSubscription) fail(err error) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.streamErr == nil {
		sub.streamErr = err
	}
	sub.signalLocked()
}

func (sub *turnSubscription) next() (turnNotification, error) {
	var clientDone <-chan struct{}
	if sub.client != nil {
		clientDone = sub.client.done
	}
	for {
		sub.mu.Lock()
		err := sub.streamErr
		ready := sub.turnID != ""
		sub.mu.Unlock()
		if ready && clientDone == nil && sub.client != nil {
			clientDone = sub.client.done
		}
		events := sub.events
		if !ready {
			events = nil
		}
		// Drain accepted notifications before a transport-close signal. A native
		// process can exit immediately after writing its successful terminal.
		select {
		case event, ok := <-events:
			if !ok {
				if err != nil {
					return turnNotification{}, err
				}
				return turnNotification{}, errors.New("codex turn stream closed before completion")
			}
			return event, nil
		default:
		}
		// A failed/invalid start response can race output already accepted by
		// the reader. Drain that bound output before reporting its failure.
		if err != nil {
			return turnNotification{}, err
		}
		sub.mu.Lock()
		var terminal *turnNotification
		if ready {
			terminal = sub.terminal
			sub.terminal = nil
		}
		sub.mu.Unlock()
		if terminal != nil {
			return *terminal, nil
		}
		select {
		case event, ok := <-events:
			if !ok {
				if err != nil {
					return turnNotification{}, err
				}
				return turnNotification{}, errors.New("codex turn stream closed before completion")
			}
			return event, nil
		case <-sub.wake:
		case <-sub.stopped:
			return turnNotification{}, context.Canceled
		case <-clientDone:
			// Recheck the queue and terminal slot after observing closure.
			clientDone = nil
			sub.mu.Lock()
			if sub.turnID == "" {
				// The response waiter will bind the ID or fail this subscription.
				// Preserve buffered output until it has made that decision.
				sub.mu.Unlock()
				continue
			}
			if len(sub.events) == 0 && sub.terminal == nil {
				sub.mu.Unlock()
				return turnNotification{}, sub.client.terminalError()
			}
			sub.mu.Unlock()
			clientDone = sub.client.done
		}
	}
}

func (sub *turnSubscription) close() {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return
	}
	sub.closed = true
	if sub.stopped != nil {
		close(sub.stopped)
	}
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
		usageCursor  int
		reconnecting bool
	)
	for {
		notification, streamErr := sub.next()
		if streamErr != nil {
			content := text.String()
			if content == "" {
				content = sub.lastAgentText
			}
			result := agent.LoopResult{
				Content: strings.TrimSpace(content), FinishReason: providers.FinishReasonError,
				InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
				CacheReadTokens: usage.CacheReadTokens, CacheCreationTokens: usage.CacheCreationTokens,
			}
			if content != "" || reasoning.Len() > 0 {
				result.NewMessages = []providers.ChatMessage{{Role: "assistant", Content: content, ReasoningContent: reasoning.String()}}
			}
			if !errors.Is(streamErr, context.Canceled) {
				sub.emit(providers.StreamEvent{Type: providers.EventError, Error: streamErr})
			}
			sub.finish(result, streamErr)
			return
		}
		if sub.turnID == "" {
			// No turn id (should not happen); drop everything.
			continue
		}
		var envelope struct {
			TurnID       string            `json:"turnId"`
			ItemID       string            `json:"itemId"`
			Delta        string            `json:"delta"`
			SummaryIndex int               `json:"summaryIndex"`
			Turn         TurnCompletedInfo `json:"turn"`
			Message      string            `json:"message"`
			WillRetry    bool              `json:"willRetry"`
			Error        *struct {
				Message string `json:"message"`
			} `json:"error"`
			TokenUsage *struct {
				Total TokenUsageBreakdown `json:"total"`
				Last  TokenUsageBreakdown `json:"last"`
			} `json:"tokenUsage"`
			Item json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(notification.params, &envelope); err != nil {
			sub.fail(fmt.Errorf("decode codex turn notification: %w", err))
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
			if envelope.TokenUsage != nil && envelope.TokenUsage.Total.TotalTokens > usageCursor {
				// The lifetime total is a cursor, not this turn's bill. Only
				// accept advancing snapshots; retries can replay the last call.
				usageCursor = envelope.TokenUsage.Total.TotalTokens
				last := envelope.TokenUsage.Last
				usage.InputTokens += max(0, last.InputTokens-last.CachedInputTokens-last.CacheWriteInputTokens)
				// Reasoning is already included in outputTokens.
				usage.OutputTokens += last.OutputTokens
				usage.CacheReadTokens += last.CachedInputTokens
				usage.CacheCreationTokens += last.CacheWriteInputTokens
				snapshot := usage
				sub.emit(providers.StreamEvent{Type: providers.EventUsage, Usage: &snapshot})
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
			if reconnecting {
				sub.emit(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseFailed, Reason: lastErr.Error()}})
			}
			// A non-retryable stream error is not native turn completion. Preserve
			// partial output and let RunTurn confirm the turn stopped.
			sub.fail(lastErr)
			continue
		case NotifyTurnCompleted:
			status := envelope.Turn.Status
			var turnErr error
			finishReason := providers.FinishReasonStop
			if status != "" && status != "completed" {
				finishReason = providers.FinishReasonError
				if status == "interrupted" {
					turnErr = context.Canceled
				} else {
					message := "engine turn ended with status " + status
					if envelope.Turn.Error != nil && strings.TrimSpace(envelope.Turn.Error.Message) != "" {
						message = envelope.Turn.Error.Message
					}
					turnErr = errors.New(message)
					sub.emit(providers.StreamEvent{Type: providers.EventError, Error: turnErr})
				}
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
				Content:             strings.TrimSpace(content),
				FinishReason:        finishReason,
				StopReason:          status,
				InputTokens:         usage.InputTokens,
				OutputTokens:        usage.OutputTokens,
				CacheReadTokens:     usage.CacheReadTokens,
				CacheCreationTokens: usage.CacheCreationTokens,
				NewMessages: []providers.ChatMessage{{
					Role:             "assistant",
					Content:          content,
					ReasoningContent: reasoning.String(),
					Phase:            sub.lastAgentPhase,
					ProviderItemID:   sub.lastAgentItemID,
				}},
			}
			sub.finish(result, turnErr)
			return
		}
	}
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

func (s *Session) serviceTier() *string {
	if s.speed == "" {
		return nil
	}
	tier := s.speed
	if tier == "standard" {
		tier = "default"
	}
	return &tier
}
