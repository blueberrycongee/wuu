//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Schema correction is part of the accepted Run, even if another worker owns
// its next admission slot. Capacity waits must neither fail the Run nor append
// a correction early, and cancellation/Close must prevent delayed model calls.
func TestProjectCapacityRetainsSchemaContinuation(t *testing.T) {
	for _, outcome := range []string{"resume", "interrupt", "turn_interrupt", "close"} {
		t.Run(outcome, func(t *testing.T) {
			srv, client, calls, rt := newProjectFixture(t)
			setProjectWorkerLimit(t, srv, 2)
			lead := startProject(t, client, "Schema continuation")
			worker := capacityWorker(t, srv, lead.ID, "schema-worker", "worker")
			holder := capacityWorker(t, srv, lead.ID, "schema-holder", "worker")
			journal, err := session.NewInferenceJournalRuntime(rt.SessionDir, "schema-capacity-test")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			rt.InferenceJournalRuntime = journal
			rt.ActivityRegistry = activity.NewRegistry()

			type scheduled struct {
				delay   time.Duration
				fire    func()
				stopped *atomic.Bool
			}
			timers := make(chan scheduled, 16)
			srv.projectInboxAfterFunc = func(delay time.Duration, fire func()) func() {
				stopped := &atomic.Bool{}
				timers <- scheduled{delay, fire, stopped}
				return func() { stopped.Store(true) }
			}
			var started RunStartResult
			client.rpc(t, MethodRunStart, RunStartParams{
				ThreadID: worker.ID, Prompt: "Return schema-capacity JSON", OutputSchema: json.RawMessage(`{"type":"object","required":["ok"]}`),
				Request: execution.Request{Mode: execution.ModeStart},
			}, &started)
			initial := calls.next(t, "schema-capacity JSON")
			lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, holder.ID)
			if err != nil || !acquired {
				t.Fatalf("hold capacity: acquired=%v error=%v", acquired, err)
			}
			defer lease.Release()
			// Lowering the admission cap does not cancel the already-running turn.
			setProjectWorkerLimit(t, srv, 1)
			initial.response <- providersResponse(`{"wrong":true}`)

			var timer scheduled
			select {
			case timer = <-timers:
			case <-time.After(gatedProviderTimeout):
				run, _ := srv.runStore.Get(context.Background(), started.Run.ID)
				t.Fatalf("schema continuation was not retained for retry: %+v", run)
			}
			if timer.delay != threadExecutionLeaseRetryDelay {
				t.Fatalf("initial retry delay=%v", timer.delay)
			}
			run, err := srv.runStore.Get(context.Background(), started.Run.ID)
			if err != nil || run.Status.Terminal() || len(run.Turns) != 1 || !srv.executionRunAttached(run.ID) {
				t.Fatalf("capacity-blocked Run=%+v error=%v", run, err)
			}
			assertSchemaUserTurns(t, srv, worker.ID, 1)
			calls.assertIdle(t)

			// Repeated callbacks while capacity remains full must back off and
			// leave both the accepted Run and durable input unchanged.
			timer.fire()
			select {
			case timer = <-timers:
			case <-time.After(gatedProviderTimeout):
				t.Fatal("blocked schema continuation lost its retry")
			}
			if timer.delay <= threadExecutionLeaseRetryDelay || timer.delay > projectInboxMaxRetryDelay {
				t.Fatalf("retry backoff=%v", timer.delay)
			}
			assertSchemaUserTurns(t, srv, worker.ID, 1)
			calls.assertIdle(t)

			switch outcome {
			case "interrupt":
				var interrupted RunInterruptResult
				client.rpc(t, MethodRunInterrupt, RunInterruptParams{RunID: run.ID}, &interrupted)
				if interrupted.Run.Status != execution.StatusInterrupted {
					t.Fatalf("interrupted Run=%+v", interrupted.Run)
				}
			case "turn_interrupt":
				client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: worker.ID}, nil)
				interrupted, err := srv.runStore.Get(context.Background(), run.ID)
				if err != nil || interrupted.Status != execution.StatusInterrupted {
					t.Fatalf("thread stop left Run=%+v error=%v", interrupted, err)
				}
			case "close":
				srv.Close()
				if !timer.stopped.Load() {
					t.Fatal("Close did not cancel the schema capacity timer")
				}
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			timer.fire()
			if outcome != "resume" {
				// Join any accepted scheduler work and verify stale callbacks cannot
				// recreate a model turn after the run was interrupted or closed.
				srv.Close()
				assertSchemaUserTurns(t, srv, worker.ID, 1)
				calls.assertIdle(t)
				return
			}
			retry := calls.next(t, "schema-capacity JSON")
			retry.response <- providersResponse(`{"ok":true}`)
			deadline := time.Now().Add(gatedProviderTimeout)
			for time.Now().Before(deadline) {
				run, err = srv.runStore.Get(context.Background(), run.ID)
				if err == nil && run.Status.Terminal() {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err != nil || run.Status != execution.StatusCompleted || len(run.Turns) != 2 {
				t.Fatalf("resumed Run=%+v error=%v", run, err)
			}
			assertSchemaUserTurns(t, srv, worker.ID, 2)
			srv.Close()
			calls.assertIdle(t)
		})
	}
}

func assertSchemaUserTurns(t *testing.T, srv *Server, threadID string, want int) {
	t.Helper()
	history, err := loadChatMessages(srv.rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range history {
		if message.Role == "user" {
			count++
		}
	}
	if count != want {
		t.Fatalf("durable user turns=%d, want %d", count, want)
	}
}

// A stop in another server can change only the durable control fence while
// capacity is full. The retained correction must observe that fence before a
// later admission, even though no local run/interrupt callback was delivered.
func TestProjectCapacitySchemaContinuationHonorsRemoteStop(t *testing.T) {
	for _, adoptWhileRunning := range []bool{false, true} {
		name := "existing_member"
		if adoptWhileRunning {
			name = "adopted_while_running"
		}
		t.Run(name, func(t *testing.T) {
			srv, client, calls, rt := newProjectFixture(t)
			setProjectWorkerLimit(t, srv, 2)
			lead := startProject(t, client, "Schema stop fence")
			worker := capacityWorker(t, srv, lead.ID, "schema-stopped-worker", "worker")
			holder := capacityWorker(t, srv, lead.ID, "schema-stopped-holder", "worker")
			var control session.Control
			var err error
			if adoptWhileRunning {
				if _, err = session.SetProjectMembership(rt.SessionDir, worker.ID, "", "", ""); err != nil {
					t.Fatal(err)
				}
			} else {
				control, err = session.ChangeControl(rt.SessionDir, worker.ID, lead.ID, session.ControlActive, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Keep result reporting durable but out of the model so the only eligible
			// inference in this test is the schema Run under examination.
			leadLease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, lead.ID)
			if err != nil || !acquired {
				t.Fatalf("hold coordinator: %v %v", acquired, err)
			}
			defer leadLease.Release()
			journal, err := session.NewInferenceJournalRuntime(rt.SessionDir, "schema-stop-test")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			rt.InferenceJournalRuntime = journal
			rt.ActivityRegistry = activity.NewRegistry()
			timers := make(chan func(), 16)
			srv.projectInboxAfterFunc = func(_ time.Duration, fire func()) func() {
				timers <- fire
				return func() {}
			}
			var started RunStartResult
			client.rpc(t, MethodRunStart, RunStartParams{
				ThreadID: worker.ID, Prompt: "Return stopped-schema JSON", OutputSchema: json.RawMessage(`{"type":"object","required":["ok"]}`),
				Request: execution.Request{Mode: execution.ModeStart},
			}, &started)
			initial := calls.next(t, "stopped-schema JSON")
			if adoptWhileRunning {
				if _, err := session.SetProjectMembership(rt.SessionDir, worker.ID, projectSessionSource, lead.ID, ""); err != nil {
					t.Fatal(err)
				}
				control, err = session.ChangeControl(rt.SessionDir, worker.ID, lead.ID, session.ControlActive, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, holder.ID)
			if err != nil || !acquired {
				t.Fatalf("hold worker: %v %v", acquired, err)
			}
			defer lease.Release()
			setProjectWorkerLimit(t, srv, 1)
			initial.response <- providersResponse(`{"wrong":true}`)
			var fire func()
			select {
			case fire = <-timers:
			case <-time.After(gatedProviderTimeout):
				t.Fatal("capacity drain did not schedule its retry")
			}
			// Coordinator reporting can arm the shared timer first. Wait until the
			// accepted Run has actually parked its correction before the remote stop.
			deferred := false
			deadline := time.Now().Add(gatedProviderTimeout)
			for time.Now().Before(deadline) {
				srv.runMu.Lock()
				tracker := srv.runs[started.Run.ID]
				deferred = tracker != nil && tracker.schemaRetry != nil
				srv.runMu.Unlock()
				if deferred {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !deferred {
				t.Fatal("schema correction never entered its capacity wait")
			}
			if _, err := session.ChangeControl(rt.SessionDir, worker.ID, lead.ID, session.ControlActive, control.Revision); err != nil {
				t.Fatal(err)
			}
			// Agent/process completion continuations retain the Run ID but build
			// a fresh runtime snapshot. They must not lose the Run's original
			// control fence before reaching schema correction admission.
			err = srv.startExecutionSchemaRetry(context.Background(), worker, turnRuntimeSnapshot{ExecutionRunID: started.Run.ID}, "Correct the structured output")
			if !errors.Is(err, session.ErrControlChanged) {
				t.Fatalf("schema continuation without snapshot control bypassed the Run fence: %v", err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			fire()
			deadline = time.Now().Add(gatedProviderTimeout)
			var run execution.Run
			for time.Now().Before(deadline) {
				run, err = srv.runStore.Get(context.Background(), started.Run.ID)
				if err == nil && run.Status.Terminal() {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err != nil || run.Status != execution.StatusInterrupted {
				t.Fatalf("remote stop left Run=%+v error=%v", run, err)
			}
			srv.Close()
			assertSchemaUserTurns(t, srv, worker.ID, 1)
			calls.assertIdle(t)

		})
	}
}
