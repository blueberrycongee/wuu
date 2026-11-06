package pluginprotocol

// A proposal is durable host-owned state. Drivers persist only its ID; neither
// its transcript nor credentials belong in a driver checkpoint.
type DriverCompactionParams struct {
	DriverTurnScope
	ID       string              `json:"id"`
	Revision uint64              `json:"revision,omitempty"`
	Budget   DriverCompactBudget `json:"budget,omitempty"`
	Wait     bool                `json:"wait,omitempty"`
	Resume   bool                `json:"resume,omitempty"`
}

type DriverCompactionResult struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	Attempt         int            `json:"attempt"`
	SourceHead      int            `json:"source_head"`
	ContextRevision uint64         `json:"context_revision,omitempty"`
	Failure         *DriverFailure `json:"failure,omitempty"`
}
