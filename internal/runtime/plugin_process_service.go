package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Legacy native dispatch carries an existing Toolkit; convert only that
// explicit native context to the common process scope. External dispatch
// supplies pluginProcessScopeKey directly and never creates a Toolkit.
type pluginProcessToolkitKey struct{}

type pluginProcessStart struct {
	Key          string `json:"key"`
	Command      string `json:"command"`
	CWD          string `json:"cwd,omitempty"`
	TimeoutMS    int64  `json:"timeout_ms,omitempty"`
	FollowParent bool   `json:"follow_parent,omitempty"`
}

type pluginProcessDelivery struct {
	input  [sha256.Size]byte
	result json.RawMessage
}

// Admission owns one key while authorization or a remote transport is pending.
// The generation mutex protects receipts, never network or process I/O.
type pluginProcessAdmission struct {
	input    pluginProcessStart
	done     chan struct{}
	cancel   context.CancelFunc
	process  *pluginProcess
	err      error
	terminal bool
}

type pluginProcess struct {
	manager                  *proc.Manager
	id, processID, sessionID string
	input                    pluginProcessStart
	environment              tools.ProcessEnvironment
	cancel                   func() bool
	timer                    *time.Timer
	mu                       sync.Mutex
	reason                   string
	deliveryMu               sync.Mutex
	deliveries               map[string]pluginProcessDelivery
	deliveryOrder            []string
}

func newPluginProcess(started *proc.Process, manager *proc.Manager, sessionID string, input pluginProcessStart, location tools.ProcessEnvironment, generation context.Context) *pluginProcess {
	id := started.ID
	if location.Backend != "local" {
		id = "plugin-process-" + rand.Text()
	}
	p := &pluginProcess{manager: manager, id: id, processID: started.ID, sessionID: sessionID, input: input, environment: location}
	p.cancel = context.AfterFunc(generation, func() {
		if _, err := p.stop("generation_closed"); err != nil {
			slog.Error("stop extension process", "process", p.id, "error", err)
		}
	})
	if input.TimeoutMS > 0 {
		p.timer = time.AfterFunc(time.Duration(input.TimeoutMS)*time.Millisecond, func() {
			if _, err := p.stop("timeout"); err != nil {
				slog.Error("expire extension process", "process", p.id, "error", err)
			}
		})
	}
	return p
}

func (p *pluginProcess) stop(reason string) (*proc.Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current, err := p.manager.Get(p.processID)
	if err != nil {
		return nil, err
	}
	if current.Status != proc.StatusRunning && current.Status != proc.StatusStarting && current.Status != proc.StatusStopping {
		return current, nil
	}
	if p.reason == "" {
		p.reason = reason
	}
	return p.manager.Stop(p.processID)
}

func (s *pluginHostServices) closeProcesses() {
	s.processMu.Lock()
	s.processesClosed = true
	admissions := make([]*pluginProcessAdmission, 0, len(s.processStarts))
	for _, admission := range s.processStarts {
		admissions = append(admissions, admission)
		admission.cancel()
	}
	processes := s.processes
	s.processes = nil
	s.processMu.Unlock()
	// A start that returns after cancellation must stop its unaccepted process
	// before completing the receipt. Do not let generation cleanup race it.
	for _, admission := range admissions {
		<-admission.done
	}
	for _, p := range processes {
		p.cancel()
		if p.timer != nil {
			p.timer.Stop()
		}
		if _, err := p.stop("generation_closed"); err != nil {
			slog.Error("stop extension process", "plugin", s.pluginID, "process", p.id, "error", err)
		}
	}
	s.processMu.Lock()
	s.processStarts = nil
	s.processStartOrder = nil
	s.processMu.Unlock()
}

func (s *pluginHostServices) admitProcess(key string, input pluginProcessStart, cancel context.CancelFunc) (*pluginProcessAdmission, bool, error) {
	s.processMu.Lock()
	defer s.processMu.Unlock()
	if s.processesClosed {
		return nil, false, serviceError("generation_closed", "process owner is inactive")
	}
	if existing := s.processStarts[key]; existing != nil {
		if existing.input != input {
			return nil, false, serviceError("request_conflict", "process key already used with different arguments")
		}
		return existing, false, nil
	}
	if s.processActive >= 64 {
		return nil, false, serviceError("limit_exceeded", "at most 64 extension processes may run or prepare")
	}
	if len(s.processStarts) >= 256 {
		// Live/pending operations retain their receipts; only terminal receipts age
		// out. The order remains bounded because at most 64 operations can be live.
		order := s.processStartOrder[:0]
		for _, oldKey := range s.processStartOrder {
			old := s.processStarts[oldKey]
			complete := false
			select {
			case <-old.done:
				complete = true
			default:
			}
			if len(s.processStarts) >= 256 && old.terminal && complete {
				delete(s.processStarts, oldKey)
				if old.process != nil {
					delete(s.processes, old.process.id)
				}
				continue
			}
			order = append(order, oldKey)
		}
		s.processStartOrder = order
	}
	if s.processStarts == nil {
		s.processStarts = make(map[string]*pluginProcessAdmission)
	}
	admission := &pluginProcessAdmission{input: input, done: make(chan struct{}), cancel: cancel}
	s.processStarts[key] = admission
	s.processStartOrder = append(s.processStartOrder, key)
	s.processActive++
	return admission, true, nil
}

func (s *pluginHostServices) finishProcess(admission *pluginProcessAdmission) {
	s.processMu.Lock()
	if !admission.terminal {
		admission.terminal = true
		s.processActive--
	}
	s.processMu.Unlock()
}

func (p *pluginProcess) refreshSession(ctx context.Context, router *PluginSessionRouter) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, closed, err := router.resolveProcessSession(ctx, p.sessionID, p.input.FollowParent)
	if err != nil {
		return err
	}
	p.sessionID = id
	if closed {
		p.reason = "session_closed"
		_, err = p.manager.Stop(p.processID)
	}
	return err
}

func (p *pluginProcess) watchSession(ctx context.Context, router *PluginSessionRouter) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := p.manager.Get(p.processID)
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("inspect extension process", "process", p.id, "error", err)
				}
				continue
			}
			if current.Status == proc.StatusStopped || current.Status == proc.StatusFailed {
				return
			}
			if err := p.refreshSession(ctx, router); err != nil && ctx.Err() == nil {
				slog.Error("refresh extension process session", "process", p.id, "error", err)
			}
		}
	}
}

type processServiceInvoker struct{ parent *kernelHostServices }

func (k *processServiceInvoker) ID() string                { return k.parent.ID() }
func (k *processServiceInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *processServiceInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (result json.RawMessage, resultErr error) {
	ctx, releaseState := k.parent.captureInvocationState(ctx)
	defer releaseState()
	k.parent.mu.RLock()
	s := k.parent.services[params.Caller]
	k.parent.mu.RUnlock()
	if s == nil {
		return nil, serviceError("service_unavailable", "process owner is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	active := s.open && s.active
	generation := s.lockContext
	s.mu.RUnlock()
	if !active {
		return nil, serviceError("generation_closed", "process owner is inactive")
	}
	if params.Method == "start" || params.Method == "environment" {
		var input pluginProcessStart
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if params.Method == "start" && (input.Key == "" || len(input.Key) > 128 || input.Command == "" || len(input.Command) > 64*1024 || input.TimeoutMS < 0 || input.TimeoutMS > int64((1<<63-1)/time.Millisecond)) {
			return nil, serviceError("invalid_params", "start requires a key, a command within 64 KiB and a non-negative timeout_ms")
		}
		scope, scopeErr := params.CallerExecution(true)
		if scopeErr != nil {
			return nil, scopeErr
		}
		processScope, _ := scope.Context.Value(pluginProcessScopeKey{}).(*tools.ProcessScope)
		if processScope == nil {
			if kit, ok := scope.Context.Value(pluginProcessToolkitKey{}).(*tools.Toolkit); ok {
				var err error
				processScope, err = kit.PluginProcessScope()
				if err != nil {
					return nil, err
				}
			}
		}
		if processScope == nil {
			return nil, serviceError("invalid_execution_scope", "process start requires host process authority")
		}
		actorID := scope.ActorID
		if actorID == "" {
			actorID = scope.ThreadID
		}
		processScope, bindErr := processScope.Bind(scope.ThreadID, actorID, scope.CWD)
		if bindErr != nil {
			return nil, serviceError("invalid_execution_scope", bindErr.Error())
		}
		if err := scope.Context.Err(); err != nil {
			return nil, err
		}
		location, err := processScope.Environment()
		if err != nil {
			return nil, err
		}
		if params.Method == "environment" {
			return marshalServiceResult(location)
		}
		startCtx, cancel := context.WithCancel(scope.Context)
		stopTransport := context.AfterFunc(ctx, cancel)
		stopGeneration := context.AfterFunc(generation, cancel)
		defer func() { stopTransport(); stopGeneration(); cancel() }()
		key := scope.SessionID + ":" + input.Key
		admission, owner, err := s.admitProcess(key, input, cancel)
		if err != nil {
			return nil, err
		}
		if !owner {
			select {
			case <-admission.done:
				if admission.err != nil {
					return nil, admission.err
				}
				return processServiceSnapshot(admission.process)
			case <-startCtx.Done():
				return nil, startCtx.Err()
			}
		}
		var accepted *pluginProcess
		var binding *State
		watchStarted := false
		defer func() {
			s.processMu.Lock()
			admission.process = accepted
			if accepted == nil {
				admission.err = resultErr
				admission.terminal = true
				s.processActive--
			}
			close(admission.done)
			s.processMu.Unlock()
			if binding != nil && !watchStarted {
				_ = binding.Close()
			}
		}()
		binding, err = RetainInvocationState(ctx)
		if err != nil {
			return nil, err
		}
		started, manager, err := processScope.Start(startCtx, input.Command, input.CWD)
		if err != nil {
			return nil, err
		}
		if err := startCtx.Err(); err != nil {
			_, stopErr := manager.Stop(started.ID)
			return nil, errors.Join(err, stopErr)
		}
		p := newPluginProcess(started, manager, scope.SessionID, input, location, generation)
		s.processMu.Lock()
		if s.processesClosed || startCtx.Err() != nil {
			s.processMu.Unlock()
			p.cancel()
			if p.timer != nil {
				p.timer.Stop()
			}
			_, stopErr := p.stop("generation_closed")
			return nil, errors.Join(serviceError("generation_closed", "process owner closed during preparation"), stopErr)
		}
		if s.processes == nil {
			s.processes = make(map[string]*pluginProcess)
		}
		s.processes[p.id] = p
		accepted = p
		s.processMu.Unlock()
		watchStarted = true
		go func() {
			defer s.finishProcess(admission)
			defer p.cancel()
			defer func() {
				if p.timer != nil {
					p.timer.Stop()
				}
			}()
			defer binding.Close()
			p.watchSession(WithInvocationState(generation, binding), s.turnRouter)
		}()
		return processServiceSnapshot(p)
	}
	var input struct {
		Stream    string                        `json:"stream,omitempty"`
		ProcessID string                        `json:"process_id"`
		Offset    int64                         `json:"offset_bytes,omitempty"`
		MaxBytes  int                           `json:"max_bytes,omitempty"`
		WaitMS    int                           `json:"wait_ms,omitempty"`
		Message   *pluginhost.SessionSendParams `json:"message,omitempty"`
	}
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	s.processMu.Lock()
	p := s.processes[input.ProcessID]
	s.processMu.Unlock()
	if p == nil {
		return nil, serviceError("process_not_found", "process is not owned by this plugin generation")
	}
	if params.Method == "read" || params.Method == "send" {
		if err := p.refreshSession(ctx, s.turnRouter); err != nil {
			return nil, err
		}
	}
	switch params.Method {
	case "send":
		if input.Message == nil {
			return nil, serviceError("invalid_params", "send requires a message")
		}
		p.deliveryMu.Lock()
		defer p.deliveryMu.Unlock()
		encoded, err := json.Marshal(input.Message)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		if receipt, ok := p.deliveries[input.Message.RequestID]; ok {
			if receipt.input != digest {
				return nil, serviceError("request_conflict", "event request_id already used with different input")
			}
			return receipt.result, nil
		}
		p.mu.Lock()
		recipient, closed := p.sessionID, p.reason == "session_closed"
		p.mu.Unlock()
		if closed {
			return nil, serviceError("session_closed", "process conversation was closed")
		}
		message := *input.Message
		message.SessionID = recipient
		result, err := s.turnRouter.Send(pluginhost.WithProcessSessionDelivery(ctx, recipient), s.pluginID, message)
		if err != nil {
			return nil, err
		}
		raw, err := marshalServiceResult(result)
		if err != nil {
			return nil, err
		}
		// Preserve acknowledgement across a child-to-parent handoff. Otherwise
		// a retry could deliver the same event to both conversations.
		if p.deliveries == nil {
			p.deliveries = make(map[string]pluginProcessDelivery)
		}
		if len(p.deliveryOrder) == 256 {
			delete(p.deliveries, p.deliveryOrder[0])
			p.deliveryOrder = p.deliveryOrder[1:]
		}
		p.deliveries[input.Message.RequestID] = pluginProcessDelivery{input: digest, result: raw}
		p.deliveryOrder = append(p.deliveryOrder, input.Message.RequestID)
		return raw, nil
	case "stop":
		if _, err := p.stop("requested_stop"); err != nil {
			return nil, err
		}
		return processServiceSnapshot(p)
	case "read":
		if input.Offset < 0 || input.MaxBytes < 0 || input.MaxBytes > 1024*1024 || input.WaitMS < 0 || input.WaitMS > 30_000 {
			return nil, serviceError("invalid_params", "read requires a non-negative offset, up to 1 MiB and a wait up to 30000ms")
		}
		if input.MaxBytes == 0 {
			input.MaxBytes = 65536
		} else if input.MaxBytes < utf8.UTFMax {
			return nil, serviceError("invalid_params", "max_bytes must be at least 4")
		}
		readCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(generation, cancel)
		defer func() { stop(); cancel() }()
		out, err := p.manager.ReadOutputSnapshot(readCtx, p.processID, proc.OutputReadOptions{Stream: input.Stream, OffsetBytes: &input.Offset, MaxBytes: input.MaxBytes, Wait: time.Duration(input.WaitMS) * time.Millisecond})
		if err != nil {
			return nil, err
		}
		// Preserve an incomplete UTF-8 suffix for the next cursor read. Invalid
		// bytes elsewhere are still rendered as replacement characters by JSON.
		if out.EndOffset < out.TotalBytes || out.Process.Status == proc.StatusRunning || out.Process.Status == proc.StatusStopping {
			start := len(out.Output) - 1
			for start > 0 && !utf8.RuneStart(out.Output[start]) {
				start--
			}
			if start >= 0 && !utf8.FullRuneInString(out.Output[start:]) {
				out.EndOffset -= int64(len(out.Output) - start)
				out.Output = out.Output[:start]
			}
		}
		status := processServiceStatus(p, &out.Process)
		return marshalServiceResult(struct {
			Stream     string `json:"stream,omitempty"`
			Process    any    `json:"process"`
			Output     string `json:"output"`
			NextOffset int64  `json:"next_offset"`
			TotalBytes int64  `json:"total_bytes"`
			Truncated  bool   `json:"truncated"`
		}{out.Stream, status, tools.RedactToolOutput(out.Output), out.EndOffset, out.TotalBytes, out.Truncated})
	default:
		return nil, serviceError("method_not_found", fmt.Sprintf("unknown process method %q", params.Method))
	}
}

func processServiceSnapshot(p *pluginProcess) (json.RawMessage, error) {
	current, err := p.manager.Get(p.processID)
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(processServiceStatus(p, current))
}

func processServiceStatus(p *pluginProcess, current *proc.Process) any {
	p.mu.Lock()
	reason, sessionID := p.reason, p.sessionID
	p.mu.Unlock()
	if reason == "" && current.TerminalCause == proc.EventCauseRequestedStop {
		reason = "requested_stop"
	}
	return struct {
		ID          string                   `json:"id"`
		OperationID string                   `json:"operation_id,omitempty"`
		SessionID   string                   `json:"session_id"`
		State       proc.Status              `json:"state"`
		ExitCode    int                      `json:"exit_code"`
		Reason      string                   `json:"reason,omitempty"`
		Error       string                   `json:"error,omitempty"`
		Environment tools.ProcessEnvironment `json:"environment"`
	}{p.id, current.OperationID, sessionID, current.Status, current.ExitCode, reason, tools.RedactToolOutput(current.LastError), p.environment}
}
