package tools

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type readFileContinuation struct {
	Path           string `json:"path"`
	Offset         int    `json:"offset,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ByteOffset     *int   `json:"byte_offset,omitempty"`
	ByteEndOffset  *int   `json:"byte_end_offset,omitempty"`
}

func encodeReadFileContinuation(path string, offset, limit int, expectedSHA256 string) string {
	payload, _ := json.Marshal(readFileContinuation{Path: path, Offset: offset, Limit: limit, ExpectedSHA256: expectedSHA256})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func encodeReadFileByteContinuation(path string, offset, limit, endOffset int, expectedSHA256 string) string {
	payload, _ := json.Marshal(readFileContinuation{Path: path, Limit: limit, ExpectedSHA256: expectedSHA256, ByteOffset: &offset, ByteEndOffset: &endOffset})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeReadFileContinuation(token string) (readFileContinuation, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return readFileContinuation{}, fmt.Errorf("invalid read_file continuation: %w", err)
	}
	var continuation readFileContinuation
	if err := json.Unmarshal(payload, &continuation); err != nil || strings.TrimSpace(continuation.Path) == "" || (continuation.ByteOffset == nil && (continuation.Offset <= 0 || continuation.Limit <= 0)) || (continuation.ByteOffset != nil && continuation.Limit <= 0) {
		return readFileContinuation{}, errors.New("invalid read_file continuation token")
	}
	return continuation, nil
}

// validateStableOffset binds offset-based continuation to the exact sorted
// result set that produced the preceding page. Without that binding, insertions
// or removals before the cursor can silently repeat or skip records.
func validateStableOffset(tool string, offset int, expectedRevision, currentRevision string) error {
	if offset < 0 {
		return fmt.Errorf("%s offset must be non-negative", tool)
	}
	expectedRevision = strings.TrimSpace(expectedRevision)
	if offset > 0 && expectedRevision == "" {
		return fmt.Errorf("%s expected_revision is required when offset is non-zero", tool)
	}
	if expectedRevision != "" && expectedRevision != currentRevision {
		return fmt.Errorf("%s continuation is stale: result revision changed from %q to %q; restart at offset 0", tool, expectedRevision, currentRevision)
	}
	return nil
}

func continuationResultRevision(kind string, records any) string {
	encoded, _ := json.Marshal(records)
	hash := sha256.New()
	_, _ = hash.Write([]byte(kind))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return "result:" + hex.EncodeToString(hash.Sum(nil))
}

func continuationSnapshotRevision(workspaceRevision, kind string, records any) string {
	resultRevision := continuationResultRevision(kind, records)
	if strings.TrimSpace(workspaceRevision) == "" {
		return resultRevision
	}
	return workspaceRevision + ";" + resultRevision
}

func continuationPage(offset, returned int, hasMore bool, revision string) map[string]any {
	page := map[string]any{
		"offset":         offset,
		"returned":       returned,
		"has_more":       hasMore,
		"snapshot_token": revision,
	}
	if hasMore && returned > 0 {
		page["next"] = map[string]any{
			"offset":            offset + returned,
			"expected_revision": revision,
		}
	}
	return page
}

func pageWindow[T any](records []T, offset, pageSize int) (page []T, hasMore bool) {
	if offset >= len(records) {
		return []T{}, false
	}
	end := offset + pageSize
	if end < offset || end > len(records) {
		end = len(records)
	}
	return records[offset:end], end < len(records)
}
