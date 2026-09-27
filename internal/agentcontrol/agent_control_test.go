package agentcontrol

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// fakeClient returns a canned response on every Chat / StreamChat call.
type fakeClient struct {
	resp providers.ChatResponse
}

type blockingStreamClient struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "done"}, nil
}

func (b *blockingStreamClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	go func() {
		if b.started != nil {
			close(b.started)
		}
		select {
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		case <-b.release:
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
			ch <- providers.StreamEvent{Type: providers.EventDone}
		}
		close(ch)
	}()
	return ch, nil
}

func visibleMessagesForTest(msgs []providers.ChatMessage) []providers.ChatMessage {
	out := make([]providers.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Hidden {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (f *fakeClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return f.resp, nil
}

// StreamChat replays the canned response as a single content delta
// followed by a terminal Done event so workers — which now run
// through agent.StreamRunner — can be exercised by these tests.
func (f *fakeClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	if f.resp.Content != "" {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: f.resp.Content}
	}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

type recordingClient struct {
	resp providers.ChatResponse
	mu   sync.Mutex
	last providers.ChatRequest
}

func (r *recordingClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	r.mu.Lock()
	r.last = req
	r.mu.Unlock()
	return r.resp, nil
}

func (r *recordingClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	r.mu.Lock()
	r.last = req
	r.mu.Unlock()
	ch := make(chan providers.StreamEvent, 2)
	if r.resp.Content != "" {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: r.resp.Content}
	}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func (r *recordingClient) LastRequest() providers.ChatRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// fakeToolkit is a no-op tool executor.
type fakeToolkit struct{}

func (fakeToolkit) Definitions() []providers.ToolDefinition { return nil }
func (fakeToolkit) Execute(_ context.Context, _ providers.ToolCall) (string, error) {
	return "", nil
}

type captureFailureSink struct {
	mu       sync.Mutex
	failures []AgentFailure
}

func (s *captureFailureSink) RecordAgentFailure(failure AgentFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	return nil
}

func (s *captureFailureSink) list() []AgentFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AgentFailure(nil), s.failures...)
}

type captureReportSink struct {
	mu      sync.Mutex
	reports []AgentReport
}

func (s *captureReportSink) RecordAgentReport(report AgentReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, report)
	return nil
}

func (s *captureReportSink) list() []AgentReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AgentReport(nil), s.reports...)
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
}

func TestNew_NonGitRepoSucceeds(t *testing.T) {
	dir := t.TempDir() // not a git repo
	c, err := New(Config{
		Client:        &fakeClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("New should succeed for non-git directory, got: %v", err)
	}
	// Worktree manager should be nil for non-git workspaces.
	if c.worktrees != nil {
		t.Fatal("worktrees should be nil for non-git directory")
	}
}

func TestPrepareWorkspaceRebindMovesSubsequentInplaceSpawnAndFork(t *testing.T) {
	parent := t.TempDir()
	initRepo(t, parent)
	linked := filepath.Join(t.TempDir(), "linked")
	cmd := exec.Command("git", "worktree", "add", "-q", "-b", "rebound-workspace", linked)
	cmd.Dir = parent
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	spawnRoots := make(chan string, 2)
	c, err := New(Config{
		Client:       &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel: "fake-model",
		ParentRepo:   parent,
		WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		SessionID:    "workspace-rebind",
		ThreadDir:    filepath.Join(t.TempDir(), "threads"),
		WorkerFactory: func(root string, _ WorkerType, _ agentthread.Metadata) (agent.ToolExecutor, error) {
			spawnRoots <- root
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	commit, err := c.PrepareWorkspaceRebind(linked)
	if err != nil {
		t.Fatal(err)
	}
	if c.ParentRepo() != parent {
		t.Fatalf("workspace changed before commit: %q", c.ParentRepo())
	}
	commit()
	if c.ParentRepo() != linked {
		t.Fatalf("parent repo = %q, want %q", c.ParentRepo(), linked)
	}
	_, manager := c.workspaceSnapshot()
	if manager == nil {
		t.Fatal("rebound workspace lost worktree support")
	}

	if _, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "after_rebind",
		Prompt:      "inspect the rebound workspace",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := <-spawnRoots; got != linked {
		t.Fatalf("inplace spawn root = %q, want %q", got, linked)
	}
	if _, err := c.Fork(context.Background(), ForkRequest{
		TaskName:    "fork_after_rebind",
		Prompt:      "continue in the rebound workspace",
		Synchronous: true,
	}, []providers.ChatMessage{{Role: "user", Content: "continue"}}); err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if got := <-spawnRoots; got != linked {
		t.Fatalf("inplace fork root = %q, want %q", got, linked)
	}
}

func TestSpawn_SyncHappyPath(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "task done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-1",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-1", "workers"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "sync_happy",
		Description: "test",
		Prompt:      "do something",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("expected completed, got %s", res.Status)
	}
	if res.Result != "task done" {
		t.Fatalf("got result %q", res.Result)
	}
	// Worker now defaults to inplace — additive writes land in the
	// parent repo so users don't have to fish them out of a worktree.
	if res.Isolation != "inplace" {
		t.Fatalf("worker default should be inplace isolation, got %q", res.Isolation)
	}
	if res.WorktreePath != "" {
		t.Fatalf("inplace spawn should not produce a worktree path, got %q", res.WorktreePath)
	}
}

func TestSpawn_RegistersThreadMetadata(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-threads", "threads")
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "task done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-threads",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-threads", "workers"),
		ThreadDir:     threadDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "scan_auth_flow",
		Description: "scan auth flow",
		Prompt:      "find auth problems",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.TaskName != "scan_auth_flow" || res.AgentPath != "/root/scan_auth_flow" {
		t.Fatalf("unexpected thread metadata in result: %+v", res)
	}
	snap := c.Manager().Get(res.AgentID).Snapshot()
	if snap.TaskName != res.TaskName || snap.AgentPath != res.AgentPath || snap.ParentID != "sess-threads" {
		t.Fatalf("snapshot missing thread metadata: %+v", snap)
	}

	var threads []agentthread.Metadata
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		threads, err = c.threadStore.ListThreads()
		if err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
		if len(threads) == 2 && threads[1].Status == agentthread.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(threads) != 2 {
		t.Fatalf("expected root + child threads, got %+v", threads)
	}
	child := threads[1]
	if child.ID != res.AgentID || child.Path != res.AgentPath || child.Source.Kind != agentthread.SourceThreadSpawn {
		t.Fatalf("unexpected child thread metadata: %+v", child)
	}
	if child.Status != agentthread.StatusCompleted {
		t.Fatalf("expected completed child status, got %s", child.Status)
	}
	events, err := c.threadStore.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected root, child, and status events, got %+v", events)
	}
}

func TestForkAgentProfileOverridesInheritedSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	client := &recordingClient{resp: providers.ChatResponse{Content: "task done"}}

	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-profile-fork",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		WorkerPrompt: func(_ string, _ WorkerType, meta agentthread.Metadata, _ IsolationMode) (string, error) {
			if meta.AgentProfile == "qa workflow" {
				return "profile base prompt with persistent memory", nil
			}
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Fork(context.Background(), ForkRequest{
		TaskName:     "qa_check",
		AgentProfile: "qa workflow",
		Prompt:       "run QA",
		Synchronous:  true,
	}, []providers.ChatMessage{
		{Role: "system", Content: "parent ordinary memoryless system prompt"},
		{Role: "user", Content: "please delegate"},
		{Role: "assistant", Content: "spawning"},
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if res.AgentProfile != "qa workflow" {
		t.Fatalf("AgentProfile = %q, want qa workflow", res.AgentProfile)
	}
	req := client.LastRequest()
	if len(req.Messages) == 0 {
		t.Fatal("client received no messages")
	}
	first := req.Messages[0]
	if first.Role != "system" {
		t.Fatalf("first message role = %q, want system", first.Role)
	}
	if !strings.Contains(first.Content, "profile base prompt with persistent memory") {
		t.Fatalf("fork did not install profile system prompt:\n%s", first.Content)
	}
	if strings.Contains(first.Content, "parent ordinary memoryless system prompt") {
		t.Fatalf("fork leaked inherited parent system prompt:\n%s", first.Content)
	}
}

func TestForkWithoutAgentProfileReplacesInheritedSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	client := &recordingClient{resp: providers.ChatResponse{Content: "task done"}}

	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-plain-fork",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		WorkerPrompt: func(_ string, _ WorkerType, _ agentthread.Metadata, _ IsolationMode) (string, error) {
			return "worker tool surface prompt: use edit_file and write_file", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Fork(context.Background(), ForkRequest{
		TaskName:    "plain_check",
		Prompt:      "check the issue",
		Synchronous: true,
	}, []providers.ChatMessage{
		{Role: "system", Content: "parent OpenAI/Codex prompt: use apply_patch for edits"},
		{Role: "user", Content: "please delegate"},
		{Role: "assistant", Content: "spawning"},
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	req := client.LastRequest()
	if len(req.Messages) == 0 {
		t.Fatal("client received no messages")
	}
	first := req.Messages[0]
	if first.Role != "system" {
		t.Fatalf("first message role = %q, want system", first.Role)
	}
	if !strings.Contains(first.Content, "worker tool surface prompt") {
		t.Fatalf("fork did not install worker system prompt:\n%s", first.Content)
	}
	if strings.Contains(first.Content, "apply_patch") || strings.Contains(first.Content, "parent OpenAI/Codex prompt") {
		t.Fatalf("fork leaked inherited parent tool surface prompt:\n%s", first.Content)
	}
}

func TestSpawn_RecordsHarnessCompletedWhenWorkerSkipsReport(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-harness", "harness")
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "task done\n\nEvidence: go test ./internal/harness"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-harness",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-harness", "workers"),
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-harness", "threads"),
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "record_harness",
		Description: "record harness",
		Prompt:      "record durable task",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	store := c.HarnessStore()
	if store == nil || store.Dir() != harnessDir {
		t.Fatalf("unexpected harness store: %#v", store)
	}
	var tasks []harness.Task
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tasks, err = store.ListTasks()
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		// Lifecycle status flips to completed before the synthesized
		// final_text report is written (status is a runtime fact; the
		// report is a subsequent settlement step), so wait for the report
		// path too rather than snapshotting the task mid-settlement.
		if len(tasks) == 1 && tasks[0].Status == harness.TaskStatusCompleted && tasks[0].ReportPath != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one harness task, got %+v", tasks)
	}
	task := tasks[0]
	if task.ID != res.AgentID || task.Path != res.AgentPath || task.Name != "record_harness" || task.Role != DefaultSubagentType {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.Workspace.Mode != harness.WorkspaceShared || task.Workspace.Root != dir {
		t.Fatalf("unexpected workspace lease: %+v", task.Workspace)
	}
	if task.LastRunID != res.AgentID+"-run-1" || task.InputTokens != 0 || task.OutputTokens != 0 {
		t.Fatalf("unexpected run linkage/usage: %+v", task)
	}
	if task.ReportPath == "" {
		t.Fatalf("worker completion without agent_report should still get a synthesized final_text report path: %+v", task)
	}
	if report, ok := c.harnessReportDetailsForTask(task.ID); !ok || report.Kind != harness.ReportKindFinalText {
		t.Fatalf("report for a report-skipping worker must be final_text, got %+v ok=%v", report, ok)
	}
	var runs []harness.AgentRun
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runs, err = store.ListRuns()
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) == 1 && runs[0].TaskID == res.AgentID && runs[0].Status == harness.TaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(runs) != 1 || runs[0].TaskID != res.AgentID || runs[0].Status != harness.TaskStatusCompleted {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	reports, err := store.ListReports()
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 1 || reports[0].Kind != harness.ReportKindFinalText {
		t.Fatalf("expected exactly one synthesized final_text report, got %+v", reports)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) < 6 {
		t.Fatalf("expected lifecycle events, got %+v", events)
	}
	// First-event guard: any warm-up or diagnostic event emitted before
	// task_created would regress the harness lifecycle contract and
	// would have been caught by the previous positional check.
	if events[0].Type != harness.EventTaskCreated {
		t.Fatalf("expected first event task_created, got %+v", events[0])
	}
	// The last emitted *lifecycle* event must transition the task to
	// completed: the loop ended without error, so the run is completed
	// unconditionally. artifact_recorded events can follow run_completed
	// because the harness keeps observing worker artifacts even after
	// the worker falls silent, which would otherwise push a non-status
	// event into the tail slot this test was checking. Walk back from
	// the tail to find the last non-artifact event and verify its status
	// is completed rather than asserting a positional `last`.
	lastIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != harness.EventArtifactRecorded {
			lastIdx = i
			break
		}
	}
	if lastIdx < 0 || events[lastIdx].Status != string(harness.TaskStatusCompleted) {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
}

func TestRecordAgentReportPersistsStructuredHandoff(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "reportable result"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-agent-report",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "structured_report",
		Prompt:      "inspect code",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	reportedSource := filepath.Join(dir, "reports", "notes.md")
	if err := os.MkdirAll(filepath.Dir(reportedSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportedSource, []byte("agent notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome:      "completed",
		Summary:      "Inspected the harness path and found the spawn lifecycle.",
		ChangedFiles: []string{"internal/agentcontrol/agent_control.go"},
		WorkDone:     []string{"Read agentcontrol spawn code."},
		Risks:        []string{"No code changes were made."},
		Verification: []string{"Not run; read-only inspection."},
		Evidence: []ReportEvidence{{
			Type: "file",
			Path: "internal/agentcontrol/agent_control.go",
			Line: 180,
			Note: "spawn entry point",
		}},
		Artifacts: []string{"reports/notes.md"},
	})
	if err != nil {
		t.Fatalf("RecordAgentReport: %v", err)
	}
	if report.TaskID != res.AgentID || report.ReportPath == "" || len(report.Artifacts) != 2 {
		t.Fatalf("unexpected report result: %+v", report)
	}
	if !strings.HasPrefix(report.ReportPath, "$SESSION_DIR/") {
		t.Fatalf("agent_report should return a session artifact ref, got %+v", report)
	}
	if strings.Contains(strings.Join(report.Artifacts, ","), "reports/notes.md") {
		t.Fatalf("agent_report should return imported artifact refs, got %+v", report.Artifacts)
	}
	if _, err := os.Stat(sessionRefPath(t, c, report.ReportPath)); err != nil {
		t.Fatalf("report file missing: %v", err)
	}
	reports, err := c.HarnessStore().ListReports()
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 1 || reports[0].Summary == "" || len(reports[0].Evidence) != 1 || len(reports[0].ChangedFiles) != 1 || len(reports[0].Verification) != 1 {
		t.Fatalf("unexpected persisted reports: %+v", reports)
	}
	// The lifecycle is owned by the runtime: the loop ended without error, so
	// the task settles at completed independent of agent_report.
	waitForHarnessTaskStatus(t, c.HarnessStore(), res.AgentID, harness.TaskStatusCompleted)
	artifacts, err := c.HarnessStore().ListArtifacts()
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("expected result, report, and explicit artifacts, got %+v", artifacts)
	}
	if !artifactKindPresent(artifacts, harness.ArtifactResult) {
		t.Fatalf("expected agent result artifact, got %+v", artifacts)
	}
	var importedPath string
	for _, artifact := range artifacts {
		if artifact.Kind == harness.ArtifactEvidence && strings.Contains(artifact.Path, string(filepath.Separator)+"reported"+string(filepath.Separator)) {
			importedPath = artifact.Path
			break
		}
	}
	if importedPath == "" {
		t.Fatalf("expected imported artifact under harness reported dir, got %+v", artifacts)
	}
	if importedPath == reportedSource {
		t.Fatalf("reported artifact should be copied into Wuu storage, got source path %q", importedPath)
	}
	if got := mustReadAgentControlFile(t, importedPath); got != "agent notes\n" {
		t.Fatalf("imported artifact content mismatch: %q", got)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestRecordAgentReportRejectsMissingArtifact(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-missing-artifact",
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-missing-artifact", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "missing_artifact",
		Prompt:      "finish",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome:   "completed",
		Summary:   "Tried to report a missing artifact.",
		Artifacts: []string{"reports/missing.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("expected missing artifact error, got %v", err)
	}
}

func TestRecordAgentReportSyncsReportSinkWithoutGoalBinding(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	reportSink := &captureReportSink{}

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "reportable result"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-agent-report-goal",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report-goal", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report-goal", "harness"),
		ReportSink:    reportSink,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "structured_report_goal",
		Prompt:      "inspect code",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("agentcontrol spawn should not bind harness task to legacy goal state: %+v", tasks)
	}
	artifactPath := filepath.Join(tasks[0].Workspace.Root, "reports", "worker.patch")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("create report artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("patch\n"), 0o644); err != nil {
		t.Fatalf("write report artifact: %v", err)
	}

	report, err := c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome:      "completed",
		Summary:      "Implemented the requested worker change.",
		ChangedFiles: []string{"internal/agentcontrol/report.go"},
		Verification: []string{"go test ./internal/agentcontrol"},
		Artifacts:    []string{"reports/worker.patch"},
		NextSteps:    []string{"review diff"},
	})
	if err != nil {
		t.Fatalf("RecordAgentReport: %v", err)
	}
	reports := reportSink.list()
	if len(reports) != 1 {
		t.Fatalf("expected one synced report, got %+v", reports)
	}
	got := reports[0]
	if got.ReportPath != sessionRefPath(t, c, report.ReportPath) {
		t.Fatalf("synced report missing report path: %+v", got)
	}
	if len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "internal/agentcontrol/report.go" || len(got.Verification) != 1 || len(got.Artifacts) != 1 {
		t.Fatalf("synced report missing handoff facts: %+v", got)
	}
	if got.Artifacts[0] == artifactPath {
		t.Fatalf("synced report should use imported artifact path, got source path %q", got.Artifacts[0])
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestRecordAgentReportSyncsFailureSinkForBlockers(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	sink := &captureFailureSink{}

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "reportable result"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-agent-report-failure",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report-failure", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-agent-report-failure", "harness"),
		FailureSink:   sink,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "structured_report_failure",
		Prompt:      "inspect code",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	report, err := c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome:      "stuck",
		Summary:      "Could not finish because tests fail.",
		Blockers:     []string{"go test ./internal/agentcontrol fails"},
		Verification: []string{"go test ./internal/agentcontrol"},
	})
	if err != nil {
		t.Fatalf("RecordAgentReport: %v", err)
	}
	failures := sink.list()
	if len(failures) != 1 {
		t.Fatalf("expected one synced failure, got %+v", failures)
	}
	got := failures[0]
	if got.Source != "harness_report" || got.TaskID != res.AgentID || got.ReportPath != sessionRefPath(t, c, report.ReportPath) {
		t.Fatalf("unexpected synced failure: %+v", got)
	}
	if got.Message != "go test ./internal/agentcontrol fails" || got.Outcome != "stuck" {
		t.Fatalf("failure did not preserve blocker/outcome: %+v", got)
	}
	// The self-reported "stuck" outcome is archived as the agent's claim (and
	// still syncs a failure record), but it does not adjudicate the lifecycle:
	// the run finished without a runtime error, so it settles at completed.
	waitForHarnessTaskStatus(t, c.HarnessStore(), res.AgentID, harness.TaskStatusCompleted)
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestRecordHarnessTaskFailureSyncsFailureSink(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	sink := &captureFailureSink{}

	c, err := New(Config{
		Client:        &fakeClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-harness-failure",
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-harness-failure", "harness"),
		FailureSink:   sink,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.recordHarnessTaskFailure("agent-failed", errors.New("spawn failed"))

	failures := sink.list()
	if len(failures) != 1 {
		t.Fatalf("expected one synced failure, got %+v", failures)
	}
	got := failures[0]
	if got.Source != "harness_task" || got.TaskID != "agent-failed" || got.Message != "spawn failed" {
		t.Fatalf("unexpected synced failure: %+v", got)
	}
}

func TestAwaitFromReportsMissingAndSubmittedReports(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "plain final text"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-report",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-report", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-report", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "await_report",
		Prompt:      "finish without report",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Action != "spawn_agent" {
		t.Fatalf("Spawn result action = %q, want spawn_agent", res.Action)
	}

	awaited, err := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom: %v", err)
	}
	if awaited.Action != "await_agents" {
		t.Fatalf("AwaitFrom result action = %q, want await_agents", awaited.Action)
	}
	if len(awaited.Results) != 1 || awaited.Results[0].Status != string(harness.TaskStatusCompleted) || !awaited.Results[0].ReportMissing {
		t.Fatalf("expected completed result with report missing, got %+v", awaited)
	}

	report, err := c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome:      "completed",
		Summary:      "Submitted the missing structured report.",
		ChangedFiles: []string{"internal/agentcontrol/await.go"},
		WorkDone:     []string{"Closed the handoff contract."},
	})
	if err != nil {
		t.Fatalf("RecordAgentReport: %v", err)
	}
	if report.Action != "agent_report" {
		t.Fatalf("AgentReport result action = %q, want agent_report", report.Action)
	}
	awaited, err = c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom after report: %v", err)
	}
	if len(awaited.Results) != 1 || awaited.Results[0].Status != string(harness.TaskStatusCompleted) || awaited.Results[0].ReportPath != report.ReportPath || len(awaited.Results[0].ChangedFiles) != 1 {
		t.Fatalf("expected completed result with report path, got %+v", awaited)
	}
	waitForHarnessEvent(t, c.HarnessStore(), harness.EventRunCompleted, res.AgentID)
}

func TestAwaitFromWarnsOnOverlappingChangedFiles(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-conflict",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-conflict", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-conflict", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	first, err := c.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "edit_one", Prompt: "one", Synchronous: true})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	second, err := c.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "edit_two", Prompt: "two", Synchronous: true})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}
	for _, res := range []*SpawnResult{first, second} {
		if _, err := c.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
			Outcome:      "completed",
			Summary:      "Edited shared file.",
			ChangedFiles: []string{"internal/shared.go"},
		}); err != nil {
			t.Fatalf("RecordAgentReport: %v", err)
		}
		waitForHarnessEvent(t, c.HarnessStore(), harness.EventRunCompleted, res.AgentID)
	}
	awaited, err := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{first.AgentID, second.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom: %v", err)
	}
	if len(awaited.Warnings) != 1 || !strings.Contains(awaited.Warnings[0], "internal/shared.go") {
		t.Fatalf("expected changed-file overlap warning, got %+v", awaited)
	}
}

func TestAwaitFromTimesOutWithRunningStatus(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-timeout",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-timeout", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-timeout", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "await_timeout",
		Prompt:   "keep running",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	awaited, err := c.AwaitFrom(agentthread.RootPath, ctx, []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom: %v", err)
	}
	if !awaited.TimedOut || len(awaited.Results) != 1 || awaited.Results[0].Status != string(subagent.StatusRunning) {
		t.Fatalf("expected timed out running result, got %+v", awaited)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestActiveTaskReminderListsIncompleteChildren(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-active-reminder",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-active-reminder", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-active-reminder", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "active_child",
		Prompt:   "stay active",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	reminder := c.ActiveTaskReminder(agentthread.RootPath)
	if !strings.Contains(reminder, res.AgentPath) || !strings.Contains(reminder, "<subagent_status>") {
		t.Fatalf("active reminder should name the child inside a subagent_status block, got %q", reminder)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestWorktreeCompletionRecordsPatchArtifact(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:       &fakeClient{resp: providers.ChatResponse{Content: "changed readme"}},
		DefaultModel: "fake-model",
		ParentRepo:   dir,
		WorktreeRoot: filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:    "sess-patch-artifact",
		ThreadDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-patch-artifact", "threads"),
		HarnessDir:   filepath.Join(dir, ".wuu-state", "sessions", "sess-patch-artifact", "harness"),
		WorkerFactory: func(root string, _ WorkerType, _ agentthread.Metadata) (agent.ToolExecutor, error) {
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed\n"), 0o644); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(root, "notes", "new_file.txt"), []byte("new\n"), 0o644); err != nil {
				return nil, err
			}
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.StopAll()
		waitForRunningWorkersToStop(t, c.Manager(), time.Second)
		c.Close()
	})
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "patch_artifact",
		Prompt:      "change readme",
		Isolation:   "worktree",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	var patchPath string
	var manifestPath string
	var archivePath string
	var artifacts []harness.Artifact
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		artifacts, err = c.HarnessStore().ListArtifacts()
		if err != nil {
			t.Fatalf("ListArtifacts: %v", err)
		}
		for _, artifact := range artifacts {
			if artifact.TaskID != res.AgentID {
				continue
			}
			switch artifact.Kind {
			case harness.ArtifactPatch:
				patchPath = artifact.Path
			case harness.ArtifactManifest:
				manifestPath = artifact.Path
			case harness.ArtifactArchive:
				archivePath = artifact.Path
			}
		}
		if patchPath != "" && manifestPath != "" && archivePath != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if patchPath == "" {
		t.Fatalf("expected patch artifact, got %+v", artifacts)
	}
	data, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	if !strings.Contains(string(data), "README.md") || !strings.Contains(string(data), "+changed") {
		t.Fatalf("patch does not include README change:\n%s", data)
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read untracked manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "notes/new_file.txt") {
		t.Fatalf("manifest does not include untracked file:\n%s", manifest)
	}
	if !tarContainsFile(t, archivePath, "notes/new_file.txt") {
		t.Fatalf("archive does not include untracked file %q", archivePath)
	}
	reportPath, paths := c.harnessReportForTask(res.AgentID)
	if reportPath == "" || !stringSliceContains(paths, patchPath) || !stringSliceContains(paths, archivePath) {
		t.Fatalf("mailbox artifact lookup should expose patch and archive alongside the synthesized report, report=%q paths=%+v", reportPath, paths)
	}
	var report harness.Report
	var ok bool
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		report, ok = c.harnessReportDetailsForTask(res.AgentID)
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || report.Kind != harness.ReportKindFinalText {
		t.Fatalf("worker that filed no agent_report should get a final_text synthesized report, got %+v ok=%v", report, ok)
	}
}

func tarContainsFile(t *testing.T, path, want string) bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open tar: %v", err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err != nil {
			return false
		}
		if header.Name == want {
			return true
		}
	}
}

func TestSpawn_RegistersNestedThreadPath(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-nested",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.StopAll()
	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "parent",
		Prompt:   "p",
	})
	if err != nil {
		t.Fatalf("parent spawn: %v", err)
	}
	child, err := c.Spawn(context.Background(), SpawnRequest{
		Type:       DefaultSubagentType,
		TaskName:   "child",
		Prompt:     "p",
		ParentID:   parent.AgentID,
		ParentPath: parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("child spawn: %v", err)
	}
	if child.AgentPath != "/root/parent/child" {
		t.Fatalf("unexpected nested path: %+v", child)
	}
	meta, ok := c.threads.ResolveFrom(parent.AgentPath, "child")
	if !ok || meta.ID != child.AgentID || meta.ParentID != parent.AgentID {
		t.Fatalf("nested child did not resolve from parent path: %+v ok=%v", meta, ok)
	}
	if err := c.SendMessageFrom(parent.AgentPath, context.Background(), "child", "queued from parent"); err != nil {
		t.Fatalf("SendMessageFrom parent path: %v", err)
	}
	updated, ok := c.threads.ResolveFrom(parent.AgentPath, "child")
	if !ok || updated.LastTaskMessage != "queued from parent" {
		t.Fatalf("nested message did not resolve to the child thread: %+v ok=%v", updated, ok)
	}
	c.StopAll()
}

func TestNestedResultRoutesToParentAgent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	client := newBlockingClient()
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-nested-route",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "parent",
		Prompt:   "p",
	})
	if err != nil {
		t.Fatalf("parent spawn: %v", err)
	}
	defer c.StopAll()
	client.waitStarted(t)

	delivered := c.deliverNestedResultToParent(context.Background(), subagent.SubAgentSnapshot{
		ID:          "child-1",
		ParentID:    parent.AgentID,
		AgentPath:   parent.AgentPath + "/child",
		TaskName:    "child",
		Type:        DefaultSubagentType,
		Status:      subagent.StatusCompleted,
		Description: "child task",
		Result:      "child done",
	})
	if !delivered {
		t.Fatal("expected nested result to route to parent")
	}
	queued, ok := c.Manager().NextPendingMessage(parent.AgentID)
	if !ok {
		t.Fatal("expected parent to receive nested result")
	}
	var communication agentthread.InterAgentCommunication
	if err := json.Unmarshal([]byte(queued), &communication); err != nil {
		t.Fatalf("nested result is not an inter-agent communication: %v\n%s", err, queued)
	}
	if communication.Author != agentthread.AgentPath(parent.AgentPath+"/child") || communication.Recipient != agentthread.AgentPath(parent.AgentPath) {
		t.Fatalf("nested result routed to wrong agents: %+v", communication)
	}
	if communication.TriggerTurn || !strings.Contains(communication.Content, "child done") {
		t.Fatalf("unexpected nested result payload: %+v", communication)
	}
}

func TestStopClosesAgentSubtree(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-close-tree",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "parent",
		Prompt:   "parent task",
	})
	if err != nil {
		t.Fatalf("parent spawn: %v", err)
	}
	child, err := c.Spawn(context.Background(), SpawnRequest{
		Type:       DefaultSubagentType,
		TaskName:   "child",
		Prompt:     "child task",
		ParentID:   parent.AgentID,
		ParentPath: parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("child spawn: %v", err)
	}

	if !c.Stop(parent.AgentPath) {
		t.Fatal("expected parent subtree stop to succeed")
	}
	if _, err := c.Wait(context.Background(), parent.AgentID); err != nil {
		t.Fatalf("wait parent: %v", err)
	}
	if _, err := c.Wait(context.Background(), child.AgentID); err != nil {
		t.Fatalf("wait child: %v", err)
	}
	for _, id := range []string{parent.AgentID, child.AgentID} {
		meta, ok := c.threads.Resolve(id)
		if !ok {
			t.Fatalf("missing metadata for %s", id)
		}
		if meta.Source.EdgeStatus != agentthread.EdgeClosed {
			t.Fatalf("expected %s edge closed, got %+v", id, meta.Source)
		}
	}
}

func TestWaitForAgentNotificationFromRootReportsChildFinalStatus(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-wait-root",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "child",
		Prompt:   "child task",
	})
	if err != nil {
		t.Fatalf("child spawn: %v", err)
	}

	type waitResult struct {
		signal WaitAgentSignal
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		signal, err := c.WaitForAgentNotificationFrom(agentthread.RootPath, ctx)
		done <- waitResult{signal: signal, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	if !c.Stop(child.AgentID) {
		t.Fatal("expected stop to succeed")
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("wait mailbox: %v", got.err)
		}
		if !got.signal.Received {
			t.Fatal("expected wait to complete")
		}
		if got.signal.SignalType != WaitAgentSignalCancelled || got.signal.AgentID != child.AgentID || got.signal.Status != string(subagent.StatusCancelled) {
			t.Fatalf("unexpected wait signal: %+v", got.signal)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for mailbox update")
	}
}

func TestWaitForAgentNotificationFromAgentReturnsQueuedMessageSignal(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	client := newBlockingClient()
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-wait-agent",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "parent",
		Prompt:   "parent task",
	})
	if err != nil {
		t.Fatalf("parent spawn: %v", err)
	}
	client.waitStarted(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct {
		signal WaitAgentSignal
		err    error
	}, 1)
	go func() {
		signal, err := c.WaitForAgentNotificationFrom(parent.AgentPath, ctx)
		done <- struct {
			signal WaitAgentSignal
			err    error
		}{signal: signal, err: err}
	}()

	if err := c.SendMessage(context.Background(), parent.AgentID, "queued update"); err != nil {
		t.Fatalf("send message: %v", err)
	}

	var signal WaitAgentSignal
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("wait mailbox: %v", got.err)
		}
		signal = got.signal
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queued message signal")
	}
	if !signal.Received {
		t.Fatal("expected queued mailbox update to wake wait")
	}
	if signal.SignalType != WaitAgentSignalQueuedMessage || signal.AgentID != parent.AgentID || signal.PendingMessageCount != 1 {
		t.Fatalf("unexpected queued message signal: %+v", signal)
	}
}

func TestSpawn_InplaceSkipsWorktree(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "looked at line 42"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-inplace",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Worker defaults to inplace and must not create anything under
	// the worktree root on disk — overlap with TestSpawn_SyncHappyPath
	// on the isolation field, but this one specifically pins the
	// no-disk-side-effect property by reading the worktree dir.
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "inplace",
		Description: "look",
		Prompt:      "p",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Isolation != "inplace" {
		t.Fatalf("expected isolation=inplace, got %q", res.Isolation)
	}
	if res.WorktreePath != "" {
		t.Fatalf("expected empty worktree path for inplace spawn, got %q", res.WorktreePath)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".wuu", "worktrees", "sess-inplace")); len(entries) != 0 {
		t.Fatalf("expected no worktrees on disk, got %d entries", len(entries))
	}
}

func TestSpawn_IsolationOverride(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "ok"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-override",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	// Worker defaults to inplace; explicit isolation="worktree"
	// must override that and put the worker in a fresh worktree.
	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "force_isolated",
		Description: "force-isolated",
		Prompt:      "p",
		Isolation:   "worktree",
		Synchronous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Isolation != "worktree" {
		t.Fatalf("override failed: %q", res.Isolation)
	}

	// And: explicit isolation="inplace" is a no-op (it matches the
	// default) but must still resolve cleanly without touching the
	// worktree directory.
	res2, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "explicit_inplace",
		Description: "explicit-inplace",
		Prompt:      "p",
		Isolation:   "inplace",
		Synchronous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Isolation != "inplace" || res2.WorktreePath != "" {
		t.Fatalf("explicit inplace failed: %+v", res2)
	}
}

func TestSpawn_PreservesCleanWorktreeForFollowup(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// fakeToolkit doesn't touch the filesystem, so the worker leaves
	// its worktree pristine. The coordinator must still keep it after
	// completion because child tasks can receive follow-up turns.
	//
	// Worker no longer defaults to worktree, so this test explicitly
	// opts in via Isolation: "worktree" — that's the supported way to
	// get an isolated child task now.
	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "ok"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-recycle",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "preserve_clean",
		Description: "noop",
		Prompt:      "p",
		Isolation:   "worktree",
		Synchronous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Isolation != "worktree" {
		t.Fatalf("expected worktree isolation, got %q", res.Isolation)
	}
	if res.WorktreePath == "" {
		t.Fatal("clean worktree should be preserved for follow-up turns")
	}
	if _, err := os.Stat(res.WorktreePath); err != nil {
		t.Fatalf("preserved worktree should exist: %v", err)
	}
}

func TestSpawn_KeepDirtyWorktree(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// Toolkit that drops a file in the worker's root before returning.
	dirtyKit := func(root string, _ WorkerType, _ agentthread.Metadata) (agent.ToolExecutor, error) {
		if err := os.WriteFile(filepath.Join(root, "scratch.txt"), []byte("x"), 0o644); err != nil {
			return nil, err
		}
		return fakeToolkit{}, nil
	}

	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "ok"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-dirty",
		WorkerFactory: dirtyKit,
	})

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "keep_dirty",
		Description: "modifies",
		Prompt:      "p",
		Isolation:   "worktree",
		Synchronous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorktreePath == "" {
		t.Fatal("dirty worktree should be preserved and path returned")
	}
	if _, err := os.Stat(res.WorktreePath); err != nil {
		t.Fatalf("dirty worktree should still be on disk: %v", err)
	}
}

func TestSpawn_RequiresPrompt(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	c, _ := New(Config{
		Client:        &fakeClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	_, err := c.Spawn(context.Background(), SpawnRequest{Description: "x"})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestSpawn_ConcurrencyCap(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// fakeClient with no delay completes instantly, so the cap is hard
	// to hit. Use a slow client.
	slow := &slowClient{}

	c, _ := New(Config{
		Client:        slow,
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess",
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		MaxParallel:   2,
	})
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	c.StartQueuedWork()

	// Fire 2 async spawns to fill the cap.
	var firstID string
	for i := 0; i < 2; i++ {
		res, err := c.Spawn(context.Background(), SpawnRequest{
			Type: DefaultSubagentType, TaskName: fmt.Sprintf("slow_%d", i), Description: "x", Prompt: "p",
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstID = res.AgentID
		}
	}

	// 3rd async spawn should be durably queued instead of dropping
	// the parent agent's intent.
	queued, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "slow_2", Description: "x", Prompt: "p",
	})
	if err != nil {
		t.Fatalf("queued spawn should not fail: %v", err)
	}
	if queued.Status != "queued" || queued.AgentID == "" || queued.AgentPath != "/root/slow_2" {
		t.Fatalf("unexpected queued result: %+v", queued)
	}
	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var foundQueued bool
	for _, task := range tasks {
		if task.ID == queued.AgentID {
			foundQueued = task.Status == harness.TaskStatusQueued
			break
		}
	}
	if !foundQueued {
		t.Fatalf("queued task not persisted: %+v", tasks)
	}
	list := c.List()
	var listedQueued bool
	for _, snap := range list {
		if snap.ID == queued.AgentID && snap.Status == subagent.StatusQueued {
			listedQueued = true
			break
		}
	}
	if !listedQueued {
		t.Fatalf("queued task not visible in List: %+v", list)
	}
	if !c.Stop(firstID) {
		t.Fatalf("expected to stop %s", firstID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sa := c.Manager().Get(queued.AgentID)
		if sa != nil && sa.Snapshot().Status == subagent.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sa := c.Manager().Get(queued.AgentID)
	if sa == nil || sa.Snapshot().Status != subagent.StatusRunning {
		t.Fatalf("queued task did not start after capacity freed: %+v", sa)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestNewRestoresQueuedSpawnPayload(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-restore-queue", "harness")
	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-restore-queue", "threads")
	now := time.Now().UTC()
	meta := agentthread.Metadata{
		ID:        "worker-restored",
		SessionID: "sess-restore-queue",
		ParentID:  "sess-restore-queue",
		Path:      "/root/restored_task",
		TaskName:  "restored_task",
		Role:      DefaultSubagentType,
		Status:    agentthread.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: "sess-restore-queue",
			ParentPath:     agentthread.RootPath,
			Depth:          2,
			EdgeStatus:     agentthread.EdgeOpen,
		},
	}
	payload, err := json.Marshal(map[string]any{
		"worker_id":   meta.ID,
		"worker_type": DefaultSubagentType,
		"thread_meta": meta,
		"prompt":      "resume queued task",
		"isolation":   "inplace",
		"goal_id":     "legacy-goal",
		"goal_dir":    filepath.Join(dir, ".wuu", "state", "goals", "legacy-goal"),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	store := harness.NewStore(harnessDir)
	if err := store.UpsertTask(harness.Task{
		ID:        meta.ID,
		SessionID: meta.SessionID,
		ParentID:  meta.ParentID,
		Path:      meta.Path,
		Name:      meta.TaskName,
		Role:      meta.Role,
		Intent:    "resume queued task",
		Status:    harness.TaskStatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	if err := store.UpsertQueueItem(harness.QueueItem{
		ID:      meta.ID,
		TaskID:  meta.ID,
		Kind:    "agent_spawn",
		Payload: payload,
	}); err != nil {
		t.Fatalf("UpsertQueueItem: %v", err)
	}

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-restore-queue",
		ThreadDir:     threadDir,
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		MaxParallel:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	// Construction restores metadata but must not run it before the embedding
	// runtime has attached its model resolver and reliable terminal consumer.
	time.Sleep(2 * queuedSpawnAckRetryDelay)
	if sa := c.Manager().Get(meta.ID); sa != nil {
		t.Fatalf("restored queued task started during AgentControl construction: %+v", sa.Snapshot())
	}
	if exists, existsErr := c.HarnessStore().QueueItemExists(meta.ID); existsErr != nil || !exists {
		t.Fatalf("restored queue before explicit start = %v, %v; want true, nil", exists, existsErr)
	}
	c.StartQueuedWork()
	c.StartQueuedWork() // idempotent: one durable payload must produce one worker.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sa := c.Manager().Get(meta.ID)
		if sa != nil && sa.Snapshot().Status == subagent.StatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sa := c.Manager().Get(meta.ID)
	if sa == nil || sa.Snapshot().Status != subagent.StatusRunning {
		t.Fatalf("restored queued task did not start: %+v", sa)
	}
	if got, ok := c.threads.Resolve(meta.Path); !ok || got.ID != meta.ID {
		t.Fatalf("restored thread metadata did not resolve: %+v ok=%v", got, ok)
	}
	waitForQueuedSpawnRecoveryTest(t, func() bool {
		exists, existsErr := c.HarnessStore().QueueItemExists(meta.ID)
		return existsErr == nil && !exists
	}, "restored queued spawn acknowledgement")
	items, err := c.HarnessStore().ListQueueItems()
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("queue item should be deleted after start, got %+v", items)
	}
	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("restored queued spawn should ignore legacy goal binding fields, got %+v", tasks)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

// slowClient never returns until context is cancelled.
type slowClient struct{}

func (slowClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	<-ctx.Done()
	return providers.ChatResponse{}, ctx.Err()
}

// StreamChat opens a channel that only emits an error event once the
// caller's context is cancelled. Mirrors Chat's blocking semantics so
// the concurrency-cap test still pins a worker until StopAll fires.
func (slowClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
	}()
	return ch, nil
}

type blockingClient struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingClient() *blockingClient {
	return &blockingClient{started: make(chan struct{})}
}

func (c *blockingClient) markStarted() {
	c.once.Do(func() { close(c.started) })
}

func (c *blockingClient) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter model request")
	}
}

func (c *blockingClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.markStarted()
	<-ctx.Done()
	return providers.ChatResponse{}, ctx.Err()
}

func (c *blockingClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.markStarted()
	ch := make(chan providers.StreamEvent, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
	}()
	return ch, nil
}

func TestSendMessage_QueuesWhileRunning(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// Keep worker inside its model request so the pending mailbox cannot drain
	// before this test observes it.
	slow := newBlockingClient()
	c, err := New(Config{
		Client:        slow,
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-send-running",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "send_running", Description: "slow", Prompt: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	slow.waitStarted(t)
	if err := c.SendMessage(context.Background(), res.AgentID, "please also check logs"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := c.Manager().PendingMessageCount(res.AgentID); got != 1 {
		t.Fatalf("expected pending queue size=1, got %d", got)
	}

}

func TestSendMessage_ResolvesThreadPathAndTaskName(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-send-path",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "review_config",
		Description: "slow",
		Prompt:      "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.AgentPath, "/root/") {
		t.Fatalf("expected canonical path, got %q", res.AgentPath)
	}
	if err := c.SendMessage(context.Background(), res.AgentPath, "check env files too"); err != nil {
		t.Fatalf("SendMessage by path: %v", err)
	}
	if err := c.SendMessage(context.Background(), "review_config", "check defaults too"); err != nil {
		t.Fatalf("SendMessage by task name: %v", err)
	}
	if updated, ok := c.Threads().Resolve(res.AgentID); !ok || updated.LastTaskMessage != "check defaults too" {
		t.Fatalf("task-name message did not resolve to the child thread: %+v ok=%v", updated, ok)
	}

	c.StopAll()
}

func TestSpawn_AsyncDetachedFromParentContext(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-detached-spawn",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	parentCtx, cancelParent := context.WithCancel(context.Background())
	res, err := c.Spawn(parentCtx, SpawnRequest{
		Type: DefaultSubagentType, TaskName: "detached_spawn", Description: "slow", Prompt: "p",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	cancelParent()
	time.Sleep(50 * time.Millisecond)

	sa := c.Manager().Get(res.AgentID)
	if sa == nil {
		t.Fatalf("expected worker %q to exist", res.AgentID)
	}
	if snap := sa.Snapshot(); snap.Status != subagent.StatusRunning {
		t.Fatalf("expected detached async worker to keep running after parent cancel, got %s", snap.Status)
	}

	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestSpawn_SynchronousWaitInterruptBackgroundsWorker(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-interruptible-wait",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	waitInterrupt := make(chan struct{})
	resultCh := make(chan *SpawnResult, 1)
	errorCh := make(chan error, 1)
	go func() {
		result, spawnErr := c.Spawn(context.Background(), SpawnRequest{
			Type:          DefaultSubagentType,
			TaskName:      "background_on_steer",
			Description:   "slow",
			Prompt:        "keep working",
			Synchronous:   true,
			WaitInterrupt: waitInterrupt,
		})
		resultCh <- result
		errorCh <- spawnErr
	}()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter model request")
	}
	close(waitInterrupt)
	result := <-resultCh
	if err := <-errorCh; err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if result == nil || !result.Backgrounded || result.Status != string(subagent.StatusRunning) {
		t.Fatalf("interrupted synchronous wait result = %+v", result)
	}
	sa := c.Manager().Get(result.AgentID)
	if sa == nil || sa.Snapshot().Status != subagent.StatusRunning {
		t.Fatalf("backgrounded worker did not remain running: %+v", sa)
	}

	close(client.release)
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	completed, err := c.Manager().Wait(waitCtx, result.AgentID)
	if err != nil {
		t.Fatalf("wait for backgrounded worker completion: %v", err)
	}
	if completed.Status != subagent.StatusCompleted || completed.Result != "done" {
		t.Fatalf("backgrounded worker completion = %+v", completed)
	}
}

func TestFork_AsyncDetachedFromParentContext(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-detached-fork",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	parentHistory := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	parentCtx, cancelParent := context.WithCancel(context.Background())
	res, err := c.Fork(parentCtx, ForkRequest{
		TaskName:    "detached_fork",
		Description: "slow fork",
		Prompt:      "continue",
	}, parentHistory)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	cancelParent()
	time.Sleep(50 * time.Millisecond)

	sa := c.Manager().Get(res.AgentID)
	if sa == nil {
		t.Fatalf("expected worker %q to exist", res.AgentID)
	}
	if snap := sa.Snapshot(); snap.Status != subagent.StatusRunning {
		t.Fatalf("expected detached async fork to keep running after parent cancel, got %s", snap.Status)
	}

	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

func TestFork_WorktreeIsolation(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	client := &recordingClient{resp: providers.ChatResponse{Content: "done"}}
	var capturedRoot string
	c, err := New(Config{
		Client:       client,
		DefaultModel: "fake",
		ParentRepo:   dir,
		WorktreeRoot: filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:    "sess-fork-worktree",
		WorkerFactory: func(root string, _ WorkerType, _ agentthread.Metadata) (agent.ToolExecutor, error) {
			capturedRoot = root
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	parentHistory := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	res, err := c.Fork(context.Background(), ForkRequest{
		TaskName:    "fork_worktree",
		Description: "worktree fork",
		Prompt:      "continue",
		Isolation:   "worktree",
		Synchronous: true,
	}, parentHistory)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("expected completed fork, got %s", res.Status)
	}
	if res.Isolation != "worktree" {
		t.Fatalf("expected worktree isolation, got %q", res.Isolation)
	}
	if res.WorktreePath == "" {
		t.Fatal("expected fork worktree path")
	}
	if capturedRoot != res.WorktreePath {
		t.Fatalf("worker factory root %q did not match result worktree %q", capturedRoot, res.WorktreePath)
	}
	if capturedRoot == dir {
		t.Fatal("worktree fork should not run in the parent repo")
	}
	if _, err := os.Stat(res.WorktreePath); err != nil {
		t.Fatalf("fork worktree should exist: %v", err)
	}

	last := client.LastRequest()
	visible := visibleMessagesForTest(last.Messages)
	if len(visible) != len(parentHistory)+1 {
		t.Fatalf("expected parent history plus final fork prompt, got %+v", last.Messages)
	}
	tail := visible[len(visible)-1]
	if tail.Role != "user" || !strings.Contains(tail.Content, "continue") {
		t.Fatalf("expected final fork task prompt, got %+v", tail)
	}
	if !strings.Contains(tail.Content, res.WorktreePath) || !strings.Contains(tail.Content, "Isolation mode: worktree") {
		t.Fatalf("fork prompt should remind worker of worktree root, got %q", tail.Content)
	}
}

func TestSendMessageDoesNotRejectCancelledAgent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &slowClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-cancel-send",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "cancel_send", Description: "slow", Prompt: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Stop(res.AgentID) {
		t.Fatal("Stop returned false")
	}
	waitForSubAgentStatus(t, c.Manager(), res.AgentID, subagent.StatusCancelled, time.Second)

	// A cancelled run is resumable, so send_message must accept the message
	// instead of rejecting it with "cannot receive messages".
	if err := c.SendMessage(context.Background(), res.AgentID, "please pick this back up"); err != nil {
		t.Fatalf("SendMessage on cancelled agent should not be rejected: %v", err)
	}
	c.StopAll()
	waitForRunningWorkersToStop(t, c.Manager(), time.Second)
}

// TestSendMessage_RevivesCompletedWorker locks the queue-or-resume upgrade:
// send_message now shares followup_task's semantics, so a message to a
// completed worker revives it in place with its full context plus the new
// message rather than parking the note in a mailbox nobody drains.
func TestSendMessage_RevivesCompletedWorker(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	client := &recordingClient{resp: providers.ChatResponse{Content: "revived result"}}
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-send-revive",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "send_revive", Description: "quick", Prompt: "p", Synchronous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "completed" {
		t.Fatalf("expected completed spawn, got %s", res.Status)
	}

	if err := c.SendMessage(context.Background(), res.AgentID, "recheck the logs"); err != nil {
		t.Fatalf("SendMessage revive: %v", err)
	}
	snap, err := c.Manager().Wait(context.Background(), res.AgentID)
	if err != nil {
		t.Fatalf("Wait revive: %v", err)
	}
	if snap.Status != subagent.StatusCompleted || snap.Result != "revived result" {
		t.Fatalf("expected revived completion, got %s result=%q", snap.Status, snap.Result)
	}
	if got := c.Manager().PendingMessageCount(res.AgentID); got != 0 {
		t.Fatalf("revive should drain the queued message into the resume turn, got pending=%d", got)
	}
	visible := visibleMessagesForTest(client.LastRequest().Messages)
	if len(visible) == 0 {
		t.Fatal("revive turn issued no request")
	}
	tail := visible[len(visible)-1]
	if tail.Role != "user" || !strings.Contains(tail.Content, "recheck the logs") {
		t.Fatalf("revive request should end with the new message, got %+v", tail)
	}
}

// writeResumeSnapshot writes a raw persisted-run JSON to the history dir,
// bypassing a live worker so rehydration edge cases (missing worktree,
// pre-version snapshots) can be exercised deterministically.
func writeResumeSnapshot(t *testing.T, historyDir, id string, version int, cwd, status string) {
	t.Helper()
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"id":         id,
		"type":       DefaultSubagentType,
		"task_name":  "resumed_task",
		"agent_path": agentthread.RootPath + "/resumed_task",
		"status":     status,
		"cwd":        cwd,
		"model":      "fake-model",
		"prompt":     "do it",
		"messages": []map[string]any{
			{"role": "system", "content": "you are a worker"},
			{"role": "user", "content": "do it"},
		},
	}
	if version != 0 {
		rec["version"] = version
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFollowupTaskRehydratesAcrossRestart simulates a process restart: a
// first AgentControl runs a worker to completion, and a fresh instance
// pointed at the same history dir resumes that dead run from its snapshot
// when a follow-up addresses it, without any startup scan.
func TestFollowupTaskRehydratesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	historyDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-restart", "workers")
	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-restart", "threads")
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-restart", "harness")

	first, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "first done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-restart",
		HistoryDir:    historyDir,
		ThreadDir:     threadDir,
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := first.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "resume_restart",
		Description: "resume me",
		Prompt:      "do the first pass",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("expected completed first run, got %s", res.Status)
	}
	if _, err := os.Stat(filepath.Join(historyDir, res.AgentID+".json")); err != nil {
		t.Fatalf("history snapshot not written: %v", err)
	}
	first.Close()

	// Fresh AgentControl over the same history dir stands in for a restart.
	client := &recordingClient{resp: providers.ChatResponse{Content: "resumed done"}}
	second, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-restart",
		HistoryDir:    historyDir,
		ThreadDir:     threadDir,
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if second.Manager().Get(res.AgentID) != nil {
		t.Fatal("resumed manager should not track the dead run before rehydration")
	}

	snap, err := second.FollowupTask(context.Background(), res.AgentID, "now do the second pass")
	if err != nil {
		t.Fatalf("FollowupTask across restart: %v", err)
	}
	if snap.Status != subagent.StatusRunning {
		t.Fatalf("expected resumed run running, got %s", snap.Status)
	}
	final, err := second.Manager().Wait(context.Background(), res.AgentID)
	if err != nil {
		t.Fatalf("Wait resumed: %v", err)
	}
	if final.Status != subagent.StatusCompleted || final.Result != "resumed done" {
		t.Fatalf("expected resumed completion, got %s result=%q", final.Status, final.Result)
	}
	visible := visibleMessagesForTest(client.LastRequest().Messages)
	if len(visible) == 0 {
		t.Fatal("resumed turn issued no request")
	}
	tail := visible[len(visible)-1]
	if tail.Role != "user" || !strings.Contains(tail.Content, "now do the second pass") {
		t.Fatalf("resumed request should end with the follow-up, got %+v", tail)
	}
	// The prior turn's history must be carried into the resume request.
	var sawPriorResult bool
	for _, msg := range visible {
		if strings.Contains(msg.Content, "first done") {
			sawPriorResult = true
		}
	}
	if !sawPriorResult {
		t.Fatalf("resumed request lost the prior turn history: %+v", visible)
	}
}

// TestFollowupTaskRehydrateMissingWorktreeFails asserts that a snapshot whose
// working directory is gone (e.g. a cleaned-up worktree) refuses to resume
// with a clear error instead of silently rerooting somewhere else.
func TestFollowupTaskRehydrateMissingWorktreeFails(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	historyDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-gone", "workers")
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-gone",
		HistoryDir:    historyDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	gone := filepath.Join(dir, "worktrees", "cleaned-up")
	writeResumeSnapshot(t, historyDir, "worker-gone", subagent.ResumeSnapshotVersion, gone, "failed")

	_, err = c.FollowupTask(context.Background(), "worker-gone", "please resume")
	if err == nil {
		t.Fatal("expected resume to fail when the working directory is gone")
	}
	if !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("error should name the missing working directory, got %v", err)
	}
}

// TestFollowupTaskRehydratePreVersionSnapshotFails asserts a snapshot written
// before resume support refuses to resume with a clear, actionable error.
func TestFollowupTaskRehydratePreVersionSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	historyDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-old", "workers")
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-old",
		HistoryDir:    historyDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	// version 0 (omitted) + an existing cwd, so only the version gate fires.
	writeResumeSnapshot(t, historyDir, "worker-old", 0, dir, "failed")

	_, err = c.FollowupTask(context.Background(), "worker-old", "please resume")
	if err == nil {
		t.Fatal("expected resume to fail for a pre-version snapshot")
	}
	if !strings.Contains(err.Error(), "predates resume support") {
		t.Fatalf("error should explain the snapshot predates resume support, got %v", err)
	}
}

// TestAgentMailboxMessageResumeHint locks the failure-mailbox resumability
// hint: failed and cancelled runs advertise that their context is preserved
// and can be resumed, while completed runs do not. The root-path mailbox
// carries the same hint because both paths share the mailbox builder.
func TestAgentMailboxMessageResumeHint(t *testing.T) {
	now := time.Now().UTC()
	failed := subagent.SubAgentSnapshot{
		ID:          "worker-x",
		AgentPath:   "/root/worker_x",
		Status:      subagent.StatusFailed,
		Error:       errors.New("api terminal error"),
		StartedAt:   now.Add(-time.Second),
		CompletedAt: now,
	}
	msg := NewAgentMailboxMessage(failed)
	if !msg.Resumable || msg.ResumeHint == "" {
		t.Fatalf("failed mailbox should be resumable with a hint: %+v", msg)
	}

	cancelled := failed
	cancelled.Status = subagent.StatusCancelled
	cancelled.Error = nil
	if m := NewAgentMailboxMessage(cancelled); !m.Resumable || m.ResumeHint == "" {
		t.Fatalf("cancelled mailbox should be resumable with a hint: %+v", m)
	}

	completed := subagent.SubAgentSnapshot{
		ID:          "worker-y",
		Status:      subagent.StatusCompleted,
		Result:      "done",
		StartedAt:   now.Add(-time.Second),
		CompletedAt: now,
	}
	if m := NewAgentMailboxMessage(completed); m.Resumable || m.ResumeHint != "" {
		t.Fatalf("completed mailbox should not advertise resume: %+v", m)
	}

	// Root-path parity: the root completion communication for a failed run
	// serializes the same hint field.
	root := FormatAgentMailboxMessage(failed)
	if !strings.Contains(root, "resume_hint") {
		t.Fatalf("root-path mailbox should include the resume hint, got %q", root)
	}
}

func TestSendMessage_RejectsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "ok"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err := c.SendMessage(context.Background(), "", "x"); err == nil {
		t.Fatal("expected target required error")
	}
	if err := c.SendMessage(context.Background(), "worker-123", ""); err == nil {
		t.Fatal("expected message required error")
	}
}

func TestAgentMailboxChatMessage(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "found bug at line 42"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	res, _ := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "find_bug",
		Description: "find the bug",
		Prompt:      "look for it",
		Synchronous: true,
	})

	snap := c.Manager().Get(res.AgentID).Snapshot()
	msg := AgentMailboxChatMessage(snap)
	if msg.Role != "assistant" || msg.Name != "" {
		t.Fatalf("unexpected mailbox chat message envelope: %+v", msg)
	}
	var communication agentthread.InterAgentCommunication
	if err := json.Unmarshal([]byte(msg.Content), &communication); err != nil {
		t.Fatalf("mailbox payload is not JSON: %v\n%s", err, msg.Content)
	}
	if communication.Author != agentthread.AgentPath(snap.AgentPath) || communication.Recipient != agentthread.RootAgentPath() || communication.TriggerTurn {
		t.Fatalf("unexpected inter-agent envelope: %+v", communication)
	}
	var fragment struct {
		AgentPath string              `json:"agent_path"`
		Status    AgentMailboxMessage `json:"status"`
	}
	content := strings.TrimPrefix(communication.Content, "<subagent_notification>\n")
	content = strings.TrimSuffix(content, "\n</subagent_notification>")
	if err := json.Unmarshal([]byte(content), &fragment); err != nil {
		t.Fatalf("notification content is not JSON: %v\n%s", err, communication.Content)
	}
	payload := fragment.Status
	if payload.Type != "agent_result" || payload.AgentID != res.AgentID || payload.Result != "found bug at line 42" {
		t.Fatalf("unexpected mailbox payload: %+v", payload)
	}
	if payload.Description != "find the bug" || payload.Status != "completed" {
		t.Fatalf("missing summary/status in mailbox payload: %+v", payload)
	}
}

func TestAgentCompletionChatMessageTriggersRootTurn(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "found bug at line 42"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	res, _ := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "find_bug",
		Description: "find the bug",
		Prompt:      "look for it",
		Synchronous: true,
	})

	snap := c.Manager().Get(res.AgentID).Snapshot()
	msg := c.AgentCompletionChatMessage(snap, agentthread.RootPath)
	if msg.Role != "user" || msg.Name != wuucontext.AgentNotificationMessageName {
		t.Fatalf("unexpected completion chat message envelope: %+v", msg)
	}
	var communication agentthread.InterAgentCommunication
	if err := json.Unmarshal([]byte(msg.Content), &communication); err != nil {
		t.Fatalf("completion payload is not JSON: %v\n%s", err, msg.Content)
	}
	if communication.Author != agentthread.AgentPath(snap.AgentPath) || communication.Recipient != agentthread.RootAgentPath() || !communication.TriggerTurn {
		t.Fatalf("unexpected inter-agent envelope: %+v", communication)
	}
	if !strings.Contains(communication.Content, "found bug at line 42") {
		t.Fatalf("completion content missing result: %s", communication.Content)
	}
	if communication.ChangedFileOverlap != nil {
		t.Fatalf("single-agent completion should keep ChangedFileOverlap nil, got %+v", communication.ChangedFileOverlap)
	}
}

func TestAgentCompletionPersistsLongResultAndReturnsPreview(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-long-result", "harness")
	longResult := "BEGIN\n" + strings.Repeat("middle payload\n", 500) + "END"
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: longResult}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess-long-result",
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "long_result",
		Description: "produce long result",
		Prompt:      "return long result",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !res.ResultTruncated || res.ResultPath == "" || res.ResultBytes != len([]byte(longResult)) {
		t.Fatalf("spawn result should return a persisted preview, got %+v", res)
	}
	if res.Result == longResult || !strings.Contains(res.Result, "agent result preview truncated") {
		t.Fatalf("spawn result should be a bounded preview, got %d chars", len(res.Result))
	}
	resultPath := sessionRefPath(t, c, res.ResultPath)
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result artifact: %v", err)
	}
	if string(raw) != longResult {
		t.Fatalf("result artifact mismatch")
	}

	snap := c.Manager().Get(res.AgentID).Snapshot()
	msg := c.AgentCompletionChatMessage(snap, agentthread.RootPath)
	var communication agentthread.InterAgentCommunication
	if err := json.Unmarshal([]byte(msg.Content), &communication); err != nil {
		t.Fatalf("completion payload is not JSON: %v\n%s", err, msg.Content)
	}
	var fragment struct {
		AgentPath string              `json:"agent_path"`
		Status    AgentMailboxMessage `json:"status"`
	}
	content := strings.TrimPrefix(communication.Content, "<subagent_notification>\n")
	content = strings.TrimSuffix(content, "\n</subagent_notification>")
	if err := json.Unmarshal([]byte(content), &fragment); err != nil {
		t.Fatalf("notification content is not JSON: %v\n%s", err, communication.Content)
	}
	if !fragment.Status.ResultTruncated || fragment.Status.ResultPath != res.ResultPath || fragment.Status.Result == longResult {
		t.Fatalf("mailbox should return preview plus result_path, got %+v", fragment.Status)
	}
	if !strings.Contains(communication.Content, "result_path") || strings.Contains(communication.Content, longResult) {
		t.Fatalf("completion should include result_path without raw long result")
	}
}

func waitForRunningWorkersToStop(t *testing.T, mgr *subagent.Manager, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if mgr.CountRunning() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected workers to stop within %s, still have %d running", timeout, mgr.CountRunning())
}

func waitForSubAgentStatus(t *testing.T, mgr *subagent.Manager, id string, want subagent.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sa := mgr.Get(id); sa != nil && sa.Snapshot().Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := subagent.Status("<missing>")
	if sa := mgr.Get(id); sa != nil {
		got = sa.Snapshot().Status
	}
	t.Fatalf("expected sub-agent %s to reach %s within %s, got %s", id, want, timeout, got)
}

// waitForHarnessTaskStatus polls a single harness task until it reaches the
// expected status. Task status is now driven by the runtime completion
// notification (an async goroutine), not synchronously by agent_report, so
// callers that read it right after a synchronous spawn must wait for the
// observed lifecycle transition.
func waitForHarnessTaskStatus(t *testing.T, store *harness.Store, taskID string, status harness.TaskStatus) harness.Task {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var last []harness.Task
	for time.Now().Before(deadline) {
		tasks, err := store.ListTasks()
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		last = tasks
		for _, task := range tasks {
			if task.ID == taskID && task.Status == status {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected harness task %s to reach status %s, got %+v", taskID, status, last)
	return harness.Task{}
}

func waitForHarnessEvent(t *testing.T, store *harness.Store, eventType harness.EventType, taskID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, err := store.ReadEvents()
		if err != nil {
			t.Fatalf("ReadEvents: %v", err)
		}
		for _, event := range events {
			if event.Type == eventType && event.TaskID == taskID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected harness event %s for %s", eventType, taskID)
}

func TestAgentControlRecordsRootCompletionMessageEvent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-events", "threads")
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		ThreadDir:     threadDir,
		SessionID:     "sess-events",
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "record_event",
		Prompt:      "record",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	store := agentthread.NewStore(threadDir)
	deadline := time.Now().Add(time.Second)
	for {
		events, err := store.ReadEvents()
		if err != nil {
			t.Fatalf("ReadEvents: %v", err)
		}
		for _, event := range events {
			if event.Type == agentthread.EventMessage && event.AuthorPath == "/root/record_event" && event.RecipientPath == "/root" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected root completion message event, got %+v", events)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func artifactKindPresent(artifacts []harness.Artifact, kind harness.ArtifactKind) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}

func sessionRefPath(t *testing.T, c *AgentControl, ref string) string {
	t.Helper()
	if !strings.HasPrefix(ref, "$SESSION_DIR") {
		t.Fatalf("expected session ref, got %q", ref)
	}
	suffix := strings.TrimPrefix(ref, "$SESSION_DIR")
	suffix = strings.TrimPrefix(suffix, "/")
	return filepath.Join(c.sessionDir(), filepath.FromSlash(suffix))
}

func mustReadAgentControlFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

// recordingParticipantStore captures Upsert calls for spawn tests.
type recordingParticipantStore struct {
	mu      sync.Mutex
	upserts []participant.Participant
}

func (s *recordingParticipantStore) Upsert(p participant.Participant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, p)
	return nil
}

func (s *recordingParticipantStore) list() []participant.Participant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]participant.Participant(nil), s.upserts...)
}

func TestSpawn_CreatesEphemeralParticipant(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	store := &recordingParticipantStore{}
	c, err := New(Config{
		Client:           &fakeClient{resp: providers.ChatResponse{Content: "task done"}},
		DefaultModel:     "fake-model",
		ParentRepo:       dir,
		WorktreeRoot:     filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:        "sess-participant",
		HistoryDir:       filepath.Join(dir, ".wuu", "sessions", "sess-participant", "workers"),
		ParticipantStore: store,
		WorkerFactory:    func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "scan_auth",
		Description: "test",
		Prompt:      "do something",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	snap := c.Manager().Get(res.AgentID).Snapshot()
	if snap.ParticipantID == "" {
		t.Fatal("snapshot ParticipantID should be non-empty after spawn")
	}

	upserts := store.list()
	if len(upserts) != 1 {
		t.Fatalf("expected 1 participant upsert, got %d", len(upserts))
	}
	p := upserts[0]
	if p.ID != snap.ParticipantID {
		t.Fatalf("participant ID %q should match snapshot ParticipantID %q", p.ID, snap.ParticipantID)
	}
	if p.Kind != participant.KindEphemeral {
		t.Fatalf("participant kind = %q, want ephemeral", p.Kind)
	}
	wantName := participant.DeriveEphemeralName(snap.TaskName, DefaultSubagentType)
	if p.Name != wantName {
		t.Fatalf("participant name = %q, want %q", p.Name, wantName)
	}
	if p.Role != DefaultSubagentType {
		t.Fatalf("participant role = %q, want %q", p.Role, DefaultSubagentType)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatalf("participant timestamps should be set: %+v", p)
	}
}

func TestSpawn_WithoutParticipantStoreStillAssignsParticipantID(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "task done"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-no-store",
		HistoryDir:    filepath.Join(dir, ".wuu", "sessions", "sess-no-store", "workers"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "no_store",
		Prompt:      "do something",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	snap := c.Manager().Get(res.AgentID).Snapshot()
	if snap.ParticipantID == "" {
		t.Fatal("snapshot ParticipantID should be non-empty even without a participant store")
	}
}

// nudgeCountingClient counts model turns so the closing-nudge test can prove
// the mechanical agent_report turn happens exactly once.
type nudgeCountingClient struct {
	resp  providers.ChatResponse
	calls nudgeCallCounter
}

type nudgeCallCounter struct {
	mu sync.Mutex
	n  int
}

func (c *nudgeCallCounter) add() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (c *nudgeCallCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *nudgeCountingClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.calls.add()
	return c.resp, nil
}

func (c *nudgeCountingClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.calls.add()
	ch := make(chan providers.StreamEvent, 2)
	if c.resp.Content != "" {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.resp.Content}
	}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

// TestRequiresReportWorkerGetsOneClosingNudge locks the mechanical closing
// contract: a requires_report worker that completes without agent_report gets
// exactly one forced closing turn, and when that turn still files nothing the
// run completes with a synthesized final_text report — never a third turn,
// never a lifecycle state.
func TestRequiresReportWorkerGetsOneClosingNudge(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-nudge", "harness")
	client := &nudgeCountingClient{resp: providers.ChatResponse{Content: "looked at the diff, all good"}}
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-nudge",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-nudge", "workers"),
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-nudge", "threads"),
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	closingTerminalObserved := make(chan struct{})
	var closingObservedOnce sync.Once
	c.SetReportClosingFollowupHookForTest(func(workerID string) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			worker := c.Manager().Get(workerID)
			if client.calls.count() >= 2 && worker != nil && isFinalSubAgentStatus(worker.Snapshot().Status) {
				closingObservedOnce.Do(func() { close(closingTerminalObserved) })
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
	var finalizerMu sync.Mutex
	var finalized []subagent.Notification
	unsubscribe := c.SubscribeWorkerTerminalFinalizer(func(notification subagent.Notification) error {
		finalizerMu.Lock()
		finalized = append(finalized, notification)
		finalizerMu.Unlock()
		return nil
	})
	defer unsubscribe()

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        requiresReportWorkerType,
		TaskName:    "review_diff",
		Description: "review the diff",
		Prompt:      "review this diff",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	store := c.HarnessStore()
	var reports []harness.Report
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reports, err = store.ListReports()
		if err != nil {
			t.Fatalf("ListReports: %v", err)
		}
		if len(reports) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(reports) != 1 || reports[0].Kind != harness.ReportKindFinalText {
		t.Fatalf("expected one synthesized final_text report after the failed nudge, got %+v", reports)
	}

	// Let any stray turn settle, then prove the nudge ran exactly once:
	// initial turn + one forced closing turn, nothing further.
	time.Sleep(150 * time.Millisecond)
	if got := client.calls.count(); got != 2 {
		t.Fatalf("expected exactly 2 model turns (initial + closing nudge), got %d", got)
	}
	select {
	case <-closingTerminalObserved:
	default:
		t.Fatal("test did not force the closing generation terminal before the original observer resumed")
	}
	finalizerMu.Lock()
	finalizedCount := len(finalized)
	finalizerMu.Unlock()
	if finalizedCount != 1 {
		t.Fatalf("external terminal finalizer calls = %d, want only the closing generation", finalizedCount)
	}

	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != res.AgentID || tasks[0].Status != harness.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", tasks)
	}
}
