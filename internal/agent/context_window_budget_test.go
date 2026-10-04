package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestFreshContextShrinksFittingToolProgress(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "system", Content: "instructions"}, {Role: "user", Content: "finish the migration", Seq: 1}}
	for i := 0; i < 12; i++ {
		messages = append(messages,
			providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: string(rune('a' + i)), Name: "read", Arguments: `{}`}}, Seq: i*2 + 2},
			providers.ChatMessage{Role: "tool", ToolCallID: string(rune('a' + i)), Content: strings.Repeat("已完成迁移，验证通过。", 160), Seq: i*2 + 3},
		)
	}
	before := estimateFreshContextMessages(messages)
	replacement, err := buildFreshContext(messages, 25, 0, before*2)
	if err != nil {
		t.Fatal(err)
	}
	if estimateFreshContextMessages(replacement) >= before {
		t.Fatal("fitting history reset remained a no-op")
	}
	if err := providers.ValidateToolCallHistory(replacement); err != nil {
		t.Fatal(err)
	}
	foundTask := false
	for _, message := range replacement {
		foundTask = foundTask || message.Seq == 1
	}
	if !foundTask {
		t.Fatal("lost compact current task anchor")
	}
}

// Pending usage and pre-send budget must measure the same path-only projection.
func TestAttachmentBudgetMatchesNativeInputPolicy(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "Read /tmp/clip.mp4", Files: []providers.InputFile{{MediaType: "video/mp4", Data: strings.Repeat("AAAA", 650_000), LocalPath: "/tmp/clip.mp4"}}}}
	policy := providers.MediaInputPolicy{VideoKnown: true}
	runner := &StreamRunner{MediaInput: policy}
	usage, _ := runner.prepareUsageTracker(history)
	request := providers.ChatRequest{Messages: history, MediaInput: policy}
	expected := estimateMessages(providers.ProjectMediaForPolicy(history, policy))
	if expected > 1000 || usage.EstimateCurrent() != expected || estimateOutboundRequestTokens(request) != expected {
		t.Fatalf("budgets do not match path-only projection: pending=%d request=%d projected=%d", usage.EstimateCurrent(), estimateOutboundRequestTokens(request), expected)
	}
}

// Regression: a 2 MB upload must not become 650k tokens or disappear at rollover.
func TestFreshContextRetainsUploadedFile(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "system", Content: "instructions"}, {Role: "user", Content: strings.Repeat("old history ", 10_000)}, {Role: "assistant", Content: "done"}, {Role: "user", Seq: 4, Content: "Save the attachment at /tmp/clip.mp4", Files: []providers.InputFile{{MediaType: "video/mp4", Data: strings.Repeat("AAAA", 650_000), LocalPath: "/tmp/clip.mp4"}}}}
	replacement, err := buildFreshContext(messages, 4, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range replacement {
		if msg.Seq == 4 && strings.Contains(msg.Content, "/tmp/clip.mp4") {
			return
		}
	}
	t.Fatal("context rollover lost latest attachment")
}

func TestFreshContextCannotDiscardRequiredAttachment(t *testing.T) {
	messages := []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("old history ", 10_000)},
		{Role: "user", Content: "Analyze the required evidence", Files: []providers.InputFile{{MediaType: "application/pdf", Data: "AAAA", Required: true}}},
	}
	if _, err := buildFreshContext(messages, 2, 0, 1000); err != ErrFreshContextTooLarge {
		t.Fatalf("must fail instead of dropping required evidence: %v", err)
	}
}

func TestFreshContextFitsSmallerModel(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "system", Content: "instructions"}}
	for range 50 {
		messages = append(messages, providers.ChatMessage{Role: "user", Content: strings.Repeat("历史", 500)})
	}
	replacement, err := buildFreshContext(messages, 50, 100, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if estimateFreshContextMessages(replacement)+100 > 4000 {
		t.Fatal("fresh window exceeded the smaller model budget")
	}
}

func TestBackgroundNoteBudgetNeverClaimsUnseenSuffix(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "system", Content: "instructions"}}
	for i := 0; i < 25; i++ {
		messages = append(messages, providers.ChatMessage{Role: "user", Content: strings.Repeat("历史事实", 150), Seq: i + 1})
	}
	ctx := context.WithValue(context.Background(), compactionNoteBudgetKey{}, 4000)
	var seen []providers.ChatMessage
	note, _, err := generateCompactionNote(ctx, cancellationNoteProvider{}, &cancellationNoteStore{}, func(_ context.Context, history []providers.ChatMessage, plan CompactionNotePlan) (CompactionNoteForkResult, error) {
		seen = history
		if estimateCompactionMessagesTokens(history) > 3000 {
			t.Fatal("fork snapshot exceeded reserved input")
		}
		return CompactionNoteForkResult{Markdown: "bounded progress"}, nil
	}, "small", messages, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) >= len(messages) || note.CoveredMessages != len(seen) || !validCompactionNote(note, messages) {
		t.Fatalf("coverage includes unseen facts: %+v seen=%d", note, len(seen))
	}
}

func TestReducedNoteForkRetainsPriorProgress(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "system", Content: "instructions"}}
	for i := 0; i < 30; i++ {
		messages = append(messages, providers.ChatMessage{Role: "user", Content: strings.Repeat("历史事实", 150), Seq: i + 1})
	}
	previous := CompactionNote{Markdown: "Migration already applied; do not apply it again.", CoveredMessages: 20, CoveredHash: CompactionHistoryHash(messages[:20])}
	ctx := context.WithValue(context.Background(), compactionNoteBudgetKey{}, 4000)
	note, _, err := generateCompactionNote(ctx, cancellationNoteProvider{}, &cancellationNoteStore{note: previous}, func(_ context.Context, history []providers.ChatMessage, _ CompactionNotePlan) (CompactionNoteForkResult, error) {
		found := false
		for _, message := range history {
			found = found || strings.Contains(message.Content, previous.Markdown)
		}
		if !found || estimateCompactionMessagesTokens(history) > 3000 {
			t.Fatal("reduced fork lost prior progress or exceeded its budget")
		}
		return CompactionNoteForkResult{Markdown: "Migration applied; verification continued."}, nil
	}, "small", messages, true)
	if err != nil {
		t.Fatal(err)
	}
	if note.CoveredMessages <= previous.CoveredMessages || note.CoveredMessages >= len(messages) || !validCompactionNote(note, messages) {
		t.Fatalf("invalid incremental coverage: %+v", note)
	}
}
