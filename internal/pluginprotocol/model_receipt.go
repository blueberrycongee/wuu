package pluginprotocol

import "encoding/json"

// ModelReceiptParams carries only the opaque callback-scoped accounting grant.
// Omit execution_id for settlement: the grant remains usable while its callback
// drains after logical cancellation, but cannot admit another submission.
type ModelReceiptParams struct {
	Receipt string          `json:"receipt"`
	Input   json.RawMessage `json:"input,omitempty"`
}
type ModelSubmissionMeta struct {
	CapacityScope string `json:"capacity_scope,omitempty"`
	Provider      string `json:"provider"`
	Protocol      string `json:"protocol"`
	Transport     string `json:"transport"`
	Mode          string `json:"mode,omitempty"`
	Reason        string `json:"reason,omitempty"`
	RequestBytes  int    `json:"request_bytes,omitempty"`
}
type ModelSubmission struct {
	ID      string             `json:"id"`
	Outcome string             `json:"outcome"`
	Usage   *ModelRequestUsage `json:"usage,omitempty"`
}
type ModelSubmissionUpdate struct {
	ID         string             `json:"id"`
	Output     string             `json:"output,omitempty"`
	Usage      *ModelRequestUsage `json:"usage,omitempty"`
	Estimated  bool               `json:"estimated,omitempty"`
	FromStream bool               `json:"from_stream,omitempty"`
	Outcome    string             `json:"outcome,omitempty"`
	Failure    *ModelFailureV1    `json:"failure,omitempty"`
}
