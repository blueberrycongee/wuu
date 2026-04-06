package skills

import (
	"os"
	"path/filepath"
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
	if skill.Name != "/commit" {
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
	if skill.Name != "/review" {
		t.Fatalf("expected /review, got %q", skill.Name)
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
	if skills[0].Name != "/build" || skills[1].Name != "/deploy" {
		t.Fatalf("unexpected skill order: %v, %v", skills[0].Name, skills[1].Name)
	}

	// /build should be the user override.
	if skills[0].Description != "User build override" {
		t.Fatalf("expected user override for /build, got %q", skills[0].Description)
	}
}

func TestDiscover_EmptyDirs(t *testing.T) {
	skills := Discover("", "")
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills for empty dirs, got %d", len(skills))
	}
}
