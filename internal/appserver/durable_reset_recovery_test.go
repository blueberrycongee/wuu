package appserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// The two barriers distinguish a lost context-write acknowledgement from a
// lost turn-completion acknowledgement. Neither may reset or complete the next
// user's request. SIGKILL is injected only into independently built fixtures.
func TestDurableCloudResetCrashRecovery(t *testing.T) {
	for _, point := range []string{"complete", "context"} {
		t.Run(point, func(t *testing.T) {
			fixture := os.Getenv("WUU_DURABLE_RESET_" + strings.ToUpper(point) + "_FIXTURE")
			if fixture == "" {
				t.Skip("requires independent reset crash fixture")
			}
			installed, marker := t.TempDir(), filepath.Join(t.TempDir(), "reset-crash.marker")
			if err := os.MkdirAll(filepath.Join(installed, "dist"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"plugin.json", "package.json", "dist/runtime.js"} {
				raw, err := os.ReadFile(filepath.Join(fixture, "package", name))
				if err != nil {
					t.Fatal(err)
				}
				if name == "dist/runtime.js" {
					placeholder := []byte(`"__WUU_TEST_RESET_CRASH_MARKER__"`)
					if bytes.Count(raw, placeholder) != 1 {
						t.Fatal("crash fixture must contain exactly one marker")
					}
					encoded, _ := json.Marshal(marker)
					raw = bytes.ReplaceAll(raw, placeholder, encoded)
				}
				if err = os.WriteFile(filepath.Join(installed, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("WUU_DURABLE_FIXTURE", installed)
			model := &durableContextModel{}
			j := newDurableControlJourney(t, "reset-crash-"+point, model)
			j.start(t, "", nil, "Original instruction must remain auditable")
			j.completed(t, 1)
			model.mu.Lock()
			model.next = &providers.ToolCall{ID: "reset-call", Name: j.names["durable_context"], Arguments: `{"action":"reset","note":"Use this reset note only for future input."}`}
			model.mu.Unlock()
			j.start(t, "", nil, "Reset now")
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(marker); err == nil && strings.Contains(j.out.String(), `"method":"turn/error"`) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(marker); err != nil || !strings.Contains(j.out.String(), `"method":"turn/error"`) {
				t.Fatalf("host did not observe actual reset crash: %v; %s", err, j.out.String())
			}
			before := j.out.String()
			j.server.Close()
			if err := j.closeRuntime(); err != nil {
				t.Logf("Killed-provider cleanup result: %v", err)
			}
			reopened, err := runtime.NewSession(j.options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Cleanup() })
			j.rt = reopened
			j.out = &lockedBuffer{}
			j.server = New(reopened, j.out)
			t.Cleanup(j.server.Close)
			const next = "Please answer this new request after reset"
			j.start(t, "", nil, next)
			j.completed(t, 1)
			model.mu.Lock()
			requests := append([]providers.ChatRequest(nil), model.requests...)
			model.mu.Unlock()
			if len(requests) != 3 {
				t.Fatalf("new request swallowed or reset note sampled after crash: requests=%d want3, events=%s", len(requests), j.out.String())
			}
			seen, note := 0, 0
			for _, message := range requests[2].Messages {
				if message.Content == next {
					seen++
				}
				if message.Content == "Use this reset note only for future input." {
					note++
				}
				if message.Content == "Original instruction must remain auditable" {
					t.Fatal("reset restored stale context")
				}
			}
			if seen != 1 || note != 1 {
				t.Fatalf("new context input=%d note=%d", seen, note)
			}
			cp, found, err := j.rt.Sessions.LoadDriverCheckpoint(j.id)
			if err != nil || !found {
				t.Fatalf("new run checkpoint: %v", err)
			}
			var current struct {
				RunID string `json:"run_id"`
			}
			if err = json.Unmarshal(cp.State, &current); err != nil {
				t.Fatal(err)
			}
			tasks := durableRetryTerminalTasks(t, j, current.RunID, "completed")
			resetID := ""
			for _, value := range tasks {
				task := value.(map[string]any)
				if outcome, ok := task["outcome"].(map[string]any); ok && outcome["reason"] == "reset" {
					resetID, _ = task["id"].(string)
					if task["status"] != "completed" {
						t.Fatalf("old reset not finalized: %v", task)
					}
				}
			}
			if resetID == "" || resetID == current.RunID {
				t.Fatalf("reset reused new run: old=%s new=%s", resetID, current.RunID)
			}
			history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			oldCount, newCount := 0, 0
			for _, record := range history {
				if record.Content == "Original instruction must remain auditable" {
					oldCount++
				}
				if record.Content == next {
					newCount++
				}
			}
			if oldCount != 1 || newCount != 1 {
				t.Fatalf("history changed: old=%d new=%d", oldCount, newCount)
			}
			if evidence := os.Getenv("WUU_DURABLE_EVIDENCE"); evidence != "" {
				if err = os.MkdirAll(evidence, 0700); err != nil {
					t.Fatal(err)
				}
				raw, err := json.MarshalIndent(map[string]any{"scenario": "cloud-reset-crash-" + point, "killed": true, "host_reopened": true, "model_requests": len(requests), "old_run": resetID, "new_run": current.RunID, "tasks": tasks, "events_before": before, "events_after": j.out.String()}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(evidence, "cloud-reset-crash-"+point+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
