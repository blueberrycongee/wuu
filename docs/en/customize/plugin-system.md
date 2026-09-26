# Plugin architecture

Wuu loads plugins into a Go runtime host and an Electron desktop host. A package may use either host or both, and can also supply declarative contributions. This page explains how the current implementation discovers, activates, composes, and retires those contributions. For package fields and code examples, use the [authoring reference](plugin-authoring.md).

## Host and plugin responsibilities

The Go host owns tool dispatch, execution identity, service routing, session persistence, permissions, and resource cleanup. A plugin owns its feature logic, prompts, business state, and background policy. Features such as automation, memory, and subagents use host services instead of maintaining a second session engine.

The desktop host owns windows, layout, the conversation stream, input submission, and the bridge to the Go app-server. Plugins register views and contributions through the public SDK; they do not receive an Electron main-process object or direct access to host React state. The host provides React and shared UI components so plugin controls follow the same rendering and styling contracts.

Bundled and external packages use the same runtime and desktop registration contracts. Bundled provenance affects discovery and trust, not the meaning of a tool, capability, or UI registration. A copied package does not become official merely by using a bundled plugin's ID.

## Discovery and activation

Discovery reads user packages, workspace packages, authorized development directories, and bundled packages. User packages normally live in `$WUU_HOME/plugins`; workspace packages live in `.wuu/plugins` under the workspace. Legacy singular `plugin` directories are also recognized. When IDs collide, later discovery replaces the earlier candidate; bundled packages are considered last.

Finding a manifest does not mean running it. The host evaluates enabled state, trust, platform and minimum-version compatibility, package relationships, and runtime negotiation. Missing `requires` dependencies leave consumers inactive; `breaks` and dependency cycles can reject the activation plan. `conflicts` produces a diagnostic rather than choosing a winner automatically.

The current local installer stages packages and updates for a separate trust decision. Development authorization applies to a specific directory. Package identity includes content and source context, so an ID or version string alone is not proof that the currently approved bytes are running. See [plugin management](plugins.md) for the actual install and update flow.

## Runtime generations

A runtime generation groups the selected packages with their processes, tools, capabilities, hooks, skills, MCP connections, service registry, and owned package snapshots. Building a candidate does not immediately replace the active generation.

During preflight, processes initialize and describe their contracts. Only read-phase host services are available then; plugins should start timers and other active behavior in `activate`, not `initialize`. The host checks the candidate before binding it into the session.

Activation switches the workspace's live plugin bindings under a transaction lock. If candidate activation fails, it is closed. If the associated durable commit fails after rebinding, the old live bindings are restored and the candidate is closed. After a successful publication, later conversations use the new generation. Conversations that already started keep the generation they pinned until they rebuild; the previous generation is retired only after those conversations release it. A later cleanup error is reported rather than rolling back the successful replacement.

Retirement closes MCP connections, clears prompt and compaction registrations, cancels plugin executions, shuts down processes, revokes service routing, and removes owned snapshots. Shutdown retains access to the host services needed to release plugin-owned work before routing is closed. Plugins must still stop their own timers, subscriptions, and background work; the host cannot infer arbitrary resources created by extension code.

Cleanup produces structured revocation records, persisted in `plugin-generation-revocations.jsonl` under Wuu's home directory. These distinguish publication success from retirement failures and help diagnose resources that did not close normally.

## Services and executions

Runtime plugins communicate over JSONL. Versioned services use the `host.service.call` gateway: providers declare names, versions, and methods; consumers declare service names and major versions. The registry binds consumers to providers and supplies caller identity. A missing required provider prevents the consumer from becoming usable. Duplicate providers for the same name and major version are diagnosed, not combined.

Kernel services cover storage, settings, sessions, history, workspaces, artifacts, and execution operations. Feature services use the same routing mechanism. A service method's schema identifiers describe its contract; they are not permission to call another plugin's private process or read its private storage.

Tool and capability dispatch create bounded execution scopes. Progress, child-tool calls, questions, and artifacts attach to a live execution. Cancellation closes that scope without waiting for plugin acknowledgement, and late updates cannot revive it. Plugins receive an abort signal and must propagate it to cancellable work. Service-registry shutdown rejects new work, drains existing calls, and cancels what remains when its shutdown context expires.

Capabilities compose at defined points, such as adding prompt context or observing turn completion. They are not unrestricted interception of the agent loop or provider request. The [authoring reference](plugin-authoring.md) lists supported capabilities and error policies.

## Desktop generations and composition

The desktop loads an eligible package's ESM entry and calls `activate(api)`. Registrations belong to that package generation. The host validates declared contributions and view targets before publishing it, then disposes the prior desktop generation. A failed candidate registration is disposed without replacing the existing desktop generation.

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

Disabling a plugin removes its contributions from later conversations. A conversation that already started keeps the generation it pinned until it rebuilds. Storage and settings normally remain, and completed external effects are not undone. Safe mode and default-UI recovery provide ways to regain control after extension failures. Wuu does not audit or certify third-party packages; see the [security model](../reference/security-model.md).

Compatibility has several independent layers: product CalVer, package schema, runtime capability protocol, service major versions, and desktop snapshot versions. Match the SDK and host version you target. The current `wuu plugin dev` path builds a manifest-bearing package directory; it does not directly load an arbitrary TypeScript file. Build, package validation, runtime negotiation tests, and actual feature or UI checks each verify a different part of that path.
