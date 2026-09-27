package pluginhost

import (
	"errors"
	"testing"
)

func TestValidateDataQueryParams(t *testing.T) {
	t.Parallel()

	valid := []DataQueryParams{
		{ThreadID: "thread-1"},
		{ThreadID: "thread_2.sub", Limit: 10},
		{ThreadID: "a-b_c.d", Limit: 0},
	}
	for _, params := range valid {
		if err := ValidateDataQueryParams(params); err != nil {
			t.Fatalf("ValidateDataQueryParams(%+v) = %v", params, err)
		}
	}

	invalid := []DataQueryParams{
		{},
		{ThreadID: "   "},
		{ThreadID: "../thread-1"},
		{ThreadID: "thread/1"},
		{ThreadID: "thread 1"},
		{ThreadID: "thread-1", Limit: -1},
	}
	for _, params := range invalid {
		err := ValidateDataQueryParams(params)
		if err == nil {
			t.Fatalf("ValidateDataQueryParams(%+v) = nil, want error", params)
		}
		var hostErr *HostServiceError
		if !errors.As(err, &hostErr) || hostErr.Code != "invalid_params" {
			t.Fatalf("ValidateDataQueryParams(%+v) error = %v, want invalid_params HostServiceError", params, err)
		}
	}
}

func TestKernelDataQueryDescriptorIsValid(t *testing.T) {
	t.Parallel()

	descriptor := KernelDataQueryDescriptor()
	if err := ValidateServiceDescriptor(descriptor); err != nil {
		t.Fatalf("ValidateServiceDescriptor(%+v) = %v", descriptor, err)
	}
}

func TestValidateDataQueryParamsTypesAndTurn(t *testing.T) {
	t.Parallel()

	valid := []DataQueryParams{
		{ThreadID: "thread-1", Types: []string{DataEventTypeTurn, DataEventTypeToolRecords}},
		{ThreadID: "thread-1", TurnID: "turn-1"},
		{ThreadID: "thread-1", Types: []string{DataEventTypeStep}, TurnID: "turn_1.sub"},
		{ThreadID: "thread-1", Types: []string{"future.fact"}},
	}
	for _, params := range valid {
		if err := ValidateDataQueryParams(params); err != nil {
			t.Fatalf("ValidateDataQueryParams(%+v) = %v", params, err)
		}
	}

	invalid := []DataQueryParams{
		{ThreadID: "thread-1", Types: []string{DataEventTypeTurn, DataEventTypeTurn}},
		{ThreadID: "thread-1", TurnID: "../turn-1"},
	}
	for _, params := range invalid {
		err := ValidateDataQueryParams(params)
		if err == nil {
			t.Fatalf("ValidateDataQueryParams(%+v) = nil, want error", params)
		}
		var hostErr *HostServiceError
		if !errors.As(err, &hostErr) || hostErr.Code != "invalid_params" {
			t.Fatalf("ValidateDataQueryParams(%+v) error = %v, want invalid_params HostServiceError", params, err)
		}
	}
}

func TestFilterDataEventsAppliesTypeTurnAndLimit(t *testing.T) {
	t.Parallel()

	events := []DataEvent{
		{Type: DataEventTypeTurn, ThreadID: "thread-1", TurnID: "turn-1"},
		{Type: DataEventTypeToolRecords, ThreadID: "thread-1", TurnID: "turn-1"},
		{Type: DataEventTypeFinal, ThreadID: "thread-1", TurnID: "turn-1"},
		{Type: DataEventTypeTurn, ThreadID: "thread-1", TurnID: "turn-2"},
	}

	t.Run("type filter", func(t *testing.T) {
		got := FilterDataEvents(events, DataQueryParams{Types: []string{DataEventTypeTurn}})
		if len(got) != 2 || got[0].Type != DataEventTypeTurn || got[1].Type != DataEventTypeTurn {
			t.Fatalf("FilterDataEvents = %+v", got)
		}
	})

	t.Run("turn filter", func(t *testing.T) {
		got := FilterDataEvents(events, DataQueryParams{TurnID: "turn-2"})
		if len(got) != 1 || got[0].TurnID != "turn-2" {
			t.Fatalf("FilterDataEvents = %+v", got)
		}
	})

	t.Run("limit after filters", func(t *testing.T) {
		got := FilterDataEvents(events, DataQueryParams{Types: []string{DataEventTypeTurn}, Limit: 1})
		if len(got) != 1 || got[0].TurnID != "turn-1" {
			t.Fatalf("FilterDataEvents = %+v", got)
		}
	})

	t.Run("unknown type matches nothing", func(t *testing.T) {
		got := FilterDataEvents(events, DataQueryParams{Types: []string{"future.fact"}})
		if len(got) != 0 {
			t.Fatalf("FilterDataEvents = %+v, want empty", got)
		}
	})

	t.Run("no filters preserves events", func(t *testing.T) {
		got := FilterDataEvents(events, DataQueryParams{})
		if len(got) != len(events) {
			t.Fatalf("FilterDataEvents = %+v", got)
		}
	})
}
