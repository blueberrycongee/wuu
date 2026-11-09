package sessiontrace

import (
	"testing"
)

func TestLatestContextProjectionKeepsRequestAndTurnTogether(t *testing.T) {
	first, err := TurnEvents(TurnRecord{ThreadID: "thread", TurnID: "first", Model: "first-model"}, FinalRecord{}, nil, nil, []RequestContextRecord{{StepIndex: 0, InputTokens: 10}, {StepIndex: 1, InputTokens: 37}}, []ProviderStateRecord{{StepIndex: 1, Provider: "matching"}, {StepIndex: 2, Provider: "later-unmatched"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	later, err := TurnEvents(TurnRecord{ThreadID: "thread", TurnID: "later", Model: "later-model"}, FinalRecord{}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := LatestContextEvents(append(first, later...))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := ReplayEvents(projection)
	if err != nil {
		t.Fatal(err)
	}
	if summary.LatestTurn.TurnID != "first" || summary.LatestTurn.Model != "first-model" || len(summary.ContextRequests) != 1 || summary.ContextRequests[0].InputTokens != 37 || summary.ContextRequests[0].StepIndex != 1 {
		t.Fatalf("latest request mixed with another turn: %+v", summary)
	}
	if len(summary.ProviderStates) != 1 || summary.ProviderStates[0].Provider != "matching" {
		t.Fatalf("latest request mixed with another step: %+v", summary.ProviderStates)
	}
	for _, event := range projection {
		if event.TurnID != "first" {
			t.Fatalf("mixed projection scope: %+v", event)
		}
	}
}

func TestLatestContextProjectionRejectsIncompleteOrMismatchedSources(t *testing.T) {
	for _, change := range []string{"incomplete", "context_requests", "provider_states", "final"} {
		t.Run(change, func(t *testing.T) {
			events, err := TurnEvents(TurnRecord{ThreadID: "thread", TurnID: "turn"}, FinalRecord{}, nil, nil, []RequestContextRecord{{StepIndex: 1}}, []ProviderStateRecord{{StepIndex: 1}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if change == "incomplete" {
				events = events[:len(events)-1]
			} else {
				for i := range events {
					if events[i].Type == change {
						events[i].TurnID = "other"
					}
				}
			}
			if _, err := LatestContextEvents(events); err == nil {
				t.Fatal("invalid source produced a latest context")
			}
		})
	}
}
