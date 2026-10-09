package session

import (
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/config"
)

func fusionPairJSON(pair *config.FusionSelection) string {
	if pair == nil {
		return ""
	}
	encoded, _ := json.Marshal(pair)
	return string(encoded)
}

// SetFusion pins the pair once. Disabling and re-enabling a conversation keeps
// its original pair and Side history; settings changes affect new conversations.
func SetFusion(dir, id string, pair config.FusionSelection, enabled bool) (Session, error) {
	return updateMetadata(dir, id, false, func(s *Session) {
		if s.Fusion == nil {
			s.Fusion = &pair
		}
		s.FusionEnabled = enabled
		if enabled {
			s.Provider, s.Model = s.Fusion.Lead.Provider, s.Fusion.Lead.Model
			s.Variant, s.Effort = s.Fusion.Lead.Variant, s.Fusion.Lead.Effort
			s.Speed = ""
		}
	})
}

// FusionDispatch is the durable correlation between a tool call, its Lead turn
// and the Side input. Settled receipts prevent duplicate asynchronous reports.
type FusionDispatch struct {
	ClientID   string
	LeadID     string
	LeadTurnID string
	SideID     string
	Delivery   string
	TaskID     string
	Revision   int
	Kind       string
}

// EnqueueFusionDispatch atomically records a brief and its receipt. Replaying
// the same call cannot run it again, including after a server restart.
func EnqueueFusionDispatch(dir string, dispatch FusionDispatch, message InboxMessage) error {
	if dispatch.ClientID != message.ClientID || dispatch.SideID != message.SessionID || dispatch.LeadID != message.RelatedSessionID {
		return errors.New("Fusion dispatch does not match its inbox message")
	}
	db, err := openStore(dir)
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
	if err := insertInbox(tx, message); err != nil {
		return err
	}
	if err := validateInboxControls(tx, message.ClientID); err != nil {
		return err
	}
	if err := insertFusionDispatch(tx, dispatch); err != nil {
		return err
	}
	return tx.Commit()
}

func ListFusionDispatches(dir, leadID string) ([]FusionDispatch, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "fusion_dispatches"); err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`SELECT client_id,lead_id,lead_turn_id,side_id,delivery,task_id,revision,kind FROM fusion_dispatches WHERE lead_id=? ORDER BY created_at,rowid`, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []FusionDispatch
	for rows.Next() {
		var item FusionDispatch
		if err := rows.Scan(&item.ClientID, &item.LeadID, &item.LeadTurnID, &item.SideID, &item.Delivery, &item.TaskID, &item.Revision, &item.Kind); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// A terminal receipt cannot be reopened by a timeout racing Stop or completion.
func SetFusionDelivery(dir, clientID, delivery string) error {
	if delivery != "background" && delivery != "settled" {
		return errors.New("invalid Fusion delivery state")
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`UPDATE fusion_dispatches SET delivery=? WHERE client_id=? AND delivery!='settled'`, delivery, clientID)
	return err
}

// EnqueueFusionResult copies the dispatch's original control fence and settles
// its receipt in the same transaction. Stop can never authorize a stale result
// by advancing the Side's revision while completion is being admitted.
func EnqueueFusionResult(dir, clientID, content string) error {
	db, err := openStore(dir)
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
	if err := validateInboxControls(tx, clientID); err != nil {
		return err
	}
	var leadID, sideID, controls, delivery string
	if err := tx.QueryRow(`SELECT d.lead_id,d.side_id,i.controls_json,d.delivery FROM fusion_dispatches d JOIN session_inbox i ON i.client_id=d.client_id WHERE d.client_id=?`, clientID).Scan(&leadID, &sideID, &controls, &delivery); err != nil {
		return err
	}
	if delivery != "background" {
		return nil
	}
	message := InboxMessage{ClientID: "fusion-result:" + clientID, SessionID: leadID, RelatedSessionID: sideID, Cause: "fusion_result", Content: content, Wake: true}
	if err := json.Unmarshal([]byte(controls), &message.Controls); err != nil {
		return err
	}
	if err := insertInbox(tx, message); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE fusion_dispatches SET delivery='settled' WHERE client_id=?`, clientID); err != nil {
		return err
	}
	return tx.Commit()
}
