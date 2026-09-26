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

## Conversation image previews

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

Images, videos, PDFs, and folded long pastes wait in one tray that slides out
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

## Settings pages

Settings groups pages by task: **Agents & models** (model providers, agents, runtime), **App** (general, appearance), **Extensions** (MCP servers and plugin pages), and **Data** (usage, archive). Page IDs are part of the plugin settings snapshot, so they stay stable when a label or group changes; the runtime page keeps the `advanced` ID.

Every page shares one column measured in UI text, so the title stays in place while navigating and a label stays within a glance of its control at large sizes. A page opens with its title, an optional line that states a non-obvious scope, and page actions beside the title. Section titles are the only other semibold text; rows stay regular inside one bordered group. Keep a row description only for units, constraints, or consequences the title does not already state.

Status labels carry their meaning in text; the dot beside them repeats the tone for scanning. Model providers and agents expand in place under their own row. Rows without a disclosure reserve its footprint so status labels end on one axis. The titlebar gains its hairline only after content scrolls beneath it.

Preview `/dev/settings/` with `page` set to a page ID, and optional `theme=dark`, `size=20`, `lang=en`, `rail=` (sidebar width), `collapsed`, `long`, and `empty` parameters. Providers, agents, MCP servers, usage, and archive rows are synthetic; nothing is saved.

## Extensions catalog

The Extensions page uses the settings page column, header, and groups. Plugins come first because their runtime and approval state may need a decision; official skills and your skills follow. Every row shows a mark, the name over a one-line description, and a trailing status or owning plugin, then a chevron that opens the plugin detail or skill preview. A search hides every group it leaves empty. In a narrow column the trailing label moves under the description.

The plugin detail repeats the row's status label, followed by source and grant scope. Permissions are grouped by capability, and all groups share one label column.

Preview `/dev/extensions/` with optional `theme=dark`, `size=20`, `lang=en`, `long`, and `empty` parameters. Skills and plugin packages are synthetic and cover every status tone; actions in the detail dialog change only the preview's state.

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

The Collaboration, Pinned, Folders, and Workspace headings support mouse drag reordering. The desktop profile remembers their order; adding Collaboration to an older profile preserves its existing group order. Run `cd desktop && npx electron scripts/sidebar-collaboration-sort-e2e.cjs` for isolated rendered drag, cancellation, and reload checks. It also captures theme/font variants in `artifacts/sidebar-collaboration-sort/` for separate visual review.

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

Expand includes all history; Collapse returns to that recent range without
closing the project. Both ranges, including conversations still being created,
scroll within eight rows of height measured by the shared font-responsive row
size. Short lists use only their content height. The history controls remain
outside the scroll area. Add `mode=history` to the preview URL for the real project
component. The same Electron check covers history expansion, read transitions,
inner scrolling, creating rows, and live font changes, and writes geometry JSON
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

Use a CSS transition from the ladder for motion whose geometry is already known: an entrance, a menu, a panel sliding to a fixed size, a hover wash. Reach for a frame-driven trajectory when the destination is only known while the motion runs — scrolling to content that is still arriving — or when something else keeps moving the target underneath it. Such motion needs no deadline and cannot restart; expressed as a transition it would have to be retargeted with a second transition, which is the seam this design removes. Keep a deliberate constant cadence as it is: a summary that reveals at about twelve characters per second is not settling, and a retained glide would turn it into a different animation. A follow that shares its deadline with the height transition of the content it is following — the collaboration receipt — keeps that deadline on purpose, so both finish together.

## Conversation disclosure scrolling

Opening or closing tool/reasoning details preserves the reader's scroll mode. A conversation following the latest content continues following through the height transition; a paused conversation keeps its reading position. Wheel, touch, keyboard scrolling, scrollbar dragging, and text selection take precedence over layout correction.

Sending a query reserves reading space below the bubble. Expanded details may temporarily occupy that space, but closing them restores what remains after actual response growth or deliberate browsing. A temporarily empty gap is not proof that the response has filled the reservation. Inspect repeated toggles while streaming, including a fold taller than the remaining gap and a session switch with the fold open.

Earlier-history paging inserts rows above the viewport. A paused reader's offset belongs to native scroll anchoring, so the manual prepend correction applies only while the offset still sits where the page was requested; adding the inserted height on top of anchoring moves the whole stream down by that height the moment the page arrives, which reads as a jump.

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

For scrolling changes, inspect the top, middle, and bottom with both short and overflowing content. Append streaming content, scroll away from the bottom, close and reopen folds, switch sessions, and resize. Confirm follow/pause behavior, text selection, menus, and scrollbars remain usable. Report the conditions actually inspected; one screenshot or a passing unit suite is not full visual acceptance.
