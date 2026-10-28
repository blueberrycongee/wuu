// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func ReadProjectWork(dir, id string) (ProjectWork, error) {
	db, err := openStore(dir)
	if err != nil {
		return ProjectWork{}, err
	}
	defer db.Close()
	var encoded string
	err = db.QueryRow(`SELECT payload FROM project_work WHERE id=?`, id).Scan(&encoded)
	var work ProjectWork
	if err == nil {
		err = json.Unmarshal([]byte(encoded), &work)
	}
	return work, err
}

func ListProjectWork(dir, projectID string) ([]ProjectWork, error) {
	return listProjectWork(dir, projectID, false)
}

// PendingProjectWork is the durable host outbox, including creation before a
// member or inbox row exists. The project scheduler retries until reconciled.
func PendingProjectWork(dir, projectID string) ([]ProjectWork, error) {
	return listProjectWork(dir, projectID, true)
}

func ReconcileProjectWork(dir, id string, version int) error {
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE project_work SET reconciled_version=? WHERE id=? AND version=?`, version, id, version)
	return err
}

func listProjectWork(dir, projectID string, pending bool) ([]ProjectWork, error) {
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query := `SELECT payload FROM project_work WHERE project_id=?`
	if pending {
		query += ` AND version>reconciled_version`
	}
	rows, err := db.Query(query+` ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	work := make([]ProjectWork, 0)
	for rows.Next() {
		var encoded string
		var item ProjectWork
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &item); err != nil {
			return nil, err
		}
		work = append(work, item)
	}
	return work, rows.Err()
}

func ProjectWorkOperation(dir, operationID string) (ProjectWork, bool, error) {
	db, err := openStore(dir)
	if err != nil {
		return ProjectWork{}, false, err
	}
	defer db.Close()
	var encoded string
	err = db.QueryRow(`SELECT payload FROM project_work_operations WHERE id=?`, operationID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectWork{}, false, nil
	}
	var work ProjectWork
	if err == nil {
		err = json.Unmarshal([]byte(encoded), &work)
	}
	return work, err == nil, err
}

// SaveProjectWork atomically saves a revision and its idempotency receipt. The
// caller drains the persisted outbox after commit and on startup.
func SaveProjectWork(dir string, work ProjectWork, version int, operationID string) (ProjectWork, error) {
	if work.ID == "" || work.ProjectID == "" || operationID == "" {
		return ProjectWork{}, errors.New("work and operation identities are required")
	}
	db, err := openStore(dir)
	if err != nil {
		return work, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return work, err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRow(`SELECT payload FROM project_work_operations WHERE id=?`, operationID).Scan(&previous)
	if err == nil {
		err = json.Unmarshal([]byte(previous), &work)
		return work, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return work, err
	}
	work.Version = version + 1
	work.UpdatedAt = time.Now().UTC()
	if work.CreatedAt.IsZero() {
		work.CreatedAt = work.UpdatedAt
	}
	encoded, err := json.Marshal(work)
	if err != nil {
		return work, err
	}
	var result sql.Result
	if version == 0 {
		result, err = tx.Exec(`INSERT OR IGNORE INTO project_work(id,project_id,version,revision,phase,created_at,payload) VALUES(?,?,?,?,?,?,?)`, work.ID, work.ProjectID, work.Version, work.Revision, work.Phase, timeText(work.CreatedAt), string(encoded))
	} else {
		result, err = tx.Exec(`UPDATE project_work SET version=?,revision=?,phase=?,payload=? WHERE id=? AND project_id=? AND version=?`, work.Version, work.Revision, work.Phase, string(encoded), work.ID, work.ProjectID, version)
	}
	if err != nil {
		return work, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return work, err
	}
	if n != 1 {
		return work, ErrProjectWorkChanged
	}
	if _, err = tx.Exec(`INSERT INTO project_work_operations(id,work_id,payload) VALUES(?,?,?)`, operationID, work.ID, string(encoded)); err != nil {
		return work, err
	}
	return work, tx.Commit()
}
