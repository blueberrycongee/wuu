package appserver

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestChannelWorkRunRecoveryDoesNotReuseOldNamedSessionCompletion(t *testing.T) {
	stored := session.Session{LatestCompletedTurnID: "previous-run-turn"}
	namedRun := channels.WorkRun{NamedAgentID: "agent-1", TurnID: "current-run-turn"}
	if got := channelWorkRunRecoveryState(namedRun, false, stored, true, channels.WorkRunRecoveryActive); got != channels.WorkRunRecoveryActive {
		t.Fatalf("named session recovery = %q, want active", got)
	}
	stored.LatestCompletedTurnID = namedRun.TurnID
	if got := channelWorkRunRecoveryState(namedRun, false, stored, true, channels.WorkRunRecoveryCompleted); got != channels.WorkRunRecoveryCompleted {
		t.Fatalf("current named run recovery = %q, want completed", got)
	}
	if got := channelWorkRunRecoveryState(namedRun, false, stored, true, channels.WorkRunRecoveryFailed); got != channels.WorkRunRecoveryFailed {
		t.Fatalf("failed named run recovery = %q, want failed", got)
	}
	if got := channelWorkRunRecoveryState(namedRun, false, stored, true, channels.WorkRunRecoveryInterrupted); got != channels.WorkRunRecoveryInterrupted {
		t.Fatalf("interrupted named run recovery = %q, want interrupted", got)
	}
	if got := channelWorkRunRecoveryState(channels.WorkRun{NamedAgentID: "agent-1"}, false, stored, true, channels.WorkRunRecoveryCompleted); got != channels.WorkRunRecoveryActive {
		t.Fatalf("unattached named run recovery = %q, want active", got)
	}
	hiddenRun := channels.WorkRun{}
	if got := channelWorkRunRecoveryState(hiddenRun, false, stored, true, channels.WorkRunRecoveryCompleted); got != channels.WorkRunRecoveryCompleted {
		t.Fatalf("hidden session recovery = %q, want completed", got)
	}
	if got := channelWorkRunRecoveryState(hiddenRun, false, session.Session{}, true, channels.WorkRunRecoveryFailed); got != channels.WorkRunRecoveryFailed {
		t.Fatalf("failed hidden session recovery = %q, want failed", got)
	}
	if got := channelWorkRunRecoveryState(hiddenRun, false, session.Session{}, true, channels.WorkRunRecoveryInterrupted); got != channels.WorkRunRecoveryInterrupted {
		t.Fatalf("interrupted hidden session recovery = %q, want interrupted", got)
	}
	if got := channelWorkRunRecoveryState(namedRun, false, session.Session{}, false, channels.WorkRunRecoveryActive); got != channels.WorkRunRecoveryMissing {
		t.Fatalf("missing named session recovery = %q, want missing", got)
	}
}

func TestChannelWorkRecoverySettlesFailedAndInterruptedNamedAgentTurns(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		turnStatus   TurnStatus
		runState     channels.WorkRunState
		bindingState channels.CollaborationSessionState
		workState    channels.WorkState
	}{
		{name: "failed", turnStatus: TurnStatusFailed, runState: channels.WorkRunFailed, bindingState: channels.CollaborationSessionIdle, workState: channels.WorkInterrupted},
		{name: "interrupted", turnStatus: TurnStatusInterrupted, runState: channels.WorkRunInterrupted, bindingState: channels.CollaborationSessionInterrupted, workState: channels.WorkInterrupted},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			rt := newTestRuntime(t, &fakeClient{})
			rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
			attachNamedAgentTestToolkit(t, rt)
			server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
			t.Cleanup(server.Close)
			credential, task, run, sessionRef := prepareRecoverableNamedAgentWork(t, server, rt)

			sessionClient, err := server.channelService.BindAgentSession(ctx, credential.Agent.ID, sessionRef)
			if err != nil {
				t.Fatalf("BindAgentSession() error = %v", err)
			}
			turnID := sessionRef + "-terminal-turn"
			if _, err := sessionClient.AttachWorkRunTurn(ctx, channels.WorkRunTurnParams{
				WorkID: task.ID, RunID: run.ID, TurnID: turnID,
			}); err != nil {
				t.Fatalf("AttachWorkRunTurn() error = %v", err)
			}
			if err := session.AppendHistoryRecord(rt.SessionDir, sessionRef, session.HistoryRecord{
				Role: "meta", Content: turnTerminalHistoryRecord, ClientID: turnID,
				StopReason: string(testCase.turnStatus),
			}); err != nil {
				t.Fatalf("AppendHistoryRecord(terminal) error = %v", err)
			}

			if err := server.reconcileChannelWorkRuns(ctx); err != nil {
				t.Fatalf("reconcileChannelWorkRuns() error = %v", err)
			}
			work, err := sessionClient.GetWork(ctx, task.ID)
			if err != nil {
				t.Fatalf("GetWork() error = %v", err)
			}
			settled := findWorkRun(work.Runs, run.ID)
			if settled.State != testCase.runState || settled.TurnID != turnID {
				t.Fatalf("settled run = %#v, want state %q turn %q", settled, testCase.runState, turnID)
			}
			if work.State != testCase.workState {
				t.Fatalf("recovered work state = %q, want %q", work.State, testCase.workState)
			}
			wake, err := server.channelService.WakeState(ctx, credential.Agent.ID)
			if err != nil {
				t.Fatalf("WakeState() error = %v", err)
			}
			if !wake.Outstanding {
				t.Fatalf("recovered terminal run did not wake owner: %#v", wake)
			}
			binding, err := server.channelService.GetCollaborationSession(ctx, credential.Agent.ID, credential.Token, sessionRef)
			if err != nil {
				t.Fatalf("GetCollaborationSession() error = %v", err)
			}
			if binding.State != testCase.bindingState || binding.RunID != "" {
				t.Fatalf("settled binding = %#v, want %q without run", binding, testCase.bindingState)
			}
			server.namedAgentMu.Lock()
			resumeErr := server.resumeNamedAgentBoundSessionsLocked(ctx, agentRuntimeFromNamed(credential.Agent))
			server.namedAgentMu.Unlock()
			if resumeErr != nil {
				t.Fatalf("resumeNamedAgentBoundSessionsLocked() error = %v", resumeErr)
			}
			if th := server.thread(sessionRef); th != nil {
				th.mu.Lock()
				reran := false
				for _, turn := range th.Turns {
					if turn.ID == turnID && turn.Status == TurnStatusInProgress {
						reran = true
						break
					}
				}
				th.mu.Unlock()
				if reran {
					t.Fatalf("terminal work turn %q was rerun in thread %#v", turnID, th)
				}
			}
		})
	}
}

func TestChannelWorkRecoverySettlesExactNamedAgentTurnBeforeResume(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, task, run, sessionRef := prepareRecoverableNamedAgentWork(t, server, rt)

	sessionClient, err := server.channelService.BindAgentSession(ctx, credential.Agent.ID, sessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	turnID := sessionRef + "-turn-0001"
	if _, err := sessionClient.AttachWorkRunTurn(ctx, channels.WorkRunTurnParams{
		WorkID: task.ID, RunID: run.ID, TurnID: turnID,
	}); err != nil {
		t.Fatalf("AttachWorkRunTurn() error = %v", err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sessionRef, session.HistoryRecord{
		Role: "meta", Content: turnTerminalHistoryRecord, ClientID: turnID,
		StopReason: string(TurnStatusCompleted),
	}); err != nil {
		t.Fatalf("AppendHistoryRecord(terminal) error = %v", err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sessionRef, session.HistoryRecord{
		Role: "meta", Content: turnTerminalHistoryRecord, ClientID: sessionRef + "-turn-0002",
		StopReason: string(TurnStatusCompleted),
	}); err != nil {
		t.Fatalf("AppendHistoryRecord(later terminal) error = %v", err)
	}

	if err := server.reconcileChannelWorkRuns(ctx); err != nil {
		t.Fatalf("reconcileChannelWorkRuns() error = %v", err)
	}
	work, err := sessionClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	settled := findWorkRun(work.Runs, run.ID)
	if settled.State != channels.WorkRunCompleted || settled.TurnID != turnID {
		t.Fatalf("settled run = %#v, want completed turn %q", settled, turnID)
	}
	binding, err := server.channelService.GetCollaborationSession(ctx, credential.Agent.ID, credential.Token, sessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession() error = %v", err)
	}
	if binding.State != channels.CollaborationSessionIdle || binding.RunID != "" {
		t.Fatalf("settled binding = %#v, want idle without run", binding)
	}
	agent := agentRuntimeFromNamed(credential.Agent)
	server.namedAgentMu.Lock()
	resumeErr := server.resumeNamedAgentBoundSessionsLocked(ctx, agent)
	server.namedAgentMu.Unlock()
	if resumeErr != nil {
		t.Fatalf("resumeNamedAgentBoundSessionsLocked() error = %v", resumeErr)
	}
	if th := server.thread(sessionRef); th != nil {
		t.Fatalf("completed work turn was rerun in thread %#v", th)
	}
}

func TestChannelMaintenanceRetriesRunningBindingWithoutOutstandingWake(t *testing.T) {
	ctx := context.Background()
	stream := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(func() {
		select {
		case <-stream.release:
		default:
			close(stream.release)
		}
		server.Close()
	})
	credential, task, run, sessionRef := prepareRecoverableNamedAgentWork(t, server, rt)
	state, err := server.channelService.WakeState(ctx, credential.Agent.ID)
	if err != nil || state.Outstanding || state.Pending {
		t.Fatalf("wake state before maintenance = %#v, err %v", state, err)
	}

	server.runChannelMaintenance(ctx)
	th := server.thread(sessionRef)
	if th == nil || !threadIsRunning(th) {
		t.Fatalf("maintenance did not resume running binding: thread %#v", th)
	}
	th.mu.Lock()
	turnID := th.currentTurn
	th.mu.Unlock()
	sessionClient, err := server.channelService.BindAgentSession(ctx, credential.Agent.ID, sessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	work, err := sessionClient.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetWork() error = %v", err)
	}
	attached := findWorkRun(work.Runs, run.ID)
	if attached.TurnID == "" || attached.TurnID != turnID {
		t.Fatalf("resumed run = %#v, current turn %q", attached, turnID)
	}
	records, err := session.LoadHistoryRecords(rt.SessionDir, sessionRef, false)
	if err != nil {
		t.Fatalf("LoadHistoryRecords() error = %v", err)
	}
	foundStableWake := false
	for _, record := range records {
		if record.Role == "user" && (record.ClientID == namedAgentWorkWakeTurnID(run.ID) || strings.HasPrefix(record.ClientID, "collaboration-delivery:")) {
			foundStableWake = true
			break
		}
	}
	if !foundStableWake {
		t.Fatalf("work session history has no stable wake for run %q", run.ID)
	}
}

func TestNamedAgentDispatchErrorKeepsWakeRetryableWithoutTarget(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	server.channelService.SetWakeSink(nil)
	recipient, err := server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Recipient", Autostart: true})
	if err != nil {
		t.Fatalf("CreateNamedAgent(recipient) error = %v", err)
	}
	sender, err := server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Sender"})
	if err != nil {
		t.Fatalf("CreateNamedAgent(sender) error = %v", err)
	}
	room, err := server.channelService.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Dispatch retry", CreatedBy: "human-1",
		Members: []channels.RoomMember{
			{MemberType: channels.MemberHuman, MemberID: "human-1"},
			{MemberType: channels.MemberAgent, MemberID: recipient.Agent.ID},
			{MemberType: channels.MemberAgent, MemberID: sender.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	recipientClient, err := server.channelService.BindAgent(ctx, recipient.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(recipient) error = %v", err)
	}
	const targetSession = "vanishing-coordination-session"
	if _, err := recipientClient.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{
		SessionRef: targetSession, RoomID: room.ID, Purpose: channels.CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	senderClient, err := server.channelService.BindAgent(ctx, sender.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(sender) error = %v", err)
	}
	if _, err := senderClient.SendCollaboration(ctx, channels.CollaborationSendParams{
		RoomID: room.ID, ToAgentID: recipient.Agent.ID, TargetSessionRef: targetSession,
		Kind: channels.CollaborationControl, Body: "retry me",
	}); err != nil {
		t.Fatalf("SendCollaboration() error = %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(server.channelService.Dir(), "channels.sqlite3"))
	if err != nil {
		t.Fatalf("open channels database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM collaboration_session_bindings WHERE session_ref = ?`, targetSession); err != nil {
		_ = db.Close()
		t.Fatalf("delete target session fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close channels database fixture: %v", err)
	}

	if err := server.deliverNamedAgentWake(ctx, recipient.Agent.ID); err == nil {
		t.Fatal("deliverNamedAgentWake() succeeded without its durable target")
	}
	state, err := server.channelService.WakeState(ctx, recipient.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState() error = %v", err)
	}
	if !state.Outstanding || !state.Pending {
		t.Fatalf("dispatch failure cleared retry state: %#v", state)
	}
}

func prepareRecoverableNamedAgentWork(t *testing.T, server *Server, rt *runtime.Session) (channels.AgentCredential, channels.Message, channels.WorkRun, string) {
	t.Helper()
	ctx := context.Background()
	// Recovery is driven explicitly below; startup maintenance must not observe a partial fixture.
	server.stopChannelMaintenance()
	server.channelService.SetWakeSink(nil)
	credential, err := server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Alpha", Autostart: true})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	task, err := server.channelService.CreateTaskHuman(ctx, channels.TaskCreateParams{
		RoomID: room.ID, HumanID: "human-1", OwnerID: credential.Agent.ID,
		Title: "Recover durable work", Body: "Continue this work after restart.",
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	agent := agentRuntimeFromNamed(credential.Agent)
	sessionRef := namedAgentWorkSessionID(agent, task.ID)
	agentClient, err := server.channelService.BindAgent(ctx, credential.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	if _, err := agentClient.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{
		SessionRef: sessionRef, RoomID: room.ID, WorkID: task.ID,
		Purpose: channels.CollaborationSessionWork, State: channels.CollaborationSessionIdle,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	sessionClient, err := server.channelService.BindAgentSession(ctx, credential.Agent.ID, sessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	run, err := sessionClient.StartWorkRun(ctx, channels.WorkRunStartParams{WorkID: task.ID, Kind: channels.WorkRunProducer})
	if err != nil {
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if err := server.channelService.RoutePendingCollaborationToSession(ctx, credential.Agent.ID, task.ID, sessionRef); err != nil {
		t.Fatalf("RoutePendingCollaborationToSession() error = %v", err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, sessionRef, filepath.Dir(credential.Agent.MemoryDir)); err != nil {
		t.Fatalf("CreateWithMetadata() error = %v", err)
	}
	if _, err := session.SetSource(rt.SessionDir, sessionRef, namedAgentSessionSource+credential.Agent.ID); err != nil {
		t.Fatalf("SetSource() error = %v", err)
	}
	if err := server.channelService.ClearWakeOnCheck(ctx, credential.Agent.ID); err != nil {
		t.Fatalf("ClearWakeOnCheck() error = %v", err)
	}
	return credential, task, run, sessionRef
}

func findWorkRun(runs []channels.WorkRun, runID string) channels.WorkRun {
	for _, run := range runs {
		if run.ID == runID {
			return run
		}
	}
	return channels.WorkRun{}
}
