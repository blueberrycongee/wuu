package codexengine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
)

func (s *Session) handleUserInput(ctx context.Context, sub *turnSubscription, raw json.RawMessage) (any, error) {
	var params ToolRequestUserInputParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid user input request"}
	}
	if !s.matchesThread(params.ThreadID) {
		return nil, errRequestNotForSession
	}
	response := ToolRequestUserInputResponse{Answers: make(map[string]ToolRequestUserInputAnswer)}
	select {
	case <-ctx.Done():
		return response, nil
	case <-sub.ready:
	}
	sub.mu.Lock()
	turnID, stopped := sub.turnID, sub.nativeDone
	sub.mu.Unlock()
	if stopped || ctx.Err() != nil {
		return response, nil
	}
	if params.TurnID == "" || params.TurnID != turnID || strings.TrimSpace(params.ItemID) == "" {
		return nil, &RPCError{Code: -32602, Message: "user input request does not belong to the active turn"}
	}
	if s.userInput == nil {
		return nil, &RPCError{Code: -32601, Message: "user input is unavailable"}
	}
	request := agentengine.UserInputRequest{
		EngineID: "codex", ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID,
		Blocking:  params.IsBlocking == nil || *params.IsBlocking,
		Questions: make([]agentengine.UserInputQuestion, len(params.Questions)),
	}
	for i, question := range params.Questions {
		converted := agentengine.UserInputQuestion{
			ID: question.ID, Header: question.Header, Question: question.Question,
			AllowCustom: question.IsOther || len(question.Options) == 0, Secret: question.IsSecret,
		}
		for _, option := range question.Options {
			converted.Options = append(converted.Options, agentengine.UserInputOption{Label: option.Label, Description: option.Description})
		}
		request.Questions[i] = converted
	}
	answers, err := s.userInput(ctx, request)
	if err != nil {
		return nil, err
	}
	// Completion or cancellation wins over a late human reply. Never forward
	// an answer from a retired turn to its native session's next turn.
	if ctx.Err() != nil {
		return response, nil
	}
	for id, answers := range answers {
		response.Answers[id] = ToolRequestUserInputAnswer{Answers: answers}
	}
	return response, nil
}
