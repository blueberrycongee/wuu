package cron

import (
	"sync"
	"time"
)

type SchedulerConfig struct {
	Store    *TaskStore
	OnFire   func(prompt string)
	IsOwner  func() bool
	IsKilled func() bool
}

type Scheduler struct {
	cfg      SchedulerConfig
	ticker   *time.Ticker
	stopCh   chan struct{}
	wg       sync.WaitGroup
	inFlight map[string]struct{}
	mu       sync.Mutex
}

func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.IsKilled == nil {
		cfg.IsKilled = func() bool { return false }
	}
	return &Scheduler{
		cfg:      cfg,
		stopCh:   make(chan struct{}),
		inFlight: make(map[string]struct{}),
	}
}

func (s *Scheduler) Start() {
	s.ticker = time.NewTicker(time.Second)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.ticker.C:
				if s.cfg.IsKilled() {
					continue
				}
				s.check()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Scheduler) check() {
	if s.cfg.IsOwner != nil && !s.cfg.IsOwner() {
		return
	}

	tasks, err := s.cfg.Store.List()
	if err != nil {
		return
	}

	now := time.Now()
	var toUpdate []string
	var toRemove []string

	for _, task := range tasks {
		if s.cfg.IsKilled() {
			return
		}

		s.mu.Lock()
		if _, busy := s.inFlight[task.ID]; busy {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		if task.Recurring && IsExpired(task, now.UnixMilli()) {
			toRemove = append(toRemove, task.ID)
			continue
		}

		next, err := task.NextFireAt()
		if err != nil {
			continue
		}

		if now.Before(next) {
			continue
		}

		s.mu.Lock()
		s.inFlight[task.ID] = struct{}{}
		s.mu.Unlock()

		go func(t Task) {
			defer func() {
				s.mu.Lock()
				delete(s.inFlight, t.ID)
				s.mu.Unlock()
			}()
			if s.cfg.OnFire != nil {
				s.cfg.OnFire(t.Prompt)
			}
		}(task)

		if task.Recurring {
			toUpdate = append(toUpdate, task.ID)
		} else {
			toRemove = append(toRemove, task.ID)
		}
	}

	if len(toUpdate) > 0 {
		s.cfg.Store.UpdateLastFired(toUpdate, now.UnixMilli())
	}
	if len(toRemove) > 0 {
		s.cfg.Store.Remove(toRemove...)
	}
}

func (s *Scheduler) GetNextFireTime() time.Time {
	tasks, err := s.cfg.Store.List()
	if err != nil {
		return time.Time{}
	}
	var earliest time.Time
	for _, task := range tasks {
		next, err := task.NextFireAt()
		if err != nil {
			continue
		}
		if earliest.IsZero() || next.Before(earliest) {
			earliest = next
		}
	}
	return earliest
}
