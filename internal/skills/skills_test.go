package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commit.md")

	content := "---\nname: /commit\ndescription: Create a git commit\n---\nThis skill creates commits.\nWith multiple lines."
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	skill, err := parseSkillFile(path, "project")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}
	if canonicalName(skill.Name) != "commit" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Description != "Create a git commit" {
		t.Fatalf("unexpected description: %q", skill.Description)
	}
	if skill.Source != "project" {
		t.Fatalf("unexpected source: %q", skill.Source)
	}
	if skill.Content != "This skill creates commits.\nWith multiple lines." {
		t.Fatalf("unexpected content: %q", skill.Content)
	}
}

func TestParseSkillFile_LoopMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop.md")

	content := strings.Join([]string{
		"---",
		"name: loop",
		"description: Run a durable loop",
		"trigger-condition: long-running task",
		"allowed-tools: [read_file, bash]",
		"required-context: [state, failures]",
		"examples: [continue loop, recover failed task]",
		"verification-checklist: [state persisted, verifier passed]",
		"progressive-disclosure: load state first",
		"---",
		"Body.",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	skill, err := parseSkillFile(path, "project")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}
	if skill.TriggerCondition != "long-running task" {
		t.Fatalf("TriggerCondition = %q", skill.TriggerCondition)
	}
	if got := strings.Join(skill.RequiredContext, ","); got != "state,failures" {
		t.Fatalf("RequiredContext = %q", got)
	}
	if got := strings.Join(skill.Examples, ","); got != "continue loop,recover failed task" {
		t.Fatalf("Examples = %q", got)
	}
	if got := strings.Join(skill.VerificationChecklist, ","); got != "state persisted,verifier passed" {
		t.Fatalf("VerificationChecklist = %q", got)
	}
	if skill.ProgressiveDisclosure != "load state first" {
		t.Fatalf("ProgressiveDisclosure = %q", skill.ProgressiveDisclosure)
	}
}

func TestParseSkillFile_YAMLBlockList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "block.md")
	// Block-style YAML lists are what real Claude Code / opencode skills use; the
	// old hand-rolled parser silently dropped them.
	content := strings.Join([]string{
		"---",
		"name: block",
		"description: Uses block-style YAML lists",
		"allowed-tools:",
		"  - read_file",
		"  - bash",
		"paths:",
		"  - \"src/**/*.ts\"",
		"---",
		"Body.",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	skill, err := parseSkillFile(path, "project")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}
	if got := strings.Join(skill.AllowedTools, ","); got != "read_file,bash" {
		t.Fatalf("AllowedTools = %q, want read_file,bash (block list must parse)", got)
	}
	if got := strings.Join(skill.Paths, ","); got != "src/**/*.ts" {
		t.Fatalf("Paths = %q, want src/**/*.ts", got)
	}
}

func TestParseSkillFile_NoName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")

	content := "---\ndescription: Review code\n---\nBody here."
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	skill, err := parseSkillFile(path, "user")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}
	// Should fall back to filename.
	if canonicalName(skill.Name) != "review" {
		t.Fatalf("expected review, got %q", skill.Name)
	}
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")

	if err := os.WriteFile(path, []byte("no frontmatter here"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := parseSkillFile(path, "project")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestDiscoverDirectorySkillWithoutDescriptionIsHiddenFromCatalog(t *testing.T) {
	projectDir := t.TempDir()
	skillDir := filepath.Join(projectDir, "manual-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	body := strings.Join([]string{
		"---",
		"name: manual-skill",
		"---",
		"# Manual Skill",
		"",
		"Instructions here.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	got := Discover(projectDir, "")
	if len(got) != 1 || got[0].Name != "manual-skill" {
		t.Fatalf("expected manual-skill, got %+v", got)
	}
	if got[0].Description != "" {
		t.Fatalf("disk skill description should not be inferred, got %q", got[0].Description)
	}
	if catalog := FormatAvailable(got, true); catalog != "No skills are currently available." {
		t.Fatalf("undescribed skill should be hidden from model catalog, got:\n%s", catalog)
	}
}

func TestDiscoverDirectorySkillsValidatePortableName(t *testing.T) {
	projectDir := t.TempDir()
	writeSkill := func(dirName, name string) {
		t.Helper()
		dir := filepath.Join(projectDir, dirName)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir skill: %v", err)
		}
		body := strings.Join([]string{
			"---",
			"name: " + name,
			"description: Test skill",
			"---",
			"Body.",
		}, "\n")
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}

	writeSkill("good-skill", "good-skill")
	writeSkill("wrong-dir", "other-name")
	writeSkill("Bad_Name", "Bad_Name")
	writeSkill("bad--hyphen", "bad--hyphen")

	// Folder name is the skill name (Claude Code semantics): a name/folder
	// mismatch keeps the skill under its folder name (wrong-dir), and only
	// non-portable folder names (uppercase, underscores, double hyphens) drop.
	got := Discover(projectDir, "")
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	if len(got) != 2 || !names["good-skill"] || !names["wrong-dir"] {
		t.Fatalf("expected good-skill and wrong-dir, got %+v", got)
	}
}

func TestDiscover(t *testing.T) {
	projectDir := t.TempDir()
	userDir := t.TempDir()

	// Create project skill.
	if err := os.WriteFile(
		filepath.Join(projectDir, "build.md"),
		[]byte("---\nname: /build\ndescription: Build project\n---\nBuild body."),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	// Create user skill that overrides a project skill.
	if err := os.WriteFile(
		filepath.Join(userDir, "build.md"),
		[]byte("---\nname: /build\ndescription: User build override\n---\nUser body."),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	// Create another user skill.
	if err := os.WriteFile(
		filepath.Join(userDir, "deploy.md"),
		[]byte("---\nname: /deploy\ndescription: Deploy\n---\nDeploy body."),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	skills := Discover(projectDir, userDir)

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Skills should be sorted by name.
	if skills[0].Name != "build" || skills[1].Name != "deploy" {
		t.Fatalf("unexpected skill order: %v, %v", skills[0].Name, skills[1].Name)
	}

	// build should be the project version (project overrides user).
	if skills[0].Description != "Build project" {
		t.Fatalf("expected project description for build, got %q", skills[0].Description)
	}
}

func TestDiscover_EmptyDirs(t *testing.T) {
	skills := Discover("", "")
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills for empty dirs, got %d", len(skills))
	}
}

func TestDiscoverSourceDirsPreservesSourceLabels(t *testing.T) {
	userPluginDir := t.TempDir()
	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(userPluginDir, "compose.md"),
		[]byte("---\nname: compose\ndescription: Plugin compose\n---\nPlugin body."),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "local.md"),
		[]byte("---\nname: local\ndescription: Local skill\n---\nLocal body."),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	got := DiscoverSourceDirs(
		[]SourceDir{{Path: projectDir, Source: "project"}},
		[]SourceDir{{Path: userPluginDir, Source: "plugin:compose"}},
	)
	if len(got) != 2 {
		t.Fatalf("skills = %+v", got)
	}
	compose, ok := Find(got, "compose")
	if !ok {
		t.Fatal("compose skill not found")
	}
	if compose.Source != "plugin:compose" {
		t.Fatalf("compose.Source = %q", compose.Source)
	}
}

func TestDiscoverSourceDirsRecursiveFindsNestedSkillDirectories(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(pathParts []string, name string) {
		t.Helper()
		dir := filepath.Join(append([]string{root}, pathParts...)...)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir skill: %v", err)
		}
		body := strings.Join([]string{
			"---",
			"name: " + name,
			"description: Nested skill",
			"---",
			"Body.",
		}, "\n")
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}
	writeSkill([]string{"packs", "deep-skill"}, "deep-skill")
	writeSkill([]string{"packs", "wrong-dir"}, "other-name")

	nonRecursive := DiscoverSourceDirs(nil, []SourceDir{{Path: root, Source: "config"}})
	if _, ok := Find(nonRecursive, "deep-skill"); ok {
		t.Fatalf("non-recursive source unexpectedly found nested skill: %+v", nonRecursive)
	}

	recursive := DiscoverSourceDirs(nil, []SourceDir{{Path: root, Source: "config", Recursive: true}})
	skill, ok := Find(recursive, "deep-skill")
	if !ok {
		t.Fatalf("recursive source did not find nested skill: %+v", recursive)
	}
	if skill.Source != "config" || !strings.HasSuffix(skill.Path, filepath.Join("packs", "deep-skill", "SKILL.md")) {
		t.Fatalf("unexpected recursive skill metadata: %+v", skill)
	}
	if _, ok := Find(recursive, "other-name"); ok {
		t.Fatalf("recursive source accepted mismatched directory skill: %+v", recursive)
	}
}

func TestBundledSkillsExcludesCommit(t *testing.T) {
	if _, ok := Find(BundledSkills(), "commit"); ok {
		t.Fatal("generic commit workflow must not be bundled")
	}
}
