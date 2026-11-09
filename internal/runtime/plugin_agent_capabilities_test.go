package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPluginCompactionNotePlanDoesNotDuplicateInitialTranscript(t *testing.T) {
	var inputs []pluginhost.CompactionInput
	client := &generationClient{
		id: "note-compactor",
		capabilities: []pluginhost.CapabilityDescriptor{{
			ID: pluginhost.CapabilityAgentCompaction, Kind: "decision", Version: 2,
		}},
		invoke: func(params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			var input pluginhost.CompactionInput
			if err := json.Unmarshal(params.Input, &input); err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
			inputs = append(inputs, input)
			output, err := json.Marshal(pluginhost.CompactionOutput{NotePrompt: "write note", CheckpointIntervalTokens: 12_000})
			return pluginhost.CapabilityInvokeResult{Output: output}, err
		},
	}
	host := pluginhost.New(client)
	_, compactions, err := buildPluginAgentCapabilities(context.Background(), host, "openai", "model", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := compactions.Resolve(nil).(agent.ForkingCompactionProvider)
	if !ok {
		t.Fatal("forking compaction provider missing")
	}
	messages := []providers.ChatMessage{{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}}
	if _, err := provider.PlanCompactionNote(context.Background(), "model", messages, messages, agent.CompactionNote{}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.PlanCompactionNote(context.Background(), "model", messages, messages[1:], agent.CompactionNote{Markdown: "old note", CoveredMessages: 1}); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || len(inputs[0].Messages) != 2 || len(inputs[0].Delta) != 0 || len(inputs[1].Delta) != 1 {
		t.Fatalf("plan inputs = %+v", inputs)
	}
}

func TestPluginCompactionRejectsInvalidToolCallHistory(t *testing.T) {
	client := &generationClient{
		id: "invalid-compactor",
		capabilities: []pluginhost.CapabilityDescriptor{{
			ID: pluginhost.CapabilityAgentCompaction, Kind: "decision", Version: 1,
		}},
		invoke: func(pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			output, err := json.Marshal(pluginhost.CompactionOutput{Messages: []providers.ChatMessage{{
				Role: "tool", ToolCallID: "orphan", Content: "unpaired result",
			}}})
			return pluginhost.CapabilityInvokeResult{Output: output}, err
		},
	}
	host := pluginhost.New(client)
	_, compactions, err := buildPluginAgentCapabilities(context.Background(), host, "openai", "model", "/workspace")
	if err != nil {
		t.Fatal(err)
	}

	_, err = compactions.Resolve(nil).Compact(context.Background(), "model", []providers.ChatMessage{{Role: "user", Content: "history"}})
	if err == nil || !strings.Contains(err.Error(), "invalid tool-call history") {
		t.Fatalf("compaction error = %v", err)
	}
}
