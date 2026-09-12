# Wuu Plugin authoring reference

This is the complete Wuu Plugin reference: package shape, Agent protocol, Desktop API,
lifecycle, local development, and trust boundaries. Use it after choosing a plugin
type rather than as the first introduction.

Start from your goal:

- [Extend Wuu](index.md) — compare Skills, MCP, Hooks, and Wuu Plugins;
- [Agent plugin quickstart](plugin-quickstart.md) — register a model-visible tool;
- [Desktop plugin quickstart](desktop-plugin-quickstart.md) — add a Composer control;
- [Desktop UI extension map](desktop-plugins.md) — choose a View, Slot, Presenter, or Surface;
- [Desktop plugin recipes](plugin-recipes.md) — combine common capabilities.

For user-side installation and management, see [Wuu Plugins](plugins.md). Complete the
matching quickstart before returning to this reference for exact fields and APIs.

The Wuu plugin platform is local-first: there is no marketplace or central
registry. Plugins are installed locally as directories or zip packages.
Developers usually maintain their plugins in their own GitHub repositories, and
users clone or download them before installing in Wuu. Distribution features
will only be built once a natural ecosystem emerges; plugin authors do not need
to prepare for any platform account or review process today.

## What a plugin can do

One plugin package can contain any combination of the following contributions:

| Contribution | Where it runs | Code required? |
| --- | --- | --- |
| Declarative theme | Renderer (CSS tokens) | No |
| Declarative settings | Renderer + app-server | No |
| Agent plugin | Separate runtime process (Node, etc.) | Yes |
| Desktop plugin | Wuu Renderer (ESM module) | Yes |

Plugins can form a strong visual language across the whole product through
public tokens, the UI Kit, and coarse semantic boundaries — never by taking
over private DOM. Window safe areas, navigation structure, tabs, scrolling,
overflow, keyboard, and recovery paths stay with the host. High-trust
capabilities such as Surface replacement are an escape hatch for complex
structural customization, not the default entry point for ordinary controls.
See the [plugin system architecture](plugin-system.md) for the overall design and
interface-selection principles.

## Package structure and manifest

```text
my-plugin/
├── plugin.json
└── dist/
    ├── runtime.js     # Agent plugin entry (optional)
    └── desktop.js     # Desktop plugin entry (optional)
```

`plugin.json` is the manifest. Wuu re-reads and validates it on install and
before every load. Minimal example:

```json
{
  "schema_version": 1,
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "description": "What this plugin does",
  "icon": "plug",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "node",
    "args": ["dist/runtime.js"]
  },
  "desktop": {
    "entry": "dist/desktop.js"
  }
}
```

Both `schema_version` and `schemaVersion` are accepted for compatibility, but a
manifest must choose one; declaring both is invalid. New manifests should use the
scaffolded `schema_version` form.

Common fields:

- `id` is a globally unique identifier. It determines the install directory
  name and the namespace prefix of every registration; once published it
  should never change.
- `version` is a semantic version. Updates from the same source identity keep
  trust; Wuu does not re-approve per file change.
- The top-level `icon` is the plugin's brand icon, used in the plugin catalog
  and detail views; it does not automatically enter host navigation. Prefer a
  public semantic icon name so the mark follows the active theme. Custom
  artwork is also accepted as `{ "path": "assets/icon.svg" }` or
  `{ "light": "assets/icon-light.svg", "dark": "assets/icon-dark.svg" }`.
- `runtime` declares a long-lived external process that talks to Wuu over
  standard input/output (an Agent plugin).
- `desktop.entry` points to a package-relative `.js` or `.mjs` browser ESM file, using
  slash separators and remaining inside the package. The entry is limited to 10 MiB.
- `contributes.themes` declares themes; `contributes.settings` declares
  settings.
- `skills`, `hooks`, `mcp_servers`, and `commands` let the plugin provide
  these capabilities directly, with the same effect as configuring them by
  hand.
- `minimum_wuu_version` declares the minimum Wuu version; the plugin is not
  activated when the requirement is not met.
- `requires` lists plugin IDs that must be enabled together; the plugin stays
  inactive when any is missing. `breaks` declares explicit incompatibilities;
  the host refuses to enable both. `conflicts` only shows a warning in the
  plugin catalog and leaves the decision to the user. All three are simple ID
  arrays; there is no version range or automatic solving.

The authoritative field definitions are
[`internal/plugin/manifest.go`](../../../internal/plugin/manifest.go) and
[`packages/plugin-sdk`](../../../packages/plugin-sdk/).

## Declarative contributions

### Themes

Declare them in `contributes.themes`; no code is required. Themes of enabled
plugins appear under **Settings → Appearance**; when the plugin is
disabled or the user switches back to a built-in theme, Wuu removes every
token contributed by that plugin's settings.

```json
{
  "contributes": {
    "themes": [
      {
        "id": "my-dark",
        "name": "My Dark",
        "base": "dark",
        "tokens": {
          "--wuu-paper": "#111827",
          "--wuu-ink": "#f9fafb"
        }
      }
    ]
  }
}
```

Public tokens are defined once by
[`config/desktop-theme-contract.json`](../../../config/desktop-theme-contract.json),
which generates the manifest, the public SDK, and the Desktop validators.
Stable families cover semantic colors, typography, spacing, density, radius,
borders, elevation, motion, content width, and `--wuu-syntax-*` syntax colors.
Early names such as `--wuu-paper`, `--wuu-ink`, `--wuu-accent`, and `--hljs-*`
remain compatible and are mapped when applied; new themes should prefer the
current `--wuu-color-*` and `--wuu-font-*` names.

Shared neutral UI is skinned through a few coarse semantic tokens, so an
appearance plugin does not need private DOM. The complete token list,
descriptions, and host wiring status are generated from the theme contract and
the host stylesheet dependency graph and published on the [theme token
reference](theme-surface-matrix.md). Text and fonts continue to inherit the
corresponding public color and typography tokens.
Compact controls keep host-owned border widths so a strong panel border
cannot change their box model or break text wrapping.

### Settings

`contributes.settings` generates controls of four types: `boolean`, `string`,
`number`, and `enum`. Every setting has a `scope` (`user` or `workspace`) and
an `apply` mode (`live` or `restart`). After the user changes a value in the
settings UI, the agent runtime can read it through the versioned
`host.settings.get/list` services; desktop views use `host.getSetting` from
their render props. Settings and Storage are stored under the plugin
namespace, and desktop views use `host.getStorage` / `host.setStorage` for
Storage. Disabling, upgrading, or uninstalling a plugin keeps this data by
default so it can be restored on reinstall; there is no implicit
"delete data on uninstall" behavior today.

```json
{
  "contributes": {
    "settings": {
      "enabled": {
        "type": "boolean",
        "title": "Enable counter",
        "default": true,
        "scope": "user",
        "apply": "live"
      }
    }
  }
}
```

## Agent plugins

An Agent plugin is an external process declared by `runtime`. Installing or
enabling the plugin grants that process the same user authority as Wuu, so
only install plugins you trust, and check the source before enabling.

### Process and protocol

Wuu starts the runtime process; it is long-lived and communicates with Wuu
through line-delimited JSON on standard input and output. It first negotiates
the protocol and capabilities, then continuously receives events and calls.
You do not need to hand-write the protocol:

- On the TypeScript side, use the public `@wuu/plugin-sdk` package;
- On the Go side, import
  `github.com/blueberrycongee/wuu/packages/plugin-go`.

The protocol channel is asynchronous and duplex: while the host is invoking a
plugin capability, the plugin can call host services back through the
`RuntimeHost` passed in; after a capability call returns, background work
started by the plugin may also keep calling already-negotiated host services.
The SDK routes concurrent responses by request id. Plugins must not read or
write standard input/output directly, and must not assume requests and
responses strictly alternate. Background timers, watchers, or processes should
start after the session lifecycle begins and stop when the generation closes;
they must not run past the disable, upgrade, or unload boundary.

`initialize` is a read-only prepare phase: it may only read Storage, Settings,
and existing Session summaries. Shared Storage writes, Session create/send,
and background effects open only after the generation's package and policy are
committed, and start through `activate(host)`. SDKs that do not declare a
lifecycle version cannot enter a candidate generation.

Host capabilities are published through the Service Registry: the kernel
registers services under a stable name and version, plugins declare what they
consume with `required_services` in `initialize`, and declaring is the only way
to gain call authority. Calls all ride the `host.service.call` gateway frame
and are routed and validated by the host against the registry. The callable
kernel services are:

| Service | Purpose |
| --- | --- |
| `host.storage.get` / `set` / `delete` / `keys` / `compare-exchange` | Namespaced storage; every call must pass `scope: "user" \| "workspace"` |
| `host.settings.get` / `list` | Settings, read-only at runtime |
| `host.session.create` / `send` / `list` / `cancel` | Create, deliver to, list, and cancel plugin-owned Sessions |
| `host.data.query` | Read a versioned, read-only event snapshot for one thread |
| `registry.introspect` | Read-only introspection: which services exist, at what version, provided by which generation |
| `execution.update` | Report progress for one in-flight execution (see "Execution scope") |

Service names, parameters, and result types follow the SDK's
`KERNEL_SERVICE_NAMES`, `kernelServiceCall`, and `HostServiceContracts`.
`host.session.info`, `host.workspace.*`, and `host.diagnostics.log` are not
part of the production contract.

`host.data.query` version `1.0.0` exposes persisted session facts without giving a
plugin filesystem paths or private host objects. Declare it in `required_services` with
major version `1`, then call method `call` through `host.service.call`. Parameters are
`{ thread_id, types?, turn_id?, limit? }`; the result is
`{ version, thread_id, events }`. Each event has the stable envelope
`{ type, thread_id?, turn_id?, created_at?, data? }`. Current event types include
`turn`, `step`, `model_call`, `tool_call`, `tool_result`, `streaming_chunk`,
`context_requests`, `provider_states`, `compact_attempts`,
`barrier_tool_batch_rejected`, `tool_inventory`, `tool_records`, and `final`.
Unknown requested types return no matches rather than failing. This is a snapshot API;
`host.data.subscribe` is not yet a production-registered plugin service.

To start ordinary agent work in the background, plugins compose the
product-neutral Session services: `host.session.create` creates a Session with
explicit `owner`, `visibility`, `parent`, `fresh | fork`, workspace (`shared`
project directory or an isolated `worktree` created from the current HEAD), and
optional model-alias semantics; the create result reports the effective
`workspace_root` so the plugin can record where the Session actually runs.
`host.session.send` delivers input to an
existing Session. A send request carries a plugin-generated `request_id`, the
model input, request-only context blocks with a size cap, a stable `cause`,
and an optional `presentation: { kind: "query_bubble", text, name }`.
Plugins choose how a busy target handles the input with
`if_running: "queue" | "steer"`: `queue` starts another Turn after the active
one, while `steer` injects the input into the active Turn. The default remains
`queue`; a steered result returns `steered: true` and the active `turn_id`.
Steering does not create a second lifecycle event, and cannot carry
`context_blocks`; completion remains correlated with the active Turn's original
request.

A child Session inherits its parent's provider, model and reasoning settings
unless the create request explicitly overrides them. `fresh` still starts with
an independent context. Tools that support Named Agent conversations opt in
with `execution_scopes: ["collaboration"]`; `root` and `child` keep their
ordinary execution scopes. This opt-in exposes tools only, without importing
interactive hooks, prompts or loop drivers into Collaboration.

To return a child result to a Named Agent, send to its continuing Session with
`reply_to_turn_id` set to the parent Turn that created the child and
`presentation.related_session_id` set to that plugin-owned child. The host verifies
ownership and parentage, then durably queues the result in that Turn's original
Room and task scope. It does so even if the Agent has since moved to another
Room. Revoked task scopes return `discarded`; a stopped Agent retains a valid
result until resumed. Result delivery does not create another producer or steer
an unrelated active Turn.

`host.session.list` defaults to Sessions owned by the calling plugin's
generation. Passing `scope: "shared"` instead returns non-archived,
user-visible Session metadata across owners and workspaces for read-only discovery;
plugin-private Sessions remain excluded. `host.session.cancel` always enforces
the original ownership check, including for Sessions discovered through the
shared scope.
Lifecycle completion events include the final model output but are delivered
only to the plugin that submitted the turn, so a plugin can update its own
state or deliver results to a parent Session without reading private host
history structures.

`presentation.text` is the safe summary shown in the front-end query bubble,
not the full internal prompt. User input and plugin wake-ups share the same
standard query bubble; the host still stamps `origin=user | host | plugin` in
the persisted record and marks plugin-generated items read-only and
auditable. The provider adapter projects such input to a plain `user` role to
drive the next model turn; that protocol role does not claim a human author
and cannot override the plugin origin in product records. Plugins should not
copy the full internal prompt into the display summary, and the desktop side
must not generate bubble content from model input on its own. When the target
Session is busy, input enters the same ordinary turn queue unless the request
selects `if_running: "steer"`. If the plugin
declares the `agent.turn.lifecycle` observe capability, the host delivers
subsequent `running`, `completed`, `failed`, `interrupted`, and `discarded`
states only to the submitting plugin, echoing the original `request_id`;
terminal states also include the final model output. Cron scheduling, retry,
missed-trigger recovery, concurrent merging, and business state must live in
the plugin; the core provides no timer tick and does not interpret
`request_id` or `cause`.

Every plugin tool call carries the `turn_id` that owns it plus the unique
`execution_id` of that dispatch (progress reporting and precise cancellation
are covered under "Execution scope"). A plugin declaring the
`agent.turn.interrupted` observe capability also receives a product-neutral
turn interruption signal. The host does not build a cancellation tree on
`parent_session_id`; the plugin may forward the signal to any child turn it
tracks, or let the work detach from the current turn. Orchestration semantics —
trees, DAGs, worker pools, fan-in, retry, and recovery — all belong to the
plugin.

Process lifecycle is managed by Wuu: started on enable, terminated on disable,
upgrade, or uninstall. A plugin cannot restart itself or bypass host
supervision.

### Providing and consuming services

Plugins and the kernel compose through the same registry. A provider declares
`provided_services` in its initialize result: a stable name (for example
`search.provider`), a strict semver version, and a method list whose input and
output carry versioned schema identifiers. A consumer declares
`required_services`: name plus major version. Declarations are collected during
prepare, and calls only flow once both generations — provider and consumer —
are active.

- There is no dependency solver: when no provider can be resolved for the
  required major version, that consumer's activation is blocked with an
  explicit diagnostic;
- Calls are authenticated by the host: the `caller` on the `ServiceCall` a
  provider receives is the consumer plugin ID verified by the host and cannot
  be forged;
- After a provider upgrades, consumers re-resolve by name plus major version
  and keep working; the host sends a `service.changed` notice. When a provider
  is uninstalled or replaced, call authority is withdrawn with it, and
  in-flight calls converge to typed errors.

Now that kernel services live in the registry, the host and third parties use
the exact same provide/consume contract; no private entry point exists that
only first-party plugins can call.

### Custom authorization and process sandboxes

Security policy and process isolation are separate extension seams:

- Provide `security.authorize@1` with method `authorize` to inspect each tool's
  stable identity, arguments, classification, actor, workspace, and current
  permission mode. Return only `allow` or `deny`. This policy may further
  restrict an action; it cannot lift Wuu's workspace boundary.
- Provide `sandbox.process@1` with method `confine` to turn an exact argv plus a
  `read-only` or `workspace-write` file policy into the argv Wuu should execute.
  Return the actual enforcement level as `full` or `partial`. This seam covers
  model-facing shell and managed-process execution; plugin and MCP runtime
  admission remains governed by package permissions and grants. A provider may
  also return its own `denial_signatures` and `runner_failure_signatures`; Wuu
  uses only those per-call diagnostics when attributing a failed execution.

The Go SDK exports `AuthorizationService()` and
`ProcessSandboxProviderService()`; the TypeScript SDK exports matching service
descriptors and request/result types. When no provider exists, Wuu keeps its
built-in policy and platform sandbox. If the platform has no built-in sandbox,
confined modes require a provider and fail before execution when none is
available; only explicit unconfined mode bypasses confinement. Once a custom
provider is selected, its failure never falls back to unrestricted execution: unknown authorization
outcomes are denied, and empty or relative sandbox argv, provider errors, and
partial enforcement stop the process before it starts. Authorization providers
return policy decisions only; interactive approval is a separate host concern.

These services are intentionally narrow. Authorization does not execute a
command, and a sandbox provider does not decide whether an action is allowed.
Containers, virtual machines, and remote execution should expose a complete
execution service rather than pretending to be a same-host argv wrapper.

### Execution scope

Every `tool.execute`, `capability.invoke`, or `service.invoke` dispatch is one
execution, and the host gives it a unique `execution_id` carried on the
invocation frame. Open semantics ride the invocation frame itself, close rides its response, and
`execution.cancel` is the only mid-flight frame:

- While handling its own tool, capability, or service call, a plugin can call
  `execution.update` (or the TypeScript SDK's `reportExecutionUpdate`) to report
  progress (`execution_id` + `message` + arbitrary plugin-owned `detail`); the
  host verifies the caller owns the execution;
- When the host cancels the dispatch, it sends `execution.cancel`, and the SDK
  translates it into handler cancellation. Go handlers receive it through
  `context.Context`; TypeScript handlers receive it through the third argument's
  `AbortSignal`. The plugin maps that signal to any local cancellation primitive
  it owns;
- Cancel is fire-and-forget: the host's terminal state is decided by the invoke
  returning, never by plugin acknowledgement. A late or unauthorized update
  fails with `execution_not_found` / `service_not_authorized` and can never
  reopen a closed execution.

The host builds no task tree on `execution_id` — trees, DAGs, and worker pools
remain plugin-owned state.

### Extending the agent

Runtime plugins can register tools and hook into the agent lifecycle. The SDK
provides the following capabilities (the SDK exports are authoritative):

| Capability | Purpose | Semantics |
| --- | --- | --- |
| `agent.system_prompt.section` | Contribute a system prompt section | transform |
| `agent.pre_step` | Append a sourced, durable hidden message before a model step | transform |
| `agent.request.transform` | Read `ModelRequestViewV1` and return a validated narrow patch | transform |
| `agent.compaction` | Replace the summary-compaction result | decision; Experimental |
| `agent.turn.completed` | Observe summaries of settled successful/failed turns | observe |
| `agent.turn.lifecycle` | Receive owner-scoped lifecycle for turns this plugin submitted | observe |
| `agent.turn.interrupted` | Receive a non-blocking interruption signal for any turn; the plugin decides whether to propagate it | observe |
| `plugin.client.request` | Handle Desktop/client requests inside the plugin namespace | decision |

Tools are registered through the `tools` field of the initialize result, not
through a capability named `agent.tool.register`. Every capability belongs to
a generation and declares the `kind` it implements (observe / transform /
decision) plus `priority`. `guard` and `around` have no host implementation
and are not declarable public kinds.

Tool naming has two separate contracts. The host owns the model-visible
dispatch name and namespaces it to avoid collisions; plugins must not present
that value as UI. A registration may set `display.label` to a short, stable
user-facing name. `display.kind` classifies familiar activity, while
`display.capability` is the stable semantic identity used for presentation and
aggregation. Unknown capabilities aggregate only with the same capability (or
the same raw tool identity), rather than being merged into one generic group.

#### Returning generated artifacts

A tool returns generated images, documents, audio, or links as rich `content`
parts. Wuu keeps those parts on the durable tool result and projects them into
the conversation without turning them into assistant messages. Images appear
in result order between the process trail and the answer; document-like outputs
such as HTML and PDF collect in a generated-artifacts card at the turn end.

```ts
const report = await importArtifact(host, {
  path: "output/report.html",
  name: "report.html",
  mime_type: "text/html",
});

return {
  result: {
    content: [
      { type: "text", text: "Generated the requested assets." },
      {
        type: "image",
        mime_type: "image/png",
        name: "cover.png",
        data: pngBytes.toString("base64"),
      },
      {
        type: "file",
        mime_type: report.mime_type,
        name: report.name,
        uri: report.uri,
        artifact: {
          placement: "turn_end",
          ref: report.id,
          sha256: report.sha256,
          size_bytes: report.size,
        },
      },
    ],
  },
};
```

Declare `requireArtifactImportService()` in `required_services` and call
`importArtifact()` during the live tool execution for files that must survive
source deletion, restart, and thread forks. The host copies the file into
thread-owned storage and returns a digest-bound renderer URI. `report.id` (and
the content part's `artifact.ref`) is the opaque artifact identity; the URI is
a host capability that may be rewritten during fork or resolution and must not
be used as identity. Deleting the owning thread revokes the artifact. Inline
`data` is base64 and is intended for smaller payloads; remote
`https` resources can still be returned directly. The optional
`artifact.placement` is only a presentation hint:
`inline` or `turn_end`; when omitted, Wuu chooses by media type. Desktop
plugins can register a `tool-result` Renderer for the artifact MIME type. Wuu's
default renderer remains the fallback when that plugin is disabled or fails.
Generated HTML opens in a sandboxed preview rather than the host DOM.
`artifact.ref`, `artifact.sha256`, and `artifact.size_bytes` remain attached to
the ordered content part; Wuu does not maintain a second top-level artifact list.
Do not persist an absolute path, `file:`, or `wuu-file:` URL as an artifact
reference.

Tool results are a JSON wire contract, not arbitrary JavaScript objects. Cycles
and `BigInt` fail serialization; `undefined`, functions, and symbols are omitted,
and unknown fields are ignored by the host. Put extension metadata in declared
fields such as `meta` or the content-level `artifact` object. Capability message
views expose text and presence flags such as `has_tool_result`, not the complete
rich result returned by `tool.execute`.

The legacy `hook.invoke` has been removed; host→plugin traffic goes only
through the versioned capabilities above or tool execution, and plugin→host
traffic only through host services. When a candidate prepare fails, the old
generation keeps working; after a durable commit, a single-plugin activate
failure shows as `failed/last_error` in the inventory rather than pretending
to be active.

Plugins never receive private ThreadItems, protocol messages, the host React
tree, or arbitrary callbacks. Snapshots, inputs, and outputs are frozen public
structures; the concrete types are the SDK's `index.ts`.

`agent.pre_step` is the preferred entry point for stateful model context. The
host calls it stably by capability priority, validates `append_messages`,
marks each message hidden, read-only, and `origin=plugin`, and persists it
with the turn. A plugin can find the messages it previously appended through
the `origin_id` on its input and implement its own policy: inject once,
append a tombstone when state changes, or append every round. The host does
not keep state for the plugin and does not choose its caching strategy; this
interface only appends at the tail of history and never rewrites an existing
prefix.

`ModelRequestViewV1` exposes only the model, message summaries, tool schemas,
and a few cross-provider options — no retry objects, cache hints, media bytes,
provider-native replay state, or Go field names. The only writable field of
the current request transform is `prepend_system_messages`; using it means the
plugin chooses to change the request prefix and takes the cache impact of
doing so. New writable capabilities require new versioned fields with host
validation — not a return to arbitrary `ChatRequest` passthrough.

A complete TypeScript duplex example lives in
`examples/plugins/stateful-runtime`: `activate` uses Storage CAS from the
background, the capability handler uses Storage, and it creates and sends a
Session on explicit client request.

## Desktop plugins

Desktop plugins are trusted code running in the Wuu Renderer. They can
register global styles and replace or wrap the stable UI surfaces the host
provides, to form a unified visual system or make bounded structural changes.
They continue to use the session and navigation actions Wuu provides; they do
not rely on DOM monkey patches or private React state.

The desktop entry exports `activate(api)`. Do not bundle another copy of
React; use the host React instance from `api.react`:

```js
export async function activate(api) {
  const React = api.react;

  api.registerStyle({
    id: "visual-language",
    css: `
      :root {
        --wuu-accent: #7c5cff;
        --wuu-accent-press: #6847ed;
      }
    `,
  });

  api.registerSurface("conversation.timeline", {
    id: "timeline-frame",
    mode: "wrap",
    render(_context, fallback) {
      return React.createElement("section", { className: "my-frame" }, fallback);
    },
  });
}
```

`mode: "replace"` takes over the complete semantic boundary;
`mode: "wrap"` composes around the current result. A render failure falls back
to the current boundary only; the host always keeps Settings, plugin disable,
and default-UI recovery paths.

When the manifest declares Slots, Surfaces, or Presenters, activation must register
every declaration exactly, including target, mode, order, or priority. An undeclared
registration or a declaration left unregistered rejects the candidate generation.
Manifest View entries must reference a View registered by that same generation.

### Available API overview

- `react`, `ui`, `pluginId`, and `generation`: the host React runtime, UI Kit,
  stable plugin identity, and current atomic generation ID.
- `invokeRuntime`: calls a method owned by this plugin's active Agent runtime;
  `onHostEvent` observes host lifecycle notifications. Both remain scoped to the
  active generation.
- `registerStyle`: registers CSS; arbitrary CSS is only offered to trusted
  desktop-code plugins.
- `registerSurface`: replaces or wraps short semantic items that ship with a
  native fallback. App Shell, base Session, Composer, Settings, plugin
  management, and the region containers are protected roots.
- `registerSlot`: inserts content into stable positions in native UI
  (`sidebar.primary`, `workspace.header`, `composer.toolbar`, and more).
  Complex settings should be declared as `settingsPages` views instead.
- `registerViewType` + `registerViewPlacement`: register a persistent View
  and request its initial placement in a stable host-owned semantic region:
  `navigation`, `primary`, `auxiliary`, `inspector`, `settings`, or `overlay`.
  `priority` only decides the initial active View while a region has no user
  choice; later user switching and closing win and are persisted. The
  placement API does not expose host DOM, arbitrary parent nodes, the split
  tree, or panel dimensions. `registerViewPlacement` is the only placement
  API.
- View entries are declared by the manifest; the plugin does not draw its own
  navigation or tabs:
  - `contributes.navigation` appears in the scrollable plugin group of the
    left sidebar;
  - `contributes.workspaceTools` appears in the right-side tool picker and
    opens in a native workspace tab;
  - `contributes.settingsPages` appears in the plugin group of the Settings
    page.
  Each entry uses `{ id, view, title, description?, icon?, order? }` and must
  reference a View registered by the same plugin through
  `registerViewType`. Selection, closing, persistence, and overflow are
  managed by Wuu. Ordinary `contributes.settings` automatically gets a
  host-rendered settings page; use a custom settings View only for content a
  standard schema cannot express.
  Entry `icon` is independent of the top-level brand icon: use a public
  semantic icon name, or the same in-package artwork object as the top-level
  `icon`; when omitted, the host default icon is used, not the brand icon.
  Plugins can import `PUBLIC_ICON_NAMES`, `PublicIconName`, and
  `PluginManifestIcon` from `@wuu/plugin-sdk`. Custom artwork only accepts
  SVG, PNG, and WebP, up to 256 KiB per file; paths must stay inside the
  plugin package and cannot be symbolic links. SVG rejects scripts, event
  attributes, external references, and embedded documents. Wuu owns sizing,
  selected state, accessibility, theme switching, and load-failure fallback;
  plugins do not inject icon components through desktop modules.
- `registerInspectorSection`: registers a short summary in the host
  environment panel. Input is a frozen, versioned snapshot of public Session,
  Turn, Workspace, Git, and TODO data; host actions only allow opening a
  registered View or executing a registered Command. Optional `when(snapshot)`
  returns `false` when the current snapshot has no meaningful content, in which
  case the host omits the section title and container. The host provides an
  independent error boundary, max height, and overflow per section. Long
  lists, editors, and complex interactions must go into `primary` or
  `auxiliary` Views.
- `api.ui`: a small host-provided UI Kit: `Page`, `Panel`, `Card`, `Section`,
  `Stack`, `Row`, `Button`, `ToolbarToggle`, `TextInput`, `TextArea`,
  `Checkbox`, `EmptyState`, `LoadingState`, `ErrorState`, and
  `LiveDuration`. `Page` supports `comfortable`/`compact` density;
  `LiveDuration` renders a compact, live-updating duration from cumulative
  milliseconds and an optional run start. The host handles ARIA, loading
  animation, error visuals, responsive spacing, and overflow for the state
  components. Ordinary plugin pages should prefer these components, so they
  automatically inherit the current appearance plugin's colors, fonts,
  borders, radius, shadows, and density. Complex Views may still use
  arbitrary React; canvas, terminal, and specialized previews can keep their
  own theme boundaries. Plugins must not override UI Kit internal classes or
  re-own the host's page margins and public control rhythm. Binary toggles in
  the Composer toolbar should use `ToolbarToggle`; the host unifies
  `aria-pressed`, hit area, focus, and active state.
- `registerPresenter`: replaces a concrete product concept rather than a
  broad region. Targets include `conversation.item`,
  `conversation.process`, `conversation.tool-activity`,
  `conversation.composer`, `header.conversation`, `header.workspace`,
  `navigation.primary`, `app.status`, `content.preview`, and `settings`.
  Presenters receive a frozen, versioned, sanitized snapshot, the native
  fallback, and a host exposing only the actions available at that boundary;
  only actions present in `host.actions` can be invoked.
  `registerToolActivityPresenter` remains as the compatible Tool-specific
  entry point.
- `registerCommand`, `registerStatusItem`, `registerLocale`: commands, status
  items, and localization.
- `showConversationCard`: shows a plugin-rendered temporary interaction card
  at the bottom of a conversation. Omit `threadId` to use the current
  conversation; the returned handle can update the state or close the card.
  Cards are not written into conversation history and never enter model
  context; the host cleans them up when the plugin is disabled, unloaded, or
  the app restarts.
- `registerRenderer`: registers a content renderer by category
  (`message`, `tool-result`, `document`, `file-preview`), matching specific
  content with `match` and taking over rendering; `priority` decides ordering
  when several plugins compete for the same content.
- `registerThemeTokens`: applies public token overrides for a specific theme
  from code (the runtime counterpart of a declarative theme); only public
  semantic tokens can be modified.
- `registerCSSSnippet`: injects a CSS snippet managed under the plugin's
  scope, removed when the generation unloads.
- `registerCleanup`: runs a cleanup callback when the generation unloads
  (releasing external resources, unsubscribing, etc.).
- In View render props, `host.getSetting`, `host.getStorage`,
  `host.setStorage`: read declarative settings and read/write namespaced
  persistent storage. When a View is mounted as a Settings page, `host.settings`
  additionally provides `SettingsPageHostAPI` (`contractVersion: 1`), which
  currently can read and write the host's `runtime.modelAliases` setting.

## Slash actions and conversation cards

A `runtime_action` in `contributes.commands` matches a command with the same
ID registered by the desktop entry through `registerCommand`. The command
appears in the Composer slash menu only when the plugin is enabled and the
desktop command finished registering:

```json
{
  "contributes": {
    "commands": [{
      "id": "show-status",
      "title": "Show status",
      "description": "Inspect the current plugin status",
      "kind": "runtime_action",
      "aliases": ["status"]
    }]
  }
}
```

```js
export function activate(api) {
  let backgroundCard;

  api.registerCommand({
    id: "show-status",
    title: "Show status",
    execute(input) {
      const card = api.showConversationCard({
        threadId: input?.threadId,
        title: "Plugin status",
        state: { status: "ready" },
        render({ state, dismiss }) {
          return api.react.createElement(
            api.ui.Stack,
            { gap: "small" },
            api.react.createElement("span", null, state.status),
            api.react.createElement(api.ui.Button, { onClick: dismiss }, "Close"),
          );
        },
      });
      backgroundCard = card;
    },
  });

  api.onHostEvent((event) => {
    if (event?.kind === "notification" && event.message?.method === "turn/completed") {
      backgroundCard?.update({ status: "last turn completed" });
    }
  });
}
```

`onHostEvent` delivers app-server notifications shaped
`{ kind, workdir, message: { method, params } }`; `method` uses real method
names such as `turn/started`, `turn/queued`, `turn/completed`, and
`turn/error`. The method-name constants are the `Notification*` definitions in
`internal/appserver/protocol.go`, and the event types live in
`packages/protocol`. The `turn/completed` handler above fires when the
notification arrives; do not rely on any custom event name not listed in the
documentation.

Desktop plugins can also call `showConversationCard` directly from background
events or their own async tasks, without a prior slash command. An explicit
`threadId` places the card into another loaded conversation; omitting it uses
the current conversation. The host owns the bottom position, shell, close
button, error boundary, and plugin lifecycle; the plugin owns the card's
internal UI and state.

## Declarative CSS anchors

Common host dialogs, menus, popovers, tooltips, notices, and floating
navigation render through a protected Layer Host and expose stable
`data-wuu-component`, `data-wuu-layer`, and `data-wuu-state` attributes.
Drag previews, PDF ShadowRoot content, and plugin View panes remain
specialized rendering boundaries.

Major UI regions and controls expose public `data-wuu-component` anchors so
element-level tweaks can go through CSS snippets instead of new theme tokens:
`app-shell`, `sidebar`, `conversation-pane`, `settings-shell`,
`settings-sidebar`, `settings-content`, `settings-page`, `skills-catalog`,
`workspace-panel`, `workspace-tool-tab`,
`workspace-tool-tab-close`, `launch-view`, `turn`, `message`
(distinguishing `data-wuu-variant="user" | "agent"`), `composer`,
`composer-input`, and `composer-send` (distinguishing
`data-wuu-state="send" | "stop"`). Workspace tabs expose
`data-wuu-active="true" | "false"` plus transient
`data-wuu-state="closing" | "dragging"` states. Message controls expose one host-owned
`message-actions` group anchor, distinguished by
`data-wuu-placement="persistent" | "overlay"`. Themes may tune the rhythm with
`--wuu-message-actions-block-gap`, `--wuu-message-actions-overlay-gap`,
`--wuu-message-actions-control-gap`, `--wuu-message-actions-inline-offset`,
`--wuu-message-action-size`, and `--wuu-message-action-radius`; keyboard
order, hit targets, responsive overflow, and the two placement semantics
remain host-owned. Plugins should style the direct child buttons as one
control family instead of building private styles per copy/like/edit action.
The user-query bubble surface is separately exposed as `message-bubble` with
the `user` variant, controlled by `--wuu-message-user-*` tokens.

The plugin UI Kit exposes coarse anchors: `plugin-ui-page`, `plugin-ui-panel`,
`plugin-ui-card`, `plugin-ui-section`, `plugin-ui-stack`, `plugin-ui-row`,
`plugin-ui-button`, `plugin-ui-field`, `plugin-ui-input`,
`plugin-ui-empty-state`, `plugin-ui-loading-state`, `plugin-ui-error-state`,
and `plugin-ui-live-duration`. Appearance plugins should prefer public tokens
and use these boundaries only when a structural treatment is necessary. The
production semantic-anchor test enforces the host inventory above and the core UI Kit
anchors through `plugin-ui-empty-state`; the loading, error, and live-duration anchors
are current public UI Kit output but are not yet covered by that inventory test.

Trusted code plugins adding supplemental CSS should use only these public
attributes and tokens, not private class names or DOM structure. Relying on
private class names may work for local experiments but is not a compatibility
promise.

Raw CSS enters the host document as-is: selectors are not rewritten and there
is no ShadowRoot/iframe isolation. `:root`, `body`, wildcard, or host
selectors can affect the whole UI, so this is a high-trust Desktop capability,
not a safe style sandbox for unknown third-party code.

## Local development loop

### Scaffolding and build

```bash
wuu plugin create my-plugin      # generate a skeleton
wuu plugin validate .            # validate manifest and package structure
wuu plugin build .               # run the required package.json scripts.build
wuu plugin test .                # validate package and, when present, runtime descriptors
wuu plugin pack .                # package a distributable zip
```

`wuu plugin create NAME` defaults to `agent`; use `--type desktop` or `--type full`
and optionally `--output DIR`. Names start with a lowercase letter, contain only
lowercase letters, digits, `-`, or `_`, and are at most 64 characters.

`wuu plugin build` requires `package.json` and a non-empty `scripts.build`; use
`--package-manager` to override lockfile and `packageManager` detection.
`wuu plugin test` starts a declared runtime and checks initialization, negotiated
protocol, capability descriptors, and tool
registrations. Desktop-only packages skip runtime testing, and the command never
imports or renders the Desktop entry. Use `--timeout` to change the 30-second default.
Any check failure returns a non-zero exit code for CI.

### Development-mode hot reload

```bash
wuu plugin dev .
```

`wuu plugin dev .` authorizes **the supplied path** (`.` here) as a development directory: on
save it rebuilds, validates the candidate, and publishes an atomic
generation, keeping the active generation's lease until the switch completes;
if the build or activation fails, the previous generation stays. Directory
authorization is development-only and never transfers to ordinary plugins
installed from downloaded packages.

### Install and publish

```bash
wuu plugin pack .                    # package for distribution
wuu plugin install ./my-plugin-1.0.0.zip
wuu plugin approve my-plugin
wuu plugin dev .                     # iterate during development
```

Approval and enablement are the trust decision: the plugin runs with your user
authority. The current installer accepts a local directory or zip; direct npm and Git
sources are not available yet. The CLI stages each new package fingerprint until
`approve`, while the Desktop catalog combines approval and enablement into one
confirmation.

### Examples

[`examples/plugins/deep-ui`](../../../examples/plugins/deep-ui/) is a
self-contained, directly installable example with one `conversation.timeline`
wrapper and a declarative theme.

[`examples/plugins/developer-loop`](../../../examples/plugins/developer-loop/)
is a cross-surface acceptance example that depends only on the public SDK: it
covers the agent runtime (request transform, tool registration), host
actions, generation replacement, failure recovery, disposal, and unload, and
demonstrates the complete development loop
(install → build → test → dev → pack).

[`examples/plugins/manga-studio`](../../../examples/plugins/manga-studio/) is
a strong-style appearance stress test: it covers both the app shell and the
Settings page, exercising theme tokens, the UI Kit, semantic anchors, and
host layout ownership. It is not a reference for Wuu's default visual style.

[`examples/plugins/tool-card-skin`](../../../examples/plugins/tool-card-skin/)
demonstrates an appearance Presenter: it reads only the versioned
`ToolActivitySnapshot`, keeps the native fallback, and never parses tool
arguments or accesses loop-private state; it can be enabled and disabled
independently of Manga Studio.

## Version compatibility

The promise that plugins keep working across compatible Wuu product releases
(developers keep up without forking) is the platform's release gate and has not been
verified yet: protocol and manifest compatibility anchors exist, but a
previous/current SDK and host compatibility matrix is still
missing. Until the matrix is verified, do not promise that plugins work
across releases unconditionally; declare `minimum_wuu_version` when
publishing and re-validate after Wuu upgrades.

## Trust boundary and security core

- Plugin code runs with the user's authority; Wuu does not sandbox it. The
  Renderer never reads a plugin's absolute path. Before load, app-server
  records the installed package identity and fingerprint
  and confirms the plugin is enabled for the current workspace; the Electron
  main process loads the module through a content-addressed `wuu-plugin:` URL.
  The Content Security Policy does not enable `unsafe-eval` or arbitrary local
  scripts.
- Plugin management, safe mode, crash recovery, native windows, app-server
  lifecycle, and the user escape paths (Settings, disabling plugins, restoring
  default UI) always stay under Wuu host control and are **never** exposed to
  plugins through public interfaces.
- When started with `WUU_SAFE_MODE=1`, `wuu app-server --safe-mode`, or the
  Desktop `--safe-mode`, Wuu only discovers manifests for the plugin
  management UI; it activates no plugin runtime, tool, skill, user automation
  hook, or desktop module.
- Declarative themes can only modify public semantic tokens;
  `registerStyle` can use arbitrary CSS and is therefore only offered to
  trusted desktop-code plugins.
- Runtime processes share Wuu's user authority; installing a third-party
  runtime carries the same risk as running a third-party local command. Check
  the source before installing. Updates from the same source identity keep
  trust; a change of source identity asks for confirmation again.
