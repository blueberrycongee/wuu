package session

import (
	"encoding/json"
	"time"
)

type DriverCheckpointRecord struct {
	ContractVersion int
	DriverID        string
	DriverVersion   string
	State           json.RawMessage
	UpdatedAt       time.Time
}
