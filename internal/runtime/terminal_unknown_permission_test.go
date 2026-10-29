package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestUnconfirmedWorkerFollowupKeepsCurrentPermissionBoundary(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir: root,
		HomeDir: home,
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	writeCall := func(id, path string) []providers.StreamEvent {
		args := `{"path":"` + path + `","content":"payload\n"}`
		return []providers.StreamEvent{
			{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: id, Name: "write_file"}},
			{Type: providers.EventToolUseDelta, Content: args},
			{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: id, Name: "write_file"}},
			{Type: providers.EventDone},
		}
	}
	turnDone := []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "done"},
		{Type: providers.EventDone},
	}
	client := &sessionRecordingClient{
		streamBatches: [][]providers.StreamEvent{
			turnDone,                              // spawn turn: complete without touching files
			writeCall("call-w1", "tightened.txt"), // wake 1 under read-only: must be denied
			turnDone,
			writeCall("call-w2", "loosened.txt"), // wake 2 under standard: must succeed
			turnDone,
		},
	}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-wake-permissions")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()

	// Spawn under the standard boundary and let the worker complete.
	spawned, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "wake_permissions",
		Prompt:      "idle worker",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Simulate a retained draft after the prior owner disappeared. Restore it
	// under the current thread permission before explicitly requesting followup.
	ConfigureToolkitPermissions(threadRT.Toolkit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
	dir := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, "thread-wake-permissions"), "harness", "terminal-finalizations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(spawned.AgentID))
	candidate := map[string]any{"schema_version": "wuu/worker-terminal-finalization/v1", "phase": "draft", "generation": "prior", "agent_id": spawned.AgentID, "status": "completed", "snapshot": map[string]any{"id": spawned.AgentID, "status": "completed", "parent_id": "thread-wake-permissions", "agent_path": spawned.AgentPath, "result": "unconfirmed candidate"}, "created_at": time.Now().UTC()}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%x.json", digest)), raw, 0600); err != nil {
		t.Fatal(err)
	}
	recovered := make(chan subagent.Notification, 1)
	un := threadRT.AgentControl.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error {
		select {
		case recovered <- n:
		default:
		}
		return nil
	})
	defer un()
	threadRT.AgentControl.StartWorkerTerminalRecovery()
	select {
	case n := <-recovered:
		if n.Status != subagent.StatusInterrupted || !n.TerminalUnconfirmed {
			t.Fatalf("recovered=%+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown terminal was not recovered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for threadRT.AgentControl.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := threadRT.AgentControl.FollowupTask(context.Background(), spawned.AgentID, "write tightened.txt"); err != nil {
		t.Fatalf("FollowupTask (read-only): %v", err)
	}
	snap, err := threadRT.AgentControl.Wait(context.Background(), spawned.AgentID)
	if err != nil {
		t.Fatalf("Wait (read-only wake): %v", err)
	}
	if snap.Status != subagent.StatusCompleted {
		t.Fatalf("read-only wake turn = %+v, want completed", snap)
	}
	req := client.LastRequest()
	var joined strings.Builder
	for _, msg := range req.Messages {
		joined.WriteString(msg.Content)
		joined.WriteByte('\n')
	}
	content := joined.String()
	if !strings.Contains(content, "authorization_denied") ||
		!strings.Contains(content, "this agent is read-only") {
		t.Fatalf("woken worker kept its spawn-time writable boundary:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "tightened.txt")); !os.IsNotExist(err) {
		t.Fatalf("woken read-only worker should not create tightened.txt, stat err=%v", err)
	}

	// Loosen back to standard — waking must propagate that direction too.
	ConfigureToolkitPermissions(threadRT.Toolkit, config.ResolvedPermissions{Mode: config.PermissionModeStandard})

	if _, err := threadRT.AgentControl.FollowupTask(context.Background(), spawned.AgentID, "write loosened.txt"); err != nil {
		t.Fatalf("FollowupTask (standard): %v", err)
	}
	snap, err = threadRT.AgentControl.Wait(context.Background(), spawned.AgentID)
	if err != nil {
		t.Fatalf("Wait (standard wake): %v", err)
	}
	if snap.Status != subagent.StatusCompleted {
		t.Fatalf("standard wake turn = %+v, want completed", snap)
	}
	data, err := os.ReadFile(filepath.Join(root, "loosened.txt"))
	if err != nil {
		t.Fatalf("woken worker kept the tightened boundary after loosening: %v", err)
	}
	if !strings.Contains(string(data), "payload") {
		t.Fatalf("loosened.txt content = %q, want it to contain %q", string(data), "payload")
	}
}
