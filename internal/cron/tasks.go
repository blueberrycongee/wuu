package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	MaxJobs            = 50
	RecurringMaxAge    = 7 * 24 * time.Hour
	RecurringJitterCap = 15 * time.Minute
)

type Task struct {
	ID          string `json:"id"`
	Cron        string `json:"cron"`
	Prompt      string `json:"prompt"`
	CreatedAt   int64  `json:"createdAt"`
	LastFiredAt int64  `json:"lastFiredAt,omitempty"`
	Recurring   bool   `json:"recurring"`
}

func (t Task) NextFireAt() (time.Time, error) {
	ce, err := ParseCronExpression(t.Cron)
	if err != nil {
		return time.Time{}, err
	}
	anchor := time.Now()
	if t.LastFiredAt > 0 {
		anchor = time.UnixMilli(t.LastFiredAt)
	} else if t.CreatedAt > 0 {
		anchor = time.UnixMilli(t.CreatedAt)
	}
	return JitteredNextRun(ce, t.ID, anchor, t.Recurring)
}

type TaskStore struct {
	path string
}

func NewTaskStore(path string) *TaskStore {
	return &TaskStore{path: path}
}

func (s *TaskStore) load() ([]Task, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var wrapper struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse tasks file: %w", err)
	}
	return wrapper.Tasks, nil
}

func (s *TaskStore) save(tasks []Task) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	wrapper := struct {
		Tasks []Task `json:"tasks"`
	}{Tasks: tasks}
	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}

func (s *TaskStore) List() ([]Task, error) {
	return s.load()
}

func (s *TaskStore) Add(task Task) error {
	tasks, err := s.load()
	if err != nil {
		return err
	}
	if len(tasks) >= MaxJobs {
		return fmt.Errorf("maximum number of scheduled tasks reached (%d)", MaxJobs)
	}
	tasks = append(tasks, task)
	return s.save(tasks)
}

func (s *TaskStore) Remove(ids ...string) error {
	tasks, err := s.load()
	if err != nil {
		return err
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	filtered := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if _, ok := idSet[t.ID]; !ok {
			filtered = append(filtered, t)
		}
	}
	return s.save(filtered)
}

func (s *TaskStore) UpdateLastFired(ids []string, firedAt int64) error {
	tasks, err := s.load()
	if err != nil {
		return err
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	for i := range tasks {
		if _, ok := idSet[tasks[i].ID]; ok {
			tasks[i].LastFiredAt = firedAt
		}
	}
	return s.save(tasks)
}

func GenerateTaskID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func JitteredNextRun(ce CronExpression, taskID string, anchor time.Time, recurring bool) (time.Time, error) {
	next, err := ce.NextRun(anchor)
	if err != nil {
		return time.Time{}, err
	}
	if recurring {
		interval := next.Sub(anchor)
		if interval <= 0 {
			interval = time.Minute
		}
		maxJitter := interval / 10
		if maxJitter > RecurringJitterCap {
			maxJitter = RecurringJitterCap
		}
		jitter := deterministicJitter(taskID, maxJitter)
		next = next.Add(jitter)
	} else {
		if next.Minute()%30 == 0 && next.Second() == 0 {
			jitter := deterministicJitter(taskID, 90*time.Second)
			next = next.Add(-jitter)
			if next.Before(anchor) {
				next = anchor.Add(time.Second)
			}
		}
	}
	return next, nil
}

func deterministicJitter(seed string, max time.Duration) time.Duration {
	h := 0
	for i := 0; i < len(seed); i++ {
		h = h*31 + int(seed[i])
		if h < 0 {
			h = -h
		}
	}
	if max <= 0 {
		return 0
	}
	return time.Duration(h) % max
}

func IsExpired(task Task, nowMillis int64) bool {
	if !task.Recurring {
		return false
	}
	age := time.Duration(nowMillis-task.CreatedAt) * time.Millisecond
	return age > RecurringMaxAge
}

func FindMissedOneShots(tasks []Task, now time.Time) []Task {
	var missed []Task
	for _, t := range tasks {
		if t.Recurring {
			continue
		}
		ce, err := ParseCronExpression(t.Cron)
		if err != nil {
			continue
		}
		anchor := time.UnixMilli(t.CreatedAt)
		next, err := ce.NextRun(anchor)
		if err != nil {
			continue
		}
		if next.Before(now) {
			missed = append(missed, t)
		}
	}
	return missed
}
