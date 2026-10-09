package appserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
)

type FusionProgress struct {
	TurnID  string `json:"turn_id"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type FusionReport struct {
	ID       string     `json:"report_id"`
	Revision int        `json:"revision"`
	Status   TurnStatus `json:"status"`
	Output   string     `json:"output,omitempty"`
	Error    *TurnError `json:"error,omitempty"`
}

type FusionTaskView struct {
	session.FusionTask
	Report         *FusionReport   `json:"report,omitempty"`
	Progress       *FusionProgress `json:"progress,omitempty"`
	DeliveryState  string          `json:"delivery_state,omitempty"`
	WaitStatus     string          `json:"wait_status,omitempty"`
	TimedOut       bool            `json:"timed_out,omitempty"`
	Usage          FusionUsage     `json:"usage"`
	ElapsedMS      int64           `json:"elapsed_ms"`
	ExecutionState string          `json:"execution_state"`
}

type FusionUsage struct {
	LeadInputTokens  int `json:"lead_input_tokens"`
	LeadOutputTokens int `json:"lead_output_tokens"`
	SideInputTokens  int `json:"side_input_tokens"`
	SideOutputTokens int `json:"side_output_tokens"`
}

func (s *Server) fusionTaskForLead(leadID, taskID string) (session.FusionTask, error) {
	task, found, err := session.ReadFusionTask(s.rt.SessionDir, taskID)
	if err != nil {
		return task, err
	}
	if !found || task.LeadID != leadID {
		return task, errors.New("Fusion task does not belong to this Lead")
	}
	return task, nil
}

func (s *Server) inspectFusionTask(leadID, taskID string) (any, error) {
	if taskID != "" {
		task, err := s.fusionTaskForLead(leadID, taskID)
		if err != nil {
			return nil, err
		}
		return s.fusionTaskLiveView(task)
	}
	tasks, err := session.ListFusionTasks(s.rt.SessionDir, leadID)
	if err != nil {
		return nil, err
	}
	if len(tasks) > 0 {
		return s.fusionTaskLiveView(tasks[len(tasks)-1])
	}
	sides, err := s.fusionSides(leadID)
	if err != nil {
		return nil, err
	}
	if len(sides) == 0 {
		return map[string]string{"state": "idle"}, nil
	}
	return s.managedSessionView(sides[0])
}

func (s *Server) fusionTaskView(task session.FusionTask) (FusionTaskView, error) {
	current, found, err := session.ReadFusionTask(s.rt.SessionDir, task.ID)
	if err != nil {
		return FusionTaskView{}, err
	}
	if !found {
		return FusionTaskView{}, errors.New("Fusion task not found")
	}
	view := FusionTaskView{FusionTask: current}
	view.ExecutionState = "idle"
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, current.SideID); err != nil {
		return view, err
	} else if active {
		view.ExecutionState = "running"
		if current.State == session.FusionTaskQueued {
			if err := session.MarkFusionTaskRunning(s.rt.SessionDir, current.ID, current.Revision); err != nil {
				return view, err
			}
			view.FusionTask, _, err = session.ReadFusionTask(s.rt.SessionDir, current.ID)
			if err != nil {
				return view, err
			}
		}
	}
	created, err := time.Parse(time.RFC3339Nano, current.CreatedAt)
	if err != nil {
		return view, err
	}
	leadTurns, err := s.loadDurableSessionTurns(current.LeadID)
	if err != nil {
		return view, err
	}
	for _, turn := range leadTurns {
		view.Usage.LeadInputTokens += turn.InputTokens
		view.Usage.LeadOutputTokens += turn.OutputTokens
	}
	rows, err := session.ListFusionDispatches(s.rt.SessionDir, task.LeadID)
	if err != nil {
		return view, err
	}
	var latest session.FusionDispatch
	for _, row := range rows {
		if row.ClientID == current.LatestClientID {
			latest = row
		}
	}
	if latest.ClientID != "" {
		turn, err := s.fusionDispatchTurn(latest)
		if err != nil {
			return view, err
		}
		if turn != nil && turn.Status != TurnStatusInProgress {
			if current.State == session.FusionTaskQueued || current.State == session.FusionTaskRunning {
				state := session.FusionTaskAwaitingReview
				if turn.Status == TurnStatusFailed || turn.Status == TurnStatusInterrupted {
					state = session.FusionTaskFailed
				}
				if _, err := session.RecordFusionReport(s.rt.SessionDir, current.ID, latest.ClientID, current.Revision, turn.ID, state); err != nil {
					return view, err
				}
				view.FusionTask, _, err = session.ReadFusionTask(s.rt.SessionDir, current.ID)
				if err != nil {
					return view, err
				}
			}
			if view.ReportID == turn.ID || view.State == session.FusionTaskCancelled {
				view.Report = &FusionReport{ID: turn.ID, Revision: latest.Revision, Status: turn.Status, Output: finalAnswerText(*turn), Error: turn.Error}
			}
		}
	}
	sideTurns, err := s.loadDurableSessionTurns(current.SideID)
	if err != nil {
		return view, err
	}
	for _, turn := range sideTurns {
		view.Usage.SideInputTokens += turn.InputTokens
		view.Usage.SideOutputTokens += turn.OutputTokens
	}

	updated, err := time.Parse(time.RFC3339Nano, view.UpdatedAt)
	if err != nil {
		return view, err
	}
	if view.State == session.FusionTaskQueued || view.State == session.FusionTaskRunning || view.State == session.FusionTaskAwaitingReview {
		updated = time.Now().UTC()
	}
	view.ElapsedMS = updated.Sub(created).Milliseconds()
	return view, nil
}

// Durable projection is also called under the Lead's admission lock. Read live
// runtime state separately so projection never acquires another thread mutex.
func (s *Server) fusionTaskLiveView(task session.FusionTask) (FusionTaskView, error) {
	view, err := s.fusionTaskView(task)
	if err != nil {
		return view, err
	}
	return view, s.enrichFusionTaskLive(&view)
}

func (s *Server) enrichFusionTaskLive(view *FusionTaskView) error {
	metadata, found, err := session.Find(s.rt.SessionDir, view.SideID)
	if err != nil {
		return err
	}
	if !found {
		return session.ErrSessionNotFound
	}
	execution, err := s.managedSessionView(metadata)
	if err != nil {
		return err
	}
	view.ExecutionState = execution.State
	if th := s.thread(view.SideID); th != nil {
		th.mu.Lock()
		if th.running && len(th.Turns) > 0 && (view.State == session.FusionTaskQueued || view.State == session.FusionTaskRunning) {
			turn := th.Turns[len(th.Turns)-1]
			progress := FusionProgress{TurnID: turn.ID}
			for i := len(turn.Items) - 1; i >= 0; i-- {
				item := turn.Items[i]
				if item.Type == ThreadItemToolCall && progress.Tool == "" && item.Status == ThreadItemStatusInProgress {
					progress.Tool = item.Name
				}
				if item.Type == ThreadItemAgentMessage && !item.Terminal && progress.Summary == "" {
					text := []rune(strings.TrimSpace(item.Text))
					if len(text) > 400 {
						text = text[:400]
					}
					progress.Summary = string(text)
				}
			}
			view.Progress = &progress
		}
		th.mu.Unlock()
	}
	return nil
}

func (s *Server) deliverFusionTaskResult(task session.FusionTask) {
	view, err := s.fusionTaskView(task)
	if err != nil || view.Report == nil || (view.State != session.FusionTaskAwaitingReview && view.State != session.FusionTaskFailed) {
		return
	}
	content := fmt.Sprintf("Fusion task %s report %s, requirements revision %d (%s). Review actual work and evidence, then use review with this task_id, report_id and revision.\n\n%s", view.ID, view.Report.ID, view.Report.Revision, view.Report.Status, view.Report.Output)
	if view.Report.Error != nil {
		content += "\n\nFailure: " + view.Report.Error.Message
	}
	if err := session.EnqueueFusionTaskResult(s.rt.SessionDir, view.FusionTask, content); err != nil {
		return
	}
	s.drainSessionInbox(task.LeadID)
}
