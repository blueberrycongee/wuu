package appserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func (s *Server) requestEngineUserInput(ctx context.Context, threadID, turnID string, request agentengine.UserInputRequest) (map[string][]string, error) {
	if s == nil || s.rt == nil || s.rt.UserQuestions == nil {
		return nil, fmt.Errorf("user input is unavailable")
	}
	questions := make([]pluginhost.UserQuestion, len(request.Questions))
	ids := make(map[string]string, len(questions))
	seen := make(map[string]bool, len(questions))
	for i, question := range request.Questions {
		if question.Secret {
			return nil, fmt.Errorf("secret user input is not supported")
		}
		if strings.TrimSpace(question.ID) == "" || seen[question.ID] {
			return nil, fmt.Errorf("user input questions require unique nonempty IDs")
		}
		seen[question.ID] = true
		// Native IDs are opaque, and may overlap the host's approval IDs.
		// Keep the mapping scoped to this call rather than interpreting them.
		id := fmt.Sprintf("input.%d", i)
		ids[id] = question.ID
		questions[i] = pluginhost.UserQuestion{ID: id, Header: question.Header, Question: question.Question, AllowCustom: question.AllowCustom}
		for _, option := range question.Options {
			questions[i].Options = append(questions[i].Options, pluginhost.UserQuestionOption{Label: option.Label, Description: option.Description})
		}
	}
	owner := pluginhost.UserQuestionOwner{
		PluginID: "agent-engine-" + string(request.EngineID), ExecutionID: turnID,
		SessionID: threadID, ThreadID: threadID, TurnID: turnID, CallID: request.ItemID,
	}
	ask := s.rt.UserQuestions.Ask
	if !request.Blocking {
		ask = s.rt.UserQuestions.AskOffer
	}
	answer, err := ask(ctx, owner, pluginhost.UserQuestionAskParams{Questions: questions})
	if err != nil {
		for _, code := range []string{"question_cancelled", "question_expired", "execution_cancelled", "service_unavailable"} {
			if pluginhost.IsUserQuestionErrorCode(err, code) {
				return map[string][]string{}, nil
			}
		}
		return nil, err
	}
	answers := make(map[string][]string, len(answer.Answers))
	for _, item := range answer.Answers {
		values := append([]string(nil), item.Selected...)
		if custom := strings.TrimSpace(item.Custom); custom != "" {
			values = append(values, custom)
		}
		answers[ids[item.ID]] = values
	}
	return answers, nil
}
