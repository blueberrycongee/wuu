package toolresult

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	ContentTypeText         = "text"
	ContentTypeImage        = "image"
	ContentTypeAudio        = "audio"
	ContentTypeFile         = "file"
	ContentTypeResource     = "resource"
	ContentTypeResourceLink = "resource_link"

	ArtifactPlacementInline  = "inline"
	ArtifactPlacementTurnEnd = "turn_end"

	MaxContentParts       = 128
	MaxInlineDataBytes    = 2 * 1024 * 1024
	MaxStructuredJSONSize = 1 * 1024 * 1024
	MaxResultBytes        = 3 * 1024 * 1024
)

type ArtifactPresentation struct {
	// Placement is a presentation hint only. Empty lets the host choose from
	// the media type (images inline, document-like outputs at the turn end).
	Placement string `json:"placement,omitempty"`
	// Ref is an opaque host identity, independent from the renderer URI. The
	// integrity fields remain content-level metadata so ordered ToolResult parts
	// stay the single artifact protocol.
	Ref       string `json:"ref,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

type ContentPart struct {
	// RemoteRef is an output-only delivery reference for deferred media.
	RemoteRef string                `json:"remote_ref,omitempty"`
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Data      string                `json:"data,omitempty"`
	MIMEType  string                `json:"mime_type,omitempty"`
	URI       string                `json:"uri,omitempty"`
	Name      string                `json:"name,omitempty"`
	Resource  json.RawMessage       `json:"resource,omitempty"`
	Artifact  *ArtifactPresentation `json:"artifact,omitempty"`
}

type ActivityRef struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	State      string `json:"state,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	PreviewURI string `json:"preview_uri,omitempty"`
}

type Result struct {
	Content           []ContentPart   `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	Meta              json.RawMessage `json:"meta,omitempty"`
	IsError           bool            `json:"is_error,omitempty"`
	Activity          *ActivityRef    `json:"activity,omitempty"`
}

func FromText(text string) Result {
	if text == "" {
		return Result{}
	}
	return Result{Content: []ContentPart{{Type: ContentTypeText, Text: text}}}
}

func FromErrorText(text string) Result {
	result := FromText(text)
	result.IsError = true
	return result
}

func (r Result) Validate() error {
	if len(r.Content) > MaxContentParts {
		return fmt.Errorf("content has %d parts, limit is %d", len(r.Content), MaxContentParts)
	}
	for index, part := range r.Content {
		if err := validateContentPart(index, part); err != nil {
			return err
		}
	}
	if err := validateRawJSON("structured_content", r.StructuredContent); err != nil {
		return err
	}
	if err := validateRawJSON("meta", r.Meta); err != nil {
		return err
	}
	if len(r.StructuredContent) > MaxStructuredJSONSize {
		return fmt.Errorf("structured_content exceeds %d bytes", MaxStructuredJSONSize)
	}
	if len(r.Meta) > MaxStructuredJSONSize {
		return fmt.Errorf("meta exceeds %d bytes", MaxStructuredJSONSize)
	}
	if r.Activity != nil {
		if strings.TrimSpace(r.Activity.ID) == "" {
			return errors.New("activity.id is required")
		}
		if strings.TrimSpace(r.Activity.Kind) == "" {
			return errors.New("activity.kind is required")
		}
	}
	if size := r.SizeBytes(); size > MaxResultBytes {
		return fmt.Errorf("tool result exceeds %d bytes", MaxResultBytes)
	}
	return nil
}

func validateContentPart(index int, part ContentPart) error {
	kind := strings.TrimSpace(part.Type)
	prefix := fmt.Sprintf("content[%d]", index)
	switch kind {
	case ContentTypeText:
		// Empty text is valid in MCP and remains an ordered content part.
	case ContentTypeImage, ContentTypeAudio, ContentTypeFile:
		if strings.TrimSpace(part.Data) == "" && strings.TrimSpace(part.URI) == "" {
			return fmt.Errorf("%s requires data or uri", prefix)
		}
		if strings.TrimSpace(part.MIMEType) == "" {
			return fmt.Errorf("%s.mime_type is required", prefix)
		}
		if part.Data != "" {
			if len(part.Data) > base64.StdEncoding.EncodedLen(MaxInlineDataBytes)+4 {
				return fmt.Errorf("%s.data exceeds %d decoded bytes", prefix, MaxInlineDataBytes)
			}
			if _, err := base64.StdEncoding.DecodeString(part.Data); err != nil {
				return fmt.Errorf("%s.data must be base64: %w", prefix, err)
			}
		}
	case ContentTypeResource:
		if len(bytes.TrimSpace(part.Resource)) == 0 || !json.Valid(part.Resource) {
			return fmt.Errorf("%s.resource must be valid JSON", prefix)
		}
	case ContentTypeResourceLink:
		if strings.TrimSpace(part.URI) == "" {
			return fmt.Errorf("%s.uri is required", prefix)
		}
	default:
		return fmt.Errorf("%s.type %q is unsupported", prefix, part.Type)
	}
	if part.Artifact != nil {
		switch strings.TrimSpace(part.Artifact.Placement) {
		case "", ArtifactPlacementInline, ArtifactPlacementTurnEnd:
		default:
			return fmt.Errorf("%s.artifact.placement %q is unsupported", prefix, part.Artifact.Placement)
		}
		if len(part.Artifact.Ref) > 512 {
			return fmt.Errorf("%s.artifact.ref exceeds 512 bytes", prefix)
		}
		if part.Artifact.SizeBytes != nil && *part.Artifact.SizeBytes < 0 {
			return fmt.Errorf("%s.artifact.size_bytes must not be negative", prefix)
		}
		if checksum := strings.TrimSpace(part.Artifact.SHA256); checksum != "" {
			decoded, err := hex.DecodeString(checksum)
			if err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("%s.artifact.sha256 must be a 64-character hexadecimal digest", prefix)
			}
		}
	}
	if len(part.Text) > MaxStructuredJSONSize {
		return fmt.Errorf("%s.text exceeds %d bytes", prefix, MaxStructuredJSONSize)
	}
	if len(part.Resource) > MaxStructuredJSONSize {
		return fmt.Errorf("%s.resource exceeds %d bytes", prefix, MaxStructuredJSONSize)
	}
	if len(part.URI) > 8*1024 || len(part.Name) > 1024 || len(part.MIMEType) > 256 {
		return fmt.Errorf("%s metadata exceeds its size limit", prefix)
	}
	return nil
}

func validateRawJSON(field string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if !json.Valid(trimmed) {
		return fmt.Errorf("%s must be valid JSON", field)
	}
	return nil
}

func (r Result) Clone() Result {
	out := Result{
		StructuredContent: cloneRaw(r.StructuredContent),
		Meta:              cloneRaw(r.Meta),
		IsError:           r.IsError,
	}
	if len(r.Content) > 0 {
		out.Content = make([]ContentPart, len(r.Content))
		copy(out.Content, r.Content)
		for index := range out.Content {
			out.Content[index].Resource = cloneRaw(r.Content[index].Resource)
			if r.Content[index].Artifact != nil {
				artifact := *r.Content[index].Artifact
				if artifact.SizeBytes != nil {
					size := *artifact.SizeBytes
					artifact.SizeBytes = &size
				}
				out.Content[index].Artifact = &artifact
			}
		}
	}
	if r.Activity != nil {
		activity := *r.Activity
		out.Activity = &activity
	}
	return out
}

func (r Result) TextProjection() string {
	if len(r.Content) == 1 && r.Content[0].Type == ContentTypeText && len(bytes.TrimSpace(r.StructuredContent)) == 0 {
		return r.Content[0].Text
	}
	parts := make([]string, 0, len(r.Content)+1)
	for _, part := range r.Content {
		switch part.Type {
		case ContentTypeText:
			parts = append(parts, part.Text)
		case ContentTypeImage, ContentTypeAudio, ContentTypeFile:
			parts = append(parts, binaryPartProjection(part))
		case ContentTypeResource:
			label := firstNonEmpty(strings.TrimSpace(part.Name), "resource")
			parts = append(parts, fmt.Sprintf("[resource: %s] %s", label, canonicalRawString(part.Resource)))
		case ContentTypeResourceLink:
			label := firstNonEmpty(strings.TrimSpace(part.Name), "resource")
			parts = append(parts, fmt.Sprintf("[resource_link: %s (%s)]", label, strings.TrimSpace(part.URI)))
		}
	}
	// Content is the producer-owned model projection. StructuredContent is
	// retained for clients, hooks, and replay, but must not be appended when a
	// content projection already exists or the same payload is counted twice.
	// Structured-only results still need a textual model representation.
	if len(r.Content) == 0 && len(bytes.TrimSpace(r.StructuredContent)) > 0 {
		parts = append(parts, canonicalRawString(r.StructuredContent))
	}
	return strings.Join(parts, "\n")
}

func binaryPartProjection(part ContentPart) string {
	label := firstNonEmpty(strings.TrimSpace(part.Name), part.Type)
	detail := strings.TrimSpace(part.MIMEType)
	if uri := strings.TrimSpace(part.URI); uri != "" {
		detail += "; " + uri
	}
	return fmt.Sprintf("[%s: %s (%s)]", part.Type, label, detail)
}

func (r Result) HookProjection() string {
	if len(r.Content) == 1 && r.Content[0].Type == ContentTypeText &&
		len(bytes.TrimSpace(r.StructuredContent)) == 0 && len(bytes.TrimSpace(r.Meta)) == 0 && r.Activity == nil {
		return r.Content[0].Text
	}
	return r.JSONProjection()
}

func (r Result) JSONProjection() string {
	canonical := r.Clone()
	canonical.StructuredContent = canonicalRaw(canonical.StructuredContent)
	canonical.Meta = canonicalRaw(canonical.Meta)
	for index := range canonical.Content {
		canonical.Content[index].Resource = canonicalRaw(canonical.Content[index].Resource)
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	return string(data)
}

func (r Result) SizeBytes() int {
	return len(r.JSONProjection())
}

func (r Result) IsTextOnly() bool {
	if len(r.Content) != 1 || r.Content[0].Type != ContentTypeText {
		return false
	}
	return len(bytes.TrimSpace(r.StructuredContent)) == 0 && len(bytes.TrimSpace(r.Meta)) == 0 && r.Activity == nil
}

func canonicalRaw(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return cloneRaw(raw)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return cloneRaw(raw)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return cloneRaw(raw)
	}
	return data
}

func canonicalRawString(raw json.RawMessage) string {
	return string(canonicalRaw(raw))
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
