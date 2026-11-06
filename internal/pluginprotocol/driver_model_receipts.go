package pluginprotocol

// DriverModelReceipts contains metadata from the selected authoritative journal.
// Unavailable includes expired correlation retention. It never means zero usage.
type DriverModelReceipts struct {
	Status   string                    `json:"status"`
	Receipts []DriverModelUsageReceipt `json:"receipts"`
}
type DriverModelUsageReceipt struct {
	OperationID  string             `json:"operation_id"`
	SubmissionID string             `json:"submission_id"`
	Provider     string             `json:"provider"`
	Model        string             `json:"model"`
	Outcome      string             `json:"outcome"`
	CostState    string             `json:"cost_state"`
	Usage        *ModelRequestUsage `json:"usage,omitempty"`
}
