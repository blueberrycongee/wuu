package capability

import "sort"

// Surface is the per-model compilation of internal capabilities to
// direct, deferred, nested, and hidden tool entries plus a profile-specific
// system prompt fragment.
//
// The toolkit consumes a Surface to decide which ToolDefinitions to
// expose to the model and which to keep as internal / advanced
// capabilities. The frontend reads the same Surface to render
// capability-aware activity and approval UI. Telemetry attaches the
// Surface to a session so post-hoc analysis can compare how different
// models actually used the harness.
type Surface struct {
	// ProfileName is the stable compiler key (e.g. "openai_codex",
	// "anthropic_claude"). It is safe to log, persist, and compare.
	ProfileName string

	// Provider and Model are the resolved values that produced this
	// surface. They are informational; capability decisions are based
	// on ProfileName plus the underlying profile fields.
	Provider string
	Model    string

	// Tools maps direct model-visible tool names (the value sent in
	// providers.ToolDefinition.Name) to their owning capability.
	// Only these tools appear in the top-level request definitions.
	Tools map[string]Capability

	// DeferredTools maps tool names that this profile may use only
	// after tool_search returns their loadable schemas. Deferred
	// tools are available to the runtime, but they stay out of the
	// top-level tool list so the provider prompt-cache prefix stays
	// stable across turns.
	DeferredTools map[string]Capability

	// NestedTools maps tools available through a program binding rather than
	// a top-level call. Skill requirements can use these tools as well.
	NestedTools map[string]Capability

	// HiddenTools maps profile companion tool names that are known
	// to this compiled surface but are not advertised to the model.
	// Ordinary legacy command tools are not part of the profile
	// contract; the toolkit registry owns any purely internal
	// implementation details.
	HiddenTools map[string]Capability

	// Capabilities is the ordered set of direct capabilities exposed
	// to the model in the top-level tool list. The order is stable
	// per ProfileName and is used to drive capability-first rendering,
	// permission routing, and approval UI.
	Capabilities []Capability

	// DeferredCapabilities lists capabilities that may be loaded via
	// tool_search. They are available to the profile, but not part of
	// the direct model-visible prefix.
	DeferredCapabilities []Capability

	// HiddenCapabilities lists capabilities that exist but are not
	// surfaced on this profile. They still exist for permission
	// routing when advanced tools are loaded and for telemetry
	// that wants to reason about a missing capability.
	HiddenCapabilities []Capability

	// SystemFragment is the profile-specific addition to the
	// system prompt. It tells the model which tool set it has and
	// which mental model to follow (bash-first, apply_patch
	// preferred, exact-edit preferred, etc.).
	SystemFragment string

	// DeferredToolCatalog is a session-level static prompt section
	// built from trusted toolkit metadata. It lists deferred tool
	// names and short summaries without exposing their parameter
	// schemas until tool_search loads them.
	DeferredToolCatalog string
}

// HasCapability reports whether the surface knows a capability
// directly, deferred, or hidden. Tooling that needs to check whether
// a capability is available should use this; tooling that needs to
// check whether the model can see the capability in the direct tool
// list should consult Capabilities directly.
func (s Surface) HasCapability(c Capability) bool {
	for _, existing := range s.Capabilities {
		if existing == c {
			return true
		}
	}
	for _, existing := range s.DeferredCapabilities {
		if existing == c {
			return true
		}
	}
	for _, existing := range s.HiddenCapabilities {
		if existing == c {
			return true
		}
	}
	return false
}

// HasAvailableCapability reports whether the model can actually reach the
// capability on this surface, either directly in the tool list or deferred
// behind tool_search. Unlike HasCapability it excludes hidden-only
// capabilities, which the model can never load. Use it to gate guidance that
// teaches a capability the model must be able to invoke.
func (s Surface) HasAvailableCapability(c Capability) bool {
	for _, existing := range s.Capabilities {
		if existing == c {
			return true
		}
	}
	for _, existing := range s.DeferredCapabilities {
		if existing == c {
			return true
		}
	}
	return false
}

// VisibleCapabilities returns the advertised capability list. It is
// the slice to use for permission-routing checks that ask "can the
// model see X?"; HasCapability is for "does X exist for this
// surface?".
func (s Surface) VisibleCapabilities() []Capability {
	out := make([]Capability, len(s.Capabilities))
	copy(out, s.Capabilities)
	return out
}

// ToolNames returns the model-visible tool names in alphabetical
// order. The ordering is stable so test assertions and UI lists
// stay deterministic.
func (s Surface) ToolNames() []string {
	out := make([]string, 0, len(s.Tools))
	for name := range s.Tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DeferredToolNames returns the tool names that can be loaded through
// tool_search in alphabetical order.
func (s Surface) DeferredToolNames() []string {
	out := make([]string, 0, len(s.DeferredTools))
	for name := range s.DeferredTools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ToolForCapability returns the model-visible tool name that
// implements the given capability, plus true. Returns "", false if
// no model-visible tool implements the capability on this surface
// (it may still exist as a hidden tool — see HasCapability).
//
// Iteration is sorted by tool name so the return value is stable
// across runs even when a capability has more than one model-visible
// tool (e.g. file.edit on Claude exposes both edit_file and
// write_file; this method always returns edit_file). Callers that
// need the full list should iterate Tools directly.
func (s Surface) ToolForCapability(c Capability) (string, bool) {
	for _, name := range s.ToolNames() {
		if s.Tools[name] == c {
			return name, true
		}
	}
	return "", false
}

// DeferredToolForCapability returns a deferred tool name (if any)
// that implements the capability. The model must load the tool via
// tool_search before calling it.
func (s Surface) DeferredToolForCapability(c Capability) (string, bool) {
	for _, name := range s.DeferredToolNames() {
		if s.DeferredTools[name] == c {
			return name, true
		}
	}
	return "", false
}

// HiddenToolForCapability returns a hidden tool name (if any) that
// implements the capability. Used by internal callers (e.g. the
// bash result post-processor) that need to reach a tool the model
// cannot see.
func (s Surface) HiddenToolForCapability(c Capability) (string, bool) {
	hidden := make([]string, 0, len(s.HiddenTools))
	for name := range s.HiddenTools {
		hidden = append(hidden, name)
	}
	sort.Strings(hidden)
	for _, name := range hidden {
		if s.HiddenTools[name] == c {
			return name, true
		}
	}
	return "", false
}

// Summary is the compact, JSON-friendly view of a Surface used by
// the app-server protocol. InitializeResult, runtime setting
// updates, and Settings debug views all return this struct.
type Summary struct {
	ProfileName           string            `json:"profile_name"`
	Provider              string            `json:"provider"`
	Model                 string            `json:"model"`
	ToolNames             []string          `json:"tool_names"`
	DeferredToolNames     []string          `json:"deferred_tool_names"`
	HiddenToolNames       []string          `json:"hidden_tool_names"`
	Capabilities          []string          `json:"capabilities"`
	DeferredCapabilities  []string          `json:"deferred_capabilities"`
	HiddenCapabilities    []string          `json:"hidden_capabilities"`
	EditPrimitive         string            `json:"edit_primitive"`
	BashFirst             bool              `json:"bash_first"`
	ToolCapabilityMap     map[string]string `json:"tool_capability_map"`
	DeferredCapabilityMap map[string]string `json:"deferred_capability_map"`
	HiddenCapabilityMap   map[string]string `json:"hidden_capability_map"`
	SystemFragment        string            `json:"system_fragment,omitempty"`
}

// Summarize returns a compact summary of the surface. The summary
// is what flows over the app-server protocol and into the frontend
// debug UI; the full Surface stays server-side because it carries
// the implementation map and the system prompt fragment.
func (s Surface) Summarize() Summary {
	toolCaps := make(map[string]string, len(s.Tools))
	for name, c := range s.Tools {
		toolCaps[name] = string(c)
	}
	deferredCaps := make(map[string]string, len(s.DeferredTools))
	for name, c := range s.DeferredTools {
		deferredCaps[name] = string(c)
	}
	hiddenCaps := make(map[string]string, len(s.HiddenTools))
	for name, c := range s.HiddenTools {
		hiddenCaps[name] = string(c)
	}
	caps := make([]string, 0, len(s.Capabilities))
	for _, c := range s.Capabilities {
		caps = append(caps, string(c))
	}
	deferred := make([]string, 0, len(s.DeferredCapabilities))
	for _, c := range s.DeferredCapabilities {
		deferred = append(deferred, string(c))
	}
	hidden := make([]string, 0, len(s.HiddenCapabilities))
	for _, c := range s.HiddenCapabilities {
		hidden = append(hidden, string(c))
	}
	hiddenTools := make([]string, 0, len(s.HiddenTools))
	for name := range s.HiddenTools {
		hiddenTools = append(hiddenTools, name)
	}
	sort.Strings(hiddenTools)
	return Summary{
		ProfileName:           s.ProfileName,
		Provider:              s.Provider,
		Model:                 s.Model,
		ToolNames:             s.ToolNames(),
		DeferredToolNames:     s.DeferredToolNames(),
		HiddenToolNames:       hiddenTools,
		Capabilities:          caps,
		DeferredCapabilities:  deferred,
		HiddenCapabilities:    hidden,
		EditPrimitive:         s.editPrimitive(),
		BashFirst:             s.HasCapability(CapabilityCommandBash),
		ToolCapabilityMap:     toolCaps,
		DeferredCapabilityMap: deferredCaps,
		HiddenCapabilityMap:   hiddenCaps,
		SystemFragment:        s.SystemFragment,
	}
}

// editPrimitive returns a short, stable string describing the
// editing primitive this surface advertises. It is informational
// and surfaces in the debug UI; the toolkit still consults
// CapabilityFileEdit + the model execution default write mode to
// route edits to the right tool.
func (s Surface) editPrimitive() string {
	if patch, ok := s.ToolForCapability(CapabilityFileEdit); ok {
		return patch
	}
	return ""
}
