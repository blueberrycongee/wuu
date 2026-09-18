package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type ProjectedToolResult struct {
	ToolText          string
	ObservationImages []InputImage
	ObservationFiles  []InputFile
}

const emptyToolResultText = "Tool completed without a textual result."

const (
	structuredResultIndexMaxKeys     = toolresult.StructuredContentIndexMaxKeys
	structuredResultIndexMaxKeyRunes = toolresult.StructuredContentIndexMaxKeyRunes
)

func ProjectToolResult(result toolresult.Result) ProjectedToolResult {
	projected := ProjectedToolResult{}
	textParts := make([]string, 0, len(result.Content)+1)
	for _, part := range result.Content {
		switch part.Type {
		case toolresult.ContentTypeText:
			textParts = append(textParts, part.Text)
		case toolresult.ContentTypeImage:
			if strings.TrimSpace(part.Data) != "" {
				projected.ObservationImages = append(projected.ObservationImages, InputImage{MediaType: part.MIMEType, Data: part.Data})
			} else {
				textParts = append(textParts, unsupportedContentNote(part))
			}
		case toolresult.ContentTypeAudio, toolresult.ContentTypeFile:
			if strings.TrimSpace(part.Data) != "" {
				projected.ObservationFiles = append(projected.ObservationFiles, InputFile{MediaType: part.MIMEType, Data: part.Data, Filename: part.Name})
			} else {
				textParts = append(textParts, unsupportedContentNote(part))
			}
		case toolresult.ContentTypeResource:
			label := strings.TrimSpace(part.Name)
			if label == "" {
				label = "resource"
			}
			textParts = append(textParts, fmt.Sprintf("[resource: %s] %s", label, canonicalJSON(part.Resource)))
		case toolresult.ContentTypeResourceLink:
			label := strings.TrimSpace(part.Name)
			if label == "" {
				label = "resource"
			}
			textParts = append(textParts, fmt.Sprintf("[resource_link: %s (%s)]", label, strings.TrimSpace(part.URI)))
		default:
			textParts = append(textParts, fmt.Sprintf("[unsupported tool result content: %s]", part.Type))
		}
	}
	// Rich content is the producer-owned model projection. Keep the complete
	// structured payload on the durable ToolResult instead of duplicating it in
	// full on the wire. A bounded semantic index preserves shape, identity, and
	// representative scalar values. Structured-only tools receive the complete
	// canonical JSON because they have no content projection of their own.
	if len(bytes.TrimSpace(result.StructuredContent)) > 0 {
		if len(result.Content) == 0 {
			textParts = append(textParts, canonicalJSON(result.StructuredContent))
		} else if index := structuredToolResultIndex(result.StructuredContent); index != "" {
			textParts = append(textParts, index)
		}
	}
	projected.ToolText = strings.Join(nonEmptyProjectionParts(textParts), "\n")
	if projected.ToolText == "" && (len(projected.ObservationImages) > 0 || len(projected.ObservationFiles) > 0) {
		projected.ToolText = "Tool result attachments are included in the following observation message."
	}
	if projected.ToolText == "" && result.IsError {
		projected.ToolText = "Tool execution failed without a textual error message."
	}
	if projected.ToolText == "" {
		// Tool-result output is required by model protocols even when a tool
		// intentionally has no user-facing text to return.
		projected.ToolText = emptyToolResultText
	}
	return projected
}

func structuredToolResultIndex(raw json.RawMessage) string {
	return toolresult.StructuredContentIndexJSON(raw)
}

func ApplyToolResultProjections(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, 0, len(messages))
	for index := 0; index < len(messages); {
		if messages[index].Role != "tool" {
			out = append(out, CloneChatMessage(messages[index]))
			index++
			continue
		}
		var images []InputImage
		var files []InputFile
		for index < len(messages) && messages[index].Role == "tool" {
			msg := CloneChatMessage(messages[index])
			if msg.ToolResult != nil {
				projected := ProjectToolResult(*msg.ToolResult)
				msg.Content = projected.ToolText
				images = append(images, projected.ObservationImages...)
				files = append(files, projected.ObservationFiles...)
			}
			if strings.TrimSpace(msg.Content) == "" {
				// Older persisted tool messages may not have rich result data.
				// Keep those histories valid for providers that require output.
				msg.Content = emptyToolResultText
			}
			out = append(out, msg)
			index++
		}
		if len(images) > 0 || len(files) > 0 {
			out = append(out, ChatMessage{
				Role:    "user",
				Content: "Tool result observations.",
				Hidden:  true,
				Images:  append([]InputImage(nil), images...),
				Files:   append([]InputFile(nil), files...),
			})
		}
	}
	return out
}

func unsupportedContentNote(part toolresult.ContentPart) string {
	if uri := strings.TrimSpace(part.URI); uri != "" {
		if part.Artifact != nil && part.Artifact.Ref != "" && strings.HasPrefix(uri, "wuu-artifact:") {
			return fmt.Sprintf("[%s artifact %q saved at %s; presented separately to the user, not attached as model input. Do not duplicate its preview with Markdown images.]", part.Type, part.Name, uri)
		}
		return fmt.Sprintf("[%s content is available at %s but cannot be attached directly]", part.Type, uri)
	}
	return fmt.Sprintf("[%s content could not be projected to this provider]", part.Type)
}

func canonicalJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(trimmed)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return string(trimmed)
	}
	return string(data)
}

func nonEmptyProjectionParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
