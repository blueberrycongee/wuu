// Package capability defines the internal capability vocabulary that
// the Wuu harness uses to describe what an agent can do, independent
// of the specific tool names a given model happens to see.
//
// The external tool surface is per-model. Each ModelProfile compiles
// its capability set into a specific list of model-visible tool
// entries. The capability layer is the stable contract between the
// agent runtime, the permission system, telemetry, and the UI; the
// tool layer is just one possible projection of that contract.
package capability

// Capability is a stable, dotted identifier for one piece of agent
// power. Adding a new capability is a deliberate act: every consumer
// (toolkit, policy, telemetry, frontend) needs to learn how to handle
// it. Renaming or removing a capability is a breaking change for
// downstream callers.
type Capability string

const (
	// Filesystem reading and listing.
	CapabilityFileRead Capability = "file.read"
	CapabilityFileList Capability = "file.list"
	// Explicit delivery of a file snapshot to the user, not model observation.
	CapabilityArtifactPresent Capability = "artifact.present"

	// Filesystem editing. The actual model-visible tool differs per
	// profile: Codex / OpenAI get apply_patch, Claude / generic get
	// edit_file and write_file. Both projections live behind this
	// single capability.
	CapabilityFileEdit Capability = "file.edit"

	// Search surface.
	CapabilitySearchGrep     Capability = "search.grep"
	CapabilitySearchGlob     Capability = "search.glob"
	CapabilitySearchAST      Capability = "search.ast"
	CapabilitySearchSemantic Capability = "search.semantic"

	// Command execution. The default surface for every profile
	// includes capability.command.bash: the agent runs terminal
	// commands (tests, build, lint, git, npm, docker, scripts)
	// through a single bash entry point rather than splitting them
	// across separate shell / test / process / git tools.
	CapabilityCommandBash Capability = "command.bash"

	// Long-running managed processes. Profiles that hide this
	// capability still let the runtime manage processes as a backend
	// for bash background mode; they just do not advertise the
	// background process actions as part of the model surface.
	CapabilityCommandBackground Capability = "command.background"

	// External network surface.
	CapabilityWebFetch  Capability = "web.fetch"
	CapabilityWebSearch Capability = "web.search"

	// Session lookup: resolve a copied Wuu thread/session ID to the
	// persisted conversation record and history.
	CapabilitySessionLookup    Capability = "session.lookup"
	CapabilitySessionWorkspace Capability = "session.workspace"
	CapabilityContextHistory   Capability = "context.history"
	CapabilityContextWindow    Capability = "context.window"

	// Explicitly finish a conversation turn without an outward reply.
	CapabilitySessionYield Capability = "session.yield"

	// TODO / skills.
	CapabilityTodo  Capability = "todo"
	CapabilitySkill Capability = "skill"

	// Persistent named-agent group chat.
	CapabilityChat Capability = "chat"

	// Extensions (MCP, plugins).
	CapabilityMCP Capability = "mcp"

	// Discovery: tool_search and related progressive-disclosure
	// tools. Listed under its own capability so permission routing
	// can keep it open while the rest of the surface stays locked.
	CapabilityDiscovery Capability = "tool.discovery"

	// Code mode orchestrates the active tools without granting leaf capabilities.
	CapabilityCodeMode Capability = "tool.code_mode"

	// Embedded browser automation: the single browser tool backed by a
	// desktop-hosted hidden WebContentsView + CDP session. Its own capability
	// so permission routing and telemetry can reason about browser control
	// independently of the general web fetch/search surface.
	CapabilityBrowser Capability = "browser"
)

// All returns the full closed set of capabilities. The list is
// used by the default compiler to ensure every capability has a
// well-known string identifier; the slice order is stable so it
// can be used as a deterministic iteration order in tests.
func All() []Capability {
	return []Capability{
		CapabilityFileRead,
		CapabilityFileList,
		CapabilityArtifactPresent,
		CapabilityFileEdit,
		CapabilitySearchGrep,
		CapabilitySearchGlob,
		CapabilitySearchAST,
		CapabilitySearchSemantic,
		CapabilityCommandBash,
		CapabilityCommandBackground,
		CapabilityWebFetch,
		CapabilityWebSearch,
		CapabilitySessionLookup,
		CapabilitySessionWorkspace,
		CapabilitySessionYield,
		CapabilityContextHistory,
		CapabilityContextWindow,
		CapabilityTodo,
		CapabilitySkill,
		CapabilityChat,
		CapabilityMCP,
		CapabilityDiscovery,
		CapabilityCodeMode,
		CapabilityBrowser,
	}
}

// String returns the capability identifier.
func (c Capability) String() string { return string(c) }
