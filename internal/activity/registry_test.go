package activity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistryControlLeaseLifecycle(t *testing.T) {
	registry := NewRegistry()
	session, lease, err := registry.Start(StartOptions{
		ID:       "activity-1",
		Kind:     KindBrowser,
		ThreadID: "thread-1",
		Workdir:  "/repo",
		PluginID: "browser-use",
		Target:   "https://example.test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if session.State != StateStarting || session.Controller != ControllerAgent || lease.Token == "" {
		t.Fatalf("start = %+v lease=%+v", session, lease)
	}
	if err := registry.CheckControl("thread-1", session.ID, lease.Token); err != nil {
		t.Fatalf("CheckControl: %v", err)
	}

	session, err = registry.Update("thread-1", session.ID, UpdateOptions{State: StateActive, Preview: "preview://activity-1"})
	if err != nil || session.State != StateActive || session.Preview != "preview://activity-1" {
		t.Fatalf("Update = %+v, %v", session, err)
	}

	session, err = registry.Takeover("thread-1", session.ID)
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if session.Controller != ControllerUser || session.State != StateUserControlled {
		t.Fatalf("takeover session = %+v", session)
	}
	if err := registry.CheckControl("thread-1", session.ID, lease.Token); !errors.Is(err, ErrControlRevoked) {
		t.Fatalf("stale lease after takeover = %v, want ErrControlRevoked", err)
	}

	session, nextLease, err := registry.Release("thread-1", session.ID)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if session.Controller != ControllerAgent || session.State != StateBackgroundControlled || nextLease.Token == "" || nextLease.Token == lease.Token {
		t.Fatalf("release = %+v lease=%+v", session, nextLease)
	}
	if err := registry.CheckControl("thread-1", session.ID, nextLease.Token); err != nil {
		t.Fatalf("new lease: %v", err)
	}
}

func TestRegistrySuccessfulUpdateClearsPreviousError(t *testing.T) {
	registry := NewRegistry()
	session, _, err := registry.Start(StartOptions{
		ID: "activity-1", Kind: KindCUA, ThreadID: "thread-1", Workdir: "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err = registry.Update("thread-1", session.ID, UpdateOptions{
		State: StateError, Error: "previous tool failure",
	})
	if err != nil || session.Error == "" {
		t.Fatalf("error update = %+v, %v", session, err)
	}
	session, err = registry.Update("thread-1", session.ID, UpdateOptions{
		State: StateBackgroundControlled, ClearError: true,
	})
	if err != nil || session.Error != "" || session.State != StateBackgroundControlled {
		t.Fatalf("successful update = %+v, %v", session, err)
	}
}

func TestRegistryStopIsIdempotentAndRevokesControl(t *testing.T) {
	registry := NewRegistry()
	session, lease, err := registry.Start(StartOptions{ID: "activity-1", Kind: KindCUA, ThreadID: "thread-1", Workdir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Stop("thread-1", session.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	second, err := registry.Stop("thread-1", session.ID)
	if err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if first.State != StateStopped || first.Controller != ControllerNone || second != first {
		t.Fatalf("stops = %+v then %+v", first, second)
	}
	if err := registry.CheckControl("thread-1", session.ID, lease.Token); !errors.Is(err, ErrControlRevoked) {
		t.Fatalf("lease after stop = %v", err)
	}
}

func TestRegistryClearsWindowIdentityWhenCUATargetChanges(t *testing.T) {
	registry := NewRegistry()
	session, _, err := registry.Start(StartOptions{
		ID: "activity-1", Kind: KindCUA, ThreadID: "thread-1", Workdir: "/repo",
		PluginID: "cua-mac", Target: "app-a", ProcessID: 41, WindowID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err = registry.Update("thread-1", session.ID, UpdateOptions{
		Target: "app-b", ClearWindowIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Target != "app-b" || session.ProcessID != 0 || session.WindowID != 0 {
		t.Fatalf("target change retained stale identity: %+v", session)
	}
}

func TestRegistryRejectsCrossThreadOperationsAndFiltersList(t *testing.T) {
	registry := NewRegistry()
	session, lease, err := registry.Start(StartOptions{ID: "activity-1", Kind: KindBrowser, ThreadID: "thread-1", Workdir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Start(StartOptions{ID: "activity-2", Kind: KindCUA, ThreadID: "thread-2", Workdir: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if got := registry.List("thread-1"); len(got) != 1 || got[0].ID != session.ID {
		t.Fatalf("List(thread-1) = %+v", got)
	}
	if _, err := registry.Takeover("thread-2", session.ID); !errors.Is(err, ErrThreadMismatch) {
		t.Fatalf("cross-thread takeover = %v", err)
	}
	if _, _, err := registry.Release("thread-2", session.ID); !errors.Is(err, ErrThreadMismatch) {
		t.Fatalf("cross-thread release = %v", err)
	}
	if _, err := registry.Stop("thread-2", session.ID); !errors.Is(err, ErrThreadMismatch) {
		t.Fatalf("cross-thread stop = %v", err)
	}
	if err := registry.CheckControl("thread-2", session.ID, lease.Token); !errors.Is(err, ErrThreadMismatch) {
		t.Fatalf("cross-thread control check = %v", err)
	}
}

func TestRegistryValidatesKindsStatesAndDuplicateIDs(t *testing.T) {
	registry := NewRegistry()
	if _, _, err := registry.Start(StartOptions{Kind: "unknown", ThreadID: "thread-1", Workdir: "/repo"}); err == nil {
		t.Fatal("expected invalid kind error")
	}
	if _, _, err := registry.Start(StartOptions{ID: "activity-1", Kind: KindBrowser, ThreadID: "thread-1", Workdir: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Start(StartOptions{ID: "activity-1", Kind: KindBrowser, ThreadID: "thread-1", Workdir: "/repo"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Start = %v", err)
	}
	if _, err := registry.Update("thread-1", "activity-1", UpdateOptions{State: "unknown"}); err == nil {
		t.Fatal("expected invalid state error")
	}
}

func TestRegistryAcquireReusesPluginActivityAndHonorsUserControl(t *testing.T) {
	registry := NewRegistry()
	options := StartOptions{
		Kind:     KindCUA,
		ThreadID: "thread-1",
		Workdir:  "/repo",
		PluginID: "cua-mac",
		Target:   "com.apple.TextEdit",
	}

	first, firstLease, err := registry.Acquire(options)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, secondLease, err := registry.Acquire(options)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if second.ID != first.ID || secondLease.Token != firstLease.Token {
		t.Fatalf("Acquire should reuse activity and lease: first=%+v/%+v second=%+v/%+v", first, firstLease, second, secondLease)
	}

	if _, err := registry.Takeover("thread-1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Acquire(options); !errors.Is(err, ErrControlRevoked) {
		t.Fatalf("Acquire during user takeover = %v, want ErrControlRevoked", err)
	}

	_, releasedLease, err := registry.Release("thread-1", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, resumedLease, err := registry.Acquire(options)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if resumed.ID != first.ID || resumedLease.Token != releasedLease.Token || resumedLease.Token == firstLease.Token {
		t.Fatalf("Acquire after release = %+v/%+v, released=%+v", resumed, resumedLease, releasedLease)
	}

	if _, err := registry.Stop("thread-1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Acquire(options); !errors.Is(err, ErrStopped) {
		t.Fatalf("Acquire after stop = %v, want ErrStopped", err)
	}
}

func TestRegistryPreparedResumePreservesNewerUserControl(t *testing.T) {
	for _, scenario := range []string{"resume", "newer input", "stopped", "other thread", "other kind", "new pause"} {
		t.Run(scenario, func(t *testing.T) {
			registry := NewRegistry()
			options := StartOptions{ThreadID: "thread-1", Workdir: "/repo", PluginID: "browser", Kind: KindBrowser}
			if scenario == "other thread" {
				options.ThreadID = "thread-2"
			}
			if scenario == "other kind" {
				options.Kind = KindCUA
			}
			current, oldLease, err := registry.Acquire(options)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "new pause" {
				if _, err := registry.Takeover(options.ThreadID, current.ID); err != nil {
					t.Fatal(err)
				}
			}
			resume, err := registry.PrepareResume("thread-1", KindBrowser)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "newer input" || scenario == "new pause" {
				if _, err := registry.Takeover(options.ThreadID, current.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stopped" {
				if _, err := registry.Stop(options.ThreadID, current.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := registry.Acquire(options); err == nil {
				t.Fatal("preparing a resume must not restore control")
			}
			resume()
			_, nextLease, err := registry.Acquire(options)
			switch scenario {
			case "resume":
				if err != nil || nextLease.Token == oldLease.Token {
					t.Fatalf("resume lease = %+v, error = %v", nextLease, err)
				}
			case "stopped":
				if !errors.Is(err, ErrStopped) {
					t.Fatalf("Acquire = %v, want ErrStopped", err)
				}
			default:
				if !errors.Is(err, ErrControlRevoked) {
					t.Fatalf("Acquire = %v, want ErrControlRevoked", err)
				}
			}
			if err := registry.CheckControl(options.ThreadID, current.ID, oldLease.Token); !errors.Is(err, ErrControlRevoked) {
				t.Fatalf("old lease = %v, want ErrControlRevoked", err)
			}
		})
	}
}

func TestRegistryAllowsOnlyOneCUAControllerPerTarget(t *testing.T) {
	registry := NewRegistry()
	first, _, err := registry.Acquire(StartOptions{
		Kind: KindCUA, ThreadID: "thread-1", Workdir: "/repo", PluginID: "cua-mac", Target: "com.tencent.xinWeChat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Acquire(StartOptions{
		Kind: KindCUA, ThreadID: "thread-2", Workdir: "/repo", PluginID: "cua-mac", Target: " COM.TENCENT.XINWECHAT ",
	}); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("second CUA target acquire = %v, want ErrTargetBusy", err)
	}

	second, _, err := registry.Acquire(StartOptions{
		Kind: KindCUA, ThreadID: "thread-2", Workdir: "/repo", PluginID: "cua-mac", Target: "com.apple.Calculator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update("thread-2", second.ID, UpdateOptions{Target: "com.tencent.xinWeChat"}); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("target switch to busy app = %v, want ErrTargetBusy", err)
	}
	if got := registry.List("thread-2")[0].Target; got != "com.apple.Calculator" {
		t.Fatalf("failed target switch changed target to %q", got)
	}

	if _, err := registry.Stop("thread-1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update("thread-2", second.ID, UpdateOptions{Target: "com.tencent.xinWeChat"}); err != nil {
		t.Fatalf("target should be claimable after prior controller stops: %v", err)
	}
}

func TestRegistryDoesNotApplyCUATargetExclusivityToBrowserActivities(t *testing.T) {
	registry := NewRegistry()
	for _, threadID := range []string{"thread-1", "thread-2"} {
		if _, _, err := registry.Acquire(StartOptions{
			Kind: KindBrowser, ThreadID: threadID, Workdir: "/repo", PluginID: "browser", Target: "https://example.test",
		}); err != nil {
			t.Fatalf("browser target in %s: %v", threadID, err)
		}
	}
}

func TestRegistryLeaseCancellationAndAtomicPublication(t *testing.T) {
	for _, operation := range []string{"takeover", "stop", "update_stop", "release"} {
		t.Run(operation, func(t *testing.T) {
			registry := NewRegistry()
			session, lease, err := registry.Start(StartOptions{Kind: KindCUA, ThreadID: "thread", Workdir: "/repo"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel, err := registry.BindControl(context.Background(), lease)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			switch operation {
			case "takeover":
				_, err = registry.Takeover("thread", session.ID)
			case "stop":
				_, err = registry.Stop("thread", session.ID)
			case "update_stop":
				_, err = registry.Update("thread", session.ID, UpdateOptions{State: StateStopped})
			case "release":
				_, _, err = registry.Release("thread", session.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("in-flight action was not cancelled")
			}
			if !errors.Is(context.Cause(ctx), ErrControlRevoked) {
				t.Fatalf("cause: %v", context.Cause(ctx))
			}
			if _, err := registry.UpdateWithLease(lease, UpdateOptions{State: StateBackgroundControlled}); !errors.Is(err, ErrControlRevoked) {
				t.Fatalf("stale publication: %v", err)
			}
			if _, _, err := registry.BindControl(context.Background(), lease); !errors.Is(err, ErrControlRevoked) {
				t.Fatalf("stale registration: %v", err)
			}
		})
	}
}

func TestRegistryCUATargetIsExclusiveAcrossPlugins(t *testing.T) {
	registry := NewRegistry()
	_, _, err := registry.Start(StartOptions{Kind: KindCUA, ThreadID: "one", PluginID: "first-driver", Workdir: "/repo", Target: "com.apple.TextEdit"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = registry.Start(StartOptions{Kind: KindCUA, ThreadID: "two", PluginID: "other-driver", Workdir: "/repo", Target: "com.apple.TextEdit"})
	if !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("shared physical target: %v", err)
	}
}
