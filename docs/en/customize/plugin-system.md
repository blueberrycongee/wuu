# Plugin architecture

Wuu loads plugins into a Go runtime host and an Electron desktop host. A package may use either host or both, and can also supply declarative contributions. This page explains how the current implementation discovers, activates, composes, and retires those contributions. For package fields and code examples, use the [authoring reference](plugin-authoring.md).

## Host and plugin responsibilities

The Go host owns tool dispatch, execution identity, service routing, session persistence, permissions, and resource cleanup. A plugin owns its feature logic, prompts, business state, and background policy. Features such as automation, memory, and subagents use host services instead of maintaining a second session engine.

The desktop host owns windows, layout, the conversation stream, input submission, and the bridge to the Go app-server. Plugins register views and contributions through the public SDK; they do not receive an Electron main-process object or direct access to host React state. The host provides React and shared UI components so plugin controls follow the same rendering and styling contracts.

Bundled and external packages use the same runtime and desktop registration contracts. Bundled provenance affects discovery and trust, not the meaning of a tool, capability, or UI registration. A copied package does not become official merely by using a bundled plugin's ID.

## Discovery and activation

Discovery reads user packages, workspace packages, authorized development directories, and bundled packages. User packages normally live in `$WUU_HOME/plugins`; workspace packages live in `.wuu/plugins` under the workspace. Legacy singular `plugin` directories are also recognized. When IDs collide, later discovery replaces the earlier candidate; bundled packages are considered last.

Finding a manifest does not mean running it. The host evaluates enabled state, trust, platform and minimum-version compatibility, package relationships, and runtime negotiation. Required package dependencies use legacy `requires` IDs or versioned `dependencies` entries. Missing or incompatible required dependencies leave consumers and their transitive required dependents inactive; `breaks` and required dependency cycles can reject the activation plan. Matching optional dependencies add ordering when possible, but their absence or failure does not block consumers. `conflicts` produces a diagnostic rather than choosing a winner automatically.

The current local installer stages packages and updates for a separate trust decision. Development authorization applies to a specific directory. Package identity includes content and source context, so an ID or version string alone is not proof that the currently approved bytes are running. See [plugin management](plugins.md) for the actual install and update flow.

Package relationships do not install dependencies, enable disabled packages, or grant trust. They also do not grant service-call authority: runtime `provided_services` and `required_services` negotiate service names and major versions separately. See [package relationships](plugin-authoring.md#package-structure-and-manifest).

## Runtime generations

Reload does not transfer arbitrary process or module variables into the replacement. Keep durable business state in host storage or session records, and version any persistent data whose format can change. Conversation history and plugin implementation lifetime are separate.

A runtime generation groups the selected packages with their processes, tools, capabilities, hooks, skills, MCP connections, service registry, and owned package snapshots. Building a candidate does not immediately replace the active generation. Development publication may proceed while turns are running: each retained generation owns its package snapshot, so replacing the published source does not change the files used by running work. The host coordinates catalog reads with publication rather than blocking publication for an entire turn.

During preflight, native runtime processes initialize and describe their contracts through read-phase host services. Plugins should start timers and other active behavior in `activate`, not `initialize`. Preparation can also start MCP connections. These are trusted processes: restrictions on host service calls do not prevent direct filesystem, network, or other external effects during initialization. Preparation is not a side-effect-free sandbox.

The runtime transition prepares the candidate, commits the associated package or policy changes, and only then calls native `activate`. If preparation or the durable commit fails, the candidate is closed and the old runtime remains current; candidate `activate` is not called.

Providers initialize and activate before their package consumers. A required provider's initialization or activation failure blocks its transitive required consumers, including relationships through declarative-only packages. Blocked consumers do not expose their tools, hooks, skills, MCP servers, or desktop contributions. Independent packages can still activate; a provider's own runtime failure does not automatically remove its independent declarative contributions.

After the commit succeeds, an activation failure publishes the new generation in a degraded state instead of restoring the old one. The operation reports that the change was committed but runtime activation failed, and the inventory retains the failure diagnostics. Mutation handlers still synchronize committed settings, finish removal cleanup where applicable, and notify clients. Refresh accepts that published state and logs its activation failure rather than treating it as a failed refresh to retry on every watcher interval. External effects already performed by activation cannot be rolled back.

After publication, an existing conversation adopts the new generation at its next idle turn boundary. A running turn and its outstanding background work retain their generation until that work settles. The host rebuilds plugin bindings together while retaining conversation history and session identity; the previous generation retires after its last lease is released. Catalog-only changes and pre-commit failures do not replace a healthy pinned generation. Later cleanup errors are reported separately and do not roll back the committed change.

During preparation, a candidate that fails to initialize or negotiate required services cannot replace an already healthy runtime. A newly discovered optional plugin may remain failed without disabling other plugins. Intentional removal remains separate from a failed replacement.

Reload preserves history, not an unconditional cache-hit guarantee. Identical model-facing definitions can reuse the provider prefix; changed tools, prompt sections, or deferred discovery state may invalidate it.

Dependent packages retire before their providers, including revocation of retained generations after a disable or removal. Optional dependency failures do not cascade into consumers. Retirement closes MCP connections, clears prompt and compaction registrations, cancels plugin executions, shuts down processes, revokes service routing, and removes owned snapshots. Shutdown retains access to the host services needed to release plugin-owned work before routing is closed. Plugins must still stop their own timers, subscriptions, and background work; the host cannot infer arbitrary resources created by extension code.

Cleanup produces structured revocation records, persisted in `plugin-generation-revocations.jsonl` under Wuu's home directory. These distinguish publication success from retirement failures and help diagnose resources that did not close normally.

## Services and executions

Runtime plugins communicate over JSONL. Versioned services use the `host.service.call` gateway: providers declare names, versions, and methods; consumers declare service names and major versions. The registry binds consumers to providers and supplies caller identity. A missing required provider prevents the consumer from becoming usable. Duplicate providers for the same name and major version are diagnosed, not combined.

Kernel services cover storage, settings, sessions, history, workspaces, artifacts, and execution operations. Feature services use the same routing mechanism. A service method's schema identifiers describe its contract; they are not permission to call another plugin's private process or read its private storage.

Tool and capability dispatch create bounded execution scopes. Progress, child-tool calls, questions, and artifacts attach to a live execution. Cancellation closes that scope without waiting for plugin acknowledgement, and late updates cannot revive it. Plugins receive an abort signal and must propagate it to cancellable work. Service-registry shutdown rejects new work, drains existing calls, and cancels what remains when its shutdown context expires.

Capabilities compose at defined points, such as adding prompt context or observing turn completion. They are not unrestricted interception of the agent loop or provider request. The [authoring reference](plugin-authoring.md) lists supported capabilities and error policies.

## Desktop generations and composition

The desktop loads an eligible package's ESM entry and calls `activate(api)`. Registrations belong to that package generation. The host validates declared contributions and view targets before publishing it, then disposes the prior desktop generation. A failed candidate registration is disposed without replacing the existing desktop generation.

Required providers must finish desktop activation successfully before consumers start. Independent branches may initialize concurrently. A provider replacement also reloads unchanged consumers; removing or disabling a required provider blocks them. Cleanup disposes consumers before providers. A failed replacement can preserve that package's previous desktop generation, but consumers do not activate against the unsuccessfully staged required replacement. Optional startup failures do not block consumers.

This desktop transaction is separate from the Go runtime transaction. Do not assume one atomic rollback spans package installation, runtime activation, and every renderer window. Inspect the runtime and desktop diagnostics for the surface that failed.

The UI extension points serve different purposes:

| Extension point | Role |
| --- | --- |
| Views and placements | Plugin-owned content in host-managed layout regions |
| Slots | Small contributions at fixed insertion points |
| Surfaces | Replacement or wrapping of a larger supported UI area |
| Presenters | Rendering of a versioned semantic snapshot with host-owned actions |
| Themes and scoped styles | Appearance within the published token and styling contracts |

Presenters receive snapshots rather than mutable host stores. An advertised action still passes the host's current writable-state and argument checks. Error boundaries and default rendering protect supported surfaces from ordinary contribution failures; they are not a security sandbox for malicious code. See [desktop plugins](desktop-plugins.md) for targets and composition rules.

## Trust, recovery, and compatibility

Plugins are trusted code running with user authority. Manifest permissions and service declarations describe contracts but do not isolate a process from the machine. Tool permission checks and filesystem process confinement protect their respective host execution paths; they do not turn arbitrary plugin code into untrusted sandboxed code.

A committed disable or removal revokes that plugin across retained generations: native executions are canceled, owned MCP servers cannot reconnect, and future legacy hooks and transforms are removed. Already-running legacy hooks keep their caller lifetime; existing transcript content and completed external effects are not erased. Same-ID updates instead let active work finish on its original implementation. Superseded idle runtimes are released without waiting for another user message; outstanding background work can keep an older implementation alive. Plugins with activation-owned background effects must tolerate that overlap while work drains. Storage and settings normally remain, and completed external effects are not undone. Safe mode and default-UI recovery provide ways to regain control after extension failures. Wuu does not audit or certify third-party packages; see the [security model](../reference/security-model.md).

Compatibility has several independent layers: product CalVer, package schema, runtime capability protocol, service major versions, and desktop snapshot versions. Match the SDK and host version you target. `wuu plugin dev ./extension.ts` also accepts one default-exported TypeScript runtime plugin without a manifest, build, package installation, or SDK dependency install. It snapshots the source and embedded SDK into a private candidate and requires Node.js 22.7 or newer. Single-file imports are limited to Node builtins and `@wuu/plugin-sdk`; use a package directory for other dependencies. See the [quickstart](plugin-quickstart.md). Build, package validation, runtime negotiation tests, and actual feature or UI checks each verify a different part of that path.
