package agentcontrol

import (
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type AgentMailboxMessage struct {
	Type            string    `json:"type"`
	ResultID        string    `json:"result_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	AgentID         string    `json:"agent_id"`
	ParticipantID   string    `json:"participant_id,omitempty"`
	AgentPath       string    `json:"agent_path,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	TaskName        string    `json:"task_name,omitempty"`
	AgentType       string    `json:"agent_type,omitempty"`
	Status          string    `json:"status"`
	Description     string    `json:"description,omitempty"`
	Result          string    `json:"result,omitempty"`
	ResultPath      string    `json:"result_path,omitempty"`
	ResultBytes     int       `json:"result_bytes,omitempty"`
	ResultTruncated bool      `json:"result_truncated,omitempty"`
	Error           string    `json:"error,omitempty"`
	ErrorClass      string    `json:"error_class,omitempty"`
	Resumable       bool      `json:"resumable,omitempty"`
	ResumeHint      string    `json:"resume_hint,omitempty"`
	InputTokens     int       `json:"input_tokens,omitempty"`
	OutputTokens    int       `json:"output_tokens,omitempty"`
	ReportPath      string    `json:"report_path,omitempty"`
	ReportKind      string    `json:"report_kind,omitempty"`
	ReportMissing   bool      `json:"report_missing,omitempty"`
	Artifacts       []string  `json:"artifacts,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	DurationMS      int64     `json:"duration_ms,omitempty"`
}

func NewAgentMailboxMessage(snap subagent.SubAgentSnapshot) AgentMailboxMessage {
	return NewAgentMailboxMessageWithReport(snap, "", "", nil)
}

func NewAgentMailboxMessageWithReport(snap subagent.SubAgentSnapshot, reportPath string, reportKind harness.ReportKind, artifacts []string) AgentMailboxMessage {
	ref := AgentResultReference{}
	if strings.TrimSpace(snap.Result) != "" {
		preview, truncated := previewAgentResult(snap.Result)
		ref = AgentResultReference{
			Preview:   preview,
			Bytes:     len([]byte(snap.Result)),
			Truncated: truncated,
		}
	}
	return NewAgentMailboxMessageWithReportAndResult(snap, reportPath, reportKind, artifacts, ref)
}

func NewAgentMailboxMessageWithReportAndResult(snap subagent.SubAgentSnapshot, reportPath string, reportKind harness.ReportKind, artifacts []string, ref AgentResultReference) AgentMailboxMessage {
	// report_missing is a derived alias for report_kind != structured, kept for
	// wire compatibility this release. Only terminal completed runs have a
	// report kind at all.
	reportMissing := snap.Status == subagent.StatusCompleted && reportKind != harness.ReportKindStructured
	kindOut := ""
	if snap.Status == subagent.StatusCompleted && reportKind != "" {
		kindOut = string(reportKind)
	}
	msg := AgentMailboxMessage{
		Type:            "agent_result",
		ResultID:        agentResultDeliveryID(snap),
		TaskID:          snap.ID,
		AgentID:         snap.ID,
		ParticipantID:   snap.ParticipantID,
		AgentPath:       snap.AgentPath,
		ParentID:        snap.ParentID,
		TaskName:        snap.TaskName,
		AgentType:       snap.Type,
		Status:          string(snap.Status),
		Description:     snap.Description,
		Result:          ref.Preview,
		ResultPath:      ref.Path,
		ResultBytes:     ref.Bytes,
		ResultTruncated: ref.Truncated,
		InputTokens:     snap.InputTokens,
		OutputTokens:    snap.OutputTokens,
		ReportPath:      reportPath,
		ReportKind:      kindOut,
		ReportMissing:   reportMissing,
		Artifacts:       artifacts,
		StartedAt:       snap.StartedAt,
		CompletedAt:     snap.CompletedAt,
	}
	if snap.Error != nil {
		msg.Error = snap.Error.Error()
		msg.ErrorClass = string(ClassifyError(snap.Error))
	}
	// A run that failed or was cancelled keeps its full context, so tell the
	// parent it can be revived in place rather than re-spawned. Both the
	// nested-parent and root delivery paths build the mailbox through this
	// function, so the hint reaches every recipient the same way.
	if isResumableMailboxStatus(snap.Status) {
		msg.Resumable = true
		msg.ResumeHint = "This run's full context is preserved. Send it a new message with send_message (set trigger_turn=true to drive its next turn) to revive it in place with your correction; it resumes from where it stopped instead of starting over."
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		msg.DurationMS = snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
	}
	return msg
}

// isResumableMailboxStatus reports whether a terminal run should advertise
// that it can be revived. Completed runs succeeded and do not need the hint;
// failed and cancelled runs are the abnormal terminals a parent may want to
// resume with a correction.
func isResumableMailboxStatus(status subagent.Status) bool {
	switch status {
	case subagent.StatusFailed, subagent.StatusCancelled:
		return true
	default:
		return false
	}
}

func FormatAgentMailboxMessage(snap subagent.SubAgentSnapshot) string {
	content := agentthread.SubagentNotificationContent(snap.AgentPath, NewAgentMailboxMessage(snap))
	return agentthread.NewInterAgentCommunication(
		parseAgentPathOrRoot(snap.AgentPath),
		agentthread.RootAgentPath(),
		content,
		false,
	).String()
}

func AgentMailboxChatMessage(snap subagent.SubAgentSnapshot) providers.ChatMessage {
	return providers.ChatMessage{
		Role:    "assistant",
		Content: FormatAgentMailboxMessage(snap),
	}
}
