package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const nativeReviewTimeout = 20 * time.Second

type nativeToolReviewer struct {
	server *Server
}

type turnScopedReviewer struct {
	server *Server
	turnID string
}

func (r turnScopedReviewer) Review(ctx context.Context, request approvefor.Request) (approvefor.Decision, error) {
	if strings.TrimSpace(request.TurnID) == "" {
		request.TurnID = r.turnID
	}
	return nativeToolReviewer{server: r.server}.Review(ctx, request)
}

func (r nativeToolReviewer) Review(ctx context.Context, request approvefor.Request) (approvefor.Decision, error) {
	if r.server == nil {
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "reviewer unavailable"}, nil
	}
	if reason := approvefor.HardDenyReason(request); reason != "" {
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: reason}, nil
	}
	outcome, reason, err := r.server.reviewNativeToolWithModel(ctx, request)
	if err != nil || outcome == "" || outcome == approvefor.OutcomeUnsure {
		return r.server.askNativeToolApproval(ctx, request, reason)
	}
	return approvefor.Decision{Outcome: outcome, Reason: reason}, nil
}

func (s *Server) reviewNativeToolWithModel(ctx context.Context, request approvefor.Request) (string, string, error) {
	if s == nil || s.rt == nil || s.rt.StreamRunner == nil || s.rt.StreamRunner.Client == nil {
		return "", "", errors.New("no reviewer model")
	}
	reviewCtx, cancel := context.WithTimeout(ctx, nativeReviewTimeout)
	defer cancel()
	runner := s.rt.StreamRunner
	payload, _ := json.Marshal(map[string]any{
		"tool":        request.Tool.Name,
		"kind":        request.Tool.Kind,
		"risk":        request.Tool.Risk,
		"read_only":   request.Tool.ReadOnly,
		"destructive": request.Tool.Destructive,
		"reason":      request.Tool.Reason,
		"cwd":         request.CWD,
		"arguments":   approvefor.TruncateText(request.Arguments, 4000),
	})
	response, err := providers.ExecuteChat(reviewCtx, runner.Client, providers.ChatRequest{
		Provider: runner.ProviderName,
		Model:    runner.Model,
		Messages: []providers.ChatMessage{
			{Role: "system", Content: nativeReviewSystemPrompt},
			{Role: "user", Content: string(payload)},
		},
		Temperature:     runner.Temperature,
		Effort:          runner.Effort,
		ProviderOptions: runner.ProviderOptions,
	}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return "", "", err
	}
	return parseNativeReviewResponse(response.Content)
}

func (s *Server) askNativeToolApproval(ctx context.Context, request approvefor.Request, reviewerReason string) (approvefor.Decision, error) {
	if s == nil || s.rt == nil || s.rt.UserQuestions == nil {
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "user approval is unavailable"}, nil
	}
	threadID := strings.TrimSpace(request.SessionID)
	turnID := strings.TrimSpace(request.TurnID)
	if turnID == "" {
		turnID = threadID
	}
	callID := strings.TrimSpace(request.CallID)
	if callID == "" {
		callID = request.Tool.Name
	}
	detail := strings.TrimSpace(request.Tool.Reason)
	if reviewerReason != "" {
		if detail != "" {
			detail += "\n\n"
		}
		detail += "Reviewer: " + reviewerReason
	}
	if args := strings.TrimSpace(request.Arguments); args != "" {
		if detail != "" {
			detail += "\n\n"
		}
		detail += args
	}
	answer, err := s.rt.UserQuestions.Ask(ctx, pluginhost.UserQuestionOwner{
		PluginID:    "agent-engine-wuu",
		ExecutionID: turnID,
		SessionID:   threadID,
		ThreadID:    threadID,
		TurnID:      turnID,
		CallID:      callID,
	}, pluginhost.UserQuestionAskParams{Questions: []pluginhost.UserQuestion{{
		ID:       approvefor.QuestionID,
		Header:   "Approve for me",
		Question: fmt.Sprintf("Allow this %s call?", strings.TrimSpace(request.Tool.Name)),
		Detail:   approvefor.TruncateText(detail, 4096),
		Options: []pluginhost.UserQuestionOption{
			{Label: approvefor.AllowOnceLabel, Description: "Approve only this request"},
			{Label: approvefor.DenyLabel, Description: "Do not allow this request"},
		},
	}}})
	if err != nil {
		var questionErr *pluginhost.UserQuestionError
		if errors.As(err, &questionErr) {
			return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: questionErr.Message}, nil
		}
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: err.Error()}, nil
	}
	if len(answer.Answers) != 1 || len(answer.Answers[0].Selected) != 1 {
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "no approval choice"}, nil
	}
	if answer.Answers[0].Selected[0] == approvefor.AllowOnceLabel {
		return approvefor.Decision{Outcome: approvefor.OutcomeAllow, Reason: "user allowed once"}, nil
	}
	return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "user denied this request"}, nil
}

func parseNativeReviewResponse(content string) (string, string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return approvefor.OutcomeUnsure, "empty reviewer response", nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}
	var parsed struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return approvefor.OutcomeUnsure, "reviewer response was not valid JSON", nil
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Outcome)) {
	case approvefor.OutcomeAllow, approvefor.OutcomeDeny, approvefor.OutcomeUnsure:
		return strings.ToLower(strings.TrimSpace(parsed.Outcome)), strings.TrimSpace(parsed.Reason), nil
	default:
		return approvefor.OutcomeUnsure, "reviewer outcome was not allow, deny, or unsure", nil
	}
}

const nativeReviewSystemPrompt = `You are a read-only security reviewer for Wuu. Decide whether one tool call may proceed while the main agent stays in standard permission mode. Never approve raising the session to unconfined, reading Wuu credential files, or copying secrets. Reply with JSON only: {"outcome":"allow"|"deny"|"unsure","reason":"short reason"}. Use unsure when evidence is incomplete.`

func applyApproveForMeToToolkit(kit *tools.Toolkit, enabled bool, reviewer tools.Reviewer) {
	if kit == nil {
		return
	}
	kit.SetApproveForMe(enabled)
	if enabled {
		kit.SetReviewer(reviewer)
		return
	}
	kit.SetReviewer(nil)
}
