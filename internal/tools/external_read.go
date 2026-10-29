package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ExternalReadScope retains only host-validated facts for one admitted call.
// It never treats extension content as a native read observation.
type ExternalReadScope struct {
	env      *Env
	path     string
	resolved string
	offset   int
	limit    int
	native   bool
	image    bool
	previous map[string]ReadFileEntry
}

func (s *ExternalReadScope) UseNative() bool { return s.native }

// IsNativeReadRequest identifies host-owned continuation and session-artifact
// calls for admission; it does not authorize or execute the request.
func (t *Toolkit) IsNativeReadRequest(raw string) bool {
	var args struct {
		Path         string `json:"path"`
		Continuation string `json:"continuation"`
	}
	if decodeArgs(raw, &args) != nil {
		return false
	}
	if strings.TrimSpace(args.Continuation) != "" {
		return true
	}
	if strings.TrimSpace(args.Path) == "" {
		return false
	}
	_, _, managed, err := t.env.ResolveReadPath(args.Path)
	return err == nil && managed
}

// PrepareExternalRead checks the native path and sensitive-file boundaries
// before the trusted extension sees a fresh text request. Old host cursors and
// session-managed artifacts stay on the native path. Remote environments and
// non-text files are not supported by this contract.
func (t *Toolkit) PrepareExternalRead(ctx context.Context, raw string, allowImage bool) (*ExternalReadScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.env.ExecutionEnvironment != nil {
		return nil, errors.New("external read_file is unavailable in remote execution environments")
	}
	var args struct {
		Path           string          `json:"path"`
		Continuation   string          `json:"continuation"`
		Offset         int             `json:"offset"`
		Limit          *int            `json:"limit"`
		ExpectedSHA256 string          `json:"expected_sha256"`
		ByteRange      json.RawMessage `json:"byte_range"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Continuation) != "" {
		if _, err := decodeReadFileContinuation(args.Continuation); err != nil {
			return nil, err
		}
		return &ExternalReadScope{native: true}, nil
	}
	if args.ExpectedSHA256 != "" || (len(args.ByteRange) > 0 && string(args.ByteRange) != "null") {
		return nil, errors.New("external read_file does not accept native byte selectors without a continuation")
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("read_file requires path")
	}
	resolved, display, managed, err := t.env.ResolveReadPath(args.Path)
	if err != nil {
		return nil, err
	}
	if managed {
		return &ExternalReadScope{native: true}, nil
	}
	if err := rejectSensitiveReadPath(t.env, "read_file", resolved); err != nil {
		return nil, err
	}
	resolved, err = t.env.ExecPath(ctx, resolved)
	if err != nil {
		return nil, err
	}
	resolved, err = resolveReadTarget(ctx, t.env, "read_file", resolved, false)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("external read_file requires a regular file")
	}
	sample, err := io.ReadAll(io.LimitReader(f, 512))
	if err != nil {
		return nil, err
	}
	isImage := strings.HasPrefix(http.DetectContentType(sample), "image/")
	previous := make(map[string]ReadFileEntry)
	for path, entry := range t.env.ReadEntries() {
		if canonical, err := filepath.EvalSymlinks(path); err == nil && canonical == resolved {
			previous[path] = entry
		}
	}
	if isImage {
		if !allowImage {
			return nil, errors.New("external read_file supports text only; images and binary files are unavailable")
		}
		if info.Size() > maxReadImageBytes {
			return nil, fmt.Errorf("image too large (%d bytes, max %d)", info.Size(), maxReadImageBytes)
		}
		if args.Offset > 1 || args.Limit != nil {
			return nil, errors.New("image reads do not support text ranges")
		}
		return &ExternalReadScope{env: t.env, path: display, resolved: resolved, image: true, previous: previous}, nil
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return nil, errors.New("external read_file supports text only; images and binary files are unavailable")
	}
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := 0
	if args.Limit != nil {
		if *args.Limit < 0 {
			return nil, errors.New("read_file limit must be positive")
		}
		limit = *args.Limit
	}
	return &ExternalReadScope{env: t.env, path: display, resolved: resolved, offset: offset, limit: limit, previous: previous}, nil
}

// Render validates the extension's bounded text contract and builds the usual
// line view and raw PTC text. Neither content nor line facts are host-verified.
func (s *ExternalReadScope) Render(ctx context.Context, result toolresult.Result) (toolresult.Result, error) {
	if s == nil || s.native || s.env == nil {
		return toolresult.Result{}, errors.New("external read scope is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if s.image {
		if result.IsError || result.ModelText != nil || result.Activity != nil || len(result.StructuredContent) != 0 || len(result.Meta) != 0 || len(result.Content) != 1 {
			return toolresult.Result{}, errors.New("external image requires exactly one inline image")
		}
		part := result.Content[0]
		if part.Type != toolresult.ContentTypeImage || part.URI != "" || part.RemoteRef != "" || part.Data == "" || part.Text != "" || part.Artifact != nil || len(part.Resource) != 0 {
			return toolresult.Result{}, errors.New("external image requires exactly one inline image")
		}
		if len(part.Data) > base64.StdEncoding.EncodedLen(toolresult.MaxInlineDataBytes) {
			return toolresult.Result{}, errors.New("external image exceeds the inline limit")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(part.Data)
		if err != nil || len(data) > toolresult.MaxInlineDataBytes {
			return toolresult.Result{}, errors.New("invalid external image data")
		}
		rendered, err := renderReadFileImage(ctx, data, s.path, true, part.MIMEType)
		if err == nil {
			for path, entry := range s.previous {
				s.env.readState.deleteIfUnchanged(path, entry)
			}
		}
		return rendered, err
	}
	if result.ModelText != nil || len(result.Content) != 0 || len(result.StructuredContent) == 0 || result.Activity != nil {
		return toolresult.Result{}, errors.New("external read_file requires structured text only")
	}
	var data struct {
		Text       *string `json:"text"`
		TotalLines *int    `json:"total_lines"`
	}
	dec := json.NewDecoder(bytes.NewReader(result.StructuredContent))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&data); err != nil || data.Text == nil || data.TotalLines == nil || *data.TotalLines < 0 {
		return toolresult.Result{}, errors.New("invalid external read_file result")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return toolresult.Result{}, errors.New("invalid external read_file result")
	}
	text := *data.Text
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 || len(text) > defaultMaxFileBytes {
		return toolresult.Result{}, errors.New("external read_file result is not bounded UTF-8 text")
	}
	text = redactSensitiveReadContent(s.env, s.resolved, text)
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if text == "" {
		lines = nil
	}
	remaining := max(0, *data.TotalLines-s.offset+1)
	expected := remaining
	if s.limit > 0 {
		expected = min(expected, s.limit)
	}
	if len(lines) != expected {
		return toolresult.Result{}, errors.New("external read_file line range is inconsistent")
	}
	var formatted strings.Builder
	for i, line := range lines {
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
		}
		n := s.offset + i
		if i == 0 || n%10 == 0 {
			fmt.Fprint(&formatted, n)
		}
		fmt.Fprintf(&formatted, "|%s\n", line)
	}
	view := map[string]any{"action": "read", "path": s.path, "content": formatted.String(), "num_lines": len(lines), "start_line": s.offset, "total_lines": *data.TotalLines, "range": readFileRangeMetadata(s.offset, len(lines)), "omitted_ranges": readFileOmittedRanges(*data.TotalLines, s.offset, len(lines)), "truncated": s.offset <= *data.TotalLines && s.offset-1+len(lines) < *data.TotalLines, "source": "extension", "content_verified": false}
	structured := map[string]any{"text": text, "path": s.path, "start_line": s.offset, "num_lines": len(lines), "total_lines": *data.TotalLines, "source": "extension", "content_verified": false}
	view["next_suggestions"] = []string{"The extension's content is unverified; inspect the relevant range before relying on it."}
	rendered, err := toolResultWithData(view, structured)
	if err == nil {
		for path, entry := range s.previous {
			s.env.readState.deleteIfUnchanged(path, entry)
		}
	}
	return rendered, err
}
