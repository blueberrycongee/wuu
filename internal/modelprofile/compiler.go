package modelprofile

import (
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
)

// ProfileKey is the stable identifier of a tool-surface compilation.
// The same ProfileKey is used to build the JSON-RPC initialize result,
// the Settings debug view, and the prompt-fragment cache. Adding a
// new key is a deliberate act; renaming an existing key is a breaking
// change for downstream UIs.
type ProfileKey string

const (
	// Profile keys remain stable for provider diagnostics and persisted settings.
	ProfileOpenAICodex     ProfileKey = "openai_codex"
	ProfileOpenAIGPT       ProfileKey = "openai_gpt"
	ProfileAnthropicClaude ProfileKey = "anthropic_claude"
	ProfileGeneric         ProfileKey = "generic"
)

// SurfaceKind identifies which runtime role a built-in tool surface is
// compiled for. Optional plugin tools are appended after this surface and
// enforce their own execution scopes.
type SurfaceKind int

const (
	// SurfaceWorker is a pure child executor surface.
	SurfaceWorker SurfaceKind = iota
	// SurfaceMain is the ordinary project main-session surface.
	SurfaceMain
	// SurfaceProjectSession adds project communication to an ordinary session.
	SurfaceProjectSession
)

// Compiler compiles a model profile into a built-in tool surface. Plugin-owned
// product tools are not part of this compiler.
type Compiler interface {
	Compile(p Profile, kind SurfaceKind) capability.Surface
}

// DefaultCompiler returns the built-in compiler. The compiler is
// stateless: callers should keep a single instance and reuse it.
type DefaultCompiler struct{}

// Compile implements Compiler. Workers receive only the built-in executor
// surface selected by their role.
func (DefaultCompiler) Compile(p Profile, kind SurfaceKind) capability.Surface {
	key := ResolveProfileKey(p)
	b := newBuilder(p, key)

	addFileReadTools(b)
	addSearchTools(b)
	addBashFirstTools(b, p)
	addWebTools(b)
	addBrowserTools(b)
	addSessionTools(b)
	addSkillTools(b)
	addExtensionTools(b)
	b.addVisible("edit_file", capability.CapabilityFileEdit)
	b.addVisible("write_file", capability.CapabilityFileEdit)
	addPrompt(b, p)
	if kind != SurfaceWorker {
		addSessionWorkspaceTool(b)
	}
	// Presentation reads into host-owned storage, without editing the workspace.
	b.addVisible("present_artifact", capability.CapabilityArtifactPresent)
	if kind != SurfaceWorker {
		addContextWindowTools(b)
	}
	if kind == SurfaceProjectSession {
		b.addVisible("session", capability.CapabilityProjectSessions)
	}
	b.sortCaps()
	return b.surface
}

// ResolveProfileKey returns the stable ProfileKey for a model
// profile. Tool availability is shared across families; execution
// capabilities such as direct shell access still constrain the surface.
func ResolveProfileKey(p Profile) ProfileKey {
	switch p.Family {
	case FamilyCodex:
		return ProfileOpenAICodex
	case FamilyGPT:
		return ProfileOpenAIGPT
	case FamilyClaude:
		return ProfileAnthropicClaude
	default:
		return ProfileGeneric
	}
}

// surfaceBuilder is a helper that incrementally builds a Surface.
// It enforces the rule that every model-visible tool name lives in either the
// direct or deferred exposure bucket. A broad capability can appear in both
// buckets when different tools under it have different lifecycles.
type surfaceBuilder struct {
	surface  capability.Surface
	visible  map[capability.Capability]struct{}
	deferred map[capability.Capability]struct{}
}

func newSurfaceFor(p Profile, key ProfileKey) capability.Surface {
	return capability.Surface{
		ProfileName:   string(key),
		Provider:      p.ProviderName,
		Model:         p.Model,
		Tools:         map[string]capability.Capability{},
		DeferredTools: map[string]capability.Capability{},
		HiddenTools:   map[string]capability.Capability{},
	}
}

func newBuilder(p Profile, key ProfileKey) *surfaceBuilder {
	return &surfaceBuilder{
		surface:  newSurfaceFor(p, key),
		visible:  map[capability.Capability]struct{}{},
		deferred: map[capability.Capability]struct{}{},
	}
}

// addVisible registers a model-visible tool and its capability.
func (b *surfaceBuilder) addVisible(tool string, c capability.Capability) {
	b.surface.Tools[tool] = c
	b.addVisibleCapability(c)
}

func (b *surfaceBuilder) addVisibleCapability(c capability.Capability) {
	if _, ok := b.visible[c]; ok {
		return
	}
	b.visible[c] = struct{}{}
	b.surface.Capabilities = append(b.surface.Capabilities, c)
}

// addDeferred registers a tool that is available only after
// tool_search loads its schema.
func (b *surfaceBuilder) addDeferred(tool string, c capability.Capability) {
	b.surface.DeferredTools[tool] = c
	b.addDeferredCapability(c)
}

func (b *surfaceBuilder) addDeferredCapability(c capability.Capability) {
	if _, ok := b.deferred[c]; ok {
		return
	}
	b.deferred[c] = struct{}{}
	b.surface.DeferredCapabilities = append(b.surface.DeferredCapabilities, c)
}

// sortCaps sorts the capability slices for deterministic output.
func (b *surfaceBuilder) sortCaps() {
	sort.SliceStable(b.surface.Capabilities, func(i, j int) bool {
		return string(b.surface.Capabilities[i]) < string(b.surface.Capabilities[j])
	})
	sort.SliceStable(b.surface.DeferredCapabilities, func(i, j int) bool {
		return string(b.surface.DeferredCapabilities[i]) < string(b.surface.DeferredCapabilities[j])
	})
}

// ── Shared capability assembly helpers ─────────────────────────────

func addFileReadTools(b *surfaceBuilder) {
	b.addVisible("read_file", capability.CapabilityFileRead)
	b.addVisible("list_files", capability.CapabilityFileList)
}

func addSearchTools(b *surfaceBuilder) {
	b.addVisible("grep", capability.CapabilitySearchGrep)
	b.addVisible("glob", capability.CapabilitySearchGlob)
	// Keep legacy search capabilities available for MCP tools without
	// registering low-use built-in AST or semantic search tools.
	b.addDeferredCapability(capability.CapabilitySearchAST)
	b.addDeferredCapability(capability.CapabilitySearchSemantic)
}

func addBashFirstTools(b *surfaceBuilder, p Profile) {
	if p.Execution.AllowDirectShell {
		b.addVisible("bash", capability.CapabilityCommandBash)
		b.addVisible("process", capability.CapabilityCommandBackground)
	}
}

func addWebTools(b *surfaceBuilder) {
	b.addVisible("web_search", capability.CapabilityWebSearch)
	b.addVisible("web_fetch", capability.CapabilityWebFetch)
}

// addBrowserTools defers the embedded browser tool on every profile. Flat
// loading promotes it back to a direct tool. The compiler stays pure and never
// reads the environment. Runtime opt-out still
// lives in the toolkit's disabledTools, flipped by SetBrowserEnabled, so a
// session can hide the tool without changing the compiled surface.
func addBrowserTools(b *surfaceBuilder) {
	b.addDeferred("wuu_browser", capability.CapabilityBrowser)
}

func addSessionTools(b *surfaceBuilder) {
	b.addDeferred("thread_get", capability.CapabilitySessionLookup)
}

func addSessionWorkspaceTool(b *surfaceBuilder) {
	b.addDeferred("set_session_workspace", capability.CapabilitySessionWorkspace)
}

func addContextWindowTools(b *surfaceBuilder) {
	b.addVisible("notes", capability.CapabilityContextWindow)
	b.addVisible("new_context", capability.CapabilityContextWindow)
	b.addVisible("history_read", capability.CapabilityContextHistory)
	b.addVisible("history_search", capability.CapabilityContextHistory)
}

func addSkillTools(b *surfaceBuilder) {
	b.addVisible("load_skill", capability.CapabilitySkill)
	b.addVisible("tool_search", capability.CapabilityDiscovery)
}

func addExtensionTools(b *surfaceBuilder) {
	// MCP has no stable built-in tool name because concrete MCP
	// tools are discovered at runtime. The deferred capability says
	// this profile may load MCP tools through tool_search; the tools
	// themselves are still deferred and guard-gated.
	b.addDeferredCapability(capability.CapabilityMCP)
}

// ── Prompt fragments ──────────────────────────────────────────────

// Profile fragments only route the model to the primitives exposed by that
// surface and carry policy that tool schemas cannot express.
// Exact-edit recovery, background-process rules, and boundary-error recovery
// belong to the relevant tool descriptions and results, not here.
const sharedPromptPolicy = `

Stay within available workspace boundaries and do not try to bypass a denial. When the user's request already calls for an operation, act without asking for extra chat-side approval.`

const shellPromptPolicy = `

Do not access sensitive credential paths or use broad staging, destructive Git operations, force push, Git configuration changes, hook skipping, commit amendments, or interactive Git flows unless explicitly requested.`

func addPrompt(b *surfaceBuilder, p Profile) {
	label := b.surface.ProfileName
	guidance := "Use edit_file for targeted changes and write_file for new files or complete rewrites."
	if p.Execution.AllowDirectShell {
		guidance += " Use bash for command execution." + shellPromptPolicy
	} else {
		label += " (no command execution)"
	}
	b.surface.SystemFragment = strings.TrimSpace("[Tool surface: " + label + "]\n" + guidance + sharedPromptPolicy)
}
