package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type OwnerKind string
type Lifecycle string
type Status string
type EventType string

const (
	EventStarted   EventType = "started"
	EventFailed    EventType = "failed"
	EventStopped   EventType = "stopped"
	EventCleanedUp EventType = "cleaned_up"

	OwnerMainAgent OwnerKind = "main_agent"
	OwnerSubagent  OwnerKind = "subagent"

	LifecycleSession Lifecycle = "session"
	LifecycleManaged Lifecycle = "managed"

	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
)

type Process struct {
	ID        string    `json:"id"`
	OwnerKind OwnerKind `json:"owner_kind"`
	OwnerID   string    `json:"owner_id"`
	Lifecycle Lifecycle `json:"lifecycle"`
	Status    Status    `json:"status"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	LogPath   string    `json:"log_path"`
	Command   string    `json:"command"`
	CWD       string    `json:"cwd"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	ExitCode  int       `json:"exit_code,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

type StartOptions struct {
	Command   string
	CWD       string
	OwnerKind OwnerKind
	OwnerID   string
	Lifecycle Lifecycle
}

type Event struct {
	Type    EventType
	Process Process
}

type CleanupResult struct {
	Cleaned []Process
}

type Manager struct {
	rootDir     string
	registryDir string
	logDir      string
	mu          sync.Mutex
	subMu       sync.Mutex
	subscribers []chan<- Event
}

func NewManager(rootDir string) (*Manager, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("root directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	m := &Manager{rootDir: abs, registryDir: filepath.Join(abs, ".wuu", "runtime", "processes"), logDir: filepath.Join(abs, ".wuu", "runtime", "logs")}
	if err := os.MkdirAll(m.registryDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Start(ctx context.Context, opt StartOptions) (*Process, error) {
	if strings.TrimSpace(opt.Command) == "" {
		return nil, errors.New("command is required")
	}
	if opt.OwnerKind != OwnerMainAgent && opt.OwnerKind != OwnerSubagent {
		return nil, errors.New("owner_kind must be main_agent or subagent")
	}
	if opt.Lifecycle == "" {
		opt.Lifecycle = LifecycleSession
	}
	if opt.Lifecycle != LifecycleSession && opt.Lifecycle != LifecycleManaged {
		return nil, errors.New("lifecycle must be session or managed")
	}
	cwd := opt.CWD
	if strings.TrimSpace(cwd) == "" {
		cwd = m.rootDir
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(m.rootDir, cwd)
	}
	cwd, _ = filepath.Abs(cwd)
	id := "proc-" + randomHex(4)
	p := &Process{ID: id, OwnerKind: opt.OwnerKind, OwnerID: opt.OwnerID, Lifecycle: opt.Lifecycle, Status: StatusStarting, Command: opt.Command, CWD: cwd, LogPath: filepath.Join(m.logDir, id+".log"), StartedAt: time.Now(), UpdatedAt: time.Now(), ExitCode: -1}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.save(p); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(p.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		p.Status = StatusFailed
		p.LastError = err.Error()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, err
	}
	if err := ctx.Err(); err != nil {
		_ = logf.Close()
		p.Status = StatusFailed
		p.LastError = err.Error()
		p.UpdatedAt = time.Now()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, err
	}
	cmd := exec.Command("bash", "-lc", opt.Command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		p.Status = StatusFailed
		p.LastError = err.Error()
		p.UpdatedAt = time.Now()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, fmt.Errorf("start process: %w", err)
	}
	p.PID = cmd.Process.Pid
	if pgid, err := syscall.Getpgid(p.PID); err == nil {
		p.PGID = pgid
	} else {
		p.PGID = p.PID
	}
	p.Status = StatusRunning
	p.UpdatedAt = time.Now()
	_ = m.save(p)
	m.publish(Event{Type: EventStarted, Process: *p})
	go m.wait(id, cmd, logf)
	return p, nil
}

func (m *Manager) wait(id string, cmd *exec.Cmd, logf *os.File) {
	err := cmd.Wait()
	_ = logf.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, rerr := m.load(id)
	if rerr != nil {
		return
	}
	if p.Status == StatusStopping {
		p.Status = StatusStopped
	} else if err != nil {
		p.Status = StatusFailed
		p.LastError = err.Error()
	} else {
		p.Status = StatusStopped
	}
	if cmd.ProcessState != nil {
		p.ExitCode = cmd.ProcessState.ExitCode()
	}
	p.StoppedAt = time.Now()
	p.UpdatedAt = time.Now()
	_ = m.save(p)
	eventType := EventStopped
	if p.Status == StatusFailed {
		eventType = EventFailed
	}
	m.publish(Event{Type: eventType, Process: *p})
}

func (m *Manager) List() ([]Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(m.registryDir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := []Process{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil {
			var p Process
			if json.Unmarshal(b, &p) == nil {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

func (m *Manager) ReadOutput(id string, maxBytes int) (string, bool, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024
	}
	p, err := m.load(id)
	if err != nil {
		return "", false, err
	}
	f, err := os.Open(p.LogPath)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, _ := f.Stat()
	size := info.Size()
	start := int64(0)
	truncated := false
	if size > int64(maxBytes) {
		start = size - int64(maxBytes)
		truncated = true
	}
	_, _ = f.Seek(start, 0)
	b, err := io.ReadAll(f)
	return string(b), truncated, err
}

func (m *Manager) Stop(id string) (*Process, error) {
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if p.Status == StatusStopped || p.Status == StatusFailed {
		m.mu.Unlock()
		return p, nil
	}
	p.Status = StatusStopping
	p.UpdatedAt = time.Now()
	_ = m.save(p)
	m.mu.Unlock()
	pgid := p.PGID
	if pgid == 0 {
		pgid = p.PID
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := m.load(id)
		if cur.Status == StatusStopped || cur.Status == StatusFailed {
			return cur, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	cur, _ := m.load(id)
	return cur, nil
}

func (m *Manager) CleanupSession() error {
	_, err := m.CleanupSessionWithResult()
	return err
}

func (m *Manager) CleanupSessionWithResult() (CleanupResult, error) {
	result := CleanupResult{}
	list, err := m.List()
	if err != nil {
		return result, err
	}
	for _, p := range list {
		if p.Lifecycle == LifecycleSession && (p.Status == StatusRunning || p.Status == StatusStarting || p.Status == StatusStopping) {
			stopped, err := m.Stop(p.ID)
			if err != nil {
				return result, err
			}
			if stopped != nil {
				result.Cleaned = append(result.Cleaned, *stopped)
				m.publish(Event{Type: EventCleanedUp, Process: *stopped})
			}
		}
	}
	return result, nil
}

func (m *Manager) Subscribe(ch chan<- Event) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subscribers = append(m.subscribers, ch)
}

func (m *Manager) publish(event Event) {
	m.subMu.Lock()
	subs := append([]chan<- Event(nil), m.subscribers...)
	m.subMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (m *Manager) load(id string) (*Process, error) {
	b, err := os.ReadFile(filepath.Join(m.registryDir, id+".json"))
	if err != nil {
		return nil, err
	}
	var p Process
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
func (m *Manager) save(p *Process) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.registryDir, p.ID+".json"), append(b, '\n'), 0o644)
}
func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }
