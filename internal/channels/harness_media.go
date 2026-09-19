package channels

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// HarnessMediaRef selects one stored attachment, never a filesystem path or URL.
// Index is one-based within the message's images or files array.
type HarnessMediaRef struct {
	MessageID   string `json:"message_id"`
	Kind        string `json:"kind"`
	Index       int    `json:"index"`
	Description string `json:"description,omitempty"`
}

type HarnessMedia struct {
	Ref    HarnessMediaRef
	Source Message
	Image  *MessageImage
	File   *MessageFile
}

// ResolveHarnessMedia retains the room boundary of the accepted source turn.
// Recheck on every dispatch: a queued reference is not a permanent access grant.
func (s *Service) ResolveHarnessMedia(ctx context.Context, actor HarnessSessionActor, refs []HarnessMediaRef) ([]HarnessMedia, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > 32 {
		return nil, fmt.Errorf("media handoff accepts at most 32 attachments")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireRoomPrincipalAccessTx(ctx, tx, actor.RoomID, actor.AgentID); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	result := make([]HarnessMedia, 0, len(refs))
	for i, ref := range refs {
		if strings.TrimSpace(ref.MessageID) == "" || ref.Index < 1 || (ref.Kind != "image" && ref.Kind != "file") {
			return nil, fmt.Errorf("media %d requires message_id, kind image or file, and a one-based index", i+1)
		}
		key := fmt.Sprintf("%s/%s/%d", ref.MessageID, ref.Kind, ref.Index)
		if seen[key] {
			return nil, fmt.Errorf("duplicate media reference %s", key)
		}
		seen[key] = true
		message, err := loadMessageTx(ctx, tx, ref.MessageID)
		if err != nil {
			return nil, fmt.Errorf("media %d source unavailable: %w", i+1, err)
		}
		if message.RoomID != actor.RoomID {
			return nil, fmt.Errorf("media %d must belong to the source turn's room: %w", i+1, ErrUnauthorized)
		}
		item := HarnessMedia{Ref: ref, Source: message}
		switch {
		case ref.Kind == "image" && ref.Index <= len(message.Images):
			image := message.Images[ref.Index-1]
			item.Image = &image
		case ref.Kind == "file" && ref.Index <= len(message.Files):
			file := message.Files[ref.Index-1]
			item.File = &file
		default:
			return nil, fmt.Errorf("media %d attachment no longer available: %w", i+1, ErrNotFound)
		}
		result = append(result, item)
	}
	return result, nil
}
