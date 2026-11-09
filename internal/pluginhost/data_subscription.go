package pluginhost

// The subscribe surface mirrors the read-only query contract but is stream
// oriented: a consumer registers interest in a thread and receives DataEvent
// envelopes as the first-party producer emits them. This file establishes only
// the wire contract and validation for the first phase; producer granularity
// and sanitized content are intentionally deferred.

const (
	// KernelDataSubscribeService is the kernel's stream-oriented host data
	// service. It pairs with host.data.query: query is one snapshot, subscribe
	// is a bounded live stream.
	KernelDataSubscribeService = "host.data.subscribe"

	// DataSubscribeServiceVersion is the wire version of the subscribe contract.
	DataSubscribeServiceVersion = "1.0.0"
)

// DataSubscribeParams requests a bounded live stream for a thread. The shape is
// the same as DataQueryParams so the metadata filter stays consistent across
// snapshot and stream.
type DataSubscribeParams struct {
	ThreadID string   `json:"thread_id"`
	Types    []string `json:"types,omitempty"`
	TurnID   string   `json:"turn_id,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// KernelDataSubscribeDescriptor is the descriptor the kernel registers for the
// stream-oriented host data service.
func KernelDataSubscribeDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelDataSubscribeService, Version: DataSubscribeServiceVersion,
		Methods: []ServiceMethodDescriptor{{
			Name:         KernelServiceMethod,
			InputSchema:  "host.data.subscribe.input.v1",
			OutputSchema: "host.data.subscribe.output.v1",
		}},
	}
}

// ValidateDataSubscribeParams validates a subscribe request before any stream
// is opened. The validation matches the query contract.
func ValidateDataSubscribeParams(params DataSubscribeParams) error {
	return ValidateDataQueryParams(DataQueryParams{
		ThreadID: params.ThreadID,
		Types:    params.Types,
		TurnID:   params.TurnID,
		Limit:    params.Limit,
	})
}

// FilterDataEventsForSubscription applies the subscribe metadata filter to one
// emitted event. It returns false when the event should not be delivered under
// the current types/turn filter. Limit is intentionally applied by the caller
// because it is stateful across a stream.
func FilterDataEventsForSubscription(event DataEvent, params DataSubscribeParams) bool {
	allowed := make(map[string]struct{}, len(params.Types))
	for _, value := range params.Types {
		allowed[value] = struct{}{}
	}
	if len(allowed) > 0 {
		if _, ok := allowed[event.Type]; !ok {
			return false
		}
	}
	if params.TurnID != "" && event.TurnID != params.TurnID {
		return false
	}
	return true
}
