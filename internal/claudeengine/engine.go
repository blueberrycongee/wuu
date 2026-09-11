package claudeengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ResolveBinary locates the claude executable. The WUU_CLAUDE_BINARY
// environment variable wins; otherwise PATH lookup of "claude".
func ResolveBinary() (string, error) {
	if path := strings.TrimSpace(envClaudeBinary()); path != "" {
		return path, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude binary not found: set WUU_CLAUDE_BINARY or install the claude CLI on PATH")
	}
	return path, nil
}

func envClaudeBinary() string {
	return strings.TrimSpace(os.Getenv("WUU_CLAUDE_BINARY"))
}

// Version reports the CLI version via `claude --version`. Empty on failure.
func Version(binaryPath string) string {
	out, err := exec.Command(binaryPath, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Engine is the claude agent engine: the Claude Code CLI hosted as an
// external process in headless stream-json mode. It implements
// agentengine.Factory and agentengine.ThreadBoundFactory.
type Engine struct {
	binaryPath string
	rootDir    string
}

// NewEngine builds the claude engine around a resolved binary path.
func NewEngine(binaryPath, rootDir string) *Engine {
	return &Engine{binaryPath: binaryPath, rootDir: rootDir}
}

// Descriptor describes the claude engine.
func (e *Engine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	version := Version(e.binaryPath)
	return agentengine.Descriptor{
		ID:      agentengine.EngineID("claude"),
		Version: version,
		Capabilities: []string{
			"native-tool-loop",
			"native-session-resume",
		},
	}, nil
}

// Open starts a fresh claude session for a new conversation.
func (e *Engine) Open(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID: req.ThreadID,
		rootDir:  firstNonEmpty(req.RootDir, e.rootDir),
	})
}

// Resume reopens a claude session by its native session id.
func (e *Engine) Resume(ctx context.Context, req agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID:    req.ThreadID,
		rootDir:     firstNonEmpty(req.RootDir, e.rootDir),
		externalRef: req.ExternalSessionRef,
	})
}

// SessionForThread binds the claude engine to an existing thread runtime.
func (e *Engine) SessionForThread(ctx context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	return e.newSession(ctx, sessionOptions{
		threadID:       binding.ThreadID,
		rootDir:        firstNonEmpty(binding.RootDir, e.rootDir),
		model:          binding.Model,
		effort:         binding.Effort,
		permissionMode: binding.PermissionMode,
		instructions:   binding.Instructions,
		mcpServers:     binding.MCPServers,
		externalRef:    binding.ExternalRef,
		persistRef:     binding.PersistRef,
	})
}

type sessionOptions struct {
	threadID       string
	rootDir        string
	model          string
	effort         string
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer
	externalRef    string
	persistRef     func(string) error
}

func (e *Engine) newSession(ctx context.Context, opts sessionOptions) (agentengine.Session, error) {
	if e == nil {
		return nil, errors.New("claude engine is not configured")
	}
	return &Session{
		engine:         e,
		rootDir:        opts.rootDir,
		model:          opts.model,
		effort:         opts.effort,
		permissionMode: opts.permissionMode,
		instructions:   opts.instructions,
		mcpServers:     append([]agentengine.MCPServer(nil), opts.mcpServers...),
		ref:            opts.externalRef,
		persistedRef:   opts.externalRef,
		persist:        opts.persistRef,
	}, nil
}

// Session is one wuu thread's handle on a claude CLI session. The claude
// child is spawned lazily on the first turn and resumed on later turns via
// --resume <session-id>.
type Session struct {
	engine         *Engine
	rootDir        string
	model          string
	effort         string
	permissionMode string
	instructions   string
	mcpServers     []agentengine.MCPServer

	mu           sync.Mutex
	ref          string
	persistedRef string
	persist      func(string) error

	// writeMu serializes all stdin writes (user turns, tool results).
	writeMu sync.Mutex
}

// RunTurn sends one user prompt and translates the claude stream into Wuu
// events. It blocks until the result line or the context is canceled.
func (s *Session) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	if s == nil || s.engine == nil {
		return agentengine.TurnResult{}, errors.New("claude session is not configured")
	}
	if err := ctx.Err(); err != nil {
		return agentengine.TurnResult{}, err
	}
	message := turnMessage(input.History)
	if strings.TrimSpace(message.Content) == "" && len(message.Images) == 0 {
		return agentengine.TurnResult{}, errors.New("claude turn requires a user message")
	}

	done := make(chan turnOutcome, 1)
	sub := newTurnSubscriptionForContext(ctx, sink, done)
	sub.onSessionID = s.persistSessionIDValue
	transport, err := s.spawn(ctx, sub)
	if err != nil {
		sub.close()
		return agentengine.TurnResult{}, err
	}
	defer func() {
		sub.close()
		_ = transport.Close()
	}()

	s.writeMu.Lock()
	err = transport.WriteLine(ctx, marshalLine(userPromptEnvelope(message)))
	s.writeMu.Unlock()
	if err != nil {
		return agentengine.TurnResult{}, fmt.Errorf("send claude user input: %w", err)
	}

	select {
	case out := <-done:
		s.persistSessionID(sub)
		if err := ctx.Err(); err != nil {
			return out.result, err
		}
		return out.result, out.err
	case <-ctx.Done():
		// Stop accepting Claude's shutdown result before closing stdin. Claude
		// may report SIGINT/EOF as an error result; that is part of a requested
		// interruption, not a provider or Wuu failure.
		sub.close()
		_ = transport.Close()
		s.persistSessionID(sub)
		select {
		case out := <-done:
			return out.result, ctx.Err()
		default:
			return agentengine.TurnResult{}, ctx.Err()
		}
	}
}

// persistSessionID records the claude session id from system/init into the
// thread's engine_ref so later turns resume the same session.
func (s *Session) persistSessionID(sub *turnSubscription) {
	sub.mu.Lock()
	sid := sub.sessionID
	sub.mu.Unlock()
	s.persistSessionIDValue(sid)
}

func (s *Session) persistSessionIDValue(sid string) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return
	}
	s.mu.Lock()
	s.ref = sid
	if s.persistedRef == sid {
		s.mu.Unlock()
		return
	}
	persist := s.persist
	s.mu.Unlock()
	if persist == nil || persist(sid) != nil {
		return
	}
	s.mu.Lock()
	s.persistedRef = sid
	s.mu.Unlock()
}

// Interrupt cancels the in-flight turn. The child is closed; the next turn
// respawns it (resumed by session id when available).
func (s *Session) Interrupt(context.Context, string) error {
	return errors.New("claude interrupt is handled via turn context cancellation")
}

// Close releases the session. The child is closed by RunTurn's deferred
// cleanup; nothing to do here.
func (s *Session) Close(context.Context) error {
	return nil
}

// spawn starts (or resumes) the claude child for one turn. The subscription
// must be registered before spawn so no early line is missed.
func (s *Session) spawn(ctx context.Context, sub *turnSubscription) (*Transport, error) {
	s.mu.Lock()
	ref := s.ref
	s.mu.Unlock()

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", claudePermissionMode(s.permissionMode),
	}
	if instructions := strings.TrimSpace(s.instructions); instructions != "" {
		args = append(args, "--append-system-prompt", instructions)
	}
	if len(s.mcpServers) > 0 {
		servers := make(map[string]any, len(s.mcpServers))
		for _, server := range s.mcpServers {
			if strings.TrimSpace(server.Name) != "" && strings.TrimSpace(server.URL) != "" {
				servers[server.Name] = map[string]any{"type": "http", "url": server.URL}
			}
		}
		if len(servers) > 0 {
			encoded, err := json.Marshal(map[string]any{"mcpServers": servers})
			if err != nil {
				return nil, fmt.Errorf("encode named agent MCP config: %w", err)
			}
			args = append(args, "--mcp-config", string(encoded))
		}
	}
	if model := strings.TrimSpace(s.model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(s.effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if ref != "" {
		args = append(args, "--resume", ref)
	}
	transport, err := NewTransport(TransportOptions{
		BinaryPath: s.engine.binaryPath,
		Args:       args,
		CWD:        s.rootDir,
	})
	if err != nil {
		return nil, err
	}
	transport.OnLine(sub.handleLine)
	transport.OnClose(sub.handleTransportClose)
	sub.attachTransport(transport)
	return transport, nil
}

func claudePermissionMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "read_only":
		return "plan"
	case "unconfined":
		return "bypassPermissions"
	default:
		// The headless stream-json transport has no Claude permission-prompt
		// bridge yet, so standard mode denies requests instead of hanging.
		return "dontAsk"
	}
}

// turnMessage selects the latest user message without replaying older text
// when the current turn contains only images.
func turnMessage(history []providers.ChatMessage) providers.ChatMessage {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i]
		}
	}
	return providers.ChatMessage{}
}

func marshalLine(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// turnOutcome carries the converted loop result and terminal error.
type turnOutcome struct {
	result agentengine.TurnResult
	err    error
}

// turnSubscription translates claude stdout lines into Wuu stream events.
type turnSubscription struct {
	ctx  context.Context
	sink agentengine.EventSink
	done chan turnOutcome

	mu          sync.Mutex
	transport   *Transport
	sessionID   string
	onSessionID func(string)
	closed      bool

	text           strings.Builder
	reasoning      strings.Builder
	usage          providers.TokenUsage
	tools          map[string]*pendingTool
	toolIDByIndex  map[int]string
	streamText     map[int]string
	streamThinking map[int]string
	agentTasks     map[string]observedAgentTask
	lastRawResult  json.RawMessage
	finishOnce     sync.Once
}

type pendingTool struct {
	id            string
	name          string
	agentActivity bool
	arguments     strings.Builder
}

type observedAgentTask struct {
	activityID string
	label      string
}

func newTurnSubscription(sink agentengine.EventSink, done chan turnOutcome) *turnSubscription {
	return newTurnSubscriptionForContext(context.Background(), sink, done)
}

func newTurnSubscriptionForContext(ctx context.Context, sink agentengine.EventSink, done chan turnOutcome) *turnSubscription {
	if ctx == nil {
		ctx = context.Background()
	}
	return &turnSubscription{
		ctx:            ctx,
		sink:           sink,
		done:           done,
		tools:          make(map[string]*pendingTool),
		toolIDByIndex:  make(map[int]string),
		streamText:     make(map[int]string),
		streamThinking: make(map[int]string),
		agentTasks:     make(map[string]observedAgentTask),
	}
}

func (sub *turnSubscription) attachTransport(t *Transport) {
	sub.mu.Lock()
	sub.transport = t
	sub.mu.Unlock()
}

func (sub *turnSubscription) close() {
	sub.mu.Lock()
	sub.closed = true
	sub.mu.Unlock()
}

func (sub *turnSubscription) handleTransportClose(reason string) {
	if err := sub.ctx.Err(); err != nil {
		sub.finish(sub.loopResult(sub.text.String(), ""), err)
		return
	}
	err := errors.New(firstNonEmpty(strings.TrimSpace(reason), "claude exited before completing the turn"))
	sub.emit(providers.StreamEvent{Type: providers.EventError, Error: err})
	sub.finish(sub.loopResult(sub.text.String(), ""), err)
}

// claudeLine is the parsed top-level shape of one stdout line.
type claudeLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	Message         json.RawMessage `json:"message"`
	Event           json.RawMessage `json:"event"`
	Delta           json.RawMessage `json:"delta"`
	ContentBlock    json.RawMessage `json:"content_block"`
	IsError         bool            `json:"is_error"`
	StopReason      string          `json:"stop_reason"`
	Usage           json.RawMessage `json:"usage"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID string          `json:"parent_tool_use_id"`
	TaskID          string          `json:"task_id"`
	ToolUseID       string          `json:"tool_use_id"`
	TaskType        string          `json:"task_type"`
	Description     string          `json:"description"`
	Status          string          `json:"status"`
}

// handleLine dispatches one stdout line. Unknown top-level types are
// ignored (the raw JSON stays in diagnostics via stderr handlers).
func (sub *turnSubscription) handleLine(line string) {
	if sub.ctx.Err() != nil {
		return
	}
	sub.mu.Lock()
	closed := sub.closed
	sub.mu.Unlock()
	if closed {
		return
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &header); err != nil {
		return
	}
	var envelope claudeLine
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		if header.Type != "result" {
			return
		}
		protocolErr := fmt.Errorf("claude sent an invalid result: %w", err)
		sub.emit(providers.StreamEvent{Type: providers.EventError, Error: protocolErr})
		sub.finish(sub.loopResult(sub.text.String(), ""), protocolErr)
		return
	}
	if envelope.Type == "result" {
		sub.mu.Lock()
		sub.lastRawResult = json.RawMessage(line)
		sub.mu.Unlock()
	}
	switch envelope.Type {
	case "system":
		// Real CLI 2.1.x emits the session id on the first system message
		// (init or hook_started), whichever arrives first. Persist it immediately:
		// waiting for the turn result loses the resume reference if the app exits
		// while Claude is still working.
		if envelope.SessionID != "" {
			sub.observeSessionID(envelope.SessionID)
		}
		if envelope.Subtype == "init" {
			var init initMessage
			if err := json.Unmarshal(envelope.Message, &init); err == nil && init.SessionID != "" {
				sub.observeSessionID(init.SessionID)
			}
		}
		sub.handleTaskEvent(envelope)
	case "assistant":
		sub.handleAssistant(envelope)
	case "stream_event":
		sub.handleStreamEvent(envelope)
	case "user":
		sub.handleUser(envelope)
	case "result":
		sub.handleResult(envelope)
	default:
		// Unknown type: keep going, the result line still terminates.
	}
}

func (sub *turnSubscription) observeSessionID(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	sub.mu.Lock()
	changed := sub.sessionID != sessionID
	sub.sessionID = sessionID
	onSessionID := sub.onSessionID
	sub.mu.Unlock()
	if changed && onSessionID != nil {
		onSessionID(sessionID)
	}
}

func (sub *turnSubscription) handleAssistant(envelope claudeLine) {
	if strings.TrimSpace(envelope.ParentToolUseID) != "" {
		return
	}
	var msg assistantMessage
	if err := json.Unmarshal(envelope.Message, &msg); err != nil {
		return
	}
	for index, block := range msg.Content {
		switch block.Type {
		case "text":
			sub.reconcileText(assistantStreamIndex(sub.streamText, index, block.Text), block.Text)
		case "thinking":
			sub.reconcileThinking(assistantStreamIndex(sub.streamThinking, index, block.Thinking), block.Thinking)
		case "tool_use":
			sub.startTool(index, block)
		}
	}
}

// Partial assistant echoes can omit earlier content blocks, so their slice
// position is not necessarily the original stream index. Match the echo back
// to the streamed block by content before falling back to its local position.
func assistantStreamIndex(streamed map[int]string, fallback int, full string) int {
	bestIndex, bestLength := fallback, -1
	for index, partial := range streamed {
		if partial == "" || (!strings.HasPrefix(full, partial) && !strings.HasPrefix(partial, full)) {
			continue
		}
		if len(partial) > bestLength {
			bestIndex, bestLength = index, len(partial)
		}
	}
	return bestIndex
}

func (sub *turnSubscription) reconcileText(index int, full string) {
	if full == "" {
		return
	}
	streamed, seen := sub.streamText[index]
	if !seen {
		sub.streamText[index] = full
		sub.text.WriteString(full)
		sub.emit(providers.StreamEvent{Type: providers.EventContentDelta, Content: full})
		return
	}
	if strings.HasPrefix(full, streamed) {
		suffix := strings.TrimPrefix(full, streamed)
		if suffix != "" {
			sub.streamText[index] = full
			sub.text.WriteString(suffix)
			sub.emit(providers.StreamEvent{Type: providers.EventContentDelta, Content: suffix})
		}
	}
}

func (sub *turnSubscription) reconcileThinking(index int, full string) {
	if full == "" {
		return
	}
	streamed, seen := sub.streamThinking[index]
	if !seen {
		sub.streamThinking[index] = full
		sub.reasoning.WriteString(full)
		sub.emit(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: full})
		return
	}
	if strings.HasPrefix(full, streamed) {
		suffix := strings.TrimPrefix(full, streamed)
		if suffix != "" {
			sub.streamThinking[index] = full
			sub.reasoning.WriteString(suffix)
			sub.emit(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: suffix})
		}
	}
}

func (sub *turnSubscription) handleStreamEvent(envelope claudeLine) {
	if strings.TrimSpace(envelope.ParentToolUseID) != "" {
		return
	}
	event, ok := decodeStreamEvent(envelope)
	if !ok {
		return
	}
	switch event.Type {
	case "message_start":
		clear(sub.streamText)
		clear(sub.streamThinking)
		clear(sub.toolIDByIndex)
	case "content_block_start":
		if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
			block := *event.ContentBlock
			block.Input = nil
			sub.startTool(event.Index, block)
		}
	case "content_block_delta":
		if event.Delta == nil {
			return
		}
		switch event.Delta.Type {
		case "text_delta":
			if event.Delta.Text != "" {
				sub.streamText[event.Index] += event.Delta.Text
				sub.text.WriteString(event.Delta.Text)
				sub.emit(providers.StreamEvent{Type: providers.EventContentDelta, Content: event.Delta.Text})
			}
		case "thinking_delta":
			if event.Delta.Thinking != "" {
				sub.streamThinking[event.Index] += event.Delta.Thinking
				sub.reasoning.WriteString(event.Delta.Thinking)
				sub.emit(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: event.Delta.Thinking})
			}
		case "input_json_delta":
			if tool := sub.toolAtIndex(event.Index); tool != nil {
				tool.arguments.WriteString(event.Delta.PartialJSON)
			}
		}
	case "message_delta":
		if event.Usage != nil {
			sub.accumulateUsage(*event.Usage)
		}
	}
}

func decodeStreamEvent(envelope claudeLine) (streamEvent, bool) {
	var event streamEvent
	if len(envelope.Event) == 0 {
		return streamEvent{}, false
	}
	if err := json.Unmarshal(envelope.Event, &event); err == nil && event.Type != "" {
		return event, true
	}
	// Keep accepting the early flattened shape used by development stubs.
	if err := json.Unmarshal(envelope.Event, &event.Type); err != nil || event.Type == "" {
		return streamEvent{}, false
	}
	if len(envelope.Delta) > 0 {
		var delta streamEventDelta
		if json.Unmarshal(envelope.Delta, &delta) == nil {
			event.Delta = &delta
		}
	}
	if len(envelope.ContentBlock) > 0 {
		var block assistantContentBlock
		if json.Unmarshal(envelope.ContentBlock, &block) == nil {
			event.ContentBlock = &block
		}
	}
	if len(envelope.Usage) > 0 {
		var usage tokenUsage
		if json.Unmarshal(envelope.Usage, &usage) == nil {
			event.Usage = &usage
		}
	}
	return event, true
}

func (sub *turnSubscription) startTool(index int, block assistantContentBlock) {
	if block.ID == "" {
		return
	}
	if tool := sub.tools[block.ID]; tool != nil {
		if block.Input != nil {
			if args, err := json.Marshal(block.Input); err == nil {
				tool.arguments.Reset()
				tool.arguments.Write(args)
			}
		}
		sub.toolIDByIndex[index] = block.ID
		return
	}
	agentActivity := isClaudeAgentTool(block.Name)
	tool := &pendingTool{id: block.ID, name: block.Name, agentActivity: agentActivity}
	sub.tools[block.ID] = tool
	sub.toolIDByIndex[index] = block.ID
	sub.emit(providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{ID: block.ID, Name: block.Name},
	})
	if block.Input != nil {
		if args, err := json.Marshal(block.Input); err == nil {
			tool.arguments.Write(args)
		}
	}
	if agentActivity {
		sub.emitAgentActivity(block.ID, claudeAgentToolLabel(block.Input), providers.AgentActivityRunning)
	}
}

func (sub *turnSubscription) toolAtIndex(index int) *pendingTool {
	id := sub.toolIDByIndex[index]
	if id == "" {
		return nil
	}
	return sub.tools[id]
}

func (sub *turnSubscription) handleUser(envelope claudeLine) {
	if strings.TrimSpace(envelope.ParentToolUseID) != "" {
		return
	}
	var msg struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	}
	if err := json.Unmarshal(envelope.Message, &msg); err != nil {
		return
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" || sub.tools[block.ToolUseID] == nil {
			continue
		}
		state := providers.AgentActivityCompleted
		if block.IsError {
			state = providers.AgentActivityFailed
		}
		sub.finishToolResult(block.ToolUseID, state, claudeToolResultText(block.Content))
	}
}

func claudeToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var out strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				out.WriteString(block.Text)
			}
		}
		return out.String()
	}
	return string(raw)
}

func (sub *turnSubscription) handleTaskEvent(envelope claudeLine) {
	taskID := strings.TrimSpace(envelope.TaskID)
	if taskID == "" {
		return
	}
	switch envelope.Subtype {
	case "task_started":
		if taskType := strings.TrimSpace(envelope.TaskType); taskType != "" && !strings.Contains(strings.ToLower(taskType), "agent") {
			return
		}
		activityID := firstNonEmpty(envelope.ToolUseID, taskID)
		label := strings.TrimSpace(envelope.Description)
		if label == "" {
			label = "Claude agent"
		}
		sub.agentTasks[taskID] = observedAgentTask{activityID: activityID, label: label}
		sub.emitAgentActivity(activityID, label, providers.AgentActivityRunning)
	case "task_progress":
		task, ok := sub.agentTasks[taskID]
		if !ok {
			return
		}
		if label := strings.TrimSpace(envelope.Description); label != "" {
			task.label = label
			sub.agentTasks[taskID] = task
		}
		sub.emitAgentActivity(task.activityID, task.label, providers.AgentActivityRunning)
	case "task_notification":
		task, ok := sub.agentTasks[taskID]
		if !ok {
			return
		}
		state := claudeTaskTerminalState(envelope.Status)
		sub.emitAgentActivity(task.activityID, task.label, state)
		if state == providers.AgentActivityCompleted || state == providers.AgentActivityFailed {
			delete(sub.agentTasks, taskID)
		}
	}
}

func (sub *turnSubscription) finishTool(toolID string, agentState providers.AgentActivityState) {
	result := "Tool completed"
	if agentState == providers.AgentActivityFailed {
		result = "Tool failed"
	}
	sub.finishToolResult(toolID, agentState, result)
}

func (sub *turnSubscription) finishToolResult(toolID string, agentState providers.AgentActivityState, result string) {
	tool := sub.tools[toolID]
	if tool == nil {
		return
	}
	sub.emit(providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &providers.ToolCall{ID: tool.id, Name: tool.name, Arguments: tool.arguments.String()},
		ToolResult: result,
	})
	if tool.agentActivity && !sub.tracksAgentActivity(tool.id) {
		sub.emitAgentActivity(tool.id, "Claude agent", agentState)
	}
	delete(sub.tools, toolID)
	for index, id := range sub.toolIDByIndex {
		if id == toolID {
			delete(sub.toolIDByIndex, index)
		}
	}
}

func (sub *turnSubscription) tracksAgentActivity(activityID string) bool {
	for _, task := range sub.agentTasks {
		if task.activityID == activityID {
			return true
		}
	}
	return false
}

func (sub *turnSubscription) emitAgentActivity(id, label string, state providers.AgentActivityState) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	sub.emit(providers.StreamEvent{
		Type: providers.EventAgentActivity,
		AgentActivity: &providers.AgentActivity{
			ID:     id,
			Engine: "claude",
			Label:  firstNonEmpty(strings.TrimSpace(label), "Claude agent"),
			State:  state,
		},
	})
}

func claudeTaskTerminalState(status string) providers.AgentActivityState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress":
		return providers.AgentActivityRunning
	case "failed", "error", "errored":
		return providers.AgentActivityFailed
	default:
		return providers.AgentActivityCompleted
	}
}

func isClaudeAgentTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agent", "task":
		return true
	default:
		return false
	}
}

func claudeAgentToolLabel(input any) string {
	fields, ok := input.(map[string]any)
	if !ok {
		return "Claude agent"
	}
	for _, key := range []string{"name", "description", "subagent_type"} {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Claude agent"
}

func (sub *turnSubscription) accumulateUsage(u tokenUsage) {
	sub.usage = providers.TokenUsage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
	}
	sub.emit(providers.StreamEvent{Type: providers.EventUsage, Usage: &sub.usage})
}

func (sub *turnSubscription) handleResult(envelope claudeLine) {
	var res resultMessage
	_ = json.Unmarshal(sub.lastRawResult, &res)
	stopReason := firstNonEmpty(res.StopReason, envelope.StopReason)

	// Close any in-flight tool rendering.
	state := providers.AgentActivityCompleted
	if envelope.IsError {
		state = providers.AgentActivityFailed
	}
	for toolID := range sub.tools {
		sub.finishTool(toolID, state)
	}
	if res.Usage != nil {
		sub.accumulateUsage(*res.Usage)
	} else if len(envelope.Usage) > 0 {
		var usage tokenUsage
		if json.Unmarshal(envelope.Usage, &usage) == nil {
			sub.accumulateUsage(usage)
		}
	}
	if envelope.IsError {
		message := "claude turn failed"
		if res.Error != nil && strings.TrimSpace(res.Error.Message) != "" {
			message = res.Error.Message
		} else if strings.TrimSpace(res.Result) != "" {
			message = res.Result
		}
		err := errors.New(message)
		sub.emit(providers.StreamEvent{Type: providers.EventError, Error: err})
		sub.finish(sub.loopResult(sub.text.String(), stopReason), err)
		return
	}
	content := sub.reconcileResultText(res.Result)
	sub.emit(providers.StreamEvent{
		Type:         providers.EventDone,
		FinishReason: providers.FinishReasonStop,
		StopReason:   stopReason,
	})
	sub.finish(sub.loopResult(content, stopReason), nil)
}

func (sub *turnSubscription) reconcileResultText(full string) string {
	full = strings.TrimSpace(full)
	streamed := sub.text.String()
	if full == "" {
		return streamed
	}
	if streamed == "" {
		sub.text.WriteString(full)
		sub.emit(providers.StreamEvent{Type: providers.EventContentDelta, Content: full})
		return full
	}
	if strings.HasPrefix(full, streamed) {
		suffix := strings.TrimPrefix(full, streamed)
		if suffix != "" {
			sub.text.WriteString(suffix)
			sub.emit(providers.StreamEvent{Type: providers.EventContentDelta, Content: suffix})
		}
		return full
	}
	if full != streamed {
		sub.text.Reset()
		sub.text.WriteString(full)
		sub.emit(providers.StreamEvent{Type: providers.EventContentReplace, Content: full})
	}
	return full
}

func (sub *turnSubscription) loopResult(content, stopReason string) agent.LoopResult {
	result := agent.LoopResult{
		Content:             strings.TrimSpace(content),
		FinishReason:        providers.FinishReasonStop,
		StopReason:          stopReason,
		InputTokens:         sub.usage.InputTokens,
		OutputTokens:        sub.usage.OutputTokens,
		CacheCreationTokens: sub.usage.CacheCreationTokens,
		CacheReadTokens:     sub.usage.CacheReadTokens,
	}
	if content != "" || sub.reasoning.Len() > 0 {
		result.NewMessages = []providers.ChatMessage{{
			Role:             "assistant",
			Content:          content,
			ReasoningContent: sub.reasoning.String(),
		}}
	}
	return result
}

func (sub *turnSubscription) emit(ev providers.StreamEvent) {
	sub.mu.Lock()
	closed := sub.closed
	sub.mu.Unlock()
	if !closed && sub.sink != nil {
		sub.sink(ev)
	}
}

func (sub *turnSubscription) finish(result agent.LoopResult, err error) {
	sub.finishOnce.Do(func() {
		sub.mu.Lock()
		sub.closed = true
		sub.mu.Unlock()
		select {
		case sub.done <- turnOutcome{result: agentengine.TurnResult{Result: result}, err: err}:
		default:
		}
	})
}
