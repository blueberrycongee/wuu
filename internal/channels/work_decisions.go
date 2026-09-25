package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// WorkDecisionDocument is an immutable version of the shared implementation brief.
type WorkDecisionDocument struct {
	Version      int      `json:"version"`
	GoalRevision int      `json:"goal_revision"`
	Goal         string   `json:"goal"`
	Constraints  string   `json:"constraints"`
	Decisions    []string `json:"decisions"`
}

func (s *Service) migrateWorkDecisions() error {
	_, err := s.db.Exec(`
 CREATE TABLE IF NOT EXISTS work_decision_versions (
 work_id TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
 version INTEGER NOT NULL, goal_revision INTEGER NOT NULL,
 goal TEXT NOT NULL, constraints TEXT NOT NULL, decisions_json TEXT NOT NULL,
 PRIMARY KEY(work_id,version));
 INSERT OR IGNORE INTO work_decision_versions
 SELECT id,1,goal_revision,brief,constraints,decisions_json FROM works;
 CREATE TRIGGER IF NOT EXISTS work_revision_update AFTER UPDATE OF
 owner_named_agent_id,brief,goal_revision,candidate_revision,state,verification_state,constraints,decisions_json ON works
 BEGIN UPDATE works SET revision=OLD.revision+1 WHERE id=NEW.id; END;
 CREATE TRIGGER IF NOT EXISTS work_decision_insert AFTER INSERT ON works
 BEGIN INSERT INTO work_decision_versions VALUES(NEW.id,1,NEW.goal_revision,NEW.brief,NEW.constraints,NEW.decisions_json); END;
 CREATE TRIGGER IF NOT EXISTS work_decision_update AFTER UPDATE OF brief,goal_revision,constraints,decisions_json ON works
 WHEN NEW.brief!=OLD.brief OR NEW.goal_revision!=OLD.goal_revision OR NEW.constraints!=OLD.constraints OR NEW.decisions_json!=OLD.decisions_json
 BEGIN INSERT INTO work_decision_versions SELECT NEW.id,COALESCE(MAX(version),0)+1,NEW.goal_revision,NEW.brief,NEW.constraints,NEW.decisions_json FROM work_decision_versions WHERE work_id=NEW.id; END;`)
	return err
}

func appendWorkDecisionsTx(ctx context.Context, tx *sql.Tx, id string, additions []string) error {
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT decisions_json FROM works WHERE id=?`, id).Scan(&encoded); err != nil {
		return err
	}
	var decisions []string
	if err := json.Unmarshal([]byte(encoded), &decisions); err != nil {
		return err
	}
	for _, choice := range additions {
		decisions = appendUniqueStrings(decisions, strings.TrimSpace(choice))
	}
	data, err := json.Marshal(decisions)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE works SET decisions_json=? WHERE id=? AND decisions_json!=?`, string(data), id, string(data))
	return err
}

// RecordHarnessDecisions accepts choices only from the current execution run.
func (s *Service) RecordHarnessDecisions(ctx context.Context, sessionID, runID string, decisions []string) error {
	if len(decisions) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workID string
	err = tx.QueryRowContext(ctx, `SELECT work.id FROM works work JOIN work_runs run ON run.work_id=work.id
 WHERE run.id=? AND run.session_ref=? AND run.goal_revision=work.goal_revision AND run.kind='producer' AND run.state='completed' AND work.state NOT IN ('cancelled','failed','completed')`, runID, sessionID).Scan(&workID)
	if err != nil {
		return fmt.Errorf("record execution decisions: %w", err)
	}
	if err = appendWorkDecisionsTx(ctx, tx, workID, decisions); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) WorkDecisionHistory(ctx context.Context, workID string) ([]WorkDecisionDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version,goal_revision,goal,constraints,decisions_json FROM work_decision_versions WHERE work_id=? ORDER BY version`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []WorkDecisionDocument
	for rows.Next() {
		var d WorkDecisionDocument
		var data string
		if err := rows.Scan(&d.Version, &d.GoalRevision, &d.Goal, &d.Constraints, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &d.Decisions); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
