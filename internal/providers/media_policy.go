package providers

import (
	"fmt"
	"strings"
)

// MediaInputPolicy carries the resolved admission decision for user-supplied
// media on one request.
//
// Design: the front end may attach anything; the model backend's declared
// capabilities decide what reaches the wire. Unsupported media is stripped
// from the request copy instead of failing the turn, and the model keeps text
// plus a path it can open with tools. At submission, appserver writes every
// attachment to an expiring working copy and appends its path to the message
// text (inputImagePathReference, inputFilePathReference). At request time,
// ProjectMediaForPolicy removes the bytes the policy rejects and adds a short
// omission marker. A model that cannot take a PDF, image, or video natively
// therefore still sees the prompt, the path, and the marker, and can read the
// file by running tools or writing code. Stored history keeps the original
// media for the UI and for later models that can consume it natively.
//
// Admission per kind:
//   - Images follow the model's image capability.
//   - Videos need both the model capability and a connection that carries
//     video (ResolveVideoInputPolicy).
//   - Files follow the model's file capability, and PDF is the only file type
//     with a native transport; other attached files always reach the model as
//     a path only.
//   - Pasted long text is always materialized as a text file and referenced
//     by path, independent of this policy.
//   - Media produced by rich tool results, such as history reads, and the
//     token budgets used by compaction go through the same projection, so
//     switching to a less capable model cannot reintroduce stripped media.
//
// Deliberate exceptions:
//   - Unknown capabilities pass through so a missing catalog entry cannot
//     silently discard media the user explicitly supplied; provider
//     validation remains the authority in that case.
//   - Required media fails the request (ValidateRequiredMedia), because a
//     path cannot satisfy a task that depends on the model seeing it.
//   - Media without a working copy has no path fallback: stripped images keep
//     only the marker, and a new unsupported video fails (PrepareVideoInput).
//
// The policy is request metadata only: provider clients must never serialize
// it on the wire.
type MediaInputPolicy struct {
	Video      bool
	VideoKnown bool
	Image      bool
	File       bool
	ImageKnown bool
	FileKnown  bool
}

// MediaInputResolver exposes transport admission without credentials or I/O so
// the agent can budget the same media projection that the adapter sends.
type MediaInputResolver interface {
	ResolveMediaInput(MediaInputPolicy, map[string]any) MediaInputPolicy
}

func ResolveMediaInput(client Client, policy MediaInputPolicy, options map[string]any) MediaInputPolicy {
	if resolver, ok := client.(MediaInputResolver); ok {
		return resolver.ResolveMediaInput(policy, options)
	}
	return ResolveVideoInputPolicy(policy, options, false)
}

func ResolveVideoInputPolicy(policy MediaInputPolicy, options map[string]any, transport bool) MediaInputPolicy {
	policy.Video = transport && ((policy.VideoKnown && policy.Video) || (!policy.VideoKnown && options["video_input"] == "video_url"))
	policy.VideoKnown = true
	return policy
}

// ValidateRequiredMedia prevents a task from continuing after selected evidence
// is dropped. Unknown catalog capabilities retain the existing provider-validated
// pass-through behavior; an explicit unsupported capability fails closed.
func ValidateRequiredMedia(msgs []ChatMessage, policy MediaInputPolicy) error {
	for _, msg := range msgs {
		for _, image := range msg.Images {
			if image.Required && policy.ImageKnown && !policy.Image {
				return fmt.Errorf("required image input is not supported by the selected model; choose an image-capable model")
			}
		}
		for _, file := range msg.Files {
			if !file.Required {
				continue
			}
			if IsVideoMediaType(file.MediaType) {
				if policy.VideoKnown && !policy.Video {
					return fmt.Errorf("required video input is not supported by the selected model and connection")
				}
			} else if (file.LocalPath != "" && file.MediaType != "application/pdf") || (policy.FileKnown && !policy.File) {
				return fmt.Errorf("required file input is not supported by the selected model; choose a file-capable model")
			}
		}
	}
	return nil
}

// MediaOmissionMarker renders the fixed short marker that replaces stripped
// media in the model context. It intentionally carries no OCR, description,
// base64, or dimensions, matching the chat_read marker agents already see.
func MediaOmissionMarker(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("[%d %s omitted: unsupported]", count, label)
}

func appendMediaMarker(content string, count int, singular, plural string) string {
	marker := MediaOmissionMarker(count, singular, plural)
	if strings.TrimSpace(content) == "" {
		return marker
	}
	return content + "\n" + marker
}

// ProjectMediaForPolicy returns a request-scoped copy of msgs with media the
// policy rejects replaced by the fixed omission marker. Admitted media
// passes through untouched. The input slice and messages are never mutated;
// stored history keeps the original media for UI and for other agents/models
// that can read it.
func ProjectMediaForPolicy(msgs []ChatMessage, policy MediaInputPolicy) []ChatMessage {
	rejectImage := policy.ImageKnown && !policy.Image
	rejectFile := policy.FileKnown && !policy.File
	out := make([]ChatMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		if rejectImage && len(out[i].Images) > 0 {
			omitted := len(out[i].Images)
			out[i].Images = nil
			out[i].Content = appendMediaMarker(out[i].Content, omitted, "image", "images")
		}
		if len(out[i].Files) > 0 {
			kept := make([]InputFile, 0, len(out[i].Files))
			omittedFiles, omittedVideos := 0, 0
			for _, file := range out[i].Files {
				if IsVideoMediaType(file.MediaType) {
					if policy.VideoKnown && !policy.Video {
						omittedVideos++
						continue
					}
				} else if rejectFile || (file.LocalPath != "" && file.MediaType != "application/pdf") {
					omittedFiles++
					continue
				}
				kept = append(kept, file)
			}
			out[i].Files = kept
			if omittedFiles > 0 {
				out[i].Content = appendMediaMarker(out[i].Content, omittedFiles, "file", "files")
			}
			if omittedVideos > 0 {
				out[i].Content = appendMediaMarker(out[i].Content, omittedVideos, "video", "videos")
			}
		}
	}
	return out
}
