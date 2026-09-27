package session

import (
	"database/sql"
	"encoding/json"
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
	// Cause names the event for clients that render the delivered message.
	Cause   string
	Content string
	// Wake starts a turn when the target is idle. Other messages wait for the
	// target's next turn, whatever starts it.
	Wake      bool
	CreatedAt time.Time
	// Controls fence queued input across human takeover and return.
	Controls []Control
}

// EnqueueInbox records a message once; repeating a client ID is a no-op.
func EnqueueInbox(dir string, message InboxMessage) error {
	if strings.TrimSpace(message.ClientID) == "" || strings.TrimSpace(message.SessionID) == "" {
		return errors.New("inbox message requires a client ID and target session")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	controls, err := json.Marshal(message.Controls)
	if err != nil {
		return err
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`INSERT OR IGNORE INTO session_inbox(client_id,session_id,related_session_id,cause,content,wake,created_at,controls_json) VALUES(?,?,?,?,?,?,?,?)`,
		message.ClientID, message.SessionID, message.RelatedSessionID, message.Cause, message.Content, message.Wake, timeText(message.CreatedAt), string(controls))
	return err
}

// SettleInbox records a client ID as handled without delivering anything, so
// a later retry of the same message is a no-op.
func SettleInbox(dir, clientID, sessionID string) error {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(sessionID) == "" {
		return errors.New("inbox message requires a client ID and target session")
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	now := timeText(time.Now().UTC())
	_, err = db.Exec(`INSERT OR IGNORE INTO session_inbox(client_id,session_id,content,created_at,delivered_at) VALUES(?,?,'',?,?) ON CONFLICT(client_id) DO UPDATE SET delivered_at=excluded.delivered_at`,
		clientID, sessionID, now, now)
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
	rows, err := db.Query(`SELECT client_id,session_id,related_session_id,cause,content,wake,created_at,controls_json FROM session_inbox
		WHERE session_id=? AND delivered_at IS NULL ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []InboxMessage
	for rows.Next() {
		var message InboxMessage
		var created, controls string
		if err := rows.Scan(&message.ClientID, &message.SessionID, &message.RelatedSessionID, &message.Cause, &message.Content, &message.Wake, &created, &controls); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(controls), &message.Controls); err != nil {
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

// ValidateInboxControls also fences input already admitted to an in-memory
// steer queue. Its sender may be taken over before the recipient consumes it.
func ValidateInboxControls(dir, clientID string) error {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return err
	}
	var encoded string
	err = db.QueryRow(`SELECT controls_json FROM session_inbox WHERE client_id=?`, clientID).Scan(&encoded)
	db.Close()
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var controls []Control
	if err := json.Unmarshal([]byte(encoded), &controls); err != nil {
		return err
	}
	for _, control := range controls {
		if err := ValidateControl(dir, control); err != nil {
			return err
		}
	}
	return nil
}
