package pluginhost

import pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"

const KernelModelRequestService = "host.model.request"

func KernelModelRequestDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelModelRequestService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "host.model.request.input.v1", OutputSchema: "host.model.request.output.v1"}},
	}
}

// ModelRequestParams describes one bounded, text-only inference operation.
// ModelAlias selects an existing host-configured alias; an empty alias uses
// the workspace's configured main model. Credentials and tools stay host-owned.
type ModelRequestParams struct {
	Messages        []ModelRequestMessage `json:"messages"`
	ModelAlias      string                `json:"model_alias,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
	TimeoutMS       int                   `json:"timeout_ms,omitempty"`
}

type ModelRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelRequestResult struct {
	Text         string             `json:"text"`
	Provider     string             `json:"provider"`
	Model        string             `json:"model"`
	FinishReason string             `json:"finish_reason"`
	Truncated    bool               `json:"truncated"`
	Usage        *ModelRequestUsage `json:"usage,omitempty"`
}

// ModelRequestUsage is absent when the provider did not report token usage.
// Cancellation cannot undo a provider submission or guarantee zero charges.
type ModelRequestUsage = pluginapi.ModelRequestUsage
