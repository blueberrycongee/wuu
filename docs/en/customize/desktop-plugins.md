# Desktop UI plugins

A desktop plugin is a trusted JavaScript module loaded into Wuu's renderer. It uses the host's React instance and UI Kit to add controls, pages, and presentation at defined boundaries. Start with the [quickstart](desktop-plugin-quickstart.md) for a working package.

## Choose an extension point

| Goal | API |
| --- | --- |
| Add a small control at an existing location | `registerSlot` |
| Change the presentation of a product concept | `registerPresenter` |
| Wrap or replace a message or timeline region | `registerSurface` |
| Add a page, editor, or dashboard | `registerViewType`, with placement or a manifest entry |
| Add a compact inspector section | `registerInspectorSection` |
| Render a particular content type | `registerRenderer` |
| Show temporary interaction beneath a conversation | `showConversationCard` |
| Publish compact composer status | `registerComposerStatusSource` |
| Change appearance | Declarative themes, theme tokens, or styles |

Use the smallest boundary that fits. A toolbar button does not need a replacement composer; a long dashboard does not belong in a toolbar. The host owns view placement, tabs, navigation, window lifecycle, and recovery controls.

## Slots and surfaces

Slots add content alongside native UI. Multiple registrations compose by `order`.

| Slot | Location |
| --- | --- |
| `sidebar.primary`, `sidebar.footer` | Sidebar content and footer |
| `workspace.header`, `conversation.header` | Corresponding headers |
| `conversation.message.before`, `conversation.message.after` | Around each message |
| `composer.above`, `composer.toolbar` | Above the composer or in its toolbar |

The two surfaces are `conversation.timeline` and `conversation.message`. A surface receives a context and fallback, with `mode=wrap` or `replace`. The timeline boundary is a turn's timeline/orchestration group, not ownership of the entire conversation's scroll container.

Manifest declarations under `contributes.slots`, `surfaces`, or `presenters` describe contributions; the desktop module still registers their rendering. When a declaration list is supplied, registration IDs, targets, modes, and ordering must match it, and declared contributions must be registered during activation.

## Presenters and actions

A presenter receives `contractVersion`, a target, an optional match key, a public snapshot, a scoped `host`, and a `fallback`. A wrapper keeps the current fallback in its output. A replacement renders the whole boundary. The host resolves competing replacements and composes wrappers; render failures fall back at the affected boundary.

Current built-in targets are `conversation.item`, `conversation.process`, `conversation.tool-activity`, `conversation.composer`, `header.conversation`, `header.workspace`, `navigation.primary`, `app.status`, `content.preview`, and `settings`. A permissive TypeScript string type does not mean the host renders arbitrary new targets.

Use `host.actions` to discover the actions advertised by that render and `host.invoke(action, input)` to call them. The host checks that the generation remains active and validates the action's input and current state. An advertised action can still reject because the composer is read-only or submission is disabled.

For example, `conversation.composer.set-draft` accepts a string; attachment removal accepts an attachment ID. Submit and stop take no input. Do not assume an SDK constant is an implemented action: the current composer does not advertise `conversation.composer.set-submission-mode`.

Tool activity presenters customize execution summaries and controls. Rich result bodies have separate host-owned placement: `inline` output precedes the answer and `turn_end` output follows it. Use a `tool-result` renderer for those bodies instead of rendering `structuredResult.content` again inside the activity presenter.

## Views, cards, and status

A view is a registered component placed in `navigation`, `primary`, `auxiliary`, `inspector`, `settings`, or `overlay`. `registerViewPlacement` requests an initial placement. Manifest entries in `contributes.navigation`, `workspaceTools`, or `settingsPages` give users a host-owned way to open the view; each must refer to a view registered by that plugin.

`persistence=durable` preserves view layout state across restoration; it does not persist arbitrary component state. Use namespaced storage for data that must survive unmounting. View props include a host API for storage, settings, commands, and view navigation.

Conversation cards are transient interaction, not saved history or durable pages. A card handle can update its state or dismiss it. For composer status, return structured items from `getSnapshot(context)` and notify through `subscribe`; keep snapshots stable until data changes. Wuu renders the row and handles overflow and the optional `open-session` action, rather than accepting arbitrary React content for each status item.

## Appearance and cleanup

Use `api.react` and `api.ui` so controls follow the host's theme, spacing, and typography. Keep UI size separate from code size. Prefer declarative theme tokens for appearance-only changes; the [surface matrix](theme-surface-matrix.md) lists the public contract.

Standard browser APIs such as selection and resize observers are available. Public `data-wuu-component`, `data-wuu-slot`, and `data-wuu-surface` anchors are preferable to private class names or React internals. They do not make surrounding DOM structure stable. Register cleanup for listeners, observers, timers, and other resources you create outside React's own effect cleanup.

Registrations belong to a plugin generation and are removed when it unloads. That lifecycle is not a security sandbox: trusted renderer code and CSS can still misbehave. Keep the default-UI recovery path available, handle asynchronous action errors, and test disable/reload as well as initial rendering. Package trust and update behavior are described in [plugin management](plugins.md).
