package sessiontrace

import (
	"encoding/json"
	"errors"
)

// LatestContextEvents projects the newest recorded request with its own turn
// and matching provider step. Context-less turns never relabel older requests.
// The result excludes tool payloads, checkpoints, answers, and earlier steps.
func LatestContextEvents(events []RawEvent) ([]RawEvent, error) {
	var latest []RawEvent
	var envelope RawEvent
	var turn TurnRecord
	var request *RequestContextRecord
	var states []ProviderStateRecord
	var final *FinalRecord
	flush := func() error {
		if request == nil {
			return nil
		}
		if turn.ThreadID == "" || turn.TurnID == "" || final == nil {
			return errors.New("context request has no complete matching turn")
		}
		projected := make([]RawEvent, 0, 4)
		add := func(kind string, value any) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			event := envelope
			event.Type = kind
			event.Data = raw
			projected = append(projected, event)
			return nil
		}
		metadata := TurnRecord{ThreadID: turn.ThreadID, TurnID: turn.TurnID, ProviderName: turn.ProviderName, Model: turn.Model, APIModel: turn.APIModel, ModelProfile: turn.ModelProfile}
		if err := add("turn", metadata); err != nil {
			return err
		}
		if err := add("context_requests", []RequestContextRecord{*request}); err != nil {
			return err
		}
		for i := len(states) - 1; i >= 0; i-- {
			if states[i].StepIndex == request.StepIndex {
				if err := add("provider_states", []ProviderStateRecord{states[i]}); err != nil {
					return err
				}
				break
			}
		}
		if final != nil {
			if err := add("final", FinalRecord{Status: final.Status}); err != nil {
				return err
			}
		}
		latest = projected
		return nil
	}
	for _, event := range events {
		if event.Type == "context_requests" || event.Type == "provider_states" || event.Type == "final" {
			if (event.ThreadID != "" && event.ThreadID != turn.ThreadID) || (event.TurnID != "" && event.TurnID != turn.TurnID) {
				return nil, errors.New("context event does not match its turn")
			}
		}
		switch event.Type {
		case "turn":
			if err := flush(); err != nil {
				return nil, err
			}
			turn = TurnRecord{}
			if err := json.Unmarshal(event.Data, &turn); err != nil {
				return nil, err
			}
			if turn.ThreadID == "" {
				turn.ThreadID = event.ThreadID
			}
			if turn.TurnID == "" {
				turn.TurnID = event.TurnID
			}
			if (event.ThreadID != "" && event.ThreadID != turn.ThreadID) || (event.TurnID != "" && event.TurnID != turn.TurnID) {
				return nil, errors.New("context metadata does not match its event scope")
			}
			envelope = event
			envelope.ThreadID = turn.ThreadID
			envelope.TurnID = turn.TurnID
			request = nil
			states = nil
			final = nil
		case "context_requests":
			var records []RequestContextRecord
			if err := json.Unmarshal(event.Data, &records); err != nil {
				return nil, err
			}
			if len(records) > 0 {
				request = &records[len(records)-1]
			}
		case "provider_states":
			var records []ProviderStateRecord
			if err := json.Unmarshal(event.Data, &records); err != nil {
				return nil, err
			}
			states = append(states, records...)
		case "final":
			var record FinalRecord
			if err := json.Unmarshal(event.Data, &record); err != nil {
				return nil, err
			}
			final = &record
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if latest == nil {
		latest = []RawEvent{}
	}
	return latest, nil
}
