package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

// These business facts belong to the selected personal.agents provider. The
// host verifies session ownership and the source history independently. During
// an effect, the provider's opaque user state remains under its durable storage
// inspection; a service response alone is not an execution fence.
type personalExecutionFacts struct {
	SessionID     string             `json:"session_id"`
	AgentID       string             `json:"agent_id"`
	MainSessionID string             `json:"main_session_id"`
	Active        bool               `json:"active"`
	Task          *personalTaskFacts `json:"task,omitempty"`
}
type personalTaskFacts struct {
	ID               string `json:"id"`
	Revision         int64  `json:"revision"`
	RunID            string `json:"run_id"`
	Active           bool   `json:"active"`
	Goal             string `json:"goal"`
	SourceSessionID  string `json:"source_session_id"`
	SourceMessageSeq int    `json:"source_message_seq"`
	RoutineID        string `json:"routine_id,omitempty"`
	RoutineRevision  int64  `json:"routine_revision,omitempty"`
}

func (p *personalActions) executionFacts(ctx context.Context, state *runtime.State, sessionID string) (personalExecutionFacts, error) {
	var result personalExecutionFacts
	raw, _ := json.Marshal(map[string]string{"session_id": sessionID})
	response, err := state.Host.CallService(ctx, personalActionsConsumerID, "personal.agents", "observe-execution", raw)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, err
	}
	if result.SessionID != sessionID || result.AgentID == "" || result.Task != nil && (result.Task.ID == "" || result.Task.Revision < 1 || result.Task.SourceMessageSeq < 0) {
		return result, errors.New("invalid Personal execution facts")
	}
	return result, nil
}
func (p *personalActions) legacyMemory(ctx context.Context, state *runtime.State) (map[string]string, error) {
	raw, err := state.Host.CallService(ctx, personalActionsConsumerID, "personal.agents", "legacy-memory", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var result map[string]string
	decodeErr := json.Unmarshal(raw, &result)
	return result, decodeErr
}
