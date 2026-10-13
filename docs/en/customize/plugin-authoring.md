# Plugin authoring reference

A Wuu plugin can combine an agent runtime, desktop UI, skills, hooks, MCP servers, commands, themes, and settings in one local package. Use the [agent quickstart](plugin-quickstart.md) or [desktop quickstart](desktop-plugin-quickstart.md) for a complete example. This page describes the package and runtime contracts those examples use.

## Package structure and manifest

The package root contains `plugin.json`. Declare only the entry points and contributions you need:

```json
{
  "schema_version": 1,
  "id": "example-plugin",
  "name": "Example Plugin",
  "version": "0.1.0",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "node",
    "args": ["dist/runtime.js"]
  },
  "desktop": { "entry": "dist/renderer.js" }
}
```

This example needs both declared files. Omit `runtime` for a desktop-only or declarative package; omit `desktop` if no renderer code is needed. A desktop entry is a package-relative, self-contained ESM module. Runtime dependencies must also be available after packaging, which excludes `node_modules`; bundle JavaScript dependencies into the runtime entry where practical.

| Field | Contract |
| --- | --- |
| `schema_version` | Package schema, currently `1` |
| `id` | Stable package identity; keep it unchanged across updates |
| `version` | Plugin's version, separate from Wuu's product version |
| `minimum_wuu_version` | Minimum Wuu CalVer, such as `2026.9.1`; not a substitute for service/API compatibility |
| `platforms` | Optional supported host platforms |
| `requires` | Legacy required package IDs, without version constraints |
| `dependencies` | Package relationships as `{ id, version?, optional? }`; required dependencies must be available and match the version constraint |
| `breaks` | Hard incompatibilities; an active incompatible pair rejects the activation plan |
| `conflicts` | Soft conflicts reported without automatically disabling either package |
| `skills`, `hooks`, `mcpServers` | Contributions using the corresponding [skill](skill-authoring.md), [hook](hooks.md), and [MCP](mcp.md) contracts |
| `contributes` | Commands, themes, settings, UI declarations, and view entry points |

`dependencies` adds version checks without changing the package schema. For example:

```json
{
  "dependencies": [
    { "id": "text-analysis-example", "version": "^1.0.0" },
    { "id": "draft-export", "version": ">=1.2.0 <2.0.0", "optional": true }
  ]
}
```

Omit `version` to accept any package version. Constraints use SemVer syntax, including exact versions, comparisons, `^`, and `~`; a prerelease must be explicitly admitted by the constraint. These are plugin package versions, not Wuu's product CalVer. `requires` remains supported and combines with required `dependencies`. Do not declare the same dependency as both optional and required.

A missing, disabled, untrusted, or version-incompatible required dependency leaves its consumer and downstream required consumers inactive. Optional dependencies do not block activation when unavailable or incompatible. Available matching dependencies establish provider-before-consumer ordering; required dependency cycles reject the activation plan. An optional ordering edge that would create a cycle is skipped with a diagnostic. Relationships never download, install, enable, or grant trust to another package. `breaks` and `conflicts` remain ID lists. `requested_permissions` describes requested authority; it does not sandbox the plugin process.

Package dependencies and runtime service requirements serve different purposes. A package dependency selects an installed package and its lifecycle order. `provided_services` / `required_services` negotiate a named API and service major version; they do not select by package ID or inherit that package's version. Declare both when a feature needs a specific package and its API. A service-only consumer can instead accept any eligible provider of that service.

## Declarative contributions

Themes and settings need no desktop module. For example, merge this fragment into a manifest:

```json
{
  "contributes": {
    "themes": [{
      "id": "calm-night",
      "name": "Calm Night",
      "base": "dark",
      "tokens": {
        "--wuu-color-canvas": "#151820",
        "--wuu-color-accent": "#8fa7ff"
      }
    }],
    "settings": {
      "enabled": {
        "type": "boolean",
        "title": "Enable feature",
        "default": true,
        "scope": "workspace",
        "apply": "live"
      }
    }
  }
}
```

Theme tokens come from the [generated theme contract](theme-surface-matrix.md), not arbitrary host CSS variables. Settings support `boolean`, `string`, `number`, and `enum`, with `scope=user|workspace` and `apply=live|restart`. Read settings through `host.settings.get/list` services in the runtime or `host.getSetting` in a desktop view. Disabling or removing the package preserves settings and storage by default.

Commands under `contributes.commands` have a local ID and a kind. `prompt_template` references a bounded UTF-8 file inside the package and supplies a host-owned composer action. `runtime_action` points to a command registered by the desktop generation; declaring it alone does not execute code. `side_conversation` references a package-owned prompt and opens the host’s attached conversation surface. Its messages use the existing side-chat execution and history; the host retains its current permission boundary. The send request identifies the public `plugin.command` ID, and disabling or removing its owning package cancels active answers without deleting history. Same-ID updates apply to the next answer; an active answer keeps its retained generation. No desktop entry or helper process is required.

First-run setup preserves applied plugin choices if it is interrupted before completion. Inventory distinguishes the effective `enabled` state from an optional `enabled_override` user policy; an absent override means the package default applies.

UI declarations belong under `contributes.slots`, `surfaces`, `presenters`, `navigation`, `workspaceTools`, or `settingsPages`. They describe placement and discovery, while the desktop module registers rendering. See [desktop UI plugins](desktop-plugins.md) for matching and composition rules.

## Runtime process and lifecycle

The host starts the manifest's command and exchanges one JSON object per line over stdin/stdout. Reserve stdout for protocol frames and stderr for diagnostics. The manifest transport name `wuu-plugin-v1` and the negotiated capability protocol number are different fields; new service-using runtimes should negotiate `protocol_version: 3`.

The official plugin authoring SDK is TypeScript: `@wuu/plugin-sdk`. Implement `RuntimePlugin` and pass it to `runJSONLRuntime`; the SDK handles framing, request IDs, cancellation signals, and the lifecycle-v1 handshake. A process written in another language can implement the same JSONL contract, but Wuu does not provide a separate authoring SDK for it.

| Callback | Responsibility |
| --- | --- |
| `initialize(params, host, lifecycle)` | Return tools, capabilities, provided services, and required services |
| `activate(host, lifecycle)` | Start timers, subscriptions, or other behavior after the runtime change commits |
| `executeTool(params, host, execution)` | Run a registered tool and return its result |
| `invokeCapability(params, host, execution)` | Handle a declared capability |
| `invokeService(params, host, execution)` | Serve one host-routed service call |
| `serviceChanged(params, host)` | Respond to a service resolution change |
| `shutdown()` | Stop plugin-owned work and release resources |

Initialization is preparation, not permission to start product behavior. The host permits only its read-phase services during native preflight. Defer writes and background work to `activate`, and make shutdown safe after partial initialization or partial activation. Preparation and persistence failures preserve the old runtime without calling candidate `activate`. Once the change commits, an activation failure leaves a degraded new generation with failure diagnostics; it does not restore the old generation or undo external effects. Avoid duplicate effects when explicitly retrying activation. Trusted initialization code and MCP preparation may still have external effects outside the host service boundary. See [runtime generations](plugin-system.md#runtime-generations) for the commit boundary.

`lifecycle.signal` aborts when shutdown is requested or the transport closes.
Use it for activation-owned background work and stop timers in shutdown. Cleanup
may still use the unscoped lifecycle host to persist state or release resources;
do not make cleanup depend on an already-aborted signal.

## Tools and capabilities

Register model tools in the initialization result's `tools` array. Each tool needs an `id`, `description`, and object `input_schema`. The host creates a namespaced public name. `execution_scopes` can limit availability to `root` or `child`; `activity` describes read-only behavior, concurrency safety, risk, and whether the tool orchestrates child tools. Declare real effects rather than marking a writer read-only to bypass scheduling or permission checks. These declarations are trusted-extension contracts, not a sandbox.

The TypeScript runtime adapter honors `activity.concurrency_safe: true` for tool dispatch.
Adjacent explicitly safe tools may overlap, with at most eight tool handlers
running per runtime. Omitted or false values, unknown tool IDs, and ordinary
capability/service callbacks remain FIFO exclusive barriers. `read_only` alone
does not enable concurrency. These barriers apply across both shared and distinct
execution IDs. Initialize, activate, and shutdown also wait for previously
admitted reentrant callbacks before proceeding.

Protect shared mutable state before opting in. Keep each invocation's execution
context and scoped host with that invocation; do not store them in a shared current-call variable. Cancellation is registered while a
frame is read and checked again before its handler starts, including after
waiting for a tool slot. Canceling one execution aborts all its live callbacks
without canceling independent executions. Already running code must cooperate
with cancellation. Explicitly reentrant capabilities and services retain their
separate lane so nested host callbacks can finish even when tool slots are full.
The limit does not bound those callbacks or queued frames, and TypeScript overlap
does not move synchronous CPU work to worker threads.

Ordinary tools are PTC bindings by default when the session enables PTC. Set
`direct_only: true` for user interaction, explicit delivery, or lifecycle
controls that must remain model-direct. Such tools stay in the top-level
provider surface and reject nested invocation. The optional flag does not
grant permissions or change execution scopes; undeclared tools retain ordinary
routing. Keep this host metadata out of input schemas.

`executeTool` receives arguments and execution context such as `cwd`, call ID, and available session/turn identifiers. Validate arguments even when a schema is present. Return `{ result: { content: [...] } }`, setting `is_error: true` for a tool failure. Content can contain text and supported rich result parts. Use `importArtifact` for a file that should become a thread-owned artifact rather than returning a temporary path that may disappear.

Capabilities are declared separately, with an ID, `kind`, and version. Current supported capability IDs are:

| Capability | Kind and purpose |
| --- | --- |
| `agent.system_prompt.section` | `transform`: contribute a prompt section |
| `agent.request.transform` | `transform`: prepend system messages to the model request |
| `agent.pre_step` | `transform`: append identified messages before a step |
| `agent.turn.completed`, `agent.turn.lifecycle`, `agent.turn.interrupted` | `observe`: react to supported turn events |
| `plugin.client.request` | `decision`: dispatch requests from the plugin's desktop UI |
| `agent.compaction` | `decision`: experimental compaction contract |

`agent.request.transform` receives a provider-neutral request view; its public output is `prepend_system_messages`, not arbitrary mutation of a provider's wire request. The SDK marks compaction experimental, so do not treat it as a stable general replacement for the whole agent loop.

Capabilities can declare `priority`, `depends_on`, and `conflicts`. Higher priority runs first; equal priorities preserve discovery order. `error_policy` is `propagate`, `isolate`, or `ignore`: observers cannot propagate, and only observers may ignore. The built-in turn observers default to isolation; other capabilities default to propagation. Handle expected failures inside the plugin rather than relying on one blanket error policy.

## Calling host services

The current host exposes **`host.service.call` as the runtime gateway**. Declare each consumed service in `required_services` with its name and major version. Old direct frames such as `host.storage.get` are not the wire contract. The TypeScript SDK translates typed conveniences such as `host.call("host.storage.get", params)` to that gateway. Declare requirements only through `required_services`; helpers such as `requireKernelService` construct these versioned entries. There is no second routing path in the host.

For example, this initialization result requests storage reads:

```json
{
  "protocol_version": 3,
  "required_services": [
    { "name": "host.storage.get", "major_version": 1, "required": true }
  ]
}
```

Call it through the gateway after activation:

```ts
import type { RuntimeHost } from "@wuu/plugin-sdk";

async function readCounter(host: RuntimeHost): Promise<string | null> {
  const result = await host.call("host.service.call", {
    service: "host.storage.get",
    method: "call",
    params: { scope: "workspace", key: "counter" },
  }) as { value: string | null };
  return result.value;
}
```

`requireKernelService` builds the corresponding version-1 requirement. `host.supports("host.service.call")` checks the gateway, not whether a particular service provider exists. Optional services still need a declaration and error handling when unavailable.

| Service family | Use |
| --- | --- |
| `host.storage.get/set/delete/keys` | String values in the plugin's user or workspace namespace |
| `host.storage.compare-exchange` | Conditional replacement using `{ scope, key, expected, value }`; returns `{ swapped, value }` |
| `host.file-lock` | Acquire and release a process-safe lock under a host-provided data root |

| `host.model.request` | One bounded, text-only model request using host configuration |
| `host.model.invoke` | Independent full logical model calls and streams |
| `host.model.emit` | Events from a streaming adapter or middleware |
| `host.background` | Generation-owned calls to a plugin’s own service |
| `host.settings.get/list` | Declared setting values |
| `host.session.create/send/list/inspect/cancel/control` | Create and manage sessions through host lifecycle rules |
| `host.session.history.read/search` | Bounded history access with sequence snapshots and continuation cursors |
| `host.workspace.status/apply/discard` | Inspect or resolve work from an owned session workspace |
| `execution.update` | Progress for a live execution |
| `execution.invoke-tool` | Invoke a child tool from an orchestrator execution |
| `host.artifact.import` | Import a file or bytes as a thread-owned artifact |
| `host.user-question.ask` | Ask a structured question during an execution |

Most kernel services use method `call`. Storage is not a transaction across keys: use compare-exchange for concurrent updates to one value, check `swapped`, and bound retries. A separate read followed by an unconditional set can lose updates. Missing values are `null`; store structured values as strings, for example JSON.

For session creation, provide a stable `request_id`, `visibility=user|plugin`, and `context_source=fresh|fork|seed`. Sending also needs a stable request ID and `input.prompt`. Admission or queueing is not completion: inspect the turn or handle lifecycle events before consuming its result. Keep model prompts, business state, and retry policy in the plugin; use the host for execution, history, workspace changes, and recovery.

`host.workspace.status` and `apply` compare tracked files against the commit frozen at workspace creation, including committed, staged, and unstaged changes. `apply` writes the cumulative patch into the parent working tree without committing it, then removes the isolated workspace and rebinds the session. Conflicts, untracked files, or an unavailable baseline fail without discarding the workspace or its binding. Automatic cleanup also preserves committed work and workspaces whose baseline cannot be verified.

A completed turn may have an empty `final_output`: normal provider completion does not require an outward reply or an acknowledgement tool. It records the end of execution, not proof that the user's objective was fulfilled. Consumers must inspect results and evidence; transport failures and abnormal stops still fail.

The Peers plugin uses `if_running: "steer"` to deliver terminal replies into active work when possible. Replies arriving too late for that turn are retained for a follow-up; idle sessions still wake automatically. A `running` response with `steered: true` can still be an unconsumed input, not durable delivery. Peers keeps the reply pending until a durable receipt confirms delivery and retries inputs lost on restart with the same request ID.

### Shared file locks

Declare `{ name: "host.file-lock", major_version: 1, required: true }` when
read-modify-write operations on files must coordinate across plugin processes.
The service exposes `acquire` and `release`; `acquireFileLock` and `releaseFileLock`
are the SDK helpers. Acquire takes a relative lock-file path and one root:
`workspace_state`, `wuu_home`, or `project`. A successful acquisition returns an
opaque `lock_id`; release returns `{ released: boolean }`. All writers must use
the same lock path. The host owns the operating-system lock and releases handles
when their plugin generation closes.

Keep the unscoped host passed to initialization for this resource handshake.
Pass the current execution ID to acquisition, wait for its authoritative reply,
then check cancellation and release in `finally`:

```ts
const { lock_id } = await acquireFileLock(lifecycleHost, {
  root: "workspace_state", path: "example/state.json.lock",
}, execution.executionId);
try {
  execution.signal.throwIfAborted();
  await updateStateFile();
} finally {
  await releaseFileLock(lifecycleHost, lock_id);
}
```

Do not locally abandon an acquisition response: cancellation can race a successful
grant, leaving the caller without the handle it must release. The execution ID
lets the host cancel a waiting acquisition; generation cleanup remains the fallback
for process loss. The lock does not make arbitrary plugin filesystem access a
sandbox or create a transaction across different files.

## One-shot model requests

Declare `requireModelRequestService()` in your initialization result's `required_services`, then call `requestModel(host, input)` from a live host-owned tool, capability (including `plugin.client.request`), or service callback. Use the handler-scoped `RuntimeHost`; the TypeScript SDK supplies its execution ID. A generation-owned background job also supplies a live service callback. To outlive an observer or other handler, start such a job and make the request inside its callback. Activation and detached timers or tasks cannot call the model service directly without a live execution scope.

```ts
import { requestModel, type RuntimeHost } from "@wuu/plugin-sdk";

async function requestText(host: RuntimeHost, content: string) {
  return await requestModel(host, {
    messages: [{ role: "user", content }],
    max_output_tokens: 256,
    timeout_ms: 10_000,
  });
}
```

`host.model.request` version `1.0.0` uses method `call`, with schema identifiers `host.model.request.input.v1` and `host.model.request.output.v1`. Service negotiation checks the declared major version; SDK helper availability or `host.supports("host.service.call")` alone does not prove this service is available on an older host.

| Input | Contract |
| --- | --- |
| `messages` | 1–32 messages with `system`, `user`, or `assistant` roles; nonblank UTF-8 text, at most 256 KiB of content in total |
| `model_alias` | Optional existing `agent.model_aliases` name; omitted or empty uses the saved effective configuration's main model |
| `max_output_tokens` | Integer 1–8192; default 1024 |
| `timeout_ms` | Integer 1–45000; default 45000 |

`max_output_tokens` is a requested output-token limit, not a billing ceiling. Configured token-limit options cannot raise this call's limit. If provider preparation removes the limit or raises it above the requested value, the call fails with `unsupported_limit` before inference submission. This host-side check does not guarantee how a remote server enforces token limits or bills requests.

The timeout is bounded by both `timeout_ms` and the owning invocation's remaining lifetime. The runtime's default capability timeout is 30 seconds, so a request may end before 45 seconds. Set the package's runtime timeout deliberately rather than assuming the model service extends it. Cancellation, invocation completion, or generation revocation cancels outstanding provider work.

The remote Web client also has an independent 30-second RPC deadline for `api.invokeRuntime`; increasing the package timeout does not extend it. When that deadline expires, the client requests cancellation of the original host invocation.

The result contains `text`, `provider`, `model`, `finish_reason`, and `truncated`. Optional `usage` contains input, output, cache-creation, and cache-read token counts plus `cache_creation_unknown`; missing usage does not mean zero cost. Empty/non-text responses and responses requesting tool calls fail instead of starting a tool loop.

The host reuses configured credentials, provider preparation, capacity coordination, retries, and inference accounting. It adds no conversation history, agent-specific request hooks, or tools and creates no persistent session. Select and limit context in the plugin. The service does not accept credentials, endpoint URLs, or arbitrary provider overrides. Explain what draft/history text will reach the configured provider, bound request frequency and output, and treat cancellation as best effort at avoiding further work: a submitted request may already incur charges.

## Composable model extensions

Use `host.model.invoke` version 1 for full logical requests. The earlier
`host.model.request` text-only contract remains unchanged. Declare
`requireModelInvokeService()` in `required_services`; declarations grant access
to the service, while a live tool, capability, service, or background execution
supplies the lifetime. Independent inference does not create a conversation turn
or execute returned tool calls.

`LogicalModelRequestV1` carries `version: 1`, provider/model identity, ordered
messages and tools, cache hints, model options, tool choice, media admission and
native replay state. Messages retain reasoning blocks, provider-native items and
their fallback history, tool calls/results, discovered schemas, and resolved
image/file bytes. Rich tool results retain artifact and deferred-media references
and their budgeted `model_text`. Local working-copy paths, credentials, host retry
objects and execution authority are not exported. Preserve unknown provider option
keys and opaque native item data when copying a snapshot.

These are logical snapshots, not guarantees of identical HTTP bytes or a cache
hit. Plugins must invalidate retained snapshots when their context, route, model,
adapter, native-state compatibility or media changes. The host remains responsible
for configured credentials, provider preparation, cancellation and inference
accounting. Native forwarding cannot install arbitrary endpoints or credentials.

| Surface | Contract |
| --- | --- |
| `model.request.transform` | Version-1 `transform` capability. Receives `{ request, origin }`; returns `{ request? }` before provider dispatch. |
| `model.request.observe` | Version-1 `observe` capability. Receives request and terminal response/error phases, with the logical request and host-authenticated origin. |
| `model.middleware` | Version-1 `decision` capability with `propagate` errors. Wraps dispatch, returns a replacement response, or calls its single-use continuation. |
| `model.adapter` | Version-1 `decision` capability with `propagate` errors. Supplies an explicitly selected adapter implementation. |

Plugin-initiated independent calls bypass request hooks and around-call middleware,
so observing a normal request and replaying it does not recursively trigger the
same policy. `origin.kind`, `plugin_id`, operation IDs and available session context
are host metadata; supplying copied metadata cannot impersonate an agent request.

### Independent calls and streaming

`invokeModel(host, { request, model_alias?, max_output_tokens?, timeout_ms? })`
returns `{ response, origin }`. Use an existing configured alias to compare a
second model. An explicit alias supplies its configured options and effort defaults.
An explicit request effort selects that model's corresponding variant, and explicit
`provider_options` keys override those defaults. `request.adapter` takes precedence
over the resulting `provider_options.adapter`. Without an alias, the complete
request snapshot is preserved rather than refreshed from current configuration.
Output-token limits and timeouts are bounded by the host and owning
execution; missing usage does not imply zero cost, and cancellation cannot undo
an already submitted request. Full requests preserve their output limit unless
`max_output_tokens` overrides it. The independent-call timeout defaults to
120 seconds and accepts at most 600 seconds. Inputs and retained stream data are
bounded to 16 MiB; each stream retains at most 4096 events.

`startModelInvocation` returns an `invocation_id`. `readModelInvocation` accepts
that ID plus `cursor` and optional `wait_ms`, and returns ordered events, the next
cursor, `done`, and a terminal result or error. Handles belong to the current
plugin generation and execution. `cancelModelInvocation` stops an active stream.
`streamModel` wraps those methods as an async iterator and cancels unfinished work
when a consumer breaks or throws. Consume streams within the handler lifetime;
use a background job for work that must outlive the originating handler. Preserve
opaque `provider_state` and `lifecycle` payloads when forwarding events. If
`reset_partial` is true, discard prior partial output before accepting replacement
events from a retry.

A middleware receives `ModelMiddlewareInput` with `request`, `origin`, `stream`
and `continuation_id`. Forward through `invokeModel` or `startModelInvocation`
with that continuation ID and the unchanged request. The continuation may be used
once, only by its live callback; alias/output overrides are not accepted. Use the
request-transform seam for input edits. Returning a response without forwarding
short-circuits the remaining chain. Forwarding and then modifying the response
wraps downstream behavior without creating another inference operation.

### Replaceable adapters

Register `model_adapters: [{ id: "example.adapter", version: 1 }]` during
initialization and implement `model.adapter`. IDs are globally unique dotted
names, at most 160 characters, with lowercase letters, digits, `_` and `-` in
each segment; duplicate registrations anywhere in a candidate generation fail.
Select an implementation with `request.adapter`, or set `options.adapter` on a
configured model for foreground calls. An explicit request value takes precedence;
`"native"` selects the host protocol. The reserved option is stripped before native
transport. A missing or revoked selected adapter fails without falling back.
An adapter can return synthetic output or delegate
a separate bounded call through `host.model.invoke` with `adapter: "native"`.

The callback receives `{ adapter_id, request, stream }`. For a unary call, return
`{ response }`. For a stream, either return that response or emit ordered events
with `emitModelEvent`, ending in exactly one `done` or `error`, then return `{}`.
Declare `requireModelEmitService()` to emit. Mixing response and emitted events,
missing terminal events, or emitting outside the exact streaming callback fails.
No credential is sent to the plugin.

Adapter accounting is declared at registration:

- `mode: "direct"` (the default) records a potentially billable submission and
  checks the host workflow budget before invoking the callback. Returned usage
  is plugin-reported usage for this adapter's work. Omitted usage remains unknown,
  not zero. Any additional `host.model.invoke` calls have their own submissions
  and costs.
- `mode: "delegate"` is for wrappers such as
  `{ id: "example.forward", version: 1, mode: "delegate" }`. The wrapper has
  logical-operation accounting but no billable parent submission. Its response
  usage is a summary only; each host model child records its own actual provider
  submissions and usage, so forwarding a response does not charge it twice.

These modes are explicit accounting contracts for trusted extensions. Native
forwarding uses the host's configured credentials and provider transport. The
Extension API does not provide a credential-attaching arbitrary HTTP transport,
and Wuu cannot enforce its inference budget over arbitrary network requests made
by plugin code outside host services.

`model.adapter` and `model.middleware` handlers are intrinsically reentrant in
the TypeScript runtime adapter: they can overlap a tool or callback that is
awaiting them. Protect shared mutable state. Additional `concurrentCapabilities`
and `concurrentServices` remain explicit opt-ins; lifecycle barriers still apply.

### Generation-owned background work

Declare `requireBackgroundService()`, provide a versioned service with a finite
handler, then call `startBackground(host, { key, service, method, params?,
timeout_ms? })` after activation. The target must be a service owned by that same
plugin generation. Starting returns `{ job_id, execution_id }`; the callback
receives a normal scoped host and cancellation signal. It may call the model
service after the originating conversation or observer handler has returned.

Use `inspectBackground(host, { job_id })` for `running`, `completed`, `failed` or
`cancelled` and any result/error; use `cancelBackground` to stop it. A running key
is unique per plugin, with at most 64 running jobs and 64 retained terminal results.
Parameters and results are each bounded to 1 MiB. Handles and results are not a
durable scheduler: reload, disable, generation retirement and process loss cancel
owned work; old generations cannot start more. Plugins own timers, scheduling,
budgets, snapshot invalidation and durable business state, and clear timers during
shutdown.

The public-SDK [bounded replay fixture](../../../scripts/fixtures/model-pipeline/replay.ts)
and [shadow comparison fixture](../../../scripts/fixtures/model-pipeline/shadow.ts)
exercise these surfaces with synthetic data. They are acceptance fixtures, not
installed caching or comparison products. Run
`node scripts/plugin-model-pipeline-e2e.mjs` against a source-built core; the driver
uses a local fake provider and records repeatable evidence without paid inference.

## Providing a service

Return `provided_services` during initialization and implement `invokeService`. A descriptor contains a dotted lowercase name, strict `MAJOR.MINOR.PATCH` version, and methods with `input_schema` and `output_schema` identifiers. These identifiers name contracts; they are not inline JSON Schema definitions.

Consumers declare the service name and major version, then call the gateway. The host supplies the caller identity and routes to the active provider. Do not address another plugin's process or depend on its private files. A package cannot provide and require the same service name in one initialization result. Missing required services block the consumer; duplicate providers for a name and major version produce diagnostics rather than merging their implementations.

The [service composition example](../../../examples/plugins/service-composition/README.md) contains two buildable packages: a stateless text-analysis provider and a writing-report tool that consumes its version-1 API. It demonstrates both declarations and a real gateway call.

For composition acceptance, exercise more than successful service calls: activate a provider and a consumer with a package-owned skill, then terminate the provider and make its next initialization fail. After a recovery check, verify that retained and new consumer toolkits cannot load the skill or call the revoked contributions, while independent packages and ordinary workspace skills still work. Verify that an already-created prompt remains unchanged. This scenario needs no model request: a JSONL runtime fixture and direct `load_skill` calls can check the lifecycle boundary.

### Reentrant service handlers

The TypeScript runtime adapter keeps ordinary requests ordered by default. Set `RuntimePlugin.concurrentServices` to the names of provided services whose handlers may safely overlap other handlers:

```ts
const plugin: RuntimePlugin = {
  concurrentServices: ["example.text.analysis"],
  // initialize, invokeService, and other callbacks...
};
```

Use this only for reentrant handlers: protect shared mutable state and propagate cancellation. In an A → B → A service call chain, A's callback service must be able to run while its original handler awaits B. Opt-in applies to incoming `service.invoke` requests for the listed names; it does not make every tool or capability concurrent, grant service authority, or resolve package dependency cycles. `RuntimePlugin.concurrentCapabilities` provides the same opt-in for listed capability IDs, for example a read-only status request while a foreground task waits. Protect the state each concurrent handler reads or writes. Initialization and activation remain barriers. Shutdown stops new admission and waits for admitted work before cleanup. This is asynchronous overlap in one JavaScript process, not worker-thread execution.

## Execution and cancellation

Tool and capability calls have a host-owned execution ID. The SDK supplies `execution.signal`; pass it to cancellable operations and stop work when it aborts. The `host` passed to tool, service, and capability handlers automatically binds
both the execution ID and its abort signal to service calls. Use that handler host
for foreground work instead of an unscoped host saved at initialization.
`host.call(method, params, { signal })` can add cancellation; it cannot remove the
execution's signal. A canceled pending call rejects and a late response is ignored.
Returning from the invocation closes its scope. Late progress cannot reopen a completed execution, and cancellation does not wait for the plugin to acknowledge it.

The TypeScript runtime adapter rejects cancelled tool, capability, and service
requests before entering their handlers, including requests waiting behind other
work or a lifecycle barrier. Cancelling an execution does not skip lifecycle
callbacks needed for cleanup, and reaches every live callback sharing that
execution ID.
Once a handler has started, cancellation remains cooperative: propagate its
abort signal to operations that support it. Cancellation cannot
undo completed effects or forcibly interrupt arbitrary plugin code.

App-server's read-only `plugin/executions/list` returns `{ executions: [...] }`
for the connected runtime's current generation and older generations still held
by active work. Each row adds `generation_id` and `retained` to the host-local
`id`, owning `plugin_id`, available execution scope, and latest progress. Use the
pair `(generation_id, id)` as a stable diagnostic key: different generations can
both have `exec-1`. The original `id` and cancellation routes do not change.
Rows are sorted by generation ID, then execution ID, and disappear when the
invocation finishes; reading the list does not keep a generation alive. This is
a point-in-time view, not execution history or a cross-workspace inventory.
Disabling a plugin cancels its work across retained generations as well.

`reportExecutionUpdate`, `invokeTool`, `askUserQuestions`, and `importArtifact` call versioned services and require the corresponding declarations. An orchestrator uses `activity.orchestrator=true` and `execution.invoke-tool` to delegate effects through the normal tool path. Keep a child `call_id` stable across retries with identical tool name and arguments; do not reuse it for different work.

The `security.authorize` service may further restrict an operation, but cannot relax the host's mandatory boundary. A `sandbox.process` provider must supply real filesystem enforcement; it is not a command approval dialog. See [permissions](../reference/permissions.md) before implementing either.

## Agent-managed development

The built-in deferred `plugin_manager` tool accepts `status`, `apply`, `enable`, and `disable`. `apply` takes a workspace-local TypeScript file or development package directory and performs a one-shot publish through a host-selected CLI bridge. It accepts neither arbitrary commands nor a caller-selected Wuu home. Source execution requires host-code authorization before validation/import, separately from permission to write the source file. See the [agent workflow](plugin-quickstart.md#let-the-agent-add-a-tool).

Status returns a `plugins` list. Compare `published_fingerprint` with `current_conversation_fingerprint` and inspect `adoption_pending`, `desired_enabled`, `trusted`, and `current_conversation_runtime_state`. An `error` reports runtime diagnostics when present. These fields report publication, policy, and this conversation's bindings; they do not prove a particular tool succeeded or a desktop view rendered.

An ordinary update leaves the current turn and its outstanding workers or managed processes on their tool and prompt generation. Discover and invoke updated tools at the next idle turn boundary after that work settles. A committed disable or removal instead revokes the plugin across retained generations without waiting for an idle turn. Publication is not a tool invocation or a rendered UI check, and it does not schedule a continuation by itself.

## Development and distribution

`wuu plugin dev ./extension.ts` runs a single file whose default export implements `RuntimePlugin`. Wuu supplies the matching SDK and generates the process adapter internally; manifests, installation, packaging, and builds are optional for this development path. It requires Node.js 22.7 or later with `--experimental-transform-types` support and supports only Node builtins and `@wuu/plugin-sdk` imports. It snapshots each candidate and validates syntax, imports, and its default export before publication, retaining the last published source on loading failure. Import validation executes module top-level code, so it is not a side-effect-free check. `initialize` runs only in the actual host with its read-phase services. Preparation failure preserves the old runtime; activation happens after commit and can leave a failed new runtime, as described above. See the [agent quickstart](plugin-quickstart.md) for a complete example.

`wuu plugin create` generates `agent`, `desktop`, or `full` package scaffolds. For a directory, `wuu plugin dev` uses its `plugin.json` and `package.json` build script, then publishes the built development generation. Both paths keep watching after a failed build/load so fixing and saving recovers without restarting. Filesystem events are debounced, with periodic reconciliation for missed events; `--poll` must be positive.

For monorepo packages, `package.json` may declare additional build inputs with `"wuu": { "dev": { "watch": ["../shared/src", "../assets/prompt.md"] } }`. Paths are relative to the package directory; sibling files and directories are supported. Wuu watches and fingerprints those inputs as well as the package, so edits rebuild and publish without restarting. Absolute paths, package ancestors, symlinked inputs, and more than 64 additional inputs are rejected. Keep generated files out of the list; `.git`, `node_modules`, and `dist` descendants remain ignored. This metadata affects development watching only, not runtime permissions.

Use `validate` for package structure, `test` for executable initialization and negotiated descriptors, and real conversations or rendered UI for behavior. `pack` creates a local zip; it does not upload to a registry. Inspect the archive before distributing it: preparation excludes `.git` and `node_modules`, not every local file that might contain private data.

The current install/update CLI stages packages and uses a separate approval step before code activation. Development-directory authorization does not become trust for a distributed package. See [plugin management](plugins.md) for the commands and [security](../reference/security-model.md) for the trust boundary.

Exact authoring types live in [`packages/plugin-sdk/src/index.ts`](../../../packages/plugin-sdk/src/index.ts). The Go host and its internal application-embedding facade are host implementation code, not additional plugin authoring SDKs. Check these against the Wuu version you target. Product CalVer, package schema, capability protocol, service versions, and desktop snapshot versions are distinct compatibility checks.
