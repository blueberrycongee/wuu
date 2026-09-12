package tools

import (
	"bytes"
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
	bashActionRun              = "run"
	bashActionStartBackground  = "start_background"
	bashActionListBackground   = "list_background"
	bashActionReadBackground   = "read_background"
	bashActionWriteBackground  = "write_background"
	bashActionStopBackground   = "stop_background"
	bashActionUpdateBackground = "update_background"
)

const (
	// readBackgroundMaxWaitMS caps one bounded event-driven wait at 5 minutes.
	readBackgroundMaxWaitMS = 300_000
	// backgroundWaitMinDwell paces output-driven early returns so a
	// continuously producing process releases a wait at most this often.
	backgroundWaitMinDwell = 5 * time.Second
)

// BashTool is the bash-first command tool exposed to the model on
// the Codex / GPT / Claude / generic surfaces. It exposes one
// unified terminal entry point for bounded commands, verification,
// and managed background processes.
//
// Implementation note: short-lived commands use executeShellCommandWithCWD,
// verification commands get result enrichment as a post-processor,
// and long-running or interactive commands use the managed process backend.
type BashTool struct{ env *Env }

func NewBashTool(env *Env) *BashTool { return &BashTool{env: env} }

func (t *BashTool) Name() string            { return "bash" }
func (t *BashTool) IsReadOnly() bool        { return false }
func (t *BashTool) IsConcurrencySafe() bool { return false }

func (t *BashTool) Classify(argsJSON string) ToolClassification {
	var args bashArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskHigh,
			Reason:          "invalid bash invocation",
		}
	}
	switch normalizeBashAction(args) {
	case bashActionRun:
		return classifyShellCommand(args.Command)
	case bashActionStartBackground:
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
	case bashActionListBackground, bashActionReadBackground:
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            ToolRiskMedium,
			Reason:          "managed background process observation",
		}
	case bashActionWriteBackground, bashActionStopBackground, bashActionUpdateBackground:
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: true,
			Risk:            ToolRiskHigh,
			Reason:          "managed background process control",
		}
	default:
		return highRiskShellClassification("invalid bash action", false)
	}
}

func (t *BashTool) ValidateInput(argsJSON string) error {
	var args bashArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	switch normalizeBashAction(args) {
	case bashActionRun, bashActionStartBackground:
		if strings.TrimSpace(args.Command) == "" {
			return errors.New("bash requires command")
		}
	case bashActionListBackground:
		return nil
	case bashActionReadBackground, bashActionWriteBackground, bashActionStopBackground, bashActionUpdateBackground:
		if strings.TrimSpace(args.ProcessID) == "" {
			return errors.New("bash background action requires process_id")
		}
	default:
		return errors.New("bash action must be one of run, start_background, list_background, read_background, write_background, stop_background, update_background")
	}
	return nil
}

func (t *BashTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "bash",
		Description: "Run bash operations in the workspace. Use for terminal work: tests, lint, builds, git, package managers, scripts, docker, and managed background processes.\n\n" +
			"Commands may run under the session's filesystem process sandbox. A failed result with sandbox.denied=true means the OS blocked a file write outside the current boundary; do not retry the same access through another command. Wuu does not offer per-call escalation or approval cards: use a registered workspace path, ask the user to add the required directory as a workspace, or explain that the user must explicitly switch the session to unconfined mode.\n\n" +
			"Prefer dedicated file/search/edit tools for reading, searching, or changing files. Default action=run is non-interactive and returns exit_code, duration_ms, workspace_revision, output tails, and full_log_ref when available; verification commands add verification metadata. If a run command hits its timeout it keeps running as a managed background process instead of being killed, with its output so far attached. action=start_background uses an interactive pseudo-terminal by default so long-lived commands can be taken over from a terminal UI; set tty=false only for log-only automation that must run without terminal semantics. Use action=write_background to send input and action=read_background to read output. Use the background actions for long-lived processes as well. cwd defaults to the workspace root. Shell state does not persist between run calls.\n\n" +
			"Wake-ups always come to you — polling is never the only way to learn an outcome: a naturally exiting background process starts a new turn with its status and output tail, and a process started with recheck_minutes additionally wakes you with a progress snapshot on that schedule (the schedule is cancelled automatically on completion). Observation on demand: read_background without wait_ms is a non-blocking snapshot and is always fine; with wait_ms it becomes a bounded event-driven wait that returns early when new output arrives or the process exits (at most one return per call). Rules for waits: do not wait on a process you just launched this turn when its result gates your next step — run that work with action=run instead; if a wait times out with the process still running, do not immediately wait again on the same process — continue other work or end the turn; never chain waits just to keep a turn open. For long silent tasks (downloads, backups), set recheck_minutes on start_background or later via action=update_background so progress finds you on a schedule.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{bashActionRun, bashActionStartBackground, bashActionListBackground, bashActionReadBackground, bashActionWriteBackground, bashActionStopBackground, bashActionUpdateBackground},
					"description": "Operation. Defaults to run; background actions manage long-lived processes.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute or start in the background. action=run must be non-interactive and must not rely on editors, pagers, or terminal prompts. action=start_background is interactive by default; set tty=false only for log-only automation. Do not background with '&'.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Max runtime in seconds for action=run (1-3600).",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "Why this command is needed. Stored in redacted logs for replay and audit.",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"targeted", "affected", "full"},
					"description": "Verification scope for test/build/lint/typecheck commands. Defaults to targeted.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for run/start_background. Defaults to workspace root.",
				},
				"lifecycle": map[string]any{
					"type":        "string",
					"enum":        []string{"session", "managed"},
					"description": "Background process lifecycle. Defaults to session.",
				},
				"completion_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"resume", "detached"},
					"description": "What happens when the process exits. resume (default) starts another model turn and keeps wuu exec alive until that continuation finishes. Use detached only for long-lived services whose exit must not block or resume the current task.",
				},
				"tty": map[string]any{
					"type":        "boolean",
					"description": "Run action=start_background in a pseudo-terminal. Defaults to true so live commands preserve terminal colors and interaction; set false only for log-only automation.",
				},
				"wait_ms": map[string]any{
					"type":        "integer",
					"description": "For background start/read: wait this many milliseconds for new output before returning. The wait returns early when output arrives (paced to at most one return per ~5s for continuously producing processes) or immediately when the process exits; it never exceeds the deadline. Max 60000 for start, 300000 for read. Do not chain waits to sit on a process — completion notifications and recheck_minutes cover wake-ups.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum background output bytes to return. Default 32768.",
				},
				"offset_bytes": map[string]any{
					"type":        "integer",
					"description": "For read_background: byte offset to read from. Use the previous end_offset for incremental logs.",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Managed background process id for read_background/write_background/stop_background/update_background.",
				},
				"recheck_minutes": map[string]any{
					"description": "Optional. For start_background/update_background: minutes between scheduled progress wake-ups for this process (1-1440); 0 cancels the schedule. Each wake-up delivers a status/output snapshot in a new turn, and completing the process cancels the schedule automatically. Pick an interval proportional to the expected duration — around 5 minutes for short tasks, much longer for big downloads.",
					"type":        "integer",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "Text to write to background process input for action=write_background. Include a trailing newline when needed.",
				},
			},
			"required": []string{},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args bashArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	switch normalizeBashAction(args) {
	case bashActionRun:
		return t.executeRun(ctx, args)
	case bashActionStartBackground:
		return t.executeStartBackground(ctx, args)
	case bashActionListBackground:
		return t.executeListBackground()
	case bashActionReadBackground:
		return t.executeReadBackground(ctx, args)
	case bashActionWriteBackground:
		return t.executeWriteStdin(args)
	case bashActionStopBackground:
		return t.executeStopBackground(args)
	case bashActionUpdateBackground:
		return t.executeUpdateBackground(args)
	default:
		return "", errors.New("bash action must be one of run, start_background, list_background, read_background, write_background, stop_background, update_background")
	}
}

type bashArgs struct {
	Action         string `json:"action"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Purpose        string `json:"purpose"`
	Scope          string `json:"scope"`
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
	Background     bool   `json:"background"`
	RecheckMinutes int    `json:"recheck_minutes"`
}

func normalizeBashAction(args bashArgs) string {
	action := strings.TrimSpace(args.Action)
	switch action {
	case "":
		if args.Background {
			return bashActionStartBackground
		}
		return bashActionRun
	default:
		return action
	}
}

type bashVerificationResult struct {
	Kind              string             `json:"kind"`
	Scope             string             `json:"scope"`
	Passed            bool               `json:"passed"`
	FailureSummary    testFailureSummary `json:"failure_summary"`
	WorkspaceRevision string             `json:"workspace_revision,omitempty"`
	RepeatGuard       map[string]any     `json:"repeat_guard,omitempty"`
	CommandHash       string             `json:"command_hash,omitempty"`
	NextSuggestions   []string           `json:"next_suggestions,omitempty"`
}

func (t *BashTool) executeRun(ctx context.Context, args bashArgs) (string, error) {
	if len(args.Command) == 0 || len(bytes.TrimSpace([]byte(args.Command))) == 0 {
		return "", errors.New("bash requires command")
	}
	command := strings.TrimSpace(args.Command)
	runCWD, err := resolveShellWorkingDir(ctx, t.env, args.CWD)
	if err != nil {
		return "", err
	}
	verification := bashCommandLooksLikeVerification(command)
	resolved := resolvedRunTestCommand{Requested: command, Command: command}
	if verification {
		resolved, err = resolveRunTestCommand(runCWD, command)
		if err != nil {
			return "", err
		}
		command = resolved.Command
	}

	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellTimeoutSeconds
	}
	if timeout > maxShellTimeoutSeconds {
		timeout = maxShellTimeoutSeconds
	}

	revision := workspaceRevision(ctx, t.env.RevisionRoot(ctx))
	commandHash := sha256Hex([]byte(command))
	if verification && revision != "" {
		previousFailures := t.env.ConsecutiveTestFailures(commandHash, revision)
		if previousFailures >= maxRepeatedRunTestFailures {
			return "", repeatedBashVerificationFailureError{
				PreviousFailures: previousFailures,
				MaxFailures:      maxRepeatedRunTestFailures,
				Revision:         revision,
				CommandHash:      commandHash,
			}
		}
	}

	result, err := executeShellCommandInDir(ctx, t.env, command, timeout, runCWD)
	if err != nil {
		return "", err
	}
	result.Purpose = t.env.RedactToolOutput(args.Purpose)
	fullLogRef, fullLogBytes, fullLogSections, fullLogErr := persistShellLog(t.env.SessionDir, result)
	if fullLogRef != "" {
		result.FullLogRef = fullLogRef
		result.FullLogBytes = fullLogBytes
		result.FullLogSections = fullLogSections
	} else if fullLogErr != "" {
		result.FullLogError = fullLogErr
	}
	if verification {
		result.Verification = t.enrichVerificationResult(command, commandHash, revision, args, result)
		result.NextSuggestions = result.Verification.NextSuggestions
		if resolved.Changed {
			result.RequestedCommand = t.env.RedactToolOutput(resolved.Requested)
			result.ResolvedCommand = result.Command
		}
	}
	return mustJSON(result)
}

func bashCommandLooksLikeVerification(command string) bool {
	if testCommandLooksLikeLocalRunnerVerification(command) {
		return true
	}
	classification := classifyShellCommand(command)
	return classification.Risk == ToolRiskMedium && classification.Reason == "local verification command"
}

func (t *BashTool) enrichVerificationResult(command, commandHash, revision string, args bashArgs, shellResult shellExecutionResult) *bashVerificationResult {
	scope := strings.TrimSpace(args.Scope)
	if scope == "" {
		scope = "targeted"
	}
	failureSummary := summarizeTestFailure(shellResult.Output)
	if shellResult.ExitCode != 0 {
		failureSummary.Failed = true
	}
	failed := shellResult.ExitCode != 0 || shellResult.TimedOut || failureSummary.Failed
	previousFailures := 0
	if revision != "" {
		previousFailures = t.env.ConsecutiveTestFailures(commandHash, revision)
	}
	t.env.RecordTestRunResult(testRunEntry{
		CommandHash:    commandHash,
		Revision:       revision,
		Failed:         failed,
		Command:        command,
		Scope:          scope,
		Purpose:        args.Purpose,
		ExitCode:       shellResult.ExitCode,
		TimedOut:       shellResult.TimedOut,
		DurationMS:     shellResult.DurationMS,
		FailureSummary: failureSummary,
		FullLogRef:     shellResult.FullLogRef,
	})
	return &bashVerificationResult{
		Kind:              "verification",
		Scope:             scope,
		Passed:            shellResult.ExitCode == 0 && !shellResult.TimedOut,
		FailureSummary:    failureSummary,
		WorkspaceRevision: revision,
		CommandHash:       commandHashPrefix(commandHash),
		RepeatGuard: map[string]any{
			"previous_failed_runs":                    previousFailures,
			"max_failed_runs_without_revision_change": maxRepeatedRunTestFailures,
		},
		NextSuggestions: runTestNextSuggestions(shellResult, failureSummary),
	}
}

type repeatedBashVerificationFailureError struct {
	PreviousFailures int
	MaxFailures      int
	Revision         string
	CommandHash      string
}

func (e repeatedBashVerificationFailureError) Error() string {
	return fmt.Sprintf(
		"bash blocked repeated failing verification command: error_kind=repeated_failure_same_revision previous_failed_runs=%d max_failed_runs_without_revision_change=%d workspace_revision=%s command_hash=%s safe_retry=%q model_next_action=%q",
		e.PreviousFailures,
		e.MaxFailures,
		e.Revision,
		commandHashPrefix(e.CommandHash),
		"change code, narrow the command, or inspect verification.failure_summary/full_log_ref before rerunning",
		"read the latest failure evidence, form a new hypothesis, patch minimally, then rerun targeted verification after the workspace revision changes",
	)
}

func (t *BashTool) executeStartBackground(ctx context.Context, args bashArgs) (string, error) {
	if strings.TrimSpace(args.Command) == "" {
		return "", errors.New("bash requires command")
	}
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
		return "", errors.New("bash start_background requires a bound session ID")
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
		response.Action = bashActionStartBackground
		response.NextSuggestions = bashBackgroundNextSuggestions(args.WaitMS, args.CompletionMode)
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
				response.Action = bashActionStartBackground
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

func bashBackgroundNextSuggestions(waitMS int, completionMode string) []string {
	if strings.TrimSpace(completionMode) == string(proc.CompletionModeDetached) {
		return []string{"this process is detached and will not start another model turn when it exits", "use bash action=read_background snapshots only when you explicitly need its output"}
	}
	if waitMS <= 0 {
		return []string{"continue independent work; if completion is the only remaining dependency, end this turn now and the natural exit will start a new turn automatically", "read_background without wait_ms gives a non-blocking snapshot whenever you need progress; for a long silent task, schedule wake-ups with bash action=update_background and recheck_minutes"}
	}
	return []string{"continue independent work; if completion is the only remaining dependency, end this turn now and the natural exit will start a new turn automatically", "when the still-running process needs more output, pass initial_end_offset as offset_bytes to bash action=read_background — a bounded wait_ms is fine, but do not chain waits to keep this turn open"}
}

func (t *BashTool) executeListBackground() (string, error) {
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
		"action":    bashActionListBackground,
		"processes": redacted,
	})
}

func (t *BashTool) executeReadBackground(ctx context.Context, args bashArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	waitMS := args.WaitMS
	if waitMS > readBackgroundMaxWaitMS {
		waitMS = readBackgroundMaxWaitMS
	}
	wait := time.Duration(waitMS) * time.Millisecond
	minDwell := time.Duration(0)
	if wait > 0 {
		minDwell = backgroundWaitMinDwell
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
		"action":           bashActionReadBackground,
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
		"next_suggestions": bashReadBackgroundNextSuggestions(waitMS, minDwell, snapshot, process),
	})
}

// bashReadBackgroundNextSuggestions turns the wait outcome into in-context
// guidance. The chatty hint fires exactly when it matters: a wait released
// right at the pacing floor with new output, meaning the process produces
// output continuously and repeated waits would spin.
func bashReadBackgroundNextSuggestions(waitMS int, minDwell time.Duration, snapshot proc.OutputSnapshot, process proc.Process) []string {
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
			"for a long silent task, schedule a wake-up instead: bash action=update_background with process_id and recheck_minutes",
		}
	}
	if snapshot.Duration <= minDwell+time.Second {
		return []string{
			"this process is producing output continuously (the wait returned after ~" + snapshot.Duration.Round(time.Second).String() + "); do not chain waits on it — use non-blocking snapshots when you need progress and rely on the completion notification or recheck_minutes for wake-ups",
		}
	}
	return []string{"process is still running; pass end_offset as offset_bytes if you wait for more output again"}
}

func (t *BashTool) executeWriteStdin(args bashArgs) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	p, err := m.WriteStdin(args.ProcessID, args.Input)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{
		"action":        bashActionWriteBackground,
		"process_id":    args.ProcessID,
		"bytes_written": len(args.Input),
		"process":       redactProcessPtr(t.env, p),
	})
}

func (t *BashTool) executeStopBackground(args bashArgs) (string, error) {
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
		redacted.Action = bashActionStopBackground
	}
	return mustJSON(redacted)
}

func (t *BashTool) executeUpdateBackground(args bashArgs) (string, error) {
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
		"action":           bashActionUpdateBackground,
		"process_id":       args.ProcessID,
		"recheck_minutes":  p.RecheckMinutes,
		"next_recheck_at":  p.NextRecheckAt,
		"process":          redactProcessPtr(t.env, p),
		"next_suggestions": next,
	})
}
