package runtime

import (
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestModelMiddlewarePreservesCompletedAssistantCheckpoint(t *testing.T) {
	message := providers.ChatMessage{Role: "assistant", Content: "completed progress", ProviderItemID: "item-1", ProviderItemProvider: "responses", ProviderItemModel: "model-1"}
	event := providers.StreamEvent{Type: providers.EventAssistantItemDone, Message: &message}
	restored, err := modelStreamEventFromLogical(logicalModelStreamEvent(event))
	if err != nil || !reflect.DeepEqual(restored, event) {
		t.Fatalf("checkpoint changed through model middleware: %+v, %v", restored, err)
	}
	for _, invalid := range []*providers.ChatMessage{nil, {Role: "user", Content: "invalid checkpoint"}} {
		if _, err := modelStreamEventFromLogical(logicalModelStreamEvent(providers.StreamEvent{Type: providers.EventAssistantItemDone, Message: invalid})); err == nil {
			t.Fatal("invalid assistant checkpoint accepted")
		}
	}
}
