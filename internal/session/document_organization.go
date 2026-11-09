package session

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *documentStore) ListOrganization() (SessionOrganization, error) {
	var folders []OrganizationGroup
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		folders, err = readDocumentRows[OrganizationGroup](tx, "session_folders", "")
		return err
	})
	sort.Slice(folders, func(i, j int) bool {
		a, b := folders[i], folders[j]
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return SessionOrganization{Folders: folders}, err
}

func (s *documentStore) CreateFolder(name string) (OrganizationGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return OrganizationGroup{}, errors.New("group name is required")
	}
	now := time.Now().UTC()
	group := OrganizationGroup{ID: NewID(), Name: name, CreatedAt: now, UpdatedAt: now}
	err := s.transaction(func(tx *persistence.Tx) error {
		folders, err := readDocumentRows[OrganizationGroup](tx, "session_folders", "")
		if err != nil {
			return err
		}
		group.SortOrder = 0
		for _, folder := range folders {
			if folder.SortOrder >= group.SortOrder {
				group.SortOrder = folder.SortOrder + 1
			}
		}
		return tx.Put(documentNamespace("session_folders"), group.ID, group)
	})
	return group, err
}

func (s *documentStore) RenameFolder(id, name string) (OrganizationGroup, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" {
		return OrganizationGroup{}, errors.New("group id and name are required")
	}
	var group OrganizationGroup
	now := time.Now().UTC()
	err := s.transaction(func(tx *persistence.Tx) error {
		found, err := tx.Get(documentNamespace("session_folders"), id, &group)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("folder not found: %q", id)
		}
		group.Name, group.UpdatedAt = name, now
		return tx.Put(documentNamespace("session_folders"), id, group)
	})
	return group, err
}

func (s *documentStore) DeleteFolder(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("group id is required")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if err := validateFolderDocument(tx, id); err != nil {
			return err
		}
		rows, err := readDocumentRows[Session](tx, "sessions", "")
		if err != nil {
			return err
		}
		for _, sess := range rows {
			if sess.FolderID == id {
				sess.FolderID = ""
				if err := putSessionDocument(tx, sess); err != nil {
					return err
				}
			}
		}
		return tx.Delete(documentNamespace("session_folders"), id)
	})
}

func (s *documentStore) ReorderFolders(ids []string) error {
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[OrganizationGroup](tx, "session_folders", "")
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return errors.New("reorder must include every group exactly once")
		}
		existing := make(map[string]OrganizationGroup, len(rows))
		for _, row := range rows {
			existing[row.ID] = row
		}
		seen := make(map[string]bool, len(ids))
		for order, id := range ids {
			id = strings.TrimSpace(id)
			group, found := existing[id]
			if !found {
				return fmt.Errorf("folder not found: %q", id)
			}
			if seen[id] {
				return fmt.Errorf("duplicate folder: %q", id)
			}
			seen[id] = true
			group.SortOrder, group.UpdatedAt = order, now
			if err := tx.Put(documentNamespace("session_folders"), id, group); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *documentStore) UpdateOrganization(id string, folderID *string) (Session, error) {
	return s.updateSessionOrganization(id, folderID, nil)
}

func (s *documentStore) updateSessionOrganization(id string, folderID *string, pinned *bool) (Session, error) {
	id = strings.TrimSpace(id)
	var result Session
	now := time.Now().UTC()
	err := s.transaction(func(tx *persistence.Tx) error {
		sess, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if folderID != nil {
			value := strings.TrimSpace(*folderID)
			if err := validateFolderDocument(tx, value); err != nil {
				return err
			}
			sess.FolderID = value
		}
		if pinned != nil {
			sess.PinnedAt = nil
			if *pinned {
				sess.PinnedAt = &now
			}
		}
		if err := putSessionDocument(tx, sess); err != nil {
			return err
		}
		result = sess
		return nil
	})
	return result, err
}

func validateFolderDocument(tx *persistence.Tx, id string) error {
	if id == "" {
		return nil
	}
	var group OrganizationGroup
	found, err := tx.Get(documentNamespace("session_folders"), id, &group)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("folder not found: %q", id)
	}
	return nil
}
