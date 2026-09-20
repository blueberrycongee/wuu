package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func budgetHistory(results ...toolresult.Result) []providers.ChatMessage {
	messages := []providers.ChatMessage{{Role: "assistant"}}
	for i, result := range results {
		id := fmt.Sprintf("call_%d", i)
		messages[0].ToolCalls = append(messages[0].ToolCalls, providers.ToolCall{ID: id, Name: "lookup", Arguments: `{}`})
		messages = append(messages, providers.ChatMessage{Role: "tool", ToolCallID: id, Content: result.TextProjection(), ToolResult: &result})
	}
	return messages
}

func TestAggregateBudgetSurvivesRequestPreparation(t *testing.T) {
	text := func(n int) toolresult.Result { return toolresult.FromText(strings.Repeat("x", n)) }
	structured := toolresult.Result{StructuredContent: json.RawMessage(`{"value":"` + strings.Repeat("s", 200001) + `"}`)}
	mixed := text(199999)
	mixed.StructuredContent = json.RawMessage(`{"continuation":{"next":"synthetic-cursor"},"count":2}`)
	mixed.Meta = json.RawMessage(`{"private":"never-send-this"}`)
	mixed.IsError = true
	mixed.Content = append(mixed.Content,
		toolresult.ContentPart{Type: "image", MIMEType: "image/png", Data: "aW1hZ2U="},
		toolresult.ContentPart{Type: "audio", MIMEType: "audio/wav", Data: "YXVkaW8=", Name: "clip.wav"},
		toolresult.ContentPart{Type: "resource", Resource: json.RawMessage(`{"text":"resource body"}`)},
	)
	indexed := text(199999)
	indexed.StructuredContent = json.RawMessage(`{"count":1}`)
	for _, tc := range []struct {
		name    string
		results []toolresult.Result
	}{
		{"at_limit", []toolresult.Result{text(200000)}},
		{"one_over", []toolresult.Result{text(200001)}},
		{"asymmetric", []toolresult.Result{text(170000), text(50001)}},
		{"zero_allocation", []toolresult.Result{text(200001), text(200000)}},
		{"partial_marker", []toolresult.Result{text(100050), text(99975), text(99975)}},
		{"utf8", []toolresult.Result{toolresult.FromText(strings.Repeat("界🙂", 30000))}},
		{"structured_only", []toolresult.Result{structured}},
		{"index_tips_over", []toolresult.Result{indexed}},
		{"mixed_index_media_error", []toolresult.Result{mixed}},
		{"empty_results", []toolresult.Result{{}, {IsError: true}, toolresult.FromText(" \t")}},
		{"empty_result_at_limit", []toolresult.Result{text(200000), {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := budgetHistory(tc.results...)
			original := providers.CloneChatMessages(history)
			enforceAggregateResultBudget(history)
			settled := providers.CloneChatMessages(history)
			for _, model := range []string{"gpt-4o", "claude-sonnet-4-6", "mistral-large"} {
				prepared, err := providers.PrepareMessagesForModelRequest(model, history)
				if err != nil {
					t.Fatal(err)
				}
				total, count := 0, 0
				for _, msg := range prepared {
					if msg.Role != "tool" {
						continue
					}
					total += len(msg.Content)
					if !utf8.ValidString(msg.Content) {
						t.Errorf("%s: invalid UTF-8", model)
					}
					if strings.Contains(msg.Content, "never-send-this") {
						t.Error("private metadata leaked")
					}
					if !reflect.DeepEqual(msg.ToolResult.Content, original[count+1].ToolResult.Content) ||
						!reflect.DeepEqual(msg.ToolResult.StructuredContent, original[count+1].ToolResult.StructuredContent) ||
						msg.ToolResult.IsError != original[count+1].ToolResult.IsError {
						t.Error("raw result or error state lost")
					}
					if history[count+1].Content != original[count+1].Content && msg.Content != history[count+1].Content {
						t.Errorf("%s: budgeted text restored: settled=%d projected=%d", model, len(history[count+1].Content), len(msg.Content))
					}
					count++
				}
				if count != len(tc.results) || total > 200000 {
					t.Errorf("%s: tool count=%d bytes=%d", model, count, total)
				}
				if !reflect.DeepEqual(history, settled) {
					t.Fatal("request preparation mutated history")
				}
				if tc.name == "mixed_index_media_error" {
					last := prepared[len(prepared)-1]
					if last.Role != "user" || len(last.Images) != 1 || len(last.Files) != 1 || last.Files[0].Data != "YXVkaW8=" {
						t.Fatal("media observation lost or reordered")
					}
				}
			}
			if tc.name == "at_limit" && !reflect.DeepEqual(history, original) {
				t.Error("untrimmed result changed")
			}
			if tc.name == "asymmetric" && !reflect.DeepEqual(history[2], original[2]) {
				t.Error("smaller result changed")
			}
			// Resume must preserve the settled projection, including intentional
			// empty text, without discarding the producer's recovery payload.
			raw, err := json.Marshal(history)
			if err != nil {
				t.Fatal(err)
			}
			var resumed []providers.ChatMessage
			if err := json.Unmarshal(raw, &resumed); err != nil {
				t.Fatal(err)
			}
			enforceAggregateResultBudget(resumed)
			if !reflect.DeepEqual(resumed, settled) {
				t.Error("budget is not stable across serialization and replay")
			}
		})
	}
}

func TestAggregateBudgetLegacyZeroAllocation(t *testing.T) {
	history := budgetHistory(toolresult.FromText(strings.Repeat("a", 200001)), toolresult.FromText(strings.Repeat("b", 200000)))
	for i := 1; i < len(history); i++ {
		history[i].ToolResult = nil
	}
	enforceAggregateResultBudget(history)
	prepared, err := providers.PrepareMessagesForModelRequest("gpt-4o", history)
	if err != nil {
		t.Fatal(err)
	}
	if prepared[1].Content != "" || prepared[2].Content != strings.Repeat("b", 200000) {
		t.Fatal("legacy zero allocation or untouched peer was changed by projection")
	}
}
