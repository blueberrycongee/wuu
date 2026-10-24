package pluginhost

import "github.com/blueberrycongee/wuu/internal/composition"

const CompositionService = "host.composition"

// CompositionDescriptor exposes caller-owned runtime scopes through the registry.
// Selection changes future admissions; an admitted execution retains its scope.
func CompositionDescriptor() ServiceDescriptor {
	methods := make([]ServiceMethodDescriptor, 0, 5)
	for _, name := range []string{"create", "inspect", "bind", "run", "dispose"} {
		methods = append(methods, ServiceMethodDescriptor{Name: name,
			InputSchema:  CompositionService + "." + name + ".input.v1",
			OutputSchema: CompositionService + "." + name + ".output.v1"})
	}
	return ServiceDescriptor{Name: CompositionService, Version: "1.0.0", Methods: methods}
}

// CompositionCreate creates a temporary runtime and installs explicitly supplied
// local sources in that scope. Selection is required; no parent profile is assumed.
type CompositionCreate struct {
	Selection composition.Config `json:"selection"`
	Paths     []string           `json:"paths,omitempty"`
	// Root defaults to a new empty workspace. An explicit root shares its files.
	Root          string `json:"root,omitempty"`
	DriverProfile string `json:"driver_profile,omitempty"`
}

type CompositionInspect struct {
	ID string `json:"id"`
}

type CompositionBind struct {
	Agent string `json:"agent"`
	// Empty ID unbinds the agent. Expected is required, including the empty initial value.
	ID       string  `json:"id"`
	Expected *string `json:"expected"`
}

type CompositionBinding struct {
	Agent    string `json:"agent"`
	ID       string `json:"id"`
	Previous string `json:"previous"`
}

type CompositionRun struct {
	// Exactly one of ID (trial) or Agent (selected composition) is required.
	ID     string `json:"id,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt"`
}

type CompositionView struct {
	ID        string               `json:"id"`
	Root      string               `json:"root"`
	State     string               `json:"state"`
	Running   int                  `json:"running"`
	Agents    []string             `json:"agents"`
	Selection composition.Resolved `json:"selection"`
	Modules   []Status             `json:"modules"`
	Plugins   []CompositionPackage `json:"plugins"`
}

type CompositionPackage struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

type CompositionRunResult struct {
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id"`
	Content     string `json:"content"`
	// Execution failure preserves the captured composition identity for diagnosis.
	Error string `json:"error,omitempty"`
}

type CompositionDisposed struct {
	ID       string `json:"id"`
	Draining bool   `json:"draining"`
}
