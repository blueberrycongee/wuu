package session

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

var ErrProjectWorkNotFound = errors.New("project work not found")

type projectWorkDocument struct {
	ProjectWork
	ReconciledVersion int `json:"reconciled_version"`
}

func (s *Store) ReadProjectWork(id string) (ProjectWork, error) {
	var work ProjectWork
	err := s.transaction(func(tx *persistence.Tx) error {
		var current projectWorkDocument
		found, err := tx.Get(documentNamespace("project_work"), id, &current)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %s", ErrProjectWorkNotFound, id)
		}
		work = current.ProjectWork
		return nil
	})
	return work, err
}

func (s *Store) ListProjectWork(projectID string) ([]ProjectWork, error) {
	return s.listProjectWork(projectID, false)
}
func (s *Store) PendingProjectWork(projectID string) ([]ProjectWork, error) {
	return s.listProjectWork(projectID, true)
}

func (s *Store) listProjectWork(projectID string, pending bool) ([]ProjectWork, error) {
	var result []ProjectWork
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[projectWorkDocument](tx, "project_work", "")
		if err != nil {
			return err
		}
		result = make([]ProjectWork, 0)
		for _, row := range rows {
			if row.ProjectID == projectID && (!pending || row.Version > row.ReconciledVersion) {
				result = append(result, row.ProjectWork)
			}
		}
		sort.Slice(result, func(i, j int) bool {
			if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
				return result[i].CreatedAt.Before(result[j].CreatedAt)
			}
			return result[i].ID < result[j].ID
		})
		return nil
	})
	return result, err
}

func (s *Store) ReconcileProjectWork(id string, version int) error {
	return s.transaction(func(tx *persistence.Tx) error {
		var row projectWorkDocument
		found, err := tx.Get(documentNamespace("project_work"), id, &row)
		if err != nil || !found || row.Version != version {
			return err
		}
		row.ReconciledVersion = version
		return tx.Put(documentNamespace("project_work"), id, row)
	})
}

func (s *Store) ProjectWorkOperation(operationID string) (ProjectWork, bool, error) {
	var result ProjectWork
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		found, err = tx.Get(documentNamespace("project_work_operations"), operationID, &result)
		return err
	})
	return result, found, err
}

func (s *Store) SaveProjectWork(work ProjectWork, version int, operationID string) (ProjectWork, error) {
	if work.ID == "" || work.ProjectID == "" || operationID == "" {
		return ProjectWork{}, errors.New("work and operation identities are required")
	}
	work.Version, work.UpdatedAt = version+1, time.Now().UTC()
	if work.CreatedAt.IsZero() {
		work.CreatedAt = work.UpdatedAt
	}
	var result ProjectWork
	err := s.transaction(func(tx *persistence.Tx) error {
		var receipt ProjectWork
		found, err := tx.Get(documentNamespace("project_work_operations"), operationID, &receipt)
		if err != nil {
			return err
		}
		if found {
			result = receipt
			return nil
		}
		if _, err := requireSessionDocument(tx, work.ProjectID); err != nil {
			return err
		}
		var prior projectWorkDocument
		found, err = tx.Get(documentNamespace("project_work"), work.ID, &prior)
		if err != nil {
			return err
		}
		if version == 0 && found || version != 0 && (!found || prior.ProjectID != work.ProjectID || prior.Version != version) {
			return ErrProjectWorkChanged
		}
		if err := tx.Put(documentNamespace("project_work"), work.ID, projectWorkDocument{ProjectWork: work, ReconciledVersion: prior.ReconciledVersion}); err != nil {
			return err
		}
		if err := tx.Put(documentNamespace("project_work_operations"), operationID, work); err != nil {
			return err
		}
		result = work
		return nil
	})
	return result, err
}
