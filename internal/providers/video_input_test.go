package providers

import "testing"

func TestVideoAdmissionUsesModelAndTransport(t *testing.T) {
	for _, tt := range []struct {
		name    string
		policy  MediaInputPolicy
		wire    bool
		options map[string]any
		want    bool
	}{
		{"catalog and supported wire", MediaInputPolicy{Video: true, VideoKnown: true}, true, nil, true},
		{"responses cannot infer video from model", MediaInputPolicy{Video: true, VideoKnown: true}, false, nil, false},
		{"unknown model is not evidence", MediaInputPolicy{}, true, nil, false},
		{"custom explicit contract", MediaInputPolicy{}, true, map[string]any{"video_input": "video_url"}, true},
		{"known text model rejects override", MediaInputPolicy{VideoKnown: true}, true, map[string]any{"video_input": "video_url"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{Model: "user-defined-name", MediaInput: tt.policy, ProviderOptions: tt.options, Messages: []ChatMessage{{Role: "user", Files: []InputFile{{MediaType: "video/mp4", Data: "AAAA"}}}}}
			out, err := PrepareVideoInput(req, tt.wire)
			if (err == nil) != tt.want {
				t.Fatalf("admission error=%v, want supported=%v", err, tt.want)
			}
			if err == nil {
				if _, ok := out.ProviderOptions["video_input"]; ok {
					t.Fatal("routing option leaked to provider")
				}
				if !out.MediaInput.Video {
					t.Fatal("video not admitted")
				}
			}
		})
	}
}

func TestVideoHistorySurvivesModelSwitchWithoutLosingPDFPolicy(t *testing.T) {
	messages := []ChatMessage{{Role: "user", Files: []InputFile{{MediaType: "video/mp4", Data: "AAAA"}, {MediaType: "application/pdf", Data: "BBBB"}}}, {Role: "assistant", Content: "Seen"}, {Role: "user", Content: "Continue"}, {Role: "user", Hidden: true, Content: "Runtime context"}}
	req, err := PrepareVideoInput(ChatRequest{Messages: messages, MediaInput: MediaInputPolicy{File: true, FileKnown: true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	out := ProjectMediaForPolicy(req.Messages, req.MediaInput)
	if len(out[0].Files) != 1 || out[0].Files[0].MediaType != "application/pdf" {
		t.Fatal("PDF and video policies conflated")
	}
	if len(messages[0].Files) != 2 {
		t.Fatal("stored history mutated")
	}
	if out[0].Content != "[1 video omitted: unsupported]" {
		t.Fatalf("missing omission marker: %q", out[0].Content)
	}
}

func TestVideoAdmissionWithTrailingRuntimeContext(t *testing.T) {
	for _, hiddenVideo := range []bool{false, true} {
		messages := []ChatMessage{
			{Role: "user", Hidden: hiddenVideo, Files: []InputFile{{MediaType: "video/mp4", Data: "AAAA"}}},
			{Role: "user", Hidden: true, Content: "Runtime context"},
			{Role: "system", Content: "Additional instructions"},
			{Role: "user", Hidden: true, Content: "Plugin context"},
		}
		req := ChatRequest{Messages: messages, MediaInput: MediaInputPolicy{Video: true, VideoKnown: true}}
		if _, err := PrepareVideoInput(req, false); err == nil {
			t.Fatalf("runtime context bypassed video admission (hidden video=%v)", hiddenVideo)
		}
		prepared, err := PrepareVideoInput(req, true)
		if err != nil {
			t.Fatal(err)
		}
		out := ProjectMediaForPolicy(prepared.Messages, prepared.MediaInput)
		if len(out[0].Files) != 1 || out[0].Files[0].Data != "AAAA" {
			t.Fatal("supported video was lost behind runtime context")
		}
		if len(messages[0].Files) != 1 {
			t.Fatal("admission mutated stored media")
		}
	}
}

func TestOpenRouterMOVDoesNotChangeStoredMediaType(t *testing.T) {
	original := []ChatMessage{{Role: "user", Files: []InputFile{{MediaType: "video/quicktime", Data: "AAAA"}}}}
	out := VideoMessagesForEndpoint(original, "https://openrouter.ai/api/v1")
	if out[0].Files[0].MediaType != "video/mov" || original[0].Files[0].MediaType != "video/quicktime" {
		t.Fatal("MOV mapping did not preserve source")
	}
}
