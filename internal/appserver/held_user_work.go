package appserver

import (
	"encoding/json"
	"errors"
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
		if !isHumanUserMessage(msg) || snapshot.PluginTurn != nil || msg.ClientID != row.ID {
			return nil, fmt.Errorf("invalid held human input %q", row.ID)
		}
		turns = append(turns, queuedTurn{id: row.ID, msg: msg, snapshot: snapshot, origin: row.Origin, durable: true})
	}
	return turns, nil
}

func heldUserWorkRow(turn queuedTurn) (session.HeldUserWork, error) {
	if !isHumanUserMessage(turn.msg) || turn.snapshot.PluginTurn != nil {
		return session.HeldUserWork{}, errors.New("only human input can be held")
	}
	messageJSON, err := json.Marshal(turn.msg)
	if err != nil {
		return session.HeldUserWork{}, err
	}
	runtimeJSON, err := json.Marshal(turn.snapshot)
	if err != nil {
		return session.HeldUserWork{}, err
	}
	origin := turn.origin
	if origin == "" {
		origin = session.HeldUserWorkOriginQueue
	}
	return session.HeldUserWork{ID: turn.id, Origin: origin, MessageJSON: messageJSON, RuntimeJSON: runtimeJSON}, nil
}

func (s *Server) persistAcceptedUserTurn(threadID string, turn queuedTurn) (bool, error) {
	if s == nil || s.rt == nil {
		return false, errors.New("accepted user work requires a session store")
	}
	row, err := heldUserWorkRow(turn)
	if err != nil {
		return false, err
	}
	return session.AcceptHeldUserWork(s.rt.SessionDir, threadID, row)
}

func (s *Server) persistUpdatedUserTurn(threadID string, previous, turn queuedTurn) error {
	row, err := heldUserWorkRow(turn)
	if err != nil {
		return err
	}
	expected, err := heldUserWorkRow(previous)
	if err != nil {
		return err
	}
	updated, err := session.UpdateHeldUserWork(s.rt.SessionDir, threadID, row, expected)
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("accepted input changed before update; review the held message")
	}
	return nil
}

func (s *Server) appendHeldUserTurns(threadID string, turns []queuedTurn) ([]queuedTurn, error) {
	s.heldUserWorkMu.Lock()
	defer s.heldUserWorkMu.Unlock()
	for _, turn := range turns {
		// Accepted work is already durable. Do not overwrite a newer edit or
		// resurrect an input admitted before a launcher/notification failure.
		if _, err := s.persistAcceptedUserTurn(threadID, turn); err != nil && !errors.Is(err, session.ErrHeldUserWorkSettled) {
			return nil, err
		}
	}
	return s.loadHeldUserTurns(threadID)
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
		previous := turns[index]
		msg.ClientID = id
		msg.Steered = turns[index].origin == session.HeldUserWorkOriginSteer
		turns[index].msg = msg
		if err := s.persistUpdatedUserTurn(threadID, previous, turns[index]); err != nil {
			return queuedTurn{}, nil, false, err
		}
		return turns[index], turns, true, nil
	}
	return queuedTurn{}, turns, false, nil
}

// A live queue has the same durable recovery record as held input. Only its
// current owner may automatically drain it; another window/restart sees it held.
func (s *Server) heldUserMessageSnapshot(threadID string, turns []queuedTurn) []HeldUserMessage {
	pending := s.pendingUserMessageSummaries(threadID)
	live := make(map[string]bool, len(pending))
	for _, message := range pending {
		live[message.ID] = true
	}
	messages := make([]HeldUserMessage, 0, len(turns))
	for _, turn := range turns {
		if !live[turn.id] {
			messages = append(messages, heldUserMessageSummary(threadID, turn))
		}
	}
	return messages
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
	s.notifyHeldUserTurnsWithError(threadID, turns, "")
}

func (s *Server) notifyHeldUserTurnsWithError(threadID string, turns []queuedTurn, reason string) {
	_ = s.writeNotification(NotificationTurnHeld, TurnHeldNotification{
		ThreadID: threadID,
		Messages: s.heldUserMessageSnapshot(threadID, turns),
		Error:    reason,
	})
}

// holdRejectedQueuedTurn keeps queued input whose admission failed after the
// client was told it was queued. The client gets the reason and an editable
// paused copy rather than a silent removal.
func (s *Server) holdRejectedQueuedTurn(threadID string, entry queuedTurn, cause error) error {
	entry.origin = session.HeldUserWorkOriginQueue
	held, err := s.appendHeldUserTurns(threadID, []queuedTurn{entry})
	if err != nil {
		return err
	}
	s.notifyHeldUserTurnsWithError(threadID, held, cause.Error())
	return nil
}

func (s *Server) acceptQueuedUserTurn(threadID string, entry queuedTurn, hold bool) (queuedTurn, bool, error) {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	if s.closed.Load() {
		return queuedTurn{}, false, errServerClosed
	}
	if s.isCancelledPendingSubmissionLocked(threadID, entry.id, session.HeldUserWorkOriginQueue) {
		return queuedTurn{}, false, errQueuedTurnCancelled
	}
	accepted, err := s.persistAcceptedUserTurn(threadID, entry)
	if err != nil {
		return queuedTurn{}, false, err
	}
	entry.durable = true
	if !accepted {
		turns, err := s.loadHeldUserTurns(threadID)
		if err != nil {
			return queuedTurn{}, false, err
		}
		for _, existing := range turns {
			if existing.id == entry.id {
				return existing, false, nil
			}
		}
		return queuedTurn{}, false, session.ErrHeldUserWorkSettled
	}
	if !hold {
		if s.pendingQueuedTurns == nil {
			s.pendingQueuedTurns = make(map[string][]queuedTurn)
		}
		s.pendingQueuedTurns[threadID] = append(s.pendingQueuedTurns[threadID], entry)
	}
	return entry, !hold, nil
}

func (s *Server) updateAcceptedQueuedTurn(threadID, id string, msg providers.ChatMessage) (queuedTurn, bool, error) {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	if claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, id)]; claim != nil && !claim.cancelled {
		return queuedTurn{}, false, errors.New("queued input is already being admitted")
	}
	for index, entry := range s.pendingQueuedTurns[threadID] {
		if entry.id != id {
			continue
		}
		previous := entry
		msg.ClientID, msg.Steered = id, false
		entry.msg = msg
		if entry.durable {
			if err := s.persistUpdatedUserTurn(threadID, previous, entry); err != nil {
				return queuedTurn{}, false, err
			}
		}
		s.pendingQueuedTurns[threadID][index] = entry
		return entry, true, nil
	}
	return queuedTurn{}, false, nil
}

func (s *Server) cancelQueuedUserTurn(threadID, id string, persist bool) (queuedTurn, bool, error) {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, id)]
	if claim != nil && claim.committed {
		return queuedTurn{}, false, nil
	}
	var entry queuedTurn
	for _, pending := range s.pendingQueuedTurns[threadID] {
		if pending.id == id {
			entry = pending
			break
		}
	}
	if claim != nil && !claim.cancelled {
		entry = claim.entry
	}
	removed := false
	if persist && s.rt != nil && entry.snapshot.PluginTurn == nil {
		var err error
		removed, err = session.CancelHeldUserWork(s.rt.SessionDir, threadID, id, session.HeldUserWorkOriginQueue)
		if err != nil {
			return queuedTurn{}, false, err
		}
	}
	pending := s.pendingQueuedTurns[threadID]
	for index, queued := range pending {
		if queued.id != id {
			continue
		}
		s.pendingQueuedTurns[threadID] = append(pending[:index], pending[index+1:]...)
		removed = true
		break
	}
	if claim != nil && !claim.cancelled {
		claim.cancelled = true
		removed = true
	}
	// Keep the in-process admission gate for non-persistent/plugin callers too.
	if s.cancelledPendingSubmissions == nil {
		s.cancelledPendingSubmissions = make(map[string]string)
	}
	if len(s.cancelledPendingSubmissions) >= maxCancelledPendingSubmissions {
		clear(s.cancelledPendingSubmissions)
	}
	s.cancelledPendingSubmissions[queuedTurnClaimKey(threadID, id)] = session.HeldUserWorkOriginQueue
	return entry, removed, nil
}
