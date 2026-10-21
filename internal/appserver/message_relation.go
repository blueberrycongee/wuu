package appserver

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Only fresh output inherits the admission envelope. Checkpoints can contain
// earlier addressed rows and must never reattribute those to the current turn.
func inheritMessageRelation(messages []providers.ChatMessage, relation *providers.MessageRelation) {
	if relation == nil {
		return
	}
	for i := range messages {
		if messages[i].Role == "assistant" && messages[i].Seq == 0 {
			messages[i].Relation = providers.CloneMessageRelation(relation)
		}
	}
}

func (th *threadState) applyStreamEventWithRelationLocked(turnID string, ev providers.StreamEvent, now time.Time, relation *providers.MessageRelation) []outboundNotification {
	out := th.applyStreamEventLocked(turnID, ev, now)
	if relation == nil {
		return out
	}
	if index := th.turnIndexLocked(turnID); index >= 0 {
		for i := range th.Turns[index].Items {
			item := &th.Turns[index].Items[i]
			if item.Type == ThreadItemAgentMessage {
				item.Relation = providers.CloneMessageRelation(relation)
			}
		}
	}
	for i := range out {
		switch p := out[i].params.(type) {
		case ItemStartedNotification:
			if p.Item.Type == ThreadItemAgentMessage {
				p.Item.Relation = providers.CloneMessageRelation(relation)
				out[i].params = p
			}
		case ItemCompletedNotification:
			if p.Item.Type == ThreadItemAgentMessage {
				p.Item.Relation = providers.CloneMessageRelation(relation)
				out[i].params = p
			}
		}
	}
	return out
}

func (s *Server) validateMessageRelation(pluginID, target string, relation *providers.MessageRelation) error {
	if relation == nil {
		return nil
	}
	encoded, err := json.Marshal(relation)
	if err != nil {
		return err
	}
	if len(encoded) > 64*1024 {
		return errors.New("message relation exceeds 64 KB")
	}
	metadata, found, err := session.Find(s.rt.SessionDir, target)
	if err != nil {
		return err
	}
	if !found || metadata.Owner != "plugin:"+pluginID {
		return errors.New("message relation target must be owned by the plugin")
	}
	if relation.ReplyTo == nil {
		return nil
	}
	ref := relation.ReplyTo
	if ref.SessionID != target || ref.Seq <= 0 {
		return errors.New("reply_to must address a message in the target session")
	}
	records, err := session.LoadActiveHistoryRecords(s.rt.SessionDir, target, false)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Seq == ref.Seq && !record.Hidden && (record.Role == "user" || record.Role == "assistant" || (record.Role == "tool" && len(runtime.HistoryAttachmentMetadata(record)) > 0)) {
			return nil
		}
	}
	return errors.New("reply_to message is not visible in the target session")
}
