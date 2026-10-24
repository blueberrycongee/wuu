package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

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

// Both transport entrypoints must release connection-owned work when a late
// composition registration fails, before returning that startup error.
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
				if s.presenceLease == nil {
					t.Fatal("registration ran before presence initialization")
				}
				if !s.startBackground(func() { <-s.nativeHistoryCtx.Done(); close(done) }) {
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
