// Package mcpconfig owns configuration shared by MCP declarations and clients.
package mcpconfig

import (
	"encoding/json"
	"fmt"
	"time"
)

const DefaultToolTimeout = 30 * time.Second
const maxToolTimeoutMS = 60 * 60 * 1000

// ToolTimeoutMS is a per-call wall-clock budget in milliseconds. Its zero value
// means omitted; an explicitly configured value must be within 1..3600000.
type ToolTimeoutMS int64

func (v *ToolTimeoutMS) UnmarshalJSON(data []byte) error {
	var ms *int64
	if err := json.Unmarshal(data, &ms); err != nil {
		return fmt.Errorf("tool_timeout_ms must be an integer between 1 and %d: %w", maxToolTimeoutMS, err)
	}
	if ms == nil || *ms == 0 {
		return fmt.Errorf("tool_timeout_ms must be between 1 and %d; omit it to use the default", maxToolTimeoutMS)
	}
	value := ToolTimeoutMS(*ms)
	if _, err := value.Duration(); err != nil {
		return err
	}
	*v = value
	return nil
}

// Duration validates programmatic values and resolves an omitted budget.
func (v ToolTimeoutMS) Duration() (time.Duration, error) {
	if v == 0 {
		return DefaultToolTimeout, nil
	}
	if v < 1 || v > maxToolTimeoutMS {
		return 0, fmt.Errorf("tool_timeout_ms must be between 1 and %d", maxToolTimeoutMS)
	}
	return time.Duration(v) * time.Millisecond, nil
}
