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
| `requires` | Package IDs that must be available; a missing requirement leaves the dependent inactive |
| `breaks` | Hard incompatibilities; an active incompatible pair rejects the activation plan |
| `conflicts` | Soft conflicts reported without automatically disabling either package |
| `skills`, `hooks`, `mcpServers` | Contributions using the corresponding [skill](skill-authoring.md), [hook](hooks.md), and [MCP](mcp.md) contracts |
| `contributes` | Commands, themes, settings, UI declarations, and view entry points |

Package relationships are IDs, not version ranges or a dependency downloader. Dependency cycles reject the activation plan. `requested_permissions` describes requested authority; it does not sandbox the plugin process.

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

Commands under `contributes.commands` have a local ID and a kind. `prompt_template` references a bounded UTF-8 file inside the package and supplies a host-owned composer action. `runtime_action` points to a command registered by the desktop generation; declaring it alone does not execute code.

UI declarations belong under `contributes.slots`, `surfaces`, `presenters`, `navigation`, `workspaceTools`, or `settingsPages`. They describe placement and discovery, while the desktop module registers rendering. See [desktop UI plugins](desktop-plugins.md) for matching and composition rules.

## Runtime process and lifecycle

The host starts the manifest's command and exchanges one JSON object per line over stdin/stdout. Reserve stdout for protocol frames and stderr for diagnostics. The manifest transport name `wuu-plugin-v1` and the negotiated capability protocol number are different fields; new service-using runtimes should negotiate `protocol_version: 3`.

With `@wuu/plugin-sdk`, implement `RuntimePlugin` and pass it to `runJSONLRuntime`. The SDK handles framing, request IDs, cancellation signals, and the lifecycle-v1 handshake.

| Callback | Responsibility |
| --- | --- |
| `initialize(params, host)` | Return tools, capabilities, provided services, and required services |
| `activate(host)` | Start timers, subscriptions, or other behavior after activation |
| `executeTool(params, host, execution)` | Run a registered tool and return its result |
| `invokeCapability(params, host, execution)` | Handle a declared capability |
| `invokeService(params, host, execution)` | Serve one host-routed service call |
| `serviceChanged(params, host)` | Respond to a service resolution change |
| `shutdown()` | Stop plugin-owned work and release resources |

Initialization is preparation, not permission to start product behavior. The host permits only its read-phase services during preflight. Defer writes and background work until activation, and make shutdown safe after partial initialization. A generation can fail or be superseded before it becomes active.

## Tools and capabilities

Register model tools in the initialization result's `tools` array. Each tool needs an `id`, `description`, and object `input_schema`. The host creates a namespaced public name. `execution_scopes` can limit availability to `root`, `child`, or `collaboration`; `activity` describes read-only behavior, concurrency safety, risk, and whether the tool orchestrates child tools. Declare real effects rather than marking a writer read-only to bypass scheduling or permission checks.

Collaboration conversations coordinate work with read-only built-in tools. Extensions explicitly registered for `collaboration` may manage tasks and delegate execution; they must perform project writes in execution sessions. Verification sessions expose only read-only extension tools. These scopes are trusted-extension contracts, not a sandbox.

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

The current host exposes **`host.service.call` as the runtime gateway**. Declare each consumed service in `required_services` with its name and major version. Old direct frames such as `host.storage.get`, or declarations that require them in `required_host_services`, are not the current wire contract even though compatibility-shaped types remain in the SDK.

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

## Providing a service

Return `provided_services` during initialization and implement `invokeService`. A descriptor contains a dotted lowercase name, strict `MAJOR.MINOR.PATCH` version, and methods with `input_schema` and `output_schema` identifiers. These identifiers name contracts; they are not inline JSON Schema definitions.

Consumers declare the service name and major version, then call the gateway. The host supplies the caller identity and routes to the active provider. Do not address another plugin's process or depend on its private files. A package cannot provide and require the same service name in one initialization result. Missing required services block the consumer; duplicate providers for a name and major version produce diagnostics rather than merging their implementations.

## Execution and cancellation

Tool and capability calls have a host-owned execution ID. The SDK supplies `execution.signal`; pass it to cancellable operations and stop work when it aborts. Returning from the invocation closes its scope. Late progress cannot reopen a completed execution, and cancellation does not wait for the plugin to acknowledge it.

`reportExecutionUpdate`, `invokeTool`, `askUserQuestions`, and `importArtifact` call versioned services and require the corresponding declarations. An orchestrator uses `activity.orchestrator=true` and `execution.invoke-tool` to delegate effects through the normal tool path. Keep a child `call_id` stable across retries with identical tool name and arguments; do not reuse it for different work.

The `security.authorize` service may further restrict an operation, but cannot relax the host's mandatory boundary. A `sandbox.process` provider must supply real filesystem enforcement; it is not a command approval dialog. See [permissions](../reference/permissions.md) before implementing either.

## Development and distribution

`wuu plugin create` generates `agent`, `desktop`, or `full` package scaffolds. The current development command expects a directory with `plugin.json` and a `package.json` build script. `wuu plugin dev` rebuilds and publishes development generations; it is not a manifest-free single-TypeScript-file loader. The quickstarts show how to use the matching local SDK and bundle required runtime code.

Use `validate` for package structure, `test` for executable initialization and negotiated descriptors, and real conversations or rendered UI for behavior. `pack` creates a local zip; it does not upload to a registry. Inspect the archive before distributing it: preparation excludes `.git` and `node_modules`, not every local file that might contain private data.

The current install/update CLI stages packages and uses a separate approval step before code activation. Development-directory authorization does not become trust for a distributed package. See [plugin management](plugins.md) for the commands and [security](../reference/security-model.md) for the trust boundary.

Exact types live in [`packages/plugin-sdk/src/index.ts`](../../../packages/plugin-sdk/src/index.ts); Go integrations use [`packages/plugin-go`](../../../packages/plugin-go/runtime.go). Check these against the Wuu version you target. Product CalVer, package schema, capability protocol, service versions, and desktop snapshot versions are distinct compatibility checks.
