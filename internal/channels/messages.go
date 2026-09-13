package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *Service) SendHuman(ctx context.Context, params HumanSendParams) (SendResult, error) {
	result, err := s.send(ctx, sendParams{
		RoomID:     params.RoomID,
		AuthorType: MemberHuman,
		AuthorID:   params.HumanID,
		ThreadID:   params.ThreadID,
		ReplyTo:    params.ReplyTo,
		Body:       params.Body,
		Images:     params.Images,
		Files:      params.Files,
	})
	if err == nil {
		s.emitSendTelemetry(MemberHuman, params.HumanID, result)
	}
	return result, err
}

func (s *Service) SendAgent(ctx context.Context, params AgentSendParams) (SendResult, error) {
	if _, err := s.AuthenticateAgent(ctx, params.AgentID, params.Token); err != nil {
		return SendResult{}, err
	}
	if params.BasisSeq < 0 {
		return SendResult{}, errors.New("message basis sequence cannot be negative")
	}
	result, err := s.send(ctx, sendParams{
		RoomID:     params.RoomID,
		AuthorType: MemberAgent,
		AuthorID:   params.AgentID,
		SessionRef: params.SessionRef,
		ThreadID:   params.ThreadID,
		ReplyTo:    params.ReplyTo,
		Body:       params.Body,
		BasisSeq:   params.BasisSeq,
	})
	if err == nil {
		s.emitSendTelemetry(MemberAgent, params.AgentID, result)
	}
	return result, err
}

func (s *Service) emitSendTelemetry(memberType MemberType, memberID string, result SendResult) {
	if result.Status == SendHeld && result.Draft != nil {
		s.emitTelemetry(TelemetryEvent{
			Name: "draft_held", MemberType: memberType, MemberID: memberID, RoomID: result.Draft.RoomID,
			ThreadID: result.Draft.ThreadID, HoldCount: result.Draft.HoldCount,
		})
		return
	}
	if result.Status == SendCommitted {
		s.emitTelemetry(TelemetryEvent{
			Name: "message_committed", MemberType: memberType, MemberID: memberID, RoomID: result.Message.RoomID,
			ThreadID: result.Message.ThreadID, WakeCount: len(result.WakeAgentIDs),
		})
	}
}

type sendParams struct {
	RoomID     string
	AuthorType MemberType
	AuthorID   string
	SessionRef string
	ThreadID   string
	ReplyTo    string
	Body       string
	Images     []MessageImage
	Files      []MessageFile
	BasisSeq   int64
	DraftID    string
	Force      bool
}

func (s *Service) send(ctx context.Context, params sendParams) (SendResult, error) {
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.AuthorID = strings.TrimSpace(params.AuthorID)
	params.SessionRef = strings.TrimSpace(params.SessionRef)
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.ReplyTo = strings.TrimSpace(params.ReplyTo)
	params.DraftID = strings.TrimSpace(params.DraftID)
	params.Body = strings.TrimSpace(params.Body)
	if params.RoomID == "" || params.AuthorID == "" {
		return SendResult{}, errors.New("message room and author are required")
	}
	if params.Body == "" && len(params.Images) == 0 && len(params.Files) == 0 {
		return SendResult{}, errors.New("message body or attachment is required")
	}
	if utf8.RuneCountInString(params.Body) > MaxMessageRunes {
		return SendResult{}, fmt.Errorf("message exceeds %d characters", MaxMessageRunes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SendResult{}, fmt.Errorf("begin message send: %w", err)
	}
	defer tx.Rollback()
	if err := requireMemberTx(ctx, tx, params.RoomID, params.AuthorType, params.AuthorID); err != nil {
		return SendResult{}, err
	}
	if params.AuthorType == MemberAgent {
		if err := validateCollaborationSessionWriteTx(ctx, tx, params.SessionRef, params.AuthorID, params.RoomID, "", 0); err != nil {
			return SendResult{}, err
		}
	}
	threadID, reply, err := resolveThreadTx(ctx, tx, params.RoomID, params.ThreadID, params.ReplyTo)
	if err != nil {
		return SendResult{}, err
	}
	var existingDraft *Draft
	if params.DraftID != "" {
		draft, err := loadDraftTx(ctx, tx, params.DraftID)
		if err != nil {
			return SendResult{}, err
		}
		if draft.AgentID != params.AuthorID || draft.SessionRef != params.SessionRef ||
			draft.RoomID != params.RoomID || draft.ThreadID != threadID || draft.Body != params.Body {
			return SendResult{}, ErrUnauthorized
		}
		if draft.State != DraftHeld {
			return SendResult{}, fmt.Errorf("%w: draft %q is %s", ErrConflict, draft.ID, draft.State)
		}
		if params.Force && draft.HoldCount < 2 {
			return SendResult{}, errors.New("chat draft anyway requires hold_count >= 2")
		}
		existingDraft = &draft
	}
	if params.AuthorType == MemberAgent && !params.Force {
		currentSeq, err := currentScopeSequenceTx(ctx, tx, params.RoomID, threadID)
		if err != nil {
			return SendResult{}, err
		}
		if params.BasisSeq != currentSeq {
			now := fromMillis(toMillis(s.now()))
			var draft Draft
			var delta DraftDelta
			if existingDraft != nil {
				draft, delta, err = reholdDraftTx(ctx, tx, *existingDraft, params.BasisSeq, now)
			} else {
				draft, delta, err = holdNewDraftTx(
					ctx, tx, params.AuthorID, params.SessionRef, params.RoomID, threadID, params.Body, params.BasisSeq, now,
				)
			}
			if err != nil {
				return SendResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return SendResult{}, fmt.Errorf("commit held draft: %w", err)
			}
			return SendResult{Status: SendHeld, Draft: &draft, Delta: &delta}, nil
		}
	}
	mentions, err := resolveMentionsTx(ctx, tx, params.RoomID, params.Body)
	if err != nil {
		return SendResult{}, err
	}
	mentionIDs := make([]string, len(mentions))
	for i, mention := range mentions {
		mentionIDs[i] = mention.MemberID
	}
	mentionsJSON, err := encodeMentions(mentionIDs)
	if err != nil {
		return SendResult{}, err
	}
	imagesJSON, err := json.Marshal(params.Images)
	if err != nil {
		return SendResult{}, fmt.Errorf("encode room message images: %w", err)
	}
	filesJSON, err := json.Marshal(params.Files)
	if err != nil {
		return SendResult{}, fmt.Errorf("encode room message files: %w", err)
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, params.RoomID,
	).Scan(&seq); err != nil {
		return SendResult{}, fmt.Errorf("allocate room message sequence: %w", err)
	}
	id, err := randomID("msg", 12)
	if err != nil {
		return SendResult{}, err
	}
	now := fromMillis(toMillis(s.now()))
	message := Message{
		ID:         id,
		RoomID:     params.RoomID,
		Seq:        seq,
		ThreadID:   threadID,
		AuthorType: params.AuthorType,
		AuthorID:   params.AuthorID,
		Kind:       MessageText,
		Body:       params.Body,
		Images:     params.Images,
		Files:      params.Files,
		Mentions:   mentionIDs,
		ReplyTo:    params.ReplyTo,
		CreatedAt:  now,
	}
	if params.AuthorType == MemberAgent && params.SessionRef != "" {
		binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
		if err != nil {
			return SendResult{}, err
		}
		message.SourceSessionRef = binding.SessionRef
		message.SourceTurnID = binding.TurnID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (
			id, room_id, seq, thread_id, author_type, author_id, kind, body,
			images_json, files_json, mentions_json, reply_to, task_title, task_state, task_owner,
			task_verification_required, task_goal_revision, task_candidate_revision, created_at, source_session_ref, source_turn_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID, message.RoomID, message.Seq, nullableString(message.ThreadID),
		message.AuthorType, message.AuthorID, message.Kind, message.Body,
		string(imagesJSON), string(filesJSON), mentionsJSON, nullableString(message.ReplyTo), nullableString(message.TaskTitle),
		nullableString(message.TaskState), nullableString(message.TaskOwner), boolInt(message.TaskVerificationRequired),
		message.TaskGoalRevision, message.TaskCandidateRevision, toMillis(message.CreatedAt),
		nullableString(message.SourceSessionRef), nullableString(message.SourceTurnID),
	); err != nil {
		return SendResult{}, fmt.Errorf("insert room message: %w", err)
	}
	if existingDraft != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE drafts SET state = 'committed', updated_at = ? WHERE id = ? AND state = 'held'`, toMillis(now), existingDraft.ID); err != nil {
			return SendResult{}, fmt.Errorf("commit held draft state: %w", err)
		}
	}
	if params.AuthorType == MemberAgent {
		if _, err := tx.ExecContext(ctx, `
			UPDATE drafts SET state = 'dropped', updated_at = ?
			WHERE agent_id = ? AND COALESCE(session_ref, '') = ? AND room_id = ?
				AND COALESCE(thread_id, '') = ? AND state = 'held' AND id != ?`,
			toMillis(now), params.AuthorID, params.SessionRef, params.RoomID, threadID, params.DraftID); err != nil {
			return SendResult{}, fmt.Errorf("drop replaced held drafts: %w", err)
		}
	}
	wakeTargets, err := evaluateTriggersTx(ctx, tx, message, mentions, reply, toMillis(now))
	if err != nil {
		return SendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SendResult{}, fmt.Errorf("commit message send: %w", err)
	}
	if s.wake != nil {
		for _, agentID := range wakeTargets {
			s.wake.Deliver(agentID)
		}
	}
	return SendResult{Status: SendCommitted, Message: message, WakeAgentIDs: wakeTargets}, nil
}

func (s *Service) ListMessages(ctx context.Context, roomID string, afterSeq int64, limit int) ([]Message, error) {
	return s.queryMessages(ctx, RoomHistoryQuery{RoomID: roomID, AfterSeq: afterSeq, Limit: limit})
}

// ListMessageWindow supports bounded backward paging without loading an entire room.
// BeforeSeq is exclusive; zero means no upper bound. Results are always chronological.
func (s *Service) ListMessageWindow(ctx context.Context, p RoomHistoryQuery) ([]Message, error) {
	return s.queryMessages(ctx, p)
}

func (s *Service) queryMessages(ctx context.Context, p RoomHistoryQuery) ([]Message, error) {
	roomID, afterSeq, limit := p.RoomID, p.AfterSeq, p.Limit
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, errors.New("message room is required")
	}
	if afterSeq < 0 || p.BeforeSeq < 0 {
		return nil, errors.New("message sequence cannot be negative")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	order := "ASC"
	if p.Latest {
		order = "DESC"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, room_id, seq, COALESCE(thread_id, ''), author_type, author_id,
			kind, body, images_json, files_json, mentions_json, COALESCE(reply_to, ''),
			COALESCE(task_title, ''), COALESCE(task_state, ''), COALESCE(task_owner, ''),
			task_verification_required, task_goal_revision, task_candidate_revision, created_at,
			COALESCE(source_session_ref, ''), COALESCE(source_turn_id, '')
		FROM room_messages WHERE room_id = ? AND seq > ? AND (?=0 OR seq<?)
   AND (?='' OR thread_id=? OR id=?) AND (?='' OR instr(lower(body),lower(?))>0)
   ORDER BY seq `+order+` LIMIT ?`, roomID, afterSeq, p.BeforeSeq, p.BeforeSeq, p.ThreadID, p.ThreadID, p.ThreadID, p.Query, p.Query, limit)
	if err != nil {
		return nil, fmt.Errorf("list room messages: %w", err)
	}
	messages := make([]Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list room messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close room messages: %w", err)
	}
	if p.Latest {
		for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
			messages[left], messages[right] = messages[right], messages[left]
		}
	}
	if err := s.attachWorkDetails(ctx, messages); err != nil {
		return nil, err
	}
	if err := s.attachAgentCreationProposals(ctx, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *Service) attachWorkDetails(ctx context.Context, messages []Message) error {
	for index := range messages {
		if messages[index].Kind != MessageTask {
			continue
		}
		work, err := s.GetWork(ctx, messages[index].ID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		messages[index].Work = &work
	}
	return nil
}

func (s *Service) ListInbox(ctx context.Context, agentID string, unpulledOnly bool) ([]InboxItem, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("inbox agent is required")
	}
	query := `
		SELECT id, member_id, room_id, message_id, kind, created_at, pulled_at
		FROM inbox_items WHERE member_type = 'agent' AND member_id = ?`
	if unpulledOnly {
		query += ` AND pulled_at IS NULL`
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, fmt.Errorf("list inbox items: %w", err)
	}
	defer rows.Close()
	items := make([]InboxItem, 0)
	for rows.Next() {
		var item InboxItem
		var createdAt int64
		var pulledAt sql.NullInt64
		if err := rows.Scan(&item.ID, &item.AgentID, &item.RoomID, &item.MessageID, &item.Kind, &createdAt, &pulledAt); err != nil {
			return nil, fmt.Errorf("scan inbox item: %w", err)
		}
		item.CreatedAt = fromMillis(createdAt)
		if pulledAt.Valid {
			item.PulledAt = fromMillis(pulledAt.Int64)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list inbox items: %w", err)
	}
	return items, nil
}

func requireMemberTx(ctx context.Context, tx *sql.Tx, roomID string, memberType MemberType, memberID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM room_members WHERE room_id = ? AND member_type = ? AND member_id = ?`,
		roomID, memberType, memberID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %q is not a member of room %q", ErrUnauthorized, memberType, memberID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate room membership: %w", err)
	}
	return nil
}

func resolveThreadTx(ctx context.Context, tx *sql.Tx, roomID, requestedThreadID, replyTo string) (string, *Message, error) {
	var reply *Message
	threadID := requestedThreadID
	if replyTo != "" {
		message, err := loadMessageTx(ctx, tx, replyTo)
		if err != nil {
			return "", nil, err
		}
		if message.RoomID != roomID {
			return "", nil, errors.New("reply target belongs to a different room")
		}
		reply = &message
		derived := message.ThreadID
		if derived == "" {
			derived = message.ID
		}
		if threadID != "" && threadID != derived {
			return "", nil, errors.New("message thread does not match reply target")
		}
		threadID = derived
	}
	if threadID != "" {
		root, err := loadMessageTx(ctx, tx, threadID)
		if err != nil {
			return "", nil, err
		}
		if root.RoomID != roomID || root.ThreadID != "" {
			return "", nil, errors.New("thread root is invalid for room")
		}
	}
	return threadID, reply, nil
}

func loadMessageTx(ctx context.Context, tx *sql.Tx, id string) (Message, error) {
	message, err := scanMessage(tx.QueryRowContext(ctx, `
		SELECT id, room_id, seq, COALESCE(thread_id, ''), author_type, author_id,
			kind, body, images_json, files_json, mentions_json, COALESCE(reply_to, ''),
			COALESCE(task_title, ''), COALESCE(task_state, ''), COALESCE(task_owner, ''),
			task_verification_required, task_goal_revision, task_candidate_revision, created_at,
			COALESCE(source_session_ref, ''), COALESCE(source_turn_id, '')
		FROM room_messages WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return Message{}, fmt.Errorf("%w: message %q", ErrNotFound, id)
	}
	return message, err
}

func scanMessage(row scanner) (Message, error) {
	var message Message
	var imagesJSON string
	var filesJSON string
	var mentionsJSON string
	var taskVerificationRequired int
	var taskGoalRevision int
	var taskCandidateRevision int
	var createdAt int64
	if err := row.Scan(
		&message.ID, &message.RoomID, &message.Seq, &message.ThreadID,
		&message.AuthorType, &message.AuthorID, &message.Kind, &message.Body,
		&imagesJSON, &filesJSON, &mentionsJSON, &message.ReplyTo, &message.TaskTitle, &message.TaskState, &message.TaskOwner,
		&taskVerificationRequired, &taskGoalRevision, &taskCandidateRevision, &createdAt,
		&message.SourceSessionRef, &message.SourceTurnID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		return Message{}, fmt.Errorf("scan room message: %w", err)
	}
	if err := json.Unmarshal([]byte(mentionsJSON), &message.Mentions); err != nil {
		return Message{}, fmt.Errorf("decode room message mentions: %w", err)
	}
	if err := json.Unmarshal([]byte(imagesJSON), &message.Images); err != nil {
		return Message{}, fmt.Errorf("decode room message images: %w", err)
	}
	if err := json.Unmarshal([]byte(filesJSON), &message.Files); err != nil {
		return Message{}, fmt.Errorf("decode room message files: %w", err)
	}
	message.CreatedAt = fromMillis(createdAt)
	message.TaskVerificationRequired = taskVerificationRequired != 0
	message.TaskGoalRevision = taskGoalRevision
	message.TaskCandidateRevision = taskCandidateRevision
	return message, nil
}

func resolveMentionsTx(ctx context.Context, tx *sql.Tx, roomID, body string) ([]RoomMember, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT member.member_type, member.member_id, COALESCE(agent.name, '')
		FROM room_members member
		LEFT JOIN named_agents agent ON agent.id = member.member_id AND member.member_type = 'agent'
		WHERE member.room_id = ?`, roomID)
	if err != nil {
		return nil, fmt.Errorf("list mentionable members: %w", err)
	}
	defer rows.Close()
	mentions := make([]RoomMember, 0)
	type mentionableMember struct {
		RoomMember
		name string
	}
	members := make([]mentionableMember, 0)
	nameCounts := make(map[string]int)
	for rows.Next() {
		var memberType MemberType
		var id, name string
		if err := rows.Scan(&memberType, &id, &name); err != nil {
			return nil, fmt.Errorf("scan mentionable member: %w", err)
		}
		members = append(members, mentionableMember{RoomMember{MemberType: memberType, MemberID: id}, name})
		if memberType == MemberAgent {
			nameCounts[name]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mentionable members: %w", err)
	}
	for _, member := range members {
		// A display name only identifies one member when it is unambiguous.
		if mentionPresent(body, member.MemberID) || (member.MemberType == MemberAgent &&
			((nameCounts[member.name] == 1 && mentionPresent(body, member.name)) || mentionPresent(body, "all") || mentionPresent(body, "everyone"))) {
			mentions = append(mentions, member.RoomMember)
		}
	}
	sort.Slice(mentions, func(i, j int) bool {
		if mentions[i].MemberType != mentions[j].MemberType {
			return mentions[i].MemberType < mentions[j].MemberType
		}
		return mentions[i].MemberID < mentions[j].MemberID
	})
	return mentions, nil
}

func mentionPresent(body, name string) bool {
	needle := "@" + name
	for offset := 0; offset < len(body); {
		index := strings.Index(body[offset:], needle)
		if index < 0 {
			return false
		}
		start := offset + index
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(body[:start])
			if !unicode.IsSpace(previous) && !unicode.IsPunct(previous) && !unicode.IsSymbol(previous) {
				offset = start + 1
				continue
			}
		}
		end := start + len(needle)
		if end == len(body) {
			return true
		}
		next, _ := utf8.DecodeRuneInString(body[end:])
		if unicode.IsSpace(next) || unicode.IsPunct(next) || unicode.IsSymbol(next) {
			return true
		}
		offset = start + 1
	}
	return false
}

func evaluateTriggersTx(ctx context.Context, tx *sql.Tx, message Message, mentions []RoomMember, reply *Message, now int64) ([]string, error) {
	suppressed, err := isThreadLoopSuppressedTx(ctx, tx, message)
	if err != nil {
		return nil, err
	}
	agentSignals := make(map[string]InboxKind)
	rows, err := tx.QueryContext(ctx, `SELECT member_id FROM room_members WHERE room_id = ? AND member_type = 'agent'`, message.RoomID)
	if err != nil {
		return nil, fmt.Errorf("list room agents for inbox signals: %w", err)
	}
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return nil, err
		}
		if message.AuthorType != MemberAgent || message.AuthorID != agentID {
			agentSignals[agentID] = InboxThreadUpdate
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close room agent signals: %w", err)
	}

	explicitAgentMention := false
	for _, mention := range mentions {
		if mention.MemberType == MemberAgent {
			explicitAgentMention = true
		}
	}
	wake := make(map[string]struct{})
	for _, mention := range mentions {
		if mention.MemberType == MemberAgent && message.AuthorType == MemberAgent && message.AuthorID == mention.MemberID {
			continue
		}
		if mention.MemberType == MemberAgent {
			agentSignals[mention.MemberID] = InboxMention
			if !suppressed {
				wake[mention.MemberID] = struct{}{}
			}
		} else if mention.MemberType == MemberHuman {
			if err := insertInboxTx(ctx, tx, MemberHuman, mention.MemberID, message.RoomID, message.ID, InboxMention, now); err != nil {
				return nil, err
			}
		}
	}
	if reply != nil && reply.AuthorType == MemberAgent && !(message.AuthorType == MemberAgent && message.AuthorID == reply.AuthorID) && !(message.AuthorType == MemberHuman && explicitAgentMention) {
		if _, member := agentSignals[reply.AuthorID]; member {
			if agentSignals[reply.AuthorID] != InboxMention {
				agentSignals[reply.AuthorID] = InboxReply
			}
			if !suppressed {
				wake[reply.AuthorID] = struct{}{}
			}
		}
	}
	for agentID, kind := range agentSignals {
		if err := insertInboxTx(ctx, tx, MemberAgent, agentID, message.RoomID, message.ID, kind, now); err != nil {
			return nil, err
		}

	}

	if message.AuthorType == MemberHuman && !suppressed && !explicitAgentMention && len(wake) == 0 {
		// A reply in an established task stays with its accountable owner.
		var owner string
		if message.ThreadID != "" {
			err := tx.QueryRowContext(ctx, `SELECT owner_named_agent_id FROM works WHERE id = ? AND room_id = ?`, message.ThreadID, message.RoomID).Scan(&owner)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if _, member := agentSignals[owner]; owner != "" && member {
			wake[owner] = struct{}{}
		} else if len(agentSignals) == 1 {
			// A single member needs no routing inference, including in a DM.
			for id := range agentSignals {
				wake[id] = struct{}{}
			}
		} else {
			var coordinator string
			err := tx.QueryRowContext(ctx, `SELECT id FROM room_runtimes WHERE room_id = ? AND autostart = 1`, message.RoomID).Scan(&coordinator)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if coordinator != "" {
				wake[coordinator] = struct{}{}
			}
		}
	}

	targets := make([]string, 0, len(wake))
	for agentID := range wake {
		if message.AuthorType == MemberAgent && message.AuthorID == agentID {
			continue
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: message.RoomID, FromType: message.AuthorType, FromID: message.AuthorID,
			ToAgentID: agentID, Body: message.Body, SourceMessageID: message.ID, CreatedAt: fromMillis(now),
		}); err != nil {
			return nil, err
		}
		requested, err := requestWakeTx(ctx, tx, agentID, now)
		if err != nil {
			return nil, fmt.Errorf("request agent wake: %w", err)
		}
		if requested {
			targets = append(targets, agentID)
		}
	}
	sort.Strings(targets)
	return targets, nil
}

func isThreadLoopSuppressedTx(ctx context.Context, tx *sql.Tx, message Message) (bool, error) {
	if message.ThreadID == "" {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT author_type FROM room_messages
		WHERE room_id = ? AND (id = ? OR thread_id = ?)
		ORDER BY seq DESC
		LIMIT ?`, message.RoomID, message.ThreadID, message.ThreadID, ThreadStreakCap)
	if err != nil {
		return false, fmt.Errorf("query thread streak: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var authorType string
		if err := rows.Scan(&authorType); err != nil {
			return false, fmt.Errorf("scan thread streak: %w", err)
		}
		if authorType == string(MemberHuman) {
			break
		}
		if authorType == string(MemberAgent) {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate thread streak: %w", err)
	}
	return count >= ThreadStreakCap, nil
}

func insertInboxTx(ctx context.Context, tx *sql.Tx, memberType MemberType, memberID, roomID, messageID string, kind InboxKind, now int64) error {
	id, err := randomID("inbox", 12)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO inbox_items (id, member_type, member_id, room_id, message_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET id = excluded.id, created_at = excluded.created_at, pulled_at = NULL`,
		id, string(memberType), memberID, roomID, messageID, string(kind), now)
	if err != nil {
		return fmt.Errorf("insert inbox item: %w", err)
	}
	return nil
}

func insertReminderInboxTx(ctx context.Context, tx *sql.Tx, agentID, roomID, reminderID string, now int64) error {
	id, err := randomID("inbox", 12)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO inbox_items (id, member_type, member_id, room_id, reminder_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET id = excluded.id, created_at = excluded.created_at, pulled_at = NULL`,
		id, string(MemberAgent), agentID, nullableString(roomID), reminderID, string(InboxReminder), now)
	if err != nil {
		return fmt.Errorf("insert reminder inbox item: %w", err)
	}
	return nil
}
