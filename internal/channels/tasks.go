package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func (s *Service) CreateTask(ctx context.Context, params TaskCreateParams) (Message, error) {
	_, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return Message{}, err
	}
	if params.LeadNamedAgentID == "" {
		params.LeadNamedAgentID = params.AgentID
	}
	return s.createTask(ctx, params)
}

func (s *Service) CreateTaskHuman(ctx context.Context, params TaskCreateParams) (Message, error) {
	if err := s.requireHumanMember(ctx, params.RoomID, params.HumanID); err != nil {
		return Message{}, err
	}
	return s.createTask(ctx, params)
}

func (s *Service) createTask(ctx context.Context, params TaskCreateParams) (Message, error) {
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.Title = strings.TrimSpace(params.Title)
	params.Body = strings.TrimSpace(params.Body)
	params.OwnerID = strings.TrimSpace(params.OwnerID)
	params.LeadNamedAgentID = strings.TrimSpace(params.LeadNamedAgentID)
	params.TargetSessionRef = strings.TrimSpace(params.TargetSessionRef)
	params.SourceMessageID = strings.TrimSpace(params.SourceMessageID)
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	if params.RoomID == "" || params.Title == "" || params.OwnerID == "" {
		return Message{}, errors.New("task room, title and owner are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin task create: %w", err)
	}
	defer tx.Rollback()
	if err := s.requireRoomAgentMemberTx(ctx, tx, params.RoomID, params.OwnerID); err != nil {
		return Message{}, err
	}
	if params.LeadNamedAgentID != "" {
		if err := s.requireRoomAgentMemberTx(ctx, tx, params.RoomID, params.LeadNamedAgentID); err != nil {
			return Message{}, err
		}
	}
	if params.AgentID != "" {
		if err := requireRoomPrincipalAccessTx(ctx, tx, params.RoomID, params.AgentID); err != nil {
			return Message{}, err
		}
	}
	if params.HumanID != "" {
		if err := requireMemberTx(ctx, tx, params.RoomID, MemberHuman, params.HumanID); err != nil {
			return Message{}, err
		}
	}
	if params.SourceSessionRef != "" {
		if err := validateCollaborationSessionWriteTx(ctx, tx, params.SourceSessionRef, params.AgentID, params.RoomID, "", 0); err != nil {
			return Message{}, err
		}
	}
	if params.TargetSessionRef != "" {
		binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.TargetSessionRef))
		if err != nil {
			return Message{}, fmt.Errorf("validate task target session: %w", err)
		}
		if binding.PrincipalID != params.OwnerID {
			return Message{}, fmt.Errorf("%w: task target session belongs to another principal", ErrUnauthorized)
		}
		if binding.RoomID != params.RoomID || binding.WorkID != "" || binding.RunID != "" ||
			binding.State == CollaborationSessionRunning || binding.State == CollaborationSessionInterrupted ||
			binding.State == CollaborationSessionMissing {
			return Message{}, fmt.Errorf("%w: task target session is not available for this room", ErrConflict)
		}
	}
	if params.ThreadID != "" {
		thread, err := loadMessageTx(ctx, tx, params.ThreadID)
		if err != nil {
			return Message{}, err
		}
		if thread.RoomID != params.RoomID {
			return Message{}, errors.New("task thread does not belong to room")
		}
		if thread.ThreadID != "" {
			return Message{}, errors.New("task thread target must be a root message")
		}
	}
	message, err := s.insertTaskMessageTx(ctx, tx, params)
	if err != nil {
		return Message{}, err
	}
	if err := insertWorkTx(ctx, tx, message, params); err != nil {
		return Message{}, err
	}
	if params.TargetSessionRef != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_session_bindings
			SET work_id = ?, purpose = 'work', updated_at = ? WHERE session_ref = ?`,
			message.ID, toMillis(message.CreatedAt), params.TargetSessionRef); err != nil {
			return Message{}, fmt.Errorf("bind task target session: %w", err)
		}
	}
	assignmentFromType := MemberAgent
	assignmentFromID := params.AgentID
	if params.HumanID != "" {
		assignmentFromType = MemberHuman
		assignmentFromID = params.HumanID
	}
	if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
		RoomID: message.RoomID, FromType: assignmentFromType, FromID: assignmentFromID, FromSessionRef: params.SourceSessionRef,
		ToAgentID: params.OwnerID, TargetSessionRef: params.TargetSessionRef,
		WorkID: message.ID, Kind: CollaborationAssignment,
		Body: params.Body, SourceMessageID: params.SourceMessageID,
		GoalRevision: message.TaskGoalRevision, CreatedAt: message.CreatedAt,
	}); err != nil {
		return Message{}, fmt.Errorf("enqueue work assignment: %w", err)
	}
	if err := s.taskEventInboxTx(ctx, tx, message, params.OwnerID, ""); err != nil {
		return Message{}, err
	}
	requested, err := requestWakeTx(ctx, tx, params.OwnerID, toMillis(message.CreatedAt))
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit task create: %w", err)
	}
	if requested && s.wake != nil {
		s.wake.Deliver(params.OwnerID)
	}
	if work, err := s.GetWork(ctx, message.ID); err == nil {
		message.Work = &work
	}
	return message, nil
}

func (s *Service) UpdateTask(ctx context.Context, params TaskUpdateParams) (Message, error) {
	if _, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token); err != nil {
		return Message{}, err
	}
	return s.updateTask(ctx, params)
}

func (s *Service) UpdateTaskHuman(ctx context.Context, params TaskUpdateParams) (Message, error) {
	params.HumanID = strings.TrimSpace(params.HumanID)
	if params.HumanID == "" {
		return Message{}, errors.New("human id is required")
	}
	return s.updateTask(ctx, params)
}

func (s *Service) updateTask(ctx context.Context, params TaskUpdateParams) (Message, error) {
	params.TaskID = strings.TrimSpace(params.TaskID)
	params.OwnerID = strings.TrimSpace(params.OwnerID)
	params.GoalCorrection = strings.TrimSpace(params.GoalCorrection)
	params.SessionRef = strings.TrimSpace(params.SessionRef)
	if params.TaskID == "" {
		return Message{}, errors.New("task id is required")
	}
	if params.State != "" && params.State != TaskStateOpen && params.State != TaskStateDoing &&
		params.State != TaskStateChecking && params.State != TaskStateRevising &&
		params.State != TaskStateNeedsHuman && params.State != TaskStateDone {
		return Message{}, fmt.Errorf("invalid task state %q", params.State)
	}
	if utf8.RuneCountInString(params.GoalCorrection) > MaxMessageRunes {
		return Message{}, fmt.Errorf("task goal correction exceeds %d characters", MaxMessageRunes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin task update: %w", err)
	}
	defer tx.Rollback()
	message, err := loadMessageTx(ctx, tx, params.TaskID)
	if err != nil {
		return Message{}, err
	}
	if message.Kind != MessageTask {
		return Message{}, fmt.Errorf("%w: message %q is not a task", ErrConflict, params.TaskID)
	}
	if params.RoomID != "" && message.RoomID != params.RoomID {
		return Message{}, errors.New("task does not belong to room")
	}
	if params.AgentID != "" {
		if err := requireRoomPrincipalAccessTx(ctx, tx, message.RoomID, params.AgentID); err != nil {
			return Message{}, err
		}
	}
	var leadID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(lead_named_agent_id, '') FROM works WHERE id = ?`, message.ID).Scan(&leadID); err != nil {
		return Message{}, err
	}
	callerOk := params.AgentID != "" && (message.TaskOwner == params.AgentID || leadID == params.AgentID)
	correctionOk := params.GoalCorrection == "" || callerOk
	if params.HumanID != "" {
		if err := requireMemberTx(ctx, tx, message.RoomID, MemberHuman, params.HumanID); err == nil {
			callerOk = true
			correctionOk = true
		}
	}
	if !callerOk || !correctionOk {
		return Message{}, ErrUnauthorized
	}
	terminalWork, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, message.ID))
	if err != nil {
		return Message{}, err
	}
	workState := terminalWork.State
	if terminalWorkState(workState) && params.GoalCorrection == "" {
		return Message{}, fmt.Errorf("%w: work is %s", ErrConflict, workState)
	}
	if params.AgentID != "" {
		sourceWorkID := message.ID
		if binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef)); err == nil && binding.WorkID == "" {
			sourceWorkID = ""
		}
		if err := validateCollaborationSessionWriteTx(
			ctx, tx, params.SessionRef, params.AgentID, message.RoomID, sourceWorkID, message.TaskGoalRevision,
		); err != nil {
			return Message{}, err
		}
	}
	oldOwner := message.TaskOwner
	newOwner := message.TaskOwner
	if params.OwnerID != "" {
		if err := s.requireRoomAgentMemberTx(ctx, tx, message.RoomID, params.OwnerID); err != nil {
			return Message{}, err
		}
		newOwner = params.OwnerID
	}
	ownerChanged := newOwner != oldOwner
	updatedAt := fromMillis(toMillis(s.now()))
	interruptTargets := make([]workSessionInterruptTarget, 0)
	interruptedRuns := make([]WorkRun, 0)
	if params.GoalCorrection != "" || ownerChanged {
		activeTargets, err := activeWorkSessionInterruptTargetsTx(ctx, tx, message.ID)
		if err != nil {
			return Message{}, err
		}
		for _, target := range activeTargets {
			if params.GoalCorrection != "" || target.agentID == oldOwner {
				interruptTargets = append(interruptTargets, target)
			}
		}
		rows, err := tx.QueryContext(ctx, workRunSelect+` WHERE run.work_id = ? AND run.state IN ('queued', 'running') ORDER BY run.created_at, run.id`, message.ID)
		if err != nil {
			return Message{}, err
		}
		for rows.Next() {
			run, err := scanWorkRun(rows)
			if err != nil {
				rows.Close()
				return Message{}, err
			}
			if params.GoalCorrection != "" || run.NamedAgentID == oldOwner {
				interruptedRuns = append(interruptedRuns, run)
			}
		}
		if err := rows.Close(); err != nil {
			return Message{}, err
		}
	}
	setState := message.TaskState
	if params.State != "" {
		setState = string(params.State)
	}
	if params.GoalCorrection != "" {
		message.Body = params.GoalCorrection
		message.TaskGoalRevision++
		setState = string(TaskStateOpen)
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_messages SET invalidated_at = ?
			WHERE work_id = ? AND goal_revision < ? AND pulled_at IS NULL`,
			toMillis(updatedAt), message.ID, message.TaskGoalRevision); err != nil {
			return Message{}, fmt.Errorf("invalidate stale work deliveries: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_session_bindings
			SET state = 'interrupted', run_id = NULL, updated_at = ?
			WHERE run_id IN (
				SELECT id FROM work_runs
				WHERE work_id = ? AND goal_revision < ? AND state IN ('queued', 'running')
			)`, toMillis(updatedAt), message.ID, message.TaskGoalRevision); err != nil {
			return Message{}, fmt.Errorf("release stale work sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE work_runs SET state = 'interrupted', outcome = 'goal revised', ended_at = ?, updated_at = ?
			WHERE work_id = ? AND goal_revision < ? AND state IN ('queued', 'running')`,
			toMillis(updatedAt), toMillis(updatedAt), message.ID, message.TaskGoalRevision); err != nil {
			return Message{}, fmt.Errorf("interrupt stale work runs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE works SET current_run_ref = NULL, candidate_artifact_ref = NULL,
				candidate_workspace_revision = NULL WHERE id = ?`, message.ID); err != nil {
			return Message{}, fmt.Errorf("clear stale work candidate: %w", err)
		}
		if err := insertWorkEventTx(ctx, tx, WorkEvent{
			WorkID: message.ID, Kind: "correction", State: string(WorkOpen), Summary: "User goal revised",
			GoalRevision: message.TaskGoalRevision, CandidateRevision: message.TaskCandidateRevision, CreatedAt: updatedAt,
		}); err != nil {
			return Message{}, err
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: message.RoomID, ToAgentID: newOwner,
			WorkID: message.ID, Kind: CollaborationControl,
			Body:            "The user changed this task's goal. Output for the previous goal was not applied or delivered. Stop the old approach, read the complete current task, and continue on this same task.",
			SourceMessageID: message.ID, GoalRevision: message.TaskGoalRevision,
			CandidateRevision: message.TaskCandidateRevision, CreatedAt: updatedAt,
		}); err != nil {
			return Message{}, fmt.Errorf("enqueue goal revision notice: %w", err)
		}
	}
	if params.State == TaskStateChecking && message.TaskState != string(TaskStateChecking) {
		return Message{}, fmt.Errorf("%w: checking is entered only by canonical candidate promotion", ErrConflict)
	}
	if ownerChanged {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_verifications WHERE task_id = ?`, message.ID); err != nil {
			return Message{}, fmt.Errorf("invalidate reassigned task verification: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_messages SET invalidated_at = ?
			WHERE work_id = ? AND to_agent_id = ? AND pulled_at IS NULL AND invalidated_at IS NULL`,
			toMillis(updatedAt), message.ID, oldOwner); err != nil {
			return Message{}, fmt.Errorf("invalidate previous owner work deliveries: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE work_runs
			SET state = 'interrupted', outcome = 'owner reassigned', ended_at = ?, updated_at = ?
			WHERE work_id = ? AND state IN ('queued', 'running') AND (
				named_agent_id = ? OR id IN (
					SELECT run_id FROM collaboration_session_bindings
					WHERE work_id = ? AND principal_id = ? AND run_id IS NOT NULL
				)
			)`, toMillis(updatedAt), toMillis(updatedAt), message.ID,
			oldOwner, message.ID, oldOwner); err != nil {
			return Message{}, fmt.Errorf("interrupt previous owner work runs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_session_bindings
			SET state = 'interrupted', run_id = NULL, updated_at = ?
			WHERE work_id = ? AND principal_id = ? AND state != 'missing'`,
			toMillis(updatedAt), message.ID, oldOwner); err != nil {
			return Message{}, fmt.Errorf("interrupt previous owner work sessions: %w", err)
		}
		if err := refreshWorkCurrentRunRefTx(ctx, tx, message.ID, updatedAt); err != nil {
			return Message{}, fmt.Errorf("settle reassigned work run handle: %w", err)
		}
		assignmentFromType := MemberAgent
		assignmentFromID := params.AgentID
		if params.HumanID != "" {
			assignmentFromType = MemberHuman
			assignmentFromID = params.HumanID
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: message.RoomID, FromType: assignmentFromType, FromID: assignmentFromID, FromSessionRef: params.SessionRef,
			ToAgentID: newOwner, WorkID: message.ID, Kind: CollaborationAssignment,
			Body: message.Body, SourceMessageID: message.ID,
			GoalRevision: message.TaskGoalRevision, CandidateRevision: message.TaskCandidateRevision,
			CreatedAt: updatedAt,
		}); err != nil {
			return Message{}, fmt.Errorf("enqueue reassigned work assignment: %w", err)
		}
		setState = string(TaskStateOpen)
	}
	if message.TaskVerificationRequired && setState == string(TaskStateDone) {
		if message.TaskState != string(TaskStateChecking) {
			return Message{}, fmt.Errorf("%w: verified task completion must follow checking", ErrConflict)
		}
		var decision VerificationDecision
		var goalRevision, candidateRevision int
		if err := tx.QueryRowContext(ctx, `
			SELECT decision, goal_revision, candidate_revision
			FROM task_verifications WHERE task_id = ?`, message.ID).Scan(&decision, &goalRevision, &candidateRevision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Message{}, fmt.Errorf("%w: task requires passing verification before completion", ErrConflict)
			}
			return Message{}, fmt.Errorf("read task verification before completion: %w", err)
		}
		if decision != VerificationPass {
			return Message{}, fmt.Errorf("%w: task verification is %s, not pass", ErrConflict, decision)
		}
		if goalRevision != message.TaskGoalRevision || candidateRevision != message.TaskCandidateRevision {
			return Message{}, fmt.Errorf("%w: task verification is stale", ErrConflict)
		}
		var ownerDeliveries int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM room_messages delivery
			JOIN task_verifications verification ON verification.task_id = ?
			WHERE delivery.room_id = ? AND delivery.thread_id = ?
				AND delivery.author_type = 'agent' AND delivery.author_id = ?
				AND delivery.kind = 'text' AND delivery.created_at >= verification.updated_at`,
			message.ID, message.RoomID, message.ID, message.TaskOwner).Scan(&ownerDeliveries); err != nil {
			return Message{}, fmt.Errorf("check verified task delivery: %w", err)
		}
		if ownerDeliveries == 0 {
			return Message{}, fmt.Errorf("%w: verified task completion requires the owner to deliver the result", ErrConflict)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE room_messages
		SET body = ?, task_state = ?, task_owner = ?, task_goal_revision = ?, task_candidate_revision = ?
		WHERE id = ?`,
		message.Body, setState, newOwner, message.TaskGoalRevision, message.TaskCandidateRevision, message.ID); err != nil {
		return Message{}, fmt.Errorf("update task message: %w", err)
	}
	message.TaskState = setState
	message.TaskOwner = newOwner
	if err := syncWorkFromTaskTx(ctx, tx, message, updatedAt); err != nil {
		return Message{}, err
	}
	if message.TaskState == string(TaskStateDone) {
		if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET pulled_at = COALESCE(pulled_at, ?) WHERE member_type = 'agent' AND message_id = ? AND kind = 'task'`, toMillis(updatedAt), message.ID); err != nil {
			return Message{}, err
		}
	} else if err := s.taskEventInboxTx(ctx, tx, message, newOwner, oldOwner); err != nil {
		return Message{}, err
	}
	if ownerChanged {
		if _, err := tx.ExecContext(ctx, `
			UPDATE inbox_items SET pulled_at = ?
			WHERE member_type = 'agent' AND member_id = ? AND message_id = ?
				AND kind = 'task' AND pulled_at IS NULL`,
			toMillis(updatedAt), oldOwner, message.ID); err != nil {
			return Message{}, fmt.Errorf("settle previous owner task inbox: %w", err)
		}
		if err := recomputeAgentWakeTx(ctx, tx, oldOwner, toMillis(updatedAt)); err != nil {
			return Message{}, err
		}
	}
	requested := false
	if message.TaskState != string(TaskStateDone) {
		requested, err = requestWakeTx(ctx, tx, newOwner, toMillis(updatedAt))
	} else {
		err = recomputeAgentWakeTx(ctx, tx, newOwner, toMillis(updatedAt))
	}
	if err != nil {
		return Message{}, err
	}
	var terminalWakeIDs []string
	for _, run := range interruptedRuns {
		run.State, run.EndedAt = WorkRunInterrupted, updatedAt
		if params.GoalCorrection != "" {
			run.Outcome = "goal revised"
		} else {
			run.Outcome = "owner reassigned"
		}
		ids, err := s.enqueueWorkRunTerminalTx(ctx, tx, terminalWork, run, updatedAt)
		if err != nil {
			return Message{}, err
		}
		terminalWakeIDs = appendUniqueStrings(terminalWakeIDs, ids...)
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, updatedAt)
	if err != nil {
		return Message{}, err
	}
	terminalWakeIDs = appendUniqueStrings(terminalWakeIDs, admittedWakeIDs...)
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit task update: %w", err)
	}
	s.interruptWorkSessions(interruptTargets)
	if requested && s.wake != nil {
		s.wake.Deliver(newOwner)
	}
	if s.wake != nil {
		for _, id := range terminalWakeIDs {
			if !requested || id != newOwner {
				s.wake.Deliver(id)
			}
		}
	}
	if work, err := s.GetWork(ctx, message.ID); err == nil {
		message.Work = &work
	}
	return message, nil
}

func (s *Service) ListTasks(ctx context.Context, params TaskListParams) ([]Message, error) {
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.OwnerID = strings.TrimSpace(params.OwnerID)
	if params.AgentID != "" {
		if _, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token); err != nil {
			return nil, err
		}
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, fmt.Errorf("begin task list access check: %w", err)
		}
		if err := requireRoomPrincipalAccessTx(ctx, tx, params.RoomID, params.AgentID); err != nil {
			tx.Rollback()
			return nil, err
		}
		_ = tx.Rollback()
	} else if params.HumanID != "" {
		if err := s.requireHumanMember(ctx, params.RoomID, params.HumanID); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("task list caller is required")
	}
	query := `
		SELECT id, room_id, seq, COALESCE(thread_id, ''), author_type, author_id,
			kind, body, images_json, files_json, mentions_json, COALESCE(reply_to, ''),
			COALESCE(task_title, ''), COALESCE(task_state, ''), COALESCE(task_owner, ''),
			task_verification_required, task_goal_revision, task_candidate_revision, created_at
		FROM room_messages WHERE room_id = ? AND kind = 'task'`
	args := []any{params.RoomID}
	if params.OwnerID != "" {
		query += ` AND task_owner = ?`
		args = append(args, params.OwnerID)
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
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
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close tasks: %w", err)
	}
	if err := s.attachWorkDetails(ctx, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *Service) insertTaskMessageTx(ctx context.Context, tx *sql.Tx, params TaskCreateParams) (Message, error) {
	var seq int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, params.RoomID,
	).Scan(&seq); err != nil {
		return Message{}, fmt.Errorf("allocate task sequence: %w", err)
	}
	id, err := randomID("msg", 12)
	if err != nil {
		return Message{}, err
	}
	now := fromMillis(toMillis(s.now()))
	message := Message{
		ID:                       id,
		RoomID:                   params.RoomID,
		Seq:                      seq,
		ThreadID:                 params.ThreadID,
		AuthorType:               MemberAgent,
		AuthorID:                 params.AgentID,
		Kind:                     MessageTask,
		Body:                     params.Body,
		TaskTitle:                params.Title,
		TaskState:                string(TaskStateOpen),
		TaskOwner:                params.OwnerID,
		TaskVerificationRequired: params.VerificationRequired,
		TaskGoalRevision:         1,
		CreatedAt:                now,
	}
	if params.AgentID == "" && params.HumanID != "" {
		message.AuthorType = MemberHuman
		message.AuthorID = params.HumanID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (
			id, room_id, seq, thread_id, author_type, author_id, kind, body,
			mentions_json, reply_to, task_title, task_state, task_owner,
			task_verification_required, task_goal_revision, task_candidate_revision, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID, message.RoomID, message.Seq, nullableString(message.ThreadID),
		message.AuthorType, message.AuthorID, message.Kind, message.Body,
		"[]", nullableString(message.ReplyTo), nullableString(message.TaskTitle),
		nullableString(message.TaskState), nullableString(message.TaskOwner), boolInt(message.TaskVerificationRequired),
		message.TaskGoalRevision, message.TaskCandidateRevision, toMillis(message.CreatedAt)); err != nil {
		return Message{}, fmt.Errorf("insert task message: %w", err)
	}
	return message, nil
}

func (s *Service) taskEventInboxTx(ctx context.Context, tx *sql.Tx, message Message, newOwner, oldOwner string) error {
	threadRoot := message.ThreadID
	if threadRoot == "" {
		threadRoot = message.ID
	}
	now := toMillis(message.CreatedAt)
	if newOwner != "" {
		if err := insertInboxTx(ctx, tx, MemberAgent, newOwner, message.RoomID, message.ID, InboxTask, now); err != nil {
			return err
		}
	}
	if oldOwner != "" && oldOwner != newOwner {
		if err := insertInboxTx(ctx, tx, MemberAgent, oldOwner, message.RoomID, message.ID, InboxTask, now); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT message.author_id
		FROM room_messages message
		JOIN room_members member ON member.room_id = message.room_id
			AND member.member_type = 'agent' AND member.member_id = message.author_id
		WHERE message.room_id = ? AND message.author_type = 'agent' AND (message.id = ? OR message.thread_id = ?)`,
		message.RoomID, threadRoot, threadRoot)
	if err != nil {
		return fmt.Errorf("list task thread participants: %w", err)
	}
	var participants []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return fmt.Errorf("scan task thread participant: %w", err)
		}
		participants = append(participants, agentID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close task thread participants: %w", err)
	}
	sort.Strings(participants)
	for _, agentID := range participants {
		if agentID == newOwner || agentID == oldOwner {
			continue
		}
		if err := insertInboxTx(ctx, tx, MemberAgent, agentID, message.RoomID, message.ID, InboxTask, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requireRoomAgentMemberTx(ctx context.Context, tx *sql.Tx, roomID, agentID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`,
		roomID, agentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: agent %q is not a member of room %q", ErrUnauthorized, agentID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate agent room membership: %w", err)
	}
	return nil
}

func (s *Service) requireAgentMember(ctx context.Context, roomID, agentID string) error {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`,
		roomID, agentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: agent %q is not a member of room %q", ErrUnauthorized, agentID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate agent room membership: %w", err)
	}
	return nil
}

func (s *Service) requireHumanMember(ctx context.Context, roomID, humanID string) error {
	if roomID == "" || humanID == "" {
		return errors.New("room and human id are required")
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM room_members WHERE room_id = ? AND member_type = 'human' AND member_id = ?`,
		roomID, humanID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: human %q is not a member of room %q", ErrUnauthorized, humanID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate human room membership: %w", err)
	}
	return nil
}
