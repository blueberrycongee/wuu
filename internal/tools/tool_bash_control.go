package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
)

const (
	processActionList   = "list"
	processActionRead   = "read"
	processActionWrite  = "write"
	processActionStop   = "stop"
	processActionUpdate = "update"
)

const (
	// readProcessMaxWaitMS caps one bounded event-driven wait at 5 minutes.
	readProcessMaxWaitMS = 300_000
	// processWaitMinDwell paces output-driven early returns so a continuously
	// producing process releases a wait at most this often.
	processWaitMinDwell = 5 * time.Second
)

type processArgs struct {
	Action         string `json:"action"`
	WaitMS         int    `json:"wait_ms"`
	MaxBytes       int    `json:"max_bytes"`
	OffsetBytes    *int64 `json:"offset_bytes"`
	ProcessID      string `json:"process_id"`
	Input          string `json:"input"`
	CompletionMode string `json:"completion_mode"`
}

func (args processArgs) validate() error {
	if args.CompletionMode != "" && args.CompletionMode != string(proc.CompletionModeResume) && args.CompletionMode != string(proc.CompletionModeDetached) {
		return errors.New("completion_mode must be resume or detached")
	}
	if args.WaitMS < 0 || args.WaitMS > readProcessMaxWaitMS || args.MaxBytes < 0 || args.OffsetBytes != nil && *args.OffsetBytes < 0 {
		return errors.New("wait_ms must be between 0 and 300000; max_bytes and offset_bytes must be non-negative")
	}
	action := strings.TrimSpace(args.Action)
	if action != processActionUpdate && args.CompletionMode != "" {
		return errors.New("completion_mode requires run or update")
	}
	if action != processActionRead && (args.WaitMS != 0 || args.MaxBytes != 0 || args.OffsetBytes != nil) {
		return errors.New("output options require read")
	}
	if action != processActionWrite && args.Input != "" {
		return errors.New("input requires write")
	}
	if action == processActionList && args.ProcessID != "" {
		return errors.New("list does not accept process_id")
	}
	switch strings.TrimSpace(args.Action) {
	case processActionList:
	case processActionRead, processActionWrite, processActionStop:
		if strings.TrimSpace(args.ProcessID) == "" {
			return fmt.Errorf("bash %s requires process_id", strings.TrimSpace(args.Action))
		}
	case processActionUpdate:
		if strings.TrimSpace(args.ProcessID) == "" {
			return errors.New("bash update requires process_id")
		}
		if strings.TrimSpace(args.CompletionMode) == "" {
			return errors.New("bash update requires completion_mode")
		}
	default:
		return errors.New("bash action must be one of run, read, write, stop, list, update")
	}
	return nil
}

func (t *BashTool) executeControl(ctx context.Context, argsJSON string) (string, error) {
	if len(t.env.ProtectedPaths) > 0 {
		return "", errors.New("managed processes cannot prove protected read/write enforcement for recovered or pre-existing commands")
	}
	var args processArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if err := args.validate(); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case processActionList:
		return t.executeList()
	case processActionRead:
		return t.executeRead(ctx, args)
	case processActionWrite:
		return t.executeWrite(args)
	case processActionStop:
		return t.executeStop(args)
	default:
		return t.executeUpdate(args)
	}
}

func (t *BashTool) executeList() (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	ps, err := m.List()
	if err != nil {
		return "", err
	}
	redacted := make([]proc.Process, 0, len(ps))
	for _, p := range ps {
		redacted = append(redacted, redactProcess(t.env, p))
	}
	return mustJSON(map[string]any{
		"action":    processActionList,
		"processes": redacted,
	})
}

func (t *BashTool) executeRead(ctx context.Context, args processArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	waitMS := args.WaitMS
	if waitMS > readProcessMaxWaitMS {
		waitMS = readProcessMaxWaitMS
	}
	wait := time.Duration(waitMS) * time.Millisecond
	minDwell := time.Duration(0)
	if wait > 0 {
		minDwell = processWaitMinDwell
		if minDwell > wait {
			minDwell = wait
		}
	}
	snapshot, err := m.ReadOutputSnapshot(ctx, args.ProcessID, proc.OutputReadOptions{
		MaxBytes:    args.MaxBytes,
		OffsetBytes: args.OffsetBytes,
		Wait:        wait,
		MinDwell:    minDwell,
	})
	if err != nil {
		return "", err
	}
	process := markProcessCompletionObserved(m, snapshot.Process)
	return mustJSON(map[string]any{
		"action":           processActionRead,
		"process_id":       args.ProcessID,
		"output":           t.env.RedactToolOutput(snapshot.Output),
		"truncated":        snapshot.Truncated,
		"start_offset":     snapshot.StartOffset,
		"end_offset":       snapshot.EndOffset,
		"total_bytes":      snapshot.TotalBytes,
		"timed_out":        snapshot.TimedOut,
		"duration_ms":      snapshot.Duration.Milliseconds(),
		"status":           process.Status,
		"exit_code":        process.ExitCode,
		"process":          redactProcess(t.env, process),
		"next_suggestions": processReadNextSuggestions(waitMS, minDwell, snapshot, process),
	})
}

// processReadNextSuggestions turns a wait outcome into guidance only when it
// prevents a polling loop: an expired wait, or a wait released at the pacing
// floor because the process writes output continuously.
func processReadNextSuggestions(waitMS int, minDwell time.Duration, snapshot proc.OutputSnapshot, process proc.Process) []string {
	if waitMS <= 0 || !processIsLive(process.Status) {
		return nil
	}
	if snapshot.TimedOut {
		return []string{"The wait ended with the process still running. Do not wait on it again right away; continue other work or end the turn, and its exit will start a new one."}
	}
	if snapshot.Duration <= minDwell+time.Second {
		return []string{"This process writes output continuously; do not chain waits on it."}
	}
	return nil
}

func (t *BashTool) executeWrite(args processArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	p, err := m.WriteStdin(args.ProcessID, args.Input)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{
		"action":        processActionWrite,
		"process_id":    args.ProcessID,
		"bytes_written": len(args.Input),
		"process":       redactProcessPtr(t.env, p),
	})
}

func (t *BashTool) executeStop(args processArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	p, err := m.Stop(args.ProcessID)
	if err != nil {
		return "", err
	}
	redacted := redactProcessPtr(t.env, p)
	if redacted != nil {
		redacted.Action = processActionStop
	}
	return mustJSON(redacted)
}

func (t *BashTool) executeUpdate(args processArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	var p *proc.Process
	if mode := strings.TrimSpace(args.CompletionMode); mode != "" {
		if p, err = m.SetCompletionMode(args.ProcessID, proc.CompletionMode(mode)); err != nil {
			return "", err
		}
	}
	return mustJSON(map[string]any{
		"action":          processActionUpdate,
		"process_id":      args.ProcessID,
		"completion_mode": p.CompletionMode,
		"process":         redactProcessPtr(t.env, p),
	})
}
