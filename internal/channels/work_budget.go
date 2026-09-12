package channels

import (
	"context"
	"database/sql"
	"fmt"
)

type tokenUsageReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Runs own their usage; result-processing turns without a Run are charged to
// their captured room/work. A mirrored turn record must never count twice.
func collaborationTokenUsage(ctx context.Context, reader tokenUsageReader, roomID, workID string) (input, output int64, err error) {
	where, args := " WHERE 1=1", []any{}
	if roomID != "" {
		where += " AND room_id=?"
		args = append(args, roomID)
	}
	if workID != "" {
		where += " AND work_id=?"
		args = append(args, workID)
	}
	err = reader.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM (
		SELECT work.room_id,run.work_id,run.input_tokens,run.output_tokens
		FROM work_runs run JOIN works work ON work.id=run.work_id
		UNION ALL
		SELECT room_id,work_id,input_tokens,output_tokens FROM collaboration_turn_scopes WHERE run_id=''
	)`+where, args...).Scan(&input, &output)
	return
}

func (s *Service) checkCollaborationTokenBudgetTx(ctx context.Context, tx *sql.Tx, roomID string, work *Work) error {
	if work != nil && (work.MaxInputTokens > 0 || work.MaxOutputTokens > 0) {
		input, output, err := collaborationTokenUsage(ctx, tx, roomID, work.ID)
		if err != nil {
			return fmt.Errorf("read work usage budget: %w", err)
		}
		if work.MaxInputTokens > 0 && input >= work.MaxInputTokens || work.MaxOutputTokens > 0 && output >= work.MaxOutputTokens {
			return fmt.Errorf("%w: work token budget exhausted", ErrConflict)
		}
	}
	if roomID != "" && (s.roomInputTokenLimit > 0 || s.roomOutputTokenLimit > 0) {
		input, output, err := collaborationTokenUsage(ctx, tx, roomID, "")
		if err != nil {
			return fmt.Errorf("read room usage budget: %w", err)
		}
		if s.roomInputTokenLimit > 0 && input >= s.roomInputTokenLimit || s.roomOutputTokenLimit > 0 && output >= s.roomOutputTokenLimit {
			return fmt.Errorf("%w: room token budget exhausted", ErrConflict)
		}
	}
	return nil
}
