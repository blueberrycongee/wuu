package cron

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler_fireOneShot(t *testing.T) {
	store := NewTaskStore(filepath.Join(t.TempDir(), "tasks.json"))

	var fired atomic.Int32
	onFire := func(prompt string) {
		fired.Add(1)
	}

	s := NewScheduler(SchedulerConfig{
		Store:   store,
		OnFire:  onFire,
		IsOwner: func() bool { return true },
	})

	task := Task{
		ID:        "oneshot-1",
		Cron:      "* * * * *",
		Prompt:    "hello",
		CreatedAt: time.Now().Add(-2 * time.Minute).UnixMilli(),
		Recurring: false,
	}
	store.Add(task)

	s.Start()
	defer s.Stop()

	s.check()

	if fired.Load() != 1 {
		t.Fatalf("expected 1 fire, got %d", fired.Load())
	}

	tasks, _ := store.List()
	if len(tasks) != 0 {
		t.Fatalf("expected task removed after one-shot fire, got %d", len(tasks))
	}
}

func TestScheduler_recurringUpdatesLastFired(t *testing.T) {
	store := NewTaskStore(filepath.Join(t.TempDir(), "tasks.json"))

	var fired atomic.Int32
	s := NewScheduler(SchedulerConfig{
		Store:   store,
		OnFire:  func(string) { fired.Add(1) },
		IsOwner: func() bool { return true },
	})

	task := Task{
		ID:        "rec-1",
		Cron:      "* * * * *",
		Prompt:    "ping",
		CreatedAt: time.Now().Add(-2 * time.Minute).UnixMilli(),
		Recurring: true,
	}
	store.Add(task)

	s.Start()
	defer s.Stop()
	s.check()

	if fired.Load() != 1 {
		t.Fatalf("expected 1 fire, got %d", fired.Load())
	}

	tasks, _ := store.List()
	if len(tasks) != 1 {
		t.Fatalf("expected task to remain, got %d", len(tasks))
	}
	if tasks[0].LastFiredAt == 0 {
		t.Fatal("expected LastFiredAt to be updated")
	}
}
