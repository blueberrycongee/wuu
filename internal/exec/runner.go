package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type runState struct {
	threadID            string
	turnID              string
	runID               string
	finalMessage        string
	tracePath           string
	status              string
	commandItems        map[string]appserver.ThreadItem
	toolOutputs         map[string]string
	seenSubagents       map[string]bool
	structuredResult    any
	structuredResultSet bool
}

func Run(ctx context.Context, opts Options) (runErr error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	ctx, cancelOutput := context.WithCancel(ctx)
	defer cancelOutput()
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	stdout := &outputWriter{w: opts.Stdout, ctx: ctx, cancel: cancelOutput}
	opts.Stdout = stdout
	defer func() {
		if stdout.err != nil {
			err := fmt.Errorf("write exec output: %w", stdout.err)
			if errors.Is(stdout.err, context.DeadlineExceeded) || errors.Is(stdout.err, context.Canceled) {
				runErr = classifyProtocolOrContextError(ctx, err)
			} else {
				runErr = WithExitCode(ExitProtocol, err)
			}
		} else if runErr == nil && ctx.Err() != nil {
			runErr = classifyProtocolOrContextError(ctx, ctx.Err())
		}
	}()

	state := runState{status: "running"}
	if ctx.Err() != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, ctx.Err()))
	}
	restoreEnv, err := applyRunEnv(opts.Env)
	if err != nil {
		return finishRunError(opts, &state, WithExitCode(ExitInvalidInput, err))
	}
	defer restoreEnv()

	attachments, err := resolveRunAttachments(opts)
	if err != nil {
		return finishRunError(opts, &state, WithExitCode(ExitInvalidInput, err))
	}
	if strings.TrimSpace(opts.Prompt) == "" && attachments.Empty() {
		return finishRunError(opts, &state, WithExitCode(ExitInvalidInput, errors.New("prompt is required")))
	}

	rootDir, err := resolveWorkdir(opts.Workdir)
	if err != nil {
		return finishRunError(opts, &state, WithExitCode(ExitInvalidInput, err))
	}
	outputSchema, err := loadOutputSchema(rootDir, opts.OutputSchemaPath)
	if err != nil {
		return finishRunError(opts, &state, WithExitCode(ExitInvalidInput, err))
	}

	controller := opts.Controller
	if controller == nil {
		controller, err = NewLocalAppServerController(ctx, opts)
		if err != nil {
			if ctx.Err() != nil {
				return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, err))
			}
			return finishRunError(opts, &state, classifySetupError(err))
		}
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = controller.Shutdown(shutdownCtx)
	}()

	initResult, err := controller.Initialize(ctx)
	if err != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, err))
	}
	emitSessionConfigured(opts, initResult)
	if ctx.Err() != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, ctx.Err()))
	}

	thread, err := startOrResumeThread(ctx, controller, opts)
	if err != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, err))
	}
	state.threadID = thread.ID
	switch {
	case strings.TrimSpace(opts.ForkID) != "":
		emitThreadEvent(opts, "thread_forked", thread)
	case opts.ResumeLast || strings.TrimSpace(opts.ResumeID) != "":
		emitThreadEvent(opts, "thread_resumed", thread)
	default:
		emitThreadEvent(opts, "thread_started", thread)
	}

	if ctx.Err() != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, ctx.Err()))
	}
	input := TurnInput{Prompt: opts.Prompt, Images: attachments.Images, Files: attachments.Files}
	run, err := controller.StartRun(ctx, runStartParams(opts, thread.ID, input, outputSchema))
	if err != nil {
		return finishRunError(opts, &state, classifyProtocolOrContextError(ctx, err))
	}
	state.runID = run.ID
	if len(run.Turns) > 0 {
		state.turnID = run.Turns[len(run.Turns)-1].TurnID
	}
	if err := waitForRun(ctx, controller, opts, &state); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = interruptRunBestEffort(controller, state.runID, "timeout")
			emitTurnInterrupted(opts, state, "timeout")
			emitResult(opts, state, "timeout", "timeout")
			return WithExitCode(ExitTimeout, err)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			_ = interruptRunBestEffort(controller, state.runID, "interrupted")
			emitTurnInterrupted(opts, state, "interrupted")
			emitResult(opts, state, "interrupted", "interrupted")
			return WithExitCode(ExitInterrupted, err)
		}
		_ = interruptRunBestEffort(controller, state.runID, "exec_failed")
		return finishRunError(opts, &state, err)
	}

	if outputSchema != nil {
		// The app-server already validated and retried the final message
		// inside the Run; this parse only fills the structured_result
		// payload. A mismatch means settlement and streamed content disagree.
		structuredResult, validateErr := outputSchema.validate(state.finalMessage)
		if validateErr != nil {
			state.status = "failed"
			emitStructuredOutputValidation(opts, state, validateErr, false)
			emitResult(opts, state, "failed", validateErr.Error())
			return WithExitCode(ExitTurnFailed, validateErr)
		}
		state.structuredResult = structuredResult
		state.structuredResultSet = true
	}

	if opts.OutputLastMessage != "" {
		if err := writeLastMessage(opts.OutputLastMessage, state.finalMessage); err != nil {
			return finishRunError(opts, &state, WithExitCode(ExitTurnFailed, err))
		}
	}
	state.status = "completed"
	emitResult(opts, state, "completed", "")
	if !opts.JSON {
		if state.tracePath != "" {
			fmt.Fprintf(opts.Stderr, "trace_path: %s\n", state.tracePath)
		}
		if state.finalMessage != "" {
			fmt.Fprintln(opts.Stdout, state.finalMessage)
		}
	}
	return nil
}

func applyRunEnv(entries []string) (func(), error) {
	type priorValue struct {
		value string
		ok    bool
	}
	prior := make(map[string]priorValue)
	restore := func() {
		for key, old := range prior {
			if old.ok {
				_ = os.Setenv(key, old.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			restore()
			return func() {}, fmt.Errorf("--env must be KEY=VALUE")
		}
		if _, seen := prior[key]; !seen {
			old, existed := os.LookupEnv(key)
			prior[key] = priorValue{value: old, ok: existed}
		}
		if err := os.Setenv(key, value); err != nil {
			restore()
			return func() {}, fmt.Errorf("set env %s: %w", key, err)
		}
	}
	return restore, nil
}

func startOrResumeThread(ctx context.Context, controller Controller, opts Options) (appserver.Thread, error) {
	if id := strings.TrimSpace(opts.ForkID); id != "" {
		return controller.ForkThread(ctx, id)
	}
	if opts.ResumeLast {
		return controller.ResumeThread(ctx, "")
	}
	if id := strings.TrimSpace(opts.ResumeID); id != "" {
		return controller.ResumeThread(ctx, id)
	}
	return controller.StartThread(ctx, opts.Ephemeral)
}

func runStartParams(opts Options, threadID string, input TurnInput, outputSchema *outputSchemaValidator) appserver.RunStartParams {
	request := execution.Request{
		Mode: execution.ModeStart,
		Requested: execution.Selection{
			Provider: strings.TrimSpace(opts.Provider), Model: strings.TrimSpace(opts.Model),
			Variant: strings.TrimSpace(opts.Variant), Effort: strings.TrimSpace(opts.Effort),
			PermissionMode: strings.TrimSpace(opts.PermissionMode),
		},
		AgentProfile: strings.TrimSpace(opts.AgentProfile), MaxTurns: opts.MaxTurns,
		TimeoutMS: opts.Timeout.Milliseconds(), NoTools: opts.NoTools,
		HasPrompt: input.Prompt != "", ImageCount: len(input.Images), FileCount: len(input.Files),
		StructuredOutput: outputSchema != nil,
	}
	if strings.TrimSpace(opts.ForkID) != "" {
		request.Mode = execution.ModeFork
		request.SourceThreadID = strings.TrimSpace(opts.ForkID)
	} else if opts.ResumeLast || strings.TrimSpace(opts.ResumeID) != "" {
		request.Mode = execution.ModeResume
		request.SourceThreadID = threadID
	}
	permissionMode := strings.TrimSpace(opts.PermissionMode)
	var permission *string
	if permissionMode != "" {
		permission = &permissionMode
	}
	return appserver.RunStartParams{
		ThreadID: threadID, Prompt: input.Prompt,
		Images:         append([]appserver.TurnStartImage(nil), input.Images...),
		Files:          append([]appserver.TurnStartFile(nil), input.Files...),
		PermissionMode: permission, Request: request,
		OutputSchema: outputSchemaRaw(outputSchema),
	}
}

func outputSchemaRaw(schema *outputSchemaValidator) json.RawMessage {
	if schema == nil {
		return nil
	}
	return append(json.RawMessage(nil), schema.raw...)
}

func waitForRun(ctx context.Context, controller Controller, opts Options, state *runState) error {
	notifications := controller.Notifications()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case notification, ok := <-notifications:
			if !ok {
				return WithExitCode(ExitProtocol, errors.New("app-server notification stream closed before Run completed"))
			}
			if notification.Err != nil {
				return WithExitCode(ExitProtocol, notification.Err)
			}
			done, err := handleNotification(opts, notification, state)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func handleNotification(opts Options, notification Notification, state *runState) (bool, error) {
	switch notification.Method {
	case appserver.NotificationTurnStarted:
		var params appserver.TurnStartedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		if state == nil || (state.threadID != "" && params.ThreadID != state.threadID) {
			return false, nil
		}
		state.turnID = params.Turn.ID
		state.finalMessage = ""
		state.status = "running"
		state.commandItems = nil
		state.toolOutputs = nil
		emitTurnStarted(opts, params.ThreadID, params.Turn)
	case appserver.NotificationAgentMessageDelta:
		var params appserver.AgentMessageDeltaNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		if isCurrentTurn(state, params.ThreadID, params.TurnID) {
			state.finalMessage += params.Delta
			emitJSON(opts, map[string]any{"type": "agent_message_delta", "thread_id": params.ThreadID, "turn_id": params.TurnID, "delta": params.Delta})
		}
	case appserver.NotificationAgentMessageReplace:
		var params appserver.AgentMessageReplaceNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		if isCurrentTurn(state, params.ThreadID, params.TurnID) {
			state.finalMessage = params.Text
			emitJSON(opts, map[string]any{"type": "agent_message_final", "thread_id": params.ThreadID, "turn_id": params.TurnID, "message": params.Text})
		}
	case appserver.NotificationReasoningDelta, appserver.NotificationReasoningReplace:
		// App-server reasoning items are the desktop UI surface for provider
		// thinking. Providers do not reliably distinguish a safe reasoning
		// summary from hidden chain-of-thought on this notification path, so the
		// automation boundary must not forward either payload to JSONL.
		return false, nil
	case appserver.NotificationItemStarted:
		var params appserver.ItemStartedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitItemStarted(opts, params, state)
	case appserver.NotificationItemCompleted:
		var params appserver.ItemCompletedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitItemCompleted(opts, params, state)
	case appserver.NotificationItemRemoved:
		var params appserver.ItemRemovedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitItemRemoved(opts, params, state)
	case appserver.NotificationToolCallOutput:
		var params appserver.ToolCallOutputNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		state.appendToolOutput(params.ItemID, params.Delta)
		emitJSON(opts, map[string]any{"type": "tool_output_delta", "thread_id": params.ThreadID, "turn_id": params.TurnID, "item_id": params.ItemID, "delta": params.Delta})
		emitCommandOutputDelta(opts, params, state)
	case appserver.NotificationAgentUpdated:
		var params appserver.AgentUpdatedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitSubagentUpdated(opts, params, state)
	case appserver.NotificationTurnUsage:
		var params appserver.TurnUsageNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitJSON(opts, map[string]any{"type": "usage_updated", "thread_id": params.ThreadID, "turn_id": params.TurnID, "input_tokens": params.InputTokens, "output_tokens": params.OutputTokens, "cache_creation_tokens": params.CacheCreationTokens, "cache_read_tokens": params.CacheReadTokens})
	case appserver.NotificationTurnEvent:
		var params appserver.TurnEventNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitTurnStreamEvent(opts, params)
	case appserver.NotificationTurnCompleted:
		var params appserver.TurnCompletedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		emitJSON(opts, map[string]any{"type": "turn_completed", "thread_id": params.ThreadID, "turn_id": params.Turn.ID, "input_tokens": params.InputTokens, "output_tokens": params.OutputTokens, "trace_path": params.TracePath, "awaiting_auto_continuation": params.AwaitingAutoContinuation})
		if !isCurrentTurn(state, params.ThreadID, params.Turn.ID) {
			return false, nil
		}
		if params.Content != "" {
			state.finalMessage = params.Content
		}
		state.threadID = params.ThreadID
		state.turnID = params.Turn.ID
		state.tracePath = params.TracePath
		// A Run fans out multiple turns (structured-output retries, automatic
		// continuations); only the Run's terminal run/updated ends the wait.
		state.status = "running"
		return false, nil
	case appserver.NotificationRunUpdated:
		var params appserver.RunUpdatedNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		if state == nil || state.runID == "" || params.Run.ID != state.runID {
			return false, nil
		}
		if params.Run.Result != nil {
			state.tracePath = params.Run.Result.TracePath
			if state.turnID == "" {
				state.turnID = params.Run.Result.FinalTurnID
			}
		}
		switch params.Run.Status {
		case "completed":
			state.status = "completed"
			return true, nil
		case "timed_out":
			state.status = "timeout"
			errText := "execution run timed out"
			if params.Run.Error != nil && params.Run.Error.Message != "" {
				errText = params.Run.Error.Message
			}
			emitResult(opts, *state, "timeout", errText)
			return false, WithExitCode(ExitTimeout, errors.New(errText))
		case "interrupted", "cancelled":
			state.status = "interrupted"
			errText := "execution run interrupted"
			if params.Run.Error != nil && params.Run.Error.Message != "" {
				errText = params.Run.Error.Message
			}
			emitResult(opts, *state, "interrupted", errText)
			return false, WithExitCode(ExitInterrupted, errors.New(errText))
		case "failed":
			status := "failed"
			exitCode := runExitCode(params.Run)
			if exitCode == ExitPermissionDenied {
				status = "permission_denied"
			}
			state.status = status
			errText := "execution run failed"
			if params.Run.Error != nil && params.Run.Error.Message != "" {
				errText = params.Run.Error.Message
			}
			emitResult(opts, *state, status, errText)
			return false, WithExitCode(exitCode, errors.New(errText))
		}
	case appserver.NotificationTurnError:
		var params appserver.TurnErrorNotification
		if err := decodeNotification(notification, &params); err != nil {
			return false, err
		}
		// The core reports interrupts through turn/error with an embedded
		// Turn.Status; surface them as turn_interrupted rather than
		// turn_failed so `wuu exec --json` distinguishes a cancel from a
		// genuine failure. The process exit classification arrives with the
		// Run's terminal run/updated, so a turn error never ends the wait.
		if params.Turn.Status == appserver.TurnStatusInterrupted {
			emitJSON(opts, map[string]any{"type": "turn_interrupted", "thread_id": params.ThreadID, "turn_id": params.TurnID, "reason": "interrupted"})
			if !isCurrentTurn(state, params.ThreadID, params.TurnID) {
				return false, nil
			}
			state.threadID = params.ThreadID
			state.turnID = params.TurnID
			state.status = "interrupted"
			return false, nil
		}
		emitJSON(opts, map[string]any{"type": "turn_failed", "thread_id": params.ThreadID, "turn_id": params.TurnID, "error": params.Error})
		if !isCurrentTurn(state, params.ThreadID, params.TurnID) {
			return false, nil
		}
		state.threadID = params.ThreadID
		state.turnID = params.TurnID
		state.status = "failed"
		return false, nil
	}
	return false, nil
}

func isCurrentTurn(state *runState, threadID, turnID string) bool {
	if state == nil {
		return false
	}
	if state.threadID != "" && threadID != state.threadID {
		return false
	}
	if state.turnID != "" && turnID != state.turnID {
		return false
	}
	return true
}

func emitSessionConfigured(opts Options, result appserver.InitializeResult) {
	if opts.JSON {
		emitJSON(opts, map[string]any{
			"type":             "session_configured",
			"protocol_version": result.ProtocolVersion,
			"provider":         result.Provider,
			"model":            result.Model,
			"effort":           result.Effort,
			"variant":          result.Variant,
			"max_parallel":     result.MaxParallel,
			"workspace_root":   result.WorkspaceRoot,
			"permissions":      result.Permissions,
		})
		return
	}
	fmt.Fprintf(opts.Stderr, "provider: %s\nmodel: %s\nworkspace: %s\n", result.Provider, result.Model, result.WorkspaceRoot)
	if result.Permissions.Mode != "" {
		fmt.Fprintf(opts.Stderr, "permission_mode: %s\n", result.Permissions.Mode)
	}
}

func emitThreadEvent(opts Options, eventType string, thread appserver.Thread) {
	if opts.JSON {
		emitJSON(opts, map[string]any{"type": eventType, "thread_id": thread.ID, "model": thread.Model, "provider": thread.ModelProvider, "cwd": thread.CWD})
		return
	}
	fmt.Fprintf(opts.Stderr, "thread_id: %s\n", thread.ID)
}

func emitTurnStarted(opts Options, threadID string, turn appserver.Turn) {
	if opts.JSON {
		emitJSON(opts, map[string]any{"type": "turn_started", "thread_id": threadID, "turn_id": turn.ID})
		return
	}
	fmt.Fprintf(opts.Stderr, "turn_id: %s\n", turn.ID)
}

func emitItemStarted(opts Options, params appserver.ItemStartedNotification, state *runState) {
	switch params.Item.Type {
	case appserver.ThreadItemToolCall:
		safeItem := params.Item
		safeItem.Arguments = tools.RedactToolOutput(safeItem.Arguments)
		emitJSON(opts, map[string]any{"type": "tool_started", "thread_id": params.ThreadID, "turn_id": params.TurnID, "item_id": safeItem.ID, "name": safeItem.Name, "arguments": safeItem.Arguments})
		if isCommandTool(safeItem.Name) {
			state.rememberCommandItem(safeItem)
			emitJSON(opts, commandEventPayload("command_started", params.ThreadID, params.TurnID, safeItem))
		}
		if !opts.JSON && params.Item.Name != "" {
			fmt.Fprintf(opts.Stderr, "tool_started: %s\n", params.Item.Name)
		}
	}
}

func emitItemCompleted(opts Options, params appserver.ItemCompletedNotification, state *runState) {
	switch params.Item.Type {
	case appserver.ThreadItemToolCall:
		item := params.Item
		if item.Result == "" {
			item.Result = state.toolOutput(item.ID)
		}
		payload := map[string]any{"type": "tool_completed", "thread_id": params.ThreadID, "turn_id": params.TurnID, "item_id": item.ID, "name": item.Name, "status": item.Status, "error": item.Error}
		emitJSON(opts, payload)
		if isCommandTool(item.Name) {
			commandPayload := commandEventPayload("command_completed", params.ThreadID, params.TurnID, item)
			commandPayload["status"] = item.Status
			commandPayload["error"] = item.Error
			emitJSON(opts, commandPayload)
			state.forgetCommandItem(item.ID)
		}
		for _, event := range fileChangeEventsFromToolResult(params.ThreadID, params.TurnID, item) {
			emitJSON(opts, event)
		}
		if !opts.JSON && params.Item.Name != "" {
			fmt.Fprintf(opts.Stderr, "tool_completed: %s\n", params.Item.Name)
		}
	}
}

func emitItemRemoved(opts Options, params appserver.ItemRemovedNotification, state *runState) {
	item, isCommand := state.commandItem(params.ItemID)
	emitJSON(opts, map[string]any{"type": "tool_removed", "thread_id": params.ThreadID, "turn_id": params.TurnID, "item_id": params.ItemID})
	if isCommand {
		emitJSON(opts, map[string]any{"type": "command_removed", "thread_id": params.ThreadID, "turn_id": params.TurnID, "item_id": params.ItemID, "name": item.Name})
	}
	state.forgetItem(params.ItemID)
}

func emitCommandOutputDelta(opts Options, params appserver.ToolCallOutputNotification, state *runState) {
	item, ok := state.commandItem(params.ItemID)
	if !ok {
		return
	}
	payload := commandEventPayload("command_output_delta", params.ThreadID, params.TurnID, item)
	payload["delta"] = params.Delta
	emitJSON(opts, payload)
}

func emitSubagentUpdated(opts Options, params appserver.AgentUpdatedNotification, state *runState) {
	agent := params.Agent
	if strings.TrimSpace(agent.ID) == "" {
		return
	}
	state.ensureMaps()
	eventType := "subagent_updated"
	if !state.seenSubagents[agent.ID] {
		eventType = "subagent_started"
		state.seenSubagents[agent.ID] = true
	}
	if isTerminalAgentStatus(agent.Status) {
		eventType = "subagent_completed"
	}
	payload := map[string]any{
		"type":                  eventType,
		"thread_id":             params.ThreadID,
		"agent_id":              agent.ID,
		"agent_type":            agent.Type,
		"status":                agent.Status,
		"task_name":             agent.TaskName,
		"agent_profile":         agent.AgentProfile,
		"agent_path":            agent.AgentPath,
		"parent_id":             agent.ParentID,
		"description":           agent.Description,
		"result":                agent.Result,
		"result_path":           agent.ResultPath,
		"result_bytes":          agent.ResultBytes,
		"result_truncated":      agent.ResultTruncated,
		"error":                 agent.Error,
		"input_tokens":          agent.InputTokens,
		"output_tokens":         agent.OutputTokens,
		"cache_creation_tokens": agent.CacheCreationTokens,
		"cache_read_tokens":     agent.CacheReadTokens,
	}
	emitJSON(opts, payload)
}

func emitTurnInterrupted(opts Options, state runState, reason string) {
	emitJSON(opts, map[string]any{
		"type":      "turn_interrupted",
		"thread_id": state.threadID,
		"turn_id":   state.turnID,
		"reason":    reason,
	})
}

func (s *runState) ensureMaps() {
	if s.commandItems == nil {
		s.commandItems = make(map[string]appserver.ThreadItem)
	}
	if s.toolOutputs == nil {
		s.toolOutputs = make(map[string]string)
	}
	if s.seenSubagents == nil {
		s.seenSubagents = make(map[string]bool)
	}
}

func (s *runState) rememberCommandItem(item appserver.ThreadItem) {
	if strings.TrimSpace(item.ID) == "" {
		return
	}
	s.ensureMaps()
	s.commandItems[item.ID] = item
}

func (s *runState) commandItem(itemID string) (appserver.ThreadItem, bool) {
	if s == nil || s.commandItems == nil {
		return appserver.ThreadItem{}, false
	}
	item, ok := s.commandItems[itemID]
	return item, ok
}

func (s *runState) forgetCommandItem(itemID string) {
	if s == nil || s.commandItems == nil {
		return
	}
	delete(s.commandItems, itemID)
}

func (s *runState) forgetItem(itemID string) {
	if s == nil {
		return
	}
	if s.commandItems != nil {
		delete(s.commandItems, itemID)
	}
	if s.toolOutputs != nil {
		delete(s.toolOutputs, itemID)
	}
}

func (s *runState) appendToolOutput(itemID, delta string) {
	if strings.TrimSpace(itemID) == "" || delta == "" {
		return
	}
	s.ensureMaps()
	s.toolOutputs[itemID] += delta
}

func (s *runState) toolOutput(itemID string) string {
	if s == nil || s.toolOutputs == nil {
		return ""
	}
	return s.toolOutputs[itemID]
}

func isCommandTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "bash":
		return true
	default:
		return false
	}
}

func commandEventPayload(eventType, threadID, turnID string, item appserver.ThreadItem) map[string]any {
	payload := map[string]any{
		"type":      eventType,
		"thread_id": threadID,
		"turn_id":   turnID,
		"item_id":   item.ID,
		"name":      item.Name,
		"arguments": item.Arguments,
	}
	for _, key := range []string{"command", "process_id"} {
		if value := stringArgument(item.Arguments, key); value != "" {
			payload[key] = value
		}
	}
	return payload
}

func stringArgument(args, key string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func isTerminalAgentStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func fileChangeEventsFromToolResult(threadID, turnID string, item appserver.ThreadItem) []map[string]any {
	if strings.TrimSpace(item.Result) == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(item.Result), &payload); err != nil {
		return nil
	}
	switch strings.TrimSpace(item.Name) {
	case "write_file", "edit_file":
		event := fileChangeEventBase(threadID, turnID, item, payload)
		if path, _ := payload["path"].(string); strings.TrimSpace(path) != "" {
			event["path"] = strings.TrimSpace(path)
			return []map[string]any{event}
		}
	case "apply_patch":
		return applyPatchFileChangeEvents(threadID, turnID, item, payload)
	case "checkpoint":
		return checkpointFileChangeEvents(threadID, turnID, item, payload)
	}
	return nil
}

func fileChangeEventBase(threadID, turnID string, item appserver.ThreadItem, result map[string]any) map[string]any {
	event := map[string]any{
		"type":      "file_changed",
		"thread_id": threadID,
		"turn_id":   turnID,
		"item_id":   item.ID,
		"tool_name": item.Name,
	}
	for _, key := range []string{"action", "old_file_sha", "new_file_sha", "workspace_revision", "patch_journal_path", "manifest_path"} {
		if value, _ := result[key].(string); strings.TrimSpace(value) != "" {
			event[key] = strings.TrimSpace(value)
		}
	}
	return event
}

func applyPatchFileChangeEvents(threadID, turnID string, item appserver.ThreadItem, result map[string]any) []map[string]any {
	files, ok := result["files"].([]any)
	if !ok {
		return fileChangeEventsFromChangedFiles(threadID, turnID, item, result)
	}
	events := make([]map[string]any, 0, len(files))
	for _, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		event := fileChangeEventBase(threadID, turnID, item, result)
		copyStringField(event, file, "path")
		copyStringField(event, file, "move_path")
		copyStringField(event, file, "action")
		copyStringField(event, file, "old_file_sha")
		copyStringField(event, file, "new_file_sha")
		if _, ok := event["path"]; !ok {
			continue
		}
		events = append(events, event)
	}
	return events
}

func fileChangeEventsFromChangedFiles(threadID, turnID string, item appserver.ThreadItem, result map[string]any) []map[string]any {
	changed, ok := result["changed_files"].([]any)
	if !ok {
		return nil
	}
	events := make([]map[string]any, 0, len(changed))
	for _, raw := range changed {
		path, _ := raw.(string)
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		event := fileChangeEventBase(threadID, turnID, item, result)
		event["path"] = path
		events = append(events, event)
	}
	return events
}

func checkpointFileChangeEvents(threadID, turnID string, item appserver.ThreadItem, result map[string]any) []map[string]any {
	restored, ok := result["restored_files"].([]any)
	if !ok {
		return nil
	}
	events := make([]map[string]any, 0, len(restored))
	for _, raw := range restored {
		file, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path, _ := file["path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		event := fileChangeEventBase(threadID, turnID, item, result)
		event["path"] = path
		copyStringField(event, file, "action")
		events = append(events, event)
	}
	return events
}

func copyStringField(dst map[string]any, src map[string]any, key string) {
	value, _ := src[key].(string)
	if strings.TrimSpace(value) != "" {
		dst[key] = strings.TrimSpace(value)
	}
}

// runExitCode trusts the exit code the app-server settled into the Run
// manifest, falling back to the shared settlement mapping only when the
// record predates server-side classification.
func runExitCode(run appserver.Run) int {
	if run.Result != nil && run.Result.ExitCode != 0 {
		return run.Result.ExitCode
	}
	return execution.ExitCodeForSettlement(execution.Status(run.Status), run.Error)
}

func isProviderModelFailure(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{
		"provider",
		"model not found",
		"unsupported model",
		"unknown model",
		"no model",
		"api key",
		"auth token",
		"unsupported wire",
		"unsupported api",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func emitTurnStreamEvent(opts Options, params appserver.TurnEventNotification) {
	switch params.Event.Type {
	case "todo_update":
		emitJSON(opts, map[string]any{"type": "todo_updated", "thread_id": params.ThreadID, "turn_id": params.TurnID, "todo": params.Event.TodoUpdate})
	case "request_context":
		rc := params.Event.RequestContext
		if rc == nil {
			return
		}
		payload := map[string]any{
			"type":                        "request_context",
			"thread_id":                   params.ThreadID,
			"turn_id":                     params.TurnID,
			"step_index":                  rc.StepIndex,
			"transient_messages":          rc.TransientMessages,
			"content_bytes":               rc.ContentBytes,
			"block_kinds":                 rc.BlockKinds,
			"block_kind_counts":           rc.BlockKindCounts,
			"block_kind_bytes":            rc.BlockKindBytes,
			"segment_lifecycle_counts":    rc.SegmentLifecycleCounts,
			"segment_placement_counts":    rc.SegmentPlacementCounts,
			"segment_cache_policy_counts": rc.SegmentCachePolicyCounts,
			"message_count":               rc.MessageCount,
			"system_messages":             rc.SystemMessages,
			"hidden_messages":             rc.HiddenMessages,
			"tool_count":                  rc.ToolCount,
			"stable_prefix":               rc.StablePrefix,
			"turn_prefix":                 rc.TurnPrefix,
			"dynamic_context_bytes":       rc.DynamicBytes,
			"system_bytes":                rc.SystemBytes,
			"stable_prefix_bytes":         rc.StablePrefixBytes,
			"turn_prefix_bytes":           rc.TurnPrefixBytes,
			"message_bytes":               rc.MessageBytes,
			"tool_schema_bytes":           rc.ToolSchemaBytes,
			"loadable_tool_count":         rc.LoadableToolCount,
			"loadable_tool_schema_bytes":  rc.LoadableToolSchemaBytes,
			"loadable_tool_surface_hash":  rc.LoadableToolSurfaceHash,
			"system_hash":                 rc.SystemHash,
			"stable_prefix_hash":          rc.StablePrefixHash,
			"turn_prefix_hash":            rc.TurnPrefixHash,
			"tool_surface_hash":           rc.ToolSurfaceHash,
			"prompt_cache_key":            rc.PromptCacheKey,
		}
		if len(rc.SystemSections) > 0 {
			payload["system_sections"] = rc.SystemSections
		}
		emitJSON(opts, payload)
	case "provider_state":
		state := params.Event.ProviderState
		if state == nil {
			return
		}
		emitJSON(opts, map[string]any{
			"type":                      "provider_state",
			"thread_id":                 params.ThreadID,
			"turn_id":                   params.TurnID,
			"step_index":                state.StepIndex,
			"provider":                  state.Provider,
			"protocol":                  state.Protocol,
			"transport":                 state.Transport,
			"replay_mode":               state.ReplayMode,
			"previous_response_id_used": state.PreviousResponseIDUsed,
			"connection_reused":         state.ConnectionReused,
			"diagnostic":                state.Diagnostic,
			"transport_failure_phase":   state.TransportFailurePhase,
			"fallback_transport":        state.FallbackTransport,
			"events_emitted":            state.EventsEmitted,
			"fallback_active":           state.FallbackActive,
			"fallback_reason":           state.FallbackReason,
			"fallback_pin_status":       state.FallbackPinStatus,
			"fallback_retry_after_ms":   state.FallbackRetryAfterMS,
			"fallback_ttl_ms":           state.FallbackTTLMS,
			"input_items":               state.InputItems,
			"full_input_items":          state.FullInputItems,
			"delta_input_items":         state.DeltaInputItems,
		})
	}
}

func emitStructuredOutputValidation(opts Options, state runState, err error, retrying bool) {
	if opts.JSON {
		emitJSON(opts, map[string]any{
			"type":      "error",
			"thread_id": state.threadID,
			"turn_id":   state.turnID,
			"run_id":    state.runID,
			"error":     err.Error(),
			"retrying":  retrying,
		})
		return
	}
	if retrying {
		fmt.Fprintf(opts.Stderr, "structured_output_validation_failed: %v; retrying\n", err)
		return
	}
	fmt.Fprintf(opts.Stderr, "structured_output_validation_failed: %v\n", err)
}

func emitResult(opts Options, state runState, status, errorText string) {
	if !opts.JSON {
		return
	}
	payload := map[string]any{
		"type":          "result",
		"status":        status,
		"thread_id":     state.threadID,
		"turn_id":       state.turnID,
		"run_id":        state.runID,
		"final_message": state.finalMessage,
		"trace_path":    state.tracePath,
	}
	if errorText != "" {
		payload["error"] = errorText
	}
	if state.structuredResultSet {
		payload["structured_result"] = state.structuredResult
	}
	emitJSON(opts, payload)
}

func finishRunError(opts Options, state *runState, err error) error {
	if err == nil {
		return nil
	}
	if state == nil {
		state = &runState{}
	}
	switch state.status {
	case "failed", "permission_denied", "timeout", "interrupted":
		return err
	}
	status := "failed"
	switch ExitCode(err) {
	case ExitPermissionDenied:
		status = "permission_denied"
	case ExitTimeout:
		status = "timeout"
	case ExitInterrupted:
		status = "interrupted"
	}
	state.status = status
	emitResult(opts, *state, status, err.Error())
	return err
}

func emitJSON(opts Options, payload map[string]any) {
	if !opts.JSON || opts.Stdout == nil {
		return
	}
	enc := json.NewEncoder(opts.Stdout)
	_ = enc.Encode(payload)
}

func decodeNotification(notification Notification, dst any) error {
	if len(notification.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(notification.Params, dst); err != nil {
		return WithExitCode(ExitProtocol, fmt.Errorf("decode %s notification: %w", notification.Method, err))
	}
	return nil
}

func classifySetupError(err error) error {
	if err == nil {
		return nil
	}
	if isProviderModelFailure(err.Error()) {
		return WithExitCode(ExitProviderModelError, err)
	}
	return WithExitCode(ExitInvalidInput, err)
}

func classifyProtocolOrContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return WithExitCode(ExitTimeout, err)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return WithExitCode(ExitInterrupted, err)
	}
	// A thread that already has a running turn is a conflict the caller can
	// act on (wait, or target another thread), not a protocol fault.
	var protocolErr *ProtocolError
	if errors.As(err, &protocolErr) && strings.EqualFold(strings.TrimSpace(protocolErr.Code), "thread_busy") {
		return WithExitCode(ExitConflict, err)
	}
	return WithExitCode(ExitProtocol, err)
}

func interruptRunBestEffort(controller Controller, runID, reason string) error {
	if controller == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := controller.InterruptRun(ctx, runID, reason)
	return err
}

func writeLastMessage(path string, message string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(message), 0o644); err != nil {
		return fmt.Errorf("write last message: %w", err)
	}
	return nil
}
