package session

import (
	"database/sql"
	"errors"
)

// FusionLifecycleSessions returns a conversation followed by every Sidekick it
// owns, including archived Sidekicks and pairs with Fusion disabled. A Sidekick
// with a surviving Lead must be archived, restored or deleted through that Lead.
func FusionLifecycleSessions(dir, id string) ([]Session, error) {
	db, found, err := openStoreForScan(dir)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lead, found, err := findSessionTx(tx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	ids, err := fusionLifecycleIDs(tx, lead)
	if err != nil {
		return nil, err
	}
	members := []Session{lead}
	for _, sideID := range ids[1:] {
		side, _, err := findSessionTx(tx, sideID)
		if err != nil {
			return nil, err
		}
		members = append(members, side)
	}
	return members, nil
}

func fusionLifecycleIDs(tx *sql.Tx, lead Session) ([]string, error) {
	if lead.Source == "fusion-side" {
		_, found, err := findSessionTx(tx, lead.ParentID)
		if err != nil {
			return nil, err
		}
		if found {
			return nil, errors.New("archive, restore or delete Sidekick through its Fusion Lead")
		}
	}
	rows, err := tx.Query(`SELECT id FROM sessions WHERE source='fusion-side' AND parent_id=? ORDER BY id`, lead.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{lead.ID}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
