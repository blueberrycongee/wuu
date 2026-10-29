package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestUnconfirmedTerminalRecoverySurfacesWithoutAutoContinuation(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	const rootID = "unknown-root"
	const id = "unknown_worker"
	const pid = "unknown-participant"
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.UpsertParticipant(participant.Participant{ID: pid, Kind: participant.KindNamed, Name: "Unknown"}); err != nil {
		t.Fatal(err)
	}
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	c, err := agentcontrol.New(agentcontrol.Config{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), Client: newBlockingStreamClient("unused"), DefaultModel: "fake-model", ParentRepo: rt.RootDir, SessionID: rootID, HistoryDir: filepath.Join(artifactDir, "workers"), ThreadDir: filepath.Join(artifactDir, "threads"), HarnessDir: filepath.Join(artifactDir, "harness"), WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		return noopToolExecutor{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	s := New(rt, out)
	binding := testRuntimeState(t, rt)
	th := newThreadState(rootID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	th.state = binding
	th.running = true
	th.execRuntime = &runtime.ThreadRuntime{AgentControl: c}
	s.mu.Lock()
	s.threads[rootID] = th
	s.mu.Unlock()
	t.Cleanup(func() {
		th.mu.Lock()
		th.running = false
		th.mu.Unlock()
		c.BeginShutdown()
		c.StopAll()
		c.YieldWorkerTerminalFinalizations()
		c.Close()
		s.Close()
	})
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, ParticipantID: pid, Status: subagent.StatusCompleted, ParentID: rootID, AgentPath: "/root/unknown_worker", Result: "candidate result"}}
	rec := map[string]any{"schema_version": "wuu/worker-terminal-finalization/v1", "phase": "draft", "generation": "test-generation", "agent_id": id, "status": "completed", "snapshot": map[string]any{"id": id, "participant_id": pid, "status": "completed", "parent_id": rootID, "agent_path": "/root/unknown_worker", "result": "candidate result"}, "created_at": time.Now().UTC()}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(artifactDir, "harness", "terminal-finalizations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(id))
	if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(hash[:])+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	finalized := make(chan error, 1)
	var attempts atomic.Int32
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error {
		err := s.finalizeAgentTerminalWithCompleter(binding, rootID, c, n, func(pid, agentID, outcome, summary string) error {
			if attempts.Add(1) == 1 {
				return errors.New("temporary participant failure")
			}
			return rt.Sessions.CompleteParticipantRun(pid, agentID, outcome, summary)
		})
		if err == nil {
			select {
			case finalized <- nil:
			default:
			}
		}
		return err
	})
	defer un()
	c.StartWorkerTerminalRecovery()
	select {
	case err := <-finalized:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown terminal finalization not observed")
	}
	msgs := waitForMethod(t, out, NotificationAgentMailbox)
	updated := remarshal[AgentUpdatedNotification](t, notificationByMethod(t, msgs, NotificationAgentUpdated)["params"])
	mailbox := remarshal[AgentMailboxNotification](t, notificationByMethod(t, msgs, NotificationAgentMailbox)["params"])
	if attempts.Load() < 2 {
		t.Fatalf("participant finalizer attempts=%d, want retry", attempts.Load())
	}
	if got := len(notificationsByMethod(msgs, NotificationAgentMailbox)); got != 1 {
		t.Fatalf("unknown mailbox emitted %d times", got)
	}
	if updated.Agent.Status != "interrupted" || !strings.Contains(updated.Agent.Error, "not confirmed") || !strings.Contains(updated.Agent.Error, "tool effects may already have occurred") {
		t.Fatalf("updated = %+v", updated.Agent)
	}
	if mailbox.Message.Status != "interrupted" || mailbox.Message.Result != "candidate result" || !strings.Contains(mailbox.Message.Error, "not confirmed") {
		t.Fatalf("mailbox = %+v", mailbox.Message)
	}
	runs, err := rt.Sessions.ListParticipantRuns(pid, 0)
	if err != nil || len(runs) != 1 || runs[0].Outcome != "interrupted" || !strings.Contains(runs[0].Summary, "not confirmed") {
		t.Fatalf("participant runs=%+v err=%v", runs, err)
	}
	s.agentCompletionMu.Lock()
	pending := len(s.pendingAgentCompletionTurns[rootID])
	s.agentCompletionMu.Unlock()
	if pending != 0 {
		t.Fatalf("unknown result enqueued %d automatic turns", pending)
	}
	if c.AgentResultDeliveryID(n.Snapshot) == "" {
		t.Fatal(errors.New("test candidate has no delivery id"))
	}
}

func TestLateTerminalSnapshotDoesNotFinalizeAgentAgain(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out)
	defer s.Close()
	ch := make(chan subagent.Notification, 1)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { s.forwardAgentNotifications(testRuntimeState(t, rt), "root", nil, ch, done); close(finished) }()
	ch <- subagent.Notification{AgentID: "worker", Status: subagent.StatusCompleted, SnapshotOnly: true, Snapshot: subagent.SubAgentSnapshot{ID: "worker", Status: subagent.StatusCompleted}}
	close(ch)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot forwarder did not drain")
	}
	if got := out.String(); got != "" {
		t.Fatalf("late snapshot emitted terminal notification: %s", got)
	}
}

func TestTerminalFinalizationDeduplicatesWithinGeneration(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out)
	defer s.Close()
	binding := testRuntimeState(t, rt)
	now := time.Now().UTC()
	n := subagent.Notification{AgentID: "worker", Status: subagent.StatusInterrupted, TerminalGeneration: "g1", TerminalUnconfirmed: true, Snapshot: subagent.SubAgentSnapshot{ID: "worker", ParticipantID: "participant", Status: subagent.StatusInterrupted, ParentID: "root", AgentPath: "/root/worker", CompletedAt: now, Error: errors.New("outcome not confirmed")}}
	var calls atomic.Int32
	complete := func(string, string, string, string) error { calls.Add(1); return nil }
	for _, generation := range []string{"g1", "g1", "g2", "g2"} {
		n.TerminalGeneration = generation
		if err := s.finalizeAgentTerminalWithCompleter(binding, "root", nil, n, complete); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("participant finalizations=%d, want one per generation", calls.Load())
	}
}
