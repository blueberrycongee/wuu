package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// maxRepeatedRunTestFailures bounds how many times the unified bash tool will
// re-run the same failing verification command against an unchanged workspace
// revision before refusing (see tool_bash.go).
const maxRepeatedRunTestFailures = 2

// The verification-classification, npx-runner-resolution, and
// failure-summary helpers below are shared infrastructure for the unified
// bash tool (see tool_bash.go). The former standalone run_test tool was
// removed; bash performs local verification directly.

func commandHashPrefix(commandHash string) string {
	commandHash = strings.TrimSpace(commandHash)
	if len(commandHash) > 12 {
		return commandHash[:12]
	}
	if commandHash == "" {
		return "unknown"
	}
	return commandHash
}

func runTestNextSuggestions(shellResult shellExecutionResult, failureSummary testFailureSummary) []string {
	if shellResult.TimedOut {
		return []string{"narrow the verification scope before rerunning, or raise timeout only if the broad test is required"}
	}
	if shellResult.ExitCode != 0 || failureSummary.Failed {
		return []string{"inspect failure_summary, form a concrete hypothesis, read implicated files, patch minimally, then rerun targeted verification"}
	}
	return []string{"record this verification in the final response and inspect the final diff before finishing"}
}

type testFailureSummary struct {
	Failed       bool                  `json:"failed"`
	FailingTests []string              `json:"failing_tests,omitempty"`
	Indicators   []string              `json:"indicators,omitempty"`
	Locations    []testFailureLocation `json:"locations,omitempty"`
	Snippets     []string              `json:"snippets,omitempty"`
}

type testFailureLocation struct {
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	Text   string `json:"text,omitempty"`
}

var (
	goFailLine       = regexp.MustCompile(`^--- FAIL:\s+([^\s(]+)`)
	pytestFailLine   = regexp.MustCompile(`^FAILED\s+([^\s]+)`)
	jsFailLine       = regexp.MustCompile(`^\s*FAIL\s+(.+)$`)
	fileLocationLine = regexp.MustCompile(`([A-Za-z0-9_./\\-]+\.(?:go|py|js|jsx|ts|tsx|rs|java|kt|kts|c|cc|cpp|cxx|h|hpp|cs|rb|php|swift|m|mm)):(\d+)(?::(\d+))?`)
)

func summarizeTestFailure(output string) testFailureSummary {
	var summary testFailureSummary
	if strings.TrimSpace(output) == "" {
		return summary
	}
	lines := strings.Split(output, "\n")
	seenTests := map[string]struct{}{}
	seenIndicators := map[string]struct{}{}
	seenLocations := map[string]struct{}{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		addFailureLocations(&summary.Locations, seenLocations, trimmed)
		if match := goFailLine.FindStringSubmatch(trimmed); len(match) == 2 {
			addUnique(&summary.FailingTests, seenTests, match[1])
			addTestSnippet(&summary.Snippets, lines, i)
			summary.Failed = true
			continue
		}
		if match := pytestFailLine.FindStringSubmatch(trimmed); len(match) == 2 {
			addUnique(&summary.FailingTests, seenTests, match[1])
			addTestSnippet(&summary.Snippets, lines, i)
			summary.Failed = true
			continue
		}
		if match := jsFailLine.FindStringSubmatch(trimmed); len(match) == 2 {
			addUnique(&summary.FailingTests, seenTests, strings.TrimSpace(match[1]))
			addTestSnippet(&summary.Snippets, lines, i)
			summary.Failed = true
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "assertion") ||
			strings.Contains(lower, "expected") ||
			strings.Contains(lower, "panic:") ||
			strings.Contains(lower, "error:") ||
			strings.Contains(lower, "failed") {
			addUnique(&summary.Indicators, seenIndicators, trimmed)
			addTestSnippet(&summary.Snippets, lines, i)
			summary.Failed = true
		}
	}
	if len(summary.Indicators) > 8 {
		summary.Indicators = summary.Indicators[:8]
	}
	if len(summary.Locations) > 8 {
		summary.Locations = summary.Locations[:8]
	}
	if len(summary.Snippets) > 8 {
		summary.Snippets = summary.Snippets[:8]
	}
	return summary
}

func addUnique(values *[]string, seen map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := seen[value]; ok {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}

func addFailureLocations(values *[]testFailureLocation, seen map[string]struct{}, line string) {
	for _, match := range fileLocationLine.FindAllStringSubmatch(line, -1) {
		if len(match) < 3 {
			continue
		}
		path := strings.TrimSpace(strings.ReplaceAll(match[1], "\\", "/"))
		lineNumber, err := strconv.Atoi(match[2])
		if path == "" || err != nil || lineNumber <= 0 {
			continue
		}
		columnNumber := 0
		if len(match) > 3 && strings.TrimSpace(match[3]) != "" {
			columnNumber, _ = strconv.Atoi(match[3])
		}
		key := fmt.Sprintf("%s:%d:%d", path, lineNumber, columnNumber)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		*values = append(*values, testFailureLocation{
			Path:   path,
			Line:   lineNumber,
			Column: columnNumber,
			Text:   truncateFailureLocationText(line),
		})
	}
}

func truncateFailureLocationText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return value[:240] + "..."
}

func addTestSnippet(snippets *[]string, lines []string, idx int) {
	start := idx - 1
	if start < 0 {
		start = 0
	}
	end := idx + 2
	if end > len(lines) {
		end = len(lines)
	}
	snippet := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if snippet != "" {
		*snippets = append(*snippets, snippet)
	}
}

type resolvedRunTestCommand struct {
	Requested string
	Command   string
	Changed   bool
}

func resolveRunTestCommand(rootDir, command string) (resolvedRunTestCommand, error) {
	requested := strings.TrimSpace(command)
	resolved := resolvedRunTestCommand{Requested: requested, Command: requested}
	if rewritten, changed, err := resolveLocalNpxTestRunner(rootDir, requested); err != nil {
		return resolved, err
	} else if changed {
		resolved.Command = rewritten
		resolved.Changed = true
	}
	return resolved, nil
}

func testCommandLooksLikeLocalRunnerVerification(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if left, dir, right, ok := splitDirectoryScopedTestCommand(command); ok {
		_ = left
		_ = dir
		return testCommandLooksLikeLocalRunnerVerification(right)
	}
	if strings.ContainsAny(command, "\n;&|><`$()") {
		return false
	}
	fields := normalizeShellCommandFields(strings.Fields(command))
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "npx" {
		runnerIdx, ok := npxVerificationRunnerIndex(fields, 0)
		return ok && jsVerificationRunnerInvocation(fields, runnerIdx)
	}
	return jsVerificationRunnerInvocation(fields, 0)
}

func resolveLocalNpxTestRunner(rootDir, command string) (string, bool, error) {
	if left, dir, right, ok := splitDirectoryScopedTestCommand(command); ok {
		workDir := filepath.Join(rootDir, dir)
		rewritten, changed, err := resolveSimpleLocalNpxTestRunner(workDir, right)
		if err != nil || !changed {
			return "", false, err
		}
		return left + " && " + rewritten, true, nil
	}
	if strings.ContainsAny(command, "\n;&|><`$()") {
		return "", false, nil
	}
	return resolveSimpleLocalNpxTestRunner(rootDir, command)
}

func resolveSimpleLocalNpxTestRunner(workDir, command string) (string, bool, error) {
	fields := strings.Fields(command)
	exeIdx := shellExecutableFieldIndex(fields)
	if exeIdx < 0 || exeIdx >= len(fields) || fields[exeIdx] != "npx" {
		return "", false, nil
	}
	runnerIdx, ok := npxVerificationRunnerIndex(fields, exeIdx)
	if !ok || !jsVerificationRunnerInvocation(fields, runnerIdx) {
		return "", false, nil
	}
	runner := jsRunnerBaseName(fields[runnerIdx])
	localRunner := filepath.Join(workDir, "node_modules", ".bin", runner)
	if _, err := os.Stat(localRunner); err != nil {
		if os.IsNotExist(err) {
			return "", false, localTestRunnerMissingError{
				Runner:   runner,
				Expected: filepath.ToSlash(filepath.Join("node_modules", ".bin", runner)),
			}
		}
		return "", false, fmt.Errorf("inspect local test runner %q: %w", localRunner, err)
	}
	rewritten := make([]string, 0, len(fields)-runnerIdx+exeIdx+1)
	rewritten = append(rewritten, fields[:exeIdx]...)
	rewritten = append(rewritten, "./node_modules/.bin/"+runner)
	rewritten = append(rewritten, fields[runnerIdx+1:]...)
	return strings.Join(rewritten, " "), true, nil
}

func splitDirectoryScopedTestCommand(command string) (left, dir, right string, ok bool) {
	parts := strings.Split(command, "&&")
	if len(parts) != 2 {
		return "", "", "", false
	}
	left = strings.TrimSpace(parts[0])
	right = strings.TrimSpace(parts[1])
	leftFields := strings.Fields(left)
	if len(leftFields) != 2 || leftFields[0] != "cd" || !safeRelativeShellDir(leftFields[1]) || right == "" {
		return "", "", "", false
	}
	if strings.ContainsAny(right, "\n;&|><`$()") {
		return "", "", "", false
	}
	return left, shellPathToken(leftFields[1]), right, true
}

func shellExecutableFieldIndex(fields []string) int {
	for i := 0; i < len(fields); {
		for i < len(fields) && looksLikeEnvAssignment(fields[i]) {
			i++
		}
		if i >= len(fields) {
			return -1
		}
		switch shellCommandBaseName(fields[i]) {
		case "command", "exec":
			i++
			continue
		case "env":
			i++
			for i < len(fields) {
				field := fields[i]
				if looksLikeEnvAssignment(field) {
					i++
					continue
				}
				if strings.HasPrefix(field, "-") {
					i++
					if field == "-u" || field == "--unset" || field == "-C" || field == "--chdir" {
						if i < len(fields) {
							i++
						}
					}
					continue
				}
				break
			}
			continue
		default:
			stripped, ok := stripSafeShellWrapperPrefix(fields[i:])
			if ok {
				i += len(fields[i:]) - len(stripped)
				continue
			}
			if shellWrapperCommandName(fields[i]) {
				return -1
			}
			return i
		}
	}
	return -1
}

func npxVerificationRunnerIndex(fields []string, npxIdx int) (int, bool) {
	if npxIdx < 0 || npxIdx >= len(fields) || fields[npxIdx] != "npx" {
		return -1, false
	}
	i := npxIdx + 1
	for i < len(fields) {
		switch fields[i] {
		case "--no-install", "--":
			i++
			continue
		default:
			if strings.HasPrefix(fields[i], "-") {
				return -1, false
			}
			return i, true
		}
	}
	return -1, false
}

func jsVerificationRunnerInvocation(fields []string, runnerIdx int) bool {
	if runnerIdx < 0 || runnerIdx >= len(fields) {
		return false
	}
	switch jsRunnerBaseName(fields[runnerIdx]) {
	case "vitest", "jest", "mocha", "ava", "tap":
		return true
	case "tsc":
		return tscCommandLooksLikeTypecheck(fields[runnerIdx+1:])
	default:
		return false
	}
}

func tscCommandLooksLikeTypecheck(args []string) bool {
	hasNoEmit := false
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if tscCommandArgLooksMutatingOrLongRunning(arg) {
			return false
		}
		switch {
		case arg == "--noEmit" || arg == "--noEmit=true":
			hasNoEmit = true
		case arg == "--noEmit=false":
			return false
		}
	}
	return hasNoEmit
}

func tscCommandArgLooksMutatingOrLongRunning(arg string) bool {
	switch arg {
	case "--init", "--build", "-b", "--watch", "-w", "--generateTrace":
		return true
	}
	for _, prefix := range []string{"--init=", "--build=", "--watch=", "--generateTrace="} {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func jsRunnerBaseName(name string) string {
	name = shellPathToken(name)
	name = strings.TrimSuffix(name, ".cmd")
	name = strings.TrimSuffix(name, ".ps1")
	return filepath.Base(name)
}

type localTestRunnerMissingError struct {
	Runner   string
	Expected string
}

func (e localTestRunnerMissingError) Error() string {
	return fmt.Sprintf(
		"run_test refuses to invoke npx for local test runner %q because npx can download packages when the runner is missing: error_kind=local_test_runner_missing expected=%q model_next_action=%q",
		e.Runner,
		e.Expected,
		"install project dependencies with explicit approval, use an existing package script, or run a project-local test binary",
	)
}
