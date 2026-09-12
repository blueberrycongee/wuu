package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SubmitTaskVerification records the one machine-readable verifier decision
// and delivers the natural-language report back to the visible task owner.
// A task owner or lead may record an independent completed check; an assigned
// verifier may record its own completed check without an intermediary.
func (s *Service) SubmitTaskVerification(ctx context.Context, params TaskVerificationSubmitParams) (TaskVerificationSubmitResult, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	params.TaskID = strings.TrimSpace(params.TaskID)
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.Report = strings.TrimSpace(params.Report)
	params.RunRef = strings.TrimSpace(params.RunRef)
	if params.TaskID == "" || params.RoomID == "" || params.Report == "" {
		return TaskVerificationSubmitResult{}, errors.New("verification task, room, and report are required")
	}
	if err := s.requireRoomPrincipalAccess(ctx, params.RoomID, actor.ID); err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	if !validVerificationDecision(params.Decision) {
		return TaskVerificationSubmitResult{}, fmt.Errorf("invalid verification decision %q", params.Decision)
	}
	if utf8.RuneCountInString(params.Report) > MaxVerificationReportRunes {
		return TaskVerificationSubmitResult{}, fmt.Errorf("verification report exceeds %d characters", MaxVerificationReportRunes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("begin task verification: %w", err)
	}
	defer tx.Rollback()
	task, err := loadMessageTx(ctx, tx, params.TaskID)
	if err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	if task.RoomID != params.RoomID || task.Kind != MessageTask || strings.TrimSpace(task.TaskOwner) == "" {
		return TaskVerificationSubmitResult{}, errors.New("verification target must be an owned task in the room")
	}
	if !task.TaskVerificationRequired {
		return TaskVerificationSubmitResult{}, fmt.Errorf("%w: task does not require verification", ErrConflict)
	}
	if task.TaskState != string(TaskStateChecking) {
		return TaskVerificationSubmitResult{}, fmt.Errorf("%w: task must be checking before verification", ErrConflict)
	}
	if params.GoalRevision != task.TaskGoalRevision || params.CandidateRevision != task.TaskCandidateRevision {
		return TaskVerificationSubmitResult{}, fmt.Errorf(
			"%w: verifier revisions goal=%d candidate=%d do not match current goal=%d candidate=%d",
			ErrConflict, params.GoalRevision, params.CandidateRevision, task.TaskGoalRevision, task.TaskCandidateRevision,
		)
	}
	if params.RunRef == "" {
		return TaskVerificationSubmitResult{}, fmt.Errorf("%w: verification requires an independent run", ErrConflict)
	}
	verifierRun, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+`
		WHERE run.id = ? AND run.work_id = ?`, params.RunRef, task.ID))
	if err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("load verification run: %w", err)
	}
	if verifierRun.Kind != WorkRunVerifier || verifierRun.State != WorkRunCompleted ||
		verifierRun.GoalRevision != task.TaskGoalRevision || verifierRun.CandidateRevision != task.TaskCandidateRevision ||
		strings.TrimSpace(verifierRun.SessionRef) == "" || verifierRun.Profile == task.TaskOwner {
		return TaskVerificationSubmitResult{}, fmt.Errorf("%w: verification run is not an independent completed check of the current candidate", ErrConflict)
	}

	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, task.ID))
	if err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	if authorizeWorkActorTx(ctx, tx, work, actor) != nil && actor.ID != verifierRun.NamedAgentID {
		return TaskVerificationSubmitResult{}, ErrUnauthorized
	}

	attempt := 1
	var previousAttempt int
	err = tx.QueryRowContext(ctx, `SELECT attempt FROM task_verifications WHERE task_id = ?`, task.ID).Scan(&previousAttempt)
	if err == nil {
		attempt = previousAttempt + 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return TaskVerificationSubmitResult{}, fmt.Errorf("read prior task verification: %w", err)
	}
	now := fromMillis(toMillis(s.now()))
	verification := TaskVerification{
		TaskID: task.ID, RoomID: task.RoomID, OwnerID: task.TaskOwner,
		Decision: params.Decision, Report: params.Report, EvidenceRefs: params.EvidenceRefs, RunRef: params.RunRef, Attempt: attempt,
		GoalRevision: task.TaskGoalRevision, CandidateRevision: task.TaskCandidateRevision, UpdatedAt: now,
	}
	evidenceRefsJSON, err := json.Marshal(verification.EvidenceRefs)
	if err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("encode verification evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_verifications (
			task_id, room_id, owner_id, decision, report, evidence_refs_json, run_ref, attempt, goal_revision, candidate_revision, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			room_id = excluded.room_id,
			owner_id = excluded.owner_id,
			decision = excluded.decision,
			report = excluded.report,
			evidence_refs_json = excluded.evidence_refs_json,
			run_ref = excluded.run_ref,
			attempt = excluded.attempt,
			goal_revision = excluded.goal_revision,
			candidate_revision = excluded.candidate_revision,
			updated_at = excluded.updated_at`,
		verification.TaskID, verification.RoomID, verification.OwnerID, verification.Decision,
		verification.Report, string(evidenceRefsJSON), nullableString(verification.RunRef), verification.Attempt, verification.GoalRevision, verification.CandidateRevision,
		toMillis(verification.UpdatedAt)); err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("persist task verification: %w", err)
	}
	taskState := TaskStateRevising
	switch verification.Decision {
	case VerificationPass:
		taskState = TaskStateChecking
	case VerificationUnknown:
		taskState = TaskStateNeedsHuman
	}
	if verification.Decision == VerificationBlock {
		var maxAttempts int
		if err := tx.QueryRowContext(ctx, `SELECT max_verifier_attempts FROM works WHERE id = ?`, task.ID).Scan(&maxAttempts); err == nil && verification.Attempt >= maxAttempts {
			taskState = TaskStateNeedsHuman
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state = ? WHERE id = ?`, taskState, task.ID); err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("project task verification state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET state = ?, verification_state = ?, verifier_attempts_used = ?,
			current_run_ref = NULL, updated_at = ? WHERE id = ?`,
		workStateFromTask(taskState), verification.Decision, verification.Attempt, toMillis(now), task.ID); err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("project durable work verification: %w", err)
	}
	if err := insertWorkEventTx(ctx, tx, WorkEvent{
		WorkID: task.ID, Kind: "verification", State: string(verification.Decision),
		Summary: verification.Report, GoalRevision: verification.GoalRevision,
		CandidateRevision: verification.CandidateRevision, CreatedAt: now,
	}); err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	delivery, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
		RoomID: task.RoomID, FromType: MemberAgent, FromID: "", ToAgentID: task.TaskOwner,
		Kind: CollaborationVerificationFeedback, Body: verificationDeliveryBody(verification, taskState), WorkID: task.ID,
		RecipientNamedAgentID: task.TaskOwner, ArtifactRefs: verification.EvidenceRefs,
		SourceMessageID: task.ID, GoalRevision: verification.GoalRevision,
		CandidateRevision: verification.CandidateRevision, CreatedAt: now,
	})
	if err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	shouldDeliver, err := requestWakeTx(ctx, tx, task.TaskOwner, toMillis(now))
	if err != nil {
		return TaskVerificationSubmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskVerificationSubmitResult{}, fmt.Errorf("commit task verification: %w", err)
	}
	if shouldDeliver && s.wake != nil {
		s.wake.Deliver(task.TaskOwner)
	}
	return TaskVerificationSubmitResult{Verification: verification, Delivery: delivery}, nil
}

func (s *Service) GetTaskVerification(ctx context.Context, taskID string) (TaskVerification, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskVerification{}, errors.New("verification task id is required")
	}
	var result TaskVerification
	var updatedAt int64
	var evidenceRefsJSON string
	if err := s.db.QueryRowContext(ctx, `
		SELECT task_id, room_id, owner_id, decision, report, evidence_refs_json, COALESCE(run_ref, ''), attempt, goal_revision, candidate_revision, updated_at
		FROM task_verifications WHERE task_id = ?`, taskID).Scan(
		&result.TaskID, &result.RoomID, &result.OwnerID, &result.Decision,
		&result.Report, &evidenceRefsJSON, &result.RunRef, &result.Attempt, &result.GoalRevision, &result.CandidateRevision, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskVerification{}, fmt.Errorf("%w: task verification %q", ErrNotFound, taskID)
		}
		return TaskVerification{}, fmt.Errorf("get task verification: %w", err)
	}
	result.UpdatedAt = fromMillis(updatedAt)
	if err := json.Unmarshal([]byte(evidenceRefsJSON), &result.EvidenceRefs); err != nil {
		return TaskVerification{}, fmt.Errorf("decode verification evidence: %w", err)
	}
	return result, nil
}

func validVerificationDecision(decision VerificationDecision) bool {
	switch decision {
	case VerificationPass, VerificationBlock, VerificationUnknown:
		return true
	default:
		return false
	}
}

func verificationDeliveryBody(verification TaskVerification, taskState TaskState) string {
	action := "The task is now revising. Start the repair, mark it doing, and submit it for verification again."
	switch verification.Decision {
	case VerificationPass:
		action = "The candidate is verified. Publish the result in the task thread, then mark the task done."
	case VerificationUnknown:
		action = "The task now needs human input. Supply the missing evidence or ask the user for the decision that cannot be made safely."
	}
	if taskState == TaskStateNeedsHuman {
		action = "The task now needs human input. Do not retry or lower the standard; ask the user to clarify or decide."
	}
	return fmt.Sprintf("Verification %s for task %s goal %d candidate %d (attempt %d).\n\n%s\n\n%s",
		verification.Decision, verification.TaskID, verification.GoalRevision, verification.CandidateRevision,
		verification.Attempt, verification.Report, action)
}
