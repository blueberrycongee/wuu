package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
)

func TestToolkit_LoadSkillRecordsResultAction(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	skillDir := filepath.Join(t.TempDir(), "review")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "demo.txt"), []byte("demo"), 0644); err != nil {
		t.Fatalf("write skill resource: %v", err)
	}
	kit.SetSkills([]skills.Skill{{
		Name:         "review",
		Description:  "Review code changes.",
		WhenToUse:    "Use for code review.",
		Content:      "Review ${ARGUMENTS}. Keep `!printf should-not-run` literal.",
		Source:       "project",
		Dir:          skillDir,
		AllowedTools: []string{"read_file"},
	}})

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "load_skill",
		Arguments: `{"name":"review","arguments":"the diff"}`,
	})
	if err != nil {
		t.Fatalf("load_skill: %v", err)
	}
	var parsed struct {
		Action   string `json:"action"`
		Output   string `json:"output"`
		Metadata struct {
			Name string `json:"name"`
			Dir  string `json:"dir"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse load_skill response: %v", err)
	}
	if parsed.Action != "load_skill" || parsed.Metadata.Name != "review" || !strings.Contains(parsed.Output, "the diff") {
		t.Fatalf("unexpected load_skill response: %+v", parsed)
	}
	for _, want := range []string{
		`<skill_content name="review">`,
		"<skill_files>",
		filepath.Join(skillDir, "scripts", "demo.txt"),
		"`!printf should-not-run`",
	} {
		if !strings.Contains(parsed.Output, want) {
			t.Fatalf("load_skill output missing %q:\n%s", want, parsed.Output)
		}
	}
	if parsed.Metadata.Dir != skillDir {
		t.Fatalf("metadata dir = %q, want %q", parsed.Metadata.Dir, skillDir)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || records[0].ResultAction != "load_skill" {
		t.Fatalf("load_skill telemetry missing result action: %+v", records)
	}
}

func TestToolkit_LoadSkillFiltersByActiveSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetSkills([]skills.Skill{
		{
			Name:         "commit",
			Description:  "Create a commit.",
			WhenToUse:    "Use when asked to commit.",
			Content:      "Use bash to run git status.",
			AllowedTools: []string{"bash"},
		},
		{
			Name:         "misdeclared-shell",
			Description:  "Misdeclared shell workflow.",
			WhenToUse:    "Use when asked to inspect a repo.",
			Content:      "Git: run git-status before continuing.",
			AllowedTools: []string{"read_file"},
		},
		{
			Name:         "claude-style-shell",
			Description:  "Claude style tool declaration.",
			WhenToUse:    "Use when asked to inspect terminal output.",
			Content:      "Run the command.",
			AllowedTools: []string{"Bash(git status:*)"},
		},
		{
			Name:         "plan",
			Description:  "Plan the work.",
			WhenToUse:    "Use when asked to plan.",
			Content:      "Create an implementation plan.",
			AllowedTools: []string{"read_file", "grep", "glob"},
		},
	})
	kit.SetActiveProfile(modelprofile.Resolve("ollama", "llama-coder"), true)

	defs := kit.Definitions()
	if !containsProfileDef(defs, "load_skill") {
		t.Fatalf("local/no-shell surface should still expose load_skill for compatible skills, got %v", sortedProfileDefNames(defs))
	}
	if names := strings.Join(kit.env.SkillNames(), ","); names != "plan" {
		t.Fatalf("visible skill filtering must hide incompatible skills and keep compatible ones; names=%v", kit.env.SkillNames())
	}

	for _, skillName := range []string{"commit", "misdeclared-shell", "claude-style-shell"} {
		_, err = kit.Execute(context.Background(), providers.ToolCall{
			Name:      "load_skill",
			Arguments: `{"name":"` + skillName + `"}`,
		})
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("local/no-shell must not load incompatible skill %q, got %v", skillName, err)
		}
	}
}
