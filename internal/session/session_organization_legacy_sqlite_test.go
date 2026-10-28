// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func ListOrganization(sessDir string) (SessionOrganization, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return SessionOrganization{}, err
	}
	defer db.Close()
	folders, err := listFolders(db)
	if err != nil {
		return SessionOrganization{}, err
	}
	return SessionOrganization{Folders: folders}, nil
}

func listFolders(db *sql.DB) ([]OrganizationGroup, error) {
	rows, err := db.Query(`SELECT id, name, sort_order, created_at, updated_at FROM session_folders ORDER BY sort_order, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()
	groups := make([]OrganizationGroup, 0)
	for rows.Next() {
		var group OrganizationGroup
		var createdAt, updatedAt string
		if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan folder: %w", err)
		}
		group.CreatedAt = parseTime(createdAt)
		group.UpdatedAt = parseTime(updatedAt)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func CreateFolder(sessDir, name string) (OrganizationGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return OrganizationGroup{}, errors.New("group name is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return OrganizationGroup{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	now := time.Now().UTC()
	group := OrganizationGroup{ID: NewID(), Name: name, CreatedAt: now, UpdatedAt: now}
	if err := db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM session_folders`).Scan(&group.SortOrder); err != nil {
		return OrganizationGroup{}, fmt.Errorf("next folder order: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO session_folders (id, name, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, group.ID, group.Name, group.SortOrder, timeText(now), timeText(now)); err != nil {
		return OrganizationGroup{}, fmt.Errorf("create folder: %w", err)
	}
	return group, nil
}

func RenameFolder(sessDir, id, name string) (OrganizationGroup, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" {
		return OrganizationGroup{}, errors.New("group id and name are required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return OrganizationGroup{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	now := time.Now().UTC()
	result, err := db.Exec(`UPDATE session_folders SET name = ?, updated_at = ? WHERE id = ?`, name, timeText(now), id)
	if err != nil {
		return OrganizationGroup{}, fmt.Errorf("rename folder: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return OrganizationGroup{}, fmt.Errorf("folder not found: %q", id)
	}
	var group OrganizationGroup
	var createdAt string
	if err := db.QueryRow(`SELECT id, name, sort_order, created_at FROM session_folders WHERE id = ?`, id).Scan(&group.ID, &group.Name, &group.SortOrder, &createdAt); err != nil {
		return OrganizationGroup{}, err
	}
	group.CreatedAt, group.UpdatedAt = parseTime(createdAt), now
	return group, nil
}

func DeleteFolder(sessDir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("group id is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sessions SET folder_id = '' WHERE folder_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM session_folders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("folder not found: %q", id)
	}
	return tx.Commit()
}

func ReorderFolders(sessDir string, ids []string) error {
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM session_folders`)
	if err != nil {
		return err
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) != len(existing) {
		return errors.New("reorder must include every group exactly once")
	}
	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(ids))
	for index, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if _, ok := existing[id]; !ok {
			return fmt.Errorf("folder not found: %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate folder: %q", id)
		}
		seen[id] = struct{}{}
		if _, err := tx.Exec(`UPDATE session_folders SET sort_order = ?, updated_at = ? WHERE id = ?`, index, timeText(now), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func UpdateOrganization(sessDir, id string, folderID *string) (Session, error) {
	return updateSessionOrganization(sessDir, id, folderID, nil)
}

func updateSessionOrganization(sessDir, id string, folderID *string, pinned *bool) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	db, err := openStore(sessDir)
	if err != nil {
		return Session{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	sess, ok, err := findSessionTx(tx, id)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	if folderID != nil {
		value := strings.TrimSpace(*folderID)
		if err := validateFolderIDTx(tx, value); err != nil {
			return Session{}, err
		}
		sess.FolderID = value
	}
	if pinned != nil {
		if *pinned {
			now := time.Now().UTC()
			sess.PinnedAt = &now
		} else {
			sess.PinnedAt = nil
		}
	}
	if err := updateSessionTx(tx, sess); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func validateFolderIDTx(tx *sql.Tx, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	var found string
	err := tx.QueryRow(`SELECT id FROM session_folders WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("folder not found: %q", id)
	}
	return err
}
