package loopdriver

// Checkpoint calls are authorized against the execution and its selected driver.
const (
	DriverCheckpointService = "driver.checkpoint"
	DriverCheckpointMethod  = "call"
)

type DriverCheckpointParams struct {
	ExecutionID string     `json:"execution_id"`
	Checkpoint  Checkpoint `json:"checkpoint"`
}

type DriverCheckpointResult struct{}
