package agentcontrol

import (
	"fmt"
	"sort"
	"strings"
)

// IsolationMode controls whether a worker runs in its own git
// worktree or shares the parent's working directory.
type IsolationMode string

const (
	IsolationInplace  IsolationMode = "inplace"
	IsolationWorktree IsolationMode = "worktree"
)

// WorkerType defines a role a worker can adopt.
type WorkerType struct {
	Name             string
	Role             string
	Description      string
	SystemPrompt     string
	AllowedTools     []string
	DisallowedTools  []string
	ContextScope     string
	OutputSchema     string
	SuccessCriteria  []string
	OneShot          bool
	Background       bool
	DefaultIsolation IsolationMode
	// RequiresReport marks verdict-class workers whose value depends on a
	// structured agent_report handoff. When such a worker completes without
	// filing one, the runtime issues a single mechanical closing turn with
	// tool_choice pinned to agent_report before results are delivered. It
	// never becomes a lifecycle state: if the closing turn still files
	// nothing, the run completes with a synthesized final_text report.
	RequiresReport bool
	Internal       bool
}

const DefaultSubagentType = "general-purpose"

var builtinWorkerTypes = map[string]WorkerType{
	DefaultSubagentType: {
		Name:             DefaultSubagentType,
		Role:             "Generalist",
		Description:      "General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks.",
		AllowedTools:     nil,
		OneShot:          false,
		DefaultIsolation: IsolationInplace,
		ContextScope:     "Self-contained task prompt plus repository instructions and skills.",
		OutputSchema:     "Final message is the deliverable: outcome, what was done, what changed, blockers, and verifiable evidence. agent_report is an optional structured handoff on top.",
		SuccessCriteria: []string{
			"Task scope is completed or clearly blocked.",
			"Changed files and verification are reported with evidence.",
		},
		SystemPrompt: "",
	},
	"worker": {
		Name:         "worker",
		Role:         "Worker",
		Description:  "Implement a scoped code change in an isolated worktree when edits are required.",
		SystemPrompt: "",
		AllowedTools: nil,
		ContextScope: "Concrete task brief, plan step, allowed write set, current worktree status, and verification command.",
		OutputSchema: "Implementation report with changed files, commands run, blockers, risks, and evidence.",
		SuccessCriteria: []string{
			"Only assigned files or behavior are changed.",
			"Targeted verification ran or a blocker explains why it could not run.",
		},
		OneShot:          false,
		Background:       false,
		DefaultIsolation: IsolationWorktree,
	},
}

// AvailableWorkerTypes returns user-selectable built-in worker roles sorted by
// name. Internal worker types remain available to trusted runtime paths through
// LookupWorkerType but are not advertised to models or accepted by public
// orchestration tools.
func AvailableWorkerTypes() []WorkerType {
	keys := make([]string, 0, len(builtinWorkerTypes))
	for key := range builtinWorkerTypes {
		if builtinWorkerTypes[key].Internal {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]WorkerType, 0, len(keys))
	for _, key := range keys {
		out = append(out, builtinWorkerTypes[key])
	}
	return out
}

// LookupPublicWorkerType resolves worker types exposed through spawn_agent.
func LookupPublicWorkerType(name string) (WorkerType, error) {
	workerType, err := LookupWorkerType(name)
	if err != nil {
		return WorkerType{}, err
	}
	if workerType.Internal {
		return WorkerType{}, fmt.Errorf("worker type %q is internal (available: %s)", name, strings.Join(AvailableWorkerTypeNames(), ", "))
	}
	return workerType, nil
}

// AvailableWorkerTypeNames returns all built-in worker role names sorted by name.
func AvailableWorkerTypeNames() []string {
	items := AvailableWorkerTypes()
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name)
	}
	return out
}

// LookupWorkerType resolves a worker type name to its definition.
func LookupWorkerType(name string) (WorkerType, error) {
	if name == "" {
		name = DefaultSubagentType
	}
	wt, ok := builtinWorkerTypes[name]
	if !ok {
		return WorkerType{}, fmt.Errorf("unknown worker type %q (available: %s)", name, strings.Join(AvailableWorkerTypeNames(), ", "))
	}
	return wt, nil
}

var defaultWorkerBlockedTools = map[string]struct{}{
	"spawn_agent":  {},
	"send_message": {},
	"close_agent":  {},
}

// FilterToolsForWorker returns the subset of fullList that this worker
// type is allowed to call.
func FilterToolsForWorker(wt WorkerType, fullList []string) []string {
	out := make([]string, 0, len(fullList))
	allowSet := map[string]struct{}{}
	for _, t := range wt.AllowedTools {
		allowSet[t] = struct{}{}
	}
	denySet := map[string]struct{}{}
	for _, t := range wt.DisallowedTools {
		denySet[t] = struct{}{}
	}
	for _, name := range fullList {
		if _, blocked := defaultWorkerBlockedTools[name]; blocked {
			continue
		}
		if _, denied := denySet[name]; denied {
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

// NormalizeIsolation resolves the effective isolation mode for a spawn request.
func NormalizeIsolation(reqIsolation string, wt WorkerType) (IsolationMode, error) {
	if reqIsolation == "" {
		if wt.DefaultIsolation != "" {
			return wt.DefaultIsolation, nil
		}
		return IsolationInplace, nil
	}
	switch strings.ToLower(reqIsolation) {
	case "inplace":
		return IsolationInplace, nil
	case "worktree":
		return IsolationWorktree, nil
	default:
		return "", fmt.Errorf("invalid isolation %q (valid: inplace, worktree)", reqIsolation)
	}
}
