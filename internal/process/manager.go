package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/shellpath"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type OwnerKind string
type Lifecycle string
type Status string
type EventType string
type EventCause string
type CompletionMode string

const (
	EventStarted   EventType = "started"
	EventUpdated   EventType = "updated"
	EventFailed    EventType = "failed"
	EventStopped   EventType = "stopped"
	EventCleanedUp EventType = "cleaned_up"
	// EventRecheckDue marks a scheduled progress recheck firing for a live
	// process. It is a low-latency hint only; consumers pull the persisted
	// obligation through PendingRechecks.
	EventRecheckDue EventType = "recheck_due"

	EventCauseNaturalExit   EventCause = "natural_exit"
	EventCauseRequestedStop EventCause = "requested_stop"

	OwnerMainAgent OwnerKind = "main_agent"
	OwnerSubagent  OwnerKind = "subagent"

	LifecycleSession Lifecycle = "session"
	LifecycleManaged Lifecycle = "managed"

	CompletionModeResume   CompletionMode = "resume"
	CompletionModeDetached CompletionMode = "detached"

	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
)

type Process struct {
	Action    string    `json:"action,omitempty"`
	ID        string    `json:"id"`
	OwnerKind OwnerKind `json:"owner_kind"`
	OwnerID   string    `json:"owner_id"`
	// RootThreadID is the conversation that owns this command. It is stamped
	// at start from host state, never derived afterwards: once a thread or
	// subagent is gone there is nothing left to scan to recover the owner. This
	// step only persists the association; lifecycle cleanup is not implemented.
	RootThreadID string `json:"root_thread_id,omitempty"`
	// HostGenerationID identifies the top-level app-server host lifetime that
	// started this command. It is durable record identity, not proof that the
	// running process still has an in-memory control handle. Records written
	// before this field existed have it empty.
	HostGenerationID string `json:"host_generation_id,omitempty"`
	// Deprecated: Lifecycle is being retired with the managed process class.
	// It still parses and round-trips so existing registry records keep
	// loading; new behavior must not branch on it.
	Lifecycle        Lifecycle      `json:"lifecycle"`
	CompletionMode   CompletionMode `json:"completion_mode,omitempty"`
	Status           Status         `json:"status"`
	PID              int            `json:"pid"`
	PGID             int            `json:"pgid"`
	ProcessStartTime string         `json:"process_start_time,omitempty"`
	TTY              bool           `json:"tty,omitempty"`
	LogPath          string         `json:"log_path"`
	// LogBaseOffset is the logical offset represented by byte zero of LogPath.
	// Terminal log compaction advances it so continuation offsets issued before
	// compaction resume at the retained tail or receive the compaction marker.
	LogBaseOffset                  int64               `json:"log_base_offset,omitempty"`
	Command                        string              `json:"command"`
	CWD                            string              `json:"cwd"`
	StartedAt                      time.Time           `json:"started_at"`
	UpdatedAt                      time.Time           `json:"updated_at"`
	StoppedAt                      time.Time           `json:"stopped_at,omitempty"`
	ExitCode                       int                 `json:"exit_code,omitempty"`
	LastError                      string              `json:"last_error,omitempty"`
	SandboxMode                    processsandbox.Mode `json:"sandbox_mode,omitempty"`
	SandboxDenied                  bool                `json:"sandbox_denied,omitempty"`
	SandboxRunnerFailed            bool                `json:"sandbox_runner_failed,omitempty"`
	SandboxDenialSignatures        []string            `json:"sandbox_denial_signatures,omitempty"`
	SandboxRunnerFailureSignatures []string            `json:"sandbox_runner_failure_signatures,omitempty"`
	TerminalCause                  EventCause          `json:"terminal_cause,omitempty"`
	CompletionDeliveredAt          time.Time           `json:"completion_delivered_at,omitempty"`
	CompletionConsumedBy           string              `json:"completion_consumed_by,omitempty"`
	// RecheckMinutes is the model-declared progress recheck interval. Zero
	// disables scheduled rechecks. NextRecheckAt is the persisted deadline of
	// the next scheduled wake; PendingRecheckAt marks a fired recheck that has
	// not been delivered to the owning thread yet. Unlike the one-shot
	// completion obligation, a lost recheck is self-healing: the next interval
	// fires again while the process stays live.
	RecheckMinutes   int       `json:"recheck_minutes,omitempty"`
	NextRecheckAt    time.Time `json:"next_recheck_at,omitempty"`
	PendingRecheckAt time.Time `json:"pending_recheck_at,omitempty"`
}

type StartOptions struct {
	Command       string
	CommandPrefix string
	CWD           string
	// WorkspaceRoot is the host-owned root of the calling session. Shared
	// managers use it to resolve this launch without moving another session's
	// default directory or confinement boundary.
	WorkspaceRoot string
	OwnerKind     OwnerKind
	OwnerID       string
	// RootThreadID is host-supplied. Callers pass the conversation the command
	// belongs to; it is not a model-declared argument.
	RootThreadID string
	// Deprecated: see Process.Lifecycle.
	Lifecycle             Lifecycle
	CompletionMode        CompletionMode
	TTY                   bool
	AllowOutsideWorkspace bool
	RecheckMinutes        int
	// Env overrides the inherited environment when non-nil. Callers use this
	// to pin sandboxed jobs to their private writable temporary directory.
	Env []string
	// SandboxPolicy confines this process and every descendant to the caller's
	// preselected filesystem boundary. Nil preserves the platform's existing
	// unsandboxed behavior (including explicit unconfined mode).
	SandboxPolicy *processsandbox.Policy
	// SandboxProvider replaces the built-in backend for this execution. It is
	// ignored when SandboxPolicy is nil.
	SandboxProvider processsandbox.Provider
}

type Event struct {
	Type    EventType
	Cause   EventCause
	Process Process
}

type CleanupResult struct {
	Cleaned []Process
}

type OutputReadOptions struct {
	MaxBytes    int
	OffsetBytes *int64
	Wait        time.Duration
	// MinDwell paces output-driven early returns: new output releases the
	// wait only once the call has dwelt at least this long, so a
	// continuously producing process cannot bounce the caller into a tight
	// return loop. Process exit and the Wait deadline still return
	// immediately. Zero preserves the original first-output behavior.
	MinDwell time.Duration
}

type OutputSnapshot struct {
	Process     Process
	Output      string
	Truncated   bool
	StartOffset int64
	EndOffset   int64
	TotalBytes  int64
	TimedOut    bool
	Duration    time.Duration
}

type Manager struct {
	rootDir string
	// hostGenerationID is shared by Managers belonging to one top-level
	// app-server lifetime and stamped on every command they start. The handles
	// map, not this label, is the authority for live in-memory control.
	hostGenerationID string
	registryDir      string
	logDir           string
	mu               sync.Mutex
	subMu            sync.Mutex
	handles          map[string]*processHandle
	subscribers      []chan<- Event
	recheckWake      chan struct{}
}

// newHostGenerationID mints an identifier for one app-server lifetime. Time
// alone would collide across a fast restart, so it is paired with random bytes.
func newHostGenerationID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("host-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("host-%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf))
}

// HostGenerationID returns the durable identity label for this manager's host
// lifetime. Matching this value does not establish live controllability.
func (m *Manager) HostGenerationID() string {
	if m == nil {
		return ""
	}
	return m.hostGenerationID
}

type processHandle struct {
	mu      sync.Mutex
	stdin   io.WriteCloser
	ptyFile *os.File
	done    chan struct{}
}

func NewManager(rootDir string, runtimeDirs ...string) (*Manager, error) {
	return NewManagerWithHostGeneration(rootDir, newHostGenerationID(), runtimeDirs...)
}

// NewManagerWithHostGeneration creates a manager using an existing top-level
// host lifetime label. Runtime sessions use this for thread-local managers so
// records from one app-server host share durable identity; standalone callers
// should use NewManager, which mints its own label for compatibility.
func NewManagerWithHostGeneration(rootDir, hostGenerationID string, runtimeDirs ...string) (*Manager, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("root directory is required")
	}
	hostGenerationID = strings.TrimSpace(hostGenerationID)
	if hostGenerationID == "" {
		return nil, errors.New("host generation id is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	runtimeDir := ""
	if len(runtimeDirs) > 0 {
		runtimeDir = strings.TrimSpace(runtimeDirs[0])
	}
	if runtimeDir == "" {
		wuuHome, err := statepath.Home("")
		if err != nil {
			return nil, err
		}
		workspaceStateDir, err := statepath.WorkspaceDir(wuuHome, abs)
		if err != nil {
			return nil, err
		}
		runtimeDir = statepath.RuntimeDir(workspaceStateDir)
	}
	runtimeDir, err = filepath.Abs(runtimeDir)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		rootDir:          abs,
		hostGenerationID: hostGenerationID,
		registryDir:      filepath.Join(runtimeDir, "processes"),
		logDir:           filepath.Join(runtimeDir, "logs"),
		handles:          make(map[string]*processHandle),
		recheckWake:      make(chan struct{}, 1),
	}
	if err := ensurePrivateProcessDir(m.registryDir); err != nil {
		return nil, err
	}
	if err := ensurePrivateProcessDir(m.logDir); err != nil {
		return nil, err
	}
	if err := m.resumePersistedManagedProcesses(); err != nil {
		return nil, err
	}
	if err := m.maintainTerminalStorage(time.Now()); err != nil {
		log.Printf("wuu: process storage maintenance: %v", err)
	}
	go m.recheckScheduler()
	return m, nil
}

func ensurePrivateProcessDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("process runtime path is not a real directory: %s", path)
	}
	return os.Chmod(path, 0o700)
}

// SetRootDir changes the default working directory and confinement root for
// future process launches without discarding handles for existing processes.
func (m *Manager) SetRootDir(rootDir string) {
	if m == nil {
		return
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return
	}
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}
	m.mu.Lock()
	m.rootDir = filepath.Clean(rootDir)
	m.mu.Unlock()
}

// Start is the low-level manager launch path. Model-facing command tools must
// bind a SessionID before using it; internal callers may use it for legitimate
// non-thread work and provide RootThreadID when they own that association.
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
	if opt.CompletionMode == "" {
		opt.CompletionMode = CompletionModeResume
	}
	if opt.CompletionMode != CompletionModeResume && opt.CompletionMode != CompletionModeDetached {
		return nil, errors.New("completion_mode must be resume or detached")
	}
	if opt.RecheckMinutes < 0 || opt.RecheckMinutes > MaxRecheckMinutes {
		return nil, fmt.Errorf("recheck_minutes must be between 0 and %d", MaxRecheckMinutes)
	}
	m.mu.Lock()
	rootDir := m.rootDir
	m.mu.Unlock()
	if root := strings.TrimSpace(opt.WorkspaceRoot); root != "" {
		rootDir = root
	}
	cwd, err := resolveStartCWD(rootDir, opt.CWD, opt.AllowOutsideWorkspace)
	if err != nil {
		return nil, err
	}
	if opt.TTY && !ptySupported() {
		// Degrade to pipe mode rather than failing the start: tty is a
		// fidelity feature, and platforms without pty support (Windows)
		// still run the process fine. The record keeps TTY=false so
		// readers see the mode that actually ran.
		opt.TTY = false
	}
	id := "proc-" + randomHex(4)
	p := &Process{ID: id, OwnerKind: opt.OwnerKind, OwnerID: opt.OwnerID, RootThreadID: strings.TrimSpace(opt.RootThreadID), HostGenerationID: m.hostGenerationID, Lifecycle: opt.Lifecycle, CompletionMode: opt.CompletionMode, Status: StatusStarting, Command: opt.Command, CWD: cwd, TTY: opt.TTY, LogPath: filepath.Join(m.logDir, id+".log"), StartedAt: time.Now(), UpdatedAt: time.Now(), ExitCode: -1, RecheckMinutes: opt.RecheckMinutes}
	if opt.SandboxPolicy != nil {
		p.SandboxMode = opt.SandboxPolicy.Mode
	}
	if opt.RecheckMinutes > 0 {
		p.NextRecheckAt = p.StartedAt.Add(time.Duration(opt.RecheckMinutes) * time.Minute)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.save(p); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(p.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		p.Status = StatusFailed
		p.LastError = err.Error()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, err
	}
	if err := logf.Chmod(0o600); err != nil {
		_ = logf.Close()
		p.Status = StatusFailed
		p.LastError = err.Error()
		p.UpdatedAt = time.Now()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, fmt.Errorf("secure process log: %w", err)
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
	cmd, err := managedCommand(opt.Command, cwd, opt.CommandPrefix, opt.Env)
	if err != nil {
		_ = logf.Close()
		p.Status = StatusFailed
		p.LastError = err.Error()
		p.UpdatedAt = time.Now()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, err
	}
	if opt.SandboxPolicy != nil {
		classifier, confineErr := processsandbox.ApplyWithProvider(ctx, cmd, *opt.SandboxPolicy, opt.SandboxProvider)
		if confineErr != nil {
			_ = logf.Close()
			p.Status = StatusFailed
			p.LastError = confineErr.Error()
			p.UpdatedAt = time.Now()
			_ = m.save(p)
			m.publish(Event{Type: EventFailed, Process: *p})
			return p, fmt.Errorf("prepare filesystem process sandbox: %w", confineErr)
		}
		p.SandboxDenialSignatures = classifier.DenialSignatures
		p.SandboxRunnerFailureSignatures = classifier.RunnerFailureSignatures
	}
	if opt.TTY {
		cmd.Env = terminalCommandEnv(cmd.Env)
	}
	var stdin io.WriteCloser
	var ptyFile *os.File
	if opt.TTY {
		ptyFile, err = startPTYProcess(cmd)
		if err != nil {
			_ = logf.Close()
			p.Status = StatusFailed
			p.LastError = err.Error()
			p.UpdatedAt = time.Now()
			_ = m.save(p)
			m.publish(Event{Type: EventFailed, Process: *p})
			return p, fmt.Errorf("start pty process: %w", err)
		}
		stdin = ptyFile
	} else {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			_ = logf.Close()
			p.Status = StatusFailed
			p.LastError = err.Error()
			p.UpdatedAt = time.Now()
			_ = m.save(p)
			m.publish(Event{Type: EventFailed, Process: *p})
			return p, fmt.Errorf("stdin pipe: %w", err)
		}
		cmd.Stdout = logf
		cmd.Stderr = logf
		PrepareCommand(cmd)
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = logf.Close()
			p.Status = StatusFailed
			p.LastError = err.Error()
			p.UpdatedAt = time.Now()
			_ = m.save(p)
			m.publish(Event{Type: EventFailed, Process: *p})
			return p, fmt.Errorf("start process: %w", err)
		}
	}
	p.PID = cmd.Process.Pid
	p.PGID = ProcessTreeForPID(p.PID).ID()
	p.ProcessStartTime, _, _, err = readProcessIdentity(p.PID)
	if err != nil {
		if p.PGID > 1 {
			_ = ProcessTreeFromID(p.PGID).Kill()
		}
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = cmd.Wait()
		_ = logf.Close()
		p.Status = StatusFailed
		p.LastError = err.Error()
		p.UpdatedAt = time.Now()
		_ = m.save(p)
		m.publish(Event{Type: EventFailed, Process: *p})
		return p, fmt.Errorf("record process identity: %w", err)
	}
	p.Status = StatusRunning
	p.UpdatedAt = time.Now()
	_ = m.save(p)
	handle := &processHandle{stdin: stdin, ptyFile: ptyFile, done: make(chan struct{})}
	m.handles[id] = handle
	m.publish(Event{Type: EventStarted, Process: *p})
	if opt.TTY {
		go func() {
			defer close(handle.done)
			m.waitPTY(id, cmd, logf, ptyFile)
		}()
	} else {
		go func() {
			defer close(handle.done)
			m.wait(id, cmd, logf)
		}()
	}
	if p.RecheckMinutes > 0 {
		m.signalRecheckScheduler()
	}
	return p, nil
}

func resolveStartCWD(rootDir, cwd string, allowOutsideWorkspace bool) (string, error) {
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root %q: %w", rootDir, err)
	}
	if evaluatedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = evaluatedRoot
	}
	workDir := strings.TrimSpace(cwd)
	if workDir == "" {
		workDir = root
	} else if !filepath.IsAbs(workDir) {
		workDir = filepath.Join(root, workDir)
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", workDir, err)
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("working directory %q does not exist", abs)
		}
		return "", fmt.Errorf("inspect working directory %q: %w", abs, err)
	}
	info, err := os.Stat(evaluated)
	if err != nil {
		return "", fmt.Errorf("inspect working directory %q: %w", evaluated, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", evaluated)
	}
	if allowOutsideWorkspace {
		return evaluated, nil
	}
	cmpRoot, cmpDir := root, evaluated
	if runtime.GOOS == "windows" {
		// Windows filesystems compare names case-insensitively.
		cmpRoot, cmpDir = strings.ToLower(cmpRoot), strings.ToLower(cmpDir)
	}
	rel, err := filepath.Rel(cmpRoot, cmpDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q against workspace root %q: %w", evaluated, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working directory %q escapes workspace %q", evaluated, root)
	}
	return evaluated, nil
}

func managedCommand(command, cwd, commandPrefix string, envs ...[]string) (*exec.Cmd, error) {
	shell, err := shellpath.LoginBash()
	if err != nil {
		return nil, err
	}
	command = shellpath.NormalizeBashCommand(command)
	if commandPrefix != "" {
		command = commandPrefix + command
	}
	cmd := exec.Command(shell.Path, shell.CommandArgs(command)...)
	cmd.Dir = cwd
	var env []string
	if len(envs) > 0 {
		env = envs[0]
	}
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = shellpath.CommandEnv(env)
	return cmd, nil
}

func terminalCommandEnv(env []string) []string {
	terminalValues := map[string]string{
		"CLICOLOR":    "1",
		"COLORTERM":   "truecolor",
		"FORCE_COLOR": "1",
		"TERM":        "xterm-256color",
	}
	result := make([]string, 0, len(env)+len(terminalValues))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, terminalVariable := terminalValues[strings.ToUpper(name)]; terminalVariable {
				continue
			}
		}
		result = append(result, entry)
	}
	for _, name := range []string{"CLICOLOR", "COLORTERM", "FORCE_COLOR", "TERM"} {
		result = append(result, name+"="+terminalValues[name])
	}
	return result
}

func (m *Manager) wait(id string, cmd *exec.Cmd, logf *os.File) {
	err := cmd.Wait()
	_ = logf.Close()
	_, discarded, _ := compactProcessLog(filepath.Join(m.logDir, id+".log"), terminalProcessLogMaxBytes)
	m.finishWait(id, cmd, err, discarded)
}

func (m *Manager) waitPTY(id string, cmd *exec.Cmd, logf *os.File, ptyFile *os.File) {
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(logf, ptyFile)
		close(copyDone)
	}()
	err := cmd.Wait()
	_ = ptyFile.Close()
	<-copyDone
	_ = logf.Close()
	_, discarded, _ := compactProcessLog(filepath.Join(m.logDir, id+".log"), terminalProcessLogMaxBytes)
	m.finishWait(id, cmd, err, discarded)
}

func (m *Manager) finishWait(id string, cmd *exec.Cmd, err error, discardedLogBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.handles, id)
	p, rerr := m.load(id)
	if rerr != nil {
		return
	}
	if discardedLogBytes > 0 {
		p.LogBaseOffset += discardedLogBytes
	}
	alreadyTerminal := p.Status == StatusStopped || p.Status == StatusFailed
	requestedStop := p.Status == StatusStopping || p.Status == StatusStopped
	if p.Status == StatusStopping || p.Status == StatusStopped {
		p.Status = StatusStopped
	} else if p.Status == StatusFailed {
		// Preserve an earlier failure recorded by the manager.
	} else if err != nil {
		p.Status = StatusFailed
		p.LastError = err.Error()
	} else {
		p.Status = StatusStopped
	}
	if cmd.ProcessState != nil {
		p.ExitCode = cmd.ProcessState.ExitCode()
	}
	if p.SandboxMode != "" {
		if output, _, _, _, _, readErr := readLogWindow(p.LogPath, 8*1024, nil, p.LogBaseOffset); readErr == nil {
			classifier := processsandbox.ResultClassifier{DenialSignatures: p.SandboxDenialSignatures, RunnerFailureSignatures: p.SandboxRunnerFailureSignatures}
			p.SandboxRunnerFailed = classifier.IsRunnerFailure(p.ExitCode, output)
			p.SandboxDenied = !p.SandboxRunnerFailed && classifier.IsDenied(p.ExitCode, output)
			if p.SandboxRunnerFailed {
				p.LastError = "filesystem process sandbox runner failed; command did not run"
			}
		}
	}
	p.StoppedAt = time.Now()
	p.UpdatedAt = time.Now()
	_ = m.save(p)
	if alreadyTerminal {
		return
	}
	eventType := EventStopped
	if p.Status == StatusFailed {
		eventType = EventFailed
	}
	cause := EventCauseNaturalExit
	if requestedStop {
		cause = EventCauseRequestedStop
	}
	p.TerminalCause = cause
	_ = m.save(p)
	m.publish(Event{Type: eventType, Cause: cause, Process: *p})
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
		if err != nil {
			return nil, fmt.Errorf("read process record %q: %w", filepath.Base(f), err)
		}
		var p Process
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("decode process record %q: %w", filepath.Base(f), err)
		}
		wantID := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if p.ID != wantID {
			return nil, fmt.Errorf("process record %q contains mismatched id %q", filepath.Base(f), p.ID)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

func (m *Manager) Get(id string) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(id)
}

func (m *Manager) ReadOutput(id string, maxBytes int) (string, bool, error) {
	snapshot, err := m.ReadOutputSnapshot(context.Background(), id, OutputReadOptions{MaxBytes: maxBytes})
	if err != nil {
		return "", false, err
	}
	return snapshot.Output, snapshot.Truncated, nil
}

func (m *Manager) ReadOutputSnapshot(ctx context.Context, id string, opt OutputReadOptions) (OutputSnapshot, error) {
	started := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = 32 * 1024
	}
	if opt.Wait < 0 {
		opt.Wait = 0
	}
	p, err := m.load(id)
	if err != nil {
		return OutputSnapshot{}, err
	}
	timedOut := false
	if opt.Wait > 0 && opt.OffsetBytes != nil {
		deadline := time.Now().Add(opt.Wait)
		if opt.MinDwell < 0 {
			opt.MinDwell = 0
		}
		minDwellAt := started.Add(opt.MinDwell)
		for {
			size, sizeErr := fileSize(p.LogPath)
			if sizeErr != nil {
				return OutputSnapshot{}, sizeErr
			}
			offset := *opt.OffsetBytes
			if offset < 0 {
				offset = 0
			}
			// Process exit always releases immediately; new output releases
			// only after the minimum dwell so a continuously producing
			// process cannot bounce the caller faster than that pace.
			if !isLiveStatus(p.Status) {
				break
			}
			if p.LogBaseOffset+size > offset && !time.Now().Before(minDwellAt) {
				break
			}
			if !time.Now().Before(deadline) {
				timedOut = true
				break
			}
			wait := 50 * time.Millisecond
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
			select {
			case <-ctx.Done():
				return OutputSnapshot{}, ctx.Err()
			case <-time.After(wait):
			}
			if latest, loadErr := m.load(id); loadErr == nil {
				p = latest
			}
		}
	}
	if latest, loadErr := m.load(id); loadErr == nil {
		p = latest
	}
	output, truncated, startOffset, endOffset, totalBytes, err := readLogWindow(p.LogPath, opt.MaxBytes, opt.OffsetBytes, p.LogBaseOffset)
	if err != nil {
		return OutputSnapshot{}, err
	}
	return OutputSnapshot{
		Process:     *p,
		Output:      output,
		Truncated:   truncated,
		StartOffset: startOffset,
		EndOffset:   endOffset,
		TotalBytes:  totalBytes,
		TimedOut:    timedOut,
		Duration:    time.Since(started),
	}, nil
}

func readLogWindow(path string, maxBytes int, offsetBytes *int64, baseOffset ...int64) (string, bool, int64, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, 0, 0, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, 0, 0, 0, err
	}
	size := info.Size()
	base := int64(0)
	if len(baseOffset) > 0 && baseOffset[0] > 0 {
		base = baseOffset[0]
	}
	logicalSize := base + size
	end := logicalSize
	start := base
	truncated := false
	if offsetBytes != nil {
		start = *offsetBytes
		if start < base {
			start = base
		}
		if start > end {
			start = end
		}
		end = min(logicalSize, start+int64(maxBytes))
		if end < logicalSize {
			truncated = true
		}
	} else if size > int64(maxBytes) {
		start = logicalSize - int64(maxBytes)
		truncated = true
	}
	_, _ = f.Seek(start-base, 0)
	b, err := io.ReadAll(io.LimitReader(f, end-start))
	return string(b), truncated, start, end, logicalSize, err
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func isLiveStatus(status Status) bool {
	return status == StatusStarting || status == StatusRunning || status == StatusStopping
}

func (m *Manager) WriteStdin(id string, input string) (*Process, error) {
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if p.Status != StatusRunning && p.Status != StatusStarting {
		m.mu.Unlock()
		return p, fmt.Errorf("process %q is not running", id)
	}
	handle := m.handles[id]
	m.mu.Unlock()
	if handle == nil || handle.stdin == nil {
		return p, fmt.Errorf("stdin is not available for process %q", id)
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if _, err := io.WriteString(handle.stdin, input); err != nil {
		return p, fmt.Errorf("write stdin: %w", err)
	}
	return p, nil
}

func (m *Manager) InputAvailable(id string) bool {
	m.mu.Lock()
	handle := m.handles[id]
	m.mu.Unlock()
	return handle != nil && handle.stdin != nil
}

func (m *Manager) ResizeTTY(id string, cols, rows int) (*Process, error) {
	if cols < 1 || rows < 1 {
		return nil, errors.New("terminal size must be positive")
	}
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !p.TTY {
		m.mu.Unlock()
		return p, fmt.Errorf("process %q is not a tty", id)
	}
	if p.Status != StatusRunning && p.Status != StatusStarting {
		m.mu.Unlock()
		return p, fmt.Errorf("process %q is not running", id)
	}
	handle := m.handles[id]
	m.mu.Unlock()
	if handle == nil || handle.ptyFile == nil {
		return p, fmt.Errorf("tty is not attached for process %q", id)
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if err := resizePTY(handle.ptyFile, cols, rows); err != nil {
		return p, fmt.Errorf("resize tty: %w", err)
	}
	return p, nil
}

func (m *Manager) Stop(id string) (*Process, error) {
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	handle := m.handles[id]
	if p.Status == StatusStopped || p.Status == StatusFailed {
		m.mu.Unlock()
		waitForProcessMonitor(handle)
		return p, nil
	}
	running, err := processMatchesRecord(p)
	if err != nil {
		if errors.Is(err, errNoRecordedIdentity) {
			// The record predates persisted process identities, so the live
			// process can never be verified against it. Never signal the pid;
			// retire the record so reconciliation does not retry it forever.
			return m.retireUnverifiableRecordLocked(id, p, err)
		}
		m.mu.Unlock()
		return p, err
	}
	if !running {
		delete(m.handles, id)
		p.Status = StatusStopped
		p.StoppedAt = time.Now()
		p.UpdatedAt = p.StoppedAt
		p.TerminalCause = EventCauseRequestedStop
		if err := m.save(p); err != nil {
			m.mu.Unlock()
			return p, err
		}
		m.mu.Unlock()
		m.publish(Event{Type: EventStopped, Cause: EventCauseRequestedStop, Process: *p})
		waitForProcessMonitor(handle)
		return p, nil
	}
	p.Status = StatusStopping
	p.UpdatedAt = time.Now()
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return p, fmt.Errorf("persist stopping process: %w", err)
	}
	m.mu.Unlock()
	if err := ProcessTreeFromID(p.PGID).Terminate(); err != nil {
		return p, fmt.Errorf("terminate process group %d: %w", p.PGID, err)
	}
	cur, stopped, err := m.waitForStop(id, time.Now().Add(2*time.Second))
	if err != nil || stopped {
		if stopped {
			waitForProcessMonitor(handle)
		}
		return cur, err
	}

	running, err = processMatchesRecord(cur)
	if err != nil {
		return cur, err
	}
	if !running {
		cur, err := m.reconcileStopped(id)
		if err == nil {
			waitForProcessMonitor(handle)
		}
		return cur, err
	}
	if err := ProcessTreeFromID(cur.PGID).Kill(); err != nil {
		return cur, fmt.Errorf("kill process group %d: %w", cur.PGID, err)
	}
	cur, stopped, err = m.waitForStop(id, time.Now().Add(2*time.Second))
	if err != nil {
		return cur, err
	}
	if !stopped {
		return cur, fmt.Errorf("process group %d did not stop after SIGKILL", cur.PGID)
	}
	waitForProcessMonitor(handle)
	return cur, nil
}

func waitForProcessMonitor(handle *processHandle) {
	if handle == nil || handle.done == nil {
		return
	}
	<-handle.done
}

func (m *Manager) waitForStop(id string, deadline time.Time) (*Process, bool, error) {
	for {
		p, err := m.load(id)
		if err != nil {
			return nil, false, err
		}
		if p.Status == StatusStopped || p.Status == StatusFailed {
			return p, true, nil
		}
		if !processExists(p.PID) {
			stopped, err := m.reconcileStopped(id)
			return stopped, err == nil, err
		}
		if !time.Now().Before(deadline) {
			return p, false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *Manager) reconcileStopped(id string) (*Process, error) {
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
	delete(m.handles, id)
	p.Status = StatusStopped
	p.StoppedAt = time.Now()
	p.UpdatedAt = p.StoppedAt
	p.TerminalCause = EventCauseRequestedStop
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return p, err
	}
	m.mu.Unlock()
	m.publish(Event{Type: EventStopped, Cause: EventCauseRequestedStop, Process: *p})
	return p, nil
}

// retireUnverifiableRecordLocked ends management of a record whose process can
// never be verified. No signal is sent to the recorded pid; the record itself
// is marked failed and terminal so reconciliation does not retry it forever.
// The caller must hold m.mu; the lock is released before publishing.
func (m *Manager) retireUnverifiableRecordLocked(id string, p *Process, cause error) (*Process, error) {
	delete(m.handles, id)
	p.Status = StatusFailed
	p.LastError = fmt.Sprintf("record retired without signaling: %v", cause)
	p.StoppedAt = time.Now()
	p.UpdatedAt = p.StoppedAt
	p.TerminalCause = EventCauseRequestedStop
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return p, err
	}
	m.mu.Unlock()
	log.Printf("wuu: process %s has no recorded identity; retired its record without signaling pid %d", id, p.PID)
	m.publish(Event{Type: EventFailed, Cause: EventCauseRequestedStop, Process: *p})
	return p, nil
}

// errNoRecordedIdentity marks records that predate persisted process
// identities. A live process can never be verified against such a record, so
// the record must never be signaled; Stop retires it instead.
var errNoRecordedIdentity = errors.New("no recorded identity")

func processMatchesRecord(p *Process) (bool, error) {
	if p.PID <= 1 || !processExists(p.PID) {
		return false, nil
	}
	if p.PGID <= 1 {
		return false, fmt.Errorf("process %q has unsafe process group %d", p.ID, p.PGID)
	}
	storedIdentity := strings.TrimSpace(p.ProcessStartTime)
	if storedIdentity == "" {
		return false, fmt.Errorf("process %q has %w; refusing to signal pid %d", p.ID, errNoRecordedIdentity, p.PID)
	}
	currentIdentity, _, _, err := readProcessIdentity(p.PID)
	if err != nil {
		if !processExists(p.PID) {
			return false, nil
		}
		return false, fmt.Errorf("verify process %q identity: %w", p.ID, err)
	}
	groupRunning, err := verifyProcessGroup(p.PID, p.PGID)
	if err != nil {
		return false, fmt.Errorf("verify process %q group: %w", p.ID, err)
	}
	if !groupRunning {
		return false, nil
	}
	if storedIdentity == currentIdentity {
		return true, nil
	}
	if isLegacyStartTimeIdentity(storedIdentity) {
		currentStart, err := readLegacyProcessStartTime(p.PID)
		if err != nil {
			if !processExists(p.PID) {
				return false, nil
			}
			return false, fmt.Errorf("verify process %q identity: %w", p.ID, err)
		}
		if currentStart == storedIdentity {
			p.ProcessStartTime = currentIdentity
			return true, nil
		}
	}
	return false, fmt.Errorf("process %q identity changed; refusing to signal reused pid %d", p.ID, p.PID)
}

// legacyStartTimeLayout is the exact format older releases persisted: the
// verbatim output of "ps -o lstart=".
const legacyStartTimeLayout = "Mon Jan _2 15:04:05 2006"

func isLegacyStartTimeIdentity(identity string) bool {
	_, err := time.ParseInLocation(legacyStartTimeLayout, identity, time.Local)
	return err == nil
}

// readLegacyProcessStartTime reads the start time in the format older releases
// recorded. Legacy records are verified only by exact string comparison; any
// difference means the pid cannot be proven to belong to the record.
func readLegacyProcessStartTime(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("invalid process id %d", pid)
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", fmt.Errorf("read start time for process %d: %w", pid, err)
	}
	started := strings.TrimSpace(string(out))
	if started == "" {
		return "", fmt.Errorf("process %d has no start time", pid)
	}
	return started, nil
}

const persistedProcessWatchInterval = 250 * time.Millisecond

// resumePersistedManagedProcesses restores exit observation for managed
// processes that outlived the manager which started them. PTY/stdin handles
// cannot be reconstructed, but terminal state and completion delivery can.
func (m *Manager) resumePersistedManagedProcesses() error {
	processes, err := m.List()
	if err != nil {
		return err
	}
	for _, p := range processes {
		if p.Lifecycle != LifecycleManaged || !isLiveStatus(p.Status) {
			continue
		}
		matched, matchErr := processMatchesRecord(&p)
		if matched && matchErr == nil {
			go m.watchPersistedManagedProcess(p.ID)
			continue
		}
		if _, err := m.settlePersistedManagedProcess(p.ID, matchErr); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) watchPersistedManagedProcess(id string) {
	ticker := time.NewTicker(persistedProcessWatchInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		p, err := m.load(id)
		if err != nil || p.Lifecycle != LifecycleManaged || !isLiveStatus(p.Status) {
			m.mu.Unlock()
			return
		}
		matched, matchErr := processMatchesRecord(p)
		m.mu.Unlock()
		if matched && matchErr == nil {
			continue
		}
		_, _ = m.settlePersistedManagedProcess(id, matchErr)
		return
	}
}

func (m *Manager) settlePersistedManagedProcess(id string, observationErr error) (*Process, error) {
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !isLiveStatus(p.Status) {
		m.mu.Unlock()
		return p, nil
	}
	requestedStop := p.Status == StatusStopping
	if observationErr != nil {
		p.Status = StatusFailed
		p.LastError = fmt.Sprintf("managed process could not be re-observed after restart: %v", observationErr)
	} else {
		p.Status = StatusStopped
		p.LastError = "managed process exit observed after restart; exit code unavailable"
	}
	p.ExitCode = -1
	p.StoppedAt = time.Now()
	p.UpdatedAt = p.StoppedAt
	p.TerminalCause = EventCauseNaturalExit
	if requestedStop {
		p.TerminalCause = EventCauseRequestedStop
	}
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return p, err
	}
	m.mu.Unlock()
	eventType := EventStopped
	if p.Status == StatusFailed {
		eventType = EventFailed
	}
	m.publish(Event{Type: eventType, Cause: p.TerminalCause, Process: *p})
	return p, nil
}

// PendingCompletions returns naturally exited processes whose model-facing
// completion obligation has not yet been acknowledged.
func (m *Manager) PendingCompletions() ([]Process, error) {
	processes, err := m.List()
	if err != nil {
		return nil, err
	}
	pending := make([]Process, 0)
	for _, p := range processes {
		if processCompletionPending(p) {
			pending = append(pending, p)
		}
	}
	return pending, nil
}

func (m *Manager) CompletionPending(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.load(id)
	if err != nil {
		return false, err
	}
	return processCompletionPending(*p), nil
}

func (m *Manager) MarkCompletionDelivered(id, consumer string) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if p.CompletionMode == CompletionModeDetached || p.TerminalCause != EventCauseNaturalExit || (p.Status != StatusStopped && p.Status != StatusFailed) {
		return p, fmt.Errorf("process %q has no natural-exit completion to deliver", id)
	}
	if p.CompletionDeliveredAt.IsZero() {
		p.CompletionDeliveredAt = time.Now().UTC()
		p.CompletionConsumedBy = strings.TrimSpace(consumer)
		p.UpdatedAt = p.CompletionDeliveredAt
		if err := m.save(p); err != nil {
			return p, err
		}
	}
	return p, nil
}

// MaxRecheckMinutes bounds the model-declared recheck interval (24h).
const MaxRecheckMinutes = 24 * 60

const recheckSchedulerIdleInterval = time.Hour

// recheckScheduler is the single timer loop for persisted recheck deadlines.
// It sleeps until the earliest NextRecheckAt (or an idle interval when
// nothing is armed) and recomputes whenever signalRecheckScheduler fires.
// Deadlines live on the process record, so a manager restart rebuilds the
// schedule simply by scanning the registry on its first pass.
func (m *Manager) recheckScheduler() {
	for {
		next, due := m.scanRechecks()
		for _, id := range due {
			m.fireRecheck(id)
		}
		delay := recheckSchedulerIdleInterval
		if !next.IsZero() {
			delay = time.Until(next)
			if delay < time.Second {
				delay = time.Second
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-m.recheckWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// scanRechecks returns the earliest future recheck deadline and the ids whose
// deadline has passed. Terminal records carrying stale recheck state are
// cleaned up lazily here, which is what auto-cancels schedules on completion
// without touching every terminal transition.
func (m *Manager) scanRechecks() (time.Time, []string) {
	processes, err := m.List()
	if err != nil {
		return time.Time{}, nil
	}
	now := time.Now()
	var next time.Time
	var due []string
	for _, p := range processes {
		armed := p.RecheckMinutes > 0 || !p.NextRecheckAt.IsZero() || !p.PendingRecheckAt.IsZero()
		if !armed {
			continue
		}
		if !isLiveStatus(p.Status) {
			_ = m.clearRecheckState(p.ID)
			continue
		}
		if p.RecheckMinutes <= 0 || p.NextRecheckAt.IsZero() {
			continue
		}
		if !p.NextRecheckAt.After(now) {
			due = append(due, p.ID)
			continue
		}
		if next.IsZero() || p.NextRecheckAt.Before(next) {
			next = p.NextRecheckAt
		}
	}
	return next, due
}

// fireRecheck stamps a due recheck as pending delivery and arms the next
// interval. The persisted PendingRecheckAt is the delivery obligation; the
// published event is only a low-latency hint for subscribers.
func (m *Manager) fireRecheck(id string) {
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return
	}
	if !isLiveStatus(p.Status) || p.RecheckMinutes <= 0 {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	p.PendingRecheckAt = now
	p.NextRecheckAt = now.Add(time.Duration(p.RecheckMinutes) * time.Minute)
	p.UpdatedAt = now
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return
	}
	proc := *p
	m.mu.Unlock()
	m.publish(Event{Type: EventRecheckDue, Process: proc})
	m.signalRecheckScheduler()
}

func (m *Manager) clearRecheckState(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.load(id)
	if err != nil {
		return err
	}
	if p.RecheckMinutes == 0 && p.NextRecheckAt.IsZero() && p.PendingRecheckAt.IsZero() {
		return nil
	}
	p.RecheckMinutes = 0
	p.NextRecheckAt = time.Time{}
	p.PendingRecheckAt = time.Time{}
	p.UpdatedAt = time.Now()
	return m.save(p)
}

func (m *Manager) signalRecheckScheduler() {
	select {
	case m.recheckWake <- struct{}{}:
	default:
	}
}

// SetRecheck updates a live process's recheck schedule. Zero minutes cancels
// the schedule and drops any fired-but-undelivered recheck.
func (m *Manager) SetRecheck(id string, minutes int) (*Process, error) {
	if minutes < 0 || minutes > MaxRecheckMinutes {
		return nil, fmt.Errorf("recheck_minutes must be between 0 and %d", MaxRecheckMinutes)
	}
	m.mu.Lock()
	p, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !isLiveStatus(p.Status) {
		m.mu.Unlock()
		return p, fmt.Errorf("process %q is not running", id)
	}
	p.RecheckMinutes = minutes
	if minutes <= 0 {
		p.NextRecheckAt = time.Time{}
		p.PendingRecheckAt = time.Time{}
	} else {
		p.NextRecheckAt = time.Now().Add(time.Duration(minutes) * time.Minute)
	}
	p.UpdatedAt = time.Now()
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return p, err
	}
	m.mu.Unlock()
	m.signalRecheckScheduler()
	return p, nil
}

// PendingRechecks returns live processes with a fired-but-undelivered
// recheck. Terminal processes never appear here: their completion obligation
// supersedes any stale recheck.
func (m *Manager) PendingRechecks() ([]Process, error) {
	processes, err := m.List()
	if err != nil {
		return nil, err
	}
	pending := make([]Process, 0)
	for _, p := range processes {
		if isLiveStatus(p.Status) && !p.PendingRecheckAt.IsZero() {
			pending = append(pending, p)
		}
	}
	return pending, nil
}

// MarkRecheckDelivered clears the pending obligation once the recheck has
// been queued for the owning thread. Rechecks are periodic, so a delivery
// lost after this point self-heals at the next interval.
func (m *Manager) MarkRecheckDelivered(id string) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if !p.PendingRecheckAt.IsZero() {
		p.PendingRecheckAt = time.Time{}
		p.UpdatedAt = time.Now()
		if err := m.save(p); err != nil {
			return p, err
		}
	}
	return p, nil
}

// ReserveProcessLog allocates a fresh process id and matching log path under
// the manager's log dir without registering a record. Callers that may later
// hand a running command to Adopt tee the command's output into this file
// from the start, so a promotion keeps both past and future output.
func (m *Manager) ReserveProcessLog() (id, logPath string) {
	id = "proc-" + randomHex(4)
	return id, filepath.Join(m.logDir, id+".log")
}

// AdoptOptions describes an already-running command being promoted to a
// managed background process.
type AdoptOptions struct {
	Command        string
	CWD            string
	OwnerKind      OwnerKind
	OwnerID        string
	Lifecycle      Lifecycle
	CompletionMode CompletionMode
	StartedAt      time.Time
	// RecheckMinutes arms a progress recheck schedule on the adopted
	// process. Promoted commands are by definition underestimates — the
	// caller asked for a bounded run and the bound was wrong — so a default
	// reminder schedule replaces the kill safety net the timeout used to be.
	RecheckMinutes int
	SandboxMode    processsandbox.Mode
}

// Adopt registers a command started through StartCommand — whose output is
// already teeing into the reserved logf — as a managed background process.
// The caller keeps no ownership after a successful Adopt: terminal handling,
// completion delivery, and Stop all go through the manager from here. On
// error the caller retains the command and should stop it itself.
func (m *Manager) Adopt(id string, cmd *exec.Cmd, handle *CommandHandle, logf *os.File, opt AdoptOptions) (*Process, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("running command is required")
	}
	if handle == nil {
		return nil, errors.New("command handle is required")
	}
	if logf == nil {
		return nil, errors.New("log file is required")
	}
	if _, err := processRecordPath(m.registryDir, id); err != nil {
		return nil, err
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
	if opt.CompletionMode == "" {
		opt.CompletionMode = CompletionModeResume
	}
	if opt.CompletionMode != CompletionModeResume && opt.CompletionMode != CompletionModeDetached {
		return nil, errors.New("completion_mode must be resume or detached")
	}
	if opt.RecheckMinutes < 0 || opt.RecheckMinutes > MaxRecheckMinutes {
		return nil, fmt.Errorf("recheck_minutes must be between 0 and %d", MaxRecheckMinutes)
	}
	now := time.Now()
	startedAt := opt.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	p := &Process{
		ID:             id,
		OwnerKind:      opt.OwnerKind,
		OwnerID:        opt.OwnerID,
		Lifecycle:      opt.Lifecycle,
		CompletionMode: opt.CompletionMode,
		Status:         StatusRunning,
		Command:        opt.Command,
		CWD:            opt.CWD,
		LogPath:        filepath.Join(m.logDir, id+".log"),
		StartedAt:      startedAt,
		UpdatedAt:      now,
		ExitCode:       -1,
		SandboxMode:    opt.SandboxMode,
	}
	if opt.RecheckMinutes > 0 {
		p.RecheckMinutes = opt.RecheckMinutes
		p.NextRecheckAt = now.Add(time.Duration(opt.RecheckMinutes) * time.Minute)
	}
	p.PID = cmd.Process.Pid
	p.PGID = ProcessTreeForPID(p.PID).ID()
	var err error
	p.ProcessStartTime, _, _, err = readProcessIdentity(p.PID)
	if err != nil {
		return nil, fmt.Errorf("record process identity: %w", err)
	}
	m.mu.Lock()
	if err := m.save(p); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	adopted := &processHandle{done: make(chan struct{})}
	m.handles[id] = adopted
	m.mu.Unlock()
	m.publish(Event{Type: EventStarted, Process: *p})
	go func() {
		defer close(adopted.done)
		<-handle.Done()
		waitErr := handle.Wait()
		_ = logf.Close()
		_, discarded, _ := compactProcessLog(filepath.Join(m.logDir, id+".log"), terminalProcessLogMaxBytes)
		m.finishWait(id, cmd, waitErr, discarded)
	}()
	if p.RecheckMinutes > 0 {
		m.signalRecheckScheduler()
	}
	return p, nil
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
	if ch == nil {
		return
	}
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subscribers = append(m.subscribers, ch)
}

func (m *Manager) Unsubscribe(ch chan<- Event) {
	if ch == nil {
		return
	}
	m.subMu.Lock()
	defer m.subMu.Unlock()
	next := m.subscribers[:0]
	for _, subscriber := range m.subscribers {
		if subscriber != ch {
			next = append(next, subscriber)
		}
	}
	m.subscribers = next
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
	path, err := processRecordPath(m.registryDir, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Process
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.ID != id {
		return nil, fmt.Errorf("process record %q contains mismatched id %q", filepath.Base(path), p.ID)
	}
	return &p, nil
}

func (m *Manager) save(p *Process) error {
	path, err := processRecordPath(m.registryDir, p.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return securefs.WriteFileAtomic(path, append(b, '\n'))
}

func processRecordPath(registryDir, id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("invalid process id %q", id)
	}
	return filepath.Join(registryDir, id+".json"), nil
}

func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }
