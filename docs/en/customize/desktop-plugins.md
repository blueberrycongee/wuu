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
| Supply an inline draft suffix | `registerComposerInlineCompletionProvider` |
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

## Views, cards, and status

A view is a registered component placed in `navigation`, `primary`, `auxiliary`, `inspector`, `settings`, or `overlay`. `registerViewPlacement` requests an initial placement. Manifest entries in `contributes.navigation`, `workspaceTools`, or `settingsPages` give users a host-owned way to open the view; each must refer to a view registered by that plugin.

`persistence=durable` preserves view layout state across restoration; it does not persist arbitrary component state. Use namespaced storage for data that must survive unmounting. View props include a host API for storage, settings, commands, and view navigation.

Conversations and primary plugin views use sidebar navigation, including API-opened views without a declared navigation entry. Their headers omit `tabs`, `activeTabId`, and tab actions. Primary plugin headers provide `canNavigateBack` and `header.navigate-back`. The host adds its own close control only when closing removes the page from the sidebar: API-opened views and additional instances. A declared navigation entry's page is left through Back or the sidebar. Compact workspace headers also provide back navigation, beside a close control for the open tab. These remain optional fields and actions in contract version 1: inspect the snapshot and `host.actions` instead of rebuilding a top tab strip from saved views. Auxiliary panels and plugin content can still have their own tabs.

Conversation cards are transient interaction, not saved history or durable pages. A card handle can update its state or dismiss it. For composer status, return structured items from `getSnapshot(context)` and notify through `subscribe`; keep snapshots stable until data changes. Wuu renders the row and handles overflow and the optional `open-session` action, rather than accepting arbitrary React content for each status item.

## Composer inline completions

Register a provider during `activate(api)` with `registerComposerInlineCompletionProvider({ id, order, provideCompletion })`. Its callback receives a read-only `ComposerInlineCompletionContext` and an `AbortSignal`, and returns `{ text: "literal suffix" }` or `null`, directly or through a promise. Return `null` when your provider has no suggestion. The host appends accepted text; the callback does not replace the draft or own the textarea.

The version-1 context contains `kind`, `composerId`, `revision`, optional `threadId` and `sideThreadId`, `draftText`, `selectionStart`, `selectionEnd`, and `running`. `kind` is `main` for main and split-pane inputs, or `side` for side inputs, including a new side draft without a `sideThreadId`. Selection offsets use JavaScript UTF-16 indexing. `composerId` is opaque and scoped to the mounted input and its conversation/workspace context; a revision is not a global conversation version. For a side composer, `threadId` identifies the owning main conversation and `sideThreadId` identifies its side conversation when one exists.

Workspace-aware providers also receive optional `workspaceId` (registered project ID), `workspaceRoot` (the host event-routing root), and `attachmentCount`, `queuedCount`, `pendingCount`. Counts contain no attachment bytes or queued draft text. Missing fields mean unknown on an older host; do not interpret them as zero. These values participate in request invalidation, including in split panes. Route runtime requests with the verified workspace ID rather than assuming the active workspace or globally unique thread IDs.

Connection-aware event consumers should clear pending ephemeral work when `api.onHostEvent` receives `kind: "connection-reset"`. Remote notification events marked `replayed: true` arrived before the ordered restoration barrier completed and must not trigger new live work. Ordinary desktop/live events omit this marker. A reset is not a core failure and does not discard the user's draft.

`running` describes composer presentation activity. Main and split inputs can become idle when the final answer is ready, before runtime cleanup settles. A false value does not prove successful completion. A completion-only policy must observe turn lifecycle events and define its behavior after activation or reload.

For recent displayed conversation context, call `api.readVisibleHistory({ kind, threadId, sideThreadId, workspaceRoot, workspaceId, beforeTurnId })`. Main history uses `beforeTurnId` as an exclusive boundary and returns up to 20 earlier complete visible turns. Side history uses its own `sideThreadId` and returns up to 20 complete exchanges. The response contains only displayed user/final-answer text and image/file counts, not media bytes, names, paths, tool output, or reasoning. Match the result to a successful live completion before predicting; restoring history must not trigger work. The separate `host.session.history.read` runtime service exposes physical audit records that can include withdrawn or hidden messages and must not be treated as a visible transcript.

Providers run sequentially by ascending `order`, then plugin ID, then registration ID. The first nonempty result wins; no result or a provider error lets the next provider try. The host owns ghost-text rendering and verifies that the request is still current before accepting it. Providers own context retrieval, prompts, debouncing, and request budgets. No completion provider or model-calling policy is installed by this API.

The host calls providers when an eligible input observation changes. The registration returns a handle with `dispose()` and `refresh(scope?)`. After an asynchronous cache or setting update, call `handle.refresh({ composerId, revision })` using the original request's identity, or omit the scope to recheck all current eligible inputs. The host recalls the ordered provider chain, cancelling its previous request. Refresh does not change the input revision, accept text, override Escape dismissal, or make an old scope apply to a newer draft. Disposed and replaced-generation handles are inert. Bursts coalesce into at most one refresh batch per 100 ms per registration; plugins still own inference/debounce budgets and must not unconditionally refresh from `provideCompletion`.

Return `null` promptly while independent work is pending, and refresh when its cache is ready. A local snippet provider can refresh after workspace indexing; a translation provider can refresh after its glossary changes. These use the same boundary as a background next-message predictor, without requiring a model call or installing any completion policy. A pending provider delays later providers; there is no automatic provider timeout.

### Suggestion interactions

Results may include an opaque provider-owned `id`: `{ id: "snippet-42", text: "literal suffix" }`. Supply `onDidInteract(event)` on the registration to observe only its own suggestions. Every rendered presentation receives a host-generated `interactionId`; callbacks preserve that ID, the optional `suggestionId`, and the source's `kind`, `composerId`, `revision`, and optional thread IDs. No later draft text, message body, or attachments are sent to this callback.

- `shown`: the eligible ghost preview was committed to the input's DOM. This does not prove the user saw it or that the whole suffix fit in the viewport.
- `accepted`: Tab inserted the whole suffix into the editable draft. This does not send it.
- `dismissed`: Escape explicitly dismissed a visible suggestion. Cancelling a request before a suggestion exists, losing focus, or changing context is not a rejection event.
- `dispatched`: the composer called its submission callback and it returned without throwing or returning `false`. `mode` is the requested `send`, `queue`, or `steer` action; `edited` compares the dispatched text with the draft immediately after that suggestion's acceptance.

`dispatched` is a UI dispatch boundary, not a backend acknowledgement, persisted-message ID, final queue routing, or delivery guarantee. Asynchronous submission may still fail and restore the draft. A synchronous rejection keeps attribution for a retry; a successful dispatch consumes it once. A cleared or programmatically replaced draft, conversation switch, unmount, or retired provider drops attribution. Ordinary edits preserve it, including editing away the inserted text; `edited` does not prove any suggested characters remain. If several suffixes are accepted into one draft, each retains its own identity and comparison baseline. Only whole-suffix acceptance is supported.

Snippet and translation plugins can distinguish insertion from later dispatch and edits through this contract, without observing another provider's interactions or unrelated message text. The host neither stores these events nor sends analytics. Callbacks stop on disposal/reload; callback errors are diagnosed and do not block editing. Event handling, retention, ranking, and network use belong to the plugin.

The native input requests suggestions only while focused and writable, with a collapsed caret at the end. Empty drafts are eligible. It excludes IME composition, slash-command drafts, open menus or dialogs, and hidden collapsed prompt content. Slash-command handling keeps Tab priority. An unmodified, nonrepeated Tab accepts a current suggestion; without one, ordinary focus navigation remains available. Escape dismisses the current suggestion or pending request. Acceptance changes the draft without submitting it, preserving native undo. Multiline suffixes are supported; CRLF is normalized to LF.

The ghost preview is clipped to the native input viewport and does not grow the textarea. Acceptance inserts the entire suffix, including text beyond the preview.

Editing, selection changes, composition, focus/context or running-state changes, provider disposal, disable, reload, and unmount invalidate pending results. Honor the abort signal and release timers or subscriptions you create. Disposing the registration removes its contribution; generation teardown also disposes it. Ignoring cancellation cannot make a stale result change the draft, but it can leave your own work running.

A provider receives unsent draft text. Choose deliberately what leaves the machine, and explain model/network use to users. When delegating through `api.invokeRuntime`, pass the same signal in its options and propagate the runtime execution's signal to downstream work. [One-shot model requests](plugin-authoring.md#one-shot-model-requests) use the configured provider and may incur charges; cancellation cannot undo completed requests or guarantee that an accepted provider request is free.

For older desktop hosts, check `typeof api.registerComposerInlineCompletionProvider === "function"` before registering. Feature-detect `typeof handle.refresh === "function"` for refresh and interaction support on hosts that already expose registration. The context's `contractVersion: 1` describes this callback shape; it is separate from runtime service versions and Wuu's product CalVer. Exact public types are in the [TypeScript SDK](../../../packages/plugin-sdk/src/index.ts).

## Appearance and cleanup

Use `api.react` and `api.ui` so controls follow the host's theme, spacing, and typography. Keep UI size separate from code size. Prefer declarative theme tokens for appearance-only changes; the [surface matrix](theme-surface-matrix.md) lists the public contract.

Standard browser APIs such as selection and resize observers are available. Public `data-wuu-component`, `data-wuu-slot`, and `data-wuu-surface` anchors are preferable to private class names or React internals. They do not make surrounding DOM structure stable. Register cleanup for listeners, observers, timers, and other resources you create outside React's own effect cleanup.

Registrations belong to a plugin generation and are removed when it unloads. That lifecycle is not a security sandbox: trusted renderer code and CSS can still misbehave. Keep the default-UI recovery path available, handle asynchronous action errors, and test disable/reload as well as initial rendering. Package trust and update behavior are described in [plugin management](plugins.md).
