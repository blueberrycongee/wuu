package pluginhost

import "github.com/blueberrycongee/wuu/internal/pluginprotocol"

const KernelModelRequestService = "host.model.request"

func KernelModelRequestDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelModelRequestService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "host.model.request.input.v1", OutputSchema: "host.model.request.output.v1"}},
	}
}

// ModelRequestParams describes one text-only inference operation with bounded
// input and duration. Output tokens are strict unless explicitly relaxed.
// ModelAlias selects an existing host-configured alias. Provider and Model
// select an explicit configured model. Without either, the workspace main model
// is used. Variant strictly selects a configured tier for an explicit model.
// PreferredEffort retains the older best-effort behavior for callers that use it.
// Credentials and tools stay host-owned.
type ModelRequestParams struct {
	Messages             []ModelRequestMessage `json:"messages"`
	ModelAlias           string                `json:"model_alias,omitempty"`
	Provider             string                `json:"provider,omitempty"`
	Model                string                `json:"model,omitempty"`
	Variant              string                `json:"variant,omitempty"`
	PreferredEffort      string                `json:"preferred_effort,omitempty"`
	MaxOutputTokens      int                   `json:"max_output_tokens,omitempty"`
	AllowUnboundedOutput bool                  `json:"allow_unbounded_output,omitempty"`
	TimeoutMS            int                   `json:"timeout_ms,omitempty"`
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
type ModelRequestUsage = pluginprotocol.ModelRequestUsage
