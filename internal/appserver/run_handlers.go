package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/structuredoutput"
	"github.com/blueberrycongee/wuu/internal/version"
)

type runTracker struct {
	id              string
	threadID        string
	validator       *structuredoutput.Validator
	retries         int
	interruptStatus execution.Status
	control         *session.Control
	controlBaseline *session.Control
	schemaRetry     *executionSchemaRetry
}

type executionSchemaRetry struct {
	projectID string
	thread    *threadState
	snapshot  turnRuntimeSnapshot
	prompt    string
	queuedAt  time.Time
}

func (s *Server) handleRunStart(ctx context.Context, req Request) error {
	var params RunStartParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	if s.runStore == nil || s.rt == nil || s.rt.InferenceJournalRuntime == nil {
		return s.writeRunError(req.ID, "internal_error", errors.New("execution run store is unavailable"))
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.Prompt = strings.TrimSpace(params.Prompt)
	if params.Request.Mode == "" {
		params.Request.Mode = execution.ModeStart
	}
	if params.ThreadID == "" {
		return s.writeRunError(req.ID, "invalid_params", errors.New("thread_id is required"))
	}
	images, err := normalizeTurnStartImages(params.Images)
	if err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	files, err := normalizeTurnStartFiles(params.Files)
	if err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	if params.Prompt == "" && len(images) == 0 && len(files) == 0 {
		return s.writeRunError(req.ID, "invalid_params", errors.New("prompt or attachment is required"))
	}
	if isManualCompactPrompt(params.Prompt) {
		return s.writeRunError(req.ID, "invalid_params", errors.New("execution runs do not accept compact commands"))
	}
	validator, err := structuredoutput.New(params.OutputSchema)
	if err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	th, err := s.ensureThreadLoaded(params.ThreadID)
	if err != nil {
		return s.writeRunError(req.ID, "thread_not_found", err)
	}
	permissions, err := s.resolveThreadTurnPermissions(th, params.PermissionMode)
	if err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	prompt := params.Prompt
	if validator != nil {
		prompt = validator.InitialPrompt(prompt)
	}
	userMsg, err := s.userMessageWithInputImages(params.ThreadID, prompt, images, files, params.Images)
	if err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	if err := s.takeSessionControlForInput(params.ThreadID); err != nil {
		return s.writeRunError(req.ID, "internal_error", err)
	}

	// A structured-output correction is automatic continuation of this input.
	// Preserve its original control revision rather than accepting a newer fence
	// after a capacity wait or an intervening user stop.
	var retryControl *session.Control
	controlBaseline := session.Control{SessionID: params.ThreadID}
	if control, found, err := session.ReadControl(s.rt.SessionDir, params.ThreadID); err != nil {
		return s.writeRunError(req.ID, "internal_error", err)
	} else if found {
		controlBaseline = control
		if control.State == session.ControlActive {
			retryControl = &control
		}
	}

	params.Request.HasPrompt = params.Prompt != ""
	params.Request.ImageCount = len(images)
	params.Request.FileCount = len(files)
	params.Request.StructuredOutput = len(params.OutputSchema) > 0
	resumeBrowser, err := s.rt.ActivityRegistry.PrepareResume(params.ThreadID, activity.KindBrowser)
	if err != nil {
		return s.writeRunError(req.ID, "internal_error", err)
	}
	workspace, initialRuntime, ephemeral := s.executionFactsForThread(th, nil, turnRuntimeSnapshot{}.withPermissions(permissions))
	run, err := s.runStore.Create(ctx, execution.CreateParams{
		RuntimeID: s.rt.InferenceJournalRuntime.RuntimeID(),
		Request:   params.Request,
		Runtime:   initialRuntime,
		Workspace: workspace,
		ThreadID:  params.ThreadID,
		Ephemeral: ephemeral,
	})
	if err != nil {
		code := "internal_error"
		if errors.Is(err, execution.ErrConflict) {
			code = "thread_busy"
		}
		return s.writeRunError(req.ID, code, err)
	}

	snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
	snapshot.PermissionExplicit = params.PermissionMode != nil
	snapshot.ExecutionRunID = run.ID
	snapshot.Control = retryControl
	var threadRuntime *runtime.ThreadRuntime
	started, ok, admissionErr := s.startThreadUserTurnWithAdmission(
		ctx, th, userMsg, snapshot, true, turnReadOnlyIgnore,
		turnAdmissionHooks{afterLease: func(admitted *threadState, _ *providers.ChatMessage) error {
			var runtimeErr error
			threadRuntime, runtimeErr = s.ensureThreadRuntimeAfterAdmission(admitted)
			if runtimeErr == nil {
				s.foldFrozenWorkerTree(admitted, threadRuntime)
			}
			return runtimeErr
		}},
	)
	if admissionErr != nil || !ok {
		if admissionErr == nil {
			admissionErr = fmt.Errorf("thread %q already has a running turn", params.ThreadID)
		}
		runErr := execution.Error{Code: "admission_failed", Category: "cancelled", Message: admissionErr.Error()}
		_, _ = s.runStore.Fail(ctx, run.ID, execution.StatusCancelled, execution.Result{ExitCode: execution.ExitCodeForSettlement(execution.StatusCancelled, &runErr)}, runErr, time.Now().UTC())
		return s.writeRunError(req.ID, "thread_busy", admissionErr)
	}

	workspace, resolvedRuntime, _ := s.executionFactsForThread(th, threadRuntime, started.runtime)
	run, err = s.runStore.Resolve(ctx, run.ID, resolvedRuntime, workspace, time.Now().UTC())
	if err == nil {
		run, err = s.runStore.AttachTurn(ctx, run.ID, params.ThreadID, started.turnID, started.admittedAt)
	}
	if err != nil {
		persistErr := s.abortStartedThreadTurnDurably(th, started, err)
		_, _ = s.runStore.Fail(ctx, run.ID, execution.StatusFailed, execution.Result{}, execution.Error{Code: "manifest_update_failed", Category: "internal", Message: err.Error()}, time.Now().UTC())
		return s.writeRunError(req.ID, "internal_error", errors.Join(err, persistErr))
	}
	s.registerExecutionRun(run, validator)
	s.runMu.Lock()
	if tracker := s.runs[run.ID]; tracker != nil {
		tracker.control = retryControl
		tracker.controlBaseline = &controlBaseline
	}
	s.runMu.Unlock()
	started.runtime.Control = retryControl

	launch, accepted := s.reserveBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	})
	if !accepted {
		persistErr := s.abortStartedThreadTurnDurably(th, started, errServerClosed)
		_, _ = s.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "server_closed", "cancelled", errServerClosed)
		return s.writeRunError(req.ID, "server_closed", errors.Join(errServerClosed, persistErr))
	}
	defer launch.Cancel()

	if err := s.writeResponse(req.ID, RunStartResult{Run: run}, nil); err != nil {
		persistErr := s.abortStartedThreadTurnDurably(th, started, err)
		_, _ = s.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "response_failed", "protocol", err)
		return errors.Join(err, persistErr)
	}
	if err := s.writeNotification(NotificationRunStarted, RunStartedNotification{Run: run}); err != nil {
		persistErr := s.abortStartedThreadTurnDurably(th, started, err)
		_, _ = s.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "notification_failed", "protocol", err)
		return errors.Join(err, persistErr)
	}
	if err := s.writeNotification(NotificationTurnStarted, TurnStartedNotification{ThreadID: params.ThreadID, Turn: started.turn}); err != nil {
		persistErr := s.abortStartedThreadTurnDurably(th, started, err)
		_, _ = s.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "notification_failed", "protocol", err)
		return errors.Join(err, persistErr)
	}
	resumeBrowser()
	launch.Commit()
	return nil
}

func (s *Server) handleRunInterrupt(ctx context.Context, req Request) error {
	var params RunInterruptParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeRunError(req.ID, "invalid_params", err)
	}
	runID := strings.TrimSpace(params.RunID)
	if runID == "" {
		return s.writeRunError(req.ID, "invalid_params", errors.New("run_id is required"))
	}
	view, err := s.readExecutionRun(ctx, runID)
	if err != nil {
		if errors.Is(err, execution.ErrNotFound) {
			return s.writeRunError(req.ID, "run_not_found", err)
		}
		return s.writeRunError(req.ID, "internal_error", err)
	}
	if view.Run.Status.Terminal() {
		return s.writeResponse(req.ID, RunInterruptResult{Run: view.Run}, nil)
	}
	if !view.Attached {
		return s.writeRunError(req.ID, "run_not_attached", fmt.Errorf("run %q is not attached to this app-server", runID))
	}
	if err := s.takeSessionControl(view.Run.ThreadID, session.ControlPaused); err != nil {
		return s.writeRunError(req.ID, "internal_error", err)
	}
	interruptStatus := execution.StatusInterrupted
	if strings.EqualFold(strings.TrimSpace(params.Reason), "timeout") {
		interruptStatus = execution.StatusTimedOut
	}
	// Record the caller's reason before cancellation. A fast provider can settle
	// the Turn synchronously with cancel, so writing this afterward races the
	// terminal Run update and can misclassify a timeout as a generic interrupt.
	s.setExecutionRunInterruptStatus(runID, interruptStatus)
	turnActive, err := s.interruptThreadExecution(view.Run.ThreadID, runID, "")
	if err != nil {
		if errors.Is(err, errExecutionRunChanged) {
			latest, readErr := s.readExecutionRun(ctx, runID)
			if readErr == nil && latest.Run.Status.Terminal() {
				return s.writeResponse(req.ID, RunInterruptResult{Run: latest.Run}, nil)
			}
			if readErr != nil {
				return s.writeRunError(req.ID, "internal_error", readErr)
			}
			return s.writeRunError(req.ID, "run_not_attached", err)
		}
		return s.writeRunError(req.ID, "internal_error", err)
	}
	if !turnActive {
		status := interruptStatus
		code, category, message := "interrupted", "cancelled", "execution run interrupted"
		if strings.EqualFold(strings.TrimSpace(params.Reason), "timeout") {
			status = execution.StatusTimedOut
			code, category, message = "timeout", "timeout", "execution run timed out"
		}
		view.Run, err = s.failAndDetachExecutionRun(runID, status, code, category, errors.New(message))
		if err != nil {
			return s.writeRunError(req.ID, "internal_error", err)
		}
	}
	return s.writeResponse(req.ID, RunInterruptResult{Run: view.Run}, nil)
}

func (s *Server) readExecutionRun(ctx context.Context, runID string) (RunView, error) {
	if s == nil || s.runStore == nil {
		return RunView{}, errors.New("execution run store is unavailable")
	}
	run, err := s.runStore.Get(ctx, runID)
	if err != nil {
		return RunView{}, err
	}
	view := RunView{Run: run}
	if th := s.thread(run.ThreadID); th != nil {
		th.mu.Lock()
		thread := th.snapshotLocked()
		th.mu.Unlock()
		view.Thread = &thread
		view.Attached = s.executionRunAttached(run.ID)
		return view, nil
	}
	if !run.Ephemeral {
		if th, loadErr := s.loadPersistedThreadState(run.ThreadID, time.Now().UTC()); loadErr == nil {
			th.mu.Lock()
			thread := th.snapshotLocked()
			th.mu.Unlock()
			view.Thread = &thread
		}
	}
	return view, nil
}

func (s *Server) executionFactsForThread(th *threadState, threadRuntime *runtime.ThreadRuntime, snapshot turnRuntimeSnapshot) (execution.WorkspaceRef, execution.RuntimeManifest, bool) {
	workspace := execution.WorkspaceRef{Root: s.rt.RootDir}
	selection := execution.Selection{Provider: s.rt.ProviderName, Model: s.rt.Model, PermissionMode: snapshot.PermissionMode}
	ephemeral := false
	if th != nil {
		th.mu.Lock()
		workspace = execution.WorkspaceRef{ID: th.WorkspaceID, Root: firstNonEmpty(th.CWD, s.rt.RootDir)}
		selection.Provider = firstNonEmpty(th.ModelProvider, selection.Provider)
		selection.Model = firstNonEmpty(th.Model, selection.Model)
		selection.Variant = th.ModelVariant
		selection.Effort = th.ModelEffort
		selection.PermissionMode = firstNonEmpty(snapshot.PermissionMode, th.PermissionMode, s.rt.Permissions.Mode)
		ephemeral = th.Ephemeral
		th.mu.Unlock()
	}
	runner := s.rt.StreamRunner
	if threadRuntime != nil && threadRuntime.StreamRunner != nil {
		runner = threadRuntime.StreamRunner
	}
	if runner != nil {
		selection.Provider = firstNonEmpty(runner.ProviderName, selection.Provider)
		selection.Model = firstNonEmpty(runner.Model, selection.Model)
		selection.Variant = runner.Variant
		selection.Effort = runner.Effort
	}
	core := version.Info()
	return workspace, execution.RuntimeManifest{
		Resolved: selection, ProtocolVersion: ProtocolVersion, CoreVersion: core.Version, CoreCommit: core.Commit,
		MaxParallel: s.rt.MaxParallel(),
	}, ephemeral
}

func (s *Server) registerExecutionRun(run execution.Run, validator *structuredoutput.Validator) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	tracker := &runTracker{id: run.ID, threadID: run.ThreadID, validator: validator}
	s.runs[run.ID] = tracker
	s.activeRunByThread[run.ThreadID] = run.ID
}

func (s *Server) executionRunAttached(runID string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	_, ok := s.runs[runID]
	return ok
}

func (s *Server) activeExecutionRunID(threadID string) string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeRunByThread[strings.TrimSpace(threadID)]
}

func (s *Server) executionRunSchemaOutcome(runID, content string) (retryPrompt string, retry bool, validationErr error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	tracker := s.runs[runID]
	if tracker == nil || tracker.validator == nil {
		return "", false, nil
	}
	if err := tracker.validator.Validate(content); err == nil {
		return "", false, nil
	} else if tracker.retries < structuredoutput.MaxRetries {
		tracker.retries++
		return tracker.validator.RetryPrompt(content, err), true, nil
	} else {
		return "", false, err
	}
}

func (s *Server) setExecutionRunInterruptStatus(runID string, status execution.Status) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if tracker := s.runs[strings.TrimSpace(runID)]; tracker != nil {
		tracker.interruptStatus = status
	}
}

func (s *Server) executionRunInterruptStatus(runID string) execution.Status {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if tracker := s.runs[strings.TrimSpace(runID)]; tracker != nil && tracker.interruptStatus != "" {
		return tracker.interruptStatus
	}
	return execution.StatusInterrupted
}

func (s *Server) attachExecutionTurn(runID, threadID, turnID string, at time.Time) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	_, err := s.runStore.AttachTurn(context.Background(), runID, threadID, turnID, at)
	return err
}

func (s *Server) settleExecutionRunTurn(runID, turnID, tracePath string, turn Turn, structured *TurnError, turnErr error, awaitingContinuation bool, at time.Time) (execution.Run, bool, error) {
	if strings.TrimSpace(runID) == "" {
		return execution.Run{}, false, nil
	}
	run, err := s.runStore.FinishTurn(context.Background(), runID, turnID, execution.TurnTerminal{TracePath: tracePath, At: at})
	if err != nil {
		return execution.Run{}, false, err
	}
	result := execution.Result{FinalTurnID: turnID, TracePath: tracePath}
	if turnErr != nil {
		status := execution.StatusFailed
		if turn.Status == TurnStatusInterrupted || errors.Is(turnErr, context.Canceled) {
			status = s.executionRunInterruptStatus(runID)
		}
		runError := execution.Error{Code: "turn_failed", Category: "unknown", Message: turnErr.Error()}
		if structured != nil {
			runError = execution.Error{
				Code: structured.Code, Category: string(structured.Category), Message: structured.Message,
				Provider: structured.Provider, StatusCode: structured.StatusCode,
			}
		}
		result.ExitCode = execution.ExitCodeForSettlement(status, &runError)
		run, err = s.runStore.Fail(context.Background(), runID, status, result, runError, at)
	} else if !awaitingContinuation {
		run, err = s.runStore.Complete(context.Background(), runID, result, at)
	}
	if err != nil {
		return execution.Run{}, false, err
	}
	terminal := run.Status.Terminal()
	if terminal {
		s.detachExecutionRun(runID)
	}
	return run, terminal, nil
}

func (s *Server) executionRunAwaitsContinuation(threadID string, threadRuntime *runtime.ThreadRuntime) (bool, error) {
	return threadRuntimeAwaitsAutoContinuation(threadID, threadRuntime), nil
}

func (s *Server) executionRunSuccessfulTurnOutcome(runID, threadID string, threadRuntime *runtime.ThreadRuntime, content string) (awaitingContinuation bool, retryPrompt string, validationErr error) {
	awaitingContinuation, err := s.executionRunAwaitsContinuation(threadID, threadRuntime)
	if err != nil {
		return false, "", err
	}
	if awaitingContinuation {
		return true, "", nil
	}
	prompt, retry, err := s.executionRunSchemaOutcome(runID, content)
	if retry {
		return true, prompt, nil
	}
	return false, "", err
}

func (s *Server) startExecutionSchemaRetry(ctx context.Context, th *threadState, snapshot turnRuntimeSnapshot, prompt string) error {
	if th == nil || strings.TrimSpace(snapshot.ExecutionRunID) == "" {
		return errors.New("structured-output retry requires an attached execution run")
	}
	if snapshot.Control == nil {
		snapshot.Control = s.executionRunControl(snapshot.ExecutionRunID)
	}
	if snapshot.ExecutionControlBaseline == nil {
		snapshot.ExecutionControlBaseline = s.executionRunControlBaseline(snapshot.ExecutionRunID)
	}
	if err := s.validateExecutionSchemaRetry(snapshot); err != nil {
		return err
	}
	userMsg, err := userMessageFromPrompt(prompt, nil, nil)
	if err != nil {
		return err
	}
	var threadRuntime *runtime.ThreadRuntime
	started, ok, err := s.startThreadUserTurnWithAdmission(ctx, th, userMsg, snapshot, false, turnReadOnlyIgnore,
		turnAdmissionHooks{
			afterLease: func(admitted *threadState, _ *providers.ChatMessage) error {
				if err := s.validateExecutionSchemaRetry(snapshot); err != nil {
					return err
				}
				var runtimeErr error
				threadRuntime, runtimeErr = s.ensureThreadRuntimeAfterAdmission(admitted)
				return runtimeErr
			},
			beforeUserAppendLocked: func(_ *threadState) (func() error, error) {
				err := s.validateExecutionSchemaRetry(snapshot)
				return nil, err
			},
		},
	)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("thread %q is busy during structured-output retry: %w", th.ID, errThreadExecutionBusy)
	}
	started.runtime.Control = snapshot.Control
	if err := s.attachExecutionTurn(started.runtime.ExecutionRunID, th.ID, started.turnID, started.admittedAt); err != nil {
		return errors.Join(err, s.abortStartedThreadTurnDurably(th, started, err))
	}
	if err := s.writeNotification(NotificationTurnStarted, TurnStartedNotification{ThreadID: th.ID, Turn: started.turn}); err != nil {
		return errors.Join(err, s.abortStartedThreadTurnDurably(th, started, err))
	}
	launch, accepted := s.reserveBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	})
	if !accepted {
		return errors.Join(errServerClosed, s.abortStartedThreadTurnDurably(th, started, errServerClosed))
	}
	launch.Commit()
	return nil
}

// Automatic agent/process continuations carry the Run ID but may create a fresh
// runtime snapshot. Keep the accepted Run's fence across those continuations.
func (s *Server) executionRunControl(runID string) *session.Control {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if tracker := s.runs[runID]; tracker != nil {
		return tracker.control
	}
	return nil
}

func (s *Server) executionRunControlBaseline(runID string) *session.Control {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if tracker := s.runs[runID]; tracker != nil {
		return tracker.controlBaseline
	}
	return nil
}

func (s *Server) validateExecutionSchemaRetry(snapshot turnRuntimeSnapshot) error {
	if s.closed.Load() {
		return errServerClosed
	}
	runID := snapshot.ExecutionRunID
	s.runMu.Lock()
	tracker := s.runs[runID]
	if tracker == nil || s.activeRunByThread[tracker.threadID] != runID {
		s.runMu.Unlock()
		return errExecutionRunChanged
	}
	interruptStatus := tracker.interruptStatus
	s.runMu.Unlock()
	if interruptStatus != "" {
		return context.Canceled
	}
	if snapshot.Control != nil {
		if err := session.ValidateControl(s.rt.SessionDir, *snapshot.Control); err != nil {
			return err
		}
	}
	if baseline := snapshot.ExecutionControlBaseline; baseline != nil {
		if err := session.ValidateContinuationControl(s.rt.SessionDir, *baseline); err != nil {
			return err
		}
	}
	run, err := s.runStore.Get(context.Background(), runID)
	if err != nil {
		return err
	}
	if run.Status.Terminal() {
		return errExecutionRunChanged
	}
	return nil
}

func (s *Server) startOrDeferExecutionSchemaRetry(ctx context.Context, th *threadState, snapshot turnRuntimeSnapshot, prompt string) error {
	err := s.startExecutionSchemaRetry(ctx, th, snapshot, prompt)
	if !errors.Is(err, session.ErrProjectWorkerCapacity) {
		return err
	}
	metadata, found, readErr := session.Find(s.rt.SessionDir, th.ID)
	if readErr != nil {
		return readErr
	}
	if !found {
		return session.ErrSessionNotFound
	}
	projectID := metadata.ID
	if metadata.Source == projectSessionSource {
		projectID = metadata.ParentID
	}
	if snapshot.Control == nil {
		snapshot.Control = s.executionRunControl(snapshot.ExecutionRunID)
	}
	if snapshot.ExecutionControlBaseline == nil {
		snapshot.ExecutionControlBaseline = s.executionRunControlBaseline(snapshot.ExecutionRunID)
	}
	if err := s.validateExecutionSchemaRetry(snapshot); err != nil {
		return err
	}
	s.runMu.Lock()
	tracker := s.runs[snapshot.ExecutionRunID]
	if tracker == nil {
		s.runMu.Unlock()
		return errExecutionRunChanged
	}
	if tracker.control == nil {
		tracker.control = snapshot.Control
	}
	if tracker.schemaRetry == nil {
		tracker.schemaRetry = &executionSchemaRetry{projectID: projectID, thread: th, snapshot: snapshot, prompt: prompt, queuedAt: time.Now().UTC()}
	}
	s.runMu.Unlock()
	s.kickSessionInboxDrain(projectID)
	return nil
}

// Capacity deferrals remain part of their accepted Run and share the project's
// bounded timer. Validation still runs when this pass already knows the pool is
// full, so a stopped or detached run does not retain a useless retry loop.
func (s *Server) drainExecutionSchemaRetries(projectID string, allowStart bool) (pending, capacityFull bool) {
	s.runMu.Lock()
	var retries []*executionSchemaRetry
	for _, tracker := range s.runs {
		if retry := tracker.schemaRetry; retry != nil && retry.projectID == projectID {
			retries = append(retries, retry)
		}
	}
	s.runMu.Unlock()
	sort.Slice(retries, func(i, j int) bool {
		if retries[i].queuedAt.Equal(retries[j].queuedAt) {
			return retries[i].snapshot.ExecutionRunID < retries[j].snapshot.ExecutionRunID
		}
		return retries[i].queuedAt.Before(retries[j].queuedAt)
	})
	for _, retry := range retries {
		runID := retry.snapshot.ExecutionRunID
		err := s.validateExecutionSchemaRetry(retry.snapshot)
		if err == nil && !allowStart {
			pending = true
			continue
		}
		s.runMu.Lock()
		tracker := s.runs[runID]
		if tracker == nil || tracker.schemaRetry != retry {
			s.runMu.Unlock()
			continue
		}
		// Remove before launch: a fast model may already queue its next schema
		// correction before startExecutionSchemaRetry returns.
		tracker.schemaRetry = nil
		s.runMu.Unlock()
		if err == nil {
			err = s.startExecutionSchemaRetry(context.Background(), retry.thread, retry.snapshot, retry.prompt)
		}
		if errors.Is(err, session.ErrProjectWorkerCapacity) {
			capacityFull, allowStart = true, false
		}
		if errors.Is(err, errRetryableTurnAdmission) || errors.Is(err, errThreadExecutionBusy) {
			s.runMu.Lock()
			if tracker := s.runs[runID]; tracker != nil {
				if tracker.schemaRetry == nil {
					tracker.schemaRetry = retry
				}
				pending = true
			}
			s.runMu.Unlock()
			continue
		}
		if err != nil {
			s.failExecutionSchemaRetry(runID, err)
		}
	}
	return pending, capacityFull
}

func (s *Server) failExecutionSchemaRetry(runID string, cause error) {
	providers.DebugLogf("start structured-output retry for run %q: %v", runID, cause)
	status, category := execution.StatusFailed, "internal"
	if errors.Is(cause, context.Canceled) || errors.Is(cause, session.ErrControlChanged) || errors.Is(cause, errExecutionRunChanged) || errors.Is(cause, errServerClosed) {
		status, category = s.executionRunInterruptStatus(runID), "cancelled"
	}
	run, err := s.failAndDetachExecutionRun(runID, status, "structured_output_retry_failed", category, cause)
	if err != nil {
		providers.DebugLogf("settle structured-output retry failure for run %q: %v", runID, err)
		return
	}
	_ = s.writeNotification(NotificationRunUpdated, RunUpdatedNotification{Run: run})
}

func (s *Server) detachExecutionRun(runID string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	tracker := s.runs[runID]
	delete(s.runs, runID)
	if tracker != nil && s.activeRunByThread[tracker.threadID] == runID {
		delete(s.activeRunByThread, tracker.threadID)
	}
}

func (s *Server) failAndDetachExecutionRun(runID string, status execution.Status, code, category string, cause error) (execution.Run, error) {
	message := "execution run failed"
	if cause != nil {
		message = cause.Error()
	}
	runError := execution.Error{Code: code, Category: category, Message: message}
	run, err := s.runStore.Fail(context.Background(), runID, status, execution.Result{ExitCode: execution.ExitCodeForSettlement(status, &runError)}, runError, time.Now().UTC())
	if err != nil {
		current, readErr := s.runStore.Get(context.Background(), runID)
		if readErr != nil || !current.Status.Terminal() {
			return execution.Run{}, fmt.Errorf("settle execution run %q: %w", runID, err)
		}
		run = current
	}
	s.detachExecutionRun(runID)
	s.kickQueuedTurnDrain(run.ThreadID)
	return run, nil
}

func (s *Server) interruptAttachedRunsOnClose() {
	s.runMu.Lock()
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	s.runMu.Unlock()
	for _, id := range ids {
		_, _ = s.failAndDetachExecutionRun(id, execution.StatusInterrupted, "server_closed", "cancelled", errServerClosed)
	}
}

func (s *Server) writeRunError(id json.RawMessage, code string, err error) error {
	resp := Response{ID: id, Error: &ResponseError{Code: code, Message: err.Error()}}
	return s.writeJSON(resp)
}
