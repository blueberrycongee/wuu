package channels

import (
	"context"
	"database/sql"
	"fmt"
)

type WorkDiagnostics struct {
	RoomID                         string  `json:"room_id,omitempty"`
	WorkCount                      int     `json:"work_count"`
	CompletedCount                 int     `json:"completed_count"`
	ProducerOnlyCompletedCount     int     `json:"producer_only_completed_count"`
	ProducerVerifierCompletedCount int     `json:"producer_verifier_completed_count"`
	VerifierRunCount               int     `json:"verifier_run_count"`
	VerifierBlockCount             int     `json:"verifier_block_count"`
	VerifierUnknownCount           int     `json:"verifier_unknown_count"`
	VerifierBlockRate              float64 `json:"verifier_block_rate"`
	VerifierExtraLatencyMillis     int64   `json:"verifier_extra_latency_ms"`
	InputTokens                    int64   `json:"input_tokens"`
	OutputTokens                   int64   `json:"output_tokens"`
	ChecksRerun                    int     `json:"checks_rerun"`
	FindingsCount                  int     `json:"findings_count"`
	RepairSuccessCount             int     `json:"repair_success_count"`
	CorrectionCount                int     `json:"correction_count"`
	RecoveryFailureCount           int     `json:"recovery_failure_count"`
}

func (s *Service) GetWorkDiagnostics(ctx context.Context, roomID string) (WorkDiagnostics, error) {
	diagnostics := WorkDiagnostics{RoomID: roomID}
	where, arg := "", []any{}
	if roomID != "" {
		where, arg = " WHERE room_id = ?", []any{roomID}
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN state = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'completed' AND verification_required = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'completed' AND verification_required = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN goal_revision > 1 THEN goal_revision - 1 ELSE 0 END), 0)
		FROM works`+where, arg...).Scan(&diagnostics.WorkCount, &diagnostics.CompletedCount,
		&diagnostics.ProducerOnlyCompletedCount, &diagnostics.ProducerVerifierCompletedCount,
		&diagnostics.CorrectionCount); err != nil {
		return WorkDiagnostics{}, fmt.Errorf("summarize work outcomes: %w", err)
	}
	runWhere := ""
	runArgs := []any{}
	if roomID != "" {
		runWhere = " AND work.room_id = ?"
		runArgs = append(runArgs, roomID)
	}
	var latency sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN run.outcome = 'block' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN run.outcome = 'unknown' THEN 1 ELSE 0 END), 0),
			SUM(CASE WHEN run.ended_at IS NOT NULL AND run.started_at IS NOT NULL THEN run.ended_at - run.started_at ELSE 0 END),
			COALESCE(SUM(run.checks_rerun), 0), COALESCE(SUM(run.findings_count), 0),
			COALESCE(SUM(CASE WHEN run.repair_outcome IN ('pass', 'fixed', 'completed') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN run.state = 'interrupted' AND run.outcome LIKE '%recovery%' THEN 1 ELSE 0 END), 0)
		FROM work_runs run JOIN works work ON work.id = run.work_id
		WHERE run.kind = 'verifier'`+runWhere, runArgs...).Scan(
		&diagnostics.VerifierRunCount, &diagnostics.VerifierBlockCount,
		&diagnostics.VerifierUnknownCount, &latency,
		&diagnostics.ChecksRerun, &diagnostics.FindingsCount,
		&diagnostics.RepairSuccessCount, &diagnostics.RecoveryFailureCount,
	); err != nil {
		return WorkDiagnostics{}, fmt.Errorf("summarize verifier runs: %w", err)
	}
	input, output, err := collaborationTokenUsage(ctx, s.db, roomID, "")
	if err != nil {
		return WorkDiagnostics{}, fmt.Errorf("summarize collaboration usage: %w", err)
	}
	diagnostics.InputTokens, diagnostics.OutputTokens = input, output
	if latency.Valid {
		diagnostics.VerifierExtraLatencyMillis = latency.Int64
	}
	if diagnostics.VerifierRunCount > 0 {
		diagnostics.VerifierBlockRate = float64(diagnostics.VerifierBlockCount) / float64(diagnostics.VerifierRunCount)
	}
	return diagnostics, nil
}
