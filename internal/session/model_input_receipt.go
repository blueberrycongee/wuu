package session

import (
	"encoding/json"

	"time"
)

const (
	modelInputReceiptRetention     = 7 * 24 * time.Hour
	modelInputReceiptsPerSession   = 64
	modelInputReceiptCompressBatch = 32
)

type ModelInputReceiptMaintenanceResult struct {
	Deleted         int64
	Compressed      int64
	BytesBefore     int64
	BytesAfter      int64
	CompressionDone bool
}

type ModelInputReceiptRecord struct {
	OperationID     string
	ContractVersion int
	DriverID        string
	DriverVersion   string
	Payload         json.RawMessage
	CreatedAt       time.Time
}
