package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/blueberrycongee/wuu/internal/channels"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/shellpath"
	"github.com/blueberrycongee/wuu/internal/stringutil"
)

// Model-facing excerpt of one output stream. Test runners and build tools put
// failure summaries at either end, so the excerpt keeps the head and the tail
// and marks the omitted middle; the complete stream stays in the full log.
const (
	maxShellExcerptBytes  = 12 * 1024
	shellExcerptHeadBytes = 4 * 1024
)

// defaultPromotedRecheckMinutes is the safety-net recheck schedule armed on
// a run command whose timeout promoted it to a background process. A
// promoted command is an underestimate by definition, so the schedule
// replaces the kill safety net the timeout used to be; the model can change
// or clear it with process action=update.
const defaultPromotedRecheckMinutes = 30

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The shell classification and execution engine below is shared
// infrastructure for the bash tool (see tool_bash.go). The former standalone
// run_shell tool was removed; bash is the only model-facing foreground command
// entry point.
//
// StdoutTail and StderrTail keep the JSON names clients already read; each
// holds the bounded head+tail excerpt produced by excerptString.
type shellExecutionResult struct {
	Action              string                  `json:"action"`
	Command             string                  `json:"command"`
	RequestedCommand    string                  `json:"requested_command,omitempty"`
	ResolvedCommand     string                  `json:"resolved_command,omitempty"`
	Purpose             string                  `json:"purpose,omitempty"`
	Classification      ToolClassification      `json:"classification"`
	ExitCode            int                     `json:"exit_code"`
	DurationMS          int64                   `json:"duration_ms"`
	TimedOut            bool                    `json:"timed_out"`
	Truncated           bool                    `json:"truncated"`
	Output              string                  `json:"output"`
	StdoutTail          string                  `json:"stdout_tail"`
	StderrTail          string                  `json:"stderr_tail"`
	StdoutBytes         int                     `json:"stdout_bytes"`
	StderrBytes         int                     `json:"stderr_bytes"`
	WorkspaceRevision   string                  `json:"workspace_revision"`
	StdoutTailTruncated bool                    `json:"stdout_tail_truncated"`
	StderrTailTruncated bool                    `json:"stderr_tail_truncated"`
	Sandbox             *shellSandboxResult     `json:"sandbox,omitempty"`
	PromotedProcessID   string                  `json:"promoted_process_id,omitempty"`
	FullLogRef          string                  `json:"full_log_ref,omitempty"`
	FullLogBytes        int                     `json:"full_log_bytes,omitempty"`
	FullLogSHA256       string                  `json:"full_log_sha256,omitempty"`
	FullLogSections     shellLogSections        `json:"full_log_sections,omitempty"`
	FullLogError        string                  `json:"full_log_error,omitempty"`
	NextSuggestions     []string                `json:"next_suggestions,omitempty"`
	Verification        *bashVerificationResult `json:"verification,omitempty"`
	redactedStdout      string
	redactedStderr      string
}

type shellSandboxResult struct {
	Mode        processsandbox.Mode `json:"mode"`
	Enforcement string              `json:"enforcement"`
	Denied      bool                `json:"denied"`
}

type shellLogSections struct {
	StdoutStart int `json:"stdout_start"`
	StdoutEnd   int `json:"stdout_end"`
	StderrStart int `json:"stderr_start"`
	StderrEnd   int `json:"stderr_end"`
}

func persistShellLog(sessionDir string, shellResult shellExecutionResult) (path string, bytes int, sections shellLogSections, contentSHA256 string, errSummary string) {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return "", 0, shellLogSections{}, "", ""
	}
	dir := filepath.Join(sessionDir, "tool-results", "shell-logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, shellLogSections{}, "", redactToolOutput(err.Error())
	}
	commandHash := sha256Hex([]byte(shellResult.Command))
	name := fmt.Sprintf("%s-%s.log", time.Now().UTC().Format("20060102T150405.000000000Z"), commandHashPrefix(commandHash))
	path = filepath.Join(dir, name)
	content, sections := buildShellLog(shellResult)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", 0, shellLogSections{}, "", redactToolOutput(err.Error())
	}
	return path, len(content), sections, sha256Hex([]byte(content)), ""
}

func buildShellLog(shellResult shellExecutionResult) (string, shellLogSections) {
	var b strings.Builder
	var sections shellLogSections
	fmt.Fprintf(&b, "command: %s\n", shellResult.Command)
	if strings.TrimSpace(shellResult.Purpose) != "" {
		fmt.Fprintf(&b, "purpose: %s\n", shellResult.Purpose)
	}
	fmt.Fprintf(&b, "exit_code: %d\n", shellResult.ExitCode)
	fmt.Fprintf(&b, "duration_ms: %d\n", shellResult.DurationMS)
	fmt.Fprintf(&b, "timed_out: %t\n", shellResult.TimedOut)
	fmt.Fprintf(&b, "stdout_bytes: %d\n", shellResult.StdoutBytes)
	fmt.Fprintf(&b, "stderr_bytes: %d\n", shellResult.StderrBytes)
	fmt.Fprintf(&b, "workspace_revision: %s\n\n", shellResult.WorkspaceRevision)
	b.WriteString("--- stdout (redacted) ---\n")
	sections.StdoutStart = b.Len()
	b.WriteString(shellResult.redactedStdout)
	sections.StdoutEnd = b.Len()
	if !strings.HasSuffix(shellResult.redactedStdout, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n--- stderr (redacted) ---\n")
	sections.StderrStart = b.Len()
	b.WriteString(shellResult.redactedStderr)
	sections.StderrEnd = b.Len()
	if !strings.HasSuffix(shellResult.redactedStderr, "\n") {
		b.WriteString("\n")
	}
	return b.String(), sections
}

func replaceCommandEnv(env []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func executeShellCommandWithCWD(ctx context.Context, env *Env, command string, timeoutSeconds int, cwd string) (shellExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if env == nil {
		env = &Env{}
	}
	workDir, err := resolveShellWorkingDir(ctx, env, cwd)
	if err != nil {
		return shellExecutionResult{}, err
	}
	return executeShellCommandInDir(ctx, env, command, timeoutSeconds, workDir)
}

// executeShellCommandInDir runs command in an already-resolved absolute workDir
// without re-running resolveShellWorkingDir. Callers that resolved the shell
// working dir themselves (BashTool.executeRun resolves once so it can rewrite
// verification commands) use this to skip a second resolve: re-resolving an
// already worktree-relocated absolute path would push it back through the
// sandbox rel-check and be falsely rejected as escaping the workspace.
func executeShellCommandInDir(ctx context.Context, env *Env, command string, timeoutSeconds int, workDir string) (shellExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if env == nil {
		env = &Env{}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	shell, err := shellpath.LoginBash()
	if err != nil {
		return shellExecutionResult{}, err
	}
	executedCommand := shellpath.NormalizeBashCommand(command)
	if env.gitAttributionEnabled() {
		prefix, prefixErr := env.gitAttributionShellPrefix()
		if prefixErr != nil {
			return shellExecutionResult{}, prefixErr
		}
		executedCommand = prefix + executedCommand
	}
	cmd := exec.Command(shell.Path, shell.CommandArgs(executedCommand)...)
	cmd.Dir = workDir
	cmd.Env = shellpath.CommandEnv(shellCommandEnvForTool(os.Environ(), env))
	sandboxPolicy, sandboxTempDir, err := env.processSandboxPolicy(ctx)
	if err != nil {
		return shellExecutionResult{}, fmt.Errorf("prepare filesystem process sandbox: %w", err)
	}
	if sandboxTempDir != "" {
		cmd.Env = replaceCommandEnv(cmd.Env, "TMPDIR", sandboxTempDir)
	}
	var sandboxClassifier processsandbox.ResultClassifier
	if sandboxPolicy != nil {
		sandboxClassifier, err = processsandbox.ApplyWithProvider(ctx, cmd, *sandboxPolicy, env.ProcessSandboxProvider)
		if err != nil {
			return shellExecutionResult{}, fmt.Errorf("prepare filesystem process sandbox: %w", err)
		}
	}

	var stdout synchronizedBuffer
	var stderr synchronizedBuffer
	// Tee output into a reserved managed log from the start so a timeout can
	// promote the run to a background process with both past and future
	// output available through the background read path.
	var logf *os.File
	reservedID := ""
	mgr, mgrErr := env.ProcessManager()
	if mgrErr == nil && mgr != nil {
		if id, logPath := mgr.ReserveProcessLog(); id != "" {
			if f, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); openErr == nil {
				if chmodErr := f.Chmod(0o600); chmodErr == nil {
					reservedID, logf = id, f
				} else {
					_ = f.Close()
					_ = os.Remove(logPath)
				}
			}
		}
	}
	if logf != nil {
		cmd.Stdout = io.MultiWriter(&stdout, logf)
		cmd.Stderr = io.MultiWriter(&stderr, logf)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}
	discardReservedLog := func() {
		if logf == nil {
			return
		}
		_ = logf.Close()
		_ = os.Remove(logf.Name())
	}

	startedAt := time.Now()
	handle, err := proc.StartCommand(cmd)
	if err != nil {
		discardReservedLog()
		return shellExecutionResult{}, fmt.Errorf("run command: %w", err)
	}
	interrupted := false
	promotedID := ""
	select {
	case <-handle.Done():
		err = handle.Wait()
	case <-runCtx.Done():
		interrupted = true
		// A genuine timeout (not a caller cancellation) promotes the command
		// to a managed background process instead of killing it: the result
		// gates nothing further this turn, and completion arrives as a
		// notification later. The default recheck replaces the kill safety
		// net the timeout used to be — a promoted command is an underestimate
		// by definition, so something should look at it periodically.
		if logf != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			adopted, adoptErr := mgr.Adopt(reservedID, cmd, handle, logf, proc.AdoptOptions{
				Command:        command,
				CWD:            workDir,
				OwnerKind:      proc.OwnerKind(defaultProcessOwnerKind(env, "")),
				OwnerID:        defaultProcessOwnerID(env),
				StartedAt:      startedAt,
				RecheckMinutes: defaultPromotedRecheckMinutes,
				SandboxMode: func() processsandbox.Mode {
					if sandboxPolicy == nil {
						return ""
					}
					return sandboxPolicy.Mode
				}(),
			})
			if adoptErr == nil {
				promotedID = adopted.ID
			}
		}
		if promotedID == "" {
			if stopErr := handle.Stop(proc.DefaultStopGracePeriod); stopErr != nil {
				discardReservedLog()
				return shellExecutionResult{}, fmt.Errorf("stop command: %w", stopErr)
			}
			err = handle.Wait()
		}
	}
	if promotedID != "" {
		// The manager owns the log and the wait from here.
		logf = nil
	} else {
		discardReservedLog()
	}
	durationMS := time.Since(startedAt).Milliseconds()
	exitCode := 0
	if interrupted {
		exitCode = -1
	} else if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return shellExecutionResult{}, fmt.Errorf("run command: %w", err)
		}
	}

	stdoutText := stdout.String()
	stderrText := stderr.String()
	if sandboxPolicy != nil && sandboxClassifier.IsRunnerFailure(exitCode, stdoutText+stderrText) {
		return shellExecutionResult{}, fmt.Errorf("filesystem process sandbox runner failed; command did not run: %w", processsandbox.ErrUnavailable)
	}
	redactedStdout := env.RedactToolOutput(stdoutText)
	redactedStderr := env.RedactToolOutput(stderrText)
	redactedCommand := env.RedactToolOutput(command)
	output := redactedStdout + redactedStderr
	trimmed, truncated := truncate(output, maxShellOutputBytes)
	stdoutTail, stdoutTailTruncated := excerptString(redactedStdout, maxShellExcerptBytes)
	stderrTail, stderrTailTruncated := excerptString(redactedStderr, maxShellExcerptBytes)
	classification := classifyShellCommand(command)
	timedOut := interrupted && errors.Is(runCtx.Err(), context.DeadlineExceeded)

	nextSuggestions := shellNextSuggestions(exitCode, timedOut, classification)
	var sandboxResult *shellSandboxResult
	if sandboxPolicy != nil {
		denied := sandboxClassifier.IsDenied(exitCode, stdoutText+stderrText)
		sandboxResult = &shellSandboxResult{Mode: sandboxPolicy.Mode, Enforcement: "full", Denied: denied}
		if denied {
			nextSuggestions = []string{
				"the filesystem process sandbox blocked a file write outside the current boundary; do not retry the same access through another command",
				"use a path inside a registered workspace, ask the user to add the required directory as a workspace, or explain that the user must explicitly switch the session to unconfined mode",
			}
		}
	}
	if promotedID != "" {
		nextSuggestions = []string{
			"the command exceeded its timeout and keeps running as background process " + promotedID + " with its output so far attached; do NOT rerun the same command — its completion will start a new turn automatically",
			"progress wake-ups are scheduled every " + strconv.Itoa(defaultPromotedRecheckMinutes) + " minutes as a safety net; use process action=read with process_id for a snapshot, action=stop to cancel it, or action=update with recheck_minutes to change or clear the schedule",
		}
	}

	return shellExecutionResult{
		Action:              "run",
		Command:             redactedCommand,
		Classification:      classification,
		ExitCode:            exitCode,
		DurationMS:          durationMS,
		TimedOut:            timedOut,
		Truncated:           truncated,
		Output:              trimmed,
		StdoutTail:          stdoutTail,
		StderrTail:          stderrTail,
		StdoutBytes:         len(stdoutText),
		StderrBytes:         len(stderrText),
		WorkspaceRevision:   workspaceRevision(ctx, env.RevisionRoot(ctx)),
		StdoutTailTruncated: stdoutTailTruncated,
		StderrTailTruncated: stderrTailTruncated,
		Sandbox:             sandboxResult,
		PromotedProcessID:   promotedID,
		NextSuggestions:     nextSuggestions,
		redactedStdout:      redactedStdout,
		redactedStderr:      redactedStderr,
	}, nil
}

// shellCommandEnv keeps build-time Go runtime metadata out of workspace
// commands. A wuu process launched by `go run` can inherit the selected
// toolchain's GOROOT, while its login shell resolves a different go binary.
// Let that binary select its own matching GOROOT instead of mixing toolchains.
func shellCommandEnv(base []string) []string {
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "GOROOT") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return mergeEnv(filtered, nonInteractiveShellEnv())
}

func shellCommandEnvForTool(base []string, env *Env) []string {
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, channels.NamedAgentIDEnv) {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = shellCommandEnv(filtered)
	if env == nil || env.ChatAgent == nil {
		return filtered
	}
	agentID := strings.TrimSpace(env.ChatAgent.AgentID())
	if agentID == "" {
		return filtered
	}
	return mergeEnv(filtered, map[string]string{channels.NamedAgentIDEnv: agentID})
}

func resolveShellWorkingDir(ctx context.Context, env *Env, cwd string) (string, error) {
	if env == nil {
		env = &Env{}
	}
	if strings.TrimSpace(env.RootDir) == "" {
		if strings.TrimSpace(cwd) == "" {
			return "", nil
		}
		return "", fmt.Errorf("working directory %q requires workspace root", cwd)
	}
	workDir, err := env.ResolvePath(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", cwd, err)
	}
	// Worktree-bound execution: switch the shell CWD onto the checkout only
	// after the sandbox check above accepted the workspace path.
	workDir, err = env.ExecPath(ctx, workDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", workDir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("working directory %q does not exist", abs)
		}
		return "", fmt.Errorf("inspect working directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", abs)
	}
	return abs, nil
}

func shellNextSuggestions(exitCode int, timedOut bool, classification ToolClassification) []string {
	if timedOut {
		return []string{"if this was a dev server, watch mode, or other long-lived command, rerun it with run_in_background; otherwise narrow the command or raise timeout_seconds"}
	}
	if exitCode != 0 {
		return []string{"inspect the output and the full log when present, then retry the corrected command"}
	}
	if classification.ReadOnly {
		return []string{"use the returned observation as evidence for the next action"}
	}
	return []string{"inspect git diff or relevant artifacts before continuing"}
}

// excerptString bounds one stream to maxBytes, keeping its head and tail and
// marking how much of the middle was omitted.
func excerptString(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value, false
	}
	head := shellExcerptHeadBytes
	if head > maxBytes/2 {
		head = maxBytes / 2
	}
	marker := fmt.Sprintf("\n... %d bytes omitted ...\n", len(value)-maxBytes)
	return stringutil.HeadTail(value, head, maxBytes-head, marker), true
}

func classifyShellCommand(command string) ToolClassification {
	command = strings.TrimSpace(command)
	if command == "" {
		return highRiskShellClassification("empty shell command", false)
	}
	hasShellMetacharacters := strings.ContainsAny(command, "\n;&|><`$()")
	rawFields := strings.Fields(command)
	if len(rawFields) == 0 {
		return highRiskShellClassification("empty shell command", false)
	}
	fields := stripLeadingShellEnvAssignments(rawFields)
	if len(fields) == 0 {
		return highRiskShellClassification("environment-only shell command", false)
	}
	if shellFieldsDumpEnvironment(command, fields) {
		return highRiskShellClassification("shell command may expose environment secrets", false)
	}
	if shellFieldsTouchSensitivePath(fields) {
		return highRiskShellClassification("shell command may read secrets", false)
	}
	if shellCommandReferencesUNCPath(rawFields) {
		return highRiskShellClassification("shell command references a network (UNC) path", false)
	}
	if shellCommandUsesUnsupportedWrapper(command) {
		return highRiskShellClassification("unsupported shell wrapper command", false)
	}
	envFields := normalizeShellCommandFieldsPreservingEnv(fields)
	if shellFieldsDumpEnvironment(command, envFields) {
		return highRiskShellClassification("shell command may expose environment secrets", false)
	}
	fields = normalizeShellCommandFields(fields)
	if len(fields) == 0 {
		return highRiskShellClassification("wrapper-only shell command", false)
	}
	if shellFieldsDumpEnvironment(command, fields) {
		return highRiskShellClassification("shell command may expose environment secrets", false)
	}
	if shellFieldsTouchSensitivePath(fields) {
		return highRiskShellClassification("shell command may read secrets", false)
	}
	if classification, ok := classifyShellGitCommand(command); ok {
		return classification
	}
	if shellCommandInvokesDestructiveCommand(command) {
		return highRiskShellClassification("destructive shell command", true)
	}
	if hasShellMetacharacters {
		if shellCommandLooksLikeDirectoryScopedVerification(command) {
			return ToolClassification{
				ReadOnly:        false,
				ConcurrencySafe: false,
				Risk:            ToolRiskMedium,
				Reason:          "local verification command",
			}
		}
		return highRiskShellClassification("shell command uses shell metacharacters", false)
	}
	switch fields[0] {
	case "pwd", "ls", "cat", "head", "tail", "wc", "file", "stat", "du",
		"rg", "grep":
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            ToolRiskLow,
			Reason:          "simple read-only shell command",
		}
	}
	if shellFieldsLookLikeVerification(fields) {
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskMedium,
			Reason:          "local verification command",
		}
	}
	if shellFieldsLookLikePackageOrNetworkMutation(fields) {
		return highRiskShellClassification("package, network, or external mutation command", false)
	}
	return highRiskShellClassification("shell command is not proven read-only or verification-only", false)
}

func shellCommandInvokesGit(command string) bool {
	for _, segment := range splitShellCommandSegments(command) {
		fields := normalizeShellCommandFields(strings.Fields(segment))
		if len(fields) > 0 && shellCommandBaseName(fields[0]) == "git" {
			return true
		}
	}
	return false
}

func classifyShellGitCommand(command string) (ToolClassification, bool) {
	if !shellCommandInvokesGit(command) {
		return ToolClassification{}, false
	}
	segments, ok := splitShellCommandSegmentsQuoted(command)
	if !ok {
		return highRiskShellClassification("unsupported git shell command", false), true
	}
	sawGit := false
	readOnly := true
	concurrencySafe := true
	risk := ToolRiskLow
	reason := "git shell read-only command"
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		fields, ok := splitShellFields(segment)
		if !ok {
			return highRiskShellClassification("unsupported git shell command", false), true
		}
		fields = normalizeShellCommandFields(fields)
		if len(fields) == 0 {
			continue
		}
		if shellCommandBaseName(fields[0]) == "cd" {
			if len(fields) == 2 && safeRelativeShellDir(fields[1]) {
				continue
			}
			return highRiskShellClassification("unsupported git shell command", false), true
		}
		if shellCommandBaseName(fields[0]) != "git" {
			return highRiskShellClassification("unsupported git shell command", false), true
		}
		fields[0] = "git"
		sawGit = true
		segmentClass := classifyShellGitFields(fields)
		if segmentClass.Destructive {
			return segmentClass, true
		}
		if !segmentClass.ReadOnly {
			readOnly = false
			concurrencySafe = false
		}
		if riskRank(segmentClass.Risk) > riskRank(risk) {
			risk = segmentClass.Risk
			reason = segmentClass.Reason
		}
	}
	if !sawGit {
		return highRiskShellClassification("unsupported git shell command", false), true
	}
	return ToolClassification{
		ReadOnly:        readOnly,
		ConcurrencySafe: concurrencySafe,
		Destructive:     false,
		Risk:            risk,
		Reason:          reason,
	}, true
}

func classifyShellGitFields(fields []string) ToolClassification {
	if len(fields) > 1 && oneOf(fields[1], "reset", "clean", "checkout", "switch", "merge", "rebase") {
		return highRiskShellClassification("destructive git shell command", true)
	}
	subcmd, args, err := parseShellGitSubcommand(fields)
	if err != nil {
		return highRiskShellClassification("unsupported git shell command", false)
	}
	switch subcmd {
	case "add":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskMedium,
			Reason:          "git shell add writes the repository index",
		}
	case "restore --staged":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskMedium,
			Reason:          "git shell restore --staged writes the repository index",
		}
	case "commit":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskMedium,
			Reason:          "git shell commit writes local repository history",
		}
	case "push":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Risk:            ToolRiskHigh,
			Reason:          "git shell push writes remote branch state",
		}
	case "ls-remote":
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            ToolRiskMedium,
			Reason:          "git shell ls-remote may contact a remote",
		}
	default:
		_ = args
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Risk:            ToolRiskLow,
			Reason:          "git shell read-only command",
		}
	}
}

func parseShellGitSubcommand(fields []string) (string, []string, error) {
	if len(fields) < 2 || fields[0] != "git" {
		return "", nil, errors.New("not a git command")
	}
	subcmd := strings.TrimSpace(fields[1])
	remainingArgs := append([]string(nil), fields[2:]...)
	if subcmd == "" || strings.HasPrefix(subcmd, "-") {
		return "", nil, fmt.Errorf("git shell global options are not supported")
	}
	if !allowedGitSubcommands[subcmd] && policiedSubcommands[subcmd] == nil && len(remainingArgs) > 0 {
		combined := subcmd + " " + remainingArgs[0]
		if allowedGitSubcommands[combined] || policiedSubcommands[combined] != nil {
			subcmd = combined
			remainingArgs = remainingArgs[1:]
		}
	}
	if err := validateGlobalGitArgs(subcmd, remainingArgs); err != nil {
		return "", nil, err
	}
	if policy := policiedSubcommands[subcmd]; policy != nil {
		if err := validateSubcommandFlags(subcmd, policy, remainingArgs); err != nil {
			return "", nil, err
		}
	} else if allowedGitSubcommands[subcmd] {
		if err := validateGitArgs(subcmd, remainingArgs, false); err != nil {
			return "", nil, err
		}
	} else {
		return "", nil, fmt.Errorf("git shell subcommand %q is not allowed", subcmd)
	}
	if err := validateSensitiveGitContentArgs(subcmd, remainingArgs, false); err != nil {
		return "", nil, err
	}
	return subcmd, remainingArgs, nil
}

func riskRank(risk ToolRisk) int {
	switch risk {
	case ToolRiskHigh:
		return 3
	case ToolRiskMedium:
		return 2
	case ToolRiskLow:
		return 1
	default:
		return 0
	}
}

func splitShellCommandSegmentsQuoted(command string) ([]string, bool) {
	var segments []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && !inSingle:
			if i+1 >= len(runes) {
				return nil, false
			}
			b.WriteRune(r)
			i++
			b.WriteRune(runes[i])
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteRune(r)
		case !inSingle && !inDouble && strings.ContainsRune("\n;&|()`", r):
			segments = append(segments, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	segments = append(segments, b.String())
	return segments, true
}

func splitShellFields(segment string) ([]string, bool) {
	var fields []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	haveField := false
	flush := func() {
		if !haveField {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
		haveField = false
	}
	runes := []rune(segment)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && !inSingle:
			if i+1 >= len(runes) {
				return nil, false
			}
			i++
			b.WriteRune(runes[i])
			haveField = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			haveField = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			haveField = true
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			flush()
		default:
			b.WriteRune(r)
			haveField = true
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	flush()
	return fields, true
}

func shellCommandInvokesDestructiveCommand(command string) bool {
	for _, segment := range splitShellCommandSegments(command) {
		fields := normalizeShellCommandFields(strings.Fields(segment))
		if shellFieldsLookDestructive(fields) {
			return true
		}
	}
	return false
}

func shellCommandInvokesPackageOrNetworkMutation(command string) bool {
	for _, segment := range splitShellCommandSegments(command) {
		fields := normalizeShellCommandFields(strings.Fields(segment))
		if shellFieldsLookLikePackageOrNetworkMutation(fields) {
			return true
		}
	}
	return false
}

func shellCommandUsesUnsupportedWrapper(command string) bool {
	segments, ok := splitShellCommandSegmentsQuoted(command)
	if !ok {
		segments = splitShellCommandSegments(command)
	}
	if len(segments) == 0 {
		segments = []string{command}
	}
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		fields, ok := splitShellFields(segment)
		if !ok {
			fields = strings.Fields(segment)
		}
		if shellFieldsUseUnsupportedWrapper(fields) {
			return true
		}
	}
	return false
}

func shellFieldsUseUnsupportedWrapper(fields []string) bool {
	fields = stripLeadingShellEnvAssignments(fields)
	for len(fields) > 0 {
		switch shellCommandBaseName(fields[0]) {
		case "command", "exec":
			fields = stripLeadingShellEnvAssignments(fields[1:])
		case "env":
			fields = stripLeadingShellEnvAssignments(stripEnvCommandPrefix(fields[1:]))
		default:
			if !shellWrapperCommandName(fields[0]) {
				return false
			}
			stripped, ok := stripSafeShellWrapperPrefix(fields)
			if !ok {
				return true
			}
			fields = stripLeadingShellEnvAssignments(stripped)
		}
	}
	return false
}

func normalizeShellCommandFields(fields []string) []string {
	fields = stripLeadingShellEnvAssignments(fields)
	for len(fields) > 0 {
		switch shellCommandBaseName(fields[0]) {
		case "command", "exec":
			fields = fields[1:]
		case "env":
			fields = stripEnvCommandPrefix(fields[1:])
		default:
			stripped, ok := stripSafeShellWrapperPrefix(fields)
			if !ok {
				return fields
			}
			fields = stripped
		}
		fields = stripLeadingShellEnvAssignments(fields)
	}
	return fields
}

func normalizeShellCommandFieldsPreservingEnv(fields []string) []string {
	fields = stripLeadingShellEnvAssignments(fields)
	for len(fields) > 0 {
		switch shellCommandBaseName(fields[0]) {
		case "command", "exec":
			fields = fields[1:]
		case "env":
			return fields
		default:
			stripped, ok := stripSafeShellWrapperPrefix(fields)
			if !ok {
				return fields
			}
			fields = stripped
		}
		fields = stripLeadingShellEnvAssignments(fields)
	}
	return fields
}

func stripLeadingShellEnvAssignments(fields []string) []string {
	for len(fields) > 0 && looksLikeEnvAssignment(fields[0]) {
		fields = fields[1:]
	}
	return fields
}

func stripSafeShellWrapperPrefix(fields []string) ([]string, bool) {
	if len(fields) == 0 {
		return fields, false
	}
	switch shellCommandBaseName(fields[0]) {
	case "timeout":
		i := timeoutWrappedCommandIndex(fields)
		if i < 0 {
			return fields, false
		}
		return fields[i:], true
	case "time":
		i := timeWrappedCommandIndex(fields)
		if i < 0 {
			return fields, false
		}
		return fields[i:], true
	case "nice":
		i := niceWrappedCommandIndex(fields)
		if i < 0 {
			return fields, false
		}
		return fields[i:], true
	case "nohup":
		i := 1
		if len(fields) > i && fields[i] == "--" {
			i++
		}
		if i >= len(fields) || strings.HasPrefix(fields[i], "-") {
			return fields, false
		}
		return fields[i:], true
	case "stdbuf":
		i := stdbufWrappedCommandIndex(fields)
		if i < 0 {
			return fields, false
		}
		return fields[i:], true
	default:
		return fields, false
	}
}

func shellWrapperCommandName(name string) bool {
	switch shellCommandBaseName(name) {
	case "timeout", "time", "nice", "nohup", "stdbuf":
		return true
	default:
		return false
	}
}

func shellCommandBaseName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.ContainsAny(name, `/\`) {
		name = filepath.Base(filepath.FromSlash(name))
		if idx := strings.LastIndexByte(name, '\\'); idx >= 0 {
			name = name[idx+1:]
		}
	}
	// A Windows executable extension never changes what the command is:
	// rm.exe is rm for risk purposes on every platform.
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		if len(name) > len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

func timeoutWrappedCommandIndex(fields []string) int {
	i := 1
	for i < len(fields) {
		arg := fields[i]
		switch {
		case arg == "--foreground" || arg == "--preserve-status" || arg == "--verbose" || arg == "-v":
			i++
		case strings.HasPrefix(arg, "--kill-after=") || strings.HasPrefix(arg, "--signal="):
			value := arg[strings.IndexByte(arg, '=')+1:]
			if !shellWrapperValueLooksSafe(value) {
				return -1
			}
			i++
		case arg == "--kill-after" || arg == "--signal" || arg == "-k" || arg == "-s":
			if i+1 >= len(fields) || !shellWrapperValueLooksSafe(fields[i+1]) {
				return -1
			}
			i += 2
		case strings.HasPrefix(arg, "-k") || strings.HasPrefix(arg, "-s"):
			if len(arg) <= 2 || !shellWrapperValueLooksSafe(arg[2:]) {
				return -1
			}
			i++
		case arg == "--":
			i++
			goto duration
		case strings.HasPrefix(arg, "-"):
			return -1
		default:
			goto duration
		}
	}

duration:
	if i >= len(fields) || i+1 >= len(fields) || !shellTimeoutDurationLooksSafe(fields[i]) {
		return -1
	}
	return i + 1
}

func timeWrappedCommandIndex(fields []string) int {
	i := 1
	if i < len(fields) && fields[i] == "-p" {
		i++
	}
	if i < len(fields) && fields[i] == "--" {
		i++
	}
	if i >= len(fields) || strings.HasPrefix(fields[i], "-") {
		return -1
	}
	return i
}

func niceWrappedCommandIndex(fields []string) int {
	i := 1
	if i >= len(fields) {
		return -1
	}
	switch {
	case fields[i] == "--":
		i++
	case fields[i] == "-n":
		if i+1 >= len(fields) || !shellNiceAdjustmentLooksSafe(fields[i+1]) {
			return -1
		}
		i += 2
		if i < len(fields) && fields[i] == "--" {
			i++
		}
	case shellLegacyNiceAdjustmentLooksSafe(fields[i]):
		i++
		if i < len(fields) && fields[i] == "--" {
			i++
		}
	case strings.HasPrefix(fields[i], "-"):
		return -1
	}
	if i >= len(fields) {
		return -1
	}
	return i
}

func stdbufWrappedCommandIndex(fields []string) int {
	i := 1
	consumedFlag := false
	for i < len(fields) {
		arg := fields[i]
		switch {
		case arg == "-i" || arg == "-o" || arg == "-e":
			if i+1 >= len(fields) || !shellWrapperValueLooksSafe(fields[i+1]) {
				return -1
			}
			i += 2
			consumedFlag = true
		case len(arg) > 2 && arg[0] == '-' && (arg[1] == 'i' || arg[1] == 'o' || arg[1] == 'e'):
			if !shellWrapperValueLooksSafe(arg[2:]) {
				return -1
			}
			i++
			consumedFlag = true
		case strings.HasPrefix(arg, "--input=") || strings.HasPrefix(arg, "--output=") || strings.HasPrefix(arg, "--error="):
			value := arg[strings.IndexByte(arg, '=')+1:]
			if !shellWrapperValueLooksSafe(value) {
				return -1
			}
			i++
			consumedFlag = true
		case strings.HasPrefix(arg, "-"):
			return -1
		default:
			if !consumedFlag {
				return -1
			}
			return i
		}
	}
	return -1
}

func shellWrapperValueLooksSafe(value string) bool {
	if value == "" || strings.ContainsAny(value, "\n;&|><`$()") {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		if r == '_' || r == '.' || r == '+' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func shellTimeoutDurationLooksSafe(value string) bool {
	if value == "" || strings.ContainsAny(value, "\n;&|><`$()+-") {
		return false
	}
	if last := value[len(value)-1]; last == 's' || last == 'm' || last == 'h' || last == 'd' {
		value = value[:len(value)-1]
	}
	if value == "" {
		return false
	}
	sawDigit := false
	sawDot := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			sawDigit = true
		case r == '.':
			if sawDot {
				return false
			}
			sawDot = true
		default:
			return false
		}
	}
	return sawDigit && value[0] != '.' && value[len(value)-1] != '.'
}

func shellNiceAdjustmentLooksSafe(value string) bool {
	if value == "" || strings.ContainsAny(value, "\n;&|><`$()+.") {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func shellLegacyNiceAdjustmentLooksSafe(value string) bool {
	return strings.HasPrefix(value, "-") && shellNiceAdjustmentLooksSafe(value)
}

func splitShellCommandSegments(command string) []string {
	return strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case '\n', ';', '&', '|', '(', ')', '`':
			return true
		default:
			return false
		}
	})
}

func stripEnvCommandPrefix(fields []string) []string {
	for len(fields) > 0 {
		field := fields[0]
		if looksLikeEnvAssignment(field) {
			fields = fields[1:]
			continue
		}
		if strings.HasPrefix(field, "-") {
			fields = fields[1:]
			if field == "-u" || field == "--unset" || field == "-C" || field == "--chdir" {
				if len(fields) > 0 {
					fields = fields[1:]
				}
			}
			continue
		}
		break
	}
	return fields
}

func shellCommandLooksLikeDirectoryScopedVerification(command string) bool {
	parts := strings.Split(command, "&&")
	if len(parts) != 2 {
		return false
	}
	leftFields := strings.Fields(strings.TrimSpace(parts[0]))
	if len(leftFields) != 2 || leftFields[0] != "cd" || !safeRelativeShellDir(leftFields[1]) {
		return false
	}
	right := strings.TrimSpace(parts[1])
	if right == "" || strings.ContainsAny(right, "\n;&|><`$()") {
		return false
	}
	rightFields := strings.Fields(right)
	rightFields = normalizeShellCommandFields(rightFields)
	return shellFieldsLookLikeVerification(rightFields)
}

func safeRelativeShellDir(path string) bool {
	path = shellPathToken(path)
	if path == "" || strings.HasPrefix(path, "-") || strings.HasPrefix(path, "~") || filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(path)
	return cleaned == "." || (cleaned != ".." && !strings.HasPrefix(cleaned, "../"))
}

func highRiskShellClassification(reason string, destructive bool) ToolClassification {
	return ToolClassification{
		ReadOnly:        false,
		ConcurrencySafe: false,
		Destructive:     destructive,
		Risk:            ToolRiskHigh,
		Reason:          reason,
	}
}

func shellFieldsDumpEnvironment(command string, fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "printenv", "set", "export":
		return true
	case "env":
		if strings.ContainsAny(command, "\n;&|><`$()") {
			return true
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "-") && !looksLikeEnvAssignment(field) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func shellFieldsTouchSensitivePath(fields []string) bool {
	for _, field := range fields[1:] {
		if _, ok := shellSensitivePathTokenReason(field); ok {
			return true
		}
	}
	return false
}

// shellCommandReferencesUNCPath flags tokens addressing network (UNC)
// paths on Windows, where both \\server\share and the MSYS //server/share
// spelling reach the network stack; touching an attacker-controlled
// server can leak NTLM credentials. Windows-only: double-slash tokens
// are ordinary local paths elsewhere.
func shellCommandReferencesUNCPath(fields []string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	for _, field := range fields {
		token := shellPathToken(field)
		if len(token) < 3 {
			continue
		}
		if (strings.HasPrefix(token, `\\`) || strings.HasPrefix(token, "//")) && token[2] != '/' && token[2] != '\\' {
			return true
		}
	}
	return false
}

func shellSensitivePathTokenReason(field string) (string, bool) {
	token := shellPathToken(field)
	if token == "" || strings.HasPrefix(token, "-") {
		return "", false
	}
	lower := strings.ToLower(token)
	switch {
	case strings.Contains(lower, ".env"), strings.Contains(lower, ".netrc"), strings.Contains(lower, ".npmrc"), strings.Contains(lower, ".pypirc"), strings.Contains(lower, ".pgpass"):
		return sensitivePathReason(token)
	case strings.Contains(lower, "/") || strings.Contains(lower, `\`) || strings.Contains(lower, "."):
		return sensitivePathReason(token)
	case lower == "secret" || lower == "secrets" || lower == "credential" || lower == "credentials" || lower == "private_key":
		return sensitivePathReason(token)
	default:
		return "", false
	}
}

func shellPathToken(field string) string {
	return strings.Trim(field, " \t\r\n\"'`(){}[]<>|&;,:")
}

func shellFieldsLookDestructive(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch shellCommandBaseName(fields[0]) {
	case "rm", "rmdir", "unlink", "mv", "chmod", "chown", "sudo", "dd", "truncate":
		return true
	case "git":
		if len(fields) > 1 {
			switch fields[1] {
			case "reset", "clean", "checkout", "switch", "merge", "rebase":
				return true
			}
		}
	case "docker", "kubectl", "terraform":
		return true
	}
	return false
}

func shellFieldsLookLikeVerification(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch shellCommandBaseName(fields[0]) {
	case "go":
		return len(fields) > 1 && oneOf(fields[1], "test", "vet", "build", "list")
	case "cargo":
		return len(fields) > 1 && oneOf(fields[1], "test", "check", "build", "clippy")
	case "pytest", "ruff", "mypy":
		return true
	case "tsc":
		return tscCommandLooksLikeTypecheck(fields[1:])
	case "python", "python3":
		return len(fields) > 2 && fields[1] == "-m" && oneOf(fields[2], "pytest", "mypy")
	case "npm", "pnpm", "yarn", "bun":
		return packageManagerCommandLooksLikeVerification(fields)
	case "make":
		return len(fields) > 1 && makeTargetLooksLikeVerification(fields[1])
	case "uv":
		return len(fields) > 2 && fields[1] == "run" && shellFieldsLookLikeVerification(fields[2:])
	}
	return false
}

func packageManagerCommandLooksLikeVerification(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	switch fields[1] {
	case "test", "lint", "build", "check", "typecheck":
		return true
	case "run":
		return len(fields) > 2 && makeTargetLooksLikeVerification(fields[2])
	}
	return false
}

func makeTargetLooksLikeVerification(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return oneOf(target, "test", "tests", "check", "lint", "typecheck", "build", "verify", "ci")
}

func shellFieldsLookLikePackageOrNetworkMutation(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch shellCommandBaseName(fields[0]) {
	case "npm", "pnpm", "yarn", "bun":
		if len(fields) > 1 {
			return oneOf(fields[1], "install", "i", "add", "remove", "update", "upgrade", "publish", "exec", "dlx")
		}
	case "npx":
		return true
	case "pip", "pip3", "uv":
		if len(fields) > 1 {
			return oneOf(fields[1], "install", "add", "remove", "sync", "publish")
		}
	case "curl", "wget", "ssh", "scp", "rsync", "gh":
		return true
	}
	return false
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func looksLikeEnvAssignment(value string) bool {
	idx := strings.IndexByte(value, '=')
	if idx <= 0 {
		return false
	}
	name := value[:idx]
	for i, r := range name {
		if r == '_' {
			continue
		}
		if i == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
				return false
			}
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}
