package modelwire

import (
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"testing"
)

func TestResumeSamplingSurvivesPublicModelBoundary(t *testing.T) {
	for _, resume := range []bool{false, true} {
		original := providers.ChatRequest{ResumeSampling: resume, Model: "fixture", Messages: []providers.ChatMessage{{Role: "assistant", Content: "completed progress"}}, Tools: []providers.ToolDefinition{}}
		encoded, err := json.Marshal(RequestToWire(original))
		if err != nil {
			t.Fatal(err)
		}
		var wire pluginprotocol.LogicalModelRequestV1
		if err = json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		restored, err := ApplyRequest(providers.ChatRequest{ResumeSampling: !resume}, wire)
		if err != nil {
			t.Fatal(err)
		}
		if restored.ResumeSampling != resume {
			t.Fatalf("resume_sampling=%v lost over %s", resume, encoded)
		}
		before, err := providers.InferenceRequestHash(original)
		if err != nil {
			t.Fatal(err)
		}
		after, err := providers.InferenceRequestHash(restored)
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("public model boundary changed physical request identity")
		}
		if len(restored.Messages) != 1 || restored.Messages[0].Role != "assistant" {
			t.Fatal("wire boundary invented a durable user instruction")
		}
	}
}
