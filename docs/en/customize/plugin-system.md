# Plugin system architecture

This page explains the current product model and architectural boundaries of the wuu
plugin system: how feature plugins and appearance plugins compose, what the host and
the plugins each own, and when a developer should choose a settings Schema, a View,
the UI Kit, a Slot, a Presenter, a Surface, or an Agent capability.

This is a design document for platform maintainers and advanced plugin authors. If you
are extending Wuu for the first time, start with [Extend Wuu](index.md). For a UI
plugin, start with the [Desktop UI extension map](desktop-plugins.md).

If you only want to install and manage plugins, read [plugins](plugins.md); if you are
about to write a plugin, read [writing plugins](plugin-authoring.md). This page
explains why these APIs exist and how they work together; it does not repeat the full
API.

## North star

> Let feature plugins extend the product freely, and let appearance plugins change the
> look of those extensions uniformly; the two are orthogonal and composable, and do
> not need to know each other exists.

This means:

- a feature plugin declares capabilities, entry points, and content, and does not
  write a theme-specific version;
- an appearance plugin acts on public semantic tokens, the UI Kit, and coarse-grained
  UI boundaries, and does not chase private DOM one by one;
- the wuu host owns layout, overflow, scrolling, tabs, closing, keyboard,
  accessibility, and system safe areas;
- first-party plugins and third-party plugins use the same mechanisms; there is no
  special-casing by product ID;
- it is acceptable to give up arbitrary pixel-level control in exchange for plugin
  composition, host upgrades, and long-term compatibility.

The goal of the plugin system is to let plugins form a complete, strong, and consistent
visual language without breaking the product structure or its recovery paths.

### A higher north star: a fixed plugin kernel, a replaceable agent loop

wuu's long-term goal is not to add a plugin API around the existing agent runtime, nor
to define one particular ReAct loop as the core forever. What wuu fixes is a small,
tightly constrained **Plugin Kernel**: service discovery, scoped lifecycle, reliable
Session/Event storage, an input queue, execution leases, cancellation, permissions,
the Provider and Tool protocol gateway, generation transactions, and the host UI. The
current default agent loop is driven by the bundled `DefaultDriver`. Drivers are
currently an in-process experimental contract, not yet an ordinary plugin contribution
that can be installed via a Manifest or chosen in settings.

The Loop Driver decides how the agent runs: how input is consumed, how prompts and
context are organized, how many steps one input contains, whether tools run serially or
in parallel, when to retry, compact, reflect, stop, or continue, and whether to use a
single agent, Plan-Execute, multiple agents, or a workflow. The Kernel does not dictate
how an agent should think; it only guarantees that no Driver can break message
ordering, tool call/result pairing, permissions, persistence, cancellation, or
recovery.

```text
Wuu Plugin Kernel
  ├── Service / Event / Scope / Effect
  ├── Session/Event Store + Inbox/Outbox
  ├── Provider Gateway + Tool/Permission Gateway
  ├── Lease + Cancel + Checkpoint + Recovery
  └── Generation + Shell/UI Host
          ↓
Bundled default plugins
  ├── default Agent/Session services
  ├── default ReAct Loop Driver
  ├── default Prompt / Tools / TODO / Conversation UI
  └── Subagent / Automation / Memory / Dream
```

TODO already runs as a bundled first-party plugin: the runtime owns the tool,
argument validation, and result contract, and the Desktop module owns the Tool
Activity Presenter and the Inspector section. The host only persists ordinary
Tool call/result facts and emits versioned events and read-only snapshots based on the
public `display.capability = "todo"`; it does not maintain or restore mutable TODO
state, inject staleness reminders, or render a native TODO UI. TODO does not expand
into cross-turn auto-continuation, scheduled wake-ups, or a
long-term goal.

Provider protocol invariants, cancellation, execution leases, persistence integrity,
the final permission boundary, and crash recovery remain guaranteed by the Kernel;
advanced products such as memory, dream, Cron, and Subagent should not exist as
product branches inside the default loop. HelpMe and Goal were removed from the
product and the code entirely, not migrated into plugins; collaboration sits above these
capabilities and is not yet part of the completed first-party migration scope.

These advanced products should become first-party plugins and use the same public
capabilities as third-party plugins. A plugin owns its own business model, prompts,
tools, state, background policy, and complete UI; the plugins wuu ships are just the
first implementations of the ecosystem, not exceptions allowed to call private
interfaces. Whether the plugin platform is sufficient depends on whether these real
products can fully run, be disabled, be upgraded, and be uninstalled without the core
knowing their product names or having product branches — not on how many interfaces
were published.

This does not mean sacrificing the existing product experience. For example, the
memory settings page can keep offering an automatic overview, manual re-summarization,
viewing sources, and letting the agent modify memory through conversation. After the
migration, those interactions, prompts, and data formats belong to the memory plugin;
the host only owns the settings-page entry, View lifecycle, model and execution
safety, persistence primitives, and recovery paths. The product can stay rich while
the core does not need to know what a "memory overview" is.

### A few composable chains, not a workflow API

Cron, Memory, Dream, and Subagent look like four products, but all fit the same
closed loop:

```text
Register Tool / Prompt / View
        ↓
Observe Session / Turn events, or the plugin's own Timer
        ↓
Create, select, or reuse a Session
        ↓
Deliver model input to the Session
        ↓
Kernel reliably accepts input; the chosen Loop Driver consumes and executes it
        ↓
The plugin observes the result, updates its own state and UI, and delivers again if needed
```

Here "Session" means a conversational execution unit that can keep receiving Turns;
in the current code it is mainly called a Thread. Which final name is chosen can be
settled with the SDK version, but two semantically duplicate execution systems must
not coexist.

What the plugin platform needs is not `cron.run` or
`subagent.spawn`, but four groups of horizontal capabilities:

| Public capability | Minimal responsibility |
| --- | --- |
| Register contributions | Register model-visible Tools, prompts and request context, commands, settings, Views, and product entry points |
| Session operations | Create, deliver to, cancel, and list owned Sessions; explicitly discover shareable user-visible Sessions |
| Lifecycle events | Observe Session creation/close and Turn queuing, start, completion, failure, and cancellation |
| State and resources | Namespaced storage, controlled files/processes/workspaces, and Timers and business state machines the plugin runtime maintains itself |

Creating a Session needs a few generic attributes: `owner`, `visibility`, optional
`parent`, the context source `fresh | fork(parent)`, workspace isolation, and the
model/Tool/Profile choice. A Session with `visibility=user` appears in the normal
session list, search, and history; a Session with `visibility=plugin` does not appear
in those product entry points and can only be managed by its owning plugin, but is
still persisted, recovered, and audited by the host. Dream and Subagent can use
plugin-private Sessions; Cron can create or reuse a user-visible Session.

The Timer, Cron expression, catch-up policy for missed triggers, and run records
belong to the Cron plugin. The plugin process can keep time itself while alive and
recompute from persisted state after a restart; unless the product later explicitly
promises "wake up even when wuu is not running", the host should not first build a Cron
product or a universal scheduler. If OS-level wake-up is truly needed in the future,
it should be designed separately as a product-neutral plugin process wake-up
capability.

### Waking the main agent and "generated queries"

Subagent completion callbacks are one especially important public chain: the plugin
delivers a new input to an existing main Session, waking
the main agent. Cron can reuse the same semantics when delivering to a user-visible
Session. The product surface uniformly uses the existing query bubble: user-sent and
plugin-woken messages do not need two sets of layout, color, or interaction rhythm.
"Not recording it as a message the user personally sent" only constrains the persisted
source and editability; it does not require the frontend to draw generated queries as
a different component.

So generic delivery must distinguish three things:

- **Model input**: the actual prompt or structured context sent to the agent;
- **Display summary**: the short text in the frontend query bubble, which may differ
  from the full model input;
- **Source**: the user, the host, or a specific plugin, plus a stable cause/request id;
  this source does not change the query bubble's basic visual semantics.

The conceptual contract looks like:

```text
session.send({
  session,
  input,
  presentation: { kind: query_bubble, text, name },
  cause,
  request_id
})
```

`query_bubble` means the standard query-bubble presentation is used; it does not mean
the author is necessarily the user. The host stamps `origin=user | host | plugin` in
persisted records, and the plugin identity is bound to its generation and cannot be
forged by a request; system-generated entries are read-only and auditable by default.
To start one model turn, the provider adapter projects such input as an ordinary
`user` role: to the model it is simply the query driving the next turn, and no extra
"system wake message" protocol is needed. Here `user` is a provider protocol role, not
a claim that a human typed it personally; the trusted source in the product data is
still the plugin, and the frontend can only use the distinguished display summary, not
pass off full internal prompts as the user's own words. The host owns idempotency,
persistence, execution leases, queueing, cancellation, and recovery, and guarantees
that real user work goes first; the plugin only decides when to deliver, what the
model sees, and what the user sees. A Subagent completion notice can use the full
handoff content as model input while only showing "subtask A has updated" in the
bubble; a background plugin can use its continuation prompt as model input while only
showing a brief status like "continuing". The frontend only receives the
distinguished display summary and source
metadata; it does not build bubbles from full internal prompts. This needs no
product-specific wake chains.

This capability is also not a disguised Automation-specific interface. Any
plugin that needs to hand background results, scheduled triggers, approval resumes,
retry results, or external events back to a persistent agent needs the same "deliver
an ordinary query to a Session" chain. The Kernel therefore keeps `session.send`'s
reliable acceptance, persistence, priority, and execution lease; the chosen Loop
Driver decides when and how to consume it. Business meanings such as "scheduled
wake-up", "auto-continuation", or "subtask delivery" belong entirely to the plugin.

### Deriving public capabilities from product needs, not exposing product internals

First-party product migrations expose gaps in the plugin platform, but current
implementations must not be translated directly into low-frequency interfaces such as
`host.memory.*`, `host.automation.*`, or `host.collaboration.*`. When adding a public
capability, follow this order:

1. first migrate one product as a complete vertical slice, defining its own domain
   logic, state, and UI;
2. at the real call sites, decide whether the missing piece is logic the plugin can
   implement itself or a generic primitive the host must arbitrate;
3. only add a host capability when host ownership, cross-process safety, shared
   resources, or lifecycle integrity is involved;
4. validate the abstraction with at least one other reasonable first-party or
   third-party scenario, so the interface is not just hiding the current product name;
5. publish the narrowest versioned input, output, and lifecycle contract, and do not
   expose private ThreadItems, React state, private callbacks of a specific Loop
   Driver, or internal storage structures.

"Generic" does not mean pre-building a universal framework. Scheduler, background
task, or resource APIs that real migrations have not yet reached should not be added
to the SDK from imagination; what multiple products have already proven needed are
"create/reuse a Session", "deliver input to a Session and choose its presentation",
and "subscribe to lifecycle events". These should become product-neutral capabilities
instead of each keeping its own app-server RPC.

The current code gives several clear migration directions:

| Product | How it composes from public chains | The plugin should own | Product interfaces that should not stay in the core |
| --- | --- | --- | --- |
| Cron | Timer → create or reuse a user-visible Session → deliver a prompt; register management Tool and View | Cron expressions, tasks and run records, catch-up policy, prompts, Tool, Timer, and full UI | `automation/create`, `AutomationRunID`, and the automation branch in Turns |
| Memory | Register prompts and file Tools → read/write files at the right time; the management View can call the agent to modify files | User, workspace, and session memory formats, read/write Tools, safety policy, overview and modification prompts, audit and settings UI | `memory/overview`, `memory/chat`, the `session_memory` core Tool, and core configuration fields |
| Dream | Timer + Memory → plugin-private Session → write back through the Memory Tool after consolidation | Candidate selection, consolidation prompts, failure backoff, result state, and management UI | `sessionDreamScheduler` and the StreamRunner product-specific AfterTurn Hook |
| Subagent | Create a private child Session → fresh/fork context → deliver a task → report back to the parent Session with a generated query when done | `spawn_agent` and similar Tools, task naming, worker policy, proactive-delegation settings and request prompts, reports, and UI | The spawn/send/close/list/await/report product actions of `host.child_session.request`, plus core Ultra configuration, Turn snapshots, CLI/API, and Composer controls |
| TODO | Register a Tool with a semantic capability → ordinary Tool facts enter the log → Presenter and Inspector read the public snapshot | Tool schema, validation, result contract, Presenter, Inspector section, and styling | Core mutable state, recovery, staleness reminders, and native display |

The table describes the public capability model; the current completion status of the
five first-party migrations follows below. Fields and methods must still form
versioned contracts at real call sites. For example, the memory overview and
modification do not need a host "constrained model task": the plugin can create or
reuse a Session and send a prompt. Only when this public chain genuinely cannot
protect a host invariant should a lower-level capability be distilled further.

### The boundary between the Plugin Kernel and replaceable policy

A small Kernel is not a zero-capability host. The following mechanisms, shared by all
Drivers and product plugins, continue to be arbitrated by wuu:

- protocol correctness such as provider message ordering, tool call/result pairing,
  streaming responses, and the context window;
- append-only Session/Event persistence, Inbox/Outbox, idempotent acceptance,
  execution leases, cancellation, and recovery primitives;
- tool execution, final permission decisions, workspace boundaries, and user
  confirmation;
- Service/Event/Scope/Effect, dependency resolution, plugin install/trust,
  atomic generation replacement, and error isolation;
- native windows, system safe areas, and host-owned navigation, tabs, scrolling,
  overflow, and accessibility.

The Kernel keeps mechanisms, not policy. That messages cannot be lost, and that one
execution right cannot be held by two generations at once, belongs to the Kernel;
whether input is consumed as a steer, the next Turn, or an interrupt, and when to open
a Step, retry, or stop, belongs to the Loop Driver. That Session logs must be
rebuildable belongs to the Kernel; how a Turn, Round, Plan Node, or Worker Branch is
interpreted and displayed belongs to the Driver and its UI plugins. A Driver can only
work through the versioned Provider, Tool, Session, Permission, and Checkpoint
endpoints; it cannot bypass these gateways to operate the core database or the
Provider directly.

Everything else belongs to plugins by default. The host publishes a small set of
composable Services: model-visible Tools, system prompts and request context, request
transforms, compaction decisions, Session creation and delivery, lifecycle events,
generation-bound runtime calls and events, and namespaced settings and storage.
Public Services should compose into multiple products and multiple Loops, rather than
one endpoint per wuu feature.

Every new plugin interface should pass four questions:

1. After removing product names such as memory, automation, or collaboration, does
   this capability still feel natural?
2. Does it protect an invariant only the host can protect, or does it just save the
   plugin a few lines of domain code?
3. Can first-party and external plugins use it under the same permissions and
   lifecycle?
4. After the plugin is disabled, can the Kernel still manage, audit, and open the
   Session read-only; can the default distribution still compose a usable agent from
   bundled Drivers, instead of leaving half a product state or a product-specific loop
   branch?

If the answer fails, keep the logic inside the plugin, or look again for a lower-level,
more reusable capability boundary.

### The Loop Driver contract and recovery principles

`internal/loopdriver` currently provides an experimental version 1 contract:
`Descriptor`, `create/resume`, `run`, `checkpoint`, `cancel`, `shutdown`, the Kernel
Gateway, and a terminal outcome. Both the default multi-turn tool loop and the
single-turn no-tool Driver only request model execution through this Gateway; they do
not read the Session database or app-server private objects. Each execution saves the
Driver ID, version, contract version, and an opaque checkpoint to the Session; before
sending to the provider it also saves a final provider-neutral model-input receipt
containing the stable fact sequence number, final messages, tool surface, prompt
section metadata, and the transformed actual content. A crash leaves a `running`
checkpoint; the terminal checkpoint is written only after messages are committed.
If the Driver or version does not match, continuing fails closed, but history remains
readable.

When Drivers later become installable first-party or third-party contributions, they
still cannot get core internal objects. The full conceptual contract includes:

- `start`: start execution for a Session bound to a Driver;
- `deliver`: receive an input occurrence the Kernel has reliably persisted;
- `cancel`: respond to host cancellation and stop derived work;
- `checkpoint`: save Driver-owned state at stable boundaries;
- `resume`: recover from Session facts and versioned checkpoints;
- `shutdown`: stop accepting new work, wait for in-flight calls to converge, and
  release Effects.

The current binding point is the first execution in the Session checkpoint; the
global, workspace, or session-level selection UI is not decided in advance. A Driver
cannot be switched silently while running. When a Driver is missing, disabled, or
rejects an old checkpoint, the Session can still be opened read-only. Fork
inheritance and explicit derived selection belong to later selection semantics; the
current fast-iteration phase does not promise complex checkpoint backward
compatibility: a new version may explicitly reject an old one, but must not guess,
interpret, or silently discard it.

The generic event stream persists host facts such as user/plugin input, model output,
tool call/result, permissions, errors, cancellation, and checkpoints. A Driver can
append namespaced events to express a Plan, Research Round, Critic, Worker, or
Workflow Stage, and register its own Presenter/View; when a plugin is missing or
rendering fails, the host uses a generic read-only fallback instead of making the
whole history unopenable. Anything the model can see must be rebuildable from Session
facts.

### Borrowing a plugin-runtime model, not a TypeScript implementation

Plugin runtimes in the TypeScript ecosystem provide Context, Service, typed Event,
Fiber, Effect, and a Loader EntryTree. They demonstrate a runtime model for
assembling applications from replaceable Services, explicit dependencies, and
reclaimable plugin lifecycles; wuu borrows those generic architectural concepts, not
any specific agent product's implementation or unpublished design.

Those convenient specifics depend on TypeScript/Node: dynamic `import`, same-process
object Services, declaration merging, and browser bundle loading. wuu does not copy
those implementation details, but implements the same runtime model on the Go core +
separate runtime process + Electron shell:

| Reference concept | wuu's corresponding model |
| --- | --- |
| Context | generation-bound Plugin Scope |
| Service object | versioned RPC Service / Host endpoint |
| typed Event | versioned event catalog with namespaced payloads |
| Fiber | the unified plugin instance spanning the runtime process, Renderer modules, and contributions |
| context-scoped `effect()` | resources, subscriptions, child Sessions, and background work recorded and awaited by the Scope |
| `inject` | `requires` / `provides` in the Manifest/handshake |
| Loader entry tree | the validated Activation Plan and dependency graph |

In the Go ecosystem, the closest process-plugin boundary is a subprocess + RPC
approach like HashiCorp's `go-plugin`; the in-process `.so` mechanism of the `plugin`
standard library is not suitable as a cross-platform desktop plugin base, and
`dig`/`fx`/`wire` solve dependency injection but do not provide runtime install,
uninstall, or generations. wuu already has its own duplex plugin process protocol,
fingerprint, and atomic generation, so it does not need to rewrite the core in
TypeScript to chase the same runtime experience. The current runtime process is owned by the
Go generation and shuts down within the close deadline; the Desktop generation owns
registrations and cleanup uniformly and releases them in reverse order; the Manifest's
`requires`, `breaks`, and `conflicts` already form a simple Activation Plan.
Runtime composition is now the generation-scoped Service Registry: kernel
services, introspection, and the execution scope all ride the same
provide/consume contract. What does not exist yet is a single
cross-Go/Desktop Scope and transactional promises across external side
effects.

### Migration results of the first-party advanced features

The following records the completed vertical slices of the advanced features in the
real distribution. The acceptance criterion is that business state, prompts, tools,
background chains, and Desktop display are all owned by the plugin — not merely that
code was moved into `plugins/`:

1. **Goal has been removed.** The Goal capability and its bundled plugin were removed
   from the product rather than migrated; its auto-continuation flow is no longer a
   product feature. The remaining plugins observe `agent.turn.completed` and deliver
   through `host.session.send` on their own.
2. **Subagent has migrated to the public Session chain.** The plugin creates and
   manages private child Sessions through `host.session.create/send/list/cancel`,
   keeps task names and delivery state in plugin storage, and, after observing the
   owner-scoped Turn lifecycle, delivers a read-only query back to the parent Session.
   fresh/fork, shared directory/worktree, model aliases, ownership, cancellation, and
   final output are all product-neutral Session contracts; `host.child_session.request`
   and its `spawn/send/close/list/await/report` switch have been removed. The existing
   `agentcontrol` lease and recovery code can still serve core-internal execution, but
   is no longer a public or private call entry for the Subagent plugin. Proactive
   delegation also belongs to the Subagent plugin: it keeps the switch in namespaced
   storage, appends a sourced persistent message through `agent.pre_step` on the next
   model step after the switch state changes, and registers its own control in
   `composer.toolbar`. The core no longer stores `agent.ultra_mode`, takes Turn
   snapshots, or injects delegation policy, and no longer exposes Ultra
   app-server/CLI/IPC/native Composer state.
3. **Generic Session create/send has replaced `host.turn.submit`.** Creation and
   delivery are two separate calls; creation persists the generation-bound owner,
   `user | plugin` visibility, parent, `fresh | fork`, and an idempotent request id,
   and delivery separates the model input from the query-bubble summary while
   persisting the real plugin source, cause, and read-only attribute. The provider
   still executes under the ordinary `user` role, private Sessions do not enter the
   normal list or search, and real user queued work is prioritized over plugin
   wake-ups.
4. **HelpMe has been fully removed.** The Tool, Schema, Prompt, internal worker type,
   history rewrite/compaction special-casing, desktop copy, tests, and dead code have
   all been removed; no compatibility entry remains, and it was not renamed into
   another plugin workflow.
5. **TODO uses the semantic Tool-fact chain.** The bundled plugin owns the
   Tool schema, argument validation, result contract, Tool Activity Presenter,
   Inspector section, and styling. The core only writes ordinary Tool call/result
   facts into the Session log and projects events and public snapshots by
   `display.capability = "todo"`; core mutable state, recovery, staleness reminders,
   a native TODO section, and tool-name special-casing are absent,
   with no compatibility adapter kept.
6. **Automation has migrated to the public Session chain.** The first-party plugin owns
   the Cron expression, Timer, catch-up, tasks and run records, prompts, the `cron`
   Tool, and the full desktop View; on trigger it only calls `host.session.create/send`
   and converges run state through the generic Turn lifecycle. The core's Automation
   RPC, Manager, scheduler, Turn special-casing, native pages, IPC, and Tool display
   special-casing have been removed; generation shutdown first stops the plugin's
   background Timers.
7. **Memory has fully migrated to the public plugin chain.** The first-party plugin owns
   the user notebook, workspace `project_memory`, the file layout of session
   `summary/checkpoint/notes`, safety filtering, the `memory_*`/`session_memory`
   Tools, system prompts, overview/management private Sessions, and the full desktop
   View. The core no longer reads the user's `MEMORY.md`, no longer auto-injects
   session memory, and no longer provides Memory RPC, native pages, IPC, core
   Tool/capability, enable/disable configuration, or a dedicated model role. The host
   only hands the resolved `workspace_state_dir` to the plugin as generic
   initialization context, and continues to own ordinary Session/Turn persistence. The
   legacy `memory` instruction-discovery configuration only migrates at the load
   boundary into the product-neutral `instructions` configuration. The ordinary core
   file Tools no longer allow user Memory or the entire `WUU_HOME` either; the
   identity notebooks of named agents belong to the collaboration domain that is not
   yet migrated, are open only within that agent's explicit file scope, and must not
   be expanded into `host.memory.*` from this.
8. **Dream has migrated to the public Session chain.** The first-party plugin observes
   standard Turn-completion events, keeps candidates, the Timer, the interval, failure
   backoff, and run state in plugin storage, and creates a fork private Session through
   `host.session.create/send`; the prompt and the settings View are also plugin-owned,
   and the Session writes back through the `session_memory` Tool provided by the Memory
   plugin. The core `sessionDreamScheduler`, Dream state/locks, the AfterTurn Hook, the
   configuration fields, and the native settings have been removed.
9. **Desktop lifecycle is proven by real first-party modules.** The bundled
   `desktop.js` of Subagent, Automation, Memory, Dream, and TODO runs directly on
   the `WorkbenchController`/`PluginHost` product path; Views, Slots, navigation,
   settings entries, Locale, and Style activate and replace atomically with the
   generation, and all are withdrawn when disabled. Tests do not use a fake registry
   in place of module execution.

### Refactoring order and completion criteria

The refactoring should reuse existing reliable execution capability, replace the
public boundary first, then delete the product-specific entry points:

1. Define generic Session create/send/lifecycle on top of the existing Turn
   submission; fill in owner, visibility, parent, fresh/fork, workspace, source,
   display summary, and request id, and unify user-work-first and idempotency rules.
2. Subagent already creates and manages private child Sessions with the public API;
   `host.child_session.request` has been removed, and the task prompt, state, desktop
   status bar, and parent-Session callback are plugin-owned. The host no longer
   recognizes the `spawn_agent` Tool name, parses `<subagent_notification>`, generates
   a dedicated Tool item, or maintains a native subtask panel; the plugin's generated
   callback only relies on the generic `display_content/origin/cause/read_only` query
   metadata and does not change the public contract.
3. HelpMe has been removed end to end; TODO runs as a bundled plugin, and the
   core Tool, state, recovery, and native display branches were removed.
4. Cron, Memory, and Dream have each completed the vertical slices of "plugin Timer →
   user-visible Session", "Prompt + Tool + private Session + View", and "Timer +
   Memory Tool + plugin-private Session"; none of them has a product-specific host
   service.
5. Every migration must verify that after disabling, upgrading, or uninstalling the
   plugin, it no longer wakes, and no UI, Prompt, Tool, subscription, or background
   generation remains; the core deletes old protocols, dead code, and tests that only
   existed for the old boundary.
6. The Go runtime and Desktop registrations have each converged on the generation
   owner, and the Manifest supports simple dependencies and conflicts; runtime
   composition has converged on the generation-scoped Service Registry — kernel
   services, introspection, and the execution scope all ride the same
   `host.service.call` entry point, and no parallel fixed host-service table is
   being kept.
7. The existing execution loop has been wrapped as the Experimental v1 `DefaultDriver`
   with unchanged product behavior; Sessions persist the Driver identity, checkpoint,
   and final model-input receipt, and recovery only proceeds from stable boundaries.

The acceptance of the plugin chain is not that interfaces exist, but that when the
core has no product-specific execution or mutable state branches for Cron, Memory,
Dream, Subagent, or TODO, these five first-party plugins can still keep the
existing experience solely through public contracts; external plugins can compose
equivalent capabilities under the same permissions and lifecycle.

## One plugin package, four kinds of contributions

The same plugin package can contain only one kind of contribution, or several kinds
at once.

| Layer | Contribution | Where it runs | Typical use |
| --- | --- | --- | --- |
| Declaration layer | Themes, settings, entry points, permissions, and metadata | Manifest + host | Themes, toggles, left-side entries, right-side tools, settings pages |
| Agent layer | Tools, versioned capabilities, request transforms, system prompts, compaction policy | Separate runtime process | Search, policy control, memory, context handling |
| Workbench layer | Views, commands, state items, Presenters, Slots, Surfaces | Electron Renderer | Collaboration pages, review tools, message rendering, complex settings |
| Appearance layer | Theme tokens, UI Kit styles, public semantic anchors | Renderer CSS | Full reskinning, density, fonts, materials, and control visuals |

The four layers can combine in one plugin package. For example, a future
collaboration plugin could register Agent tools, a left-side entry, workspace Views,
settings items, and commands at the same time; a Manga appearance plugin provides only
a theme and styles. Both install and layer naturally.

## Responsibilities of the host, feature plugins, and appearance plugins

| Party | Owns | Should not do |
| --- | --- | --- |
| wuu host | Window safe areas, left/right sidebars, title bar, tabs, panels, scrolling, overflow, persistence, accessibility, and recovery paths | Write product special-casing for a plugin |
| Feature plugin | Business capabilities, content, commands, settings definitions, entry intent, and plugin-internal state | Redraw host navigation, arrange system tabs itself, depend on the current theme |
| Appearance plugin | Colors, fonts, borders, corner radii, shadows, materials, limited density, and semantic control states | Move the traffic-light safe area, rewrite private DOM, adapt per feature-plugin ID |

The core rule: **whoever owns the layout controls the spacing rhythm.** Host
containers decide page margins, tab sizes, system safe distances, and large-region
arrangement; plugins organize functionality inside their assigned content area. After
a feature plugin uses the UI Kit, its public UI automatically inherits the current
appearance.

## Declarative first; code is an escape hatch opened level by level

Developers should use the narrowest interface that can express the need. The lower
the interface, the more freedom but also the more compatibility and trust cost.

| Need | Prefer | Where the user sees it | Controls the host keeps |
| --- | --- | --- | --- |
| Toggle, text, number, enum settings | `contributes.settings` | The plugin group in the settings page | Form layout, storage, validation, and theming |
| Left-side main feature entry | `contributes.navigation` + View | The scrollable plugin group in the left sidebar | Selection state, base ordering, and scrolling |
| Right-side contextual tools | `contributes.workspaceTools` + View | The right-side tool picker and native workspace tabs | Opening, closing, tabs, panel width, and persistence |
| Complex plugin settings | `contributes.settingsPages` + View | The plugin group in the settings page | Settings navigation, page frame, and scrolling |
| Own workspace page | `registerViewType` | The host-managed workspace area | View lifecycle, placement, and persistence |
| Ordinary plugin UI | `api.ui` | Inside a View or custom settings page | Common component rhythm and theme compatibility |
| Append a little content to an existing area | `registerSlot` | Fixed host slots | Native content and slot arrangement |
| Change product concepts such as messages, Composer, navigation | `registerPresenter` | The corresponding semantic component | Versioned snapshots, allowed actions, and failure fallbacks |
| Wrap or replace one complete large area | `registerSurface` | Surfaces such as Sidebar, Settings, Catalog | Layout outside the boundary, fault isolation, and recovery entry |
| Strongly stylized decoration | Theme tokens, `registerStyle` when necessary | The whole desktop | Public semantic contract and structural safety |

Do not replace an entire settings page because you need one button, and do not invent
an anchor for every layer of container because you need global reskinning. Interface
granularity should match the real visual or functional responsibility boundary.

## Feature entry points are managed by the host

Plugins declare placement intent, not absolute coordinates:

- `navigation` suits high-frequency main features such as collaboration, project
  management, or session features;
- `workspaceTools` suits contextual tools such as files, review, terminal, and
  browser;
- `settingsPages` suits complex settings that a standard Schema cannot express;
- plugins that do not need persistent UI can contribute only Tools, Hooks, commands,
  or background capabilities.

The plugin provides a title, icon, ordering preference, and View; wuu owns entry
selection, tabs, closing, recovery, and panel lifecycle. A plugin cannot replace the
whole navigation or tab bar just to gain "one more entry".

The current left-side plugin entries live in a scrollable group; the right side opens
with the host tool picker and native workspace tabs. Full user pinning, unpinning,
reordering, and a unified "more" overflow menu are later capabilities; see [current
boundaries](#current-boundaries-and-unfinished-capabilities).

## How settings pages compose with appearance plugins

Standard settings use `contributes.settings`. The host generates navigation and
controls and stores values in the plugin's namespace. Complex settings can register a
custom View and enter the same settings navigation through
`contributes.settingsPages`.

Whether the settings come from wuu, plugin A, or plugin B, appearance plugins act on
all of them through the same settings semantics and UI Kit:

1. the feature plugin only describes fields or renders content that uses the UI Kit;
2. wuu provides the settings-page frame, page width, scrolling, and common controls;
3. the appearance plugin provides colors, fonts, borders, corner radii, and
   materials;
4. the three parties do not need to know each other's IDs and do not need paired
   releases.

If a custom settings page is fully self-drawn, it still inherits the public base
tokens, but only areas using the UI Kit get full common-component compatibility.
Canvases, terminals, webviews, and dedicated previews are explicit theming
boundaries.

## UI Kit coverage

`api.ui` currently provides `Page`, `Panel`, `Card`, `Section`, `Stack`, `Row`,
`Button`, `ToolbarToggle`, `TextInput`, `TextArea`, `Checkbox`, `EmptyState`,
`LoadingState`, `ErrorState`, `LiveDuration`, and `ComposerDrawer`.
`ComposerDrawer` provides the shared accessory shell for `composer.above`, including
header sizing, scrolling, and Escape dismissal. Pass controlled `expanded` and
`onExpandedChange`, an accessible `toggleLabel`, `summary`, and optional `icon`,
`actions`, `notice`, and `children`. `notice` remains visible when collapsed.
The `tone` can be `default`, `muted`, or `warning`; plugins own empty-state visibility
and feature behavior.
`Page` unifies density and responsive spacing, and the three state components unify
ARIA, focus, error, and loading behavior. Its purposes are threefold:

- converge the common rhythm of pages, cards, rows, and controls;
- let feature plugins automatically inherit any compatible appearance plugin;
- keep appearance plugins from tracking every feature plugin's private classes.

Complex Views can still use React and their own components, as long as the UI Kit is
used in the public areas suited to unification and the theme boundary of self-drawn
areas is explicit. The UI Kit component set will grow driven by real plugin needs; it
will not pre-copy a full component library.

## Appearance contract: tokens, components, and semantic anchors

Appearance capability has three layers, in priority order:

1. **Theme tokens**: semantic colors, fonts, spacing density, corner radii, borders,
   shadows, motion, content width, and syntax colors. The registry lives in
   `config/desktop-theme-contract.json` and generates Manifest, SDK, and desktop
   validation code.
2. **UI Kit**: the host's common components used by feature plugins. When an
   appearance plugin changes tokens, these components change automatically.
3. **Coarse semantic anchors**: `data-wuu-component`, `data-wuu-layer`,
   `data-wuu-state`, and variant attributes, used for structured decoration that
   tokens cannot express, such as message bubbles, panels, tab selection states, or
   overlays.

Anchors are divided by visual responsibility boundary and usually stop at pages,
panels, cards, rows, and composite controls. An appearance plugin can change a tab's
overall material and selection indicator, but cannot split a tab apart from its close
button and rearrange them; it can decorate title-bar buttons uniformly, but cannot
shrink the macOS traffic-light safe area.

Trusted desktop code can register arbitrary CSS through `registerStyle`, but private
classes, DOM hierarchies, and incidental structures are not part of the compatibility
contract. Manga Studio exists to stress-test the public contract, not to make the host
add special-casing for Manga. Tool Card Skin only consumes the versioned
`ToolActivitySnapshot` and native fallbacks, and registers a replacement Presenter for
`command.bash`; it does not parse arguments or read any Driver or product-plugin
private state. The two examples can be enabled at the same time, separately verifying
the independent composition of Theme/Token/snippet and Presenter.

## Agent capabilities and the closed loop

Agent plugins run in a separate process managed by wuu and register capabilities
through a versioned protocol. The current public capabilities cover:

- registering model-visible Tools;
- contributing system-prompt fragments through `agent.system_prompt.section`;
- appending sourced, persistable hidden messages before model steps through
  `agent.pre_step`;
- reading a versioned request view and returning a validated narrow patch through
  `agent.request.transform`;
- replacing summarization/compaction results through the Experimental
  `agent.compaction`;
- observing Turns through `agent.turn.completed` and the owner-scoped
  `agent.turn.lifecycle`;
- handling Desktop/client requests inside the plugin namespace through
  `plugin.client.request`.

Experimental capabilities already include in-process `LoopDriver` injection, but
Manifest registration or user selection is not yet open. Drivers do not get private
Go `Session`, database, or App Server objects; they only run through the Kernel
Gateway, versioned input, and Checkpoint. Ordinary feature plugins should not gain
capabilities by modifying the default Loop's private callbacks.

Tools are registered through the `tools` of the initialize result, not as
capabilities. Each capability can only declare the `observe`, `transform`, or
`decision` kind the host has implemented, and they compose by stable priority;
`guard` and `around` are not current public contracts. A plugin package can also
declare configuration Hooks through the manifest, but they go through the Hook event
and command/model execution chain, not the runtime capability system. When a tool or
capability errors, the host propagates, isolates, or falls back per the public error
policy; it cannot maintain a surface of success by swallowing exceptions.

Whenever an LLM-visible capability is added, both ends must be closed: implement the
registration and execution path, and let the prompts or public documentation tell the
model when to use it; while keeping provider protocol invariants such as message
ordering and tool call/result pairing.

## Generation: the atomic unit of install, activate, replace, and uninstall

All executable contributions of a plugin belong to one generation:

1. wuu reads the Manifest, records the source identity, and checks it is enabled
   for the current workspace;
2. the candidate generation is built and registered externally, not yet visible to
   users;
3. after validation succeeds, the old generation is replaced in one step;
4. if validation or activation fails, the old generation keeps running;
5. when disabled, upgraded, uninstalled, or hot-reloaded during development, the old
   generation's Views, styles, commands, event subscriptions, and runtime are
   released together.

The target Plugin Scope registers every Service, Event subscription, Timer, child
Session, background task, Renderer contribution, and child Scope inside a generation
as an Effect. A candidate only becomes visible to users after its dependencies are
satisfied and it enters ACTIVE; on shutdown, it stops accepting new work first, then
waits for in-flight Effects to converge and releases them in reverse order. The Agent
Service and Desktop View contributed by one plugin must come from the same Activation
Plan; they cannot separately depend on incidental load order.

React components can subscribe to host events after the generation activates, and the
subscriptions are cleaned up when the component unmounts or the generation is
replaced. Duplicate diagnostics of the same kind are deduplicated per generation;
after the same generation successfully re-activates, stale diagnostics are cleared.

Desktop Slots, Presenters, and Surfaces all have local error boundaries. When one
contribution fails to render, wuu isolates the current boundary and keeps the native
fallback instead of white-screening the whole Renderer. Plugin management, settings,
disabling, and restoring the default UI always remain reachable.

## Service Registry: the single composition primitive

Runtime composition is no longer a fixed host-mediated table: plugins and the
kernel publish capabilities as Services under a stable name and version, other
plugins consume them by name and major version, and every call is routed and
validated by the host through the `host.service.call` gateway against the
registry. The registry is the single composition primitive; contracts,
manifests, and introspection interfaces are products of it, and every
registration is published and withdrawn with its generation — no parallel
lifecycle is created.

- **The kernel is the first registrant, not a special case**: Storage,
  Settings, Session, the execution scope (`execution.update`), and registry
  introspection (`registry.introspect`) are all registered as kernel services;
  the host and third parties use the same provide/consume contract;
- **Declaring is the only source of call authority**: a consumer gains
  authority only by declaring `required_services`; there is no dependency
  solver — an unsatisfied requirement fails that consumer's activation with an
  explicit diagnostic instead of silent degradation;
- **Calls are validated on the wire**: unregistered or unauthorized services
  cannot be reached, and the `caller` a provider receives is authenticated by
  the host;
- **Introspectable**: `registry.introspect` lets programs query the current
  registry (services, versions, providers, generations); self-evolution tooling
  and diagnostics share this entry point.

The execution scope is the registry's first kernel Service consumer: every
tool/capability dispatch gets a unique `execution_id`, `execution.update`
reports progress, and `execution.cancel` cancels exactly one dispatch; the host
builds no task tree. Cross-plugin composition speaks the same vocabulary:
plugin B consumes plugin A's versioned Service, keeps working by re-resolving
after A upgrades, and loses call authority when A is unloaded — in-flight calls
converge to typed errors.

## Multi-plugin composition and conflicts

Normal composition needs no arbitration: multiple Slots append in stable order,
multiple `wrap` Presenters/Surfaces wrap in sequence, and feature plugins and
appearance plugins compose orthogonally through semantic contracts.

Mutual-exclusion conflicts only arise when several plugins all request `replace` of
the same semantic boundary. wuu picks one winner using a stable default order and
shows the candidates in plugin settings so the user can choose explicitly. The
conflict choice is persisted by the host; a plugin cannot override another plugin on
its own.

A typical acceptance composition is:

- plugin A provides standard settings;
- plugin B provides a right-side tool;
- a collaboration plugin provides a left-side entry, its own tab, and complex pages;
- an appearance plugin provides a complete theme.

The four do not know each other, but the public UI of A, B, and the collaboration
plugin all adopt the current theme; disabling the appearance plugin leaves the
functionality and layout complete, and disabling any feature plugin cleanly unloads
the corresponding entry, subscriptions, and state.

## First-party and third-party plugins are isomorphic

Subagent, Automation, Memory, Dream, and TODO already run through the same
generation, capability, and public host contracts as third-party plugins. Each owns
its prompts, Tools, state, background policy, and Desktop contributions; the
product-specific host execution seams and the native product shells have been
removed, which is the current vertical proof of "first-party/third-party isomorphism".
Collaboration is not yet part of the current refactoring scope and interfaces should
not be pre-built for it.

The test is: if a first-party feature can only be implemented by modifying the private
loop or private UI, first identify which generic capability is missing; only extend
the host when real needs prove the public contracts insufficient. wuu's own plugins
are the ecosystem's first users, not exceptions that bypass the rules.

## Trust and recovery boundary

Plugins are locally installed application code, with no sandbox promise:

- declarative themes and settings do not execute plugin code;
- Agent runtime processes share the same user permissions as wuu;
- desktop code runs in the Renderer and can register arbitrary CSS;
- installing or enabling a source is the trust decision; updates from the same
  source identity keep trust, and a source-identity change asks for confirmation;
- the runtime only receives a documented whitelist of environment variables; it does
  not directly inherit the whole host environment;
- the Renderer does not read plugin absolute paths; Electron loads plugins through
  content-addressed `wuu-plugin:` URLs;
- CSP does not enable `unsafe-eval`.

Plugin management, safe mode, native windows, app-server lifecycle, crash recovery,
and the default UI are always controlled by wuu. Only install and enable executable
plugins from trusted sources.

## Current boundaries and unfinished capabilities

The following must not be written as completed compatibility promises:

- there is no previous/current SDK and host compatibility matrix yet;
  plugin releases should declare `minimum_wuu_version`, and re-validate after wuu
  upgrades;
- left-side entries have scrolling and right-side tabs have overflow, but user
  pinning, unpinning, reordering, and a unified "more" menu are not done;
- right-side tools and settings pages have declarative entries, but a generic bottom
  panel contribution has not formed a stable public contract;
- the UI Kit is still small; structured forms, multiline inputs, and lists should
  keep converging driven by real plugin needs;
- some Presenters' `replace` snapshots and actions are not yet enough to rebuild the
  full native semantics losslessly; prefer `wrap`;
- canvases, terminals, webviews, PDF ShadowRoots, and dedicated previews remain
  explicit theme boundaries;
- a Marketplace, remote auto-update, ranking, dependency resolution, and signed
  distribution are not part of the current local-first platform;
- Subagent, Automation, user/workspace/session Memory, Dream, and TODO have
  completed their vertical migrations, and the product-specific host execution seams
  were removed;
- HelpMe and Goal have been removed from the code and the product; TODO has no core
  Tool, state, recovery, and native display were also removed;
- the Go runtime and Desktop contributions are now reclaimed uniformly per
  generation, and a simple Activation Plan, Default Driver, checkpoints, and
  model-input receipts are implemented; the Service Registry
  (kernel services + introspection + execution scope) is live as the runtime
  composition primitive; the unified cross-Go/Desktop Plugin Scope and Driver
  Manifest/selection UI are not done.

New capabilities should be driven by real plugin cases: first decide whether the
responsibility belongs to the host, a feature plugin, or an appearance plugin, then
choose the narrowest public contract.

## Development and acceptance principles

When developing a plugin, verify real compositions:

1. a feature plugin and an appearance plugin enabled at the same time;
2. standard settings, custom settings, left-side entries, and right-side tools are
   all discoverable;
3. disabling the appearance plugin leaves layout and functionality unchanged;
4. disabling or updating the feature plugin cleanly unloads its contributions;
5. a failed generation does not replace the currently working version;
6. a strongly styled theme does not break the traffic-light safe area, tabs,
   scrolling, closing, or accessibility;
7. the implementation mainly relies on tokens, the UI Kit, and a few semantic
   anchors.

The examples in the repository each play a different role:

- [`examples/plugins/developer-loop`](../../../examples/plugins/developer-loop/): the
  development loop for the public SDK, runtime, Views, entries, settings, storage, and
  generations;
- [`examples/plugins/manga-studio`](../../../examples/plugins/manga-studio/): a
  strong-style appearance stress test, verifying that native and plugin UIs can be
  reskinned uniformly;
- [`examples/plugins/tool-card-skin`](../../../examples/plugins/tool-card-skin/): a
  Tool card Presenter that only depends on the public Tool snapshot and fallbacks, and
  does not understand any specific Loop's private state;
- [`examples/plugins/deep-ui`](../../../examples/plugins/deep-ui/): a minimal example
  of a Surface wrapper and declarative theming.

The final judgment is one sentence: **features can extend freely, appearance can
reskin as a whole, multiple plugins compose naturally, and the host UI stays stable.**

