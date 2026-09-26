package session

import (
	"errors"
	"strings"
	"time"
)

// InboxMessage is host-produced input waiting to reach a session, such as a
// managed session's result for its project coordinator. A message counts as
// delivered once its client ID appears in the target's persisted history, so
// delivery can be retried after a restart without duplicating the input.
type InboxMessage struct {
	ClientID         string
	SessionID        string
	RelatedSessionID string
	Content          string
	CreatedAt        time.Time
}

// EnqueueInbox records a message once; repeating a client ID is a no-op.
func EnqueueInbox(dir string, message InboxMessage) error {
	if strings.TrimSpace(message.ClientID) == "" || strings.TrimSpace(message.SessionID) == "" {
		return errors.New("inbox message requires a client ID and target session")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`INSERT OR IGNORE INTO session_inbox(client_id,session_id,related_session_id,content,created_at) VALUES(?,?,?,?,?)`,
		message.ClientID, message.SessionID, message.RelatedSessionID, message.Content, timeText(message.CreatedAt))
	return err
}

// PendingInbox settles messages the target already persisted and returns the
// rest in creation order.
func PendingInbox(dir, sessionID string) ([]InboxMessage, error) {
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	_, err = db.Exec(`UPDATE session_inbox SET delivered_at=? WHERE session_id=? AND delivered_at IS NULL
		AND EXISTS(SELECT 1 FROM session_messages m WHERE m.session_id=session_inbox.session_id AND m.client_id=session_inbox.client_id)`,
		timeText(time.Now().UTC()), sessionID)
	storeWriteMu.Unlock()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT client_id,session_id,related_session_id,content,created_at FROM session_inbox
		WHERE session_id=? AND delivered_at IS NULL ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []InboxMessage
	for rows.Next() {
		var message InboxMessage
		var created string
		if err := rows.Scan(&message.ClientID, &message.SessionID, &message.RelatedSessionID, &message.Content, &created); err != nil {
			return nil, err
		}
		message.CreatedAt = parseTime(created)
		pending = append(pending, message)
	}
	return pending, rows.Err()
}

// InboxTargets lists sessions with undelivered messages.
func InboxTargets(dir string) ([]string, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_inbox"); err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`SELECT DISTINCT session_id FROM session_inbox WHERE delivered_at IS NULL ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		targets = append(targets, id)
	}
	return targets, rows.Err()
}

// InboxHas reports whether a client ID was recorded, delivered or not.
func InboxHas(dir, clientID string) (bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_inbox"); err != nil || !exists {
		return false, err
	}
	var found bool
	err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_inbox WHERE client_id=?)`, clientID).Scan(&found)
	return found, err
}
