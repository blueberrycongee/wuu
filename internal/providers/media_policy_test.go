package providers

import (
	"strings"
	"testing"
)

func testImage() InputImage {
	return InputImage{MediaType: "image/png", Data: "aW1hZ2U=", Width: 2, Height: 2}
}

func testFile() InputFile {
	return InputFile{MediaType: "application/pdf", Data: "cGRm", Filename: "spec.pdf"}
}

func TestProjectMediaForPolicyStripsMediaWithMarker(t *testing.T) {
	t.Parallel()
	msgs := []ChatMessage{
		{Role: "user", Content: "look at this", Images: []InputImage{testImage(), testImage()}, Files: []InputFile{testFile()}},
		{Role: "user", Content: "", Images: []InputImage{testImage()}},
		{Role: "assistant", Content: "no media here"},
	}
	out := ProjectMediaForPolicy(msgs, MediaInputPolicy{ImageKnown: true, FileKnown: true})

	if len(out[0].Images) != 0 || len(out[0].Files) != 0 {
		t.Fatalf("media not stripped: %+v", out[0])
	}
	if !strings.Contains(out[0].Content, "look at this") {
		t.Fatalf("original text lost: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "[2 images omitted: unsupported]") {
		t.Fatalf("missing plural image marker: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "[1 file omitted: unsupported]") {
		t.Fatalf("missing singular file marker: %q", out[0].Content)
	}
	if out[1].Content != "[1 image omitted: unsupported]" {
		t.Fatalf("empty content should become marker only, got %q", out[1].Content)
	}
	if out[2].Content != "no media here" {
		t.Fatalf("media-free message changed: %q", out[2].Content)
	}
	// Input must stay untouched: stored history keeps media for other readers.
	if len(msgs[0].Images) != 2 || len(msgs[0].Files) != 1 {
		t.Fatal("input messages mutated")
	}
}

func TestProjectMediaForPolicyAdmittedKindsPassThrough(t *testing.T) {
	t.Parallel()
	msgs := []ChatMessage{
		{Role: "user", Content: "hi", Images: []InputImage{testImage()}, Files: []InputFile{testFile()}},
	}
	for name, policy := range map[string]MediaInputPolicy{
		"both admitted": {Image: true, File: true},
		"image only":    {Image: true, FileKnown: true},
		"file only":     {File: true, ImageKnown: true},
	} {
		out := ProjectMediaForPolicy(msgs, policy)
		wantImages, wantFiles := 0, 0
		if policy.Image {
			wantImages = 1
		}
		if policy.File {
			wantFiles = 1
		}
		if len(out[0].Images) != wantImages || len(out[0].Files) != wantFiles {
			t.Fatalf("%s: got %+v, want %d images %d files", name, out[0], wantImages, wantFiles)
		}
	}
}

func TestProjectMediaForPolicyUnknownKindsPassThrough(t *testing.T) {
	t.Parallel()
	msgs := []ChatMessage{{
		Role: "user", Content: "inspect this",
		Images: []InputImage{testImage()}, Files: []InputFile{testFile()},
	}}

	out := ProjectMediaForPolicy(msgs, MediaInputPolicy{})
	if len(out[0].Images) != 1 || len(out[0].Files) != 1 {
		t.Fatalf("unknown media capability must not discard explicit input: %+v", out[0])
	}
	if out[0].Content != "inspect this" {
		t.Fatalf("unknown media capability added an omission marker: %q", out[0].Content)
	}
}

func TestRequiredMediaFailsInsteadOfOmittingEvidence(t *testing.T) {
	image, file := testImage(), testFile()
	image.Required, file.Required = true, true
	for name, input := range map[string]ChatMessage{
		"image": {Role: "user", Images: []InputImage{image}},
		"pdf":   {Role: "user", Files: []InputFile{file}},
		"video": {Role: "user", Files: []InputFile{{Required: true, MediaType: "video/mp4", Data: "dmlkZW8="}}},
	} {
		t.Run(name, func(t *testing.T) {
			// The request boundary also protects restored history, not just the
			// latest input, when a later turn chooses an incompatible model.
			msgs := []ChatMessage{input, {Role: "assistant", Content: "Seen"}, {Role: "user", Content: "Continue"}}
			if out, err := PrepareMessagesForProviderRequestWithPolicy("p", "m", msgs, MediaInputPolicy{ImageKnown: true, FileKnown: true, VideoKnown: true}); err == nil || out != nil {
				t.Fatal("required evidence degraded to a text-only request")
			}
			for _, policy := range []MediaInputPolicy{{}, {ImageKnown: true, Image: true, FileKnown: true, File: true, VideoKnown: true, Video: true}} {
				out, err := PrepareMessagesForProviderRequestWithPolicy("p", "m", msgs, policy)
				if err != nil || len(out[0].Images) != len(input.Images) || len(out[0].Files) != len(input.Files) {
					t.Fatalf("supported/unknown input lost media: %v", err)
				}
			}
		})
	}
}

func TestPrepareMessagesForProviderRequestWithPolicyStripsBeforeValidation(t *testing.T) {
	t.Parallel()
	msgs := []ChatMessage{
		{Role: "user", Content: "see attached", Images: []InputImage{testImage()}},
		{Role: "assistant", Content: "ok"},
	}
	prepared, err := PrepareMessagesForProviderRequestWithPolicy("p", "m", msgs, MediaInputPolicy{ImageKnown: true})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if len(prepared[0].Images) != 0 {
		t.Fatal("images survived the request boundary")
	}
	if !strings.Contains(prepared[0].Content, "[1 image omitted: unsupported]") {
		t.Fatalf("marker missing: %q", prepared[0].Content)
	}
}
