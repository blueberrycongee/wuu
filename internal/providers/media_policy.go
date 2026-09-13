package providers

import (
	"fmt"
	"strings"
)

// MediaInputPolicy carries the resolved admission decision for user-supplied
// media on one request. A known unsupported kind is stripped at the request
// boundary and replaced with a short marker. Unknown kinds pass through so a
// missing catalog entry cannot silently discard media the user explicitly
// supplied; provider validation remains the authority in that case.
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
	if !rejectImage && !rejectFile && !(policy.VideoKnown && !policy.Video) {
		return msgs
	}
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
				} else if rejectFile {
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
