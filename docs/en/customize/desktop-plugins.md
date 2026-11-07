# Desktop UI plugins

A desktop plugin is a trusted JavaScript module loaded into Wuu's renderer. It uses the host's React instance and UI Kit to add controls, pages, and presentation at defined boundaries. Start with the [quickstart](desktop-plugin-quickstart.md) for a working package.

Plugins that use `application` must declare `{ "name": "ui.application", "major_version": 1, "required": true }` in `desktop.required_services`. This keeps their lifecycle ordered with the application provider during workspace changes.

## Choose an extension point

| Goal | API |
| --- | --- |
| Add a small control at a declared location | `registerUIContribution` |
| Change the presentation of a product concept | `registerPresenter` |
| Wrap or replace a declared message or timeline region | `registerUIContribution` |
| Add a page, editor, or dashboard | `registerViewType`, with placement or a manifest entry |
| Add a compact inspector section | `registerUIContribution` targeting `wuu.default-shell/inspector.sections` |
| Render a particular content type | `registerRenderer` |
| Show temporary interaction beneath a conversation | `showConversationCard` |
| Publish compact composer status | `registerComposerStatusSource` |
| Supply an inline draft suffix | `registerComposerInlineCompletionProvider` |
| Change appearance | Declarative themes, theme tokens, or styles |

Use the smallest boundary that fits. A toolbar button does not need a replacement composer; a long dashboard does not belong in a toolbar. The host owns view placement, navigation, window lifecycle, and recovery controls.

## Slots and surfaces

Slot owners declare and render regions; contributions target a named owner. Multiple content registrations compose by `order`.

When a primary plugin page is active, its contextual sidebar also renders that plugin's `sidebar.primary` contributions below its navigation entries. Its `context` contains `ownerPluginId`, `viewTypeId` and `viewContext` (the same immutable context object passed to the page). Use them to show page-specific summaries and keep separate page instances isolated. These fields are absent in the conversation sidebar.

| Slot | Location |
| --- | --- |
| `wuu.default-navigation/sidebar.primary`, `wuu.default-navigation/sidebar.footer` | Sidebar content and footer |
| `wuu.default-shell/workspace.header`, `wuu.default-shell/conversation.header` | Corresponding headers |
| `wuu.default-conversation/welcome`, `welcome.greeting`, `welcome.mascot`, `welcome.usage` | Empty-conversation welcome and its independent parts |
| `wuu.default-conversation/conversation.turn.after`, `wuu.default-preview/conversation.turn.after` | After each presented turn; reported timing and usage |
| `wuu.default-conversation/conversation.tool-activity.after`, `wuu.default-preview/conversation.tool-activity.after` | After an individual tool activity inside its disclosed process details |
| `wuu.default-conversation/conversation.message.before`, `wuu.default-conversation/conversation.message.after` | Around each message |
| `wuu.default-composer/composer.above`, `wuu.default-composer/composer.toolbar` | Above the composer or in its toolbar |
| `wuu.default-preview/workspace.rail` | Compact controls and meters in the expanded right tool rail; fixed to one toolbar control's width |

Use an explicitly qualified slot target to compose a region. The default conversation provider declares `wuu.default-conversation/conversation.turn` and `wuu.default-conversation/conversation.message`; the preview provider declares its own message slots under `wuu.default-preview`. The turn boundary includes that turn’s messages and process group, not the entire scroll container.

```js
api.registerUIContribution({
  id: "timeline-frame",
  target: "wuu.default-conversation/conversation.turn",
  mode: "wrap",
  render: ({ children }) => api.react.createElement("section", null, children),
});
```

Omit `mode` (or use `content`) to supply content instead of the caller’s fallback. Single-content slots reject conflicting registrations during preparation, preserving the active generation. Wrappers surround the composed content in ascending `order`, then plugin ID and contribution ID; later wrappers are outermost. A failed wrapper reports a render diagnostic and preserves its children. Removing the slot owner removes the region; unloading a contribution removes only that contribution. Provider cleanup waits for mounted views to release their effects. Graph-selected service slots retain their exact provider binding and reject wrappers. The local SDK preview uses the same content/wrapper ordering and conflict rules; wrapper failures remain visible in its browser console.

### New conversation welcome

The default conversation provider renders `wuu.default-conversation/welcome` only while its conversation is empty. The composer and message flow remain outside this slot. A content contribution can replace the whole welcome, or replace `wuu.default-conversation/welcome.greeting`, `welcome.mascot`, or `welcome.usage` independently. Return `null` to hide a part. Each slot has one content provider; the normal conflict, wrapper, and unload rules above apply.

The welcome context is `ConversationWelcomeContextV1`: the React nodes `greeting`, `mascot`, and `usage`. Each node includes its own slot and default fallback, so a replacement layout can arrange or omit them without importing private components. Render each node at most once. The default usage summary is absent when there is no usage. The default greeting includes the workspace switcher where applicable. Replacing it also replaces that control. The default mascot's idle interaction with the usage card requires both defaults inside the default welcome container.

For example, this layout moves the usage summary above the greeting and hides the mascot:

```js
export function activatePresentation(api) {
  const h = api.react.createElement;
  api.registerUIContribution({
    id: "welcome-layout",
    target: "wuu.default-conversation/welcome",
    render: ({ context }) => h("section", { style: { padding: 32, display: "grid", gap: 24 } },
      context.usage, context.greeting),
  });
}
```

Use `wuu.default-conversation/welcome.greeting` instead to customize only the greeting, or target `welcome.mascot` / `welcome.usage` with the same owner prefix to replace those parts. A replacement layout owns its responsive styling and accessibility. With a TypeScript plugin, cast the welcome context to `ConversationWelcomeContextV1` from `@wuu/plugin-sdk/ui`.

For additive metrics, annotations, or contextual controls, prefer `conversation.turn.after` and `conversation.tool-activity.after` over wrapping the native content. Both accept multiple contributions in order, in bubble and IM styles. Turn addons stay inside the native turn’s measured bounds. A complete replacement of `conversation.turn` owns its contents and must render any desired slots itself. Adding, removing, or failing an additive contribution leaves the native turn and tool details mounted, preserving their disclosure state and focus. Replacing or wrapping the content changes its React ancestry and does not offer that state-preservation guarantee.

The turn slot receives `ConversationTurnSlotContextV1`: `threadId` (optional), `turnId`, and a frozen `snapshot` with status, reported timestamps, duration, token usage, and usage model. Unknown metrics remain absent; do not treat them as zero. The snapshot is display data, not permission to invoke an action. Tool slots share `ConversationToolActivitySlotContextV1`, including optional `threadId`/`turnId`, `itemId`, and the existing immutable `ToolActivitySnapshot`. A preview turn without a durable thread identity must not borrow the currently selected thread. Tool contributions are visible in disclosed tool details, not the compact process summary; single command/MCP details use the same slots as grouped activities. An owning provider must be enabled and render the slot; contributions do not restore a disabled provider.

[Conversation metrics](../../../examples/plugins/conversation-metrics/desktop.js) is a standalone plugin that reports turn usage and adds a local tool-result action without private imports or a replacement application. It uses `activatePresentation` so a business-service rebind does not recreate its presentation registrations.

This replaces `registerSlot`, `registerSurface`, `contributes.slots`, and `contributes.surfaces`, which have been removed. Change `render(context)` to `render({ context, locale, translate })`, use a qualified target, and remove the obsolete manifest fields. `registerUIContribution` needs no parallel manifest declaration. Manifest `contributes.presenters` declarations still require matching IDs, targets, modes, priorities, and registrations during activation.

The Inspector uses the same public slot contract. `wuu.default-shell/inspector.sections` accepts multiple contributions and passes `context.snapshot` as `InspectorSnapshotV1`. Render your section and heading, return `null` when there is no content, and use `order` for placement. Application actions are scoped to the contribution generation. The former `registerInspectorSection`, `when`, and Inspector-specific host API have been removed; move the condition into the component and use the public application or captured plugin APIs for actions. The bundled TODO module is a complete example. The stock information panel is currently feature-disabled; declaring this slot alone does not make it visible. An alternative shell must declare and render its own slot; contributions target that explicit owner.

A contribution receives `locale` and `translate(key, values?)` alongside `context`, `application`, and `slots`. Register translations with `registerLocale`. Lookup tries the exact locale, its language, `en-US`, then `en`; missing entries return the key. The packed SDK preview follows the same contract.

Slot owners may pass a React component through `<Slot layout={Layout} ... />`. It receives the ordered `items` (`key`, `pluginId`, `id`, `title`, `node`) and `context`; render each selected `node` using its `key`. A contribution's optional `title` defaults to its ID. Layouts can group or filter content without creating another registry. Wrappers surround the result; empty slots use `fallback`. Graph-selected service slots render their selected provider directly and ignore custom layouts.

The default composer uses this layout to place its first two contributions inline and the remainder in a titled overflow menu. Use compact controls inline and allow menu content to wrap. Composer context includes `threadId`, `variant`, `mainConversation`, `running`, `readOnly`, `hasDraft`, and `attachmentCount`. Goal uses `registerUIContribution`; remove its old `contributes.slots` entry. Monitor runs without desktop UI. `application.composerPresentation.getContributions` and its presentation-adapter types have been removed; custom composers render their declared slots instead.

The `workspace.rail` slot belongs to the default preview provider and appears while the right tool rail is expanded. Contributions share one toolbar control's width and stack vertically. Use compact readouts or controls, and expose their full meaning through accessible labels. A contribution can observe the active tab through `application` and read its thread, turn, and running state through `application.composerPresentation`.

[Token Speed](../../../examples/plugins/token-speed/README.md) is an independently installable example using this slot. It estimates observable output over a two-second window from streamed text, reasoning, and tool-call arguments; hidden output and model-specific tokenization are not measured. The fixed-width readout uses compact units on a separate line and eases number changes over 320 ms, respecting reduced motion. It shows a dash before output arrives or after the turn ends, and returns to zero when an active stream pauses. Its accessible label and hover description explain the estimate.

From a checkout containing this slot, run `wuu plugin install ./examples/plugins/token-speed`, or `wuu plugin dev ./examples/plugins/token-speed` for hot updates. Disabling the plugin removes its readout.

## File explorers and editors

A plugin can replace just the file tree with a `registerUIContribution` targeting
`wuu.default-preview/workspace.file-tree`, without selecting a new `ui.preview`
provider. This single-content slot supports wrappers and the normal conflict and
unload rules. The default preview keeps its tabs, file viewer, docking and resize
controls. Disabling the contribution restores the default tree.

Its `WorkspaceFileTreeContextV1` (exported from `@wuu/plugin-sdk`) contains `root`,
`selectedFilePath` and `visible`. A missing root means no workspace; `visible`
becomes false while the region is hidden. Stop view-owned subscriptions while
hidden and on unmount. Capture `root` with each operation; never substitute the
currently selected workspace when an asynchronous operation finishes. Open a row
through `application.previewContent.openFile({ root, path })`.

`application.previewContent` supplies directory listing, bounded file reading,
and optional saving and observation. Check both the corresponding capability and
method: older or alternative V1 application providers may omit them.

```ts
const files = application.previewContent;
const target = { root, path: "notes.md" };
const original = await files.readFile(target);
if (files.capabilities.saveFile && files.saveFile && !original.binary && !original.truncated) {
  const result = await files.saveFile({
    ...target, text: editedText,
    base_mtime_ms: original.mtime_ms, base_sha256: original.sha256,
  });
  // A conflict leaves the file untouched and returns its current contents.
  // Keep the user's draft and offer an explicit reload or merge.
  if (result.status === "conflict") showConflict(result.file);
}
```

Saving uses the existing host path rules and atomic text-file replacement. It
updates existing complete text files; it does not create, rename or delete files.
Version checks detect edits observed before the write; this is not a filesystem
transaction against arbitrary external writers. Cancellation or a transport error
after admission can leave a completed write: re-read before retrying.

```ts
const controller = new AbortController();
if (files.capabilities.watch && files.watch) {
  const stop = await files.watch(target, change => {
    if (change.type === "error") showError(change.message);
    else scheduleReload(target); // Coalesce reads and retain unsaved editor text.
  }, { signal: controller.signal });
  await loadInitialState(target); // Subscribe first, then read.
  // On unmount: stop(), or controller.abort().
}
```

The supplied local desktop provider uses native filesystem events, sharing
underlying directory handles across subscriptions and coalescing bursts over
40 ms. It asynchronously revalidates paths after events; idle native subscriptions
do not scan metadata. If native observation is unavailable, shared directory
timers fall back to metadata checks every two seconds. It reports invalidations
for an existing file or a directory's direct entries, including deletion and
recreation. It does not recursively observe child file contents, guarantee every
transient change, or provide an ordered change journal. Events retain the requested root and path. A terminal
`error` ends that subscription. Unsubscribe, abort, plugin/provider retirement,
renderer reload and window closure all release observation. Remote web/mobile
transports currently advertise no observation capability.

For a knowledge base, a plugin may own its editor, link graph, recursive index,
file mutations and persistence in a runtime service, called from its desktop
module with `invokeService` or `invokeRuntime`; see [replaceable providers](replaceable-providers.md).
Trusted runtime plugins can use their execution environment's filesystem APIs.
That filesystem is not automatically the same as a remote conversation's
filesystem: the plugin must define its workspace routing and access policy.
The host does not need a new registry for each domain. Plugins can declare their
own versioned services and child UI slots for other extensions to build on.

## Presenters and actions

A presenter receives `contractVersion`, a target, an optional match key, a public `snapshot`, a scoped `host`, and three composition choices:

- `next()` continues to the next presenter. `next({ snapshot })` supplies a replacement snapshot to downstream presenters only.
- `fallback` is equivalent to `next()` with the current snapshot, preserving existing wrappers.
- `original` is the host's native rendering, bypassing the remaining presenters. A changed snapshot does not alter this native rendering.

Wrappers stay outside replacements, with higher-priority wrappers outermost. The user's preferred replacement runs first; a replacement may render the boundary itself or explicitly delegate with `next()` / `fallback`. Downstream presenters mount only when their output is included. Do not mutate the shared snapshot; pass a new value to `next` when adapting it, and preserve the target's snapshot contract.

A failing contribution falls back to the next presenter with its unmodified incoming snapshot. The rest of the plugin stays loaded; reactivation allows the failed contribution to run again.

The remaining presenter targets are `header.conversation`, `header.workspace`, `app.status`, `content.preview`, and `settings`. A permissive TypeScript string type does not mean the host renders arbitrary new targets.

Use `host.actions` to discover the actions advertised by that render and `host.invoke(action, input)` to call them. The host checks that the generation remains active and validates the action's input and current state. An advertised action can still reject because the composer is read-only or submission is disabled.

Composer and navigation customization uses public UI contributions. Add controls at `wuu.default-composer/composer.above` or `composer.toolbar`, and navigation content at `wuu.default-navigation/sidebar.primary` or `sidebar.footer`. Read current state through `application.getSnapshot()` and subscribe with `application.subscribe`; change a draft with `application.invoke("composer.set-draft", { text, ownerTabId })`. Replace the complete surface by selecting a `ui.composer` or `ui.navigation` provider. The retired `conversation.composer` and `navigation.primary` presenter targets are rejected at registration; remove their manifest presenter declarations.

Messages and process groups use qualified `conversation.message` and `conversation.process` slots under `wuu.default-conversation` or `wuu.default-preview`. The rendering component supplies the immutable `context.snapshot` (`ConversationItemSnapshotV1` or `ConversationProcessSnapshotV1`) alongside `threadId`, `turnId`, and `itemId` or `itemIds`. Message snapshots include live streamed text; process snapshots report the group's actual `active` and `streaming` states. Message actions receive the same semantic item shape. Contributions no longer look up another copy of the conversation by ID. Use `mode: "wrap"` and return `children` for item kinds you do not handle. Edit a message through `application.invoke("history.begin-edit", { threadId, turnId, itemId })`; the application validates current state and generation lifetime. Migrate `conversation.item` and `conversation.process` presenter registrations to these slots and remove their manifest presenter declarations. Process groups containing compaction notices retain the native rendering because the process payload does not describe those notices.

Tool activity uses `registerUIContribution` with `mode: "wrap"` and the qualified targets `wuu.default-conversation/conversation.tool-activity` and `wuu.default-preview/conversation.tool-activity`. The leaf supplies `context.threadId`, `turnId`, `itemId`, and an immutable `context.snapshot: ToolActivitySnapshot`, including partial arguments and structured results. Select the calls you handle using `snapshot.capability ?? snapshot.toolName`; return `children` for other calls. Multiple plugins may wrap the same activity in contribution order. A failing wrapper preserves its children, and disabling its generation removes it. The old `registerToolActivityPresenter` API and `conversation.tool-activity` presenter target have been removed; migrate their registrations and remove the corresponding manifest presenter declarations. See [Tool Card Skin](../../../examples/plugins/tool-card-skin/desktop.js).

Rich result bodies have separate host-owned placement: `inline` output precedes the answer and `turn_end` output follows it. Use a `tool-result` renderer for those bodies instead of rendering `structuredResult.content` again inside the activity contribution.

## Interactive content renderers

A desktop module can opt into scoped content rendering by declaring `desktop.content_renderers` (or `contentRenderers`) in `plugin.json`, for example `[{"category":"tool-result","match":"application/vnd.example+json"}]`. Declarations accept exact lowercase MIME types without parameters or wildcards. The module exports `activateContent(api)` and receives the SDK's `ContentRendererActivationAPI`: React, UI primitives, a cancellation signal, renderer and style registration, and cleanup registration. It does not receive the application's other plugin contributions. Register only renderers matching a declared category and MIME type. The host loads this entry separately for the selected source graph and cleans it up when that scope is retired or the plugin is disabled.

Content renderers also receive `host.actions` and `host.invoke`. When a completed conversation resource advertises `conversation.composer.submit`, it accepts `{ text, context? }`: up to 8,000 text characters and optional JSON context of up to 16,384 characters. Invoke it synchronously from a real button click or its native form submission, including keyboard activation. Form submission must match that click’s exact form and submit button; a standalone `requestSubmit()` does not authorize an action. The host consumes that interaction once, binds the source conversation and artifact, and submits through normal turn admission and permissions. The action rejects during a running turn, after source replacement or unmounting, or when its plugin is disabled. It leaves the composer draft and attachments intact. `metadata.interaction` reports `running`, `readOnly`, and an optional `disabledReason`; action availability must still be checked at invocation. This input contract is specific to content renderers; the composer presenter submits its existing draft without input.

A content renderer may call `host.reportState?.(values)` to provide its current JSON draft to the owning conversation. This untrusted data is scoped to the mounted source; it does not send a message or authorize an action. Reports over 16 KiB are ignored. Hosts may omit this capability; renderers must work without it. Do not report speculative preview state. The host discards reports when the source is replaced or unmounted, or the renderer is disabled.

To preview streamed tool arguments, a `tool-result` registration may declare `toolInput: { capability }` with a literal MIME `match`. Only the current winning renderer for that MIME receives previews. `content.preview` marks transient output; `metadata.toolInput` contains cumulative/coalesced `argumentsText`, `turnId`, `status`, and optional `error`. Parse only complete bounded units, keep the last valid preview, and do not execute actions or persist speculative state. The first matching final resource retains the preview's part identity.

Persist interaction state through ordinary plugin storage, scoped by the host's conversation and artifact identities. Stored view data is local to the current Wuu store; it is not cross-device synchronization. Restoration must not submit actions or overwrite edits made while a read was pending.

## Views, cards, and status

A view is a registered component placed in `navigation`, `primary`, `auxiliary`, `inspector`, `settings`, or `overlay`. `registerViewPlacement` requests an initial placement. Manifest entries in `contributes.navigation`, `workspaceTools`, or `settingsPages` give users a host-owned way to open the view; each must refer to a view registered by that plugin.

`persistence=durable` preserves view layout state across restoration; it does not persist arbitrary component state. Use namespaced storage for data that must survive unmounting. View props include a host API for storage, settings, commands, and view navigation.

For a website page, render `api.ui.WebPage({ url, title? })` as a component inside the same registered View. It is supported only in Wuu's main desktop workbench window, not popped-out conversation windows or remote web/mobile clients. It uses the desktop Chromium surface and shared browser login session, including cookies imported through the existing browser settings. It does not read another browser's cookie store automatically. The initial `url` must be HTTP(S); changing it navigates the page. The host provides navigation controls and keeps the native page inside its visible region. Switching to another workbench view hides the page while preserving the same live page and its in-page state. Explicitly closing the View, unmounting its WebPage component, disabling the extension, or reloading its generation closes the page and popups; cookies remain in Wuu's browser session. Restoring a durable View opens its configured URL, not its previous in-page state. There is no website-specific plugin registry or manifest. See the [website page recipe](plugin-recipes.md#add-a-website-page).

Conversations and primary plugin views use sidebar navigation, including API-opened views without a declared navigation entry. Their headers omit `tabs`, `activeTabId`, and tab actions. Primary plugin headers provide `canNavigateBack` and `header.navigate-back`. The host adds its own close control only when closing removes the page from the sidebar: API-opened views and additional instances. A declared navigation entry's page is left through Back or the sidebar. Compact workspace headers also provide back navigation, beside a close control for the open tab. These remain optional fields and actions in contract version 1: inspect the snapshot and `host.actions` instead of rebuilding a top tab strip from saved views. Auxiliary panels and plugin content can still have their own tabs.

Workspace header tabs can report `closable: false` when their provider does not support closing that entry. The field is absent on older hosts. Application providers that support closing the expanded conversation publish `previewWorkspace.conversationOpen` and implement `preview.open-conversation` and `preview.close-conversation`; older V1 providers omit the field and retain the always-open conversation entry. Check the field before invoking those actions.

Conversation cards are transient interaction, not saved history or durable pages. A card handle can update its state or dismiss it. For composer status, return structured items from `getSnapshot(context)` and notify through `subscribe`; keep snapshots stable until data changes. Wuu renders the row and handles overflow and the optional `open-session` action, rather than accepting arbitrary React content for each status item.


### Independent conversations

A primary view may use `layout: "user-space"` to suppress Coding workspace panels while retaining Wuu's normal navigation. This presentation hint grants no runtime authority.

A primary view may also declare `canvas`: `"opaque"` (default), `"translucent"`, or `"transparent"`. When the selected shell skin places artwork in the canvas, `"opaque"` keeps the view on a nearly opaque veil of the theme canvas, `"translucent"` shows the artwork behind a veiled canvas, and `"transparent"` exposes the artwork directly — in that case place content on host cards such as `api.ui.Card`, because plain text sits on the artwork itself. The hint has no effect under frame-placement skins. Activating an existing view publishes a `workbench.view.activated` host event with its `pluginId` and `viewTypeId`; scope handlers to both identifiers.

Desktop hosts provide `api.ui.ConversationMessage` and `api.ui.ConversationComposer` using Wuu's native message renderer and composer, including the normal attachment picker, paste/drop handling, previews, and send/stop controls. Supply conversation identity, a controlled prompt, attachments, and transport callbacks. The caller owns admission, error handling and when a successfully admitted draft is cleared. A failed send must retain its original text and attachments. Hosts without native conversation components report the composer as unavailable rather than silently rendering a substitute.

`ConversationComposer` optionally accepts `commands`, `commandsLoading`, `commandsError` and `onRefreshCommands`. Each command has an ID, name and description, plus either `kind: "skill"` with a catalog `skill`, or `kind: "action"` with `onSelect`. The component reuses the native slash menu and keyboard interaction; the caller supplies a catalog from its own transport. It never discovers Coding skills implicitly. Actions preserve attachments; skill drafts carry explicit identities to the caller's send handler.

`api.ui.ConversationView` provides the same reading column, native titlebar/search dialog, composer docking, scroll following and per-conversation reading restoration. Supply the title, optional avatar/status, search content, message children and composer. `ConversationMessage` accepts a stable `id`, optional timestamp, latest-answer indication and attachment content; independent conversations expose copy/time without unsupported Coding actions. `api.ui.ConversationAttachment` uses a compact native attachment card with caller-owned loading and verified local Blob URLs. Supply a scoped stable `resourceId` (including message/file identity and digest) and return the verified Blob URL from async `onLoad` to open the body in the existing workspace file-tab strip. The separate download action does not activate a tab. Remote attachments remain read-only byte snapshots, not workspace paths or Coding artifacts; raster images and bounded text are previewed, HTML/SVG/scripts are inert source, and unsupported formats offer download. Hosts without the preview context retain the loading action. These presentation components never load Coding history, choose a workspace or read attachment paths. The caller retains its scoped transport and authorization checks.

`ConversationView` accepts `presentation: "bubbles"` for messaging surfaces such as twin. This opt-in preserves the native message content, attachments and streaming while removing message action rows. Swipe left on touch, drag the conversation background with a mouse, or scroll horizontally on a trackpad to reveal persisted timestamps; reverse the gesture to hide them. A keyboard-focus-only toggle provides the same action, with Alt+Left/Right and Escape shortcuts. Unknown timestamps are not invented. Standard presentation remains the default.

`ConversationView` can receive a `sessionId` and `sessionScope` to present host questions through the same validated user-space/domain route for list, answer, cancel and hold. For non-tool offers, `onQuestionReply(requestId, prompt)` must admit the answer to that conversation with a stable request identity before resolving the offer. Keep unrelated composer text and attachments intact. A hold keeps the question unresolved; it is not an answer.

Live messages must reconcile with persisted history using stable item identities and the final sequence in the terminal turn snapshot, never by matching text. Reply cancellation must retain the selected turn ID across retries so a delayed stop cannot cancel the next reply.

Independent conversations do not pass drafts, messages or workspace selections to Coding-scoped presenters or completion providers. Use `linkPolicy: "external-only"` for messages that must not resolve Coding workspace paths. This UI boundary does not replace runtime scope checks. The default Personal contract provides logical memory-domain isolation; verified OS/host isolation is a separate optional embedding mode.

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

## Diagnostics

Plugin details use `plugin/diagnostics/list` for both built-in UI modules and installed plugins. The request's `id` and `fingerprint` are checked against the runtime generation retained for that request. Unknown, disabled, and outdated identities return errors.

The response includes runtime contribution failures and reported renderer registration, render, and cleanup failures. Renderer entries identify the window connection and phase in `contribution`, with the reported error in `message`. A healthy plugin returns an empty diagnostics list.

## Appearance and cleanup

Use `api.react` and `api.ui` so controls follow the host's theme, spacing, and typography. Keep UI size separate from code size. Prefer declarative theme tokens for appearance-only changes; the [surface matrix](theme-surface-matrix.md) lists the public contract.

`api.ui.Avatar` defaults to Wuu's circular headset identity, shared with the public `mascot` icon. It accepts `size` in pixels, `className`, `status` (`idle`, `thinking`, `sending`, `responding`, `queued`, `waiting`, `failed`, or `interrupted`), and `motion` (`static` by default, or `ambient`). An optional `avatarKey` selects a `mascot-v1:shape:accessory:hue` style: shapes are `round`, `rounded-square`, `capsule`, `triangle`, and `diamond`; accessories are `none`, `beanie`, `hard-hat`, `headset`, and `leaf`; hue is an integer from 0 to 359. For example, `mascot-v1:diamond:leaf:150`. Omitting the key keeps the brand identity. Use static avatars in compact navigation and ambient motion for larger visible identity surfaces. Status changes retain the configured shape and accessory; ambient motion respects reduced-motion preferences and page visibility. Label the surrounding control because the avatar is decorative. Hosts without the desktop avatar renderer provide a static agent mark and may ignore the key. Feature-detect `api.ui.Avatar` when supporting older hosts.

On desktop, Personal's profile panel provides **Change style** with shape, accessory, and color previews. Save persists the appearance on the existing Personal identity; Cancel or closing the editor discards changes. Reset previews the original round, headset, hue 222 style and also requires Save. The Twin sidebar entry, header, profile, and rename dialog share the saved appearance, including after navigation and restart. Global branding and other plugins retain their own icons. Renaming preserves it. Existing identities need no migration. Native iOS and Android avatars have not been adapted to this setting.

Standard browser APIs such as selection and resize observers are available. Public `data-wuu-component`, `data-wuu-slot`, and `data-wuu-surface` anchors are preferable to private class names or React internals. They do not make surrounding DOM structure stable. Register cleanup for listeners, observers, timers, and other resources you create outside React's own effect cleanup.

Registrations belong to a plugin generation and are removed when it unloads. On native desktop quit, Wuu first unmounts React consumers, aborts pending activation, cleans up plugins in reverse dependency order, and flushes activation reports before disconnecting the renderer and stopping backend processes. A renderer that cannot acknowledge cleanup within ten seconds is closed and the failure is logged. That lifecycle is not a security sandbox: trusted renderer code and CSS can still misbehave. Keep the default-UI recovery path available, handle asynchronous action errors, and test disable/reload as well as initial rendering. Package trust and update behavior are described in [plugin management](plugins.md).

### Runtime display names

`api.setNavigationTitle(id, title)` updates only a navigation entry declared by the calling plugin generation. `api.setViewTitle(id, title)` updates the visible title of one registered View type. Titles are trimmed and must contain 1–64 Unicode characters. Both return `false` for an unknown ID; invalid titles and calls from an unloaded generation fail. The overrides are presentation-only, reset on reload, and never change routes, settings-page labels, or other plugins. Persist user preferences through the plugin's existing runtime service, then republish stored titles after loading or saving them.

`api.setNavigationAvatar(id, avatarKey)` gives an owned navigation entry a static `Avatar` style using the same key format. It supports agent destinations such as personal assistants or project agents without changing global branding. Pass `null` while loading to retain the preceding generation’s artwork (or an empty icon slot on cold startup); pass `undefined` to restore the declared icon. Undeclared entries return `false`; invalid keys and unloaded generations fail. Overrides are transient and scoped to the declared entry; the plugin remains responsible for durable identity and revision ordering. Runtime calls require an active generation: subscribe to contribution changes and check `getGenerationActivation` before reading identity, then publish it after load/save. Navigation providers receive the optional `avatarKey` on each destination; `null` denotes a loading slot. Feature-detect the setter for older hosts.

### Surface focus and recovery

The default shell coordinates focus through mounted surface capabilities, not a provider's CSS classes or input markup. Its composer slot receives `context.registerFocusTarget`; its preview slot receives `context.layout.registerFocusTarget`. Register a `UIFocusTargetV1` in a layout effect and return the registration's cleanup function. `focus(options)` focuses the surface's own enabled control and returns `false` when it is not ready; `contains(element)` reports whether an element belongs to that surface. Respect `preventScroll`. Split-conversation providers forward the shell's `context.composerFocusTargets[pane]` as the corresponding composer's `registerFocusTarget`. The shell owns navigation timing and avoids overriding a later user interaction; the provider owns its DOM, readiness, and cleanup. A replacement may use an input or another editor without imitating the default composer's classes.

A failed selected surface stays visibly unavailable and may offer a local render retry. Missing or conflicting providers require fixing the selection in Settings; retry must not silently switch to a built-in provider. Optional wrappers preserve their children, while diagnostics retain the actual failure. Composer-owned selection intake and model refresh subscriptions stop when that composer unmounts; they are not shell side effects.
