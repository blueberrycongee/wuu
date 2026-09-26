package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// An explicit process-scoped permission override (exec --permission-mode) must
// beat a thread's pinned mode: a user asking for read_only can never be
// silently escalated to a resumed session's broader pinned authority.
func TestResolveThreadTurnPermissionsExplicitOverrideBeatsThreadPin(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	srv := New(rt, &lockedBuffer{})
	th := newThreadState("pinned-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	th.PermissionMode = config.PermissionModeUnconfined

	got, err := srv.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		t.Fatalf("resolve without override: %v", err)
	}
	if got.Mode != config.PermissionModeUnconfined {
		t.Fatalf("thread pin should win without override, got %q", got.Mode)
	}

	rt.PermissionModeExplicit = true
	got, err = srv.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		t.Fatalf("resolve with override: %v", err)
	}
	if got.Mode != config.PermissionModeReadOnly {
		t.Fatalf("explicit override should beat thread pin, got %q", got.Mode)
	}

	matching := config.PermissionModeReadOnly
	if _, err := srv.resolveThreadTurnPermissions(th, &matching); err != nil {
		t.Fatalf("requested mode equal to override should pass: %v", err)
	}
	conflicting := config.PermissionModeUnconfined
	if _, err := srv.resolveThreadTurnPermissions(th, &conflicting); err == nil {
		t.Fatal("requested mode conflicting with override should be rejected")
	}
}

func writeSelectionTestConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// A cached idle runtime must never run a selection it was not built for:
// when the thread's selection changes behind the runtime's back (e.g. another
// app-server process repinned the session and admission refreshed th from
// disk), reuse must detach the stale runtime and rebuild.
func TestEnsureThreadRuntimeRebuildsWhenThreadSelectionChanged(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeSelectionTestConfig(t, rt.ConfigPath)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)

	first, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime: %v", err)
	}
	reused, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime reuse: %v", err)
	}
	if reused != first {
		t.Fatal("unchanged selection should reuse the cached runtime")
	}

	th.mu.Lock()
	th.Model = "repinned-model"
	th.mu.Unlock()
	rebuilt, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime after model repin: %v", err)
	}
	if rebuilt == first {
		t.Fatal("model repin behind the runtime's back should force a rebuild")
	}
	if rebuilt.StreamRunner.Model != "repinned-model" {
		t.Fatalf("rebuilt runtime model = %q, want repinned-model", rebuilt.StreamRunner.Model)
	}
}

// A session pinned to a provider that has since been removed from config must
// not fail forever: the first turn self-heals the pin to the workspace
// defaults, persists the healed selection, and broadcasts it so the composer
// stops showing the dead provider. Only the dead provider/model pair heals:
// the thread's own variant and permission pins must survive, or the heal
// would silently widen a read_only thread to the workspace mode.
func TestEnsureThreadRuntimeHealsRemovedProviderPin(t *testing.T) {
	for _, tc := range []struct {
		name, speed, wantSpeed string
		supportsFast           bool
	}{
		{name: "inherited"},
		{name: "unsupported-fast", speed: "fast"},
		{name: "supported-fast", speed: "fast", wantSpeed: "fast", supportsFast: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			writeSelectionTestConfig(t, rt.ConfigPath)
			if tc.supportsFast {
				cfg, _, err := rt.LoadEffectiveConfig()
				if err != nil {
					t.Fatal(err)
				}
				provider := cfg.Providers["fake-provider"]
				provider.Models = map[string]config.ProviderModelConfig{"fake-model": {FastMode: &tc.supportsFast}}
				cfg.Providers["fake-provider"] = provider
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)

			if _, err := session.CreateWithMetadata(rt.SessionDir, "healed-thread", rt.RootDir); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if _, err := session.SetRuntimeSelection(rt.SessionDir, "healed-thread", session.RuntimeSelection{
				Provider:       "removed-provider",
				Model:          "removed-model",
				Speed:          tc.speed,
				Variant:        "high",
				PermissionMode: config.PermissionModeReadOnly,
			}); err != nil {
				t.Fatalf("pin removed provider: %v", err)
			}
			if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/resume","params":{"session_id":"healed-thread"}}`)); err != nil {
				t.Fatalf("thread/resume: %v", err)
			}
			th := srv.thread("healed-thread")
			if th == nil {
				t.Fatal("resumed thread is not cached")
			}

			built, err := srv.ensureThreadRuntime(th)
			if err != nil {
				t.Fatalf("ensureThreadRuntime should heal the dead pin: %v", err)
			}
			if built.Selection.Speed != tc.wantSpeed {
				t.Fatalf("runtime speed = %q, want %q", built.Selection.Speed, tc.wantSpeed)
			}
			if tc.wantSpeed == "fast" && built.StreamRunner.ProviderOptions["serviceTier"] != "priority" {
				t.Fatalf("supported speed was dropped: %v", built.StreamRunner.ProviderOptions)
			}
			if built.StreamRunner.Model != "fake-model" {
				t.Fatalf("healed runtime model = %q, want workspace default fake-model", built.StreamRunner.Model)
			}
			th.mu.Lock()
			provider, model := th.ModelProvider, th.Model
			variant, permission := th.ModelVariant, th.PermissionMode
			th.mu.Unlock()
			if provider != "fake-provider" || model != "fake-model" {
				t.Fatalf("thread selection not healed: provider=%q model=%q", provider, model)
			}
			if variant != "high" || permission != config.PermissionModeReadOnly {
				t.Fatalf("heal must keep the thread's own pins: variant=%q permission=%q", variant, permission)
			}
			sess, ok, err := session.Find(rt.SessionDir, "healed-thread")
			if err != nil || !ok {
				t.Fatalf("find session: ok=%v err=%v", ok, err)
			}
			if sess.Speed != tc.wantSpeed {
				t.Fatalf("persisted speed = %q, want %q", sess.Speed, tc.wantSpeed)
			}
			if sess.Provider != "fake-provider" || sess.Model != "fake-model" {
				t.Fatalf("session row not healed: provider=%q model=%q", sess.Provider, sess.Model)
			}
			if sess.Variant != "high" || sess.PermissionMode != config.PermissionModeReadOnly {
				t.Fatalf("session row lost its own pins: variant=%q permission=%q", sess.Variant, sess.PermissionMode)
			}
			var healed *Thread
			for _, msg := range parseOutput(t, out.String()) {
				if msg["method"] != "thread/updated" {
					continue
				}
				notif := remarshal[ThreadUpdatedNotification](t, msg["params"])
				if notif.Thread.ID == "healed-thread" {
					healed = &notif.Thread
				}
			}
			if healed == nil || healed.ModelProvider != "fake-provider" || healed.Model != "fake-model" || healed.Speed != tc.wantSpeed {
				t.Fatalf("no thread/updated broadcasting the healed selection, got %+v", healed)
			}
		})
	}
}

// An exec --permission-mode override is process-scoped and documented as
// never persisted; the dead-provider heal must not leak it into the session
// row or the thread's pinned mode.
func TestEnsureThreadRuntimeHealKeepsRowPermissionUnderExecOverride(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeUnconfined}
	rt.PermissionModeExplicit = true
	writeSelectionTestConfig(t, rt.ConfigPath)
	srv := New(rt, &lockedBuffer{})

	if _, err := session.CreateWithMetadata(rt.SessionDir, "override-heal", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, "override-heal", session.RuntimeSelection{
		Provider:       "removed-provider",
		Model:          "removed-model",
		PermissionMode: config.PermissionModeStandard,
	}); err != nil {
		t.Fatalf("pin removed provider: %v", err)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/resume","params":{"session_id":"override-heal"}}`)); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	th := srv.thread("override-heal")
	if th == nil {
		t.Fatal("resumed thread is not cached")
	}

	if _, err := srv.ensureThreadRuntime(th); err != nil {
		t.Fatalf("ensureThreadRuntime should heal the dead pin: %v", err)
	}
	th.mu.Lock()
	permission := th.PermissionMode
	th.mu.Unlock()
	if permission != config.PermissionModeStandard {
		t.Fatalf("heal replaced the thread's pinned mode with the override: %q", permission)
	}
	sess, ok, err := session.Find(rt.SessionDir, "override-heal")
	if err != nil || !ok {
		t.Fatalf("find session: ok=%v err=%v", ok, err)
	}
	if sess.PermissionMode != config.PermissionModeStandard {
		t.Fatalf("heal persisted the process-scoped override into the session row: %q", sess.PermissionMode)
	}
}

// A stale idle runtime that cannot be rebuilt because background agents still
// depend on it must fail admission instead of silently reusing the model it
// was built for: a cross-process repin would otherwise run the next turn on
// the old model with no signal to the user. Once the work drains, admission
// detaches the stale runtime and rebuilds on the current selection.
func TestEnsureThreadRuntimeSelectionMismatchWithOutstandingAgentWorkFailsAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeSelectionTestConfig(t, rt.ConfigPath)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	workerClient := newBlockingStreamClient("worker finished")
	control, err := agentcontrol.New(agentcontrol.Config{
		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    "mismatch-outstanding",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	t.Cleanup(func() {
		control.StopAll()
		control.Close()
	})

	th := newThreadState("mismatch-outstanding", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	staleRuntime := &runtime.ThreadRuntime{
		AgentControl: control,
		// Stamped as built for a selection the thread no longer pins.
		Selection: runtime.ThreadModelSelection{Provider: rt.ProviderName, Model: "stale-model"},
	}
	th.execRuntime = staleRuntime
	th.runtimeSubscription = srv.subscribeThreadRuntime(th.ID, staleRuntime)
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()

	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "outlive_repin",
		Description: "keep the stale runtime pinned by live work",
		Prompt:      "wait for release",
		Isolation:   string(agentcontrol.IsolationInplace),
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background agent did not start")
	}

	if _, err := srv.ensureThreadRuntime(th); err == nil ||
		!strings.Contains(err.Error(), "background agents are running") {
		t.Fatalf("mismatch with outstanding agent work should fail admission, got err=%v", err)
	}
	th.mu.Lock()
	stillInstalled := th.execRuntime == staleRuntime
	th.mu.Unlock()
	if !stillInstalled {
		t.Fatal("failed admission must not detach the runtime the live work depends on")
	}

	close(workerClient.release)
	waitForAgentStatus(t, control, spawned.AgentID, subagent.StatusCompleted)
	waitForWorkerFinalization(t, control)

	rebuilt, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime after work drained: %v", err)
	}
	if rebuilt == staleRuntime {
		t.Fatal("drained runtime should be detached and rebuilt for the current selection")
	}
	if rebuilt.StreamRunner == nil || rebuilt.StreamRunner.Model != "fake-model" {
		t.Fatalf("rebuilt runtime should run the thread's current selection, got %+v", rebuilt.StreamRunner)
	}
}

// Permission mode is re-resolved and applied to the toolkit on every turn, so
// pin drift on the permission dimension alone must not churn the cached
// runtime (a rebuild would break the thread's prompt-cache prefix for
// nothing).
func TestEnsureThreadRuntimePermissionPinDriftDoesNotRebuild(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)

	first, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime: %v", err)
	}
	th.mu.Lock()
	th.PermissionMode = config.PermissionModeUnconfined
	th.mu.Unlock()
	reused, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatalf("ensureThreadRuntime after pin drift: %v", err)
	}
	if reused != first {
		t.Fatal("permission pin drift alone should not rebuild the cached runtime")
	}
}

func TestThreadSpeedSelectionPersistsAndRebuilds(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	cfg := `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test","model":"fake-model","models":{"fake-model":{"fast_mode":true,"variants":{"high":{"reasoningEffort":"high"}}}}}}}`
	if err := os.WriteFile(rt.ConfigPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		if err = srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s: %v", method, response["error"])
		}
		return response
	}
	start := call("speed-start", "thread/start", map[string]any{"speed": "fast", "effort": "high"})
	th := remarshal[ThreadStartResult](t, start["result"]).Thread
	if th.Speed != "fast" {
		t.Fatalf("thread speed = %q", th.Speed)
	}
	first, err := srv.ensureThreadRuntime(srv.thread(th.ID))
	if err != nil {
		t.Fatal(err)
	}
	if first.StreamRunner.ProviderOptions["serviceTier"] != "priority" {
		t.Fatalf("fast options = %v", first.StreamRunner.ProviderOptions)
	}
	call("speed-off", "config/model/update", map[string]any{"thread_id": th.ID, "speed": "standard"})
	next, err := srv.ensureThreadRuntime(srv.thread(th.ID))
	if err != nil {
		t.Fatal(err)
	}
	if next == first || next.StreamRunner.ProviderOptions["serviceTier"] != "default" {
		t.Fatalf("stale speed runtime: %v", next.StreamRunner.ProviderOptions)
	}
	saved, ok, err := session.Find(rt.SessionDir, th.ID)
	if err != nil || !ok || saved.Speed != "standard" || saved.Variant != "high" {
		t.Fatalf("saved selection = %+v, %v", saved, err)
	}
	resumed := remarshal[ThreadResumeResult](t, call("speed-resume", "thread/resume", map[string]any{"session_id": th.ID})["result"]).Thread
	if resumed.Speed != "standard" {
		t.Fatalf("resumed speed = %q", resumed.Speed)
	}
}
