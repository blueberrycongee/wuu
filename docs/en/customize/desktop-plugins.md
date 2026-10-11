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

Use the smallest boundary that fits. A toolbar button does not need a replacement composer; a long dashboard does not belong in a toolbar. The host owns view placement, navigation, window lifecycle, and recovery controls.

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

A presenter receives `contractVersion`, a target, an optional match key, a public `snapshot`, a scoped `host`, and three composition choices:

- `next()` continues to the next presenter. `next({ snapshot })` supplies a replacement snapshot to downstream presenters only.
- `fallback` is equivalent to `next()` with the current snapshot, preserving existing wrappers.
- `original` is the host's native rendering, bypassing the remaining presenters. A changed snapshot does not alter this native rendering.

Wrappers stay outside replacements, with higher-priority wrappers outermost. The user's preferred replacement runs first; a replacement may render the boundary itself or explicitly delegate with `next()` / `fallback`. Downstream presenters mount only when their output is included. Do not mutate the shared snapshot; pass a new value to `next` when adapting it, and preserve the target's snapshot contract.

A failing contribution falls back to the next presenter with its unmodified incoming snapshot. The rest of the plugin stays loaded; reactivation allows the failed contribution to run again.

Current built-in targets are `conversation.item`, `conversation.process`, `conversation.tool-activity`, `conversation.composer`, `header.conversation`, `header.workspace`, `navigation.primary`, `app.status`, `content.preview`, and `settings`. A permissive TypeScript string type does not mean the host renders arbitrary new targets.

Use `host.actions` to discover the actions advertised by that render and `host.invoke(action, input)` to call them. The host checks that the generation remains active and validates the action's input and current state. An advertised action can still reject because the composer is read-only or submission is disabled.

For example, `conversation.composer.set-draft` accepts a string; attachment removal accepts an attachment ID. Submit and stop take no input. Do not assume an SDK constant is an implemented action: the current composer does not advertise `conversation.composer.set-submission-mode`.

Tool activity presenters customize execution summaries and controls. Rich result bodies have separate host-owned placement: `inline` output precedes the answer and `turn_end` output follows it. Use a `tool-result` renderer for those bodies instead of rendering `structuredResult.content` again inside the activity presenter.

## Interactive content renderers

Content renderers also receive `host.actions` and `host.invoke`. When a completed conversation resource advertises `conversation.composer.submit`, it accepts `{ text, context? }`: up to 8,000 text characters and optional JSON context of up to 16,384 characters. Invoke it synchronously from a real button click or its native form submission, including keyboard activation. Form submission must match that click’s exact form and submit button; a standalone `requestSubmit()` does not authorize an action. The host consumes that interaction once, binds the source conversation and artifact, and submits through normal turn admission and permissions. The action rejects during a running turn, after source replacement or unmounting, or when its plugin is disabled. It leaves the composer draft and attachments intact. `metadata.interaction` reports `running`, `readOnly`, and an optional `disabledReason`; action availability must still be checked at invocation. This input contract is specific to content renderers; the composer presenter submits its existing draft without input.

To preview streamed tool arguments, a `tool-result` registration may declare `toolInput: { capability }` with a literal MIME `match`. Only the current winning renderer for that MIME receives previews. `content.preview` marks transient output; `metadata.toolInput` contains cumulative/coalesced `argumentsText`, `turnId`, `status`, and optional `error`. Parse only complete bounded units, keep the last valid preview, and do not execute actions or persist speculative state. The first matching final resource retains the preview's part identity.

Persist interaction state through ordinary plugin storage, scoped by the host's conversation and artifact identities. Stored view data is local to the current Wuu store; it is not cross-device synchronization. Restoration must not submit actions or overwrite edits made while a read was pending.

## Views, cards, and status

A view is a registered component placed in `navigation`, `primary`, `auxiliary`, `inspector`, `settings`, or `overlay`. `registerViewPlacement` requests an initial placement. Manifest entries in `contributes.navigation`, `workspaceTools`, or `settingsPages` give users a host-owned way to open the view; each must refer to a view registered by that plugin.

`persistence=durable` preserves view layout state across restoration; it does not persist arbitrary component state. Use namespaced storage for data that must survive unmounting. View props include a host API for storage, settings, commands, and view navigation.

Conversations and primary plugin views use sidebar navigation, including API-opened views without a declared navigation entry. Their headers omit `tabs`, `activeTabId`, and tab actions. Primary plugin headers provide `canNavigateBack` and `header.navigate-back`. The host adds its own close control only when closing removes the page from the sidebar: API-opened views and additional instances. A declared navigation entry's page is left through Back or the sidebar. Compact workspace headers also provide back navigation, beside a close control for the open tab. These remain optional fields and actions in contract version 1: inspect the snapshot and `host.actions` instead of rebuilding a top tab strip from saved views. Auxiliary panels and plugin content can still have their own tabs.

Conversation cards are transient interaction, not saved history or durable pages. A card handle can update its state or dismiss it. For composer status, return structured items from `getSnapshot(context)` and notify through `subscribe`; keep snapshots stable until data changes. Wuu renders the row and handles overflow and the optional `open-session` action, rather than accepting arbitrary React content for each status item.

## Appearance and cleanup

Use `api.react` and `api.ui` so controls follow the host's theme, spacing, and typography. Keep UI size separate from code size. Prefer declarative theme tokens for appearance-only changes; the [surface matrix](theme-surface-matrix.md) lists the public contract.

Standard browser APIs such as selection and resize observers are available. Public `data-wuu-component`, `data-wuu-slot`, and `data-wuu-surface` anchors are preferable to private class names or React internals. They do not make surrounding DOM structure stable. Register cleanup for listeners, observers, timers, and other resources you create outside React's own effect cleanup.

Registrations belong to a plugin generation and are removed when it unloads. That lifecycle is not a security sandbox: trusted renderer code and CSS can still misbehave. Keep the default-UI recovery path available, handle asynchronous action errors, and test disable/reload as well as initial rendering. Package trust and update behavior are described in [plugin management](plugins.md).
