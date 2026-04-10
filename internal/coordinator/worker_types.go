package coordinator

import (
	"fmt"
	"sort"
	"strings"
)

// WorkerType describes a worker class: which tools it has, what its
// system prompt says, and how its output should be structured.
//
// Workers are NOT a closed set — callers can register custom types.
// Built-in types: explorer, planner, worker, verifier.
type WorkerType struct {
	Name         string
	Description  string
	SystemPrompt string
	// AllowedTools is a list of tool names the worker may call.
	// Empty means "all tools the parent toolkit exposes" minus the
	// orchestration tools (which are always blocked for workers to
	// prevent recursive spawning).
	AllowedTools []string
	// OneShot signals that follow-up messages don't make sense for
	// this worker type — it returns once and is done.
	OneShot bool
}

// builtinWorkerTypes is the registry of types known to wuu out of
// the box. Custom types can be added at runtime via RegisterWorkerType.
var builtinWorkerTypes = map[string]WorkerType{
	"explorer": {
		Name:        "explorer",
		Description: "Read-only investigator. Use for searching code, reading files, understanding architecture.",
		AllowedTools: []string{
			"read_file", "list_files", "grep", "glob", "web_search", "web_fetch", "load_skill",
		},
		OneShot: true,
		SystemPrompt: `You are an explorer sub-agent. Your job is to investigate the codebase and answer the orchestrator's question.

CRITICAL RULES:
- READ-ONLY: You may NOT modify, create, delete, or move any files. You have no edit/write/run_shell tools.
- Stay focused: Answer ONLY the question you were asked. Don't refactor, don't suggest changes, don't go on tangents.
- Be efficient: Use parallel tool calls when reading multiple files. Don't read more than you need.
- Be specific: Always cite file paths and line numbers in your findings.

OUTPUT FORMAT:
End your final message with a concise plain-text summary (under 250 words) including:
- What you found (file:line references)
- The root cause or relevant facts
- Anything notable the orchestrator should know

Do not include preamble, markdown headers, or code blocks unless they add value.`,
	},

	"planner": {
		Name:        "planner",
		Description: "Architect that designs implementation strategies. Read-only.",
		AllowedTools: []string{
			"read_file", "list_files", "grep", "glob", "web_search", "web_fetch", "load_skill",
		},
		OneShot: true,
		SystemPrompt: `You are a planner sub-agent. Your job is to design an implementation strategy for a task and report it back to the orchestrator.

CRITICAL RULES:
- READ-ONLY: You may NOT modify any files. You only explore and design.
- Be concrete: List specific files to modify, specific functions to add/change, specific patterns to follow.
- Consider alternatives: Briefly mention 1-2 alternatives if there's an obvious tradeoff.

OUTPUT FORMAT:
End your final message with a markdown plan containing these sections:

## Approach
One paragraph describing the high-level strategy.

## Files to Modify
- path/to/file1.ext: what changes
- path/to/file2.ext: what changes

## Risks / Open Questions
- Anything the orchestrator should be aware of before implementation begins.

## Verification
How to confirm the change works (test command, manual check, etc.).`,
	},

	"worker": {
		Name:         "worker",
		Description:  "General-purpose implementer. Has the full tool set including read/write/edit/run_shell.",
		AllowedTools: nil, // nil means "all non-orchestration tools"
		OneShot:      false,
		SystemPrompt: `You are a worker sub-agent. Your job is to implement the changes the orchestrator described and report what you did.

CRITICAL RULES:
- Make ONLY the changes described in your task prompt. Do not refactor surrounding code.
- Verify your work: run tests, lint, or build commands when applicable.
- Be honest: if you encounter a problem you can't fix, report it clearly instead of papering over it.

OUTPUT FORMAT:
End your final message with a concise summary including:
- Which files you changed (full paths from your worktree root)
- A brief description of each change
- Whether tests/build/lint passed
- Any follow-ups or warnings the orchestrator should know about`,
	},

	"verifier": {
		Name:        "verifier",
		Description: "Adversarial tester. Tries to break the implementation. Read-only on project files.",
		AllowedTools: []string{
			"read_file", "list_files", "grep", "glob", "run_shell", "load_skill",
		},
		OneShot: false,
		SystemPrompt: `You are a verifier sub-agent. Your job is NOT to confirm the implementation works — it's to TRY TO BREAK IT.

CRITICAL RULES:
- DO NOT modify project files. You may write ephemeral test scripts to /tmp if needed.
- Run the project's test suite, linters, type checkers, and build commands.
- Look for the LAST 20%: edge cases, error paths, race conditions, missing tests, the user-rejected-input case.
- Don't be seduced by the first 80%: a UI rendering correctly does not mean the backend handles bad input.
- Never write "PASS" without showing the actual command output that justifies it.

REQUIRED STEPS:
1. Read the project's CLAUDE.md / README / AGENTS.md for build & test commands.
2. Run the build.
3. Run the test suite.
4. Run linters / type checkers if configured.
5. Probe for edge cases related to the changes.

OUTPUT FORMAT:
For each check, use this structure:

### Check: <what you're verifying>
**Command:**
  <exact command run>
**Output:**
  <actual terminal output>
**Result:** PASS / FAIL / SKIPPED (with reason)

End your final message with EXACTLY one of these lines:
VERDICT: PASS
VERDICT: FAIL
VERDICT: PARTIAL`,
	},
}

// LookupWorkerType returns the named worker type, or an error if
// unknown. Falls back to "worker" if name is empty.
func LookupWorkerType(name string) (WorkerType, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		name = "worker"
	}
	if wt, ok := builtinWorkerTypes[name]; ok {
		return wt, nil
	}
	return WorkerType{}, fmt.Errorf("unknown worker type %q (available: %s)", name, knownTypes())
}

// knownTypes returns a sorted list of registered worker type names.
func knownTypes() string {
	names := make([]string, 0, len(builtinWorkerTypes))
	for n := range builtinWorkerTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// alwaysBlockedTools is the set of orchestration tools that workers
// can never use (no recursive sub-spawning, no agent management).
var alwaysBlockedTools = map[string]struct{}{
	"spawn_agent":           {},
	"send_message_to_agent": {},
	"stop_agent":            {},
	"list_agents":           {},
}

// FilterToolsForWorker returns the subset of fullList that this worker
// type is allowed to call. Always strips orchestration tools.
func FilterToolsForWorker(wt WorkerType, fullList []string) []string {
	out := make([]string, 0, len(fullList))
	allowSet := map[string]struct{}{}
	for _, t := range wt.AllowedTools {
		allowSet[t] = struct{}{}
	}
	for _, name := range fullList {
		if _, blocked := alwaysBlockedTools[name]; blocked {
			continue
		}
		if len(wt.AllowedTools) == 0 {
			// nil means all non-orchestration tools allowed
			out = append(out, name)
			continue
		}
		if _, ok := allowSet[name]; ok {
			out = append(out, name)
		}
	}
	return out
}
