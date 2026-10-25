package pluginhost

import "github.com/blueberrycongee/wuu/internal/composition"

const CompositionService = "host.composition"

// CompositionDescriptor exposes caller-owned runtime scopes through the registry.
// Selection changes future admissions; an admitted execution retains its scope.
func CompositionDescriptor() ServiceDescriptor {
	methods := make([]ServiceMethodDescriptor, 0, 6)
	for _, name := range []string{"create", "inspect", "inspectSession", "bind", "run", "dispose"} {
		methods = append(methods, ServiceMethodDescriptor{Name: name,
			InputSchema:  CompositionService + "." + name + ".input.v1",
			OutputSchema: CompositionService + "." + name + ".output.v1"})
	}
	return ServiceDescriptor{Name: CompositionService, Version: "1.0.0", Methods: methods}
}

// CompositionCreate creates a temporary runtime and installs explicitly supplied
// local sources in that scope. Either selection or explicit inheritance is required.
type CompositionCreate struct {
	// Inherit captures the active parent's modules and service bindings. Selection
	// can override modules, bindings and patches, but cannot select a profile.
	Inherit   bool               `json:"inherit,omitempty"`
	Selection composition.Config `json:"selection,omitempty"`
	Model     *CompositionModel  `json:"model,omitempty"`
	Paths     []string           `json:"paths,omitempty"`
	// State defaults to isolated. "parent" imports the parent's selected state
	// services and permits binding existing product sessions.
	State string `json:"state,omitempty"`
	// Root defaults to a new empty workspace. An explicit root shares its files.
	Root          string `json:"root,omitempty"`
	DriverProfile string `json:"driver_profile,omitempty"`
}

// CompositionModel selects a model connection within the copied configuration.
// Omitted model choices inherit the parent; an explicit choice uses that
// connection's defaults for empty fields.
type CompositionModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Variant  string `json:"variant,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

type CompositionInspect struct {
	ID string `json:"id"`
}

type CompositionBind struct {
	Agent string `json:"agent,omitempty"`
	// Session binds an existing product conversation instead of a logical agent.
	// Exactly one of Agent or Session is required.
	Session string `json:"session,omitempty"`
	// Empty ID unbinds the agent. Expected is required, including the empty initial value.
	ID       string  `json:"id"`
	Expected *string `json:"expected"`
}

type CompositionBinding struct {
	Agent    string `json:"agent,omitempty"`
	Session  string `json:"session,omitempty"`
	ID       string `json:"id"`
	Previous string `json:"previous"`
}

// CompositionSessionView preserves the selection snapshot even after its runtime
// has gone away. Composition is live only when State is active.
type CompositionSessionView struct {
	Session     string           `json:"session"`
	ID          string           `json:"id"`
	State       string           `json:"state"`
	Owner       string           `json:"owner,omitempty"`
	Thread      string           `json:"thread,omitempty"`
	Composition *CompositionView `json:"composition,omitempty"`
}

type CompositionRun struct {
	// Exactly one of ID (trial) or Agent (selected composition) is required.
	ID     string `json:"id,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt"`
}

type CompositionView struct {
	ParentGeneration string               `json:"parent_generation"`
	Model            CompositionModel     `json:"model"`
	ID               string               `json:"id"`
	Root             string               `json:"root"`
	State            string               `json:"state"`
	Running          int                  `json:"running"`
	Agents           []string             `json:"agents"`
	StateSource      string               `json:"state_source"`
	Selection        composition.Resolved `json:"selection"`
	Modules          []Status             `json:"modules"`
	Plugins          []CompositionPackage `json:"plugins"`
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
