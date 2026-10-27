package pluginprotocol

// DriverBinding is the durable identity of a selected loop implementation.
// Changing defaults cannot replace it; a state migration must select a new
// identity explicitly.
type DriverBinding struct {
	Profile         string `json:"profile"`
	ProviderID      string `json:"provider_id"`
	ContractVersion int    `json:"contract_version"`
	DriverID        string `json:"driver_id"`
	DriverVersion   string `json:"driver_version"`
}
