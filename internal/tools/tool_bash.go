package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// BashTool is the only model-facing way to start a command on the Codex /
// GPT / Claude / generic surfaces. A foreground run is bounded and
// non-interactive; run_in_background hands the command to the process
// manager; bash control actions observe or control it. A foreground
// run that exceeds the foreground wait budget is also handed over.
//
// Keeping every launch in one tool follows the bash-first surface: the model
// never chooses between competing entry points to start a command.
//
// The model sees a plain-text view (see command_model_view.go); the JSON
// envelope produced here is what clients render and durable records keep.
type BashTool struct{ env *Env }

func NewBashTool(env *Env) *BashTool { return &BashTool{env: env} }

func (t *BashTool) Name() string            { return "bash" }
func (t *BashTool) IsReadOnly() bool        { return false }
func (t *BashTool) IsConcurrencySafe() bool { return false }

func (t *BashTool) Classify(argsJSON string) ToolClassification {
	var args bashArgs
	if err := decodeBashArgs(argsJSON, &args); err != nil {
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskHigh,
			Reason:          "invalid bash invocation",
		}
	}
	if action := strings.TrimSpace(args.Action); action != "" && action != "run" {
		switch action {
		case processActionList, processActionRead:
			return ToolClassification{ReadOnly: true, ConcurrencySafe: true, Risk: ToolRiskMedium, Reason: "background command observation"}
		case processActionWrite, processActionStop, processActionUpdate:
			return ToolClassification{ConcurrencySafe: true, Risk: ToolRiskHigh, Reason: "background command control"}
		default:
			return highRiskShellClassification("invalid bash action", false)
		}
	}
	classification := classifyShellCommand(args.Command)
	if !args.RunInBackground {
		return classification
	}
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
}

func (t *BashTool) ValidateInput(argsJSON string) error {
	var args bashArgs
	if err := decodeBashArgs(argsJSON, &args); err != nil {
		return err
	}
	return args.validate()
}

func (t *BashTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "bash",
		Description: "Run a bash command in the workspace: tests, builds, lint, git, package managers, scripts. " +
			"Returns stdout and stderr (head and tail of long output, with the full log path), plus the exit code when it is not 0. " +
			"For Code Mode, structured_content exposes stdout, stderr (up to 64 KiB per stream), exit_code, timed_out, stdout_truncated, stderr_truncated and full_log_ref for recovery. Select fields in JS before emitting. " +
			"Each run starts a fresh shell. action defaults to run; read returns output, write sends stdin, stop cancels, list shows commands, and update changes completion_mode. " +
			fmt.Sprintf("Commands must not wait for input. Foreground waits yield after %s (or timeout_seconds if shorter) when background handoff is available. The command keeps running and its completion starts a new turn; do not rerun it or treat a process ID as a completed verification. ", maxForegroundShellWait) +
			"Set run_in_background for builds or servers you need to run concurrently instead of appending '&'. " +
			"completion_mode=resume (default) delivers the exit result; detached keeps servers from resuming the conversation. " +
			"Inspect output when needed; completion arrives automatically, so do not poll or chain waits. " +
			"Prefer read_file, grep, glob, and the edit tools over shell equivalents for reading, searching, and changing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":          map[string]any{"type": "string", "enum": []string{"run", "read", "write", "stop", "list", "update"}, "description": "Default run. command is required for run; process_id is required for read/write/stop/update."},
				"process_id":      map[string]any{"type": "string", "description": "Handle returned by a background run or foreground handoff."},
				"wait_ms":         map[string]any{"type": "integer", "minimum": 0, "maximum": readProcessMaxWaitMS, "description": "For read: bounded wait for output or exit; omit for a snapshot."},
				"offset_bytes":    map[string]any{"type": "integer", "minimum": 0, "description": "For read: byte cursor; pass the previous end_offset."},
				"max_bytes":       map[string]any{"type": "integer", "description": "For read: maximum output bytes, default 32768."},
				"input":           map[string]any{"type": "string", "description": "For write: stdin text, including a trailing newline when needed."},
				"completion_mode": map[string]any{"type": "string", "enum": []string{"resume", "detached"}, "description": "For a background run or update: resume delivers the exit result; detached does not."},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Foreground timeout in seconds (1-3600, default %d). With background handoff, waits yield after at most %s; this does not limit the background process lifetime.", defaultShellTimeoutSeconds, maxForegroundShellWait),
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory. Defaults to the workspace root.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Start the command as a managed background process and return its process_id immediately.",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "Optional one-line reason, kept in the audit log only.",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"targeted", "affected", "full"},
					"description": "Optional coverage label for test/build/lint commands. Defaults to targeted.",
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	result, err := t.ExecuteResult(ctx, argsJSON)
	return result.TextProjection(), err
}

func (t *BashTool) ExecuteResult(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	var args bashArgs
	if err := decodeBashArgs(argsJSON, &args); err != nil {
		return toolresult.Result{}, err
	}
	if err := args.validate(); err != nil {
		return toolresult.Result{}, err
	}
	if action := strings.TrimSpace(args.Action); action != "" && action != "run" {
		text, err := t.executeControl(ctx, argsJSON)
		return toolresult.FromText(text), err
	}
	if args.RunInBackground {
		text, err := t.executeStartBackground(ctx, args)
		return toolresult.FromText(text), err
	}
	return t.executeRun(ctx, args)
}

func decodeBashArgs(raw string, args *bashArgs) error {
	if !json.Valid([]byte(raw)) {
		return errors.New("invalid bash arguments: expected a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(args); err != nil {
		return fmt.Errorf("invalid bash arguments: %w", err)
	}
	return nil
}

type bashArgs struct {
	Command         string `json:"command"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Purpose         string `json:"purpose"`
	Scope           string `json:"scope"`
	CWD             string `json:"cwd"`
	RunInBackground bool   `json:"run_in_background"`
	processArgs
}

func (args bashArgs) validate() error {
	switch action := strings.TrimSpace(args.Action); action {
	case "", "run":
	case processActionList, processActionRead, processActionWrite, processActionStop, processActionUpdate:
		if args.Command != "" || args.RunInBackground || args.CWD != "" || args.TimeoutSeconds != 0 || args.Scope != "" {
			return errors.New("bash control actions do not accept command, cwd, run_in_background, timeout_seconds or scope")
		}
		return args.processArgs.validate()
	default:
		return fmt.Errorf("bash does not accept action %q", action)
	}
	if args.CompletionMode != "" && args.CompletionMode != string(proc.CompletionModeResume) && args.CompletionMode != string(proc.CompletionModeDetached) {
		return errors.New("completion_mode must be resume or detached")
	}
	if args.CompletionMode != "" && !args.RunInBackground {
		return errors.New("completion_mode on run requires run_in_background=true")
	}
	if args.ProcessID != "" || args.WaitMS != 0 || args.MaxBytes != 0 || args.OffsetBytes != nil || args.Input != "" {
		return errors.New("process_id, wait_ms, max_bytes, offset_bytes and input require a bash control action")
	}
	if strings.TrimSpace(args.Command) == "" {
		return errors.New("bash requires command")
	}
	return nil
}

func (t *BashTool) executeStartBackground(ctx context.Context, args bashArgs) (string, error) {
	completion := proc.CompletionMode(args.CompletionMode)
	if completion == "" {
		completion = proc.CompletionModeResume
	}
	p, err := t.startBackground(ctx, args, true, completion)
	response := proc.Process{}
	if p != nil {
		response = redactProcess(t.env, *p)
		response.Action = bashResultActionStart
	}
	out, _ := mustJSON(response)
	return out, err
}

func (t *BashTool) startBackground(ctx context.Context, args bashArgs, tty bool, completion proc.CompletionMode) (*proc.Process, error) {
	if len(t.env.ProtectedPaths) > 0 {
		return nil, errors.New("background commands require durable protected read/write policy tracking, which is unavailable")
	}
	commandPrefix := ""
	if t.env.gitAttributionEnabled() {
		var err error
		commandPrefix, err = t.env.gitAttributionShellPrefix()
		if err != nil {
			return nil, err
		}
	}
	rootThreadID := processRootThreadID(t.env)
	if rootThreadID == "" {
		return nil, errors.New("bash run_in_background requires a bound session ID")
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return nil, err
	}
	sandboxPolicy, sandboxTempDir, err := t.env.processSandboxPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare filesystem process sandbox: %w", err)
	}
	commandEnv := shellCommandEnv(os.Environ())
	if sandboxTempDir != "" {
		commandEnv = replaceCommandEnv(commandEnv, "TMPDIR", sandboxTempDir)
	}
	// Ordinary bash keeps its existing start handoff after policy preparation.
	// Extension starts remain cancellable until their generation takes ownership.
	startCtx := ctx
	if completion == proc.CompletionModeResume {
		startCtx = context.WithoutCancel(ctx)
	}
	// Ordinary background commands use a terminal; streaming consumers use pipes.
	return m.Start(startCtx, proc.StartOptions{
		Command:               args.Command,
		CommandPrefix:         commandPrefix,
		CWD:                   args.CWD,
		WorkspaceRoot:         t.env.RootDir,
		OwnerKind:             proc.OwnerKind(defaultProcessOwnerKind(t.env, "")),
		OwnerID:               defaultProcessOwnerID(t.env),
		RootThreadID:          rootThreadID,
		Lifecycle:             proc.LifecycleManaged,
		TTY:                   tty,
		CompletionMode:        completion,
		AllowOutsideWorkspace: t.env.BypassToolHardProtections(),
		SandboxPolicy:         sandboxPolicy,
		SandboxProvider:       t.env.ProcessSandboxProvider,
		Env:                   commandEnv,
	})
}

// bashResultActionStart marks the envelope of a background start. Clients use
// it to bind the tool call to its managed process.
const bashResultActionStart = "start"

type bashVerificationResult struct {
	Kind              string             `json:"kind"`
	Scope             string             `json:"scope"`
	Passed            bool               `json:"passed"`
	FailureSummary    testFailureSummary `json:"failure_summary"`
	WorkspaceRevision string             `json:"workspace_revision,omitempty"`
	CommandHash       string             `json:"command_hash,omitempty"`
	NextSuggestions   []string           `json:"next_suggestions,omitempty"`
}

func (t *BashTool) executeRun(ctx context.Context, args bashArgs) (toolresult.Result, error) {
	if len(args.Command) == 0 || len(bytes.TrimSpace([]byte(args.Command))) == 0 {
		return toolresult.Result{}, errors.New("bash requires command")
	}
	command := strings.TrimSpace(args.Command)
	runCWD, err := resolveShellWorkingDir(ctx, t.env, args.CWD)
	if err != nil {
		return toolresult.Result{}, err
	}
	verification := bashCommandLooksLikeVerification(command)
	resolved := resolvedRunTestCommand{Requested: command, Command: command}
	if verification {
		resolved, err = resolveRunTestCommand(runCWD, command)
		if err != nil {
			return toolresult.Result{}, err
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

	revision := t.env.workspaceRevision(ctx, t.env.RevisionRoot(ctx))
	// Identical commands in different directories verify different suites.
	commandHash := sha256Hex([]byte(runCWD + "\x00" + command))
	result, err := executeShellCommandInDir(ctx, t.env, command, timeout, runCWD)
	if err != nil {
		return toolresult.Result{}, err
	}
	result.Purpose = t.env.RedactToolOutput(args.Purpose)
	fullLogRef, fullLogBytes, fullLogSections, fullLogSHA256, fullLogErr := persistShellLog(t.env.SessionDir, result)
	if fullLogRef != "" {
		result.FullLogRef = fullLogRef
		result.FullLogBytes = fullLogBytes
		result.FullLogSHA256 = fullLogSHA256
		result.FullLogSections = fullLogSections
	} else if fullLogErr != "" {
		result.FullLogError = fullLogErr
	}
	if verification {
		// Promotion ends the foreground wait, not the verification. Preserve
		// the running-process guidance until a completion result is available.
		if result.PromotedProcessID == "" {
			result.Verification = enrichVerificationResult(commandHash, revision, args, result)
			result.NextSuggestions = result.Verification.NextSuggestions
		}
		if resolved.Changed {
			result.RequestedCommand = t.env.RedactToolOutput(resolved.Requested)
			result.ResolvedCommand = result.Command
		}
	}
	// These are transport bounds, independent of the much smaller display
	// excerpts. Two maximally JSON-escaped streams still fit the 1 MiB data
	// contract; larger output remains recoverable from the redacted full log.
	const streamDataBytes = 64 * 1024
	stdout, stdoutTruncated := truncate(result.redactedStdout, streamDataBytes)
	stderr, stderrTruncated := truncate(result.redactedStderr, streamDataBytes)
	return toolResultWithData(result, map[string]any{
		"exit_code": result.ExitCode, "timed_out": result.TimedOut,
		"stdout": stdout, "stderr": stderr,
		"stdout_truncated": stdoutTruncated, "stderr_truncated": stderrTruncated,
		"full_log_ref": result.FullLogRef, "full_log_sha256": result.FullLogSHA256,
		"full_log_sections":   result.FullLogSections,
		"promoted_process_id": result.PromotedProcessID,
	})
}

func bashCommandLooksLikeVerification(command string) bool {
	if testCommandLooksLikeLocalRunnerVerification(command) {
		return true
	}
	classification := classifyShellCommand(command)
	return classification.Risk == ToolRiskMedium && classification.Reason == "local verification command"
}

func enrichVerificationResult(commandHash, revision string, args bashArgs, shellResult shellExecutionResult) *bashVerificationResult {
	scope := strings.TrimSpace(args.Scope)
	if scope == "" {
		scope = "targeted"
	}
	failureSummary := summarizeTestFailure(shellResult.Output)
	if shellResult.ExitCode != 0 {
		failureSummary.Failed = true
	}
	return &bashVerificationResult{
		Kind:              "verification",
		Scope:             scope,
		Passed:            shellResult.ExitCode == 0 && !shellResult.TimedOut,
		FailureSummary:    failureSummary,
		WorkspaceRevision: revision,
		CommandHash:       commandHashPrefix(commandHash),
		NextSuggestions:   runTestNextSuggestions(shellResult, failureSummary),
	}
}
