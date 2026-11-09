package pluginhost

import "encoding/json"

const KernelInvokeToolService = "execution.invoke-tool"

// InvokeToolParams invokes an enabled tool within a live orchestrator tool's
// execution. CallID is a local idempotency key: retries must keep name and
// arguments identical. Children use the normal scheduler, authorization and
// durable ledger. The owner ending or being disabled cancels its children.
type InvokeToolParams struct {
	ExecutionID string          `json:"execution_id"`
	CallID      string          `json:"call_id"`
	Name        string          `json:"name"`
	Arguments   json.RawMessage `json:"arguments"`
}

func KernelInvokeToolDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelInvokeToolService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "execution.invoke-tool.input.v1", OutputSchema: "execution.invoke-tool.output.v1"}},
	}
}
