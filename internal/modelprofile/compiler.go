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
	// ProfileOpenAICodex is the OpenAI / Codex harness. It exposes
	// apply_patch as the preferred editing primitive and treats
	// bash as the single terminal entry point. Reasoning budgets
	// are higher (harness iterates more before replying).
	ProfileOpenAICodex ProfileKey = "openai_codex"

	// ProfileOpenAIGPT covers OpenAI GPT reasoning models that ship
	// with the OpenAI Responses API. It uses the same patch-first
	// editing surface as Codex: apply_patch for file edits and bash
	// as the single terminal entry point.
	ProfileOpenAIGPT ProfileKey = "openai_gpt"

	// ProfileAnthropicClaude is the Anthropic Claude harness. It
	// uses exact-edit primitives (edit_file + write_file) as the
	// preferred file-change path and exposes bash for terminal
	// work. Prompt caching and long-horizon planning are first-class.
	ProfileAnthropicClaude ProfileKey = "anthropic_claude"

	// ProfileGeneric is the catch-all for BYOK providers (Gemini,
	// Kimi, DeepSeek, Qwen, local models, anything we have not
	// explicitly classified). It uses exact-edit primitives and a
	// conservative surface; local models in particular drop the
	// command.bash capability because the underlying profile
	// disables direct shell.
	ProfileGeneric ProfileKey = "generic"
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
	// SurfaceNamedAgent is a persistent group-chat agent. It keeps the complete
	// main-agent surface and adds the group-chat tools.
	SurfaceNamedAgent
)

func (k SurfaceKind) includesSessionWorkspace() bool {
	return k == SurfaceMain || k == SurfaceNamedAgent
}

func (k SurfaceKind) includesChat() bool {
	return k == SurfaceNamedAgent
}

func (k SurfaceKind) includesContextWindows() bool {
	return k == SurfaceMain || k == SurfaceNamedAgent
}

// Compiler compiles a model profile into a built-in tool surface. Plugin-owned
// product tools are not part of this compiler.
type Compiler interface {
	Compile(p Profile, kind SurfaceKind) capability.Surface
}

// DefaultCompiler returns the built-in compiler. The compiler is
// stateless: callers should keep a single instance and reuse it.
type DefaultCompiler struct{}

// Compile implements Compiler. Named agents add collaboration chat tools;
// workers receive only the built-in executor surface selected by their role.
func (DefaultCompiler) Compile(p Profile, kind SurfaceKind) capability.Surface {
	key := ResolveProfileKey(p)
	b := newBuilder(p, key)
	switch key {
	case ProfileOpenAICodex:
		compileOpenAICodex(b, p)
	case ProfileOpenAIGPT:
		compileOpenAIGPT(b, p)
	case ProfileAnthropicClaude:
		compileAnthropicClaude(b, p)
	default:
		compileGeneric(b, p)
	}
	if kind.includesSessionWorkspace() {
		addSessionWorkspaceTool(b)
	}
	if kind.includesContextWindows() {
		addContextWindowTools(b)
	}
	if kind.includesChat() {
		addChatTools(b)
	}
	b.sortCaps()
	return b.surface
}

// ResolveProfileKey returns the stable ProfileKey for a model
// profile. The key is the primary input to the surface compiler; the
// underlying Family and WriteMode fields refine its output but do
// not change which compiler variant runs.
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

// ── Per-profile compilation ───────────────────────────────────────

const openaiPatchEditTool = "apply_patch"

func compileOpenAICodex(b *surfaceBuilder, p Profile) {
	addFileReadTools(b)
	addSearchTools(b)
	addBashFirstTools(b, p)
	addWebTools(b)
	addBrowserTools(b)
	addSessionTools(b)
	addSkillTools(b)
	addExtensionTools(b)
	addOpenAICodexEditTools(b)
	addOpenAICodexPrompt(b)
}

func compileOpenAIGPT(b *surfaceBuilder, p Profile) {
	addFileReadTools(b)
	addSearchTools(b)
	addBashFirstTools(b, p)
	addWebTools(b)
	addBrowserTools(b)
	addSessionTools(b)
	addSkillTools(b)
	addExtensionTools(b)
	addOpenAIGPTEditTools(b)
	addOpenAIGPTPrompt(b)
}

func compileAnthropicClaude(b *surfaceBuilder, p Profile) {
	addFileReadTools(b)
	addSearchTools(b)
	addBashFirstTools(b, p)
	addWebTools(b)
	addBrowserTools(b)
	addSessionTools(b)
	addSkillTools(b)
	addExtensionTools(b)
	addClaudeEditTools(b)
	addClaudePrompt(b)
}

func compileGeneric(b *surfaceBuilder, p Profile) {
	addFileReadTools(b)
	addSearchTools(b)
	addBashFirstTools(b, p)
	addWebTools(b)
	addBrowserTools(b)
	addSessionTools(b)
	addSkillTools(b)
	addExtensionTools(b)
	addGenericEditTools(b)
	addGenericPrompt(b, p)
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
		b.addVisibleCapability(capability.CapabilityCommandBackground)
	}
}

func addWebTools(b *surfaceBuilder) {
	b.addVisible("web_search", capability.CapabilityWebSearch)
	b.addVisible("web_fetch", capability.CapabilityWebFetch)
}

// addBrowserTools defers the embedded browser tool on every profile. It is
// registered unconditionally and never reads the environment: the compiler must
// stay pure so surface tests do not drift with WUU_ENABLE_BROWSER. Runtime
// gating lives entirely in the toolkit's disabledTools (default off, flipped by
// SetBrowserEnabled), so a deferred entry here is inert until both the surface
// exposes it AND the toolkit enables it.
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
	b.addVisible("new_context", capability.CapabilityContextWindow)
	b.addVisible("history_read", capability.CapabilityContextHistory)
	b.addVisible("history_search", capability.CapabilityContextHistory)
}

func addChatTools(b *surfaceBuilder) {
	b.addVisible("yield_turn", capability.CapabilityChat)
	b.addVisible("chat_check", capability.CapabilityChat)
	b.addVisible("chat_read", capability.CapabilityChat)
	b.addVisible("chat_session", capability.CapabilityChat)
	b.addVisible("chat_send", capability.CapabilityChat)
	b.addVisible("collaboration_send", capability.CapabilityChat)
	b.addVisible("chat_draft", capability.CapabilityChat)
	b.addVisible("chat_task", capability.CapabilityChat)
	b.addVisible("chat_work", capability.CapabilityChat)
	b.addVisible("chat_verify", capability.CapabilityChat)
	b.addVisible("chat_remind", capability.CapabilityChat)
	b.addVisible("chat_roster", capability.CapabilityChat)
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

func addOpenAICodexEditTools(b *surfaceBuilder) {
	b.addVisible(openaiPatchEditTool, capability.CapabilityFileEdit)
}

func addOpenAIGPTEditTools(b *surfaceBuilder) {
	b.addVisible(openaiPatchEditTool, capability.CapabilityFileEdit)
}

func addClaudeEditTools(b *surfaceBuilder) {
	b.addVisible("edit_file", capability.CapabilityFileEdit)
	b.addVisible("write_file", capability.CapabilityFileEdit)
}

func addGenericEditTools(b *surfaceBuilder) {
	b.addVisible("edit_file", capability.CapabilityFileEdit)
	b.addVisible("write_file", capability.CapabilityFileEdit)
}

// ── Prompt fragments ──────────────────────────────────────────────

// Profile fragments only route the model to the primitives exposed by that
// surface and carry policy that tool schemas cannot express. Patch grammar,
// exact-edit recovery, background-process rules, and boundary-error recovery
// belong to the relevant tool descriptions and results, not here.
const sharedPromptPolicy = `

Stay within available workspace boundaries and do not try to bypass a denial. When the user's request already calls for an operation, act without asking for extra chat-side approval.`

const shellPromptPolicy = `

Do not access sensitive credential paths or use broad staging, destructive Git operations, force push, Git configuration changes, hook skipping, commit amendments, or interactive Git flows unless explicitly requested.`

func addOpenAICodexPrompt(b *surfaceBuilder) {
	b.surface.SystemFragment = strings.TrimSpace(`
[Tool surface: openai_codex]
Use apply_patch for file changes and bash for command execution.
` + shellPromptPolicy + sharedPromptPolicy)
}

func addOpenAIGPTPrompt(b *surfaceBuilder) {
	b.surface.SystemFragment = strings.TrimSpace(`
[Tool surface: openai_gpt]
Use apply_patch for file changes and bash for command execution.
` + shellPromptPolicy + sharedPromptPolicy)
}

func addClaudePrompt(b *surfaceBuilder) {
	b.surface.SystemFragment = strings.TrimSpace(`
[Tool surface: anthropic_claude]
Use edit_file for targeted changes, write_file for new files or complete rewrites, and bash for command execution.
` + shellPromptPolicy + sharedPromptPolicy)
}

func addGenericPrompt(b *surfaceBuilder, p Profile) {
	if p.Family == FamilyLocal || !p.Execution.AllowDirectShell {
		b.surface.SystemFragment = strings.TrimSpace(`
[Tool surface: generic (no command execution)]
Use edit_file for targeted changes and write_file for new files or complete rewrites.
` + sharedPromptPolicy)
		return
	}
	b.surface.SystemFragment = strings.TrimSpace(`
[Tool surface: generic]
Use edit_file for targeted changes, write_file for new files or complete rewrites, and bash for command execution.
` + shellPromptPolicy + sharedPromptPolicy)
}
