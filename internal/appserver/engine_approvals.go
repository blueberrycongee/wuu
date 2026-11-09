package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	engineApprovalAllowOnce    = "Allow once"
	engineApprovalAllowSession = "Allow for this session"
	engineApprovalDeny         = "Deny"
)

func engineApprovalQuestionID(kind agentengine.ApprovalKind) string {
	return "approval." + string(kind)
}

func (s *Server) requestEngineApproval(ctx context.Context, wuuThreadID, wuuTurnID string, request agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
	if s == nil || s.rt == nil || s.rt.UserQuestions == nil {
		return agentengine.ApprovalDecline, nil
	}
	question, detail := engineApprovalText(request)
	callID := strings.TrimSpace(request.ItemID)
	if callID == "" {
		callID = string(request.Kind)
	}
	answer, err := s.rt.UserQuestions.Ask(ctx, pluginhost.UserQuestionOwner{
		PluginID:    "agent-engine-" + string(request.EngineID),
		ExecutionID: wuuTurnID,
		SessionID:   wuuThreadID,
		ThreadID:    wuuThreadID,
		TurnID:      wuuTurnID,
		CallID:      callID,
	}, pluginhost.UserQuestionAskParams{Questions: []pluginhost.UserQuestion{{
		ID:       engineApprovalQuestionID(request.Kind),
		Header:   string(request.EngineID) + " approval",
		Question: question,
		Detail:   truncateApprovalText(detail, 4096),
		Options: []pluginhost.UserQuestionOption{
			{Label: engineApprovalAllowOnce, Description: "Approve only this request"},
			{Label: engineApprovalAllowSession, Description: "Approve matching requests for this engine session"},
			{Label: engineApprovalDeny, Description: "Do not allow this request"},
		},
	}}})
	if err != nil {
		var questionErr *pluginhost.UserQuestionError
		if errors.As(err, &questionErr) {
			return agentengine.ApprovalCancel, nil
		}
		return agentengine.ApprovalDecline, err
	}
	if len(answer.Answers) != 1 || len(answer.Answers[0].Selected) != 1 {
		return agentengine.ApprovalDecline, nil
	}
	switch answer.Answers[0].Selected[0] {
	case engineApprovalAllowOnce:
		return agentengine.ApprovalAccept, nil
	case engineApprovalAllowSession:
		return agentengine.ApprovalAcceptForSession, nil
	default:
		return agentengine.ApprovalDecline, nil
	}
}

func engineApprovalText(request agentengine.ApprovalRequest) (string, string) {
	switch request.Kind {
	case agentengine.ApprovalCommandExecution:
		detail := strings.TrimSpace(request.Command)
		if cwd := strings.TrimSpace(request.CWD); cwd != "" {
			detail += "\n\nWorking directory: " + cwd
		}
		return "Allow this command to run?", appendApprovalReason(detail, request.Reason)
	case agentengine.ApprovalFileChange:
		return "Allow this file change?", appendApprovalReason(strings.TrimSpace(request.FilePath), request.Reason)
	case agentengine.ApprovalPermissions:
		encoded, _ := json.Marshal(request.Permissions)
		return "Allow the requested permissions?", appendApprovalReason(string(encoded), request.Reason)
	default:
		return "Allow this external engine action?", strings.TrimSpace(request.Reason)
	}
}

func appendApprovalReason(detail, reason string) string {
	detail = strings.TrimSpace(detail)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return detail
	}
	if detail == "" {
		return "Reason: " + reason
	}
	return fmt.Sprintf("%s\n\nReason: %s", detail, reason)
}

func truncateApprovalText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}
