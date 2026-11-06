package pluginprotocol

// DriverBinding is the durable identity of a selected loop implementation.
// Changing defaults cannot replace it; a state migration must select a new
// identity explicitly.
type DriverBinding struct {
	// HistoryState opts into entry-bound configuration and store snapshots.
	HistoryState    bool   `json:"history_state,omitempty"`
	Profile         string `json:"profile"`
	ProviderID      string `json:"provider_id"`
	ContractVersion int    `json:"contract_version"`
	DriverID        string `json:"driver_id"`
	DriverVersion   string `json:"driver_version"`
}

// CanUpgradeTo permits an additive history capability without fabricating old
// entries. Losing an already-selected capability or changing identity fails.
func (b DriverBinding) CanUpgradeTo(next DriverBinding) bool {
	if b.HistoryState && !next.HistoryState {
		return false
	}
	b.HistoryState = next.HistoryState
	return b == next
}
