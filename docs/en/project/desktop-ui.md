# Desktop UI maintenance

Use the renderer's shared components and design variables when changing the desktop interface. Check the result in a real browser or Electron window: type checks and jsdom tests cannot establish readable spacing, working scroll effects, or a visible keyboard focus ring.

The [Wuu design system](design-system.md) defines color, typography, spacing, radius, elevation, and interaction guidance, with regenerable light/dark boards. Use it for design roles and this page for real component previews and acceptance entry points.

## Preview real components

After [development setup](development.md), start a renderer preview server:

```bash
cd desktop
npx vite --host 127.0.0.1
```

Open a path such as `/dev/design-system/` or `/dev/button-standards/` on the port Vite reports. The [`desktop/dev`](../../../desktop/dev/) directory contains fixtures for specific components and states. These use synthetic data and do not reproduce every product bridge or lifecycle.

The onboarding preview has its own Electron entry:

```bash
npm --prefix desktop run dev:onboarding
```

It renders the real first-run component without the product preload, app-server, or persistent profile. Choices are not saved; use dummy model credentials. Reload with Cmd+R or Ctrl+R to start again. The temporary profile is removed on normal exit. This preview checks presentation, not login or settings persistence.

The [mascot lab](../../../desktop/dev/mascot/README.md) uses `npm --prefix desktop run lab:mascot`. Use the full `make dev` path when the change depends on native behavior, IPC, or real session state. Keep temporary screenshots in ignored output directories and use synthetic content in committed fixtures.

## Archived conversations

Settings → Archive lists archived conversations across all workspaces. **Delete
all archived** refreshes that catalog, then confirms its exact count, including
conversations hidden by search or workspace filters. Deletion permanently removes
conversation history and owned artifacts; clean managed worktrees are reclaimed
while dirty worktrees are kept. Unarchived conversations are never selected, and
a conversation restored before deletion is protected by the server.

Only the confirmed snapshot is processed. Progress disables repeated submissions
and restore buttons. Partial failures keep their remaining archived conversations
available for a separate confirmed retry; newly archived conversations are not
added to that retry. Cancel or dismiss the confirmation to keep the conversations.

## Workspace new pages

The right panel's plus button creates a closable **New page** tab. Choosing a tool
replaces that page in place; if the tool is already open, Wuu closes the new page
and focuses its existing tab. Installed extensions appear under **More tools**.
**Continue viewing** links to up to four currently open files, diffs, or delivered
artifacts; it is absent when none are open. Closing a page restores
the previously active tab. The page's sections keep a readable width but start
on the pane inset under the tabs instead of centering in a wide panel, so tool
icons line up with the tab icons.

Run `npm --prefix desktop run test:e2e:workspace-new-tab` for production-renderer
checks of selection, singleton reuse, close recovery, keyboard navigation, and
extension loading through a synthetic bridge. Screenshots and `results.json` in
`desktop/out/workspace-new-tab-e2e/` cover light/dark themes, 14px/20px UI sizes,
and wide/narrow windows. This does not validate a live app-server or browser engine.

## Conversation image previews

Images returned while inspecting files or reading tool output stay inside the
aggregated tool activity. Expand that process row to load its image previews,
then click an image to enlarge it. Inspection images do not split tool groups
or become separate output rows.
Explicitly presented image artifacts, message attachments, and Markdown images
remain directly visible. This distinction also applies to PTC and background
tool results.

Message and inline tool images reserve a responsive 4:3 preview area before
loading. The complete image fits inside without cropping or upscaling; portrait
and panoramic images may leave space around them. Loading failures keep the same
area, so switching conversations does not shift text when an image finishes.
Opening a preview still shows the original image. Workspace document images keep
their natural proportions.

Run `npm --prefix desktop run test:e2e:image-layout` to check cold loads, failed
loads, cached session switches, and bottom following in Electron. It saves
geometry logs and screenshots under `desktop/out/image-layout-e2e/`.

Image previews group the current conversation's displayed uploads, tool-result
images, and message images in display order. Previous/next buttons and the left
and right arrow keys move through the group; the counter shows the position.
Navigation stops at either end, and a single image has no navigation controls.
Expand collapsed content or load earlier history before opening its images.
The group stays fixed while the preview is open, so streaming output does not
shift the current position. Each image starts fitted and unrotated; Shift with
the left/right arrow keys pans a zoomed image. Escape closes the preview and
returns focus to the opener.

## Video previews

Video output cards open a player in the right panel. Workspace video files use
the same player. Playback starts only on request, with native controls for
pause, seeking, volume, and fullscreen. Local MP4/M4V, WebM, MOV, and OGV
containers are recognized; playable codecs depend on the desktop runtime.
If playback fails, the preview shows a message and keeps the download action.
File delivery snapshots retain their integrity checks, including range requests
used to seek within a video.

Run `npm --prefix desktop run test:e2e:artifact-preview` to exercise delivered
previews in Electron with synthetic content, separate from your app data.

## Composer attachments

Images, videos, PDFs, and pasted text attachments wait in one tray that slides out
from behind the input's top edge; adding or removing one never resizes the
input. The tray changes layout once, and everything above it rises or settles
through counter-translated compositor animations while the input holds still.
A card removed from the tray fades where it stood as its neighbours close the
gap. Sending or switching drafts clears the tray at once. The tray keeps a
single row and scrolls horizontally, with the inline edge fade described under
scroll-edge fading.

Unsupported attachment types and attachment failures use the shared capsule
notification, rather than a persistent status line above the input, in both
normal and split conversations.

Preview `/dev/composer-attachments/` with optional `theme=dark`, `size=20`,
`width=420`, `hero`, `queued`, and `seed` parameters. Its buttons paste
synthetic files through the real textarea paste handler.

## Composer project and worktree controls

A new conversation's project sits in one outlined group; in a Git project, the
branch and the worktree toggle share a second group of the same height. Hover
fills a segment edge to edge, and the toggle's on state uses the interaction
accent like Fast mode. A folder outside Git shows only the project group.

Both groups open compact menu cards. The project card lists **Conversation**
first, as the sidebar does, then projects in sidebar order; opening a folder and
creating a blank workspace follow a divider. The branch card needs no heading
outside a worktree: the checked-out branch comes first with a count of its
uncommitted files, then the local default branch, then the rest most recently
committed first. A shared prefix such as `codex/` is muted so the branch's own
name carries the row. The search field doubles as the new branch name: a name
that matches no branch adds a create row, and Enter creates it only when no
branch matches, so a partial match never becomes a branch. While the draft starts
in its own worktree, the card is headed **Start from**, choosing a branch changes
nothing in the project, and it offers no new branch.

Run `npm --prefix desktop run test:e2e:composer-worktree` to start a real
conversation in a worktree through Electron and the Go core with a disposable
profile, Git fixture, and local provider. It checks that choosing a start branch
leaves the project's checkout alone and captures light/dark, default/large-font,
wide/narrow, and non-Git states in `artifacts/composer-worktree/`. The window is
visible, so the pointer's position can add a hover surface to a capture.

The current worktree belongs in the environment information panel, with its
repository, base commit and path. It does not insert a creation banner into the
conversation. Agent workspace switches stay at their tool-call position in the
process trail; expand a record to see that invocation's destination or failure.
Later switches do not rewrite earlier records. The same E2E checks a switch back
to the project and captures both the tool record and the current-worktree panel.

In that panel the change summary and branch are status rows; commit and pull
request follow them as a line of quiet text actions on the label axis. Users
usually ask the agent to commit and push, so these actions do not form a group
of their own. `desktop/dev/environment-panel/` renders the panel, and its
`capture.cjs` checks the close button, row geometry and TODO alignment in both
themes at default and large text.

## Settings pages

Settings lists its pages in one unlabeled column: model providers, agents, built-in agent, MCP servers, appearance, general, phone access, usage, subscriptions, and archive. With this few pages, group headings cost more reading than they save. Plugin pages follow after a group gap, because their number and content come from installed plugins. Page IDs are part of the plugin settings snapshot, so they stay stable when a label or position changes; the built-in agent page keeps the `advanced` ID.

Every page shares one column measured in UI text, so the title stays in place while navigating and a label stays within a glance of its control at large sizes. A page opens with its title, an optional line that states a non-obvious scope, and page actions beside the title; a refresh action is an icon button whose tooltip names it. Section titles are the only other semibold text; rows stay regular inside one bordered group. Groups, service cards, the provider catalog, and subscription cards share one frame, the `--hairline` border with the small-card radius, and a section title sits `--section-heading-gap` above its content. Keep a row description only for constraints or consequences the title does not already state. A unit belongs inside its numeric field instead: a numeric placeholder keeps the unit, while a word such as "Auto" appears alone.

A row that works says nothing about it. Only a state that needs a look gets a symbol — a warning triangle for a missing credential or a pending decision, a circled mark for a failure — and the symbol's accessible name and tooltip carry the reason, so the meaning never rests on color. The Agent page lists detected agents in the default radio group; agents Wuu cannot find wait under **Not installed**, each with an install action and its path override, instead of repeating why on every row. Agents expand in place under their own row. Rows without a disclosure reserve its footprint so trailing controls end on one axis. The titlebar gains its hairline only after content scrolls beneath it.

**Model providers** is built for bring-your-own-key use and reads by how often each part is used. The default model comes first as one filled card whose model name is the picker, with the reasoning effort beside it; new conversations start there. Connected services follow as bordered cards with their vendor mark, a **Default** badge, and a warning when a key or sign-in is missing. Providers to add are quieter tiles in one run on the same surface, their marks on the cards' inset: subscription sign-ins, then likely vendors for the interface language, with **More providers** for the whole model catalog and **Custom endpoint** for any OpenAI- or Anthropic-compatible URL. Only the full catalog names its kinds (subscription, vendor, relay, local); the short list on the page does not. The page carries no explanatory lines; a service's name, mark, and model count say what it is. Connecting asks only for the key — the catalog supplies the endpoint and a suggested model — and becomes the default only when switched on, which it is when the current default cannot answer. A service card opens its own page with a way back: its connection (key, Base URL, or sign-in), edited in place without changing the row's height, and its models, whose names share the row labels' edge while a trailing check marks the model the service uses. Hidden choices wait under a disclosure, and **Add model** takes an unlisted model ID. Dialogs put their mark, title, and close button on one row, and each dropdown lines its options up under the text of its trigger. Editing a service never changes the default; **Make default** does. Vendor marks are the bundled models.dev logos, so no artwork is fetched.

`npm --prefix desktop run test:e2e:model-services` drives these flows through Electron and the Go core with a disposable profile, credential store, and local provider: it connects a catalog provider without moving the default, chooses and hides its models, makes a custom endpoint the default, removes a service with its key, and sends a turn that must reach the new default with the saved key. Screenshots and `evidence.json` go to `artifacts/model-services-e2e/`.

Programmatic tool calling is agent runtime behavior, so it sits on the built-in agent page. The Codex pet is part of how the app looks, so it has a section on **Appearance**; **General** keeps the language and **About**. Archive groups show the project with a bare conversation count, rows show the year only when it differs from this one, and restoring is an icon button named for the conversation.

Preview `/dev/settings/` with `page` set to a page ID, and optional `theme=dark`, `size=20`, `lang=en`, `rail=` (sidebar width), `collapsed`, `long`, and `empty` parameters. Providers, agents, MCP servers, usage, and archive rows are synthetic; nothing is saved.

## Plugins catalog

The Plugins page, named **Plugins** like its sidebar entry, uses the settings page column. The titlebar already names it, so the page opens on a toolbar: search, then reload and local install. **Plugins** and **Skills** are separate tabs with their counts; a catalog without plugins opens on Skills. A search hides every group it leaves empty.

Plugins are grouped by what they need from you, each group a bordered list like a settings group. **Needs attention** rows name the plugin and the one most serious reason — it failed to start or misses a requirement, then a staged update, a changed or unapproved package, or a conflict — and open the plugin's page, where that decision is made. **On** and **Off** rows carry the mark, the name over a one-line tagline, a switch that turns a trusted plugin on or off in place, and a chevron to the plugin's page. Turning on a plugin that is not approved yet opens its page when it asks for permissions and approves it directly otherwise. Rows show the manifest's display name and tagline, not the plugin ID. Skills are listed the same way, under **Official skills** and **Your skills**; a skill row ends in the owning plugin's name, when there is one, and a chevron that opens the preview.

A plugin opens as its own page, with a back link to the catalog. The header holds the mark, name, and tagline, and either the switch or the decision the plugin waits for (approve, or approve an update); rejecting, revoking, and removing live in the more menu. Notices follow: why approval is needed, an update being ready, the runtime error, or a missing or conflicting plugin. Below come the manifest's long description, **In Wuu** (the sidebar pages, workspace tools, settings pages, commands, skills, and themes it adds), and the plugin's settings as ordinary settings rows. Permissions appear only while approving. Versions, fingerprints, paths, grant scopes, and agent tools stay off the page.

Preview `/dev/extensions/` with optional `theme=dark`, `size=20`, `lang=en`, `long`, and `empty` parameters. Skills and plugin packages are synthetic and cover every plugin state; switches and detail actions change only the preview's state.

The fork destination dialog offers the current directory and a new worktree. Click outside or press Escape to dismiss it without creating a conversation; focus returns to the message action. Dismissal is temporarily locked while a fork is being created.

## Review panel

The Review panel lists changed files by folder beside the open file's diff, or, when the panel is too narrow for both, shows the list first and the diff after a file is chosen. One changed file opens directly without the list. The diff header carries the file's status and counts with the viewed, open-in-editor, previous, and next actions.

From `desktop/`, run `npx vite --config dev/review/vite.config.ts` and open `/dev/review/` with optional `theme=dark`, `size=20`, `lang=en`, `width=` (panel pixels), and `set=one`, `clean`, `repo`, or `long`. Changes are synthetic. With that server running, `./node_modules/.bin/electron dev/review/capture.cjs` captures light/dark, default/large-font, wide/docked/narrow, and opened-file states into `artifacts/review-panel/`, with each capture's layout and overflowing elements in `results.json`. Review the captures yourself; they are not a merge gate.

## Failed turns and confirmations

A turn that ends in failure shows one card: what happened in plain words, the HTTP status as a quiet code when there is one, the next step as a button, and the technical record behind **Details**. A rejected credential, an exhausted quota, or a model the service does not have leads with **Open model providers** and offers **Retry** beside it. Rate limits, overload, timeouts, and network failures offer **Retry**. A conversation too long for the model offers no retry, because replaying it fails the same way, and says to run `/compact` or start a new one. Only the latest turn carries actions.

Irreversible or disruptive actions ask through `confirmAction` in [`ConfirmDialog.tsx`](../../../desktop/src/renderer/ConfirmDialog.tsx), which resolves to the answer like the native prompt it replaced and follows the product's type, theme, and copy. The title names the object, one sentence states the consequence the title does not, and the confirm button repeats the verb; `tone: "danger"` marks actions that destroy data. Do not call `window.confirm`.

A question the agent asks, or an engine's approval request, is one card: over the composer as an offer, or in the reading flow beside its turn. The tour's agent is never offered `ask_user` and engine approvals need an engine, so preview the production card at `/dev/question-card/` with optional `theme=dark`, `size=20`, `lang=en`, `width=`, `inline`, `two`, `approval`, `long`, `multi`, and `expires`. Its requests are synthetic and nothing is sent.

## Walk the whole product

`npm --prefix desktop run test:e2e:ui-tour` drives the real Electron app, preload, and Go core with a disposable profile and home directory, synthetic projects, and a scripted local provider, so no account or model is involved. It seeds conversations through the real composer (plain text, tool calls, reasoning, a rejected credential), then visits the main surfaces — home, conversations, composer menus, workspace panel tabs, search, Plugins, the account menu, and every Settings page — in each appearance of a matrix of theme, UI text size, and window width. The default matrix is light, dark, 20px UI text, and a 720px window.

Each stop saves a screenshot and a geometry audit. Horizontal overflow fails the run. The audit only records the corner radii in use, text under 4.5:1, clipped text, and controls under 24px, so two revisions can be compared. The run also walks the journeys between surfaces and fails when one breaks: a draft survives Settings and conversation switches, the selected row follows navigation, closing a menu or search returns focus to where it came from, and a long conversation keeps its reading position across a switch.

Screenshots, `audit.json`, and `report.json` go to `artifacts/ui-tour-e2e/`, which each run clears; `WUU_TOUR_OUTPUT` moves it. `WUU_TOUR_ONLY=settings,plugins` selects stops by substring, `WUU_TOUR_MATRIX=dark:20:1280` replaces the appearances, `WUU_TOUR_CORE` points at another core binary, and `WUU_E2E_HIDDEN=true` renders without showing or focusing any window. The screenshots are evidence to look at, not a visual gate, and fonts differ between platforms.

## Shared typography and geometry

Composer feedback belongs in the shared reading area above the input, not beside
the send button. Main and split composers preserve errors, action restrictions,
and operation progress there, wrapping long text and allowing it to scroll.
Restored drafts and updated Git results already show success; do not add a second
confirmation through the global status field.

[`base.css`](../../../desktop/src/renderer/styles/base.css) defines the renderer's base roles, including typography, colors, corners, focus, and elevation. [`spacing.css`](../../../desktop/src/renderer/styles/spacing.css) defines spacing roles, density boundaries, and minimum control sizes. Prefer these existing roles to new per-component constants.

Pointer clicks do not paint an extra outline. Text fields keep the caret; other controls keep their rest and hover surfaces. A 2px `--focus-ring` outline appears after Tab, or after arrow-key movement on a non-text control. Arrow keys inside an input or textarea do not count as keyboard focus movement. Chromium still reports `:focus-visible` for a click into a text field, so rings key off `html[data-focus-modality]` from [`FocusModality.ts`](../../../desktop/src/renderer/FocusModality.ts) rather than that pseudo-class alone. Do not add a per-control click ring or a second frame around an existing field border.

Respect the user's separate UI and code font preferences. Let rows grow with their content, reserve space for trailing actions and status indicators, and align peer labels independently of whether a row is running or unread. Density changes whitespace rather than removing minimum target sizes; coarse pointers have larger control floors.

Compact menus use `--menu-inset`, `--menu-item-gap`, and `--menu-shell-radius`. The shell radius combines the inner radius with the inset to keep nested corners related. Panel and dialog overlays use their own radius role. Reusing one numeric radius on every padded layer does not produce the same geometry. Click-open overlay cards — context menus, permission pickers, and select panels — use `--font-menu` (one step below `--font-ui`) for item labels, with `--weight-medium`. Group labels and secondary hints use `--font-xs`. Compact composer chips and triggers share `--font-sm` with that overlay step. Question cards above the composer stay on `--font-ui`; they are reading surfaces.

Context menu items name actions, not the data they act on: "Open in system browser", not the full URL. Show a destination in the trigger's tooltip or a separate secondary line. Context menus size to their actions, stay inside the viewport, and ellipsize or scroll instead of spreading across the reading column. Only one context menu is open at a time, and tooltips stay closed while it is open, matching native menus.

Public plugin theme tokens are a smaller contract than all internal CSS variables. Consult the [theme reference](../customize/theme-surface-matrix.md) before exposing a new token or telling plugin authors to depend on an internal variable.

## SVG artwork

Product controls import [WuuIcons](../../../desktop/src/renderer/WuuIcons.tsx).
The original 24-unit artwork in [iconArtwork.ts](../../../desktop/src/shared/iconArtwork.ts)
also supplies the browser overlay. Named extension icons and skill marks reuse
these drawings. Third-party brand identities keep their own recognizable marks.

Use `/dev/icons/` to compare the complete family at 12–32px, including real
toolbar controls, light/dark themes, default/large fonts, and disabled states.
Match apparent size, stroke density, and centering within each control role;
equal SVG boxes alone are insufficient. Keep shape corrections in the artwork
instead of adding per-screen scales. Larger illustrations and data-driven SVGs
retain their own layout and meaningful geometry.

Twelve standalone [Morandi SVGs](../../../desktop/src/renderer/assets/morandi/)
are available for optional colored surfaces. They reuse the same silhouettes
with muted sage, slate, clay, oat, and lilac accents. The icon preview includes
them on both backgrounds. These are spare assets; ordinary controls retain
their semantic foreground colors.

## Background images

Preview the global background with `/dev/three-pane/?background`; omit `background` to check the same image across the three panes, and add `empty` to check the empty-session canvas and the dock strip under the composer. Import and processed images stay in the local desktop profile, and raster work runs in a worker rather than during layout. Only main canvases reveal the image; menus, inputs, editors, and overlapping drawers retain their theme surfaces.

Plugin page roots should leave the canvas to their host instead of painting another opaque canvas. This lets primary, settings, and workspace pages share the wallpaper while overlay and auxiliary hosts retain their solid surfaces. Preview the real Automation plugin with `/dev/automation/?region=primary`; `workspace`, `settings`, `overlay`, and `auxiliary` exercise the other containers.

With Vite running on port 5189, run `npm --prefix desktop run test:e2e:background-image` (or set `WUU_FIXTURE_ORIGIN` for another port). It uses an isolated profile to check import failures, transaction rollback, effects, persistence, cross-window updates, and the bundled worker under the production CSP. Rendered pixel checks cover the real plugin across those containers, including opaque overlays. It also captures light/dark, default/large-font, wide/narrow previews in `artifacts/background-image/`. Review those captures separately; pixel and geometry checks do not establish visual acceptance.

## Sidebar folds

The Pinned, Folders, and Workspace headings support mouse drag reordering. The desktop profile remembers their order.

Use `SidebarCollapseBody` for sidebar sections and nested groups. It animates intrinsic height and the heading gap together, retains rows until closing finishes, and prevents hidden rows from receiving focus. Avoid inherited measured-height variables or descendant animation rules that change a nested fold when its parent toggles.

Preview `/dev/sidebar-collapse/` with optional `theme=dark`, `size=20`, and `width=240` query parameters. Run `npm --prefix desktop run test:e2e:sidebar-collapse` for Electron geometry checks covering nested folds, reversals, changing content, and reduced motion. These checks do not replace visual acceptance.

Project conversation lists start with five entries in the existing sidebar order
(including saved manual ordering), plus selected, switching, running, and unread
conversations. An unread-to-read receipt retains the conversation for two minutes;
only the three most recently read conversations per project receive this grace
period. They keep their existing positions. Becoming unread again, leaving the
list, or closing the project group clears the corresponding retention. Selected,
switching, running, and unread conversations remain candidates independently of
that limit.

Show more reveals the next five hidden conversations in sidebar order, or the
remaining conversations when fewer than five remain. It disappears when all are
shown. Already visible status rows do not consume the batch. Collapse returns to
the recent range without closing the project. All ranges, including conversations still being created,
use their full content height within the shared sidebar scroll area. Scrolling
over a project conversation moves the outer sidebar; expanding history moves
following groups down. The history controls follow the rows. Add `mode=history` to the preview URL for the real project
component. The same Electron check covers batched expansion through 1,003 conversations, read transitions,
outer sidebar scrolling, creating rows, and live font changes, and writes geometry JSON
and light/dark, 14/20px, wide/narrow screenshots under `desktop/out/sidebar-collapse-e2e-*`.

## Motion

Motion tokens live in one place: the ladder in [`base.css`](../../../desktop/src/renderer/styles/base.css). `--motion-fast` (120ms) is pointer feedback, `--motion-base` (180ms) covers menus, popovers, and content swaps, `--motion-slow` (280ms) structural moves, and `--motion-slower` (440ms) large folds. `--ease-out` carries entrances and `--ease-in` exits. Transitions, entrances, and exits read a rung or one of the semantic aliases beside it. A literal duration is reserved for a rhythm, such as a spinner on `--motion-spin`, an ambient loop, or choreography paced by a JS clock, and its rule states its reduced-motion behavior next to it.

Entrances and exits use the shared keyframes rather than a new copy per surface. `wuu-enter` and `wuu-exit` read their offsets from `--enter-x`, `--enter-y`, `--enter-scale`, and `--enter-opacity` (or the matching `--exit-*` properties) on the animated element:

```css
.toast {
  --enter-y: 8px;
  --exit-y: -4px;
  animation: wuu-enter var(--motion-base) var(--ease-out) both;
}

.toast.closing {
  animation: wuu-exit var(--motion-base) var(--ease-in) both;
}
```

They move the individual `translate` and `scale` properties, so a surface's own `transform`, such as centering or a hover lift, still applies. The offsets are registered as non-inheriting, so a nested surface never picks up its parent's distance. `wuu-fade-in` and `wuu-fade-out` are pure fades, `wuu-pulse` is the ambient opacity pulse (`--pulse-opacity`), and `wuu-spin` is the only spinner. `menu-enter`, `content-swap-enter`, and the environment panel pair keep their named roles. The `/dev/motion/` fixture shows the ladder, the shared keyframes, and production surfaces that use them.

Reduced motion has two sources, the OS setting and the in-app Motion preference, and one result. `base.css` resolves either into `--motion-reduced: 1` and zeroes the ladder and its pinned aliases, so token-driven motion becomes instant without a component rule. Leave an entrance's resting style visible and let the zeroed duration carry reduced motion; `animation: none` also removes the fill that reveals a surface whose resting style starts hidden. Motion the ladder cannot reach opts out next to its definition with `@container style(--motion-reduced: 1) { ... }`. Do not use `@media (prefers-reduced-motion)`, which only sees the OS setting. Spinners keep turning, because they report ongoing work.

[`motion.ts`](../../../desktop/src/renderer/motion.ts) is the only JS bridge. `motionDurationMs` and `motionCurve` read a token when the motion starts; a value captured at module load misses a later preference change or theme override. `motionEasing` evaluates the cubic-bezier a token names for frame loops, and `messageMotionTime` is the shared document clock that frame loops and WAAPI entrances both read. Keep it that way: a hand-rolled `1 - (1 - p) ** 3` next to a `cubic-bezier()` token can drift away from the transition it was meant to match. `prefersReducedMotion`, `subscribeReducedMotion`, and `useReducedMotion` report the same two sources as the stylesheet, so never query the media feature directly. Motion the stylesheet cannot reach checks them explicitly: WAAPI, frame loops, `scrollTo({ behavior: "smooth" })`, and dnd-kit's inline sortable transitions and drop animations, which [`SortableMotion.ts`](../../../desktop/src/renderer/SortableMotion.ts) puts on the ladder. Content that stays mounted through its exit uses [`useExitPresence`](../../../desktop/src/renderer/useExitPresence.ts), which reads the exit duration when the exit starts and can release early from the motion's end event.

Check new motion at `/dev/motion/` with its Motion switch set to reduce, and again with the OS setting emulated in DevTools (Rendering > prefers-reduced-motion). Both must look the same.

Programmatic conversation scrolling uses one trajectory, [`ScrollGlide`](../../../desktop/src/renderer/ScrollGlide.ts). Each 60fps frame it keeps `0.85` of the distance still to travel, so the rate does not depend on how far the viewport has to move, the approach never reverses or overshoots, a dropped frame catches up over at most eight reference frames, and the last pixel lands exactly. The target is re-read every frame, which is what lets streaming output, a collapsing composer, or a late reflow extend the same motion instead of restarting it, and what makes the send bubble hold its screen position while the document shifts under it.

The glide models a position and a live target, not the remaining distance: the placement compensates a reflow during the React commit, at a timestamp where no frame has elapsed, so a step proportional to elapsed time would move nothing and let the bubble visibly shift until the next frame. Sending therefore ends its placement when the glide lands (about 350ms to cover 96%, then a settling tail) rather than at a fixed deadline; a longer jump takes longer instead of whipping. Reduced motion is decided by the caller, which places the bubble in one write.

Use a CSS transition from the ladder for motion whose geometry is already known: an entrance, a menu, a panel sliding to a fixed size, a hover wash. Reach for a frame-driven trajectory when the destination is only known while the motion runs — scrolling to content that is still arriving — or when something else keeps moving the target underneath it. Such motion needs no deadline and cannot restart; expressed as a transition it would have to be retargeted with a second transition, which is the seam this design removes. Keep a deliberate constant cadence as it is: a summary that reveals at about twelve characters per second is not settling, and a retained glide would turn it into a different animation.

## Conversation disclosure scrolling

Opening or closing tool/reasoning details preserves the reader's scroll mode. A conversation following the latest content continues following through the height transition; a paused conversation keeps its reading position. Wheel, touch, keyboard scrolling, scrollbar dragging, and text selection take precedence over layout correction.

Inside a bounded inspection strip, tools and reasoning follow their event order.
The first opening of live activity starts at the latest content; completed
history starts at the beginning. Closing and reopening restore the strip's
reading position and follow/pause mode, including after hidden content grows.
Position before paint, without a delayed forced scroll after the opening motion.

Sending a query reserves reading space below the bubble. Expanded details may temporarily occupy that space, but closing them restores what remains after actual response growth or deliberate browsing. A temporarily empty gap is not proof that the response has filled the reservation. Inspect repeated toggles while streaming, including a fold taller than the remaining gap and a session switch with the fold open.

Earlier-history paging inserts rows above the viewport. A paused reader's offset belongs to native scroll anchoring, so the manual prepend correction applies only while the offset still sits where the page was requested; adding the inserted height on top of anchoring moves the whole stream down by that height the moment the page arrives, which reads as a jump.

## Following and rendering long conversations

Session switches reuse the conversation loading animation and keep the outgoing view covered until the target resumes. Restore the target's folds and reading position before revealing it, forcing layout around its landing turns even when the cached scroll offset has not changed. Editing and sending stay disabled until this restoration finishes; a newer selection supersedes an earlier request. Wait for stable painted geometry, with a bounded layout phase so live output or external assets cannot keep the conversation covered indefinitely. Resume failures dismiss loading and show an error.

A conversation follows its latest content until the reader takes over. Wheel, touch, keyboard, scrollbar and selection input pause following before the browser delivers the scroll, so a streamed chunk cannot pull the view back. Moving back down to the latest content resumes following within a small band that absorbs output streamed while that scroll settles; moving up never resumes it. Jump to latest resumes following when clicked rather than when its motion lands, and the glide hands over to ordinary following once it is close to the moving bottom.

Off-screen turns skip layout and paint through `content-visibility: auto` and keep the height they last rendered at. Every turn renders once before it may skip, so a skipped turn never falls back to the placeholder height; a placeholder that differs from the real height moves the reader and the scrollbar when the turn finally renders. Chromium decides which skipped turns became visible only after a frame has painted, so [`ConversationRenderWindow`](../../../desktop/src/renderer/ConversationRenderWindow.ts) renders the turns around the viewport from a large scroll's own scroll event. Code that writes `scrollTop` inside a frame, such as a glide or the turn rail, calls it after writing. Do not read geometry inside a skipped turn: the read forces the layout the skip saves. Inactive cached conversations use `content-visibility: hidden`.

Values that change while a response streams, such as a submission's reading reservation, are written on the element that uses them rather than as an inherited custom property on an ancestor, which would restyle every rendered turn. For the same reason, shell children ahead of the conversation, such as the sidebar resizer, stay mounted and are hidden instead of inserted or removed.

`npm --prefix desktop run test:e2e:conversation-scroll` drives these behaviors with native input in a real Electron window: streaming against wheel input, returning to latest, jump to latest, completion and sidebar toggles for a paused reader, large scrolls after a sidebar toggle, history jumps, send placement and handoff, and session restore. It checks painted pixels for blank bands, so it shows its window by default; `WUU_E2E_HIDDEN=true` hides it. A very fast scrollbar drag across a long conversation can still outpace Chromium's rasterization for a frame, as it does when every turn is rendered.

## Scrollbar visibility

A scrollbar appears only while its container is actually scrolling and fades out after the last scroll event. Hovering a region reveals nothing: the pointer rests inside bounded tool/reasoning strips while they are being read, so a thumb painted across that text is noise — and the strip's edge fade already says that more content lies below. Scrolling an inner strip never lights up its ancestors' scrollbars either.

[`ScrollbarReveal.ts`](../../../desktop/src/renderer/ScrollbarReveal.ts) owns the `.scrollbar-visible` class and installs one capture-phase scroll listener for the document, so every scroll container — including ones mounted later — is covered without per-component wiring. [`scrollbars.css`](../../../desktop/src/renderer/styles/scrollbars.css) paints the thumb from `--scrollbar-ink`, a registered `<color>` property that drives the fade explicitly rather than relying on the engine's interpolation of `scrollbar-color`. The gutter stays reserved, so the message flow never shifts when a thumb arrives.

A controller that manages its own scroll node registers it with `markScrollbarRevealSelfManaged()`. The conversation viewport and every auto-follow container also scroll programmatically — following streamed content, adopting a resized viewport — and the global listener cannot tell those frames from a user gesture; without the exemption a follow would keep the thumb painted for the whole response. Terminals and editors keep a permanently visible thumb, because scroll position is part of what those surfaces show. `.scrollbar-hidden` removes the scrollbar entirely where the edge fade already carries the overflow signal.

## Scroll-edge fading

[`scroll-fade.css`](../../../desktop/src/renderer/styles/scroll-fade.css) provides opt-in fading for bounded tool/reasoning inspection strips, navigation lists, and horizontal card strips. Add the attribute to the existing scroll owner:

```tsx
<div className="existing-scroll-region" data-scroll-fade="compact" ref={scrollRef}>
  {content}
</div>
```

Use `compact` for dense inspection strips, an empty value for navigation lists, and `inline` for a horizontal strip such as the composer attachment tray, which fades its start and end edges instead. Keep ordinary clipping on primary reading surfaces such as messages, settings, and documents. Inputs, terminals, editors, image/PDF canvases, and wide content such as tables and code are not intended targets. Fixed headers, composers, and menus should remain outside the masked owner.

The utility uses self-scroll timelines and an alpha mask, with no overlay or React scroll updates. An edge fades only when more content lies beyond it; no overflow means no fade. Nested scroll owners remain independent, and each edge is capped at half the viewport. Unsupported engines, reduced motion, forced colors, and print fall back to ordinary clipping. Check for existing `animation` or `mask-image` declarations before opting in, because the utility owns both.

## Inspect the affected states

Check light and dark themes, default and large fonts, wide and narrow windows, empty and long content, and keyboard focus. Combine states that can coexist, such as selected, running, unread, hovered, disabled, and dragging. Look for clipping, overlaps, moving click targets, and labels displaced by hidden actions or placeholders.

For scrolling changes, inspect the top, middle, and bottom with both short and overflowing content. Append streaming content, scroll away from the bottom, close and reopen folds, switch sessions, and resize. Confirm follow/pause behavior, text selection, menus, and scrollbars remain usable. Report the conditions actually inspected; one screenshot or a passing unit suite is not full visual acceptance. For a change that touches shared components or tokens, run the UI tour and read its screenshots across the matrix.
