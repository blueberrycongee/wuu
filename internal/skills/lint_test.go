package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func severities(issues []LintIssue) (errors, warnings int) {
	for _, issue := range issues {
		switch issue.Severity {
		case LintError:
			errors++
		case LintWarning:
			warnings++
		}
	}
	return
}

func hasMessage(issues []LintIssue, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, fragment) {
			return true
		}
	}
	return false
}

func TestLintCleanSkill(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "review-notes", "---\nname: review-notes\ndescription: Review meeting notes.\n---\n\nDo the review.\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}

func TestLintMissingFrontmatter(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "no-front", "just a body, no fence\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := severities(issues)
	if errs != 1 || !hasMessage(issues, "missing YAML frontmatter") {
		t.Fatalf("expected one missing-frontmatter error, got %+v", issues)
	}
}

func TestLintInvalidYAML(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "bad-yaml", "---\ndescription: [unclosed\n---\n\nBody.\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMessage(issues, "not valid YAML") {
		t.Fatalf("expected YAML error, got %+v", issues)
	}
	// Broken YAML empties every field, so the empty-description warning fires too.
	if !hasMessage(issues, "description is empty") {
		t.Fatalf("expected empty-description warning, got %+v", issues)
	}
}

func TestLintNonPortableFolderName(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "Bad_Name", "---\ndescription: x\n---\n\nBody.\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := severities(issues)
	if errs != 1 || !hasMessage(issues, "not a portable skill name") {
		t.Fatalf("expected portable-name error, got %+v", issues)
	}
}

func TestLintNotExecutedFields(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "forky", strings.Join([]string{
		"---",
		"description: Forks things.",
		"context: fork",
		"agent: general-purpose",
		"model: haiku",
		"effort: high",
		"paths: [\"src/**\"]",
		"hooks:",
		"  stop:",
		"    - type: command",
		"      command: echo hi",
		"---",
		"",
		"Body.",
	}, "\n"))
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"\"context\"", "\"agent\"", "\"model\"", "\"effort\"", "\"paths\"", "\"hooks\""} {
		if !hasMessage(issues, field) {
			t.Fatalf("expected not-executed warning for %s, got %+v", field, issues)
		}
	}
	errs, _ := severities(issues)
	if errs != 0 {
		t.Fatalf("not-executed fields must be warnings, got %+v", issues)
	}
}

func TestLintDefaultsAreNotFlagged(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "plain", "---\ndescription: Plain skill.\ncontext: inline\nmodel: inherit\n---\n\nBody.\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("inline/inherit declarations match runtime behavior and must not warn, got %+v", issues)
	}
}

func TestLintUnknownKeyAndNameMismatch(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "actual-name", "---\nname: other-name\ndescription: x\ndescripton-typo: y\n---\n\nBody.\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMessage(issues, "unknown frontmatter key") {
		t.Fatalf("expected unknown-key warning, got %+v", issues)
	}
	if !hasMessage(issues, "folder name \"actual-name\" is canonical") {
		t.Fatalf("expected name-mismatch warning, got %+v", issues)
	}
}

func TestLintRootDirectoryAndFlatFile(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "good-one", "---\ndescription: Fine.\n---\n\nBody.\n")
	if err := os.WriteFile(filepath.Join(root, "flat-note.md"), []byte("---\ndescription: Flat.\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, err := LintPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected clean root lint, got %+v", issues)
	}

	empty := filepath.Join(root, "empty-root")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	issues, err = LintPath(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMessage(issues, "no skills found") {
		t.Fatalf("expected no-skills warning, got %+v", issues)
	}
}

func TestLintEmptyBody(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "hollow", "---\ndescription: x\n---\n")
	issues, err := LintPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMessage(issues, "skill body is empty") {
		t.Fatalf("expected empty-body warning, got %+v", issues)
	}
}
