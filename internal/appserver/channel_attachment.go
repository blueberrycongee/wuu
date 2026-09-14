package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/channels"
)

func channelAttachmentReference(message channels.Message, field string, index int, media, data string) string {
	digest := sha256.Sum256([]byte(media + "\x00" + data))
	ref, _ := json.Marshal([]any{message.RoomID, message.ID, message.Seq, index, fmt.Sprintf("%x", digest), field})
	return "channel:" + base64.RawURLEncoding.EncodeToString(ref)
}

func remoteChannelMessage(message channels.Message) channels.Message {
	if message.AuthorType == channels.MemberAgent {
		for _, image := range markdownImageReferences("channel", message.RoomID, fmt.Sprint(message.Seq), message.ID, message.Body) {
			message.MarkdownImages = append(message.MarkdownImages, channels.MessageImage{MediaType: image.MediaType, RemoteRef: image.RemoteRef})
		}
	}
	message.Images = append([]channels.MessageImage(nil), message.Images...)
	message.Files = append([]channels.MessageFile(nil), message.Files...)
	for i, image := range message.Images {
		message.Images[i].RemoteRef = channelAttachmentReference(message, "images", i, image.MediaType, image.Data)
		message.Images[i].Data = ""
	}
	for i, file := range message.Files {
		message.Files[i].RemoteRef = channelAttachmentReference(message, "files", i, file.MediaType, file.Data)
		message.Files[i].Data = ""
	}
	return message
}

func (s *Server) handleChannelAttachmentRead(ctx context.Context, req Request) error {
	var params struct {
		RoomID    string `json:"room_id"`
		MessageID string `json:"message_id"`
		Seq       int64  `json:"seq"`
		Index     int    `json:"index"`
		Field     string `json:"field"`
		SHA256    string `json:"sha256"`
		Offset    int    `json:"offset"`
		Preview   bool   `json:"preview"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	if params.Seq <= 0 || params.Seq == 1<<63-1 || params.Index < 0 || params.Offset < 0 || len(params.SHA256) != 64 {
		return s.writeResponse(req.ID, nil, errors.New("invalid attachment reference or offset"))
	}
	messages, err := s.channelService.ListMessageWindow(ctx, channels.RoomHistoryQuery{RoomID: params.RoomID, AfterSeq: params.Seq - 1, BeforeSeq: params.Seq + 1, Limit: 1})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var media, data string
	if len(messages) == 1 && messages[0].ID == params.MessageID && messages[0].Seq == params.Seq {
		message := messages[0]
		switch params.Field {
		case "images":
			if params.Index < len(message.Images) {
				media, data = message.Images[params.Index].MediaType, message.Images[params.Index].Data
			}
		case "files":
			if params.Index < len(message.Files) {
				media, data = message.Files[params.Index].MediaType, message.Files[params.Index].Data
			}
		}
	}
	digest := sha256.Sum256([]byte(media + "\x00" + data))
	if data == "" || fmt.Sprintf("%x", digest) != params.SHA256 {
		return s.writeResponse(req.ID, nil, errors.New("attachment changed or was removed"))
	}
	result, err := readAttachmentChunk(data, media, params.Offset, params.Preview)
	return s.writeResponse(req.ID, result, err)
}
