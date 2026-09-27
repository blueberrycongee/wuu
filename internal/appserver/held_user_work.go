package appserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) loadHeldUserTurns(threadID string) ([]queuedTurn, error) {
	if s == nil || s.rt == nil {
		return nil, nil
	}
	rows, err := session.LoadHeldUserWork(s.rt.SessionDir, threadID)
	if err != nil {
		return nil, err
	}
	turns := make([]queuedTurn, 0, len(rows))
	for _, row := range rows {
		var msg providers.ChatMessage
		var snapshot turnRuntimeSnapshot
		if err := json.Unmarshal(row.MessageJSON, &msg); err != nil {
			return nil, fmt.Errorf("decode held user message %q: %w", row.ID, err)
		}
		if err := json.Unmarshal(row.RuntimeJSON, &snapshot); err != nil {
			return nil, fmt.Errorf("decode held user runtime %q: %w", row.ID, err)
		}
		turns = append(turns, queuedTurn{id: row.ID, msg: msg, snapshot: snapshot, origin: row.Origin})
	}
	return turns, nil
}

func (s *Server) replaceHeldUserTurns(threadID string, turns []queuedTurn) error {
	rows := make([]session.HeldUserWork, 0, len(turns))
	for _, turn := range turns {
		messageJSON, err := json.Marshal(turn.msg)
		if err != nil {
			return fmt.Errorf("encode held user message %q: %w", turn.id, err)
		}
		runtimeJSON, err := json.Marshal(turn.snapshot)
		if err != nil {
			return fmt.Errorf("encode held user runtime %q: %w", turn.id, err)
		}
		origin := strings.TrimSpace(turn.origin)
		if origin == "" {
			origin = session.HeldUserWorkOriginQueue
		}
		rows = append(rows, session.HeldUserWork{
			ID: turn.id, Origin: origin, MessageJSON: messageJSON, RuntimeJSON: runtimeJSON,
		})
	}
	return session.ReplaceHeldUserWork(s.rt.SessionDir, threadID, rows)
}

func (s *Server) appendHeldUserTurns(threadID string, turns []queuedTurn) ([]queuedTurn, error) {
	s.heldUserWorkMu.Lock()
	defer s.heldUserWorkMu.Unlock()
	existing, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		return nil, err
	}
	next := append(append([]queuedTurn(nil), existing...), turns...)
	if err := s.replaceHeldUserTurns(threadID, next); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *Server) findHeldUserTurn(threadID, id string) (queuedTurn, bool, error) {
	s.heldUserWorkMu.Lock()
	defer s.heldUserWorkMu.Unlock()
	turns, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		return queuedTurn{}, false, err
	}
	for _, turn := range turns {
		if turn.id == id {
			return turn, true, nil
		}
	}
	return queuedTurn{}, false, nil
}

func (s *Server) removeHeldUserTurn(threadID, id string) (queuedTurn, []queuedTurn, bool, error) {
	s.heldUserWorkMu.Lock()
	defer s.heldUserWorkMu.Unlock()
	turns, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		return queuedTurn{}, nil, false, err
	}
	for index, turn := range turns {
		if turn.id != id {
			continue
		}
		next := append(append([]queuedTurn(nil), turns[:index]...), turns[index+1:]...)
		if err := s.replaceHeldUserTurns(threadID, next); err != nil {
			return queuedTurn{}, nil, false, err
		}
		return turn, next, true, nil
	}
	return queuedTurn{}, turns, false, nil
}

func (s *Server) updateHeldUserTurn(threadID, id string, msg providers.ChatMessage) (queuedTurn, []queuedTurn, bool, error) {
	s.heldUserWorkMu.Lock()
	defer s.heldUserWorkMu.Unlock()
	turns, err := s.loadHeldUserTurns(threadID)
	if err != nil {
		return queuedTurn{}, nil, false, err
	}
	for index := range turns {
		if turns[index].id != id {
			continue
		}
		msg.ClientID = id
		msg.Steered = turns[index].origin == session.HeldUserWorkOriginSteer
		turns[index].msg = msg
		if err := s.replaceHeldUserTurns(threadID, turns); err != nil {
			return queuedTurn{}, nil, false, err
		}
		return turns[index], turns, true, nil
	}
	return queuedTurn{}, turns, false, nil
}

func heldUserMessageSummary(threadID string, turn queuedTurn) HeldUserMessage {
	images := make([]TurnStartImage, 0, len(turn.msg.Images))
	for _, image := range turn.msg.Images {
		images = append(images, TurnStartImage{MediaType: image.MediaType, Data: image.Data})
	}
	files := make([]TurnStartFile, 0, len(turn.msg.Files))
	for _, file := range turn.msg.Files {
		files = append(files, TurnStartFile{MediaType: file.MediaType, Data: file.Data, Filename: file.Filename})
	}
	return HeldUserMessage{
		ID: turn.id, ThreadID: threadID, Origin: turn.origin,
		Prompt: strings.TrimSpace(chatMessageDisplayContent(turn.msg)), Images: images, Files: files,
		ContentParts:   providers.CloneMessageContentParts(turn.msg.ContentParts),
		ActiveDocument: cloneActiveDocument(turn.snapshot.ActiveDocument),
	}
}

func heldUserMessageSummaries(threadID string, turns []queuedTurn) []HeldUserMessage {
	messages := make([]HeldUserMessage, 0, len(turns))
	for _, turn := range turns {
		messages = append(messages, heldUserMessageSummary(threadID, turn))
	}
	return messages
}

func (s *Server) pendingUserMessageSummaries(threadID string) []HeldUserMessage {
	turns := make([]queuedTurn, 0)
	th := s.thread(threadID)
	if th != nil {
		th.mu.Lock()
		defer th.mu.Unlock()
		for _, msg := range th.pendingSteers {
			if _, _, pluginSteer := pluginSessionRequestFromClientID(msg.ClientID); pluginSteer {
				continue
			}
			turns = append(turns, queuedTurn{
				id:  strings.TrimSpace(msg.ClientID),
				msg: msg,
				snapshot: turnRuntimeSnapshot{
					ActiveDocument: th.steerDocumentOverrideLocked(msg.ClientID),
				},
				origin: session.HeldUserWorkOriginSteer,
			})
		}
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	claimPrefix := strings.TrimSpace(threadID) + "\x00"
	for key, claim := range s.claimedQueuedTurns {
		if !strings.HasPrefix(key, claimPrefix) || claim == nil || claim.cancelled || claim.committed || claim.entry.snapshot.PluginTurn != nil {
			continue
		}
		entry := claim.entry
		entry.origin = session.HeldUserWorkOriginQueue
		turns = append(turns, entry)
	}
	for _, entry := range s.pendingQueuedTurns[threadID] {
		if entry.snapshot.PluginTurn != nil {
			continue
		}
		entry.origin = session.HeldUserWorkOriginQueue
		turns = append(turns, entry)
	}
	return heldUserMessageSummaries(threadID, turns)
}

func (s *Server) notifyHeldUserTurns(threadID string, turns []queuedTurn) {
	_ = s.writeNotification(NotificationTurnHeld, TurnHeldNotification{
		ThreadID: threadID,
		Messages: heldUserMessageSummaries(threadID, turns),
	})
}
