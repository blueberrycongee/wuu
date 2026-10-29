package agentcontrol

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type generationResultClient struct{ calls atomic.Int32 }

func (*generationResultClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (c *generationResultClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	text := "first generation"
	if c.calls.Add(1) > 1 {
		text = "followup generation"
	}
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: text}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func TestFollowupKeepsEachResultAndSynthesizedReport(t *testing.T) {
	root := t.TempDir()
	client := &generationResultClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "generations", Prompt: "first", Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := c.Manager().Get(first.AgentID).Snapshot()
	firstPath := sessionRefPath(t, c, first.ResultPath)
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil || string(firstBytes) != "first generation" {
		t.Fatalf("first result=%q err=%v", firstBytes, err)
	}
	firstReport, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
	if err != nil || !ok {
		t.Fatalf("first report=%+v ok=%v err=%v", firstReport, ok, err)
	}
	firstReportBytes, err := os.ReadFile(firstReport.ReportPath)
	if err != nil || !strings.Contains(string(firstReportBytes), "first generation") {
		t.Fatalf("first report=%q err=%v", firstReportBytes, err)
	}
	if _, err := c.FollowupTask(ctx, first.AgentID, "next"); err != nil {
		t.Fatal(err)
	}
	next, err := c.Wait(ctx, first.AgentID)
	if err != nil || next.Result != "followup generation" {
		t.Fatalf("followup=%+v err=%v", next, err)
	}
	message := c.AgentMailboxMessage(next)
	if message.ResultPath == "" || message.ResultPath == first.ResultPath {
		t.Fatalf("new result path not unique: first=%q followup=%+v", first.ResultPath, message)
	}
	nextBytes, err := os.ReadFile(sessionRefPath(t, c, message.ResultPath))
	if err != nil || string(nextBytes) != "followup generation" {
		t.Fatalf("followup result=%q err=%v", nextBytes, err)
	}
	again, err := os.ReadFile(firstPath)
	if err != nil || string(again) != "first generation" {
		t.Fatalf("original result was overwritten: %q %v", again, err)
	}
	nextReport, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
	if err != nil || !ok || nextReport.ReportPath == firstReport.ReportPath {
		t.Fatalf("new report=%+v ok=%v err=%v", nextReport, ok, err)
	}
	if message.ReportPath != c.sessionArtifactRef(nextReport.ReportPath) {
		t.Fatalf("followup mailbox report path=%q, want %q", message.ReportPath, c.sessionArtifactRef(nextReport.ReportPath))
	}
	oldMessage := c.AgentMailboxMessage(firstSnapshot)
	if oldMessage.ReportPath != c.sessionArtifactRef(firstReport.ReportPath) {
		t.Fatalf("prior generation mailbox report path=%q, want %q", oldMessage.ReportPath, c.sessionArtifactRef(firstReport.ReportPath))
	}
	nextReportBytes, err := os.ReadFile(nextReport.ReportPath)
	if err != nil || !strings.Contains(string(nextReportBytes), "followup generation") {
		t.Fatalf("followup report=%q err=%v", nextReportBytes, err)
	}
	prior, err := os.ReadFile(firstReport.ReportPath)
	if err != nil || string(prior) != string(firstReportBytes) {
		t.Fatalf("original report was overwritten: %v", err)
	}
}

func TestResultArtifactsPreserveLegacyAndHandleConcurrentCalls(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	legacy := filepath.Join(root, "harness", "artifacts", "worker", "result.md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy result"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.HarnessStore().AddArtifact(harness.Artifact{ID: "worker-result", TaskID: "worker", Kind: harness.ArtifactResult, Path: legacy}); err != nil {
		t.Fatal(err)
	}
	snap := subagent.SubAgentSnapshot{ID: "worker", Status: subagent.StatusCompleted, Result: "new result"}
	const n = 16
	refs := make(chan AgentResultReference, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); refs <- c.AgentResultReference(snap) }()
	}
	wg.Wait()
	close(refs)
	path := ""
	for ref := range refs {
		if ref.Path == "" || (path != "" && ref.Path != path) {
			t.Fatalf("inconsistent concurrent reference: %+v", ref)
		}
		path = ref.Path
	}
	data, err := os.ReadFile(sessionRefPath(t, c, path))
	if err != nil || string(data) != "new result" {
		t.Fatalf("result=%q err=%v", data, err)
	}
	old, err := os.ReadFile(legacy)
	if err != nil || string(old) != "legacy result" {
		t.Fatalf("legacy changed=%q err=%v", old, err)
	}
	artifacts, err := c.HarnessStore().ListArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range artifacts {
		if a.Kind == harness.ArtifactResult {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("result artifact records=%d, want legacy and one new", count)
	}
}

func TestResultArtifactRejectsCorruptExistingContent(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	snap := subagent.SubAgentSnapshot{ID: "worker", Status: subagent.StatusCompleted, Result: "correct result"}
	ref := c.AgentResultReference(snap)
	path := sessionRefPath(t, c, ref.Path)
	if err := os.WriteFile(path, []byte("corrupt existing content"), 0600); err != nil {
		t.Fatal(err)
	}
	again := c.AgentResultReference(snap)
	if again.Path != "" {
		t.Fatalf("returned corrupt artifact: %+v", again)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "corrupt existing content" {
		t.Fatalf("overwrote existing reference: %q %v", raw, err)
	}
}

func TestFollowupPreservesExplicitStructuredReport(t *testing.T) {
	root := t.TempDir()
	client := &generationResultClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "structured", Prompt: "first", Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	prior, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
	if err != nil || !ok {
		t.Fatal("missing generated report")
	}
	reportPath := filepath.Join(root, "harness", "reports", "authored.md")
	explicit, err := c.HarnessStore().SubmitReport(harness.Report{ID: "user-explicit", TaskID: first.AgentID, Kind: harness.ReportKindStructured, Summary: "user-authored content", ReportPath: reportPath})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(explicit.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.FollowupTask(ctx, first.AgentID, "next"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(ctx, first.AgentID); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
	if err != nil || !ok || latest.ID != explicit.ID || latest.Kind != harness.ReportKindStructured {
		t.Fatalf("structured lost: %+v %v", latest, err)
	}
	after, err := os.ReadFile(explicit.ReportPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("structured report changed: %q %v", after, err)
	}
	old, err := os.ReadFile(prior.ReportPath)
	if err != nil || !strings.Contains(string(old), "first generation") {
		t.Fatalf("old generated reference lost: %q %v", old, err)
	}
}

func TestCorruptGeneratedReportIsNotReturnedToMailbox(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "valid result"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "corrupt_report", Prompt: "finish", Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	report, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
	if err != nil || !ok {
		t.Fatal("generated report missing")
	}
	if err := os.WriteFile(report.ReportPath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	msg := c.AgentMailboxMessage(c.Manager().Get(first.AgentID).Snapshot())
	if msg.ReportPath != "" {
		t.Fatalf("corrupt report returned: %+v", msg)
	}
	for _, path := range msg.Artifacts {
		if path == c.sessionArtifactRef(report.ReportPath) {
			t.Fatalf("corrupt report remains in artifact list: %+v", msg)
		}
	}
}

func TestCorruptGeneratedResultIsNotReturnedAsArtifact(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "valid result"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "corrupt_result", Prompt: "finish", Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionRefPath(t, c, first.ResultPath), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	msg := c.AgentMailboxMessage(c.Manager().Get(first.AgentID).Snapshot())
	if msg.ResultPath != "" {
		t.Fatalf("corrupt result returned: %+v", msg)
	}
	for _, path := range msg.Artifacts {
		if path == first.ResultPath {
			t.Fatalf("corrupt result remains in artifact list: %+v", msg)
		}
	}
}

func TestOversizedCorruptArtifactsDoNotCauseUnboundedAllocation(t *testing.T) {
	for _, target := range []string{"result", "report"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{resp: providers.ChatResponse{Content: "small result"}}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			first, err := c.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "large_corrupt", Prompt: "finish", Synchronous: true})
			if err != nil {
				t.Fatal(err)
			}
			report, ok, err := c.HarnessStore().ReportForTask(first.AgentID)
			if err != nil || !ok {
				t.Fatal("generated report missing")
			}
			path := sessionRefPath(t, c, first.ResultPath)
			if target == "report" {
				path = report.ReportPath
			}
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(64 << 20); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			msg := c.AgentMailboxMessage(c.Manager().Get(first.AgentID).Snapshot())
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 32<<20 {
				t.Fatalf("corrupt %s allocated %d bytes", target, allocated)
			}
			if target == "result" && msg.ResultPath != "" {
				t.Fatalf("returned corrupted result: %+v", msg)
			}
			if target == "report" && msg.ReportPath != "" {
				t.Fatalf("returned corrupted report: %+v", msg)
			}
		})
	}
}

// A terminal record may be replayed after the report index is lost while its
// derived file and task artifacts survive. Changed workspace metadata must
// produce a new handoff without adopting or overwriting the old reference.
func TestGeneratedReportRecoveryWithLostIndexAndChangedMetadata(t *testing.T) {
	for _, backend := range []string{"files", "documents"} {
		for _, scenario := range []struct {
			name            string
			changed, tamper bool
		}{{"unchanged", false, false}, {"changed", true, false}, {"tampered", false, true}} {
			t.Run(fmt.Sprintf("%s/%s", backend, scenario.name), func(t *testing.T) {
				root := t.TempDir()
				worktree := filepath.Join(root, "worktree")
				if err := os.MkdirAll(worktree, 0700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("git", "init", "-q", worktree)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git init: %s %v", output, err)
				}
				if err := os.WriteFile(filepath.Join(worktree, "before.txt"), []byte("before"), 0600); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(root, "harness")
				store := harness.NewStore(dir)
				var docs persistence.Documents
				if backend == "documents" {
					opened, err := persistence.Open(context.Background(), filepath.Join(root, "documents"), "sqlite")
					if err != nil {
						t.Fatal(err)
					}
					defer opened.Close()
					docs = opened
					store = harness.NewDocumentStore(docs, "session", dir)
				}
				c := &AgentControl{harnessDir: dir, harnessStore: store}
				task := harness.Task{ID: "worker", Workspace: harness.WorkspaceLease{Mode: harness.WorkspaceWorktree, Root: worktree}}
				if err := store.UpsertTask(task); err != nil {
					t.Fatal(err)
				}
				snap := subagent.SubAgentSnapshot{ID: "worker", Status: subagent.StatusCompleted, Result: "finished", CompletedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
				c.recordAgentResultArtifact(snap)
				c.synthesizeFinalTextReport(snap)
				first, ok, err := store.ReportForTask("worker")
				if err != nil || !ok {
					t.Fatalf("first report: %+v %v", first, err)
				}
				original, err := os.ReadFile(first.ReportPath)
				if err != nil {
					t.Fatal(err)
				}
				if backend == "files" {
					if err := os.Remove(filepath.Join(dir, "reports.json")); err != nil {
						t.Fatal(err)
					}
				} else {
					err := persistence.AccessDocumentImage(context.Background(), docs, harness.DocumentNamespace, "session", nil, func(image *persistence.DocumentImage) error { return image.Put("reports", []harness.Report{}) })
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario.tamper {
					// An unindexed file may have been altered by another writer.
					original = []byte("untrusted existing report")
					if err := os.WriteFile(first.ReportPath, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario.changed {
					if err := os.WriteFile(filepath.Join(worktree, "after.txt"), []byte("after"), 0600); err != nil {
						t.Fatal(err)
					}
					tasks, err := store.ListTasks()
					if err != nil || len(tasks) != 1 {
						t.Fatalf("tasks: %+v %v", tasks, err)
					}
					tasks[0].ArtifactPaths = append(tasks[0].ArtifactPaths, "new-artifact")
					if err := store.UpsertTask(tasks[0]); err != nil {
						t.Fatal(err)
					}
				}
				c.synthesizeFinalTextReport(snap)
				recovered, ok, err := store.ReportForTask("worker")
				if err != nil || !ok {
					t.Fatalf("recovered report: %+v %v", recovered, err)
				}
				if recovered.ReportPath == first.ReportPath {
					t.Fatal("recovery adopted the unindexed path")
				}
				after, err := os.ReadFile(first.ReportPath)
				if err != nil || string(after) != string(original) {
					t.Fatalf("old report changed: %v", err)
				}
				data, err := os.ReadFile(recovered.ReportPath)
				if err != nil || !harness.GeneratedReportReady(recovered) {
					t.Fatalf("recovered artifact unavailable: %v", err)
				}
				if strings.Contains(string(data), first.ReportPath) || strings.Contains(string(data), "untrusted existing report") {
					t.Fatal("recovered report adopted its unindexed predecessor")
				}
				if scenario.changed && (!strings.Contains(string(data), "new-artifact") || !strings.Contains(string(data), "after.txt")) {
					t.Fatalf("recovered metadata missing: %s", data)
				}
			})
		}
	}
}
