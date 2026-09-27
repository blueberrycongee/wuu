package compact

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"
)

// compactMediaEvidence describes omitted binary content without copying the
// payload into the summary prompt. The hash is over decoded bytes, so the
// durable transcript can be matched to this checkpoint index exactly.
func compactMediaEvidence(data string, inspectImage bool) string {
	trimmed := strings.TrimSpace(data)
	evidence := fmt.Sprintf("%d base64 characters", len(trimmed))
	if trimmed == "" {
		return evidence
	}
	hasher := sha256.New()
	counter := &compactByteCounter{}
	decoded := base64.NewDecoder(base64.StdEncoding, strings.NewReader(trimmed))
	stream := io.TeeReader(decoded, io.MultiWriter(hasher, counter))
	var width, height int
	if inspectImage {
		if config, _, err := image.DecodeConfig(stream); err == nil {
			width, height = config.Width, config.Height
		}
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		return evidence
	}
	evidence += fmt.Sprintf(", %d decoded bytes, sha256=%x", counter.total, hasher.Sum(nil))
	if width > 0 && height > 0 {
		evidence += fmt.Sprintf(", dimensions=%dx%d", width, height)
	}
	return evidence
}

// compactImagePathEvidence keeps an accepted image's local file readable after
// its bytes are removed from compacted history. The path is already absolute
// and conversation-scoped; empty paths stay omitted so older transcripts and
// provider-generated images do not invent a location.
func compactImagePathEvidence(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return ", path=" + strconv.Quote(path)
}

type compactByteCounter struct {
	total int64
}

func (counter *compactByteCounter) Write(data []byte) (int, error) {
	counter.total += int64(len(data))
	return len(data), nil
}
