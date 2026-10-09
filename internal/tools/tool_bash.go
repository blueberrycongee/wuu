package tools

import (
	"bytes"
	"context"
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
// manager, and the process tool then observes or controls it. A foreground
// run that outlives its timeout is also handed over instead of being killed.
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
	if err := decodeArgs(argsJSON, &args); err != nil {
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskHigh,
			Reason:          "invalid bash invocation",
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
	if err := decodeArgs(argsJSON, &args); err != nil {
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
			"Each call starts a fresh shell; state does not persist between calls. " +
			"Commands must not wait for input. A command that exceeds its timeout keeps running in the background and its completion starts a new turn; do not rerun it. " +
			"Set run_in_background for servers, watchers, and other long-lived commands instead of appending '&'; " +
			"you are notified when the process exits, so do not poll. Use the process tool to read its output, send input, or stop it. " +
			"Prefer read_file, grep, glob, and the edit tools over shell equivalents for reading, searching, and changing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Max runtime in seconds for a foreground run (1-3600, default 300).",
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
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	result, err := t.ExecuteResult(ctx, argsJSON)
	return result.TextProjection(), err
}

func (t *BashTool) ExecuteResult(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	var args bashArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return toolresult.Result{}, err
	}
	if err := args.validate(); err != nil {
		return toolresult.Result{}, err
	}
	if args.RunInBackground {
		text, err := t.executeStartBackground(ctx, args)
		return toolresult.FromText(text), err
	}
	return t.executeRun(ctx, args)
}

type bashArgs struct {
	Command         string `json:"command"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Purpose         string `json:"purpose"`
	Scope           string `json:"scope"`
	CWD             string `json:"cwd"`
	RunInBackground bool   `json:"run_in_background"`
	// Action is not part of the schema. Sessions recorded before the process
	// tool existed show bash background actions in their history, and a
	// resumed model may imitate them; they get a pointed error instead of a
	// silent foreground run. Remove once such sessions have aged out.
	Action string `json:"action"`
}

func (args bashArgs) validate() error {
	switch action := strings.TrimSpace(args.Action); action {
	case "", "run":
	case "start_background":
		return errors.New("bash no longer takes action; set run_in_background=true to start a background process")
	case "list_background", "read_background", "write_background", "stop_background", "update_background":
		return fmt.Errorf("bash only starts commands; use the process tool with action=%s", strings.TrimSuffix(action, "_background"))
	default:
		return fmt.Errorf("bash does not accept action %q", action)
	}
	if strings.TrimSpace(args.Command) == "" {
		return errors.New("bash requires command")
	}
	return nil
}

func (t *BashTool) executeStartBackground(ctx context.Context, args bashArgs) (string, error) {
	commandPrefix := ""
	if t.env.gitAttributionEnabled() {
		var err error
		commandPrefix, err = t.env.gitAttributionShellPrefix()
		if err != nil {
			return "", err
		}
	}
	rootThreadID := processRootThreadID(t.env)
	if rootThreadID == "" {
		return "", errors.New("bash run_in_background requires a bound session ID")
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	sandboxPolicy, sandboxTempDir, err := t.env.processSandboxPolicy(ctx)
	if err != nil {
		return "", fmt.Errorf("prepare filesystem process sandbox: %w", err)
	}
	commandEnv := shellCommandEnv(os.Environ())
	if sandboxTempDir != "" {
		commandEnv = replaceCommandEnv(commandEnv, "TMPDIR", sandboxTempDir)
	}
	// Background commands run in a pseudo-terminal so the desktop can take
	// them over interactively; the model view strips terminal control codes.
	p, startErr := m.Start(context.WithoutCancel(ctx), proc.StartOptions{
		Command:               args.Command,
		CommandPrefix:         commandPrefix,
		CWD:                   args.CWD,
		WorkspaceRoot:         t.env.RootDir,
		OwnerKind:             proc.OwnerKind(defaultProcessOwnerKind(t.env, "")),
		OwnerID:               defaultProcessOwnerID(t.env),
		RootThreadID:          rootThreadID,
		Lifecycle:             proc.LifecycleManaged,
		TTY:                   true,
		AllowOutsideWorkspace: t.env.BypassToolHardProtections(),
		SandboxPolicy:         sandboxPolicy,
		SandboxProvider:       t.env.ProcessSandboxProvider,
		Env:                   commandEnv,
	})
	response := proc.Process{}
	if p != nil {
		response = redactProcess(t.env, *p)
		response.Action = bashResultActionStart
	}
	out, _ := mustJSON(response)
	if startErr != nil {
		return out, startErr
	}
	return out, nil
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

	revision := workspaceRevision(ctx, t.env.RevisionRoot(ctx))
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
