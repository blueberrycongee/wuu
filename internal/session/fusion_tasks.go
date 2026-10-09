package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

type FusionTaskState string

const (
	FusionTaskQueued         FusionTaskState = "queued"
	FusionTaskRunning        FusionTaskState = "running"
	FusionTaskAwaitingReview FusionTaskState = "awaiting_review"
	FusionTaskCompleted      FusionTaskState = "completed"
	FusionTaskFailed         FusionTaskState = "failed"
	FusionTaskCancelled      FusionTaskState = "cancelled"
)

// FusionTask correlates requirements, actual Side turns and Lead review. The
// transcript owns output; a task stores references instead of another history.
type FusionTask struct {
	ReviewRounds   int             `json:"review_rounds"`
	ID             string          `json:"task_id"`
	LeadID         string          `json:"lead_id"`
	SideID         string          `json:"side_id"`
	LeadTurnID     string          `json:"lead_turn_id"`
	Revision       int             `json:"revision"`
	State          FusionTaskState `json:"state"`
	ReadOnly       bool            `json:"read_only"`
	LatestClientID string          `json:"latest_client_id"`
	ReportID       string          `json:"report_id,omitempty"`
	ReportRevision int             `json:"report_revision,omitempty"`
	Feedback       string          `json:"feedback,omitempty"`
	StopReason     string          `json:"stop_reason,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

const fusionTaskColumns = `id,lead_id,side_id,lead_turn_id,revision,state,read_only,latest_client_id,report_id,report_revision,feedback,stop_reason,created_at,updated_at,review_rounds`

func scanFusionTask(row interface{ Scan(...any) error }) (FusionTask, error) {
	var t FusionTask
	err := row.Scan(&t.ID, &t.LeadID, &t.SideID, &t.LeadTurnID, &t.Revision, &t.State, &t.ReadOnly, &t.LatestClientID, &t.ReportID, &t.ReportRevision, &t.Feedback, &t.StopReason, &t.CreatedAt, &t.UpdatedAt, &t.ReviewRounds)
	return t, err
}

func ReadFusionTask(dir, id string) (FusionTask, bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return FusionTask{}, false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "fusion_tasks"); err != nil || !exists {
		return FusionTask{}, false, err
	}
	task, err := scanFusionTask(db.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return FusionTask{}, false, nil
	}
	return task, err == nil, err
}

func ListFusionTasks(dir, leadID string) ([]FusionTask, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "fusion_tasks"); err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE lead_id=? ORDER BY created_at,rowid`, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []FusionTask
	for rows.Next() {
		task, err := scanFusionTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func insertFusionDispatch(tx *sql.Tx, d FusionDispatch) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO fusion_dispatches(client_id,lead_id,lead_turn_id,side_id,delivery,created_at,task_id,revision,kind) VALUES(?,?,?,?,?,?,?,?,?)`, d.ClientID, d.LeadID, d.LeadTurnID, d.SideID, d.Delivery, timeText(time.Now().UTC()), d.TaskID, d.Revision, d.Kind)
	return err
}

// Admission and revision advancement share the inbox transaction. An accepted
// update therefore survives a Side completion racing its next step boundary.
func AdmitFusionTask(dir string, task FusionTask, d FusionDispatch, input InboxMessage, expectedRevision int) (FusionTask, error) {
	if task.ID != d.TaskID || task.LeadID != d.LeadID || task.SideID != d.SideID || d.ClientID != input.ClientID || d.SideID != input.SessionID || d.LeadID != input.RelatedSessionID {
		return FusionTask{}, errors.New("Fusion task does not match its input")
	}
	db, err := openStore(dir)
	if err != nil {
		return FusionTask{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return FusionTask{}, err
	}
	defer tx.Rollback()
	var replayID string
	err = tx.QueryRow(`SELECT task_id FROM fusion_dispatches WHERE client_id=?`, d.ClientID).Scan(&replayID)
	if err == nil {
		if replayID != task.ID {
			return FusionTask{}, errors.New("Fusion call belongs to another task")
		}
		return scanFusionTask(tx.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=?`, replayID))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FusionTask{}, err
	}
	now := timeText(time.Now().UTC())
	if expectedRevision == 0 {
		task = FusionTask{ID: task.ID, LeadID: task.LeadID, SideID: task.SideID, LeadTurnID: task.LeadTurnID, Revision: 1, State: FusionTaskQueued, ReadOnly: task.ReadOnly, LatestClientID: d.ClientID, CreatedAt: now, UpdatedAt: now}
		_, err = tx.Exec(`INSERT INTO fusion_tasks(`+fusionTaskColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.LeadID, task.SideID, task.LeadTurnID, task.Revision, task.State, task.ReadOnly, task.LatestClientID, "", 0, "", "", now, now, 0)
	} else {
		current, readErr := scanFusionTask(tx.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=?`, task.ID))
		if readErr != nil {
			return FusionTask{}, readErr
		}
		if current.Revision != expectedRevision {
			return FusionTask{}, errors.New("Fusion requirements changed; inspect the task before updating")
		}
		if current.State == FusionTaskCompleted || current.State == FusionTaskCancelled || (d.Kind == "update" && current.State != FusionTaskQueued && current.State != FusionTaskRunning) {
			return FusionTask{}, errors.New("Fusion execution already ended; review its report or delegate a new task")
		}
		feedback := ""
		if d.Kind == "review" {
			feedback = input.Content
			current.ReviewRounds++
		}
		current.Revision++
		if d.Kind == "review" {
			current.State = FusionTaskQueued
		}
		current.LatestClientID, current.UpdatedAt = d.ClientID, now
		current.Feedback = feedback
		task = current
		_, err = tx.Exec(`UPDATE fusion_tasks SET revision=?,state=?,latest_client_id=?,feedback=?,updated_at=?,review_rounds=? WHERE id=?`, task.Revision, task.State, task.LatestClientID, task.Feedback, now, task.ReviewRounds, task.ID)
	}
	if err != nil {
		return FusionTask{}, err
	}
	d.Revision = task.Revision
	input.Content = "Fusion task " + task.ID + ", requirements revision " + strconv.Itoa(task.Revision) + ".\n\n" + input.Content
	if err := insertInbox(tx, input); err != nil {
		return FusionTask{}, err
	}
	if err := validateInboxControls(tx, input.ClientID); err != nil {
		return FusionTask{}, err
	}
	if err := insertFusionDispatch(tx, d); err != nil {
		return FusionTask{}, err
	}
	if _, err := tx.Exec(`UPDATE fusion_dispatches SET delivery='settled' WHERE task_id=? AND client_id!=?`, task.ID, d.ClientID); err != nil {
		return FusionTask{}, err
	}
	return task, tx.Commit()
}

func MarkFusionTaskRunning(dir, id string, revision int) error {
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`UPDATE fusion_tasks SET state='running',updated_at=? WHERE id=? AND revision=? AND state='queued'`, timeText(time.Now().UTC()), id, revision)
	return err
}

func RecordFusionReport(dir, id, clientID string, revision int, reportID string, state FusionTaskState) (bool, error) {
	if state != FusionTaskAwaitingReview && state != FusionTaskFailed {
		return false, errors.New("invalid Fusion report state")
	}
	db, err := openStore(dir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := validateInboxControls(tx, clientID); err != nil {
		if errors.Is(err, ErrControlChanged) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.Exec(`UPDATE fusion_tasks SET state=?,report_id=?,report_revision=?,updated_at=? WHERE id=? AND revision=? AND latest_client_id=? AND state IN ('queued','running')`, state, reportID, revision, timeText(time.Now().UTC()), id, revision, clientID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count > 0, tx.Commit()
}

func AcceptFusionReport(dir, id, reportID string, revision int, feedback string) error {
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	task, err := scanFusionTask(db.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=?`, id))
	if err != nil {
		return err
	}
	if task.State == FusionTaskCompleted && task.Revision == revision && task.ReportRevision == revision && task.ReportID == reportID {
		return nil
	}
	result, err := db.Exec(`UPDATE fusion_tasks SET state='completed',feedback=?,updated_at=? WHERE id=? AND revision=? AND report_revision=? AND report_id=? AND state='awaiting_review'`, feedback, timeText(time.Now().UTC()), id, revision, revision, reportID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("Fusion report is stale or not ready for acceptance; inspect the task")
	}
	return nil
}

func CancelFusionTasks(dir, leadID, reason string) error {
	_, err := cancelFusionTasks(dir, leadID, "", 0, reason)
	return err
}

func CancelFusionTask(dir, leadID, taskID string, revision int, reason string) (bool, error) {
	return cancelFusionTasks(dir, leadID, taskID, revision, reason)
}

// Cancellation, control fencing and receipt settlement are one admission
// boundary. Another host cannot admit an input with the pre-Stop fence.
func cancelFusionTasks(dir, leadID, taskID string, revision int, reason string) (bool, error) {
	db, err := openStore(dir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if taskID != "" {
		task, err := scanFusionTask(tx.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=? AND lead_id=?`, taskID, leadID))
		if err != nil {
			return false, err
		}
		if task.State == FusionTaskCompleted || task.State == FusionTaskCancelled || task.State == FusionTaskFailed {
			return false, nil
		}
		if task.Revision != revision {
			return false, errors.New("Fusion requirements changed before Stop; inspect the current task")
		}
	}
	if _, err := tx.Exec(`UPDATE fusion_tasks SET state='cancelled',stop_reason=?,updated_at=? WHERE lead_id=? AND state IN ('queued','running','awaiting_review')`, reason, timeText(time.Now().UTC()), leadID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE session_controls SET revision=revision+1 WHERE manager_id=? AND state='active' AND session_id IN (SELECT id FROM sessions WHERE source='fusion-side' AND parent_id=?)`, leadID, leadID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE fusion_dispatches SET delivery='settled' WHERE lead_id=?`, leadID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// The report identity, rather than each steering message, owns background
// delivery. The original control fence is checked in the same transaction.
func EnqueueFusionTaskResult(dir string, task FusionTask, content string) error {
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
	current, err := scanFusionTask(tx.QueryRow(`SELECT `+fusionTaskColumns+` FROM fusion_tasks WHERE id=?`, task.ID))
	if err != nil {
		return err
	}
	if current.Revision != task.Revision || current.ReportID != task.ReportID || (current.State != FusionTaskAwaitingReview && current.State != FusionTaskFailed) {
		return nil
	}
	if err := validateInboxControls(tx, current.LatestClientID); err != nil {
		return err
	}
	var delivery, controls string
	if err := tx.QueryRow(`SELECT d.delivery,i.controls_json FROM fusion_dispatches d JOIN session_inbox i ON i.client_id=d.client_id WHERE d.client_id=?`, current.LatestClientID).Scan(&delivery, &controls); err != nil {
		return err
	}
	if delivery != "background" {
		return nil
	}
	input := InboxMessage{ClientID: "fusion-task-result:" + task.ID + ":" + task.ReportID, SessionID: task.LeadID, RelatedSessionID: task.SideID, Cause: "fusion_result", Content: content, Wake: true}
	if err := json.Unmarshal([]byte(controls), &input.Controls); err != nil {
		return err
	}
	if err := insertInbox(tx, input); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE fusion_dispatches SET delivery='settled' WHERE task_id=?`, task.ID); err != nil {
		return err
	}
	return tx.Commit()
}
