package skills

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProcessSkillBody_VariableSubstitution(t *testing.T) {
	body := "Args: ${ARGUMENTS}\nDir: ${CLAUDE_SKILL_DIR}\nID: ${CLAUDE_SESSION_ID}"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		Arguments:        "hello",
		SkillDir:         "/tmp/skill",
		SessionID:        "abc-123",
		AllowInlineShell: true,
	})
	want := "Args: hello\nDir: /tmp/skill\nID: abc-123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProcessSkillBody_InlineBacktick(t *testing.T) {
	body := "Hello `!echo world`!"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
	})
	if got != "Hello world!" {
		t.Fatalf("got %q", got)
	}
}

func TestProcessSkillBody_CodeBlock(t *testing.T) {
	body := "Output:\n```!\necho line1\necho line2\n```\nDone."
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
	})
	if !strings.Contains(got, "line1\nline2") {
		t.Fatalf("expected line1\\nline2 in output, got %q", got)
	}
	if !strings.Contains(got, "Done.") {
		t.Fatalf("expected trailing 'Done.' preserved, got %q", got)
	}
}

func TestProcessSkillBody_VariableInsideCommand(t *testing.T) {
	body := "Result: `!echo ${ARGUMENTS}`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		Arguments:        "from-args",
		AllowInlineShell: true,
	})
	if got != "Result: from-args" {
		t.Fatalf("got %q", got)
	}
}

func TestProcessSkillBody_CommandFailure(t *testing.T) {
	body := "Try: `!false`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
	})
	if !strings.Contains(got, "[error:") {
		t.Fatalf("expected error marker, got %q", got)
	}
}

func TestProcessSkillBody_DisabledShell(t *testing.T) {
	body := "Hello `!echo dangerous`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: false,
	})
	if got != body {
		t.Fatalf("expected unchanged body when shell disabled, got %q", got)
	}
}

func TestProcessSkillBody_Timeout(t *testing.T) {
	body := "Slow: `!sleep 1`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
		PerCmdTimeout:    100 * time.Millisecond,
	})
	if !strings.Contains(got, "timed out") {
		t.Fatalf("expected timeout marker, got %q", got)
	}
}

func TestProcessSkillBody_OutputTruncation(t *testing.T) {
	// Generate output larger than the cap.
	body := "Big: `!yes y | head -c 200`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
		MaxOutputBytes:   50,
	})
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestProcessSkillBody_MultipleCommands(t *testing.T) {
	body := "A=`!echo first` B=`!echo second`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
	})
	if got != "A=first B=second" {
		t.Fatalf("got %q", got)
	}
}

func TestProcessSkillBody_CommandUsesSkillDir(t *testing.T) {
	dir := t.TempDir()
	body := "Files: `!ls`"
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		SkillDir:         dir,
		AllowInlineShell: true,
	})
	// Empty dir → empty ls output → "Files: "
	if got != "Files: " {
		t.Fatalf("expected empty ls in tempdir, got %q", got)
	}
}

func TestProcessSkillBody_NoBackticksNoChange(t *testing.T) {
	body := "Just regular text. Nothing to see here."
	got := ProcessSkillBody(context.Background(), body, ProcessOptions{
		AllowInlineShell: true,
	})
	if got != body {
		t.Fatalf("expected unchanged body, got %q", got)
	}
}
