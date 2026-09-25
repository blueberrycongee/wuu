package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	processActionStart  = "start"
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

// ProcessTool manages long-lived and interactive commands: dev servers,
// watchers, REPLs, downloads. It is separate from bash so the everyday command
// schema stays small; the runtime's process manager backs both, which is how a
// timed-out bash run can be handed over and later observed here.
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
	case processActionStart:
		classification := classifyShellCommand(args.Command)
		reason := "managed background process"
		if classification.Reason != "" {
			reason = "background command: " + classification.Reason
		}
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     classification.Destructive,
			Risk:            ToolRiskHigh,
			Reason:          reason,
		}
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
		Description: "Manage long-lived or interactive commands (servers, watchers, REPLs, downloads) as background processes. " +
			"action=start launches a command in a pseudo-terminal by default and returns its process_id; a naturally exiting process starts a new turn with its status and output tail, so end the turn instead of polling when its completion is the only remaining dependency. " +
			"action=read returns output (non-blocking snapshot, or a bounded wait when wait_ms is set); action=write sends input; action=stop ends the process tree; action=list shows managed processes; action=update changes the recheck schedule. " +
			"For a result that gates your next step, run the command with bash instead.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{processActionStart, processActionList, processActionRead, processActionWrite, processActionStop, processActionUpdate},
					"description": "Operation to perform.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Command to start (action=start). Do not append '&'.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for action=start. Defaults to the workspace root.",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Managed process id for read/write/stop/update.",
				},
				"wait_ms": map[string]any{
					"type":        "integer",
					"description": "For start/read: wait up to this long for new output before returning; returns early when output arrives or the process exits. Max 60000 for start, 300000 for read. Do not chain waits to keep a turn open.",
				},
				"offset_bytes": map[string]any{
					"type":        "integer",
					"description": "For read: byte offset to read from. Pass the previous end_offset for incremental reads.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum output bytes to return. Default 32768.",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "Text to write to the process input for action=write. Include a trailing newline when needed.",
				},
				"tty": map[string]any{
					"type":        "boolean",
					"description": "Run action=start in a pseudo-terminal. Defaults to true; set false only for log-only automation.",
				},
				"completion_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"resume", "detached"},
					"description": "resume (default) starts another model turn when the process exits. Use detached for long-lived services whose exit must not resume the current task.",
				},
				"lifecycle": map[string]any{
					"type":        "string",
					"enum":        []string{"session", "managed"},
					"description": "Process lifecycle. Defaults to session.",
				},
				"recheck_minutes": map[string]any{
					"type":        "integer",
					"description": "For start/update: minutes between scheduled progress wake-ups (1-1440); 0 cancels. Completion cancels the schedule automatically.",
				},
			},
			"required": []string{"action"},
		},
	}
}

type processArgs struct {
	Action         string `json:"action"`
	Command        string `json:"command"`
	CWD            string `json:"cwd"`
	OwnerKind      string `json:"owner_kind"`
	OwnerID        string `json:"owner_id"`
	Lifecycle      string `json:"lifecycle"`
	CompletionMode string `json:"completion_mode"`
	TTY            *bool  `json:"tty"`
	WaitMS         int    `json:"wait_ms"`
	MaxBytes       int    `json:"max_bytes"`
	OffsetBytes    *int64 `json:"offset_bytes"`
	ProcessID      string `json:"process_id"`
	Input          string `json:"input"`
	RecheckMinutes int    `json:"recheck_minutes"`
}

func (args processArgs) validate() error {
	switch strings.TrimSpace(args.Action) {
	case processActionStart:
		if strings.TrimSpace(args.Command) == "" {
			return errors.New("process start requires command")
		}
	case processActionList:
	case processActionRead, processActionWrite, processActionStop, processActionUpdate:
		if strings.TrimSpace(args.ProcessID) == "" {
			return fmt.Errorf("process %s requires process_id", strings.TrimSpace(args.Action))
		}
	default:
		return errors.New("process action must be one of start, list, read, write, stop, update")
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
	case processActionStart:
		return t.executeStart(ctx, args)
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

func (t *ProcessTool) executeStart(ctx context.Context, args processArgs) (string, error) {
	commandPrefix := ""
	if t.env.gitAttributionEnabled() {
		var err error
		commandPrefix, err = t.env.gitAttributionShellPrefix()
		if err != nil {
			return "", err
		}
	}
	args.OwnerKind = defaultProcessOwnerKind(t.env, args.OwnerKind)
	if strings.TrimSpace(args.OwnerID) == "" {
		args.OwnerID = defaultProcessOwnerID(t.env)
	}
	rootThreadID := processRootThreadID(t.env)
	if rootThreadID == "" {
		return "", errors.New("process start requires a bound session ID")
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	tty := true
	if args.TTY != nil {
		tty = *args.TTY
	}
	sandboxPolicy, sandboxTempDir, err := t.env.processSandboxPolicy(ctx)
	if err != nil {
		return "", fmt.Errorf("prepare filesystem process sandbox: %w", err)
	}
	commandEnv := shellCommandEnvForTool(os.Environ(), t.env)
	if sandboxTempDir != "" {
		commandEnv = replaceCommandEnv(commandEnv, "TMPDIR", sandboxTempDir)
	}
	p, startErr := m.Start(context.WithoutCancel(ctx), proc.StartOptions{Command: args.Command, CommandPrefix: commandPrefix, CWD: args.CWD, WorkspaceRoot: t.env.RootDir, OwnerKind: proc.OwnerKind(args.OwnerKind), OwnerID: args.OwnerID, RootThreadID: rootThreadID, Lifecycle: proc.Lifecycle(args.Lifecycle), CompletionMode: proc.CompletionMode(args.CompletionMode), TTY: tty, AllowOutsideWorkspace: t.env.BypassToolHardProtections(), RecheckMinutes: args.RecheckMinutes, SandboxPolicy: sandboxPolicy, SandboxProvider: t.env.ProcessSandboxProvider, Env: commandEnv})
	response := startProcessResponse{}
	if p != nil {
		response.Process = redactProcess(t.env, *p)
		response.Action = processActionStart
		response.NextSuggestions = processStartNextSuggestions(args.WaitMS, args.CompletionMode)
		if startErr == nil && args.WaitMS > 0 {
			wait := time.Duration(args.WaitMS) * time.Millisecond
			if wait > maxStartProcessInitialWait {
				wait = maxStartProcessInitialWait
			}
			offset := int64(0)
			snapshot, readErr := m.ReadOutputSnapshot(ctx, p.ID, proc.OutputReadOptions{
				MaxBytes:    args.MaxBytes,
				OffsetBytes: &offset,
				Wait:        wait,
			})
			if readErr != nil {
				response.LastError = t.env.RedactToolOutput(readErr.Error())
			} else {
				process := markProcessCompletionObserved(m, snapshot.Process)
				response.Process = redactProcess(t.env, process)
				response.Action = processActionStart
				response.InitialOutput = t.env.RedactToolOutput(snapshot.Output)
				response.InitialTruncated = snapshot.Truncated
				response.InitialStartOffset = snapshot.StartOffset
				response.InitialEndOffset = snapshot.EndOffset
				response.InitialTotalBytes = snapshot.TotalBytes
				response.InitialTimedOut = snapshot.TimedOut
				response.InitialDurationMS = snapshot.Duration.Milliseconds()
			}
		}
	}
	out, _ := mustJSON(response)
	if startErr != nil {
		return out, startErr
	}
	return out, nil
}

func processStartNextSuggestions(waitMS int, completionMode string) []string {
	if strings.TrimSpace(completionMode) == string(proc.CompletionModeDetached) {
		return []string{"this process is detached and will not start another model turn when it exits", "use process action=read snapshots only when you explicitly need its output"}
	}
	if waitMS <= 0 {
		return []string{"continue independent work; if completion is the only remaining dependency, end this turn now and the natural exit will start a new turn automatically", "process action=read without wait_ms gives a non-blocking snapshot whenever you need progress; for a long silent task, schedule wake-ups with process action=update and recheck_minutes"}
	}
	return []string{"continue independent work; if completion is the only remaining dependency, end this turn now and the natural exit will start a new turn automatically", "when the still-running process needs more output, pass initial_end_offset as offset_bytes to process action=read — a bounded wait_ms is fine, but do not chain waits to keep this turn open"}
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

// processReadNextSuggestions turns the wait outcome into in-context guidance.
// The chatty hint fires exactly when it matters: a wait released right at the
// pacing floor with new output, meaning the process produces output
// continuously and repeated waits would spin.
func processReadNextSuggestions(waitMS int, minDwell time.Duration, snapshot proc.OutputSnapshot, process proc.Process) []string {
	live := process.Status == proc.StatusStarting || process.Status == proc.StatusRunning || process.Status == proc.StatusStopping
	if !live {
		return []string{"process is terminal; page remaining output with offset_bytes when the tail is insufficient — no further waits are useful"}
	}
	if waitMS <= 0 {
		return nil
	}
	if snapshot.TimedOut {
		return []string{
			"the wait expired with the process still running; do not immediately wait again on the same process — continue other work or end this turn and let the completion notification start the next turn",
			"for a long silent task, schedule a wake-up instead: process action=update with process_id and recheck_minutes",
		}
	}
	if snapshot.Duration <= minDwell+time.Second {
		return []string{
			"this process is producing output continuously (the wait returned after ~" + snapshot.Duration.Round(time.Second).String() + "); do not chain waits on it — use non-blocking snapshots when you need progress and rely on the completion notification or recheck_minutes for wake-ups",
		}
	}
	return []string{"process is still running; pass end_offset as offset_bytes if you wait for more output again"}
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
	p, err := m.SetRecheck(args.ProcessID, args.RecheckMinutes)
	if err != nil {
		return "", err
	}
	next := []string{}
	if p.RecheckMinutes > 0 {
		next = append(next, "progress wake-ups are scheduled every "+strconv.Itoa(p.RecheckMinutes)+" minute(s) until the process completes; each wake-up starts a new turn with a status/output snapshot")
	} else {
		next = append(next, "recheck schedule cancelled; the completion notification still starts a new turn when the process exits")
	}
	return mustJSON(map[string]any{
		"action":           processActionUpdate,
		"process_id":       args.ProcessID,
		"recheck_minutes":  p.RecheckMinutes,
		"next_recheck_at":  p.NextRecheckAt,
		"process":          redactProcessPtr(t.env, p),
		"next_suggestions": next,
	})
}
