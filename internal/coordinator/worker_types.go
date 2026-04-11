package coordinator

import (
	"fmt"
	"sort"
	"strings"
)

// IsolationMode controls whether a worker runs in its own git
// worktree or shares the parent's working directory.
type IsolationMode string

const (
	// IsolationInplace runs the worker directly in the parent repo's
	// working directory. Cheap (no checkout, no disk hit) and the
	// default for ALL built-in worker types — additive writes that
	// the user expects to land in the main repo are by far the common
	// case, and isolating them by default just hides the artifacts in
	// a worktree the user has to manually cherry-pick from. Matches
	// Claude Code's default: workers share the parent fs unless the
	// caller explicitly opts into isolation.
	IsolationInplace IsolationMode = "inplace"
	// IsolationWorktree creates a fresh `git worktree add --detach`
	// rooted at HEAD so the worker can edit files without colliding
	// with the parent or other concurrent workers. Costs one full
	// checkout per spawn. Reach for it when the worker may break the
	// build, fight with the user's uncommitted work, or run alongside
	// another writer touching the same files — never as a blanket
	// default for "this worker writes things".
	IsolationWorktree IsolationMode = "worktree"
)

// WorkerType describes a worker class: which tools it has, what its
// system prompt says, and how its output should be structured.
//
// wuu only ships ONE built-in type, "worker", with the full tool set.
// Role posture (verification mindset, read-only research, etc.) is
// injected by the coordinator at spawn time via the verbatim prompt
// presets in presets.go — that gives us CC's "single-type, prompt-
// driven role" model and avoids the per-type cognitive overhead of
// "which type should I pick?" the coordinator used to face on every
// spawn. The WorkerType struct is kept (rather than inlined) so
// future custom types can still be registered if a real need arises.
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
	// DefaultIsolation is the isolation mode used when the spawn
	// request doesn't specify one. All built-in types default to
	// IsolationInplace — the worker shares the parent repo, which
	// is what users expect when they ask for additive work. Custom
	// types that genuinely need a sandbox (e.g. an experiment runner
	// that may break the build) can override to IsolationWorktree
	// at registration time.
	DefaultIsolation IsolationMode
}

// builtinWorkerTypes is the registry of types known to wuu out of
// the box. Only one type is shipped: "worker", a general-purpose
// agent with the full tool set. Specialized roles (read-only
// research, adversarial verification) are injected by the
// coordinator at spawn time via the verbatim prompt presets in
// presets.go — see ResearchPreset / VerificationPreset.
//
// The previous design exposed four types (explorer, planner, worker,
// verifier), each with its own hardcoded tool whitelist and system
// prompt. That forced the coordinator to do a classification step
// on every spawn ("which type fits this task?"), which is a high-
// variance LLM judgment that the model often got subtly wrong on
// tasks that crossed type boundaries (e.g. "explore X then write a
// summary file" — explorer can't write, worker has no read-only
// frame). Collapsing to one type with prompt-driven role injection
// matches Claude Code's coordinator-mode design and removes that
// per-spawn cognitive tax.
var builtinWorkerTypes = map[string]WorkerType{
	"worker": {
		Name:             "worker",
		Description:      "General-purpose sub-agent with the full tool set (read/write/edit/run_shell). Use the VERIFICATION or RESEARCH prompt preset at the start of the worker prompt to inject a specialized role posture.",
		AllowedTools:     nil, // nil means "all non-orchestration tools"
		OneShot:          false,
		DefaultIsolation: IsolationInplace,
		SystemPrompt: `You are a worker sub-agent. The orchestrator delegated a focused task to you and you have the full tool set to complete it.

CRITICAL RULES:
- Make ONLY the changes described in your task prompt. Do not refactor surrounding code.
- Verify your work when applicable: run tests, lint, or build commands.
- Be honest: if you encounter a problem you can't fix, report it clearly instead of papering over it.
- If your task prompt starts with a "VERIFICATION mode" or "READ-ONLY RESEARCH mode" preamble, treat that preamble as authoritative and follow its rules — it overrides the generic guidance above.

OUTPUT FORMAT:
End your final message with a concise summary including:
- Which files you changed (absolute paths), if any
- A brief description of each change
- Whether tests/build/lint passed, if applicable
- Any follow-ups or warnings the orchestrator should know about

If your task prompt specified its own output format (e.g. the VERIFICATION preset's VERDICT line, or the RESEARCH preset's Answer/Evidence/Notes shape), follow THAT instead of this generic format.`,
	},
}

// NormalizeIsolation validates and lowercases an isolation mode. An
// empty input returns the worker type's default; if the type itself
// has no default, fall back to IsolationInplace (matching the global
// "share parent fs unless told otherwise" stance). Unknown values are
// rejected so the model can't sneak through arbitrary strings.
func NormalizeIsolation(raw string, wt WorkerType) (IsolationMode, error) {
	v := IsolationMode(strings.TrimSpace(strings.ToLower(raw)))
	if v == "" {
		if wt.DefaultIsolation == "" {
			return IsolationInplace, nil
		}
		return wt.DefaultIsolation, nil
	}
	switch v {
	case IsolationInplace, IsolationWorktree:
		return v, nil
	}
	return "", fmt.Errorf("unknown isolation %q (use %q or %q)", raw, IsolationInplace, IsolationWorktree)
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

// alwaysBlockedTools is the set of tools that workers can never use.
// Orchestration tools (no recursive sub-spawning / agent management)
// plus ask_user (workers cannot interrupt the human; only the main
// agent running in a live TUI may ask clarifying questions).
var alwaysBlockedTools = map[string]struct{}{
	"spawn_agent":           {},
	"send_message_to_agent": {},
	"stop_agent":            {},
	"list_agents":           {},
	"ask_user":              {},
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
