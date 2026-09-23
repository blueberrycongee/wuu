package openai

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func (item responsesOutputItem) generatedImage() (providers.InputImage, error) {
	data := strings.TrimSpace(item.Result)
	if data == "" {
		return providers.InputImage{}, fmt.Errorf("Responses image generation returned no image (status %q)", item.Status)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return providers.InputImage{}, fmt.Errorf("invalid Responses image data: %w", err)
	}
	var mediaType string
	switch item.OutputFormat {
	case "", "png":
		mediaType = "image/png"
	case "jpeg":
		mediaType = "image/jpeg"
	case "webp":
		mediaType = "image/webp"
	default:
		return providers.InputImage{}, fmt.Errorf("unsupported Responses image format %q", item.OutputFormat)
	}
	return providers.InputImage{ProviderItemID: item.ID, MediaType: mediaType, Data: data}, nil
}

// Both item.done and response.completed can carry the same final image. Publish
// it once; partial-image previews are not durable assistant attachments.
type responsesImageStream struct {
	seen map[string]bool
}

func (s *responsesImageStream) consume(event responsesStreamEvent, emit *providers.StreamEmitter) error {
	switch event.Type {
	case "response.output_item.done":
		return s.emit(event.Item, event.outputIndex(), emit)
	case "response.completed", "response.done", "response.incomplete":
		if event.Response != nil {
			for index, item := range event.Response.Output {
				if err := s.emit(item, index, emit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *responsesImageStream) emit(item responsesOutputItem, index int, emit *providers.StreamEmitter) error {
	if item.Type != "image_generation_call" {
		return nil
	}
	key := item.ID
	if key == "" {
		key = fmt.Sprintf("index:%d", index)
	}
	if s.seen[key] {
		return nil
	}
	image, err := item.generatedImage()
	if err != nil {
		return err
	}
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	s.seen[key] = true
	emit.Send(providers.StreamEvent{Type: providers.EventImage, Image: &image})
	return nil
}
