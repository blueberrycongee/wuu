package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestPTCSkillsRetainReachableTools(t *testing.T) {
	// PTC must preserve skill discovery and loading, without restoring tools
	// disabled by the session or incompatible with the selected model family.
	all := []skills.Skill{
		{Name: "inspect", Description: "Inspect files", Content: "Inspect the workspace.", AllowedTools: []string{"read_file", "grep", "glob"}},
		{Name: "patch", Description: "Apply a patch", Content: "Apply the patch.", AllowedTools: []string{"apply_patch"}},
		{Name: "edit", Description: "Edit text", Content: "Edit the file.", AllowedTools: []string{"edit_file"}},
		{Name: "shell", Description: "Run a command", Content: "Run the command.", AllowedTools: []string{"bash"}},
	}
	for _, tc := range []struct {
		name, provider, model string
		disabled              []string
		want                  []string
	}{
		{"gpt", "openai", "gpt-5", nil, []string{"inspect", "patch", "shell"}},
		{"claude", "anthropic", "claude-sonnet-4", nil, []string{"inspect", "edit", "shell"}},
		{"local", "ollama", "llama-coder", nil, []string{"inspect", "edit"}},
		{"disabled read", "openai", "gpt-5", []string{"read_file"}, []string{"patch", "shell"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit := newCodeModeTestToolkit(t)
			kit.DisableTools(tc.disabled...)
			kit.ConfigureSurfaceForProviderModel(tc.provider, tc.model, true)
			kit.SetSkills(all)
			visible := FilterSkillsForSurface(all, kit.ActiveSurface())
			var names []string
			for _, skill := range visible {
				names = append(names, skill.Name)
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("skill catalog=%v, want %v", names, tc.want)
			}
			for _, skill := range all {
				args, err := json.Marshal(map[string]string{"name": skill.Name})
				if err != nil {
					t.Fatal(err)
				}
				result := runPTCProgram(t, kit, "return JSON.parse((await tools.load_skill("+string(args)+")).content[0].text).metadata.name;")
				allowed := slices.Contains(tc.want, skill.Name)
				if result.IsError == allowed {
					t.Errorf("load %s allowed=%v result=%s", skill.Name, allowed, result.TextProjection())
					continue
				}
				if allowed {
					var loaded string
					if err := json.Unmarshal([]byte(result.TextProjection()), &loaded); err != nil || loaded != skill.Name {
						t.Errorf("loaded %s: %s %v", skill.Name, result.TextProjection(), err)
					}
				}
			}
		})
	}
}
