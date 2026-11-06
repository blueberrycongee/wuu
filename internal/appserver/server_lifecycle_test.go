package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A managed-session pass loses its connection when registration closes. Give
// active turns the host shutdown cause before that ordinary cancellation can
// win, without changing an earlier explicit user stop.
func TestServerClosePreservesTurnCancellationBeforeRegistration(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(map[bool]string{false: "shutdown", true: "already-stopped"}[stopped], func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			registration := srv.managedSessionRegistration
			if registration == nil {
				if _, err := newManagedSessionBridge().Bind(srv); err != nil {
					t.Fatal(err)
				}
				registration = srv.managedSessionRegistration
			}
			borrowed, cancelBorrowed := context.WithCancel(context.Background())
			defer cancelBorrowed()
			propagated := make(chan struct{})
			stop := context.AfterFunc(registration.ctx, func() {
				cancelBorrowed()
				close(propagated)
			})
			defer stop()
			closeRegistration := registration.cancel
			registration.cancel = func() {
				closeRegistration()
				<-propagated // deterministically finish the real pass cancellation
			}
			turnCtx, cancelTurn := context.WithCancelCause(borrowed)
			defer cancelTurn(context.Canceled)
			th := newThreadState("close-cause", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
			th.state = testRuntimeState(t, rt)
			th.running = true
			th.cancel = func() { cancelTurn(context.Canceled) }
			th.shutdownCancel = func() { cancelTurn(pluginhost.ErrExecutionShutdown) }
			srv.mu.Lock()
			srv.threads[th.ID] = th
			srv.mu.Unlock()
			if stopped {
				th.cancel()
			}
			srv.Close()
			want := pluginhost.ErrExecutionShutdown
			if stopped {
				want = context.Canceled
			}
			if got := context.Cause(turnCtx); !errors.Is(got, want) {
				t.Fatalf("turn cause = %v, want %v", got, want)
			}
		})
	}
}

func TestServerCloseReleasesConnectionOwnedResources(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ActivityRegistry = activity.NewRegistry()
	out := &lockedBuffer{}
	srv := New(rt, out)

	cancelled := make(chan struct{})
	th := newThreadState("thread-close", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	th.state = testRuntimeState(t, rt)
	th.running = true
	th.cancel = func() { close(cancelled) }
	sub := &threadRuntimeSubscription{done: make(chan struct{})}
	th.execRuntime = &runtime.ThreadRuntime{}
	th.runtimeSubscription = sub
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()

	srv.Close()
	srv.Close()

	select {
	case <-cancelled:
	default:
		t.Fatal("Server.Close did not cancel the active turn")
	}
	select {
	case <-sub.done:
	default:
		t.Fatal("Server.Close left the thread runtime subscription running")
	}
	th.mu.Lock()
	if th.execRuntime != nil || th.runtimeSubscription != nil {
		t.Fatalf("Server.Close retained runtime ownership: runtime=%p subscription=%p", th.execRuntime, th.runtimeSubscription)
	}
	th.mu.Unlock()
	if srv.thread(th.ID) != nil {
		t.Fatal("Server.Close retained the thread registry")
	}

	if _, _, err := rt.ActivityRegistry.Start(activity.StartOptions{
		ThreadID: th.ID,
		Workdir:  rt.RootDir,
		Kind:     activity.KindBrowser,
	}); err != nil {
		t.Fatalf("start activity after close: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("closed server still received activity notifications: %s", got)
	}
}

func TestRunStdioClosesServerResourcesOnEOF(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.ActivityRegistry = activity.NewRegistry()
	out := &lockedBuffer{}
	if err := RunStdio(context.Background(), rt, strings.NewReader(""), out); err != nil {
		t.Fatalf("RunStdio: %v", err)
	}

	if _, _, err := rt.ActivityRegistry.Start(activity.StartOptions{
		ThreadID: "after-eof",
		Workdir:  rt.RootDir,
		Kind:     activity.KindBrowser,
	}); err != nil {
		t.Fatalf("start activity after EOF: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("RunStdio retained its activity subscription after EOF: %s", got)
	}
}

// Both transport entrypoints apply connection options before subscriptions or
// output, and must join option-owned work before returning a registration error.
func TestRunStdioOptionFailureClosesConnectionWork(t *testing.T) {
	for _, device := range []bool{false, true} {
		name := "local"
		if device {
			name = "device"
		}
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			done := make(chan struct{})
			rejected := errors.New("registration rejected")
			option := func(s *Server) error {
				if s.sessionOperationCtx == nil || s.presenceLease != nil {
					t.Fatal("connection options must receive a cancellation scope before presence subscriptions")
				}
				if !s.startBackground(func() { <-s.sessionOperationCtx.Done(); close(done) }) {
					t.Fatal("startup work was rejected")
				}
				return rejected
			}
			var err error
			if device {
				err = RunStdioForDevice(context.Background(), rt, strings.NewReader(""), &lockedBuffer{}, nil, option)
			} else {
				err = RunStdio(context.Background(), rt, strings.NewReader(""), &lockedBuffer{}, option)
			}
			if !errors.Is(err, rejected) {
				t.Fatalf("startup error=%v", err)
			}
			select {
			case <-done:
			default:
				t.Fatal("startup failure returned before connection cleanup")
			}
		})
	}
}
