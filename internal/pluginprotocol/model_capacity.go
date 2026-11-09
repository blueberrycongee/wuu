package pluginprotocol

// ModelCapacityAcquire identifies an opaque physical account domain, not a
// credential. Strategy scope can differ while physical_scope stays shared.
type ModelCapacityAcquire struct {
	Scope   string `json:"scope"`
	Profile string `json:"profile,omitempty"`
}
type ModelCapacityLease struct {
	Lease string `json:"lease"`
}
type ModelCapacitySettle struct {
	Lease   string          `json:"lease"`
	Outcome string          `json:"outcome"`
	Failure *ModelFailureV1 `json:"failure,omitempty"`
}
