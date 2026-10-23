package session

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) UpsertParticipant(person participant.Participant) error {
	now := time.Now().UTC()
	if person.CreatedAt.IsZero() {
		person.CreatedAt = now
	}
	if person.UpdatedAt.IsZero() {
		person.UpdatedAt = now
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if person.Kind == participant.KindNamed && person.RetiredAt == nil {
			rows, err := readDocumentRows[participant.Participant](tx, "participants", "")
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ID != person.ID && row.Kind == participant.KindNamed && row.RetiredAt == nil && row.Name == person.Name {
					return fmt.Errorf("active named participant %q already exists", person.Name)
				}
			}
		}
		var prior participant.Participant
		found, err := tx.Get(documentNamespace("participants"), person.ID, &prior)
		if err != nil {
			return err
		}
		current := person
		if found {
			current.CreatedAt = prior.CreatedAt
		}
		return tx.Put(documentNamespace("participants"), current.ID, current)
	})
}

func (s *Store) UpsertParticipantRun(run ParticipantRun) error {
	run.ParticipantID = strings.TrimSpace(run.ParticipantID)
	if run.ParticipantID == "" {
		return errors.New("participant_id is required")
	}
	run.ID, run.AgentID = strings.TrimSpace(run.ID), strings.TrimSpace(run.AgentID)
	if run.ID == "" {
		run.ID = run.AgentID
	}
	if run.ID == "" {
		run.ID = NewID()
	}
	run.TaskID, run.SessionID = strings.TrimSpace(run.TaskID), strings.TrimSpace(run.SessionID)
	run.Summary, run.Outcome = strings.TrimSpace(run.Summary), strings.TrimSpace(run.Outcome)
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = now
	}
	return s.transaction(func(tx *persistence.Tx) error {
		var prior ParticipantRun
		found, err := tx.Get(documentNamespace("participant_runs"), run.ID, &prior)
		if err != nil {
			return err
		}
		current := run
		if found {
			current.CreatedAt = prior.CreatedAt
			live := func(outcome string) bool {
				return outcome == "" || outcome == "pending" || outcome == "queued" || outcome == "running"
			}
			if !live(prior.Outcome) && live(current.Outcome) {
				current.Outcome, current.Summary, current.UpdatedAt = prior.Outcome, prior.Summary, prior.UpdatedAt
			}
		}
		return tx.Put(documentNamespace("participant_runs"), current.ID, current)
	})
}

func (s *Store) CompleteParticipantRun(participantID, agentID, outcome, summary string) error {
	participantID, agentID = strings.TrimSpace(participantID), strings.TrimSpace(agentID)
	outcome, summary = strings.TrimSpace(outcome), strings.TrimSpace(summary)
	if participantID == "" {
		return errors.New("participant_id is required")
	}
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[ParticipantRun](tx, "participant_runs", "")
		if err != nil {
			return err
		}
		found := false
		for _, row := range rows {
			if row.ParticipantID != participantID || row.AgentID != agentID {
				continue
			}
			found = true
			row.Outcome, row.UpdatedAt = outcome, now
			if summary != "" {
				row.Summary = summary
			}
			if err := tx.Put(documentNamespace("participant_runs"), row.ID, row); err != nil {
				return err
			}
		}
		if found {
			return nil
		}
		var prior ParticipantRun
		if exists, err := tx.Get(documentNamespace("participant_runs"), agentID, &prior); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("participant run %q already belongs to another participant", agentID)
		}
		return tx.Put(documentNamespace("participant_runs"), agentID, ParticipantRun{ID: agentID, ParticipantID: participantID, AgentID: agentID, Outcome: outcome, Summary: summary, CreatedAt: now, UpdatedAt: now})
	})
}

func (s *Store) ListParticipantRuns(participantID string, limit int) ([]ParticipantRun, error) {
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return nil, errors.New("participant_id is required")
	}
	var result []ParticipantRun
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[ParticipantRun](tx, "participant_runs", "")
		if err != nil {
			return err
		}
		result = nil
		for _, row := range rows {
			if row.ParticipantID == participantID {
				result = append(result, row)
			}
		}
		sort.Slice(result, func(i, j int) bool {
			if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
				return result[i].CreatedAt.After(result[j].CreatedAt)
			}
			return result[i].ID > result[j].ID
		})
		if limit > 0 && len(result) > limit {
			result = result[:limit]
		}
		return nil
	})
	return result, err
}

func (s *Store) GetParticipant(id string) (participant.Participant, error) {
	var result participant.Participant
	err := s.transaction(func(tx *persistence.Tx) error {
		found, err := tx.Get(documentNamespace("participants"), strings.TrimSpace(id), &result)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %q", ErrParticipantNotFound, id)
		}
		return nil
	})
	return result, err
}

func (s *Store) ListParticipants(kind participant.Kind) ([]participant.Participant, error) {
	var result []participant.Participant
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[participant.Participant](tx, "participants", "")
		if err != nil {
			return err
		}
		result = nil
		for _, row := range rows {
			if row.RetiredAt == nil && (kind == "" || row.Kind == kind) {
				result = append(result, row)
			}
		}
		sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
		return nil
	})
	return result, err
}

func (s *Store) CountParticipantsByKind(kind participant.Kind) (int, error) {
	if kind == "" {
		return 0, errors.New("kind is required")
	}
	var count int
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[participant.Participant](tx, "participants", "")
		if err != nil {
			return err
		}
		count = 0
		for _, row := range rows {
			if row.Kind == kind {
				count++
			}
		}
		return nil
	})
	return count, err
}

func (s *Store) RetireParticipant(id string) error {
	id = strings.TrimSpace(id)
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		var row participant.Participant
		found, err := tx.Get(documentNamespace("participants"), id, &row)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %q", ErrParticipantNotFound, id)
		}
		if row.RetiredAt == nil {
			row.RetiredAt = &now
		}
		row.UpdatedAt = now
		return tx.Put(documentNamespace("participants"), id, row)
	})
}

func (s *Store) FindRetiredParticipantByName(kind participant.Kind, name string) (participant.Participant, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return participant.Participant{}, false, errors.New("name is required")
	}
	var result participant.Participant
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[participant.Participant](tx, "participants", "")
		if err != nil {
			return err
		}
		result, found = participant.Participant{}, false
		for _, row := range rows {
			if row.RetiredAt == nil || kind != "" && row.Kind != kind || !strings.EqualFold(strings.TrimSpace(row.Name), name) {
				continue
			}
			if !found || retiredAfter(row, result) {
				result, found = row, true
			}
		}
		return nil
	})
	return result, found, err
}
