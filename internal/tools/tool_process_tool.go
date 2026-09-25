package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
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

// ProcessTool observes and controls processes that bash started with
// run_in_background or handed over after a timeout. Starting stays in bash so
// the model has one entry point for launching commands; this tool keeps the
// bash schema free of process-management parameters.
type ProcessTool struct{ env *Env }

func NewProcessTool(env *Env) *ProcessTool { return &ProcessTool{env: env} }

func (t *ProcessTool) Name() string            { return "process" }
func (t *ProcessTool) IsReadOnly() bool        { return false }
func (t *ProcessTool) IsConcurrencySafe() bool { return false }

func (t *ProcessTool) Classify(argsJSON string) ToolClassification {
	var args processArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return highRiskShellClassification("invalid process invocation", false)
	}
	switch strings.TrimSpace(args.Action) {
	case processActionList, processActionRead:
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            ToolRiskMedium,
			Reason:          "managed background process observation",
		}
	case processActionWrite, processActionStop, processActionUpdate:
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: true,
			Risk:            ToolRiskHigh,
			Reason:          "managed background process control",
		}
	default:
		return highRiskShellClassification("invalid process action", false)
	}
}

func (t *ProcessTool) ValidateInput(argsJSON string) error {
	var args processArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	return args.validate()
}

func (t *ProcessTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "process",
		Description: "Inspect or control background processes started by bash with run_in_background, or handed to the background after a timeout. " +
			"action=read returns new output (a snapshot, or a bounded wait with wait_ms); action=write sends input; action=stop ends the process tree; action=list shows processes; " +
			"action=update sets completion_mode or recheck_minutes. A process whose exit should not resume this task, such as a dev server, should be set to completion_mode=detached. " +
			"Exits are reported to you automatically, so do not chain waits.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{processActionRead, processActionWrite, processActionStop, processActionList, processActionUpdate},
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Process id for read/write/stop/update.",
				},
				"wait_ms": map[string]any{
					"type":        "integer",
					"description": "For read: wait up to this long (max 300000) for new output; returns early on output or exit.",
				},
				"offset_bytes": map[string]any{
					"type":        "integer",
					"description": "For read: byte offset to read from; pass the previous next offset.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "For read: maximum bytes to return (default 32768).",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "For write: text to send, including a trailing newline when needed.",
				},
				"completion_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"resume", "detached"},
					"description": "For update: resume (default) starts a new turn when the process exits; detached does not.",
				},
				"recheck_minutes": map[string]any{
					"type":        "integer",
					"description": "For update: minutes between progress wake-ups (1-1440); 0 cancels.",
				},
			},
			"required": []string{"action"},
		},
	}
}

type processArgs struct {
	Action         string `json:"action"`
	WaitMS         int    `json:"wait_ms"`
	MaxBytes       int    `json:"max_bytes"`
	OffsetBytes    *int64 `json:"offset_bytes"`
	ProcessID      string `json:"process_id"`
	Input          string `json:"input"`
	CompletionMode string `json:"completion_mode"`
	RecheckMinutes *int   `json:"recheck_minutes"`
}

func (args processArgs) validate() error {
	switch strings.TrimSpace(args.Action) {
	case processActionList:
	case processActionRead, processActionWrite, processActionStop:
		if strings.TrimSpace(args.ProcessID) == "" {
			return fmt.Errorf("process %s requires process_id", strings.TrimSpace(args.Action))
		}
	case processActionUpdate:
		if strings.TrimSpace(args.ProcessID) == "" {
			return errors.New("process update requires process_id")
		}
		if strings.TrimSpace(args.CompletionMode) == "" && args.RecheckMinutes == nil {
			return errors.New("process update requires completion_mode or recheck_minutes")
		}
	default:
		return errors.New("process action must be one of read, write, stop, list, update")
	}
	return nil
}

func (t *ProcessTool) Execute(ctx context.Context, argsJSON string) (string, error) {
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

func (t *ProcessTool) executeList() (string, error) {
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

func (t *ProcessTool) executeRead(ctx context.Context, args processArgs) (string, error) {
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
		return []string{"The wait ended with the process still running. Do not wait on it again right away; continue other work or end the turn, and its exit will start a new one. For a long quiet task, set recheck_minutes with process action=update."}
	}
	if snapshot.Duration <= minDwell+time.Second {
		return []string{"This process writes output continuously; do not chain waits on it."}
	}
	return nil
}

func (t *ProcessTool) executeWrite(args processArgs) (string, error) {
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

func (t *ProcessTool) executeStop(args processArgs) (string, error) {
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

func (t *ProcessTool) executeUpdate(args processArgs) (string, error) {
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
	if args.RecheckMinutes != nil {
		if p, err = m.SetRecheck(args.ProcessID, *args.RecheckMinutes); err != nil {
			return "", err
		}
	}
	return mustJSON(map[string]any{
		"action":          processActionUpdate,
		"process_id":      args.ProcessID,
		"completion_mode": p.CompletionMode,
		"recheck_minutes": p.RecheckMinutes,
		"next_recheck_at": p.NextRecheckAt,
		"process":         redactProcessPtr(t.env, p),
	})
}
