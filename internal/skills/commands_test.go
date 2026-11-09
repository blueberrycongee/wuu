package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCommandFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParseCommandFile_FrontmatterStrippedToPureContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commit.md")
	writeCommandFile(t, path, strings.Join([]string{
		"---",
		"description: Create a git commit",
		"argument-hint: <message>",
		"allowed-tools: [Bash(git commit:*), Read]",
		"model: claude-opus-4",
		"---",
		"Create a commit for $ARGUMENTS.",
		"Keep it atomic.",
	}, "\n"))

	cmd, err := parseCommandFile(path, "project")
	if err != nil {
		t.Fatalf("parseCommandFile: %v", err)
	}
	if cmd.Name != "commit" {
		t.Fatalf("Name = %q, want commit", cmd.Name)
	}
	if cmd.Description != "Create a git commit" {
		t.Fatalf("Description = %q", cmd.Description)
	}
	if cmd.ArgumentHint != "<message>" {
		t.Fatalf("ArgumentHint = %q", cmd.ArgumentHint)
	}
	if cmd.Source != "project" {
		t.Fatalf("Source = %q", cmd.Source)
	}
	if !cmd.UserInvocable {
		t.Fatalf("UserInvocable = false, want true")
	}
	// Execution-binding fields must be silently ignored.
	if len(cmd.AllowedTools) != 0 {
		t.Fatalf("AllowedTools = %v, want none (allowed-tools must be ignored)", cmd.AllowedTools)
	}
	if cmd.Model != "" {
		t.Fatalf("Model = %q, want empty (model must be ignored)", cmd.Model)
	}
	// $ARGUMENTS is normalized to the ${ARGUMENTS} form the pipeline substitutes.
	want := "Create a commit for ${ARGUMENTS}.\nKeep it atomic."
	if cmd.Content != want {
		t.Fatalf("Content = %q, want %q", cmd.Content, want)
	}
}

func TestParseCommandFile_NoFrontmatterUsesFilenameAndBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summarize.md")
	body := "Summarize the current diff.\nBe concise."
	writeCommandFile(t, path, body)

	cmd, err := parseCommandFile(path, "user")
	if err != nil {
		t.Fatalf("parseCommandFile: %v", err)
	}
	if cmd.Name != "summarize" {
		t.Fatalf("Name = %q, want summarize", cmd.Name)
	}
	if cmd.Description != "" {
		t.Fatalf("Description = %q, want empty", cmd.Description)
	}
	if cmd.Content != body {
		t.Fatalf("Content = %q, want %q", cmd.Content, body)
	}
}

func TestParseCommandFile_PreservesInlineExecutionSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.md")
	// Claude Code's inline !`command` execution syntax must be preserved as text.
	body := "Current status: !`git status`\nProceed accordingly."
	writeCommandFile(t, path, "---\ndescription: Show status\n---\n"+body)

	cmd, err := parseCommandFile(path, "project")
	if err != nil {
		t.Fatalf("parseCommandFile: %v", err)
	}
	if !strings.Contains(cmd.Content, "!`git status`") {
		t.Fatalf("inline execution syntax not preserved: %q", cmd.Content)
	}
}

func TestParseCommandFile_AlreadyBracedArgumentsUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.md")
	writeCommandFile(t, path, "Run ${ARGUMENTS} now.")

	cmd, err := parseCommandFile(path, "project")
	if err != nil {
		t.Fatalf("parseCommandFile: %v", err)
	}
	if cmd.Content != "Run ${ARGUMENTS} now." {
		t.Fatalf("Content = %q, want braced form left untouched", cmd.Content)
	}
}

func TestDiscoverCommandsSourceDirs_Discovery(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude", "commands")
	wuuDir := filepath.Join(root, ".wuu", "commands")

	writeCommandFile(t, filepath.Join(claudeDir, "review.md"), "---\ndescription: Review changes\n---\nReview the diff.")
	writeCommandFile(t, filepath.Join(wuuDir, "deploy.md"), "Deploy the service.")
	// Non-markdown files and nested directories are ignored.
	writeCommandFile(t, filepath.Join(claudeDir, "notes.txt"), "ignore me")
	writeCommandFile(t, filepath.Join(claudeDir, "nested", "deep.md"), "nested command")

	got := DiscoverCommandsSourceDirs([]SourceDir{
		{Path: claudeDir, Source: "project"},
		{Path: wuuDir, Source: "project"},
	}, nil)

	names := map[string]bool{}
	for _, c := range got {
		names[c.Name] = true
	}
	if !names["review"] || !names["deploy"] {
		t.Fatalf("expected review and deploy, got %v", names)
	}
	if names["notes"] {
		t.Fatalf("non-markdown file was discovered")
	}
	if names["deep"] {
		t.Fatalf("nested command was discovered (should be flat only)")
	}
}

func TestDiscoverCommandsSourceDirs_ProjectOverridesUser(t *testing.T) {
	projectDir := t.TempDir()
	userDir := t.TempDir()
	writeCommandFile(t, filepath.Join(projectDir, "build.md"), "project build")
	writeCommandFile(t, filepath.Join(userDir, "build.md"), "user build")

	got := DiscoverCommandsSourceDirs(
		[]SourceDir{{Path: projectDir, Source: "project"}},
		[]SourceDir{{Path: userDir, Source: "user"}},
	)
	cmd, ok := Find(got, "build")
	if !ok {
		t.Fatalf("build command not found")
	}
	if cmd.Content != "project build" || cmd.Source != "project" {
		t.Fatalf("project did not override user: %+v", cmd)
	}
}

func TestMergeCommands_SkillsTakePrecedence(t *testing.T) {
	discovered := []Skill{
		{Name: "review", Description: "native skill", Source: "project"},
	}
	commands := []Skill{
		{Name: "review", Description: "command shadow", Source: "project"},
		{Name: "deploy", Description: "command", Source: "user"},
	}
	merged := MergeCommands(discovered, commands)

	review, ok := Find(merged, "review")
	if !ok || review.Description != "native skill" {
		t.Fatalf("native skill was shadowed by command: %+v", review)
	}
	if _, ok := Find(merged, "deploy"); !ok {
		t.Fatalf("non-conflicting command not merged")
	}
	if len(merged) != 2 {
		t.Fatalf("merged len = %d, want 2", len(merged))
	}
}
