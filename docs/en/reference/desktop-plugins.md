# Desktop plugin API

Desktop modules export `activate(api)` and use the generation-scoped `PluginGenerationApi`. Obtain types from the matching Wuu checkout's `packages/plugin-sdk`; use the host's `api.react` and `api.ui` at runtime.

| Contract | Guide |
| --- | --- |
| Slots, surfaces, presenters, views, cards, and status sources | [Desktop UI plugins](../customize/desktop-plugins.md) |
| Composer inline-completion providers | [Composer inline-completion providers](../customize/desktop-plugins.md#composer-inline-completions) |
| Runtime model service | [One-shot model requests](../customize/plugin-authoring.md#one-shot-model-requests) |
| A complete local package and build commands | [Desktop quickstart](../customize/desktop-plugin-quickstart.md) |
| Draft actions, workspace pages, and runtime calls | [Desktop recipes](../customize/plugin-recipes.md) |
| Manifest, runtime lifecycle, storage, and distribution | [Plugin authoring](../customize/plugin-authoring.md) |
| Public theme tokens and semantic anchors | [Theme surface matrix](../customize/theme-surface-matrix.md) |

Registration methods return disposable handles and belong to the active generation. Actions advertised by a presentation host are specific to that render, not a global command permission. Private renderer modules, DOM nesting, and React internals are outside the public API.

`api.signal` remains live after activation and aborts when that generation is disposed. Async startup is bounded; see [desktop generation lifecycle](../customize/plugin-system.md#desktop-generations-and-composition) for timeout and supersession behavior.

## Stable presentation and graph-bound business scopes

A presentation-only module can export `activatePresentation(api)` using
`ApplicationPresentationAPI`. The desktop and standalone preview hosts prefer
this entry point over `activate`. It exposes the host React runtime, manifest
identity, contribution discovery, a presentation-lifetime signal, and shell,
slot and contribution registration. It deliberately exposes no application
writer, runtime invocation, or business-provider registration.

Healthy, unchanged presentation code keeps its registrations and React component identity
when the authoritative service graph changes. Render props receive a fresh
`application` scope after the selected business provider becomes active; its identity
stays stable for that business lifetime. Include
that object in effect dependencies; do not capture it in module-level state.
Authority is pinned when the scope is created, before its first method call.
Previously captured actions, subscriptions and handles remain bound to the
retired provider and never retarget a successor. Retired observer registrations
are empty; they cannot attach a listener to either the old or new provider. During the writer gap the host
may display already-read synchronous presentation values, but cannot execute old
business operations. Preparation/activation failure still exposes recovery; it
does not choose another provider or keep the previous writer alive.

A code change, disable, missing required dependency, or connection replacement
still retires presentation. An intentional selected-surface change can replace
its rendered tree. Modules exporting only
`activate(api)` keep their existing conservative graph-owned lifecycle. Opt in
only when registrations are independent of business authority; use a separate
normal module for runtime work.

Environment snapshots may expose `actionBusyPending` to distinguish a pending
occupancy read from a settled busy/unavailable result. Retained Git status belongs
to its exact context and worktree root. A different target with unknown status
may retain an existing Git control as inert and neutral, never the previous
workspace's branch name. Pending status alone does not add Git chrome when no
Git control was already visible; a confirmed repository can add it once.

## Application-owned workbench and contribution discovery

`api.contributions` is the public `UIContributionRegistryV1`: stable immutable
snapshots of commands, views, placements, renderers, presenters, conversation
cards, theme tokens, locale entries and composer providers, with change
subscriptions. It also exposes activation identity, mounted-generation retention
and render diagnostics. A provider may compose these contributions without
importing `PluginHost` or accessing a desktop singleton. Retaining a mounted
activation delays cleanup until its React subtree has released its effects;
retention does not authorize new calls to a retired activation.

The selected `ui.application` implementation supplies `application.workbench`
(`ApplicationWorkbenchV1`). It owns view/layout state, opening and closing views,
captured view/presentation APIs and rendering of layers, registered content and
embedded views. The shipped implementation preserves durable layout version 2;
application switch checkpoints also retain session-only view instances. Native
views and theme effects are disposed with their owning application, not merely
removed from a registry. Loading an application must not treat its not-yet-loaded
inventory as an authoritative empty inventory.

A slot declaration creates a composition boundary, **not product behavior**.
Declaring `workspace.tools`, `workspace.view`, `conversation.cards`,
`conversation.message.actions`, `header.workspace`, `header.conversation`,
`content.preview` or `workbench.layers` no longer invokes implicit host adapters.
The shipped shell/conversation/preview providers explicitly register their
implementations using `registerUIContribution`. A replacement surface can render
`api.contributions` itself or register its own wrappers; a replacement shell can
render `application.workbench.renderLayers({ settingsOnly: false })` in an
explicit contribution. Unloading the owner removes its contribution and effects.
No hidden default workbench is resurrected.

Captured view APIs are generation-fenced before and after asynchronous work.
They never retarget stale actions to a successor with the same fingerprint.
An already-performed external side effect is not rolled back by that fence;
callers must preserve uncertain outcomes rather than blindly retry them.
