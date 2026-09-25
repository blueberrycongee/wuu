package channels

import (
	"context"
	"fmt"
)

// StartHarnessWorkRun records execution owned by an ordinary managed session.
// Only the host calls this; model tools cannot select the anonymous run path.
func (s *Service) StartHarnessWorkRun(ctx context.Context, sessionID, requestID string) (WorkRun, error) {
	link, err := s.HarnessLink(ctx, sessionID)
	if err != nil {
		return WorkRun{}, err
	}
	client, err := s.BindAgent(ctx, link.AgentID)
	if err != nil {
		return WorkRun{}, err
	}
	kind := WorkRunProducer
	if link.Purpose == CollaborationSessionVerification {
		kind = WorkRunVerifier
	}
	return s.StartWorkRun(ctx, WorkRunStartParams{harness: true, WorkID: link.WorkID, SessionRef: sessionID, Kind: kind, RequestID: requestID, AgentID: client.agentID, Token: client.token})
}

// FinishHarnessWorkRun ties accounting and completion to the accepted run and
// turn. Replaying an outbox result cannot attach it to a newer execution.
func (s *Service) FinishHarnessWorkRun(ctx context.Context, sessionID, turnID string, params WorkRunFinishParams) (WorkRun, error) {
	link, err := s.HarnessLink(ctx, sessionID)
	if err != nil {
		return WorkRun{}, err
	}
	client, err := s.BindAgent(ctx, link.AgentID)
	if err != nil {
		return WorkRun{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE work_runs SET turn_id=? WHERE id=? AND work_id=? AND session_ref=? AND (turn_id IS NULL OR turn_id='' OR turn_id=?)`, turnID, params.RunID, link.WorkID, sessionID, turnID)
	if err != nil {
		return WorkRun{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return WorkRun{}, err
	}
	if count != 1 {
		return WorkRun{}, fmt.Errorf("%w: execution run does not match its turn", ErrConflict)
	}
	params.AgentID, params.Token, params.WorkID = client.agentID, client.token, link.WorkID
	params.RequestID = "harness-result:" + sessionID + ":" + turnID
	return s.FinishWorkRun(ctx, params)
}
