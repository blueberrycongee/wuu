package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func insertWorkTx(ctx context.Context, tx *sql.Tx, task Message, params TaskCreateParams) error {
	sourceMessageID := strings.TrimSpace(params.SourceMessageID)
	if sourceMessageID == "" {
		sourceMessageID = strings.TrimSpace(params.ThreadID)
	}
	if sourceMessageID == "" {
		// A manually created root task has no preceding user message. Keeping the
		// task projection as its own debt anchor is explicit and recoverable.
		sourceMessageID = task.ID
	} else {
		source, err := loadMessageTx(ctx, tx, sourceMessageID)
		if err != nil {
			return fmt.Errorf("load work source message: %w", err)
		}
		if source.RoomID != task.RoomID {
			return errors.New("work source message belongs to another room")
		}
	}
	verification := WorkVerificationNotRequired
	if task.TaskVerificationRequired {
		verification = WorkVerificationPending
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO works(
			id, room_id, source_message_id, owner_named_agent_id, lead_named_agent_id,
			title, brief, goal_revision, candidate_revision, state,
			verification_state, verification_required, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, ?)`,
		task.ID, task.RoomID, sourceMessageID, task.TaskOwner, nullableString(params.LeadNamedAgentID),
		task.TaskTitle, task.Body, task.TaskGoalRevision, task.TaskCandidateRevision,
		verification, boolInt(task.TaskVerificationRequired), toMillis(task.CreatedAt), toMillis(task.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert durable work: %w", err)
	}
	if err := insertWorkEventTx(ctx, tx, WorkEvent{
		WorkID: task.ID, Kind: "state", State: string(WorkOpen), Summary: "Work created",
		GoalRevision: task.TaskGoalRevision, CandidateRevision: task.TaskCandidateRevision, CreatedAt: task.CreatedAt,
	}); err != nil {
		return err
	}
	return nil
}

func syncWorkFromTaskTx(ctx context.Context, tx *sql.Tx, task Message, updatedAt time.Time) error {
	state := workStateFromTask(TaskState(task.TaskState))
	var previousState WorkState
	_ = tx.QueryRowContext(ctx, `SELECT state FROM works WHERE id = ?`, task.ID).Scan(&previousState)
	verification := WorkVerificationNotRequired
	if task.TaskVerificationRequired {
		verification = WorkVerificationPending
		var decision VerificationDecision
		var goalRevision, candidateRevision int
		if err := tx.QueryRowContext(ctx, `
			SELECT decision, goal_revision, candidate_revision FROM task_verifications WHERE task_id = ?`, task.ID,
		).Scan(&decision, &goalRevision, &candidateRevision); err == nil &&
			goalRevision == task.TaskGoalRevision && candidateRevision == task.TaskCandidateRevision {
			verification = WorkVerificationState(decision)
		}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE works SET owner_named_agent_id = ?, brief = ?, goal_revision = ?, candidate_revision = ?,
			state = ?, verification_state = ?, updated_at = ? WHERE id = ?`,
		task.TaskOwner, task.Body, task.TaskGoalRevision, task.TaskCandidateRevision,
		state, verification, toMillis(updatedAt), task.ID)
	if err != nil {
		return fmt.Errorf("sync durable work projection: %w", err)
	}
	if state == WorkCompleted || state == WorkFailed || state == WorkCancelled {
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_messages SET invalidated_at = ?
			WHERE work_id = ? AND pulled_at IS NULL AND invalidated_at IS NULL`,
			toMillis(updatedAt), task.ID); err != nil {
			return fmt.Errorf("invalidate terminal work deliveries: %w", err)
		}
	}
	if previousState != state {
		if err := insertWorkEventTx(ctx, tx, WorkEvent{
			WorkID: task.ID, Kind: "state", State: string(state), Summary: "Work state changed",
			GoalRevision: task.TaskGoalRevision, CandidateRevision: task.TaskCandidateRevision, CreatedAt: updatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func insertWorkEventTx(ctx context.Context, tx *sql.Tx, event WorkEvent) error {
	if event.ID == "" {
		id, err := randomID("workevent", 12)
		if err != nil {
			return err
		}
		event.ID = id
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO work_events(id, work_id, kind, state, summary, goal_revision, candidate_revision, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.WorkID, event.Kind, event.State,
		event.Summary, event.GoalRevision, event.CandidateRevision, toMillis(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert work event: %w", err)
	}
	return nil
}

func workStateFromTask(state TaskState) WorkState {
	switch state {
	case TaskStateDoing:
		return WorkWorking
	case TaskStateChecking:
		return WorkChecking
	case TaskStateRevising:
		return WorkRevising
	case TaskStateNeedsHuman:
		return WorkNeedsHuman
	case TaskStateDone:
		return WorkCompleted
	default:
		return WorkOpen
	}
}

func terminalWorkState(state WorkState) bool {
	return state == WorkCompleted || state == WorkFailed || state == WorkCancelled
}

func (s *Service) GetWork(ctx context.Context, workID string) (Work, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return Work{}, errors.New("work id is required")
	}
	work, err := scanWork(s.db.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, workID))
	if err != nil {
		return Work{}, err
	}
	if err := s.loadWorkDetails(ctx, &work); err != nil {
		return Work{}, err
	}
	return work, nil
}

func (s *Service) ListWorks(ctx context.Context, roomID, agentID, token string) ([]Work, error) {
	if _, err := s.AuthenticatePrincipal(ctx, agentID, token); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if err := requireRoomPrincipalAccessTx(ctx, tx, roomID, agentID); err != nil {
		tx.Rollback()
		return nil, err
	}
	_ = tx.Rollback()
	rows, err := s.db.QueryContext(ctx, workSelect+` WHERE work.room_id = ? ORDER BY work.created_at, work.id`, roomID)
	if err != nil {
		return nil, fmt.Errorf("list works: %w", err)
	}
	defer rows.Close()
	var works []Work
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	return works, rows.Err()
}

const workSelect = `
	SELECT work.id, work.room_id, work.source_message_id, work.owner_named_agent_id,
		COALESCE(work.lead_named_agent_id, ''), work.title, work.brief,
		work.goal_revision, work.candidate_revision, work.state,
		COALESCE(work.current_run_ref, ''), COALESCE(work.candidate_artifact_ref, ''),
		COALESCE(work.candidate_workspace_revision, ''), work.promotion_run_ref, work.selection_reason,
		work.promotion_request_id, work.verification_state,
		work.verification_required, work.max_verifier_attempts, work.max_candidates,
		work.verifier_attempts_used, work.candidates_used, work.fanout_reason,
		work.max_rounds, work.current_round, work.qualified_candidates,
		work.max_input_tokens, work.max_output_tokens, work.deadline_at,
		work.checks_summary, work.changed_files_count, work.unresolved_items, work.failure_reason,
		work.cancelled_at, work.created_at, work.updated_at
	FROM works work`

func scanWork(row scanner) (Work, error) {
	var work Work
	var verificationRequired int
	var cancelledAt, deadlineAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&work.ID, &work.RoomID, &work.SourceMessageID, &work.OwnerNamedAgentID,
		&work.LeadNamedAgentID, &work.Title, &work.Brief, &work.GoalRevision,
		&work.CandidateRevision, &work.State, &work.CurrentRunRef, &work.CandidateArtifactRef,
		&work.CandidateWorkspaceRevision, &work.PromotionRunRef, &work.SelectionReason,
		&work.PromotionRequestID, &work.VerificationState, &verificationRequired,
		&work.MaxVerifierAttempts, &work.MaxCandidates, &work.VerifierAttemptsUsed,
		&work.CandidatesUsed, &work.FanoutReason, &work.MaxRounds, &work.CurrentRound,
		&work.QualifiedCandidates, &work.MaxInputTokens, &work.MaxOutputTokens, &deadlineAt,
		&work.ChecksSummary, &work.ChangedFilesCount, &work.UnresolvedItems, &work.FailureReason,
		&cancelledAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Work{}, ErrNotFound
		}
		return Work{}, fmt.Errorf("scan work: %w", err)
	}
	work.VerificationRequired = verificationRequired != 0
	if cancelledAt.Valid {
		work.CancelledAt = fromMillis(cancelledAt.Int64)
	}
	if deadlineAt.Valid {
		work.DeadlineAt = fromMillis(deadlineAt.Int64)
	}
	work.CreatedAt, work.UpdatedAt = fromMillis(createdAt), fromMillis(updatedAt)
	return work, nil
}

func (s *Service) loadWorkDetails(ctx context.Context, work *Work) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM collaboration_messages
		WHERE work_id = ? AND pulled_at IS NULL AND invalidated_at IS NULL
		ORDER BY created_at, id`, work.ID)
	if err != nil {
		return fmt.Errorf("list pending work deliveries: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		work.PendingDeliveryRefs = append(work.PendingDeliveryRefs, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	work.Deliveries, err = s.listWorkDeliveries(ctx, work.ID)
	if err != nil {
		return err
	}
	work.Runs, err = s.listWorkRuns(ctx, work.ID)
	if err != nil {
		return err
	}
	for _, run := range work.Runs {
		work.TotalCostUSD += run.CostUSD
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT run.id) FROM work_runs run
		WHERE run.work_id = ? AND run.goal_revision = ?
			AND run.state = 'completed' AND run.qualified = 1
			AND EXISTS (SELECT 1 FROM work_artifacts artifact WHERE artifact.run_id = run.id AND artifact.kind = 'candidate')`,
		work.ID, work.GoalRevision).Scan(&work.QualifiedCandidates); err != nil {
		return fmt.Errorf("count current qualified candidates: %w", err)
	}
	work.Artifacts, err = s.listWorkArtifacts(ctx, work.ID)
	if err != nil {
		return err
	}
	work.Events, err = s.listWorkEvents(ctx, work.ID)
	if err != nil {
		return err
	}
	if verification, err := s.GetTaskVerification(ctx, work.ID); err == nil {
		work.Verification = &verification
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	work.OwnerCapacity, err = s.NamedAgentCapacity(ctx, work.OwnerNamedAgentID)
	if err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM work_runs run JOIN works work ON work.id = run.work_id WHERE work.room_id = ? AND run.state = 'running' AND run.turn_id != ''),
			(SELECT COUNT(*) FROM work_runs run JOIN works work ON work.id = run.work_id WHERE work.room_id = ? AND run.state = 'running' AND run.turn_id = ''),
			(SELECT COUNT(*) FROM work_runs run JOIN works work ON work.id = run.work_id WHERE work.room_id = ? AND run.state = 'queued'),
			(SELECT COUNT(*) FROM work_runs WHERE state = 'running' AND turn_id != ''),
			(SELECT COUNT(*) FROM work_runs WHERE state = 'running' AND turn_id = ''),
			(SELECT COUNT(*) FROM work_runs WHERE state = 'queued')`,
		work.RoomID, work.RoomID, work.RoomID).Scan(
		&work.RoomCapacity.Active, &work.RoomCapacity.Starting, &work.RoomCapacity.Queued,
		&work.GlobalCapacity.Active, &work.GlobalCapacity.Starting, &work.GlobalCapacity.Queued,
	); err != nil {
		return fmt.Errorf("read work capacity summaries: %w", err)
	}
	work.RoomCapacity.RoomID, work.RoomCapacity.Limit = work.RoomID, s.roomRunLimit
	work.GlobalCapacity.Limit = s.globalRunLimit
	return nil
}

func (s *Service) listWorkDeliveries(ctx context.Context, workID string) ([]CollaborationMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT delivery.id, delivery.room_id, delivery.from_type,
			CASE WHEN sender.kind = 'named_agent' THEN delivery.from_id ELSE '' END,
			COALESCE(delivery.from_session_ref, ''),
			delivery.to_agent_id,
			COALESCE(delivery.target_session_ref, ''),
			CASE WHEN principal.kind = 'named_agent' THEN delivery.to_agent_id ELSE '' END,
			delivery.kind, delivery.body, COALESCE(delivery.work_id, ''),
			COALESCE(delivery.source_message_id, ''), delivery.goal_revision, delivery.candidate_revision,
			delivery.artifact_refs_json, COALESCE(delivery.reply_to, ''), delivery.target_kind,
			delivery.target_id, delivery.visibility, delivery.correlation_id, delivery.request_id,
			delivery.terminal_state, delivery.created_at,
			COALESCE(delivery.consumed_at, 0), COALESCE(delivery.invalidated_at, 0)
		FROM collaboration_messages delivery
		JOIN collaboration_principals principal ON principal.id = delivery.to_agent_id
		LEFT JOIN collaboration_principals sender ON sender.id = delivery.from_id
		WHERE delivery.work_id = ?
		ORDER BY delivery.created_at, delivery.rowid`, workID)
	if err != nil {
		return nil, fmt.Errorf("list work collaboration messages: %w", err)
	}
	defer rows.Close()
	var messages []CollaborationMessage
	for rows.Next() {
		var message CollaborationMessage
		var artifactRefsJSON string
		var createdAt, consumedAt, invalidatedAt int64
		if err := rows.Scan(
			&message.ID, &message.RoomID, &message.FromType, &message.FromID, &message.FromSessionRef, &message.ToAgentID,
			&message.TargetSessionRef,
			&message.RecipientNamedAgentID, &message.Kind, &message.Body, &message.WorkID,
			&message.SourceMessageID, &message.GoalRevision, &message.CandidateRevision,
			&artifactRefsJSON, &message.ReplyTo, &message.TargetKind, &message.TargetID,
			&message.Visibility, &message.CorrelationID, &message.RequestID, &message.TerminalState,
			&createdAt, &consumedAt, &invalidatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan work collaboration message: %w", err)
		}
		message.CreatedAt = fromMillis(createdAt)
		if message.RecipientNamedAgentID == "" {
			message.ToAgentID = ""
		}
		if consumedAt != 0 {
			message.ConsumedAt = fromMillis(consumedAt)
		}
		if invalidatedAt != 0 {
			message.InvalidatedAt = fromMillis(invalidatedAt)
		}
		if err := json.Unmarshal([]byte(artifactRefsJSON), &message.ArtifactRefs); err != nil {
			return nil, fmt.Errorf("decode work collaboration artifacts: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work collaboration messages: %w", err)
	}
	return messages, nil
}

func (s *Service) StartWorkRun(ctx context.Context, params WorkRunStartParams) (WorkRun, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return WorkRun{}, err
	}
	params.WorkID, params.SessionRef = strings.TrimSpace(params.WorkID), strings.TrimSpace(params.SessionRef)
	params.NamedAgentID = strings.TrimSpace(params.NamedAgentID)
	params.Profile = strings.TrimSpace(params.Profile)
	params.RequestID = strings.TrimSpace(params.RequestID)
	if params.WorkID == "" || !validWorkRunKind(params.Kind) {
		return WorkRun{}, errors.New("work run requires a work and valid kind")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkRun{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return WorkRun{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return WorkRun{}, err
	}
	if params.RequestID != "" {
		existing, findErr := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.work_id = ? AND run.request_id = ?`, work.ID, params.RequestID))
		if findErr == nil {
			if existing.Kind != params.Kind {
				return WorkRun{}, fmt.Errorf("%w: start request id was used for another run kind", ErrConflict)
			}
			return existing, nil
		}
		if !errors.Is(findErr, ErrNotFound) {
			return WorkRun{}, findErr
		}
	}
	namedAgentID := params.NamedAgentID
	if !actor.IsRoomRuntime() {
		if namedAgentID != "" && namedAgentID != actor.ID {
			return WorkRun{}, fmt.Errorf("%w: named agents may only start their own sessions", ErrUnauthorized)
		}
		namedAgentID = actor.ID
	} else if params.Kind == WorkRunVerifier && params.Profile != "" && params.Profile != WorkVerifierProfileIndependent {
		if namedAgentID != "" && namedAgentID != params.Profile {
			return WorkRun{}, fmt.Errorf("%w: verifier profile and named session owner must match", ErrConflict)
		}
		namedAgentID = params.Profile
	}
	if namedAgentID != "" {
		if err := s.requireRoomAgentMemberTx(ctx, tx, work.RoomID, namedAgentID); err != nil {
			return WorkRun{}, err
		}
	}
	if (params.Kind == WorkRunSelector || params.Kind == WorkRunIntegration) && namedAgentID == "" {
		return WorkRun{}, fmt.Errorf("%w: selector and integration runs require a visible named agent", ErrUnauthorized)
	}
	freshNamedSession := false
	if params.SessionRef == "" && namedAgentID != "" {
		params.SessionRef, err = randomID("collab-session", 12)
		if err != nil {
			return WorkRun{}, err
		}
		freshNamedSession = true
	}
	allowFreshNamedSession := freshNamedSession ||
		(actor.IsRoomRuntime() && params.Kind == WorkRunVerifier && namedAgentID != "")
	bindRunSession := params.SessionRef != "" && namedAgentID != ""
	if bindRunSession && (actor.IsRoomRuntime() || params.Kind != WorkRunVerifier) {
		if err := validateCollaborationSessionRouteTx(ctx, tx, params.SessionRef, namedAgentID, work.RoomID, work.ID); err != nil {
			if allowFreshNamedSession && errors.Is(err, ErrNotFound) {
				// The binding is inserted atomically with the run below.
			} else {
				// A fresh unscoped conversation binding can be promoted to this run.
				binding, bindErr := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
				if bindErr != nil || binding.PrincipalID != namedAgentID || binding.RoomID != work.RoomID || binding.WorkID != "" || binding.RunID != "" || binding.State == CollaborationSessionRunning || binding.State == CollaborationSessionMissing {
					if bindErr != nil {
						return WorkRun{}, fmt.Errorf("validate work session: %w", bindErr)
					}
					return WorkRun{}, fmt.Errorf("%w: work session is already scoped or unavailable", ErrConflict)
				}
			}
		}
	}
	if work.State == WorkCancelled || work.State == WorkCompleted || work.State == WorkFailed {
		return WorkRun{}, fmt.Errorf("%w: work is %s", ErrConflict, work.State)
	}
	if params.Kind == WorkRunVerifier {
		if !work.VerificationRequired || work.VerifierAttemptsUsed >= work.MaxVerifierAttempts {
			return WorkRun{}, fmt.Errorf("%w: verifier budget is unavailable", ErrConflict)
		}
		if work.State != WorkChecking || work.CandidateRevision == 0 || work.CandidateArtifactRef == "" {
			return WorkRun{}, fmt.Errorf("%w: verifier run requires a checking candidate", ErrConflict)
		}
		if !actor.IsRoomRuntime() || actor.RoomID != work.RoomID {
			return WorkRun{}, fmt.Errorf("%w: only the room runtime may start verification", ErrUnauthorized)
		}
		verifierID := strings.TrimSpace(params.Profile)
		if verifierID == "" {
			verifierID = WorkVerifierProfileIndependent
			params.Profile = verifierID
		}
		if namedAgentID != "" && verifierID != namedAgentID {
			return WorkRun{}, fmt.Errorf("%w: verifier profile and named session owner must match", ErrConflict)
		}
		if verifierID == work.OwnerNamedAgentID {
			return WorkRun{}, fmt.Errorf("%w: verifier run requires a different named agent", ErrConflict)
		}
		if verifierID != WorkVerifierProfileIndependent {
			var verifierMember int
			if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM room_members member
			JOIN named_agents agent ON agent.id = member.member_id AND agent.kind = 'named'
			WHERE member.room_id = ? AND member.member_type = 'agent' AND member.member_id = ?`,
				work.RoomID, verifierID).Scan(&verifierMember); err != nil {
				return WorkRun{}, fmt.Errorf("validate named verifier: %w", err)
			}
			if verifierMember == 0 {
				return WorkRun{}, fmt.Errorf("%w: named verifier must be a current room member", ErrUnauthorized)
			}
		}
		if work.CurrentRunRef != "" {
			var state WorkRunState
			if err := tx.QueryRowContext(ctx, `SELECT state FROM work_runs WHERE id = ?`, work.CurrentRunRef).Scan(&state); err == nil && state == WorkRunRunning {
				return WorkRun{}, fmt.Errorf("%w: verifier run %q is already active", ErrConflict, work.CurrentRunRef)
			}
		}
	}
	if params.Kind == WorkRunSelector || params.Kind == WorkRunIntegration {
		var qualifiedCandidates int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT artifact.id) FROM work_artifacts artifact
			JOIN work_runs run ON run.id = artifact.run_id
			WHERE artifact.work_id = ? AND artifact.kind = 'candidate'
				AND run.goal_revision = ? AND run.candidate_revision = ?
				AND run.state = 'completed' AND run.qualified = 1`,
			work.ID, work.GoalRevision, work.CandidateRevision).Scan(&qualifiedCandidates); err != nil {
			return WorkRun{}, fmt.Errorf("count qualified fan-in candidates: %w", err)
		}
		if work.MaxCandidates < 2 || qualifiedCandidates < 2 || strings.TrimSpace(work.FanoutReason) == "" {
			return WorkRun{}, fmt.Errorf("%w: selector or integration requires two qualified candidates and an explicit fan-out policy", ErrConflict)
		}
	}
	if params.Kind == WorkRunSelector || params.Kind == WorkRunIntegration {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_runs WHERE work_id = ? AND goal_revision = ? AND kind IN ('selector', 'integration') AND state IN ('queued', 'running')`, work.ID, work.GoalRevision).Scan(&active); err != nil {
			return WorkRun{}, fmt.Errorf("count active fan-in runs: %w", err)
		}
		if active != 0 {
			return WorkRun{}, fmt.Errorf("%w: work already has an active selector or integration run", ErrConflict)
		}
	}
	if params.Round <= 0 {
		params.Round = work.CurrentRound
	}
	if params.Round <= 0 {
		params.Round = 1
	}
	if work.MaxRounds > 0 && params.Round > work.MaxRounds {
		return WorkRun{}, fmt.Errorf("%w: work round budget exhausted", ErrConflict)
	}
	if !work.DeadlineAt.IsZero() && !s.now().Before(work.DeadlineAt) {
		return WorkRun{}, fmt.Errorf("%w: work deadline expired", ErrConflict)
	}
	var usedInputTokens, usedOutputTokens int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM work_runs WHERE work_id = ?`, work.ID).Scan(&usedInputTokens, &usedOutputTokens); err != nil {
		return WorkRun{}, fmt.Errorf("read work usage budget: %w", err)
	}
	if work.MaxInputTokens > 0 && usedInputTokens >= work.MaxInputTokens || work.MaxOutputTokens > 0 && usedOutputTokens >= work.MaxOutputTokens {
		return WorkRun{}, fmt.Errorf("%w: work token budget exhausted", ErrConflict)
	}
	var roomInputTokens, roomOutputTokens int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(run.input_tokens), 0), COALESCE(SUM(run.output_tokens), 0)
		FROM work_runs run JOIN works room_work ON room_work.id = run.work_id
		WHERE room_work.room_id = ?`, work.RoomID).Scan(&roomInputTokens, &roomOutputTokens); err != nil {
		return WorkRun{}, fmt.Errorf("read room usage budget: %w", err)
	}
	if s.roomInputTokenLimit > 0 && roomInputTokens >= s.roomInputTokenLimit || s.roomOutputTokenLimit > 0 && roomOutputTokens >= s.roomOutputTokenLimit {
		return WorkRun{}, fmt.Errorf("%w: room token budget exhausted", ErrConflict)
	}
	id, err := randomID("workrun", 12)
	if err != nil {
		return WorkRun{}, err
	}
	now := fromMillis(toMillis(s.now()))
	deadline := now.Add(30 * time.Minute)
	if params.Deadline > 0 {
		deadline = now.Add(params.Deadline)
	}
	if !work.DeadlineAt.IsZero() && work.DeadlineAt.Before(deadline) {
		deadline = work.DeadlineAt
	}
	state, queueReason, err := s.workRunAdmissionTx(ctx, tx, work.RoomID, namedAgentID, params.SessionRef)
	if err != nil {
		return WorkRun{}, err
	}
	run := WorkRun{
		ID: id, WorkID: work.ID, NamedAgentID: namedAgentID, Kind: params.Kind, Profile: params.Profile,
		SessionRef: params.SessionRef, State: state, GoalRevision: work.GoalRevision,
		CandidateRevision: work.CandidateRevision, WorkspaceRevision: strings.TrimSpace(params.WorkspaceRevision),
		RequestID: params.RequestID, Round: params.Round, DeadlineAt: deadline, QueueReason: queueReason,
		CreatedAt: now, UpdatedAt: now,
	}
	if run.State == WorkRunRunning {
		run.StartedAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_runs(id, work_id, named_agent_id, kind, profile, session_ref, state, goal_revision,
			candidate_revision, workspace_revision, request_id, round, deadline_at, queue_reason, started_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkID, nullableString(run.NamedAgentID), run.Kind, run.Profile, nullableString(run.SessionRef), run.State,
		run.GoalRevision, run.CandidateRevision, run.WorkspaceRevision, run.RequestID, run.Round, toMillis(run.DeadlineAt), run.QueueReason,
		nullableTimeMillis(run.StartedAt), toMillis(now), toMillis(now)); err != nil {
		return WorkRun{}, fmt.Errorf("insert work run: %w", err)
	}
	verifierIncrement := 0
	if run.Kind == WorkRunVerifier {
		verifierIncrement = 1
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET current_run_ref = ?, verifier_attempts_used = verifier_attempts_used + ?,
			current_round = MAX(current_round, ?), updated_at = ? WHERE id = ?`,
		run.ID, verifierIncrement, run.Round, toMillis(now), work.ID); err != nil {
		return WorkRun{}, fmt.Errorf("activate work run: %w", err)
	}
	if bindRunSession {
		purpose := CollaborationSessionWork
		if run.Kind == WorkRunVerifier {
			purpose = CollaborationSessionVerification
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO collaboration_session_bindings(
				session_ref, principal_id, named_agent_id, room_id, work_id, run_id,
				purpose, state, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_ref) DO UPDATE SET
				room_id = excluded.room_id, work_id = excluded.work_id, run_id = excluded.run_id,
				purpose = excluded.purpose, state = excluded.state, updated_at = excluded.updated_at`,
			run.SessionRef, namedAgentID, namedAgentID, work.RoomID, work.ID, run.ID,
			purpose, collaborationSessionStateForRun(run.State), toMillis(now), toMillis(now)); err != nil {
			return WorkRun{}, fmt.Errorf("bind work run session: %w", err)
		}
	}
	shouldWakeNamedAgent := false
	if actor.IsRoomRuntime() && run.State == WorkRunRunning && run.NamedAgentID != "" && run.Kind != WorkRunVerifier {
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: work.RoomID, ToAgentID: run.NamedAgentID, TargetKind: CollaborationTargetSession,
			TargetID: run.SessionRef, TargetSessionRef: run.SessionRef, Visibility: CollaborationVisibilitySystem,
			Kind: CollaborationControl, Body: "Work Run admitted", WorkID: work.ID,
			GoalRevision: run.GoalRevision, CandidateRevision: run.CandidateRevision,
			CorrelationID: run.ID, RequestID: run.RequestID, CreatedAt: now,
		}); err != nil {
			return WorkRun{}, fmt.Errorf("enqueue work run launch: %w", err)
		}
		shouldWakeNamedAgent, err = requestWakeTx(ctx, tx, run.NamedAgentID, toMillis(now))
		if err != nil {
			return WorkRun{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkRun{}, err
	}
	if shouldWakeNamedAgent && s.wake != nil {
		s.wake.Deliver(run.NamedAgentID)
	}
	return run, nil
}

func (s *Service) FinishWorkRun(ctx context.Context, params WorkRunFinishParams) (WorkRun, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return WorkRun{}, err
	}
	if !terminalWorkRunState(params.State) {
		return WorkRun{}, errors.New("work run finish requires a terminal state")
	}
	if params.InputTokens < 0 || params.OutputTokens < 0 || params.CostUSD < 0 {
		return WorkRun{}, errors.New("work run usage cannot be negative")
	}
	params.RequestID = strings.TrimSpace(params.RequestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkRun{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return WorkRun{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return WorkRun{}, err
	}
	run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ? AND run.work_id = ?`, params.RunID, work.ID))
	if err != nil {
		return WorkRun{}, err
	}
	if run.State != WorkRunRunning && run.State != WorkRunQueued {
		if params.RequestID != "" && run.FinishRequestID == params.RequestID {
			return run, nil
		}
		return WorkRun{}, fmt.Errorf("%w: work run is already %s", ErrConflict, run.State)
	}
	promotedByRun := work.PromotionRunRef == run.ID && run.CandidateRevision+1 == work.CandidateRevision
	if run.GoalRevision != work.GoalRevision {
		return WorkRun{}, fmt.Errorf("%w: work run revisions are stale", ErrConflict)
	}
	supersededCandidateRevision := run.CandidateRevision != work.CandidateRevision && !promotedByRun
	now := fromMillis(toMillis(s.now()))
	run.State, run.Outcome, run.Provider, run.Model = params.State, strings.TrimSpace(params.Outcome), strings.TrimSpace(params.Provider), strings.TrimSpace(params.Model)
	run.InputTokens, run.OutputTokens, run.CostUSD, run.ChecksRerun = params.InputTokens, params.OutputTokens, params.CostUSD, params.ChecksRerun
	run.FindingsCount, run.RepairOutcome, run.EndedAt, run.UpdatedAt = params.FindingsCount, strings.TrimSpace(params.RepairOutcome), now, now
	run.FinishRequestID, run.Qualified = params.RequestID, params.Qualified
	if supersededCandidateRevision {
		run.Qualified = false
		if run.Outcome == "" {
			run.Outcome = "candidate revision superseded"
		} else {
			run.Outcome = "candidate revision superseded: " + run.Outcome
		}
	}
	persistedState := run.State
	if persistedState == WorkRunTimedOut {
		// Legacy databases have a CHECK constraint that predates timed_out. The
		// outcome preserves the honest terminal while the physical state remains
		// interrupted until that table is naturally recreated.
		persistedState = WorkRunInterrupted
		if run.Outcome == "" {
			run.Outcome = string(WorkRunTimedOut)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE work_runs SET state = ?, outcome = ?, provider = ?, model = ?, input_tokens = ?,
			output_tokens = ?, cost_usd = ?, checks_rerun = ?, findings_count = ?, repair_outcome = ?, finish_request_id = ?, qualified = ?, ended_at = ?, updated_at = ?
		WHERE id = ?`, persistedState, run.Outcome, run.Provider, run.Model, run.InputTokens, run.OutputTokens,
		run.CostUSD, run.ChecksRerun, run.FindingsCount, run.RepairOutcome, run.FinishRequestID, boolInt(run.Qualified), toMillis(now), toMillis(now), run.ID); err != nil {
		return WorkRun{}, fmt.Errorf("finish work run: %w", err)
	}
	if run.Qualified {
		if _, err := tx.ExecContext(ctx, `UPDATE works SET qualified_candidates = qualified_candidates + 1 WHERE id = ?`, work.ID); err != nil {
			return WorkRun{}, fmt.Errorf("count qualified candidate: %w", err)
		}
	}
	if err := refreshWorkCurrentRunRefTx(ctx, tx, work.ID, now); err != nil {
		return WorkRun{}, fmt.Errorf("settle work run handle: %w", err)
	}
	if run.SessionRef != "" {
		nextState := CollaborationSessionIdle
		if run.State == WorkRunInterrupted {
			nextState = CollaborationSessionInterrupted
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE collaboration_session_bindings SET state = ?, run_id = NULL, updated_at = ?
			WHERE session_ref = ? AND run_id = ?`, nextState, toMillis(now), run.SessionRef, run.ID); err != nil {
			return WorkRun{}, fmt.Errorf("release work run session: %w", err)
		}
	}
	wakeIDs, err := s.enqueueWorkRunTerminalTx(ctx, tx, work, run, now)
	if err != nil {
		return WorkRun{}, err
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	if err != nil {
		return WorkRun{}, err
	}
	wakeIDs = appendUniqueStrings(wakeIDs, admittedWakeIDs...)
	if err := tx.Commit(); err != nil {
		return WorkRun{}, err
	}
	if s.wake != nil {
		for _, wakeID := range wakeIDs {
			s.wake.Deliver(wakeID)
		}
	}
	return run, nil
}

func (s *Service) ExpireWorkRuns(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := fromMillis(toMillis(s.now()))
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_runs WHERE state IN ('queued', 'running') AND deadline_at IS NOT NULL AND deadline_at <= ? ORDER BY deadline_at, id`, toMillis(now))
	if err != nil {
		return 0, fmt.Errorf("list expired work runs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var wakeIDs []string
	for _, id := range ids {
		run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ?`, id))
		if err != nil {
			return 0, err
		}
		work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, run.WorkID))
		if err != nil {
			return 0, err
		}
		run.State, run.Outcome, run.EndedAt, run.UpdatedAt = WorkRunTimedOut, string(WorkRunTimedOut), now, now
		if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state = 'interrupted', outcome = 'timed_out', ended_at = ?, updated_at = ? WHERE id = ? AND state IN ('queued', 'running')`, toMillis(now), toMillis(now), run.ID); err != nil {
			return 0, fmt.Errorf("expire work run: %w", err)
		}
		if run.SessionRef != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = 'interrupted', run_id = NULL, updated_at = ? WHERE session_ref = ? AND run_id = ?`, toMillis(now), run.SessionRef, run.ID); err != nil {
				return 0, err
			}
		}
		if err := refreshWorkCurrentRunRefTx(ctx, tx, work.ID, now); err != nil {
			return 0, err
		}
		terminalWakeIDs, err := s.enqueueWorkRunTerminalTx(ctx, tx, work, run, now)
		if err != nil {
			return 0, err
		}
		wakeIDs = appendUniqueStrings(wakeIDs, terminalWakeIDs...)
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	wakeIDs = appendUniqueStrings(wakeIDs, admittedWakeIDs...)
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.wake != nil {
		for _, id := range wakeIDs {
			s.wake.Deliver(id)
		}
	}
	return len(ids), nil
}

func (s *Service) AttachWorkRunTurn(ctx context.Context, params WorkRunTurnParams) (WorkRun, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return WorkRun{}, err
	}
	params.WorkID = strings.TrimSpace(params.WorkID)
	params.RunID = strings.TrimSpace(params.RunID)
	params.SessionRef = strings.TrimSpace(params.SessionRef)
	params.TurnID = strings.TrimSpace(params.TurnID)
	if params.WorkID == "" || params.RunID == "" || params.SessionRef == "" || params.TurnID == "" {
		return WorkRun{}, errors.New("work run turn requires work, run, session, and turn")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkRun{}, err
	}
	defer tx.Rollback()
	run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ? AND run.work_id = ?`, params.RunID, params.WorkID))
	if err != nil {
		return WorkRun{}, err
	}
	if run.NamedAgentID == "" || run.NamedAgentID != actor.ID || run.SessionRef != params.SessionRef {
		return WorkRun{}, ErrUnauthorized
	}
	if run.State != WorkRunRunning && run.State != WorkRunQueued {
		return WorkRun{}, fmt.Errorf("%w: work run is already %s", ErrConflict, run.State)
	}
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
	if err != nil {
		return WorkRun{}, err
	}
	if binding.PrincipalID != actor.ID || binding.WorkID != params.WorkID || binding.RunID != params.RunID || binding.State != CollaborationSessionRunning {
		return WorkRun{}, fmt.Errorf("%w: work run session binding does not match", ErrConflict)
	}
	if run.TurnID != "" && run.TurnID != params.TurnID {
		return WorkRun{}, fmt.Errorf("%w: work run is already attached to turn %q", ErrConflict, run.TurnID)
	}
	if run.TurnID == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET turn_id = ?, updated_at = ? WHERE id = ?`, params.TurnID, toMillis(s.now()), run.ID); err != nil {
			return WorkRun{}, fmt.Errorf("attach work run turn: %w", err)
		}
	}
	updated, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ?`, run.ID))
	if err != nil {
		return WorkRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkRun{}, err
	}
	return updated, nil
}

func (s *Service) AddWorkArtifact(ctx context.Context, params WorkArtifactAddParams) (WorkArtifact, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return WorkArtifact{}, err
	}
	params.WorkID, params.URI = strings.TrimSpace(params.WorkID), strings.TrimSpace(params.URI)
	if params.WorkID == "" || params.URI == "" || !validWorkArtifactKind(params.Kind) {
		return WorkArtifact{}, errors.New("work artifact requires work, uri, and valid kind")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkArtifact{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return WorkArtifact{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return WorkArtifact{}, err
	}
	if runID := strings.TrimSpace(params.RunID); runID != "" {
		run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ? AND run.work_id = ?`, runID, work.ID))
		if err != nil {
			return WorkArtifact{}, err
		}
		if run.GoalRevision != work.GoalRevision || run.CandidateRevision != work.CandidateRevision {
			return WorkArtifact{}, fmt.Errorf("%w: artifact run revisions are stale", ErrConflict)
		}
	}
	if params.Kind == WorkArtifactCandidate {
		if strings.TrimSpace(params.RunID) == "" {
			return WorkArtifact{}, errors.New("candidate artifact requires its producer, selector, or integration run")
		}
		var currentCandidates int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM work_artifacts artifact
			JOIN work_runs run ON run.id = artifact.run_id
			WHERE artifact.work_id = ? AND artifact.kind = 'candidate'
				AND run.goal_revision = ? AND run.candidate_revision = ?`,
			work.ID, work.GoalRevision, work.CandidateRevision).Scan(&currentCandidates); err != nil {
			return WorkArtifact{}, fmt.Errorf("count current candidate artifacts: %w", err)
		}
		if currentCandidates >= work.MaxCandidates {
			return WorkArtifact{}, fmt.Errorf("%w: candidate budget exhausted", ErrConflict)
		}
	}
	id, err := randomID("artifact", 12)
	if err != nil {
		return WorkArtifact{}, err
	}
	now := fromMillis(toMillis(s.now()))
	artifact := WorkArtifact{ID: id, WorkID: work.ID, RunID: strings.TrimSpace(params.RunID), Kind: params.Kind, URI: params.URI, Label: strings.TrimSpace(params.Label), Summary: strings.TrimSpace(params.Summary), WorkspaceRevision: strings.TrimSpace(params.WorkspaceRevision), CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_artifacts(id, work_id, run_id, kind, uri, label, summary, workspace_revision, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, artifact.ID, artifact.WorkID, nullableString(artifact.RunID), artifact.Kind,
		artifact.URI, artifact.Label, artifact.Summary, artifact.WorkspaceRevision, toMillis(now)); err != nil {
		return WorkArtifact{}, fmt.Errorf("insert work artifact: %w", err)
	}
	if artifact.Kind == WorkArtifactCandidate {
		if _, err := tx.ExecContext(ctx, `UPDATE works SET candidates_used = candidates_used + 1, updated_at = ? WHERE id = ?`, toMillis(now), work.ID); err != nil {
			return WorkArtifact{}, fmt.Errorf("count candidate artifact: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkArtifact{}, err
	}
	return artifact, nil
}

func (s *Service) PromoteWorkCandidate(ctx context.Context, params WorkCandidatePromoteParams) (Work, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return Work{}, err
	}
	params.WorkID = strings.TrimSpace(params.WorkID)
	params.ArtifactRef = strings.TrimSpace(params.ArtifactRef)
	params.RunID = strings.TrimSpace(params.RunID)
	params.RequestID = strings.TrimSpace(params.RequestID)
	params.SelectionReason = strings.TrimSpace(params.SelectionReason)
	if params.WorkID == "" || params.ArtifactRef == "" || params.RunID == "" || params.RequestID == "" {
		return Work{}, errors.New("candidate promotion requires work, artifact, run, and request id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return Work{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return Work{}, err
	}
	if work.PromotionRequestID == params.RequestID {
		return work, nil
	}
	if work.PromotionRequestID != "" && work.CandidateArtifactRef == params.ArtifactRef {
		return Work{}, fmt.Errorf("%w: candidate was already promoted with another request id", ErrConflict)
	}
	run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ? AND run.work_id = ?`, params.RunID, work.ID))
	if err != nil {
		return Work{}, err
	}
	if run.GoalRevision != work.GoalRevision || run.CandidateRevision != work.CandidateRevision {
		return Work{}, fmt.Errorf("%w: promotion run revisions are stale", ErrConflict)
	}
	if run.State != WorkRunRunning && run.State != WorkRunCompleted {
		return Work{}, fmt.Errorf("%w: promotion run is not active or completed", ErrConflict)
	}
	legalSingleCandidate := work.MaxCandidates <= 1 && run.Kind == WorkRunProducer
	if !legalSingleCandidate && run.Kind != WorkRunSelector && run.Kind != WorkRunIntegration {
		return Work{}, fmt.Errorf("%w: multi-candidate promotion requires selector or integration", ErrUnauthorized)
	}
	var artifact WorkArtifact
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, work_id, COALESCE(run_id, ''), kind, uri, label, summary, workspace_revision, created_at
		FROM work_artifacts WHERE id = ? AND work_id = ?`, params.ArtifactRef, work.ID).Scan(
		&artifact.ID, &artifact.WorkID, &artifact.RunID, &artifact.Kind, &artifact.URI,
		&artifact.Label, &artifact.Summary, &artifact.WorkspaceRevision, &createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Work{}, fmt.Errorf("%w: candidate artifact %q", ErrNotFound, params.ArtifactRef)
		}
		return Work{}, err
	}
	if artifact.RunID != "" {
		artifactRun, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ?`, artifact.RunID))
		if err != nil {
			return Work{}, err
		}
		if artifactRun.GoalRevision != work.GoalRevision {
			return Work{}, fmt.Errorf("%w: candidate artifact goal revision is stale", ErrConflict)
		}
	}
	workspaceRevision := strings.TrimSpace(params.WorkspaceRevision)
	if workspaceRevision == "" {
		workspaceRevision = artifact.WorkspaceRevision
	}
	now := fromMillis(toMillis(s.now()))
	nextCandidateRevision := work.CandidateRevision + 1
	verificationState := WorkVerificationNotRequired
	if work.VerificationRequired {
		verificationState = WorkVerificationPending
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET state = 'checking', candidate_revision = ?, candidate_artifact_ref = ?,
			candidate_workspace_revision = ?, promotion_run_ref = ?, selection_reason = ?,
			promotion_request_id = ?, verification_state = ?, updated_at = ?
		WHERE id = ? AND goal_revision = ? AND candidate_revision = ?`,
		nextCandidateRevision, artifact.ID, workspaceRevision, run.ID, params.SelectionReason,
		params.RequestID, verificationState, toMillis(now), work.ID, work.GoalRevision, work.CandidateRevision); err != nil {
		return Work{}, fmt.Errorf("promote canonical candidate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state = 'checking', task_candidate_revision = ? WHERE id = ?`, nextCandidateRevision, work.ID); err != nil {
		return Work{}, fmt.Errorf("project promoted candidate to task: %w", err)
	}
	if err := insertWorkEventTx(ctx, tx, WorkEvent{
		WorkID: work.ID, Kind: "state", State: string(WorkChecking), Summary: "Canonical candidate promoted: " + params.SelectionReason,
		GoalRevision: work.GoalRevision, CandidateRevision: nextCandidateRevision, CreatedAt: now,
	}); err != nil {
		return Work{}, err
	}
	var runtimeID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM room_runtimes WHERE room_id = ?`, work.RoomID).Scan(&runtimeID); err != nil {
		return Work{}, err
	}
	if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
		RoomID: work.RoomID, ToAgentID: runtimeID, TargetKind: CollaborationTargetRoomRuntime,
		TargetID: runtimeID, Visibility: CollaborationVisibilitySystem, Kind: CollaborationCandidateReady,
		Body: "Canonical candidate promoted", WorkID: work.ID, SourceMessageID: work.ID,
		ArtifactRefs: []string{artifact.ID}, GoalRevision: work.GoalRevision,
		CandidateRevision: nextCandidateRevision, CorrelationID: run.ID, RequestID: params.RequestID, CreatedAt: now,
	}); err != nil {
		return Work{}, err
	}
	shouldWake, err := requestWakeTx(ctx, tx, runtimeID, toMillis(now))
	if err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	if shouldWake && s.wake != nil {
		s.wake.Deliver(runtimeID)
	}
	return s.GetWork(ctx, work.ID)
}

func (s *Service) CancelWork(ctx context.Context, workID, reason, agentID, token string) (Work, error) {
	actor, err := s.AuthenticatePrincipal(ctx, agentID, token)
	if err != nil {
		return Work{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, workID))
	if err != nil {
		return Work{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return Work{}, err
	}
	if work.State == WorkCompleted {
		return Work{}, fmt.Errorf("%w: completed work cannot be cancelled", ErrConflict)
	}
	interruptTargets, err := activeWorkSessionInterruptTargetsTx(ctx, tx, work.ID)
	if err != nil {
		return Work{}, err
	}
	wakeRecipients, err := pendingWorkWakeRecipientsTx(ctx, tx, work.ID)
	if err != nil {
		return Work{}, err
	}
	now := fromMillis(toMillis(s.now()))
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_runs WHERE work_id = ? AND state IN ('queued', 'running') ORDER BY created_at, id`, work.ID)
	if err != nil {
		return Work{}, err
	}
	var activeRunIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Work{}, err
		}
		activeRunIDs = append(activeRunIDs, id)
	}
	if err := rows.Close(); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE works SET state = 'cancelled', current_run_ref = NULL, failure_reason = ?, cancelled_at = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(reason), toMillis(now), toMillis(now), work.ID); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state = 'open' WHERE id = ?`, work.ID); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at = ? WHERE work_id = ? AND pulled_at IS NULL`, toMillis(now), work.ID); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE inbox_items SET pulled_at = ?
		WHERE member_type = 'agent' AND message_id = ? AND kind = 'task' AND pulled_at IS NULL`, toMillis(now), work.ID); err != nil {
		return Work{}, fmt.Errorf("retire cancelled work inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collaboration_session_bindings
		SET state = 'interrupted', run_id = NULL, updated_at = ?
		WHERE work_id = ? AND state != 'missing'`, toMillis(now), work.ID); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state = 'cancelled', ended_at = ?, updated_at = ? WHERE work_id = ? AND state IN ('queued', 'running')`, toMillis(now), toMillis(now), work.ID); err != nil {
		return Work{}, err
	}
	var terminalWakeIDs []string
	for _, runID := range activeRunIDs {
		run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ?`, runID))
		if err != nil {
			return Work{}, err
		}
		run.State, run.Outcome, run.EndedAt = WorkRunCancelled, strings.TrimSpace(reason), now
		ids, err := s.enqueueWorkRunTerminalTx(ctx, tx, work, run, now)
		if err != nil {
			return Work{}, err
		}
		terminalWakeIDs = appendUniqueStrings(terminalWakeIDs, ids...)
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	if err != nil {
		return Work{}, err
	}
	terminalWakeIDs = appendUniqueStrings(terminalWakeIDs, admittedWakeIDs...)
	if err := insertWorkEventTx(ctx, tx, WorkEvent{WorkID: work.ID, Kind: "cancellation", State: string(WorkCancelled), Summary: strings.TrimSpace(reason), GoalRevision: work.GoalRevision, CandidateRevision: work.CandidateRevision, CreatedAt: now}); err != nil {
		return Work{}, err
	}
	for _, recipientID := range wakeRecipients {
		if err := recomputeAgentWakeTx(ctx, tx, recipientID, toMillis(now)); err != nil {
			return Work{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	s.interruptWorkSessions(interruptTargets)
	if s.wake != nil {
		for _, id := range terminalWakeIDs {
			s.wake.Deliver(id)
		}
	}
	return s.GetWork(ctx, work.ID)
}

type workSessionInterruptTarget struct {
	agentID    string
	sessionRef string
}

func pendingWorkWakeRecipientsTx(ctx context.Context, tx *sql.Tx, workID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT member_id FROM inbox_items
		WHERE member_type = 'agent' AND message_id = ? AND kind = 'task' AND pulled_at IS NULL
		UNION
		SELECT to_agent_id FROM collaboration_messages
		WHERE work_id = ? AND pulled_at IS NULL AND invalidated_at IS NULL
		ORDER BY 1`, workID, workID)
	if err != nil {
		return nil, fmt.Errorf("list cancelled work wake recipients: %w", err)
	}
	defer rows.Close()
	recipients := make([]string, 0)
	for rows.Next() {
		var recipientID string
		if err := rows.Scan(&recipientID); err != nil {
			return nil, fmt.Errorf("scan cancelled work wake recipient: %w", err)
		}
		if recipientID != "" {
			recipients = append(recipients, recipientID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cancelled work wake recipients: %w", err)
	}
	return recipients, nil
}

func activeWorkSessionInterruptTargetsTx(ctx context.Context, tx *sql.Tx, workID string) ([]workSessionInterruptTarget, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT
			COALESCE(NULLIF(run.named_agent_id, ''), NULLIF(binding.named_agent_id, ''), ''),
			COALESCE(NULLIF(run.session_ref, ''), NULLIF(binding.session_ref, ''), '')
		FROM work_runs run
		LEFT JOIN collaboration_session_bindings binding ON binding.run_id = run.id
		WHERE run.work_id = ? AND run.state IN ('queued', 'running')
		ORDER BY 1, 2`, workID)
	if err != nil {
		return nil, fmt.Errorf("list active work sessions for cancellation: %w", err)
	}
	defer rows.Close()
	targets := make([]workSessionInterruptTarget, 0)
	for rows.Next() {
		var target workSessionInterruptTarget
		if err := rows.Scan(&target.agentID, &target.sessionRef); err != nil {
			return nil, fmt.Errorf("scan active work session for cancellation: %w", err)
		}
		if target.sessionRef != "" {
			targets = append(targets, target)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active work sessions for cancellation: %w", err)
	}
	return targets, nil
}

func (s *Service) interruptWorkSessions(targets []workSessionInterruptTarget) {
	if s == nil || s.wake == nil {
		return
	}
	namedInterrupt, hasNamedInterrupt := s.wake.(WakeSessionInterruptSink)
	runInterrupt, hasRunInterrupt := s.wake.(WakeRunSessionInterruptSink)
	fallback, hasFallback := s.wake.(WakeInterruptSink)
	interruptedAgents := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.agentID == "" {
			if hasRunInterrupt {
				runInterrupt.InterruptRunSession(target.sessionRef)
			}
			continue
		}
		if hasNamedInterrupt {
			namedInterrupt.InterruptSession(target.agentID, target.sessionRef)
			continue
		}
		if !hasFallback {
			continue
		}
		if _, exists := interruptedAgents[target.agentID]; exists {
			continue
		}
		interruptedAgents[target.agentID] = struct{}{}
		fallback.Interrupt(target.agentID)
	}
}

func (s *Service) UpdateWorkPolicy(ctx context.Context, params WorkPolicyUpdateParams) (Work, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return Work{}, err
	}
	if params.MaxVerifierAttempts < 1 || params.MaxVerifierAttempts > 10 || params.MaxCandidates < 1 || params.MaxCandidates > 4 {
		return Work{}, errors.New("work policy budgets are out of range")
	}
	params.FanoutReason = strings.TrimSpace(params.FanoutReason)
	if params.MaxCandidates > 1 && params.FanoutReason == "" {
		return Work{}, errors.New("multi-candidate policy requires a concrete fan-out reason")
	}
	if params.MaxRounds < 0 || params.MaxRounds > 10 || params.MaxInputTokens < 0 || params.MaxOutputTokens < 0 {
		return Work{}, errors.New("adaptive work policy budgets are out of range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return Work{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return Work{}, err
	}
	if params.LeadNamedAgentID != "" {
		if err := requireMemberTx(ctx, tx, work.RoomID, MemberAgent, params.LeadNamedAgentID); err != nil {
			return Work{}, err
		}
	}
	if params.MaxRounds == 0 {
		params.MaxRounds = work.MaxRounds
	}
	if params.DeadlineAt.IsZero() {
		params.DeadlineAt = work.DeadlineAt
	}
	now := toMillis(s.now())
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET lead_named_agent_id = ?, max_verifier_attempts = ?, max_candidates = ?,
			fanout_reason = ?, max_rounds = ?, max_input_tokens = ?, max_output_tokens = ?, deadline_at = ?,
			updated_at = ? WHERE id = ?`, nullableString(params.LeadNamedAgentID),
		params.MaxVerifierAttempts, params.MaxCandidates, params.FanoutReason, params.MaxRounds,
		params.MaxInputTokens, params.MaxOutputTokens, nullableTimeMillis(params.DeadlineAt), now, work.ID); err != nil {
		return Work{}, fmt.Errorf("update work policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	return s.GetWork(ctx, work.ID)
}

func (s *Service) UpdateWorkEvidence(ctx context.Context, params WorkEvidenceUpdateParams) (Work, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return Work{}, err
	}
	if params.ChangedFilesCount < 0 {
		return Work{}, errors.New("changed files count cannot be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, params.WorkID))
	if err != nil {
		return Work{}, err
	}
	if err := authorizeWorkActorTx(ctx, tx, work, actor); err != nil {
		return Work{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET checks_summary = ?, changed_files_count = ?, unresolved_items = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(params.ChecksSummary), params.ChangedFilesCount,
		strings.TrimSpace(params.UnresolvedItems), toMillis(s.now()), work.ID); err != nil {
		return Work{}, fmt.Errorf("update work evidence summary: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	return s.GetWork(ctx, work.ID)
}

func authorizeWorkActorTx(ctx context.Context, tx *sql.Tx, work Work, actor AgentRuntime) error {
	if actor.ID == work.OwnerNamedAgentID || actor.ID == work.LeadNamedAgentID {
		return nil
	}
	if actor.IsRoomRuntime() && actor.RoomID == work.RoomID {
		return nil
	}
	return ErrUnauthorized
}

func refreshWorkCurrentRunRefTx(ctx context.Context, tx *sql.Tx, workID string, updatedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE works
		SET current_run_ref = (
			SELECT run.id FROM work_runs run
			WHERE run.work_id = works.id AND run.state IN ('queued', 'running')
			ORDER BY run.created_at DESC, run.id DESC LIMIT 1
		), updated_at = ?
		WHERE id = ?`, toMillis(updatedAt), workID)
	return err
}

func (s *Service) enqueueWorkRunTerminalTx(ctx context.Context, tx *sql.Tx, work Work, run WorkRun, now time.Time) ([]string, error) {
	var runtimeID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM room_runtimes WHERE room_id = ?`, work.RoomID).Scan(&runtimeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Legacy/test fixtures can predate room runtimes. Production startup
			// backfills them; recovery must still settle the run if none exists.
			return nil, nil
		}
		return nil, fmt.Errorf("resolve work terminal room runtime: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_artifacts WHERE work_id = ? AND run_id = ? ORDER BY created_at, id`, work.ID, run.ID)
	if err != nil {
		return nil, fmt.Errorf("list terminal run artifacts: %w", err)
	}
	var artifactRefs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		artifactRefs = append(artifactRefs, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"type": "work_run_terminal", "work_id": work.ID, "run_id": run.ID,
		"kind": run.Kind, "state": run.State, "artifact_refs": artifactRefs,
		"goal_revision": run.GoalRevision, "round": run.Round, "qualified": run.Qualified,
	})
	if err != nil {
		return nil, err
	}
	if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
		RoomID: work.RoomID, ToAgentID: runtimeID, TargetKind: CollaborationTargetRoomRuntime,
		TargetID: runtimeID, Visibility: CollaborationVisibilitySystem, Kind: CollaborationWorkRunTerminal,
		Body: string(body), WorkID: work.ID, ArtifactRefs: artifactRefs, GoalRevision: run.GoalRevision,
		CandidateRevision: run.CandidateRevision, CorrelationID: run.ID,
		TerminalState: collaborationTerminalStateForRun(run.State), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("enqueue work run terminal: %w", err)
	}
	shouldWake, err := requestWakeTx(ctx, tx, runtimeID, toMillis(now))
	if err != nil {
		return nil, err
	}
	if shouldWake {
		return []string{runtimeID}, nil
	}
	return nil, nil
}

func collaborationTerminalStateForRun(state WorkRunState) CollaborationTerminalState {
	switch state {
	case WorkRunCompleted:
		return CollaborationTerminalCompleted
	case WorkRunFailed:
		return CollaborationTerminalFailed
	case WorkRunCancelled:
		return CollaborationTerminalCancelled
	case WorkRunTimedOut:
		return CollaborationTerminalTimedOut
	default:
		return CollaborationTerminalInterrupted
	}
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func (s *Service) admitQueuedWorkRunsTx(ctx context.Context, tx *sql.Tx, now time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_runs WHERE state = 'queued' ORDER BY created_at, id LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("list queued work runs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var wakeIDs []string
	for _, id := range ids {
		run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id = ?`, id))
		if err != nil {
			return nil, err
		}
		work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, run.WorkID))
		if err != nil {
			return nil, err
		}
		state, _, err := s.workRunAdmissionTx(ctx, tx, work.RoomID, run.NamedAgentID, run.SessionRef)
		if err != nil {
			return nil, err
		}
		if state != WorkRunRunning {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state = 'running', queue_reason = '', started_at = ?, updated_at = ? WHERE id = ? AND state = 'queued'`, toMillis(now), toMillis(now), run.ID); err != nil {
			return nil, fmt.Errorf("admit queued work run: %w", err)
		}
		if run.SessionRef != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = 'running', updated_at = ? WHERE session_ref = ? AND run_id = ?`, toMillis(now), run.SessionRef, run.ID); err != nil {
				return nil, fmt.Errorf("start admitted work session: %w", err)
			}
		}
		recipientID := run.NamedAgentID
		targetKind := CollaborationTargetNamedAgent
		if recipientID == "" {
			targetKind = CollaborationTargetRoomRuntime
			if err := tx.QueryRowContext(ctx, `SELECT id FROM room_runtimes WHERE room_id = ?`, work.RoomID).Scan(&recipientID); err != nil {
				return nil, err
			}
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: work.RoomID, ToAgentID: recipientID, TargetKind: targetKind, TargetID: recipientID,
			TargetSessionRef: run.SessionRef, Visibility: CollaborationVisibilitySystem,
			Kind: CollaborationControl, Body: "Queued Work Run admitted", WorkID: work.ID,
			GoalRevision: run.GoalRevision, CandidateRevision: run.CandidateRevision,
			CorrelationID: run.ID, CreatedAt: now,
		}); err != nil {
			return nil, err
		}
		shouldWake, err := requestWakeTx(ctx, tx, recipientID, toMillis(now))
		if err != nil {
			return nil, err
		}
		if shouldWake {
			wakeIDs = appendUniqueStrings(wakeIDs, recipientID)
		}
	}
	return wakeIDs, nil
}

func nullableTimeMillis(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return toMillis(value)
}

func collaborationSessionStateForRun(state WorkRunState) CollaborationSessionState {
	if state == WorkRunRunning {
		return CollaborationSessionRunning
	}
	return CollaborationSessionIdle
}

func (s *Service) workRunAdmissionTx(ctx context.Context, tx *sql.Tx, roomID, namedAgentID, sessionRef string) (WorkRunState, string, error) {
	agentActive, roomActive, globalActive, err := activeCollaborationCountsTx(ctx, tx, namedAgentID, roomID, sessionRef)
	if err != nil {
		return "", "", err
	}
	if namedAgentID != "" && agentActive >= s.agentRunLimit {
		return WorkRunQueued, "named_agent_capacity", nil
	}
	if roomActive >= s.roomRunLimit {
		return WorkRunQueued, "room_capacity", nil
	}
	if globalActive >= s.globalRunLimit {
		return WorkRunQueued, "global_capacity", nil
	}
	return WorkRunRunning, "", nil
}

func (s *Service) WorkCapacities(ctx context.Context, roomID string) ([]WorkCapacity, error) {
	roomID = strings.TrimSpace(roomID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(run.named_agent_id, ''),
			SUM(CASE WHEN run.state = 'running' AND run.turn_id != '' THEN 1 ELSE 0 END),
			SUM(CASE WHEN run.state = 'running' AND run.turn_id = '' THEN 1 ELSE 0 END),
			SUM(CASE WHEN run.state = 'queued' THEN 1 ELSE 0 END)
		FROM work_runs run JOIN works work ON work.id = run.work_id
		WHERE (? = '' OR work.room_id = ?)
		GROUP BY run.named_agent_id ORDER BY run.named_agent_id`, roomID, roomID)
	if err != nil {
		return nil, fmt.Errorf("list work capacities: %w", err)
	}
	defer rows.Close()
	var result []WorkCapacity
	for rows.Next() {
		var item WorkCapacity
		if err := rows.Scan(&item.NamedAgentID, &item.Active, &item.Starting, &item.Queued); err != nil {
			return nil, err
		}
		item.RoomID, item.Limit = roomID, s.agentRunLimit
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) NamedAgentCapacity(ctx context.Context, namedAgentID string) (WorkCapacity, error) {
	namedAgentID = strings.TrimSpace(namedAgentID)
	capacity := WorkCapacity{NamedAgentID: namedAgentID, Limit: s.agentRunLimit}
	if namedAgentID == "" {
		return capacity, errors.New("named agent id is required")
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM work_runs WHERE named_agent_id = ? AND state = 'running' AND turn_id != ''),
			(SELECT COUNT(*) FROM work_runs WHERE named_agent_id = ? AND state = 'running' AND turn_id = ''),
			(SELECT COUNT(*) FROM work_runs WHERE named_agent_id = ? AND state = 'queued'),
			(SELECT COUNT(*) FROM collaboration_session_bindings WHERE named_agent_id = ? AND state = 'idle' AND run_id IS NULL)`,
		namedAgentID, namedAgentID, namedAgentID, namedAgentID).Scan(&capacity.Active, &capacity.Starting, &capacity.Queued, &capacity.Idle); err != nil {
		return WorkCapacity{}, fmt.Errorf("read named agent capacity: %w", err)
	}
	return capacity, nil
}

func validWorkRunKind(kind WorkRunKind) bool {
	return kind == WorkRunProducer || kind == WorkRunVerifier || kind == WorkRunSelector || kind == WorkRunIntegration
}

func terminalWorkRunState(state WorkRunState) bool {
	return state == WorkRunCompleted || state == WorkRunFailed || state == WorkRunCancelled || state == WorkRunInterrupted || state == WorkRunTimedOut
}

func validWorkArtifactKind(kind WorkArtifactKind) bool {
	switch kind {
	case WorkArtifactCandidate, WorkArtifactDiff, WorkArtifactSnapshot, WorkArtifactCheckLog, WorkArtifactScreenshot, WorkArtifactReport, WorkArtifactOther:
		return true
	default:
		return false
	}
}

const workRunSelect = `
	SELECT run.id, run.work_id, COALESCE(run.named_agent_id, ''), run.kind, run.profile, COALESCE(run.session_ref, ''), run.turn_id, run.state,
		run.goal_revision, run.candidate_revision, run.workspace_revision, run.provider, run.model,
		run.input_tokens, run.output_tokens, run.cost_usd, run.checks_rerun, run.findings_count, run.outcome,
		run.repair_outcome, run.request_id, run.finish_request_id, run.round, run.qualified, run.deadline_at, run.queue_reason,
		run.started_at, run.ended_at, run.created_at, run.updated_at
	FROM work_runs run`

func scanWorkRun(row scanner) (WorkRun, error) {
	var run WorkRun
	var startedAt, endedAt, deadlineAt sql.NullInt64
	var qualified int
	var createdAt, updatedAt int64
	if err := row.Scan(&run.ID, &run.WorkID, &run.NamedAgentID, &run.Kind, &run.Profile, &run.SessionRef, &run.TurnID, &run.State,
		&run.GoalRevision, &run.CandidateRevision, &run.WorkspaceRevision, &run.Provider, &run.Model,
		&run.InputTokens, &run.OutputTokens, &run.CostUSD, &run.ChecksRerun, &run.FindingsCount, &run.Outcome,
		&run.RepairOutcome, &run.RequestID, &run.FinishRequestID, &run.Round, &qualified, &deadlineAt, &run.QueueReason,
		&startedAt, &endedAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkRun{}, ErrNotFound
		}
		return WorkRun{}, err
	}
	if startedAt.Valid {
		run.StartedAt = fromMillis(startedAt.Int64)
	}
	if endedAt.Valid {
		run.EndedAt = fromMillis(endedAt.Int64)
	}
	run.Qualified = qualified != 0
	if run.State == WorkRunInterrupted && run.Outcome == string(WorkRunTimedOut) {
		run.State = WorkRunTimedOut
	}
	if deadlineAt.Valid {
		run.DeadlineAt = fromMillis(deadlineAt.Int64)
	}
	run.CreatedAt, run.UpdatedAt = fromMillis(createdAt), fromMillis(updatedAt)
	return run, nil
}

func (s *Service) listWorkRuns(ctx context.Context, workID string) ([]WorkRun, error) {
	rows, err := s.db.QueryContext(ctx, workRunSelect+` WHERE run.work_id = ? ORDER BY run.created_at, run.rowid`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []WorkRun
	for rows.Next() {
		run, err := scanWorkRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Service) listWorkArtifacts(ctx context.Context, workID string) ([]WorkArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, work_id, COALESCE(run_id, ''), kind, uri, label, summary, workspace_revision, created_at
		FROM work_artifacts WHERE work_id = ? ORDER BY created_at, id`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []WorkArtifact
	for rows.Next() {
		var artifact WorkArtifact
		var createdAt int64
		if err := rows.Scan(&artifact.ID, &artifact.WorkID, &artifact.RunID, &artifact.Kind, &artifact.URI, &artifact.Label, &artifact.Summary, &artifact.WorkspaceRevision, &createdAt); err != nil {
			return nil, err
		}
		artifact.CreatedAt = fromMillis(createdAt)
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

func (s *Service) listWorkEvents(ctx context.Context, workID string) ([]WorkEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, work_id, kind, state, summary, goal_revision, candidate_revision, created_at
		FROM work_events WHERE work_id = ? ORDER BY created_at, id`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []WorkEvent
	for rows.Next() {
		var event WorkEvent
		var createdAt int64
		if err := rows.Scan(&event.ID, &event.WorkID, &event.Kind, &event.State, &event.Summary, &event.GoalRevision, &event.CandidateRevision, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt = fromMillis(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}
