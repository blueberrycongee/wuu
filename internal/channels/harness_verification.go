package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type HarnessVerificationReport struct {
	Decision     VerificationDecision `json:"decision"`
	Report       string               `json:"report"`
	EvidenceRefs []string             `json:"evidence_refs"`
}

// RecordHarnessVerification records the reviewer's conclusion; it becomes a
// verification receipt only after that exact execution successfully finishes.
func (c *AgentClient) RecordHarnessVerification(ctx context.Context, sessionID string, report HarnessVerificationReport) error {
	if _, err := c.service.AuthenticatePrincipal(ctx, c.agentID, c.token); err != nil {
		return err
	}
	if report.Decision != VerificationPass && report.Decision != VerificationBlock && report.Decision != VerificationUnknown {
		return errors.New("decision must be pass, block or unknown")
	}
	if strings.TrimSpace(report.Report) == "" || utf8.RuneCountInString(report.Report) > MaxVerificationReportRunes {
		return errors.New("a bounded verification report is required")
	}
	link, err := c.service.HarnessLink(ctx, sessionID)
	if err != nil {
		return err
	}
	if !link.Active || link.AgentID != c.agentID || link.Purpose != CollaborationSessionVerification {
		return ErrUnauthorized
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	result, err := c.service.db.ExecContext(ctx, `INSERT OR REPLACE INTO harness_verification_reports(run_id,payload)
 SELECT run.id,? FROM work_runs run JOIN works work ON work.id=run.work_id
 WHERE run.session_ref=? AND run.work_id=? AND run.kind='verifier' AND run.state='running'
 AND run.goal_revision=work.goal_revision AND run.candidate_revision=work.candidate_revision AND work.state='checking'`, string(data), sessionID, link.WorkID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) HarnessVerificationReport(ctx context.Context, runID string) (*HarnessVerificationReport, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM harness_verification_reports WHERE run_id=?`, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report HarnessVerificationReport
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return nil, err
	}
	return &report, nil
}
