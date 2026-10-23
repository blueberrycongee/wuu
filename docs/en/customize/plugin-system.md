# Plugin architecture

Wuu loads plugins into a Go runtime host and an Electron desktop host. A package may use either host or both, and can also supply declarative contributions. This page explains how the current implementation discovers, activates, composes, and retires those contributions. For package fields and code examples, use the [authoring reference](plugin-authoring.md).

## Host and plugin responsibilities

The Go host owns tool dispatch, execution identity, service routing, session persistence, permissions, and resource cleanup. A plugin owns its feature logic, prompts, business state, and background policy. Features such as automation, memory, and subagents use host services instead of maintaining a second session engine.

The desktop host owns windows, layout, the conversation stream, input submission, and the bridge to the Go app-server. Plugins register views and contributions through the public SDK; they do not receive an Electron main-process object or direct access to host React state. The host provides React and shared UI components so plugin controls follow the same rendering and styling contracts.

Bundled and external packages use the same runtime and desktop registration contracts. Bundled provenance affects discovery and trust, not the meaning of a tool, capability, or UI registration. A copied package does not become official merely by using a bundled plugin's ID.

## Authoring SDK and process contract

Wuu maintains one official plugin authoring SDK: TypeScript `@wuu/plugin-sdk` in
`packages/plugin-sdk`. Bundled and external plugins use its runtime and desktop
contracts. The runtime transport remains language-independent JSONL: an external
process written in another language can implement that contract directly. Wuu
does not provide a second official authoring SDK for those processes.

The Go core remains the runtime host. `internal/sdk` is an internal facade for
embedding the host in Wuu entry points; it is not a plugin authoring API.

## Bundled TypeScript runtimes

The nine bundled business plugins are `todo`, `ask-user`, `memory`, `subagent`,
`peers`, `automation`, `goal`, `dream`, and `next-message`. Their source lives under
`plugins/<id>/src/`; each exports the public TypeScript `RuntimePlugin` contract.
The build produces self-contained `runtime.mjs` files beside the bundled
manifests. Go embeds those packages and remains the runtime host. The former
`wuu-<id>-plugin` Go helper executables are no longer needed.

The desktop starts these runtimes with its own Electron executable in Node mode;
the CLI uses Node from `PATH` or the absolute `WUU_NODE_EXECUTABLE` override.
There is no separate installed npm dependency for a bundled runtime. The same
JSONL initialization, service negotiation, activation, cancellation, and shutdown
contracts apply to bundled and third-party plugins.

This changes implementation language, not plugin identity or durable data layout.
Existing settings, approvals, plugin storage keys, memory Markdown files, and
session request identities remain in their existing locations. No export/import
step or second Go implementation is used. Module variables and timers are rebuilt
on replacement. Automation recovers durable work with stable dispatch identities;
Goal restores unfinished goals paused for an explicit resume; Dream retains its
settings, run state, and failure backoff. Disabling a plugin stops its owned
behavior while preserving durable data and conversation history.

For the edit-and-reload workflow and executable checks, see
[develop a bundled plugin](plugin-quickstart.md#develop-a-bundled-plugin).

## Discovery and activation

Discovery reads user packages, workspace packages, authorized development directories, and bundled packages. User packages normally live in `$WUU_HOME/plugins`; workspace packages live in `.wuu/plugins` under the workspace. Legacy singular `plugin` directories are also recognized. When IDs collide, later discovery normally replaces the earlier candidate; bundled packages are considered last. An explicitly authorized development package with the same ID overrides its bundled package while retaining development provenance. An ordinary untrusted same-ID copy does not gain that override.

Finding a manifest does not mean running it. The host evaluates enabled state, trust, platform and minimum-version compatibility, package relationships, and runtime negotiation. Required package dependencies use legacy `requires` IDs or versioned `dependencies` entries. Missing or incompatible required dependencies leave consumers and their transitive required dependents inactive; `breaks` and required dependency cycles can reject the activation plan. Matching optional dependencies add ordering when possible, but their absence or failure does not block consumers. `conflicts` produces a diagnostic rather than choosing a winner automatically.

The current local installer stages packages and updates for a separate trust decision. Development authorization applies to a specific directory. Package identity includes content and source context, so an ID or version string alone is not proof that the currently approved bytes are running. See [plugin management](plugins.md) for the actual install and update flow.

Package relationships do not install dependencies, enable disabled packages, or grant trust. They also do not grant service-call authority: runtime `provided_services` and `required_services` negotiate service names and major versions separately. See [package relationships](plugin-authoring.md#package-structure-and-manifest).

## Runtime generations

Reload does not transfer arbitrary process or module variables into the replacement. Keep durable business state in host storage or session records, and version any persistent data whose format can change. Conversation history and plugin implementation lifetime are separate.

A runtime generation groups the selected packages with their processes, tools, capabilities, hooks, skills, MCP connections, service registry, and owned package snapshots. Building a candidate does not immediately replace the active generation. Development publication may proceed while turns are running: each retained generation owns its package snapshot, so replacing the published source does not change the files used by running work. The host coordinates catalog reads with publication rather than blocking publication for an entire turn.

Lifecycle v2 separates declarations from state validation. `initialize` returns contracts without service calls. After binding the complete dependency graph, the host calls `prepare` in provider-before-consumer order. During this callback, declared services may be called only through methods marked `preparation: true`; these methods promise no durable writes or external effects. This applies to selected native and process providers alike. Plugins start timers and other active behavior in `activate`. Preparation can also start MCP connections. These are trusted processes: restrictions on host service calls do not prevent direct filesystem, network, or other external effects during initialization. Preparation is not a side-effect-free sandbox.

The runtime transition prepares the candidate, commits the associated package or policy changes, and only then calls native `activate`. If preparation or the durable commit fails, the candidate is closed and the old runtime remains current; candidate `activate` is not called.

Providers prepare and activate before their package and service consumers. A required provider's initialization or activation failure blocks its transitive required consumers, including relationships through declarative-only packages. Blocked consumers do not expose their tools, hooks, skills, MCP servers, or desktop contributions. Independent packages can still activate; a provider's own runtime failure does not automatically remove its independent declarative contributions.

If an already-active provider process exits, existing recovery checks revoke its transitive required consumers before preparing a replacement. This uses the normal consumer cleanup and blocks future access to their tools, hooks, MCP servers, skills, and desktop contributions. A failed replacement does not re-enable those consumers. Detection follows the existing recovery checks, not an immediate crash notification.

Revocation belongs to the affected generation. Retained conversation and worker toolkits stop listing or loading its plugin-owned skills, including when working in another checkout. Independent workspace skills remain available. A failure in a retained old generation does not revoke a healthy replacement with the same package ID. Already-materialized prompts and conversation history are not rewritten, and completed effects are not undone.

Disable and removal take effect after their changes commit and revoke the affected plugin across retained generations without waiting for an idle turn; inspect the operation result. To restart the app-server without activating plugin contributions, use its [safe-mode option](../automation/app-server.md).

After the commit succeeds, an activation failure publishes the new generation in a degraded state instead of restoring the old one. The operation reports that the change was committed but runtime activation failed, and the inventory retains the failure diagnostics. Mutation handlers still synchronize committed settings, finish removal cleanup where applicable, and notify clients. Refresh accepts that published state and logs its activation failure rather than treating it as a failed refresh to retry on every watcher interval. External effects already performed by activation cannot be rolled back.

For an ordinary same-ID update, an existing conversation adopts the published generation at its next idle turn boundary. A running turn, its outstanding workers (including terminal finalization), and its managed processes retain their generation until that work settles. The host rebuilds plugin bindings together while retaining conversation history and session identity; the previous generation retires after its last lease is released. An idle superseded runtime is released without waiting for another user message. Catalog-only changes and pre-commit failures do not replace a healthy pinned generation. Later cleanup errors are reported separately and do not roll back the committed change.

Local browser and computer-use control has a shorter lifetime than retained tool and prompt code. Before activating a replacement, the host revokes the outgoing generation’s local-control ownership and stops affected previews. Disable, removal, runtime closure, and an activity-bound MCP connection disconnect or replacement also revoke that owner. Plugin retirement is scoped to the plugin ID and generation ID; MCP connection retirement additionally identifies the exact connection. A late failure cannot stop a healthy replacement. A healthy MCP reconnect in the same plugin generation may serve a new explicit user run. Revocation blocks further local-control actions in the affected run; an old owner cannot reacquire control or switch to another local-control backend. A new explicit instruction is required to start fresh work.

Generation-owned `host.background` jobs have a different lifetime from conversation workers. Publication stops new background admission in the old generation and cancels its existing jobs and autonomous service executions before the replacement activates. Plugin-created timers and other direct effects still need explicit cleanup; host cancellation cannot undo effects already performed.

During preparation, a candidate that fails to initialize or negotiate required services cannot replace an already healthy runtime. A newly discovered optional plugin may remain failed without disabling other plugins. Intentional removal remains separate from a failed replacement.

When automatic refresh cannot prepare the same candidate, retries back off from one second to a maximum of 30 seconds. During that wait, background notifications and waiting turns share package-content checks at a one-second cadence; configuration or newly published catalog epochs can trigger an immediate check. Changed inputs can retry sooner. Successful refresh resets the delay. Lease contention uses a separate short retry and does not count as a failed candidate. The catalog's explicit Refresh action bypasses this automatic delay, reports preparation errors, and retains the previous generation on a pre-commit failure.

Reload preserves conversation history and the conversation-scoped `prompt_cache_key` used for supported provider routing. A stable cache key is not a content hash or a cache-hit guarantee. An implementation-only change can leave the model-facing prefix unchanged; changed tool definitions, prompt sections, or deferred discovery state change request content and can invalidate provider prefix reuse even with the same key.

Dependent packages retire before their providers, including revocation of retained generations after a disable or removal. Optional dependency failures do not cascade into consumers. Retirement closes MCP connections, clears prompt and compaction registrations, cancels plugin executions, shuts down processes, revokes service routing, and removes owned snapshots. Shutdown retains access to the host services needed to release plugin-owned work before routing is closed. Plugins must still stop their own timers, subscriptions, and background work; the host cannot infer arbitrary resources created by extension code.

Native and process runtime clients are owned by a generation-local [cordis-go](https://github.com/tr1v3r/cordis-go) scope. The resolved dependency graph supplies preparation and activation injections; the registry does not maintain a second native-provider lifecycle. Duplicate runtime identities reject the candidate before processes start. Client resources remain owned outside the activation scope so a prepared or failed client can still shut down, and providers remain available until consumer cleanup returns. Wuu serializes lifecycle transitions and waits for cleanup before returning from retirement. Preparation, activation, and cleanup panics become attributable errors. A closed generation cannot reactivate. Executions and generation leases keep the version-retention rules above; Cordis does not independently reload a retained generation.

Cleanup produces structured revocation records, persisted in `plugin-generation-revocations.jsonl` under Wuu's home directory. These distinguish publication success from retirement failures and help diagnose resources that did not close normally.

## Services and executions

Runtime process writes honor cancellation and a bounded transport deadline. If a frame is interrupted during delivery or an execution cancellation cannot be delivered, the host retires that transport. Canceling an invocation before it is sent leaves other running work intact. Shutdown terminates the runtime process group on macOS/Linux and requests process-tree termination on Windows, with a bounded pipe drain. Windows tree cleanup is best-effort if the runtime has already exited. Unexpected nonzero shutdown exits remain visible in retirement reports. Runtime stderr keeps the most recent 64 KiB and marks omitted earlier output; verbose logs cannot grow this buffer indefinitely. These limits do not shorten a running tool’s execution deadline or replace the generation-retention rules above.

Runtime plugins communicate over JSONL. Versioned services use the `host.service.call` gateway: providers declare names, versions, and methods; consumers declare service names and major versions. The registry binds consumers to providers and supplies caller identity. A missing required provider prevents the consumer from becoming usable. Duplicate providers for the same name and major version require an explicit choice; an ambiguous candidate is rejected before activation.

Select a runtime service provider in the trusted user configuration:

```json
{
  "composition": {
    "version": 1,
    "services": [
      { "service": "search.provider", "major": 1, "provider": "my-search" }
    ]
  }
}
```

The selected package must already be enabled and trusted and must declare the requested service major. Bindings do not install or enable packages. Duplicate bindings, an unavailable selected provider, and an ambiguous service reject preparation and preserve the previous running generation. A selected provider that crashes or is removed causes calls to fail; another registered implementation is never used as a fallback. With no explicit binding, a service must have exactly one provider. Registry inspection reports the selection layer in `binding_source`, or `unique-provider` for an unambiguous discovered service, alongside its version and provider ID.

Composition versions 1 and 2 are independent of product CalVer and service versions. Version 1 retains inline profiles and module/service choices. Bundles and patches require version 2, so older hosts reject the configuration instead of ignoring overrides. To downgrade, first replace the layered configuration with its complete resolved `modules` and `services` using version 1; merely changing the version number is unsafe. Existing configurations without this section retain unique-provider discovery. A configuration that previously depended on first-registration precedence must add explicit selections. Normal startup ignores project overrides of `composition`, including case variants, because bindings can select execution and credential policies. Apply a changed configuration at generation preparation; retained executions keep their existing registry. Restoring a previous selection creates a new generation and does not undo completed effects or migrate provider data.

A named runtime composition can also select the complete module set. For example, after installing and enabling both [service composition example packages](../../../examples/plugins/service-composition/README.md):

```json
{
  "composition": {
    "version": 1,
    "profile": "writing",
    "profiles": {
      "writing": {
        "modules": ["text-analysis-example", "writing-report-example"],
        "services": [
          { "service": "example.text.analysis", "major": 1, "provider": "text-analysis-example" }
        ]
      }
    }
  }
}
```

`profile` selects one entry in `profiles`. A profile supplies `modules`, ordered `bundles`, or `patches`, including all dependencies. Module order does not determine service selection or lifecycle order. An explicit empty array starts no modules. Omitting a profile, `modules`, and `patches` preserves enabled, trusted discovery defaults. An unknown profile or bundle, repeated bundle, duplicate module, excluded binding target, unavailable selected module, or failed selected runtime rejects preparation. Module selection cannot install, enable, or trust a package. Filtering happens before runtime, hook, skill, and MCP contributions are loaded.

Bundles are reusable static configuration. For example, the same analysis capability can be shared by several profiles:

```json
{
  "composition": {
    "version": 2,
    "profile": "writing",
    "bundles": {
      "analysis": {
        "patches": [{
          "modules": { "text-analysis-example": true },
          "services": [{ "service": "example.text.analysis", "major": 1, "provider": "text-analysis-example" }]
        }]
      }
    },
    "profiles": {
      "analysis": { "bundles": ["analysis"] },
      "writing": {
        "bundles": ["analysis"],
        "patches": [{ "modules": { "writing-report-example": true } }]
      }
    }
  }
}
```

Resolution starts with an empty module set and applies the selected profile’s bundles in listed order, then its inline `modules` / `services`, then its `patches`. Top-level `modules` / `services` follow, and top-level `patches` apply last. Each inline `modules` array replaces the complete set; services override by name and major. A patches-only composition also starts empty. Bundles contain only patches and do not execute code or include other bundles.

In a patch, `modules` maps IDs to `true` (include) or `false` (exclude). `services` replaces individual bindings; `remove_services` accepts `{ "service": "...", "major": 1 }` entries to remove explicit selections. Removing a binding returns to unique-provider resolution, so multiple remaining providers still fail. Excluding a module does not silently erase bindings that target it: remove or replace those in the same composition. Duplicate or conflicting operations within one patch fail; later patches may intentionally override earlier choices. Empty arrays survive configuration saves. Resolution builds fresh state, so one profile cannot mutate another.

The current native runtime modules are `kernel` (the native service collection), `wuu.default-loop` (the provider for `driver.default`), `wuu.tool-execution` (the declared consumer for tool security services), and `wuu.session-execution` (the declared consumer for the selected driver). Explicit module sets include these only when needed. Excluding `kernel` removes its services and result materializer; it does not select a substitute. These runtime choices do not yet replace the application shell or the complete session persistence implementation. Safe mode starts no composition modules, even when the configured selection refers to unavailable packages; discovery remains available for recovery.

`plugin/registry/introspect` returns `composition.profile`, ordered `layers`, the last module-set change in `module_source`, and each selected module’s `source`, package version and current runtime state. Sources identify inline choices (`profile:<name>` or `composition`) and zero-based patches (`bundle:<name>:patch:<index>`, `profile:<name>:patch:<index>`, or `composition:patch:<index>`). Native module versions identify the product build; service versions remain separate. Packages without a runtime have no runtime state. This diagnostic also works when the native registry-introspection provider is excluded. Same-ID updates retain running executions; removing a module revokes it across retained generations, with consumers completing cleanup before their providers stop.

Package dependencies and selected service dependencies share one generation lifecycle graph. Providers activate before consumers; cleanup runs in reverse order while provider resources remain available. A required missing service, incompatible major, or failed provider blocks transitive consumers. An absent optional service does not block activation; an available optional service participates in ordering. Cycles across package and service edges reject preparation before activation. Retained generations keep their own resolved graph when newer versions change requirements. Removing a provider stops its required consumers; independent plugins remain active.

An empty driver profile selects `driver.default` through the service graph. The native `wuu.default-loop` module and external providers use the same driver protocol, declared dependencies, cancellation, and cleanup. Excluding that module stops its driver; a composition can instead include an external provider of `driver.default`. Without a provider, new turns fail. The experimental `driver.<profile>` bridge pins its provider and callback table to the same generation. Descriptor, creation, and resume calls honor turn cancellation. A descriptor must supply a non-empty ID and a semantic version; errors are returned before creating an instance. Creation, resume, and terminal checkpoints must match the selected driver ID, version, and checkpoint contract. Unknown terminal statuses or incompatible checkpoints fail without replacing the last accepted state. Late instances returned after cancellation receive bounded shutdown. If a run fails or is canceled after saving a checkpoint, its result retains the latest accepted checkpoint so application finalization cannot overwrite it with the instance's initial state. Already observed history remains available for final persistence even when cancellation prevents a terminal receipt from arriving. `driver.<profile>` version 2 returns a structured `failure` alongside a failed status, receipt, and checkpoint. It preserves cancellation, deadlines, empty-answer and summary-output-limit codes, provider classification, and spent recovery budgets. Use `driverServiceDescriptor(profile)` and return `DriverRunResult`; service version 2 is independent of the driver's identity and checkpoint versions.

Conversations durably bind the service profile, provider ID, driver ID/version, and checkpoint contract before creating or resuming an instance. A changed default does not replace that identity. Rebuilt runtimes resolve the saved profile and reject a different provider or incompatible version before inference. The saved provider must still be selected for that service in the composition; changing the binding for the same service makes the old conversation read-only until its original selection is restored. Binding and checkpoint writes compare the stored identity atomically. A missing session or failed write cannot acknowledge a checkpoint. Fork creation inherits the source binding in the same transaction, while starting without the source execution checkpoint. Explicitly ephemeral conversations keep their binding and checkpoint in memory across runtime rebuilds; closing the conversation or process discards that state.

The additive `session_driver_bindings` table retains these identities in the existing session database. Its first migration pins older Wuu conversations to the historical native default, preserving any recorded checkpoint version. Old external checkpoints that never recorded a profile or provider retain their driver identity and fail with a migration diagnostic; the host cannot infer their missing binding safely. Keep the database and its WAL together during backup. Versions that ignore this table cannot safely resume conversations using replacement drivers.

Embedded Go callers must also select a driver explicitly. `StreamRunner` requires `LoopDriver` or `AcquireLoopDriver`; a worker manager without `AcquireLoopDriver` fails before lifecycle hooks, inference, or tool execution. `loopdriver.AcquireLocal(driver)` binds a caller-owned in-process driver and checks its persisted identity on every acquisition.

Background workers use the selected driver too. Before effects start, they save the service profile, provider ID, driver ID/version and checkpoint contract. Follow-up and restart retain that binding; unavailable or incompatible providers fail explicitly. Records from before driver selection retain the native default identity. Worker executions hold a generation lease through terminal persistence and cleanup, and use distinct callback IDs from their parent. Pending checkpoints preserve their execution input separately from the last committed terminal result, so crash recovery cannot erase a result awaiting delivery. A terminal snapshot commits the matching execution ID. Worker checkpoint files are part of the worker's recovery data and must be kept with its history; older versions that ignore driver bindings cannot safely resume workers using a replacement driver.

All drivers, including the default multi-round provider, declare `driver.turn` major 4 and `driver.checkpoint` major 1. The SDK's `driverTurn(host, execution)` exposes these operations:

| Operation | Observable behavior |
| --- | --- |
| `read()` | Returns bound model defaults, available tools, and current active history with recovered tool receipts. |
| `settings()` | Returns selected budgets, compaction settings, system-section metadata and available resources without executing them. |
| `contextState()` / `saveContextState(state)` | Reads or persists identity-bound request insertions and context estimates. Save insertions before model dispatch. An absent `retained` clears insertions; absent `usage` leaves estimates unchanged. Estimates do not replace authoritative inference usage. |
| `observe(observation)` | Reports request shape and compaction decisions. `before_compact` and `after_compact` invoke configured hooks; `after_compact` carries a classified `compact.failure` when the proposal failed. |
| `compact(messages, budget)` | Requests a proposal from the selected compaction provider using explicit recovery observations. The driver validates and commits it separately. A classified failure throws `DriverOperationError`. |
| `archiveContext()` | Archives accepted originals and returns their durable addresses without replacing active context. |
| `admitInput()` | Consumes queued input at the driver's chosen boundary and returns new contributions plus active context. Steering input is archived before its durable addresses are returned. |
| `requestContext()` | Reads request-only context segments, including typed blocks and their model-message projections. The driver decides assembly and retention; reading does not append them to history. |
| `transformRequest(request)` | Applies configured request transforms to an independently copied request. It does not admit input or call a model. A forced tool must be present in the resulting tool surface. |
| `context()` | Returns active model history, an execution-local revision, the durable archive head, and whether durable replacement is available. |
| `replaceContext(revision, messages)` | Archives accepted originals, then atomically commits the driver's replacement and any concurrent durable tail. Stale revisions fail before writing. |
| `model(request)` | Executes one streaming model request through the selected model pipeline. Returns the assistant response. It does not admit input, append request context, apply request transforms, or execute tools. A failed operation throws `DriverModelError`, retaining `round.messages`, reported `round.usage`, and structured `failure` evidence. |
| `modelWithTools(request, batchID)` | Explicitly permits safe prefix tools to start during model streaming. Reserves a unique batch ID before dispatch. A successful nonempty `tool_batch_id` must be joined through `tools()` with all returned calls in order, before admitting input, submitting other effects, replacing context, or completing. |
| `tools(batchID, messages, calls)` | Executes an explicit batch through the selected executor and durable tool ledger. Batch IDs are unique within an execution; call IDs are unique within a model round. A new model operation may reuse a provider call ID after previous batches settle. Calls can be chosen directly; missing assistant declarations are recorded before execution. Pending calls can be submitted in subsets; the reply contains only tool result messages. |
| `complete(content, messages?, completion?)` | Returns a terminal receipt and preserves finish reason, stop reason and truncation metadata. Model and tool messages are already recorded; optional messages are additional authored output. |
| `checkpoint(checkpoint)` | Saves state for this exact driver identity and checkpoint contract. Save intent before issuing external effects. |

Operations belong to the active execution and provider generation, run serially, and reject further effects after cancellation or completion. Model events use the session's event stream. Tool cleanup completes before its execution resources are released. Failed steering persistence prevents the new input from reaching the model. Cancellation does not undo external effects; an interrupted tool batch must be reconciled from durable receipts before it can be resumed safely.

Native driver modules can use the same independent turn operations. Their callback dependencies are explicit, and calls pass through the service registry with the provider's execution identity. An undeclared callback is rejected even for a native module. Model requests and context operations preserve client message IDs, transcript sequence numbers, steering markers, and tool invocation IDs; these are correlation metadata, not call authority.

`driver.turn` 4.0 adds the resources used by the default algorithm and scopes call IDs to each model round. Update the SDK and required service major together; earlier majors are rejected. Input admission, request context, and request transforms remain explicit: call `admitInput()` at the loop's input boundary, apply the driver's compaction policy, assemble `requestContext()` segments, and call `transformRequest()` before `model()`. Retrying a prepared model request does not consume more input or apply transforms twice. The checkpoint contract and default driver's state format remain version 1; existing session identities and histories are retained.

Model-operation failures return `failure` alongside partial assistant messages and reported usage. The SDK converts it to `DriverModelError`; native drivers receive `FailureError` with the partial `ModelRound`. Failure evidence includes origin, category, provider code, HTTP status, retry delay, context-overflow classification, and the recovery executor's stopping decision and attempt counts when available. A retryable cause does not grant a fresh retry budget. Transport, authorization, and malformed-request errors remain service errors. Direct protocol consumers must inspect `failure`; a response containing partial output does not imply success. Failed responses retain visible text and images in history, while incomplete tool declarations and provider replay state remain only in the failure reply.

Save intent before calling `modelWithTools()`, since tools may start before the model reply arrives. `round.tool_work_started` reports accepted work even when the model fails. Failed model operations cancel and drain their tools; their batch IDs cannot be reused. Call IDs remain reserved until the next accepted model operation. Successful streaming receipts wait for the original executions rather than issuing them again. Execution cleanup also drains orphaned and nested work before releasing tool providers. A model stream cannot automatically replay after tools have started, including when no durable ledger is configured.

Before either model operation dispatches, the execution service commits a model-input receipt when a receipt store is configured. It records the selected driver and execution identity, the final messages and durable input addresses, tools, and request settings under the model operation ID. A failed receipt write prevents model and streaming-tool execution. Cancellation during the write may leave a committed receipt without a submission; a receipt alone is not proof that the provider accepted or completed the request. Native and process drivers use this same barrier.

Replacing active context preserves the original transcript. If archive succeeds but checkpoint commit fails, the archive stays durable and the previous active context remains; read a fresh revision before retrying. Cancellation after commit does not undo the committed context. Missing archive or atomic checkpoint services make durable replacement unavailable. Embedded callers can explicitly pass `{ volatile: true }` only when no durable context writer is configured. That mode replaces execution memory, may archive originals if available, and promises no context checkpoint. It is never selected implicitly by the resource service.

The [evidence workflow example](../../../examples/plugins/workflow-loop/runtime.ts) uses only the public SDK. It plans one tool batch, commits an active context containing the current task and evidence, then runs a synthesis stage with tools disabled. This example requires durable context replacement. Its checkpoint records the current stage; recovery consumes settled tool receipts instead of replaying their effects. Run `node scripts/workflow-driver-e2e.mjs` from a checkout to install a packed SDK in an independent directory and verify real file evidence, original-history preservation, committed-context recovery after a default-profile change, interruption before the planned history is flushed, driver-controlled context-overflow recovery, and provider removal. The check also runs an independent SDK driver that reads a real file before the model finishes streaming and joins its durable result once. The same check runs the workflow as `driver.default` with the native default module excluded; set the example runtime environment variable `WUU_WORKFLOW_PROFILE=default` to declare that service. Repeatable logs and the installed package remain under `.tmp/e2e/workflow-driver/`.

The default `agent.DefaultDriver` owns the multi-round, compaction, overflow-recovery and termination policy. Its model, tool, context, observation and checkpoint effects use these same public resources. The former whole-loop gateway and host-owned loop entry have been removed; embedded callers must select the provider explicitly. The workflow example resumes its saved task context; it does not merge a new user task into an interrupted workflow. Driver profiles are currently exposed through the debug app-server `--driver` option, rather than a desktop selection control.

Kernel services cover storage, settings, sessions, history, workspaces, artifacts, and execution operations. Feature services use the same routing mechanism. A service method's schema identifiers describe its contract; they are not permission to call another plugin's private process or read its private storage.

Tool and capability dispatch create bounded execution scopes. Progress, child-tool calls, questions, and artifacts attach to a live execution. Cancellation closes that scope without waiting for plugin acknowledgement, and late updates cannot revive it. Plugins receive an abort signal and must propagate it to cancellable work. Service-registry shutdown rejects new work, drains existing calls, and cancels what remains when its shutdown context expires.

Native and process service providers use the same declarations, method checks, and callback scopes. Native consumers also declare dependencies and participate in the same lifecycle graph. A caller named `kernel` has no special authority. Driver instance IDs travel in domain payloads; they cannot be used as service execution IDs. `service.invoke.execution_id` belongs to the provider callback. When the consumer calls from a live invocation, `caller_scope` supplies its authenticated identity, kind (`tool` or `callback`), and session/workspace attribution. The SDK exposes this as `execution.caller`, including a separate `signal`. That signal survives individual service responses and aborts when the caller ends or the provider stops. Use it for resources such as a model stream that spans start/read/cancel calls. Use `execution.signal` for work bounded to the current callback. A nested call uses the provider's own execution ID; caller attribution never grants permission to impersonate the caller or its tool authority.

A method may declare `cleanup: true` to release resources after autonomous admission stops. Cleanup must not acquire resources or start work, and receives no caller scope. Registry snapshots identify implementations by provider ID and binding source; the former `kernel` flag has been removed. These contract changes do not change product CalVer or stored user data.

Capabilities compose at defined points, such as adding prompt context or observing turn completion. They are not unrestricted interception of the agent loop or provider request. The [authoring reference](plugin-authoring.md) lists supported capabilities and error policies.

## Desktop generations and composition

The desktop loads an eligible package's ESM entry and calls `activate(api)`. Registrations belong to that package generation. The host validates declared contributions and view targets before publishing it, then disposes the prior desktop generation. A failed candidate registration is disposed without replacing the existing desktop generation.

Each candidate has a 30-second asynchronous staging budget covering the desktop module request, ESM import, and `activate(api)`. Timeout, unload, or a newer inventory sync settles pending activation without waiting for the plugin to finish. Its partial registrations are cleaned up and late registrations are rejected. A timed-out replacement leaves its last working generation active.

`api.signal` is the generation's lifetime signal. Pass it to cancellable work and register resource cleanup before waiting. It is aborted on failed or superseded staging and on disposal of a published generation; successful activation clears the startup deadline. This is cooperative cancellation: it cannot interrupt synchronous JavaScript, stop an already-running import, or undo external side effects.

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

A committed disable or removal revokes that plugin across retained generations without waiting for an idle turn: native executions are canceled, owned MCP servers cannot reconnect, and future legacy hooks and transforms are removed. Already-running legacy hooks keep their caller lifetime; existing transcript content and completed external effects are not erased. Same-ID updates instead let active work finish on its original implementation. Superseded idle runtimes are released without waiting for another user message; outstanding conversation workers and managed processes can keep an older implementation alive. Plugins with activation-owned background effects must tolerate that overlap while work drains. Storage and settings normally remain, and completed external effects are not undone. Safe mode and default-UI recovery provide ways to regain control after extension failures. Wuu does not audit or certify third-party packages; see the [security model](../reference/security-model.md).

Compatibility has several independent layers: product CalVer, package schema, runtime capability protocol, service major versions, and desktop snapshot versions. Match the SDK and host version you target. `wuu plugin dev ./extension.ts` also accepts one default-exported TypeScript runtime plugin without a manifest, build, package installation, or SDK dependency install. It snapshots the source and embedded SDK into a private candidate and requires Node.js 22.7 or newer. Single-file imports are limited to Node builtins and `@wuu/plugin-sdk`; use a package directory for other dependencies. See the [quickstart](plugin-quickstart.md). Build, package validation, runtime negotiation tests, and actual feature or UI checks each verify a different part of that path.
