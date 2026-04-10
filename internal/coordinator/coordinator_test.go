package coordinator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// fakeClient returns a canned response on every Chat call.
type fakeClient struct {
	resp providers.ChatResponse
}

func (f *fakeClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return f.resp, nil
}

// fakeToolkit is a no-op tool executor.
type fakeToolkit struct{}

func (fakeToolkit) Definitions() []providers.ToolDefinition { return nil }
func (fakeToolkit) Execute(_ context.Context, _ providers.ToolCall) (string, error) {
	return "", nil
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

func TestNew_RequiresGitRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	_, err := New(Config{
		Client:        &fakeClient{},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		WorkerFactory: func(string) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err == nil {
		t.Fatal("expected error for non-git directory")
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
		HistoryDir:    filepath.Join(dir, ".wuu", "sessions", "sess-1", "workers"),
		WorkerFactory: func(string) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        "worker",
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
	if res.WorktreePath == "" {
		t.Fatal("worktree path empty")
	}
	if _, statErr := os.Stat(res.WorktreePath); statErr != nil {
		t.Fatalf("worktree should exist after spawn, got: %v", statErr)
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
		WorkerFactory: func(string) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
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
		WorkerFactory: func(string) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		MaxParallel:   2,
	})

	// Fire 2 async spawns to fill the cap.
	for i := 0; i < 2; i++ {
		_, err := c.Spawn(context.Background(), SpawnRequest{
			Type: "worker", Description: "x", Prompt: "p",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// 3rd spawn should be rejected.
	_, err := c.Spawn(context.Background(), SpawnRequest{
		Type: "worker", Description: "x", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected concurrency cap error")
	}
	c.StopAll()
}

// slowClient never returns until context is cancelled.
type slowClient struct{}

func (slowClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	<-ctx.Done()
	return providers.ChatResponse{}, ctx.Err()
}

func TestFormatWorkerResult(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, _ := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "found bug at line 42"}},
		DefaultModel:  "fake",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		SessionID:     "sess",
		WorkerFactory: func(string) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})

	res, _ := c.Spawn(context.Background(), SpawnRequest{
		Type:        "explorer",
		Description: "find the bug",
		Prompt:      "look for it",
		Synchronous: true,
	})

	snap := c.Manager().Get(res.AgentID).Snapshot()
	xml := FormatWorkerResult(snap)
	if !contains(xml, "<worker-result") || !contains(xml, "found bug at line 42") {
		t.Fatalf("worker-result XML missing expected fields: %s", xml)
	}
	if !contains(xml, "find the bug") {
		t.Fatalf("summary missing: %s", xml)
	}
	if !contains(xml, "completed") {
		t.Fatalf("status missing: %s", xml)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
