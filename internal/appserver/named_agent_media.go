package appserver

import (
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Room windows overlap between deliveries and discussion rounds. Only reuse
// media still present in the admitted history, not a permanent delivered flag:
// compaction may have removed it, in which case the source must be attached again.
func appendRoomMessageMedia(input *providers.ChatMessage, messages []channels.Message, history []providers.ChatMessage) {
	type imageKey struct{ mediaType, data string }
	existingImages := make(map[imageKey]bool)
	existingFiles := make(map[providers.InputFile]bool)
	for _, message := range history {
		for _, image := range message.Images {
			existingImages[imageKey{image.MediaType, image.Data}] = true
		}
		for _, file := range message.Files {
			existingFiles[file] = true
		}
		if message.ToolResult != nil {
			projection := providers.ProjectToolResult(*message.ToolResult)
			for _, image := range projection.ObservationImages {
				existingImages[imageKey{image.MediaType, image.Data}] = true
			}
			for _, file := range projection.ObservationFiles {
				existingFiles[file] = true
			}
		}
	}
	seen := make(map[string]bool)
	for _, message := range messages {
		if seen[message.ID] {
			continue
		}
		seen[message.ID] = true
		for _, image := range message.Images {
			if !existingImages[imageKey{image.MediaType, image.Data}] {
				input.Images = append(input.Images, providers.InputImage{MediaType: image.MediaType, Data: image.Data, Width: image.Width, Height: image.Height})
			}
		}
		for _, file := range message.Files {
			attachment := providers.InputFile{MediaType: file.MediaType, Data: file.Data, Filename: file.Filename}
			if !existingFiles[attachment] {
				input.Files = append(input.Files, attachment)
			}
		}
	}
}
