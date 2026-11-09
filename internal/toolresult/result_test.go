package toolresult

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResultValidatesAndProjectsDeterministically(t *testing.T) {
	result := Result{
		Content: []ContentPart{
			{Type: "text", Text: "alpha"},
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png", Name: "shot"},
			{Type: "resource_link", URI: "https://example.test/docs", Name: "docs"},
		},
		StructuredContent: json.RawMessage(`{"b":2,"a":1}`),
		Meta:              json.RawMessage(`{"z":true,"a":"kept"}`),
		Activity:          &ActivityRef{ID: "activity-1", Kind: "browser", State: "running", ThreadID: "thread-1"},
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	wantText := "alpha\n[image: shot (image/png)]\n[resource_link: docs (https://example.test/docs)]"
	if got := result.TextProjection(); got != wantText {
		t.Fatalf("TextProjection = %q, want %q", got, wantText)
	}
	wantJSON := `{"content":[{"type":"text","text":"alpha"},{"type":"image","data":"aW1hZ2U=","mime_type":"image/png","name":"shot"},{"type":"resource_link","uri":"https://example.test/docs","name":"docs"}],"structured_content":{"a":1,"b":2},"meta":{"a":"kept","z":true},"activity":{"id":"activity-1","kind":"browser","state":"running","thread_id":"thread-1"}}`
	if got := result.JSONProjection(); got != wantJSON {
		t.Fatalf("JSONProjection = %s\nwant = %s", got, wantJSON)
	}
	if got := result.SizeBytes(); got != len(wantJSON) {
		t.Fatalf("SizeBytes = %d, want %d", got, len(wantJSON))
	}
}

func TestStructuredOnlyResultHasTextProjection(t *testing.T) {
	result := Result{StructuredContent: json.RawMessage(`{"b":2,"a":1}`)}
	if got, want := result.TextProjection(), `{"a":1,"b":2}`; got != want {
		t.Fatalf("TextProjection = %q, want %q", got, want)
	}
}

func TestResultCloneDoesNotShareMutableData(t *testing.T) {
	original := Result{
		Content:           []ContentPart{{Type: "resource", Resource: json.RawMessage(`{"uri":"file:///a"}`)}},
		StructuredContent: json.RawMessage(`{"items":[1]}`),
		Meta:              json.RawMessage(`{"secret":"metadata"}`),
		Activity:          &ActivityRef{ID: "activity-1", Kind: "cua"},
	}
	clone := original.Clone()
	clone.Content[0].Resource[2] = 'X'
	clone.StructuredContent[2] = 'Y'
	clone.Meta[2] = 'Z'
	clone.Activity.ID = "changed"

	if strings.Contains(string(original.Content[0].Resource), "X") ||
		strings.Contains(string(original.StructuredContent), "Y") ||
		strings.Contains(string(original.Meta), "Z") ||
		original.Activity.ID != "activity-1" {
		t.Fatalf("Clone shared mutable data: original=%+v clone=%+v", original, clone)
	}
}

func TestResultRejectsMalformedContentAndActivity(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{name: "unknown content", result: Result{Content: []ContentPart{{Type: "video"}}}, want: "content[0].type"},
		{name: "image without mime", result: Result{Content: []ContentPart{{Type: "image", Data: "aW1hZ2U="}}}, want: "mime_type"},
		{name: "invalid base64", result: Result{Content: []ContentPart{{Type: "image", Data: "not base64", MIMEType: "image/png"}}}, want: "base64"},
		{name: "malformed resource", result: Result{Content: []ContentPart{{Type: "resource", Resource: json.RawMessage(`{`)}}}, want: "resource"},
		{name: "missing activity id", result: Result{Activity: &ActivityRef{Kind: "browser"}}, want: "activity.id"},
		{name: "missing activity kind", result: Result{Activity: &ActivityRef{ID: "activity-1"}}, want: "activity.kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.result.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTextResultPreservesLegacyStringExactly(t *testing.T) {
	const legacy = "  legacy result\nwith spacing  "
	result := FromText(legacy)
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := result.TextProjection(); got != legacy {
		t.Fatalf("TextProjection = %q, want exact legacy string %q", got, legacy)
	}
	if got := result.HookProjection(); got != legacy {
		t.Fatalf("HookProjection = %q, want exact legacy string %q", got, legacy)
	}
}
