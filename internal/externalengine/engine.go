// Package externalengine hosts external coding agents through their published
// protocols. Agents retain their own authentication, tools, and native history.
package externalengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type Engine struct {
	entry        enginecatalog.Entry
	binary, root string
}

func New(entry enginecatalog.Entry, binary, root string) *Engine {
	return &Engine{entry: entry, binary: binary, root: root}
}

func (e *Engine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	requiresHostModel := false
	return agentengine.Descriptor{ID: agentengine.EngineID(e.entry.ID), Version: "1", RequiresHostModel: &requiresHostModel, Capabilities: []string{"native-tool-loop", "native-session-resume", "interactive-approval", "model-options", "default-permission-unconfined", e.entry.Protocol}}, nil
}

func (e *Engine) Open(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, error) {
	return e.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: req.ThreadID, RootDir: req.RootDir})
}

func (e *Engine) Resume(ctx context.Context, req agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: req.ThreadID, RootDir: req.RootDir, ExternalRef: req.ExternalSessionRef})
}

func (e *Engine) SessionForThread(ctx context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if binding.RootDir == "" {
		binding.RootDir = e.root
	}
	if binding.Speed != "" && e.entry.Protocol != "acp" {
		return nil, fmt.Errorf("%s does not expose speed selection through this integration", e.entry.Name)
	}
	binding.MCPServers = append([]agentengine.MCPServer(nil), binding.MCPServers...)
	if e.entry.Protocol != "acp" && binding.Effort != "" {
		return nil, fmt.Errorf("%s does not expose reasoning effort through this integration; clear the effort selection", e.entry.Name)
	}
	return &Session{engine: e, binding: binding}, nil
}

type Session struct {
	engine  *Engine
	binding agentengine.ThreadBinding
	mu      sync.Mutex
	cancel  context.CancelFunc
	closed  bool
}

func (s *Session) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closed || s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return agentengine.TurnResult{}, errors.New("engine session is closed or already running")
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancel = nil; s.mu.Unlock() }()
	t := newTurn(s.engine.entry.ID, sink)
	message := latestUserMessage(input.History)
	var err error
	switch {
	case ctx.Err() != nil:
		err = ctx.Err()
	case strings.TrimSpace(message.Content) == "" && len(message.Images) == 0:
		err = errors.New("engine turn requires a user message")
	case s.binding.PermissionMode == "read_only" && s.engine.entry.Protocol != "acp":
		err = readOnlyUnsupportedError(s.engine.entry.Name)
	case s.engine.entry.Protocol == "acp":
		err = s.runACP(ctx, message, t)
	case s.engine.entry.Protocol == "opencode":
		err = s.runOpenCode(ctx, message, t)
	default:
		err = fmt.Errorf("unsupported engine protocol %q", s.engine.entry.Protocol)
	}
	result := t.result()
	if err != nil {
		result.FinishReason = providers.FinishReasonError
		t.failReconnect(err)
		t.emit(providers.StreamEvent{Type: providers.EventError, Error: err})
	} else {
		t.clearReconnect()
		t.emit(providers.StreamEvent{Type: providers.EventDone, StopReason: t.stopReason, FinishReason: result.FinishReason})
	}
	return agentengine.TurnResult{Result: result}, err
}

func (s *Session) Interrupt(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return agentengine.ErrNoActiveTurn
	}
	s.cancel()
	return nil
}

func (s *Session) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *Session) persist(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("engine returned an empty session ID")
	}
	if s.binding.PersistRef != nil {
		if err := s.binding.PersistRef(ref); err != nil {
			return fmt.Errorf("persist engine session: %w", err)
		}
	}
	s.binding.ExternalRef = ref
	return nil
}

func readOnlyUnsupportedError(name string) error {
	return fmt.Errorf("%s does not enforce Wuu's read-only boundary; choose another engine or change the permission mode explicitly", name)
}

func latestUserMessage(history []providers.ChatMessage) providers.ChatMessage {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i]
		}
	}
	return providers.ChatMessage{}
}

type turn struct {
	engine         string
	sink           agentengine.EventSink
	text, thinking strings.Builder
	diagBuf        strings.Builder
	reconnecting   bool
	retryCount     int
	apiError       string
	terminatedErr  string
	tools          map[string]*providers.ToolCall
	acpToolKinds   map[string]string
	completed      map[string]bool
	usage          providers.TokenUsage
	stopReason     string
}

func newTurn(engine string, sink agentengine.EventSink) *turn {
	return &turn{engine: engine, sink: sink, tools: make(map[string]*providers.ToolCall), completed: make(map[string]bool)}
}

func (t *turn) emit(event providers.StreamEvent) {
	if t.sink != nil {
		t.sink(event)
	}
}
func (t *turn) content(text string, thinking bool) {
	if text != "" {
		t.clearReconnect()
	}
	kind := providers.EventContentDelta
	if thinking {
		t.thinking.WriteString(text)
		kind = providers.EventThinkingDelta
	} else {
		t.text.WriteString(text)
	}
	t.emit(providers.StreamEvent{Type: kind, Content: text})
}

func (t *turn) startReconnect(attempt int, reason string, retryIn time.Duration) {
	if attempt <= 0 {
		t.retryCount++
		attempt = t.retryCount
	} else {
		t.retryCount = attempt
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		t.apiError = reason
	}
	t.reconnecting = true
	t.emit(providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:      providers.StreamPhaseReconnecting,
			RetryCount: t.retryCount,
			RetryIn:    retryIn,
			Reason:     t.apiError,
		},
	})
}

func (t *turn) clearReconnect() {
	if !t.reconnecting {
		return
	}
	t.reconnecting = false
	t.apiError = ""
	t.terminatedErr = ""
	t.retryCount = 0
	t.emit(providers.StreamEvent{
		Type:      providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{Phase: providers.StreamPhaseConnected},
	})
}

func (t *turn) failReconnect(err error) {
	if !t.reconnecting {
		return
	}
	t.reconnecting = false
	reason := t.apiError
	if err != nil && !errors.Is(err, context.Canceled) && strings.TrimSpace(err.Error()) != "" {
		reason = err.Error()
	}
	t.emit(providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:      providers.StreamPhaseFailed,
			RetryCount: t.retryCount,
			Reason:     reason,
		},
	})
}

func (t *turn) tool(id, name, args, status, output string) {
	if id == "" || t.completed[id] {
		return
	}
	t.clearReconnect()
	call, exists := t.tools[id]
	if !exists {
		call = &providers.ToolCall{ID: id, Name: name, Arguments: args}
		t.tools[id] = call
	}
	if name != "" {
		call.Name = name
	}
	if call.Name == "" {
		call.Name = "tool"
	}
	if args != "" {
		call.Arguments = args
	}
	copy := *call
	kind := providers.EventToolUseDelta
	if !exists {
		kind = providers.EventToolUseStart
	}
	t.emit(providers.StreamEvent{Type: kind, ToolCall: &copy})
	if status == "completed" || status == "failed" {
		if output == "" {
			output = status
		}
		t.emit(providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &copy, ToolResult: output})
		t.completed[id] = true
		delete(t.tools, id)
	}
}

func (t *turn) result() agent.LoopResult {
	r := agent.LoopResult{Content: t.text.String(), StopReason: t.stopReason, FinishReason: providers.FinishReasonStop,
		InputTokens: t.usage.InputTokens, OutputTokens: t.usage.OutputTokens, CacheReadTokens: t.usage.CacheReadTokens, CacheCreationTokens: t.usage.CacheCreationTokens}
	if t.text.Len() > 0 || t.thinking.Len() > 0 {
		r.NewMessages = []providers.ChatMessage{{Role: "assistant", Content: t.text.String(), ReasoningContent: t.thinking.String()}}
	}
	return r
}
