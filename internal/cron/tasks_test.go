package cron

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	store := NewTaskStore(filepath.Join(dir, "tasks.json"))

	task := Task{
		ID:        "test-1",
		Cron:      "*/5 * * * *",
		Prompt:    "check deploy",
		CreatedAt: time.Now().UnixMilli(),
		Recurring: true,
	}

	if err := store.Add(task); err != nil {
		t.Fatalf("Add error: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 task, got %d", len(list))
	}

	if err := store.Remove("test-1"); err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	list, _ = store.List()
	if len(list) != 0 {
		t.Fatalf("expected 0 tasks after remove, got %d", len(list))
	}
}

func TestTaskStore_MaxJobs(t *testing.T) {
	dir := t.TempDir()
	store := NewTaskStore(filepath.Join(dir, "tasks.json"))

	for i := 0; i < MaxJobs; i++ {
		store.Add(Task{ID: fmt.Sprintf("t%d", i), Cron: "* * * * *", Prompt: "x"})
	}

	err := store.Add(Task{ID: "overflow", Cron: "* * * * *", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error when exceeding max jobs")
	}
}

func TestJitteredNextRun_recurring(t *testing.T) {
	ce, _ := ParseCronExpression("*/5 * * * *")
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	jittered, err := JitteredNextRun(ce, "task-1", now, true)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	base, _ := ce.NextRun(now)
	if !jittered.After(base) && !jittered.Equal(base) {
		t.Fatalf("jittered %v should be >= base %v", jittered, base)
	}
	maxJittered := base.Add(15 * time.Minute)
	if jittered.After(maxJittered) {
		t.Fatalf("jittered %v exceeds cap %v", jittered, maxJittered)
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now().UnixMilli()
	old := now - (8 * 24 * 60 * 60 * 1000)

	task := Task{
		CreatedAt: old,
		Recurring: true,
	}
	if !IsExpired(task, now) {
		t.Fatal("expected expired task")
	}

	task.CreatedAt = now - (6 * 24 * 60 * 60 * 1000)
	if IsExpired(task, now) {
		t.Fatal("expected non-expired task")
	}
}
