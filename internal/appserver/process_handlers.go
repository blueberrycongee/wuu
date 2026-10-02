package appserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const (
	processReadDefaultBytes = 64 * 1024
	processReadMaxBytes     = 512 * 1024
	processReadMaxWait      = 30 * time.Second
)

func (s *Server) handleProcessList(req Request) error {
	var params ProcessListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	manager := s.processManagerForThread(threadID)
	if manager == nil {
		return s.writeResponse(req.ID, nil, errors.New("process manager is not available"))
	}
	processes, err := manager.List()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	summaries := make([]ManagedProcessSummary, 0, len(processes))
	for _, p := range processes {
		if !s.processBelongsToThread(threadID, p) {
			continue
		}
		summaries = append(summaries, managedProcessSummaryForManager(manager, p))
	}
	return s.writeResponse(req.ID, ProcessListResult{Processes: summaries}, nil)
}

func (s *Server) handleProcessRead(ctx context.Context, req Request) error {
	var params ProcessReadParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	manager, _, err := s.authorizedProcess(params.ThreadID, params.ProcessID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	maxBytes := params.MaxBytes
	if maxBytes <= 0 {
		maxBytes = processReadDefaultBytes
	}
	if maxBytes > processReadMaxBytes {
		maxBytes = processReadMaxBytes
	}
	if params.Offset != nil && *params.Offset < 0 {
		return s.writeResponse(req.ID, nil, errors.New("offset_bytes must be non-negative"))
	}
	wait := time.Duration(params.WaitMillis) * time.Millisecond
	if wait < 0 || wait > processReadMaxWait {
		return s.writeResponse(req.ID, nil, errors.New("wait_ms must be between 0 and 30000"))
	}
	snapshot, err := manager.ReadOutputSnapshot(ctx, strings.TrimSpace(params.ProcessID), process.OutputReadOptions{
		MaxBytes:    maxBytes,
		OffsetBytes: params.Offset,
		Wait:        wait,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ProcessReadResult{
		Process:     managedProcessSummaryForManager(manager, snapshot.Process),
		Output:      tools.RedactToolOutput(snapshot.Output),
		Truncated:   snapshot.Truncated,
		StartOffset: snapshot.StartOffset,
		EndOffset:   snapshot.EndOffset,
		TotalBytes:  snapshot.TotalBytes,
		TimedOut:    snapshot.TimedOut,
	}, nil)
}

func (s *Server) handleProcessWrite(req Request) error {
	var params ProcessWriteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	manager, p, err := s.authorizedProcess(params.ThreadID, params.ProcessID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if !p.TTY || !manager.InputAvailable(p.ID) {
		return s.writeResponse(req.ID, nil, errors.New("interactive input is not available for this process"))
	}
	written, err := manager.WriteStdin(p.ID, params.Input)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ProcessWriteResult{
		Process:      managedProcessSummaryForManager(manager, *written),
		BytesWritten: len(params.Input),
	}, nil)
}

func (s *Server) handleProcessResize(req Request) error {
	var params ProcessResizeParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Cols < 2 || params.Cols > 500 || params.Rows < 2 || params.Rows > 200 {
		return s.writeResponse(req.ID, nil, errors.New("terminal size is out of range"))
	}
	manager, p, err := s.authorizedProcess(params.ThreadID, params.ProcessID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	resized, err := manager.ResizeTTY(p.ID, params.Cols, params.Rows)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ProcessResizeResult{Process: managedProcessSummaryForManager(manager, *resized)}, nil)
}

func (s *Server) handleProcessStop(req Request) error {
	var params ProcessStopParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	manager, p, err := s.authorizedProcess(params.ThreadID, params.ProcessID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	stopped, err := manager.Stop(p.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if stopped == nil {
		return s.writeResponse(req.ID, nil, errors.New("process not found"))
	}
	return s.writeResponse(req.ID, ProcessStopResult{Process: managedProcessSummaryForManager(manager, *stopped)}, nil)
}

func (s *Server) authorizedProcess(threadID, processID string) (*process.Manager, *process.Process, error) {
	threadID = strings.TrimSpace(threadID)
	processID = strings.TrimSpace(processID)
	if threadID == "" {
		return nil, nil, errors.New("thread_id is required")
	}
	if processID == "" {
		return nil, nil, errors.New("process_id is required")
	}
	manager := s.processManagerForThread(threadID)
	if manager == nil {
		return nil, nil, errors.New("process manager is not available")
	}
	p, err := manager.Get(processID)
	if err != nil {
		return nil, nil, err
	}
	if !s.processBelongsToThread(threadID, *p) {
		return nil, nil, errors.New("process does not belong to thread")
	}
	return manager, p, nil
}

func (s *Server) processManagerForThread(threadID string) *process.Manager {
	if th := s.thread(strings.TrimSpace(threadID)); th != nil {
		th.mu.Lock()
		var manager *process.Manager
		if th.execRuntime != nil {
			manager = th.execRuntime.ProcessManager
		}
		th.mu.Unlock()
		if manager == nil && s.rt != nil {
			var err error
			manager, err = s.rt.RemoteProcesses(threadID)
			if err != nil {
				return nil
			}
		}
		if manager != nil {
			return manager
		}
	}
	if s.rt == nil {
		return nil
	}
	return s.rt.ProcessManager
}

func (s *Server) processBelongsToThread(threadID string, p process.Process) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	if p.OwnerKind == process.OwnerMainAgent {
		return strings.TrimSpace(p.OwnerID) == threadID
	}
	th := s.thread(threadID)
	if th == nil {
		return false
	}
	th.mu.Lock()
	if th.execRuntime == nil {
		th.mu.Unlock()
		return false
	}
	control := th.execRuntime.AgentControl
	th.mu.Unlock()
	return processEventBelongsToThread(threadID, control, process.Event{Process: p})
}

func managedProcessSummaryForManager(manager *process.Manager, p process.Process) ManagedProcessSummary {
	summary := managedProcessSummary(p)
	if manager != nil {
		summary.InputAvailable = p.TTY && manager.InputAvailable(p.ID)
	}
	return summary
}

func managedProcessSummary(p process.Process) ManagedProcessSummary {
	return ManagedProcessSummary{
		ID:        p.ID,
		OwnerKind: string(p.OwnerKind),
		OwnerID:   p.OwnerID,
		Lifecycle: string(p.Lifecycle),
		Status:    string(p.Status),
		PID:       p.PID,
		TTY:       p.TTY,
		Command:   tools.RedactToolOutput(p.Command),
		CWD:       p.CWD,
		StartedAt: p.StartedAt,
		UpdatedAt: p.UpdatedAt,
		StoppedAt: p.StoppedAt,
		ExitCode:  p.ExitCode,
		LastError: tools.RedactToolOutput(p.LastError),
	}
}
