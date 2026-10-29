package agentcontrol

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestUnconfirmedTerminalIsNotReturnedAsSynchronousSuccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		staleDraft bool
	}{
		{name: "prepare_never_succeeded"},
		{name: "confirmation_rejects_stale_generation", staleDraft: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
			var failed atomic.Bool
			c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
				if failed.CompareAndSwap(false, true) {
					if !tc.staleDraft {
						return subagent.ErrTerminalPrepareStopped
					}
					n.TerminalGeneration = "stale"
				}
				return c.prepareWorkerTerminal(n)
			})
			entered := make(chan string, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			c.SetWorkerExecutionReleaseHookForTest(func(id string) {
				select {
				case entered <- id:
				default:
				}
				<-release
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type outcome struct {
				result *SpawnResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "uncertain", Prompt: "finish", Synchronous: true})
				done <- outcome{result, err}
			}()
			var id string
			select {
			case id = <-entered:
			case <-ctx.Done():
				t.Fatal("release hook not reached")
			}
			snap := c.Manager().Get(id).Snapshot()
			if snap.Status != subagent.StatusInterrupted || snap.Error == nil || !strings.Contains(snap.Error.Error(), "not confirmed") {
				t.Errorf("lease release exposed unconfirmed candidate as %+v", snap)
			}
			if tc.staleDraft {
				un := c.SubscribeWorkerTerminalFinalizer(func(subagent.Notification) error { return nil })
				defer un()
				c.StartWorkerTerminalRecovery()
			}
			waitDone := make(chan error, 1)
			waitStarted := make(chan struct{})
			go func() { close(waitStarted); _, err := c.Wait(ctx, id); waitDone <- err }()
			<-waitStarted
			followup := make(chan error, 1)
			go func() { _, err := c.FollowupTask(ctx, id, "continue"); followup <- err }()
			select {
			case err := <-followup:
				t.Errorf("followup crossed terminal release: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			received := <-done
			if received.err == nil || (received.result != nil && received.result.Status == "completed") {
				t.Fatalf("unconfirmed completed exposed: %+v, %v", received.result, received.err)
			}
			if err := <-waitDone; err == nil {
				t.Fatal("Wait returned an unconfirmed terminal without error")
			}
			select {
			case err := <-followup:
				if err != nil {
					t.Fatalf("explicit followup after recovery: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("followup did not finish")
			}
			resumed, err := c.Wait(ctx, id)
			if err != nil || resumed.Status != subagent.StatusCompleted {
				t.Fatalf("resumed outcome=%+v err=%v", resumed, err)
			}
		})
	}
}

func TestConfirmedTerminalStillReturnsSynchronousSuccess(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "confirmed"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "normal", Prompt: "finish", Synchronous: true})
	if err != nil || got == nil || got.Status != "completed" || got.Result != "confirmed" {
		t.Fatalf("confirmed result=%+v err=%v", got, err)
	}
}

func TestInterruptedSynchronousWaitDoesNotReturnUnconfirmedTerminal(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
	var failed atomic.Bool
	c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
		if failed.CompareAndSwap(false, true) {
			return subagent.ErrTerminalPrepareStopped
		}
		return c.prepareWorkerTerminal(n)
	})
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c.SetWorkerExecutionReleaseHookForTest(func(id string) {
		select {
		case entered <- id:
		default:
		}
		<-release
	})
	interrupt := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		value *SpawnResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		v, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "uncertain_interrupt", Prompt: "finish", Synchronous: true, WaitInterrupt: interrupt})
		done <- result{v, err}
	}()
	id := <-entered
	close(interrupt)
	got := <-done
	if got.err != nil || got.value == nil || !got.value.Backgrounded || got.value.Result != "" || got.value.ResultID != "" {
		t.Fatalf("interrupt exposed outcome: %+v %v", got.value, got.err)
	}
	once.Do(func() { close(release) })
	if _, err := c.Wait(ctx, id); err == nil {
		t.Fatal("Wait accepted unconfirmed terminal")
	}
	if _, err := c.FollowupTask(ctx, id, "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfirmedTerminalIsNotVisibleWhileConfirmationIsBlocked(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "pending candidate"}}, nil)
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c.SetWorkerTerminalTransitionHookForTest(func(id string) { entered <- id; <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "pending_snapshot", Prompt: "finish"})
	if err != nil {
		t.Fatal(err)
	}
	id := <-entered
	snapshot := c.Manager().Get(id).Snapshot()
	if snapshot.Status != subagent.StatusRunning || snapshot.Result != "" || !snapshot.CompletedAt.IsZero() {
		t.Fatalf("public snapshot before confirmation: %+v", snapshot)
	}
	list := c.List()
	if len(list) != 1 || list[0].Status != subagent.StatusRunning || list[0].Result != "" {
		t.Fatalf("list before confirmation: %+v", list)
	}
	if got.Status == "completed" {
		t.Fatalf("async spawn reported unconfirmed completion: %+v", got)
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	pending, err := c.Wait(short, id)
	if err == nil || pending.Status != subagent.StatusRunning || pending.Result != "" {
		t.Fatalf("wait timeout exposed candidate: %+v %v", pending, err)
	}
	once.Do(func() { close(release) })
	final, err := c.Wait(ctx, id)
	if err != nil || final.Status != subagent.StatusCompleted || final.Result != "pending candidate" {
		t.Fatalf("confirmed terminal=%+v err=%v", final, err)
	}
}

func TestUnconfirmedTerminalDoesNotLeaveCompletedHistory(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "control")
	c := newDormantQueuedSpawnConcurrencyControl(t, root, state, filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
	var failed atomic.Bool
	c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
		if failed.CompareAndSwap(false, true) {
			return subagent.ErrTerminalPrepareStopped
		}
		return c.prepareWorkerTerminal(n)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "unknown_history", Prompt: "finish", Synchronous: true})
	if err == nil || got != nil {
		t.Fatalf("unconfirmed synchronous result=%+v err=%v", got, err)
	}
	list := c.List()
	if len(list) != 1 {
		t.Fatalf("workers=%+v", list)
	}
	id := list[0].ID
	persisted, err := subagent.LoadPersistedRun(filepath.Join(state, "workers", id+".json"))
	if err != nil || persisted.Status != subagent.StatusInterrupted || !strings.Contains(persisted.Error, "not confirmed") {
		t.Errorf("unconfirmed history=%+v err=%v", persisted, err)
	}
	if _, err := c.FollowupTask(ctx, id, "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfirmedHistoryFailureRetainsLeaseUntilWritable(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "control")
	c := newDormantQueuedSpawnConcurrencyControl(t, root, state, filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
	var failed atomic.Bool
	c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
		if failed.CompareAndSwap(false, true) {
			return subagent.ErrTerminalPrepareStopped
		}
		return c.prepareWorkerTerminal(n)
	})
	injected := make(chan string, 1)
	c.SetWorkerTerminalTransitionHookForTest(func(id string) {
		path := filepath.Join(state, "workers", id+".json")
		if err := os.Remove(path); err != nil {
			t.Errorf("remove completed history: %v", err)
			return
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Errorf("block history replacement: %v", err)
			return
		}
		injected <- id
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "blocked_history", Prompt: "finish"})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	select {
	case id = <-injected:
	case <-ctx.Done():
		t.Fatal("failure injection not reached")
	}
	if result.AgentID != id {
		t.Fatal("wrong worker")
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		worker := c.Manager().Get(id)
		return worker != nil && worker.Snapshot().Status == subagent.StatusInterrupted
	}, "in-memory unknown outcome")
	if !c.workerExecutionOwned(id) {
		t.Fatal("lease released without a durable unknown outcome")
	}
	short, stop := context.WithTimeout(ctx, 60*time.Millisecond)
	defer stop()
	if _, err := c.Wait(short, id); err == nil {
		t.Fatal("wait succeeded while history is unavailable")
	}
	if err := os.Remove(filepath.Join(state, "workers", id+".json")); err != nil {
		t.Fatal(err)
	}
	c.SetWorkerTerminalTransitionHookForTest(nil)
	if _, err := c.Wait(ctx, id); err == nil {
		t.Fatal("wait accepted unconfirmed result")
	}
	run, err := subagent.LoadPersistedRun(filepath.Join(state, "workers", id+".json"))
	if err != nil || run.Status != subagent.StatusInterrupted {
		t.Fatalf("durable outcome=%+v err=%v", run, err)
	}
	if _, err := c.FollowupTask(ctx, id, "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfirmedOldNotificationDoesNotOverwriteFollowup(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "control")
	c := newDormantQueuedSpawnConcurrencyControl(t, root, state, filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
	var failPrepare atomic.Bool
	c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
		if failPrepare.CompareAndSwap(false, true) {
			return subagent.ErrTerminalPrepareStopped
		}
		return c.prepareWorkerTerminal(n)
	})
	released := make(chan string, 1)
	resume := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(resume) }) })
	var first atomic.Bool
	c.Manager().SetTerminalObserver(func(n subagent.Notification) error {
		err := c.consumeWorkerTerminal(n)
		if first.CompareAndSwap(false, true) {
			released <- n.AgentID
			<-resume
		}
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		value *SpawnResult
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		v, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "stale_notification", Prompt: "finish", Synchronous: true})
		done <- outcome{v, err}
	}()
	id := <-released
	if _, err := c.FollowupTask(ctx, id, "resume"); err != nil {
		t.Fatal(err)
	}
	next, err := c.Wait(ctx, id)
	if err != nil || next.Status != subagent.StatusCompleted {
		t.Fatalf("followup=%+v err=%v", next, err)
	}
	once.Do(func() { close(resume) })
	prior := <-done
	if prior.err == nil || (prior.value != nil && prior.value.Status == "completed") {
		t.Fatalf("old unconfirmed result=%+v err=%v", prior.value, prior.err)
	}
	persisted, err := subagent.LoadPersistedRun(filepath.Join(state, "workers", id+".json"))
	if err != nil || persisted.Status != subagent.StatusCompleted {
		t.Fatalf("old notification overwrote followup: %+v err=%v", persisted, err)
	}
}

func TestUnconfirmedSynchronousForkDoesNotReturnCompletion(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
	var failed atomic.Bool
	c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
		if failed.CompareAndSwap(false, true) {
			return subagent.ErrTerminalPrepareStopped
		}
		return c.prepareWorkerTerminal(n)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := c.Fork(ctx, ForkRequest{TaskName: "fork_unknown", Prompt: "finish", Synchronous: true}, []providers.ChatMessage{{Role: "user", Content: "fork task"}})
	if err == nil || got != nil {
		t.Fatalf("unconfirmed fork=%+v err=%v", got, err)
	}
	list := c.List()
	if len(list) != 1 {
		t.Fatalf("workers=%+v", list)
	}
	if _, err := c.FollowupTask(ctx, list[0].ID, "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfirmedHistorySurvivesProcessExit(t *testing.T) {
	root := os.Getenv("WUU_UNCONFIRMED_WAIT_CRASH_ROOT")
	if root != "" {
		state := filepath.Join(root, "control")
		c := newDormantQueuedSpawnConcurrencyControl(t, root, state, filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "candidate"}}, nil)
		c.Manager().SetTerminalPrepareObserver(func(subagent.Notification) error { return subagent.ErrTerminalPrepareStopped })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if got, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "unknown_exit", Prompt: "finish", Synchronous: true}); err == nil || got != nil {
			os.Exit(61)
		}
		list := c.List()
		if len(list) != 1 {
			os.Exit(62)
		}
		if err := os.WriteFile(filepath.Join(root, "worker-id"), []byte(list[0].ID), 0600); err != nil {
			os.Exit(63)
		}
		os.Exit(0)
	}
	root = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, exe, "-test.run=^TestUnconfirmedHistorySurvivesProcessExit$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "WUU_UNCONFIRMED_WAIT_CRASH_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("worker child: %v\n%s", err, output)
	}
	idBytes, err := os.ReadFile(filepath.Join(root, "worker-id"))
	if err != nil {
		t.Fatal(err)
	}
	id := string(idBytes)
	run, err := subagent.LoadPersistedRun(filepath.Join(root, "control", "workers", id+".json"))
	if err != nil || run.Status != subagent.StatusInterrupted || !strings.Contains(run.Error, "not confirmed") {
		t.Fatalf("recovered run=%+v err=%v", run, err)
	}
	client := &queuedSpawnCountingClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	c.StartWorkerTerminalRecovery()
	c.StartQueuedWork()
	if client.calls.Load() != 0 {
		t.Fatal("restart automatically reran unconfirmed worker")
	}
	if _, err := c.FollowupTask(ctx, id, "explicit followup"); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Wait(ctx, id)
	if err != nil || snap.Status != subagent.StatusCompleted {
		t.Fatalf("explicit followup=%+v err=%v", snap, err)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("explicit followup model calls=%d", client.calls.Load())
	}
}

type orderedTerminalClient struct{ calls atomic.Int32 }

func (*orderedTerminalClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (c *orderedTerminalClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	content := "first result"
	if c.calls.Add(1) > 1 {
		content = "followup result"
	}
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: content}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func TestSynchronousFirstTurnKeepsItsOwnConfirmedResult(t *testing.T) {
	for _, fork := range []bool{false, true} {
		t.Run(fmt.Sprintf("fork_%t", fork), func(t *testing.T) {
			root := t.TempDir()
			client := &orderedTerminalClient{}
			c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
			released := make(chan string, 1)
			resume := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(resume) }) })
			var first atomic.Bool
			c.Manager().SetTerminalObserver(func(n subagent.Notification) error {
				err := c.consumeWorkerTerminal(n)
				if first.CompareAndSwap(false, true) {
					released <- n.AgentID
					<-resume
				}
				return err
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type outcome struct {
				value *SpawnResult
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				var v *SpawnResult
				var err error
				if fork {
					v, err = c.Fork(ctx, ForkRequest{TaskName: "first", Prompt: "finish", Synchronous: true}, []providers.ChatMessage{{Role: "user", Content: "delegate"}})
				} else {
					v, err = c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "first", Prompt: "finish", Synchronous: true})
				}
				done <- outcome{v, err}
			}()
			id := <-released
			if _, err := c.FollowupTask(ctx, id, "second"); err != nil {
				t.Fatal(err)
			}
			second, err := c.Wait(ctx, id)
			if err != nil || second.Result != "followup result" {
				t.Fatalf("followup=%+v err=%v", second, err)
			}
			once.Do(func() { close(resume) })
			got := <-done
			if got.err != nil || got.value == nil || got.value.Status != "completed" || got.value.Result != "first result" {
				t.Fatalf("initial synchronous outcome=%+v err=%v", got.value, got.err)
			}
		})
	}
}
