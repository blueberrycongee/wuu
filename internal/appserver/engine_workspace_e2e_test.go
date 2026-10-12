package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func testCodexWorkspaceFidelity(t *testing.T, binary string) {
	repo := initSessionWorkspaceRepo(t)
	linked := filepath.Join(t.TempDir(), "linked worktree")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "workspace-fidelity", linked)
	outside := t.TempDir()
	project := filepath.Join(t.TempDir(), "project link")
	if err := os.Symlink(repo, project); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	for root, marker := range map[string]string{repo: "project", linked: "linked"} {
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(marker), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Fatal(err)
		}
	}
	requests := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	t.Setenv("WUU_TEST_CODEX_WORKSPACE_STATE", filepath.Join(t.TempDir(), "native-state.json"))
	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = project
	rt.SetEnginesForTest(agentengine.NewRegistry())
	rt.RebuildCodexEngine(true, binary)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() { srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.handleLine(ctx, []byte(`{"id":"thread","method":"thread/start","params":{"engine":"codex","permission_mode":"standard"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "thread")
	if response["error"] != nil {
		t.Fatalf("thread/start: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	turns := 0
	run := func(root, marker, policy, entrypoint string) {
		t.Helper()
		turns++
		filename := fmt.Sprintf("probe-%d", turns)
		paths := []string{filepath.Join(project, filename), filepath.Join(linked, filename), filepath.Join(outside, filename), filepath.Join(root, "escape", filename)}
		prompt, _ := json.Marshal(map[string]any{"paths": paths})
		out.Reset()
		if entrypoint == "queued" {
			entry := queuedTurn{
				msg: providers.ChatMessage{Role: "user", Content: string(prompt)},
				snapshot: turnRuntimeSnapshot{
					PermissionMode: config.PermissionModeReadOnly, PermissionExplicit: true,
				},
			}
			if ok, err := srv.startQueuedTurn(ctx, threadID, entry); err != nil || !ok {
				t.Fatalf("queued turn: %v, %v", ok, err)
			}
		} else if entrypoint == "completion" {
			msg := providers.ChatMessage{Role: "user", Content: string(prompt)}
			if ok, err := srv.startSyntheticTurn(ctx, threadID, msg, []agentCompletionTurn{{msg: msg}}); err != nil || !ok {
				t.Fatalf("completion turn: %v, %v", ok, err)
			}
		} else {
			params := TurnStartParams{ThreadID: threadID, Prompt: string(prompt)}
			if entrypoint == "explicit" {
				mode := config.PermissionModeStandard
				if policy == "readOnly" {
					mode = config.PermissionModeReadOnly
				}
				params.PermissionMode = &mode
			}
			raw, _ := json.Marshal(map[string]any{"id": "turn", "method": "turn/start", "params": params})
			if err := srv.handleLine(ctx, raw); err != nil {
				t.Fatal(err)
			}
			if response := responseByID(t, parseOutput(t, out.String()), "turn"); response["error"] != nil {
				t.Fatalf("turn/start: %+v", response)
			}
		}
		messages := waitForTurnCompletedForThread(t, out, threadID)
		completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
		var probe struct {
			CWD           string          `json:"cwd"`
			Turns         int             `json:"turns"`
			Marker        string          `json:"marker"`
			Writes        map[string]bool `json:"writes"`
			Policy        string          `json:"policy"`
			WritableRoots []string        `json:"writableRoots"`
		}
		if err := json.Unmarshal([]byte(completed.Content), &probe); err != nil {
			t.Fatalf("probe result %q: %v", completed.Content, err)
		}
		if probe.CWD != root || probe.Marker != marker || probe.Turns != turns || probe.Policy != policy {
			t.Errorf("turn %d workspace/history/permission = %+v; want root=%q marker=%q policy=%q", turns, probe, root, marker, policy)
		}
		if policy == "workspaceWrite" && (len(probe.WritableRoots) != 1 || probe.WritableRoots[0] != root) {
			t.Errorf("turn %d writable roots = %v, want only %q", turns, probe.WritableRoots, root)
		}
		if policy == "readOnly" && len(probe.WritableRoots) != 0 {
			t.Errorf("read-only turn has writable roots: %v", probe.WritableRoots)
		}
		for _, path := range paths {
			want := policy == "workspaceWrite" && filepath.Dir(path) == root
			_, err := os.Stat(path)
			written := err == nil
			if probe.Writes[path] != want || written != want {
				t.Errorf("turn %d write %q: result=%v exists=%v, want %v", turns, path, probe.Writes[path], written, want)
			}
		}
		stored, found, err := session.Find(rt.SessionDir, threadID)
		if err != nil || !found || stored.EngineRef != "workspace-native" {
			t.Fatalf("native identity not preserved: %+v, %v", stored, err)
		}
		t.Logf("turn %d: cwd=%q marker=%q policy=%q native-history=%d roots=%v", turns, probe.CWD, probe.Marker, probe.Policy, probe.Turns, probe.WritableRoots)
	}
	run(project, "project", "workspaceWrite", "direct")
	if err := srv.rebindThreadWorkspace(threadID, linked); err != nil {
		t.Fatal(err)
	}
	run(linked, "linked", "workspaceWrite", "direct")
	run(linked, "linked", "workspaceWrite", "direct")
	// The saved thread still says standard. The process's explicit boundary
	// must be taken from admission, not re-read from that broader pin.
	rt.PermissionModeExplicit = true
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	run(linked, "linked", "readOnly", "direct")
	rt.PermissionModeExplicit = false
	if err := srv.rebindThreadWorkspace(threadID, repo); err != nil {
		t.Fatal(err)
	}
	run(project, "project", "workspaceWrite", "direct")
	if err := srv.rebindThreadWorkspace(threadID, linked); err != nil {
		t.Fatal(err)
	}
	// Exercise the existing deterministic admission/background boundary.
	// A later metadata refresh must not change this already-admitted turn.
	srv.beforeQueuedTurnBackgroundForTest = func() {
		if err := srv.rebindThreadWorkspace(threadID, project); err != nil {
			t.Fatal(err)
		}
	}
	run(linked, "linked", "workspaceWrite", "queued")
	srv.beforeQueuedTurnBackgroundForTest = nil
	run(project, "project", "workspaceWrite", "direct")
	srv.Close()
	srv = New(rt, out)
	out.Reset()
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	run(project, "project", "workspaceWrite", "direct")
	// Another app-server can change the durable permission pin while this
	// server still has a cached, broader selection. Admission must refresh
	// before choosing the effective policy for both direct and queued turns.
	setDurablePermission := func(mode string) {
		t.Helper()
		stored, found, err := session.Find(rt.SessionDir, threadID)
		if err != nil || !found {
			t.Fatalf("find session for permission update: found=%v err=%v", found, err)
		}
		_, err = session.SetRuntimeSelection(rt.SessionDir, threadID, session.RuntimeSelection{
			Provider: stored.Provider, Model: stored.Model, Variant: stored.Variant,
			Effort: stored.Effort, Speed: stored.Speed, ModelOptions: stored.ModelOptions,
			PermissionMode: mode, ApproveForMe: stored.ApproveForMe,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setDurablePermission(config.PermissionModeReadOnly)
	run(project, "project", "readOnly", "direct")
	setDurablePermission(config.PermissionModeStandard)
	run(project, "project", "workspaceWrite", "direct")
	setDurablePermission(config.PermissionModeReadOnly)
	run(project, "project", "readOnly", "queued")
	setDurablePermission(config.PermissionModeStandard)
	run(project, "project", "workspaceWrite", "direct")
	// Automatic completions enter admission without a request permission
	// snapshot, but still must honor the process's explicit read-only mode.
	rt.PermissionModeExplicit = true
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	run(project, "project", "readOnly", "completion")
	stored, found, err := session.Find(rt.SessionDir, threadID)
	if err != nil || !found || stored.PermissionMode != config.PermissionModeStandard {
		t.Fatalf("process override changed the saved pin: %+v, %v", stored, err)
	}
	rt.PermissionModeExplicit = false
	// Changes after admission govern the next turn only.
	srv.beforeQueuedTurnBackgroundForTest = func() {
		setDurablePermission(config.PermissionModeReadOnly)
	}
	run(project, "project", "workspaceWrite", "queued")
	srv.beforeQueuedTurnBackgroundForTest = nil
	run(project, "project", "readOnly", "direct")
	// Explicit requests are checked against durable metadata too, rather
	// than being rejected against a stale in-memory permission pin.
	setDurablePermission(config.PermissionModeStandard)
	run(project, "project", "workspaceWrite", "explicit")
	setDurablePermission(config.PermissionModeReadOnly)
	run(project, "project", "readOnly", "explicit")
	out.Reset()
	raw, _ := json.Marshal(map[string]any{"id": "conflict", "method": "turn/start", "params": map[string]any{
		"thread_id": threadID, "prompt": "must not run", "permission_mode": config.PermissionModeStandard,
	}})
	if err := srv.handleLine(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "conflict")["error"] == nil {
		t.Fatal("explicit permission mismatch was accepted")
	}
	data, err := os.ReadFile(requests)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request struct{ Method string }
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		counts[request.Method]++
	}
	if counts["initialize"] != 2 || counts["thread/start"] != 1 || counts["thread/resume"] != turns-1 || counts["turn/start"] != turns {
		t.Errorf("native process/history lifecycle: %v", counts)
	}
	t.Logf("native process/history lifecycle: %v", counts)
}
