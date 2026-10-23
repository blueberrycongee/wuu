package appserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestServerFinalizesTerminalWorkerBeforeDeleteCanAcquireItsLease(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	const (
		rootID        = "terminal-finalization-owner"
		participantID = "prt-terminal-finalization"
	)
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}
	if err := rt.Sessions.UpsertParticipant(participant.Participant{
		ID: participantID, Kind: participant.KindNamed, Name: "Terminal finalizer",
	}); err != nil {
		t.Fatalf("create participant: %v", err)
	}

	workerClient := newBlockingStreamClient("worker done")
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	control, err := agentcontrol.New(agentcontrol.Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    rootID,
		HistoryDir:   filepath.Join(artifactDir, "workers"),
		ThreadDir:    filepath.Join(artifactDir, "threads"),
		HarnessDir:   filepath.Join(artifactDir, "harness"),
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create agent control: %v", err)
	}

	out := &lockedBuffer{}
	owner := New(rt, out)
	root := newThreadState(rootID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	root.state = testRuntimeState(t, rt)
	// Keep the synthetic root-completion drain parked so the test can inspect
	// the queued completion at the exact pre-lease-release boundary.
	root.running = true
	threadRuntime := &runtime.ThreadRuntime{AgentControl: control}
	root.execRuntime = threadRuntime
	owner.mu.Lock()
	owner.threads[rootID] = root
	owner.mu.Unlock()

	firstCompletionAttempt := make(chan struct{})
	releaseFirstCompletionAttempt := make(chan struct{})
	var completionAttempts atomic.Int32
	completeParticipantRun := func(pid, agentID, outcome, summary string) error {
		if completionAttempts.Add(1) == 1 {
			close(firstCompletionAttempt)
			<-releaseFirstCompletionAttempt
			return errors.New("injected transient participant-run write failure")
		}
		return rt.Sessions.CompleteParticipantRun(pid, agentID, outcome, summary)
	}
	terminalFinalized := make(chan subagent.Notification, 1)
	terminalFinalizeErr := make(chan error, 1)
	unsubscribeTerminal := control.SubscribeWorkerTerminalFinalizer(func(notification subagent.Notification) error {
		if err := owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, control, notification, completeParticipantRun); err != nil {
			terminalFinalizeErr <- err
			return err
		}
		terminalFinalized <- notification
		return nil
	})

	var releaseProviderOnce sync.Once
	releaseProvider := func() { releaseProviderOnce.Do(func() { close(workerClient.release) }) }
	var releaseFirstCompletionOnce sync.Once
	releaseFirstCompletion := func() { releaseFirstCompletionOnce.Do(func() { close(releaseFirstCompletionAttempt) }) }
	var releaseFinalizerOnce sync.Once
	finalizerEntered := make(chan struct{})
	releaseFinalizer := make(chan struct{})
	control.SetWorkerExecutionReleaseHookForTest(func(string) {
		close(finalizerEntered)
		<-releaseFinalizer
	})
	t.Cleanup(func() {
		releaseProvider()
		releaseFirstCompletion()
		releaseFinalizerOnce.Do(func() { close(releaseFinalizer) })
		unsubscribeTerminal()
		owner.Close()
		control.StopAll()
		control.Close()
	})

	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:          agentcontrol.DefaultSubagentType,
		TaskName:      "terminal_finalization",
		ParticipantID: participantID,
		Description:   "verify terminal ownership",
		Prompt:        "finish",
	})
	if err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker provider request did not start")
	}
	if err := rt.Sessions.UpsertParticipantRun(session.ParticipantRun{
		ID:            spawned.AgentID,
		ParticipantID: participantID,
		AgentID:       spawned.AgentID,
		TaskID:        spawned.TaskName,
		SessionID:     rootID,
		Summary:       "running",
		Outcome:       "running",
	}); err != nil {
		t.Fatalf("seed participant run: %v", err)
	}

	releaseProvider()
	select {
	case <-firstCompletionAttempt:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not attempt durable participant-run completion")
	}
	if !control.HasOwnedWorkerExecutions() {
		t.Fatal("worker lease was released while durable participant-run completion was blocked")
	}
	runs, err := rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatalf("list participant runs before retry: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != "running" {
		t.Fatalf("participant run changed before durable retry succeeded: %+v", runs)
	}
	if child := owner.thread(spawned.AgentID); child != nil {
		t.Fatal("terminal worker UI state materialized before durable completion")
	}
	owner.agentCompletionMu.Lock()
	pendingBeforeRetry := len(owner.pendingAgentCompletionTurns[rootID])
	owner.agentCompletionMu.Unlock()
	if pendingBeforeRetry != 0 {
		t.Fatalf("root completion queue length before durable retry = %d, want 0", pendingBeforeRetry)
	}
	if got := len(notificationsByMethod(parseOutput(t, out.String()), NotificationAgentMailbox)); got != 0 {
		t.Fatalf("mailbox notifications before durable retry = %d, want 0", got)
	}

	releaseFirstCompletion()
	select {
	case <-finalizerEntered:
	case err := <-terminalFinalizeErr:
		t.Fatalf("terminal finalization failed after transient retry: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach the pre-lease-release boundary after durable retry")
	}

	var terminalNotification subagent.Notification
	select {
	case terminalNotification = <-terminalFinalized:
	case err := <-terminalFinalizeErr:
		t.Fatalf("terminal finalization failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("terminal finalization did not finish")
	}
	if got := completionAttempts.Load(); got != 2 {
		t.Fatalf("participant-run completion attempts = %d, want 2", got)
	}

	runs, err = rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatalf("list participant runs: %v", err)
	}
	if len(runs) != 1 || runs[0].AgentID != spawned.AgentID || runs[0].Outcome != string(subagent.StatusCompleted) {
		t.Fatalf("participant run was not finalized before lease release: %+v", runs)
	}
	child := owner.thread(spawned.AgentID)
	if child == nil {
		t.Fatal("terminal worker thread was not materialized by the server finalizer")
	}
	child.mu.Lock()
	childStatus := child.snapshotLocked().Status
	var childTurn Turn
	if len(child.Turns) > 0 {
		childTurn = child.Turns[len(child.Turns)-1]
	}
	child.mu.Unlock()
	if childStatus != ThreadStatusIdle || childTurn.Status != TurnStatusCompleted {
		t.Fatalf("terminal worker UI state was not finalized: thread=%s turn=%s", childStatus, childTurn.Status)
	}
	owner.agentCompletionMu.Lock()
	pendingCompletion := len(owner.pendingAgentCompletionTurns[rootID])
	owner.agentCompletionMu.Unlock()
	if pendingCompletion != 1 {
		t.Fatalf("root completion queue length = %d, want 1 before worker lease release", pendingCompletion)
	}
	waitForMethod(t, out, NotificationAgentMailbox)

	// The best-effort status stream can replay the same terminal snapshot after
	// the reliable finalizer returns. It must not write or settle the run twice.
	if err := owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, control, terminalNotification, completeParticipantRun); err != nil {
		t.Fatalf("replay terminal finalization: %v", err)
	}
	if got := completionAttempts.Load(); got != 2 {
		t.Fatalf("terminal replay issued another durable write: attempts=%d", got)
	}
	owner.agentCompletionMu.Lock()
	pendingAfterReplay := len(owner.pendingAgentCompletionTurns[rootID])
	owner.agentCompletionMu.Unlock()
	if pendingAfterReplay != 1 {
		t.Fatalf("root completion queue length after terminal replay = %d, want 1", pendingAfterReplay)
	}
	if got := len(notificationsByMethod(parseOutput(t, out.String()), NotificationAgentMailbox)); got != 1 {
		t.Fatalf("mailbox notifications after terminal replay = %d, want 1", got)
	}

	contenderRuntime := newTestRuntime(t, &fakeClient{})
	contenderRuntime.RootDir = rt.RootDir
	contenderRuntime.SessionDir = rt.SessionDir
	contenderRuntime.Documents, contenderRuntime.Sessions = rt.Documents, rt.Sessions
	contenderRuntime.StateDir = rt.StateDir
	contenderOut := &lockedBuffer{}
	contender := New(contenderRuntime, contenderOut)
	t.Cleanup(contender.Close)
	dispatchPayload(t, contender, "delete-before-release", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	blocked := responseByID(t, parseOutput(t, contenderOut.String()), "delete-before-release")
	if blocked["error"] == nil || !strings.Contains(fmt.Sprint(blocked["error"]), "active agents") {
		t.Fatalf("delete acquired ownership before terminal finalization released the worker lease: %+v", blocked)
	}

	releaseFinalizerOnce.Do(func() { close(releaseFinalizer) })
	waitForOwnedWorkerExecutions(t, control, 0)
	owner.Close()
	dispatchPayload(t, contender, "delete-after-release", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	deleted := responseByID(t, parseOutput(t, contenderOut.String()), "delete-after-release")
	if deleted["error"] != nil {
		t.Fatalf("delete after terminal finalization failed: %+v", deleted["error"])
	}
	if _, found, err := rt.Sessions.Find(rootID); err != nil || found {
		t.Fatalf("root session survived delete: found=%t err=%v", found, err)
	}
}

func TestServerTerminalFinalizationFailureYieldsDurablyAndRecoversOnce(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	const (
		rootID        = "terminal-durable-recovery-owner"
		participantID = "prt-terminal-durable-recovery"
	)
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}
	if err := rt.Sessions.UpsertParticipant(participant.Participant{
		ID: participantID, Kind: participant.KindNamed, Name: "Durable recovery",
	}); err != nil {
		t.Fatalf("create participant: %v", err)
	}

	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	newControl := func(client *blockingStreamClient) *agentcontrol.AgentControl {
		t.Helper()
		control, err := agentcontrol.New(agentcontrol.Config{
			AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

			Client:       client,
			DefaultModel: "fake-model",
			ParentRepo:   rt.RootDir,
			WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
			SessionID:    rootID,
			HistoryDir:   filepath.Join(artifactDir, "workers"),
			ThreadDir:    filepath.Join(artifactDir, "threads"),
			HarnessDir:   filepath.Join(artifactDir, "harness"),
			WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
				return noopToolExecutor{}, nil
			},
		})
		if err != nil {
			t.Fatalf("create agent control: %v", err)
		}
		return control
	}
	attachRoot := func(server *Server, control *agentcontrol.AgentControl) {
		t.Helper()
		root := newThreadState(rootID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
		root.state = testRuntimeState(t, rt)
		root.running = true
		root.execRuntime = &runtime.ThreadRuntime{AgentControl: control}
		server.mu.Lock()
		server.threads[rootID] = root
		server.mu.Unlock()
	}

	workerClient := newBlockingStreamClient("worker done")
	control := newControl(workerClient)
	owner := New(rt, &lockedBuffer{})
	attachRoot(owner, control)

	injectedFailure := errors.New("injected persistent participant-run write failure")
	var failedAttempts atomic.Int32
	unsubscribe := control.SubscribeWorkerTerminalFinalizer(func(notification subagent.Notification) error {
		return owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, control, notification, func(string, string, string, string) error {
			failedAttempts.Add(1)
			return injectedFailure
		})
	})
	var releaseProviderOnce sync.Once
	releaseProvider := func() { releaseProviderOnce.Do(func() { close(workerClient.release) }) }
	t.Cleanup(func() {
		releaseProvider()
		control.YieldWorkerTerminalFinalizations()
		unsubscribe()
		owner.Close()
		control.Close()
	})

	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:          agentcontrol.DefaultSubagentType,
		TaskName:      "terminal_durable_recovery",
		ParticipantID: participantID,
		Description:   "verify durable terminal recovery",
		Prompt:        "finish",
	})
	if err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker provider request did not start")
	}
	if err := rt.Sessions.UpsertParticipantRun(session.ParticipantRun{
		ID: spawned.AgentID, ParticipantID: participantID, AgentID: spawned.AgentID,
		TaskID: spawned.TaskName, SessionID: rootID, Summary: "running", Outcome: "running",
	}); err != nil {
		t.Fatalf("seed participant run: %v", err)
	}

	releaseProvider()
	deadline := time.Now().Add(3 * time.Second)
	for failedAttempts.Load() < int32(participantRunCompletionMaxAttempts*2) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := failedAttempts.Load(); got < int32(participantRunCompletionMaxAttempts*2) {
		t.Fatalf("participant-run completion attempts = %d, want at least %d", got, participantRunCompletionMaxAttempts*2)
	}
	if !control.HasOwnedWorkerExecutions() {
		t.Fatal("worker lease was released after repeated negative acknowledgements")
	}
	if !control.HasPendingWorkerTerminalFinalizations() {
		t.Fatal("terminal retry intent was not durable while completion kept failing")
	}
	runs, err := rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Outcome != "running" {
		t.Fatalf("participant run changed before terminal acknowledgement: %+v", runs)
	}

	closeDone := make(chan struct{})
	go func() {
		owner.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Close blocked after terminal intent became durable")
	}
	unsubscribe()
	if control.HasOwnedWorkerExecutions() {
		t.Fatal("shutdown retained the physical worker lease after durable handoff")
	}
	if !control.HasPendingWorkerTerminalFinalizations() {
		t.Fatal("shutdown removed the unacknowledged durable terminal intent")
	}
	active, err := agentcontrol.WorkerExecutionActive(filepath.Join(artifactDir, "harness"), spawned.AgentID)
	if err != nil {
		t.Fatalf("probe durable worker ownership: %v", err)
	}
	if !active {
		t.Fatal("durable terminal intent did not block destructive ownership probes")
	}
	deleteBlocked, err := owner.threadHasDurableActiveAgents(rootID)
	if err != nil {
		t.Fatalf("inspect thread delete barrier: %v", err)
	}
	if !deleteBlocked {
		t.Fatal("thread delete did not recognize durable terminal ownership")
	}

	// Recreate the exact crash window protected by the prepare record: the
	// terminal intent is durable, but the final worker snapshot and internal
	// harness projection never became durable. Generic startup reconciliation
	// therefore has no terminal history to infer from; recovery must use the
	// intent itself before running the participant/server finalizer.
	if err := os.Remove(filepath.Join(artifactDir, "workers", spawned.AgentID+".json")); err != nil {
		t.Fatalf("remove final worker snapshot: %v", err)
	}
	tasks, err := control.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("list harness tasks before crash simulation: %v", err)
	}
	foundTask := false
	for _, task := range tasks {
		if task.ID != spawned.AgentID {
			continue
		}
		foundTask = true
		task.Status = "running"
		task.CompletedAt = time.Time{}
		if err := control.HarnessStore().UpsertTask(task); err != nil {
			t.Fatalf("reset harness task: %v", err)
		}
	}
	if !foundTask {
		t.Fatalf("worker %s had no harness task", spawned.AgentID)
	}
	runsBeforeRecovery, err := control.HarnessStore().ListRuns()
	if err != nil {
		t.Fatalf("list harness runs before crash simulation: %v", err)
	}
	for _, run := range runsBeforeRecovery {
		if run.TaskID != spawned.AgentID {
			continue
		}
		run.Status = "running"
		run.CompletedAt = time.Time{}
		if err := control.HarnessStore().UpsertRun(run); err != nil {
			t.Fatalf("reset harness run: %v", err)
		}
	}
	if err := os.Remove(filepath.Join(artifactDir, "harness", "reports.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove harness report index: %v", err)
	}
	if err := os.Remove(filepath.Join(artifactDir, "harness", "reports", spawned.AgentID+".md")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove harness report: %v", err)
	}

	recoveryControl := newControl(newBlockingStreamClient("unused"))
	resumeCtx, cancelResume := context.WithTimeout(context.Background(), time.Second)
	_, resumeErr := recoveryControl.FollowupTask(resumeCtx, spawned.AgentID, "continue")
	cancelResume()
	if resumeErr == nil || !strings.Contains(resumeErr.Error(), "unacknowledged terminal state") {
		t.Fatalf("resume crossed durable terminal barrier: %v", resumeErr)
	}
	recoveryOwner := New(rt, &lockedBuffer{})
	attachRoot(recoveryOwner, recoveryControl)
	var successfulWrites atomic.Int32
	recovered := make(chan subagent.Notification, 1)
	recoveryUnsubscribe := recoveryControl.SubscribeWorkerTerminalFinalizer(func(notification subagent.Notification) error {
		err := recoveryOwner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, recoveryOwner.rt), rootID, recoveryControl, notification, func(pid, agentID, outcome, summary string) error {
			successfulWrites.Add(1)
			return rt.Sessions.CompleteParticipantRun(pid, agentID, outcome, summary)
		})
		if err == nil {
			select {
			case recovered <- notification:
			default:
			}
		}
		return err
	})
	recoveryControl.StartWorkerTerminalRecovery()
	t.Cleanup(func() {
		recoveryUnsubscribe()
		recoveryOwner.Close()
		recoveryControl.Close()
	})

	var recoveredNotification subagent.Notification
	select {
	case recoveredNotification = <-recovered:
	case <-time.After(3 * time.Second):
		t.Fatal("new app-server did not recover the durable terminal finalization")
	}
	waitForOwnedWorkerExecutions(t, recoveryControl, 0)
	if recoveryControl.HasPendingWorkerTerminalFinalizations() {
		t.Fatal("acknowledged terminal intent survived recovery")
	}
	runs, err = rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Outcome != string(subagent.StatusCompleted) {
		t.Fatalf("participant run after terminal recovery = %+v", runs)
	}
	if got := successfulWrites.Load(); got != 1 {
		t.Fatalf("successful durable terminal writes = %d, want 1", got)
	}
	recoveredTasks, err := recoveryControl.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("list recovered harness tasks: %v", err)
	}
	foundRecoveredTask := false
	for _, task := range recoveredTasks {
		if task.ID == spawned.AgentID {
			foundRecoveredTask = true
			if task.Status != "completed" {
				t.Fatalf("recovered harness task status = %s, want completed", task.Status)
			}
		}
	}
	if !foundRecoveredTask {
		t.Fatalf("terminal recovery did not rebuild harness task %s", spawned.AgentID)
	}
	if _, ok, err := recoveryControl.HarnessStore().ReportForTask(spawned.AgentID); err != nil || !ok {
		t.Fatalf("terminal recovery did not rebuild harness report: ok=%t err=%v", ok, err)
	}
	if meta, ok := recoveryControl.Threads().Resolve(spawned.AgentID); !ok || meta.Status != agentthread.StatusCompleted {
		t.Fatalf("terminal recovery did not rebuild worker thread: %+v ok=%t", meta, ok)
	}
	if err := recoveryOwner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, recoveryOwner.rt), rootID, recoveryControl, recoveredNotification, func(pid, agentID, outcome, summary string) error {
		successfulWrites.Add(1)
		return rt.Sessions.CompleteParticipantRun(pid, agentID, outcome, summary)
	}); err != nil {
		t.Fatalf("replay recovered terminal finalization: %v", err)
	}
	if got := successfulWrites.Load(); got != 1 {
		t.Fatalf("terminal recovery settled more than once: writes=%d", got)
	}
}

func TestServerTerminalFinalizationFailureRemainsReplayable(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	const (
		rootID        = "terminal-replay-owner"
		participantID = "prt-terminal-replay"
		agentID       = "agt-terminal-replay"
	)
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.UpsertParticipant(participant.Participant{
		ID: participantID, Kind: participant.KindNamed, Name: "Replay owner",
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.UpsertParticipantRun(session.ParticipantRun{
		ID: agentID, ParticipantID: participantID, AgentID: agentID,
		SessionID: rootID, Outcome: "running", Summary: "working",
	}); err != nil {
		t.Fatal(err)
	}

	out := &lockedBuffer{}
	owner := New(rt, out)
	t.Cleanup(owner.Close)
	now := time.Now().UTC()
	notification := subagent.Notification{
		AgentID: agentID,
		Status:  subagent.StatusCompleted,
		Snapshot: subagent.SubAgentSnapshot{
			ID: agentID, ParentID: rootID, ParticipantID: participantID,
			Status: subagent.StatusCompleted, Result: "done", CompletedAt: now,
		},
	}

	var failedAttempts atomic.Int32
	injectedFailure := errors.New("injected persistent participant-run write failure")
	err := owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, nil, notification, func(string, string, string, string) error {
		failedAttempts.Add(1)
		return injectedFailure
	})
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("terminal finalization error = %v, want injected failure", err)
	}
	if got := failedAttempts.Load(); got != participantRunCompletionMaxAttempts {
		t.Fatalf("terminal finalization attempts = %d, want %d", got, participantRunCompletionMaxAttempts)
	}
	runs, err := rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Outcome != "running" {
		t.Fatalf("participant run after failed finalization = %+v", runs)
	}
	if got := len(parseOutput(t, out.String())); got != 0 {
		t.Fatalf("terminal notifications after failed finalization = %d, want 0", got)
	}

	var successfulWrites atomic.Int32
	succeed := func(pid, runAgentID, outcome, summary string) error {
		successfulWrites.Add(1)
		return rt.Sessions.CompleteParticipantRun(pid, runAgentID, outcome, summary)
	}
	if err := owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, nil, notification, succeed); err != nil {
		t.Fatalf("replay terminal finalization: %v", err)
	}
	runs, err = rt.Sessions.ListParticipantRuns(participantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Outcome != string(subagent.StatusCompleted) {
		t.Fatalf("participant run after successful replay = %+v", runs)
	}
	if err := owner.finalizeAgentTerminalWithCompleter(testRuntimeState(t, owner.rt), rootID, nil, notification, succeed); err != nil {
		t.Fatalf("deduplicated terminal replay: %v", err)
	}
	if got := successfulWrites.Load(); got != 1 {
		t.Fatalf("successful durable terminal writes = %d, want 1", got)
	}
	if got := len(notificationsByMethod(parseOutput(t, out.String()), NotificationAgentMailbox)); got != 1 {
		t.Fatalf("mailbox notifications after successful replay = %d, want 1", got)
	}
}
