package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/cron"
)

func TestScheduleCronTool(t *testing.T) {
	dir := t.TempDir()
	env := &Env{RootDir: dir}
	tool := NewScheduleCronTool(env)

	result, err := tool.Execute(context.Background(), `{"cron":"*/5 * * * *","prompt":"check deploy","recurring":true}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestCancelCronTool(t *testing.T) {
	dir := t.TempDir()
	env := &Env{RootDir: dir}
	store := cron.NewTaskStore(filepath.Join(dir, ".wuu", "scheduled_tasks.json"))
	store.Add(cron.Task{ID: "abc123", Cron: "* * * * *", Prompt: "x"})

	tool := NewCancelCronTool(env)
	result, err := tool.Execute(context.Background(), `{"id":"abc123"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestListCronTool(t *testing.T) {
	dir := t.TempDir()
	env := &Env{RootDir: dir}
	store := cron.NewTaskStore(filepath.Join(dir, ".wuu", "scheduled_tasks.json"))
	store.Add(cron.Task{ID: "abc", Cron: "*/5 * * * *", Prompt: "check"})

	tool := NewListCronTool(env)
	result, err := tool.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}
