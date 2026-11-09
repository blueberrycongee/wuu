package exec

import (
	"strings"
	"testing"
)

func TestResolvePromptFromArgs(t *testing.T) {
	got, err := ResolvePrompt(PromptInput{Args: []string{"fix", "the", "test"}})
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if got != "fix the test" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestResolvePromptFromStdin(t *testing.T) {
	got, err := ResolvePrompt(PromptInput{
		Stdin:       strings.NewReader("from stdin\n"),
		StdinIsPipe: true,
	})
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if got != "from stdin" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestResolvePromptWrapsPipedContext(t *testing.T) {
	got, err := ResolvePrompt(PromptInput{
		Args:        []string{"use this log"},
		Stdin:       strings.NewReader("panic: boom\n"),
		StdinIsPipe: true,
	})
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	want := "use this log\n\n<stdin>\npanic: boom\n</stdin>"
	if got != want {
		t.Fatalf("prompt mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestResolvePromptDashReadsStdin(t *testing.T) {
	got, err := ResolvePrompt(PromptInput{
		Args:  []string{"-"},
		Stdin: strings.NewReader("task body\n"),
	})
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if got != "task body" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestResolvePromptRejectsEmptyInput(t *testing.T) {
	_, err := ResolvePrompt(PromptInput{})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt required error, got %v", err)
	}
}

func TestResolvePromptAllowsEmptyInputForAttachmentOnlyRun(t *testing.T) {
	got, err := ResolvePrompt(PromptInput{AllowEmpty: true})
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if got != "" {
		t.Fatalf("prompt = %q", got)
	}
}
