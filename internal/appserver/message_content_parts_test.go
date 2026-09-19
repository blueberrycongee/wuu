package appserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestUserMessageFromPromptPreservesValidContentParts(t *testing.T) {
	prompt := "pasted body\nregular text"
	parts := []providers.MessageContentPart{
		{Type: "pasted_text", Text: "pasted body\n"},
		{Type: "text", Text: "regular text"},
	}

	msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != prompt {
		t.Fatalf("Content = %q, want %q", msg.Content, prompt)
	}
	if len(msg.ContentParts) != 2 || msg.ContentParts[0].Type != "pasted_text" {
		t.Fatalf("ContentParts = %#v, want structured pasted and text parts", msg.ContentParts)
	}
}

func TestUserMessageFromPromptRejectsMismatchedContentParts(t *testing.T) {
	msg, err := userMessageFromPrompt(
		"canonical prompt",
		nil,
		nil,
		[]providers.MessageContentPart{{Type: "pasted_text", Text: "different text"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ContentParts) != 0 {
		t.Fatalf("ContentParts = %#v, want no presentation metadata for mismatched content", msg.ContentParts)
	}
}

func fileSelectionPartForTest(intent string) providers.MessageContentPart {
	part := providers.MessageContentPart{
		Type: "file_selection", ID: "selection-1", Intent: intent,
		Source: &providers.FileSelectionSource{
			Workspace: "/workspace", Path: "src/main.go",
			StartLine: 3, StartColumn: 2, EndLine: 4, EndColumn: 1,
			Quote: "return value\n", Revision: "sha256:captured-revision",
		},
	}
	if intent != "quote" {
		part.Comment = "Explain or improve this return."
	}
	// The client supplies the serialization; the server must preserve it verbatim.
	source, _ := json.Marshal(part.Source)
	part.Text = fmt.Sprintf("<file_selection id=%q intent=%q>\n%s\n%s\n</file_selection>", part.ID, part.Intent, source, part.Comment)
	return part
}

func TestFileSelectionContentPartsPreserveCanonicalPrompt(t *testing.T) {
	for _, intent := range []string{"comment", "edit", "quote"} {
		t.Run(intent, func(t *testing.T) {
			part := fileSelectionPartForTest(intent)
			parts := []providers.MessageContentPart{
				{Type: "text", Text: "  Review this:\n"}, part,
				{Type: "pasted_text", Text: "\nAdditional reference\n", Title: "Reference"},
			}
			prompt := strings.TrimSpace(parts[0].Text + part.Text + parts[2].Text)
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || !reflect.DeepEqual(msg.ContentParts, parts) {
				t.Fatalf("normalized message lost content or metadata: %+v", msg)
			}
			parts[1].Text = strings.Replace(part.Text, "return value", "return other", 1)
			msg, err = userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || len(msg.ContentParts) != 0 {
				t.Fatalf("mismatched metadata changed canonical prompt or survived: %+v", msg)
			}
		})
	}
}

func TestFileSelectionContentPartsDiscardInvalidMetadata(t *testing.T) {
	tests := map[string]func(*providers.MessageContentPart){
		"missing source":        func(p *providers.MessageContentPart) { p.Source = nil },
		"blank workspace":       func(p *providers.MessageContentPart) { p.Source.Workspace = " \t" },
		"blank path":            func(p *providers.MessageContentPart) { p.Source.Path = " " },
		"blank revision":        func(p *providers.MessageContentPart) { p.Source.Revision = " " },
		"empty quote":           func(p *providers.MessageContentPart) { p.Source.Quote = "" },
		"blank id":              func(p *providers.MessageContentPart) { p.ID = " " },
		"missing intent":        func(p *providers.MessageContentPart) { p.Intent = "" },
		"unknown intent":        func(p *providers.MessageContentPart) { p.Intent = "delete" },
		"zero start line":       func(p *providers.MessageContentPart) { p.Source.StartLine = 0 },
		"negative start column": func(p *providers.MessageContentPart) { p.Source.StartColumn = -1 },
		"zero end line":         func(p *providers.MessageContentPart) { p.Source.EndLine = 0 },
		"zero end column":       func(p *providers.MessageContentPart) { p.Source.EndColumn = 0 },
		"reversed lines":        func(p *providers.MessageContentPart) { p.Source.EndLine = p.Source.StartLine - 1 },
		"reversed columns": func(p *providers.MessageContentPart) {
			p.Source.EndLine = p.Source.StartLine
			p.Source.EndColumn = p.Source.StartColumn - 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			part := fileSelectionPartForTest("edit")
			mutate(&part)
			prompt := "Review:\n" + part.Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, []providers.MessageContentPart{
				{Type: "text", Text: "Review:\n"}, part,
			})
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || len(msg.ContentParts) != 0 {
				t.Fatalf("invalid metadata survived or changed model content: %+v", msg)
			}
		})
	}
}

func TestFileSelectionContentPartsAcceptRangeBoundaries(t *testing.T) {
	for _, end := range []struct{ line, column int }{{3, 2}, {3, 8}, {4, 1}} {
		t.Run(fmt.Sprintf("%d:%d", end.line, end.column), func(t *testing.T) {
			part := fileSelectionPartForTest("quote")
			part.Source.EndLine, part.Source.EndColumn = end.line, end.column
			part.Source.Quote = " \t"
			parts := normalizeMessageContentParts([]providers.MessageContentPart{part})
			if len(parts) != 1 || !reflect.DeepEqual(parts[0], part) {
				t.Fatalf("valid range or whitespace selection lost: %+v", parts)
			}
		})
	}
}

func TestFileSelectionContentPartsHistoryRoundTrip(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("rewrite=%t", rewrite), func(t *testing.T) {
			dir := t.TempDir()
			sess, err := session.CreateWithMetadata(dir, "selection-history", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			parts := []providers.MessageContentPart{
				{Type: "text", Text: "Review:\n"}, fileSelectionPartForTest("comment"),
				{Type: "pasted_text", Text: "\nReference", Title: "Notes"},
			}
			prompt := parts[0].Text + parts[1].Text + parts[2].Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if rewrite {
				err = rewriteChatHistory(dir, sess.ID, []providers.ChatMessage{msg})
			} else {
				_, err = appendChatMessage(dir, sess.ID, msg)
			}
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loadChatMessages(dir, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 || loaded[0].Content != prompt || !reflect.DeepEqual(loaded[0].ContentParts, parts) {
				t.Fatalf("provider replay lost content or metadata: %+v", loaded)
			}
			records, err := loadPersistedMessages(dir, sess.ID, false)
			if err != nil || len(records) != 1 {
				t.Fatalf("load display history: records=%+v err=%v", records, err)
			}
			item := chatMessageItem("item-1", chatMessageFromPersistedMessage(records[0]))
			if item.Text != prompt || !reflect.DeepEqual(item.ContentParts, parts) {
				t.Fatalf("history item lost content or metadata: %+v", item)
			}
			prepared, err := providers.PrepareMessagesForModelRequest("gpt-5", loaded)
			if err != nil || len(prepared) != 1 || prepared[0].Content != prompt {
				t.Fatalf("model request changed canonical content: %+v, err=%v", prepared, err)
			}
		})
	}
}

func TestFileSelectionContentPartsQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateWithMetadata(dir, "selection-queue", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	part := fileSelectionPartForTest("edit")
	msg, err := userMessageFromPrompt(part.Text, nil, nil, []providers.MessageContentPart{part})
	if err != nil {
		t.Fatal(err)
	}
	msg.ClientID = "queue-1"
	srv := &Server{rt: &runtime.Session{SessionDir: dir}}
	srv.enqueueQueuedUserTurn(sess.ID, queuedTurn{id: msg.ClientID, msg: msg})
	entry, ok := srv.takeNextQueuedUserTurn(sess.ID)
	if !ok {
		t.Fatal("queued message missing")
	}
	if _, err := srv.appendHeldUserTurns(sess.ID, []queuedTurn{entry}); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{rt: &runtime.Session{SessionDir: dir}}
	held, err := restarted.loadHeldUserTurns(sess.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("load held messages: %+v, err=%v", held, err)
	}
	if held[0].msg.Content != msg.Content || !reflect.DeepEqual(held[0].msg.ContentParts, msg.ContentParts) {
		t.Fatalf("held message lost content or metadata: %+v", held[0].msg)
	}
	// Exercise the protocol response as well as the stored Go representation.
	raw, err := json.Marshal(heldUserMessageSummary(sess.ID, held[0]))
	if err != nil {
		t.Fatal(err)
	}
	var summary HeldUserMessage
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Prompt != msg.Content || !reflect.DeepEqual(summary.ContentParts, msg.ContentParts) {
		t.Fatalf("queue response lost content or metadata: %+v", summary)
	}
	restarted.enqueueQueuedUserTurn(sess.ID, held[0])
	replayed, ok := restarted.takeNextQueuedUserTurn(sess.ID)
	if !ok || replayed.msg.Content != msg.Content || !reflect.DeepEqual(replayed.msg.ContentParts, msg.ContentParts) {
		t.Fatalf("replayed queue lost content or metadata: %+v", replayed)
	}
}
