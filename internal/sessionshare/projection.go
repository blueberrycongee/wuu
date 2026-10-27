package sessionshare

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Project is a typed allowlist, not a secret detector. Review is required because
// visible user/assistant text and intentionally selected results may be sensitive.
func Project(source Source, selection Selection) (Document, []Item, error) {
	out := Document{Version: 1, SessionID: source.SessionID, Items: []VisibleItem{}}
	selected := []Item{}
	ids := map[string]string{}
	for kind, list := range map[string][]string{"message": selection.MessageIDs, "tool_result": selection.ToolResultIDs, "attachment": selection.AttachmentIDs} {
		for _, id := range list {
			if id == "" || ids[id] != "" {
				return out, nil, errors.New("selection IDs must be nonempty and unique")
			}
			ids[id] = kind
		}
	}
	if len(ids) > MaxItems {
		return out, nil, errors.New("too many selected items")
	}
	if selection.After != nil && selection.Before != nil && !selection.After.Before(*selection.Before) {
		return out, nil, errors.New("invalid sharing time range")
	}
	seen := map[string]bool{}
	for _, item := range source.Items {
		if ids[item.ID] != item.Kind {
			continue
		}
		if seen[item.ID] {
			return out, nil, errors.New("duplicate source identity")
		}
		seen[item.ID] = true
		if selection.After != nil && (item.At.IsZero() || item.At.Before(*selection.After)) {
			continue
		}
		if selection.Before != nil && (item.At.IsZero() || !item.At.Before(*selection.Before)) {
			continue
		}
		visible := VisibleItem{ID: item.ID, TurnID: item.TurnID, Kind: item.Kind, Role: item.Role, Text: item.Text, At: item.At}
		if item.Kind == "attachment" {
			data, err := base64.StdEncoding.DecodeString(item.Data)
			if err != nil || len(data) > MaxAttachmentBytes {
				return out, nil, errors.New("invalid or oversized attachment")
			}
			sum := sha256.Sum256(data)
			visible.Role = ""
			visible.Text = ""
			visible.Attachment = &Attachment{ID: item.ID, SHA256: hex.EncodeToString(sum[:]), MediaType: item.MediaType, Bytes: len(data)}
		} else {
			item.Data = ""
			item.MediaType = ""
		}
		out.Items = append(out.Items, visible)
		selected = append(selected, item)
	}
	return out, selected, out.Validate()
}
