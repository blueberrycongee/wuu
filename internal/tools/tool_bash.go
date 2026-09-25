package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// BashTool runs one bounded, non-interactive command in the workspace. It is
// the only model-facing command entry point on the Codex / GPT / Claude /
// generic surfaces; long-lived and interactive processes belong to the
// process tool. A run that outlives its timeout is handed to the process
// manager instead of being killed, so the model never loses a slow build.
//
// The model sees a plain-text view of the run (see bash_model_view.go); the
// JSON envelope produced here is what clients render and durable records keep.
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
	return classifyShellCommand(args.Command)
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
		Description: "Run a non-interactive bash command in the workspace: tests, builds, lint, git, package managers, scripts. " +
			"Returns the exit code, duration, and bounded stdout/stderr (head and tail of each stream) with a full log path when output was cut. " +
			"Each call starts a fresh shell; state does not persist between calls. " +
			"A command that exceeds its timeout keeps running as a managed background process and its completion starts a new turn; do not rerun it. " +
			"Use the process tool for servers, watchers, and interactive programs instead of appending '&'. " +
			"Prefer read_file, grep, glob, and the edit tools over shell equivalents for reading, searching, and changing files. " +
			"A sandbox denial means the OS blocked a write outside the workspace boundary; do not retry it through another command.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run. Must not rely on editors, pagers, or terminal prompts.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Max runtime in seconds (1-3600, default 300).",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory. Defaults to the workspace root.",
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
	var args bashArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if err := args.validate(); err != nil {
		return "", err
	}
	return t.executeRun(ctx, args)
}

type bashArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Purpose        string `json:"purpose"`
	Scope          string `json:"scope"`
	CWD            string `json:"cwd"`
	// Action is not part of the schema. Older transcripts and models that
	// learned the previous surface may still send background actions; those
	// now belong to the process tool and get a pointed error instead of a
	// silent foreground run.
	Action string `json:"action"`
}

func (args bashArgs) validate() error {
	switch action := strings.TrimSpace(args.Action); action {
	case "", "run":
	case "start_background", "list_background", "read_background", "write_background", "stop_background", "update_background":
		return fmt.Errorf("bash no longer manages background processes; use the process tool (action=%s)", strings.TrimSuffix(action, "_background"))
	default:
		return fmt.Errorf("bash does not accept action %q", action)
	}
	if strings.TrimSpace(args.Command) == "" {
		return errors.New("bash requires command")
	}
	return nil
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
		"change code, narrow the command, or inspect the verification failure and full log before rerunning",
		"read the latest failure evidence, form a new hypothesis, patch minimally, then rerun targeted verification after the workspace revision changes",
	)
}
