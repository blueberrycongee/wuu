package promptservice

import (
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/prompt"
)

// WorkerContext supplies role and execution facts to the selected policy.
type WorkerContext struct {
	Type            string   `json:"type,omitempty"`
	Role            string   `json:"role,omitempty"`
	Prompt          string   `json:"prompt,omitempty"`
	ContextScope    string   `json:"context_scope,omitempty"`
	OutputSchema    string   `json:"output_schema,omitempty"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
	Isolation       string   `json:"isolation"`
	AgentID         string   `json:"agent_id,omitempty"`
	AgentPath       string   `json:"agent_path,omitempty"`
	AgentProfile    string   `json:"agent_profile,omitempty"`
}

// AssembleWorkerPrompt is native worker policy, also used by explicit embedded
// callers without a selected prompt callback. External providers need not use it.
func AssembleWorkerPrompt(base, workerRoot string, wt WorkerContext) Result {
	var b strings.Builder
	b.WriteString(wt.Prompt)
	b.WriteString("\n\n")
	if wt.Role != "" || wt.ContextScope != "" || wt.OutputSchema != "" || len(wt.SuccessCriteria) > 0 {
		b.WriteString("## Role Contract\n")
		if wt.Role != "" {
			fmt.Fprintf(&b, "Role: %s\n", wt.Role)
		}
		if wt.ContextScope != "" {
			fmt.Fprintf(&b, "Context scope: %s\n", wt.ContextScope)
		}
		if wt.OutputSchema != "" {
			fmt.Fprintf(&b, "Output schema: %s\n", wt.OutputSchema)
		}
		if len(wt.SuccessCriteria) > 0 {
			b.WriteString("Success criteria:\n")
			for _, item := range wt.SuccessCriteria {
				fmt.Fprintf(&b, "- %s\n", item)
			}
		}
		b.WriteString("\n")
	}
	switch wt.Isolation {
	case "worktree":
		fmt.Fprintf(&b, "Your working directory is %s — an isolated worktree for this worker. ", workerRoot)
		b.WriteString("Edits you make stay sandboxed; the orchestrator will inspect the worktree after you finish. ")
	default: // inplace
		fmt.Fprintf(&b, "Your working directory is %s — the SHARED parent repository. ", workerRoot)
		b.WriteString("You are running inplace (no worktree isolation), so be especially careful: ")
		b.WriteString("read-only operations are safe, but any file you modify is visible to the orchestrator and other workers immediately. ")
	}
	b.WriteString("All file paths in your tools resolve relative to this directory. ")
	b.WriteString("Treat command execution as non-interactive when the active tool surface exposes it. Never rely on editors, pagers, password prompts, or confirmation dialogs. If command execution is unavailable under the active tool surface, report skipped command-based verification instead of inventing another path. Profile-specific tool-surface guidance tells you which command capability exists and how to use it. ")
	b.WriteString("You cannot spawn or manage other agents from this worker. If the task seems to require additional delegation, report that need in your final handoff so the parent can decide and coordinate.\n")
	var sections prompt.Builder
	sections.AddSection("worker_role", b.String(), true)
	sections.AddSection("worker_base", base, true)
	if base != "" {
		sections.AddSection("worker_override", "Worker override: if any inherited text above describes the MAIN interactive agent as read-only, or says file changes / command execution must be delegated, ignore that text. It applies to the parent, not to you. If a tool is in your tool list, you may use it unless your task prompt explicitly forbids it.", true)
	}
	built := sections.BuildWithInfo()
	out := Result{Content: built.Content, Sections: make([]Section, 0, len(built.Sections))}
	for _, s := range built.Sections {
		out.Sections = append(out.Sections, Section{Key: s.Key, Static: s.Static, Bytes: s.Bytes, Hash: s.Hash})
	}
	return out
}
