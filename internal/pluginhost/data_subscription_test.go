package pluginhost

import "testing"

func TestValidateDataSubscribeParams(t *testing.T) {
	if err := ValidateDataSubscribeParams(DataSubscribeParams{ThreadID: "t1", Limit: 2}); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if err := ValidateDataSubscribeParams(DataSubscribeParams{ThreadID: ""}); err == nil {
		t.Fatal("missing thread_id accepted")
	}
	if err := ValidateDataSubscribeParams(DataSubscribeParams{ThreadID: "t1", Limit: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestFilterDataEventsForSubscription(t *testing.T) {
	params := DataSubscribeParams{ThreadID: "t1", Types: []string{DataEventTypeToolCall}, TurnID: "turn-2"}
	cases := []struct {
		event DataEvent
		want  bool
	}{
		{DataEvent{Type: DataEventTypeToolCall, TurnID: "turn-2"}, true},
		{DataEvent{Type: DataEventTypeToolCall, TurnID: "turn-1"}, false},
		{DataEvent{Type: DataEventTypeTurn, TurnID: "turn-2"}, false},
	}
	for _, tc := range cases {
		if got := FilterDataEventsForSubscription(tc.event, params); got != tc.want {
			t.Fatalf("filter(%+v) = %v, want %v", tc.event, got, tc.want)
		}
	}
}
