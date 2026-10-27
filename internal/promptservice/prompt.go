// Package promptservice owns the selected complete system-prompt policy.
package promptservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/instructions"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/prompts"
)

const ServiceName = "agent.prompt"
const NativeModule = "wuu.prompt.default"

// Request contains facts and explicit contributions, never an obligatory host base.
// Existing nested records retain their Go wire field spelling.
type Request struct {
	Role           string              `json:"role"`
	Worker         *WorkerContext      `json:"worker,omitempty"`
	CWD            string              `json:"cwd"`
	Date           string              `json:"date"`
	Provider       string              `json:"provider,omitempty"`
	Model          string              `json:"model,omitempty"`
	UserPrompt     string              `json:"user_prompt,omitempty"`
	Surface        capability.Surface  `json:"surface"`
	Instructions   []instructions.File `json:"instructions,omitempty"`
	Skills         []skills.Skill      `json:"skills,omitempty"`
	MemoryTeaching string              `json:"memory_teaching,omitempty"`
	MemoryIndex    string              `json:"memory_index,omitempty"`
	Contributions  []Contribution      `json:"contributions,omitempty"`
}

type Contribution struct {
	Key     string `json:"key"`
	Content string `json:"content"`
	Static  bool   `json:"static"`
}
type Section struct {
	Key    string `json:"key"`
	Static bool   `json:"static"`
	Bytes  int    `json:"bytes"`
	Hash   string `json:"hash"`
}
type Result struct {
	Content  string    `json:"content"`
	Sections []Section `json:"sections"`
}

// Assemble is the shipped policy. External providers are not required to call it.
func Assemble(in Request) (Result, error) {
	base := prompts.System()
	switch in.Role {
	case "main":
		base += "\n\n" + prompts.SystemMain()
	case "worker":
	default:
		return Result{}, fmt.Errorf("unknown prompt role %q", in.Role)
	}
	var b prompt.Builder
	b.AddSection("base", base, true)
	b.AddSection("tool_surface", in.Surface.SystemFragment, true)
	if _, ok := in.Surface.Tools["tool_search"]; ok {
		b.AddSection("deferred_tool_catalog", in.Surface.DeferredToolCatalog, true)
	}
	var environment strings.Builder
	environment.WriteString("# Environment\n\n")
	if in.CWD != "" {
		fmt.Fprintf(&environment, "- Current working directory: %s\n", in.CWD)
	}
	if in.Date != "" {
		fmt.Fprintf(&environment, "- Current date: %s\n", in.Date)
	}
	b.AddSection("environment", environment.String(), true)
	if strings.TrimSpace(in.UserPrompt) != "" {
		b.AddSection("user_custom_prompt", "# User Custom Instructions\n\nFollow these user-defined instructions unless they conflict with wuu's built-in behavior, safety, or tool-use discipline above.\n\n"+in.UserPrompt, true)
	}
	b.AddInstructions(in.Instructions)
	if strings.TrimSpace(in.MemoryTeaching) != "" {
		b.AddMemdir(in.MemoryTeaching, in.MemoryIndex)
	}
	if in.Surface.ProfileName != "" {
		b.AddSkills(FilterSkillsForSurface(in.Skills, in.Surface))
	}
	built := b.BuildWithInfo()
	out := Result{Content: built.Content, Sections: make([]Section, 0, len(built.Sections)+len(in.Contributions))}
	for _, s := range built.Sections {
		out.Sections = append(out.Sections, Section{Key: s.Key, Static: s.Static, Bytes: s.Bytes, Hash: s.Hash})
	}
	// Explicit contributions follow the native base; the selected external policy
	// may instead choose its own order or omit them entirely.
	var extra prompt.Builder
	for _, s := range in.Contributions {
		extra.AddSection(s.Key, s.Content, s.Static)
	}
	appended := extra.BuildWithInfo()
	if appended.Content != "" {
		out.Content += "\n\n" + appended.Content
	}
	for _, s := range appended.Sections {
		out.Sections = append(out.Sections, Section{Key: s.Key, Static: s.Static, Bytes: s.Bytes, Hash: s.Hash})
	}
	if in.Role == "worker" && in.Worker != nil {
		worker := AssembleWorkerPrompt(out.Content, in.CWD, *in.Worker)
		sections := make([]Section, 0, len(out.Sections)+2)
		for _, section := range worker.Sections {
			if section.Key == "worker_base" {
				sections = append(sections, out.Sections...)
			} else {
				sections = append(sections, section)
			}
		}
		worker.Sections = sections
		return worker, nil
	}
	return out, nil
}

// Client's callback must retain the admitted generation and authenticated caller.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Assemble(ctx context.Context, in Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return Result{}, err
	}
	raw, err = c.call(ctx, "assemble", raw)
	if err != nil {
		return Result{}, err
	}
	var result struct {
		Content  *string    `json:"content"`
		Sections *[]Section `json:"sections"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return Result{}, err
	}
	if result.Content == nil || result.Sections == nil {
		return Result{}, fmt.Errorf("invalid agent.prompt result: content and sections required")
	}
	return Result{Content: *result.Content, Sections: *result.Sections}, nil
}
