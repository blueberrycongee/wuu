# Desktop UI maintenance

Use the renderer's shared components and design variables when changing the desktop interface. Check the result in a real browser or Electron window: type checks and jsdom tests cannot establish readable spacing, working scroll effects, or a visible keyboard focus ring.

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

## Shared typography and geometry

[`base.css`](../../../desktop/src/renderer/styles/base.css) defines the renderer's base roles, including typography, colors, corners, focus, and elevation. [`spacing.css`](../../../desktop/src/renderer/styles/spacing.css) defines spacing roles, density boundaries, and minimum control sizes. Prefer these existing roles to new per-component constants.

Respect the user's separate UI and code font preferences. Let rows grow with their content, reserve space for trailing actions and status indicators, and align peer labels independently of whether a row is running or unread. Density changes whitespace rather than removing minimum target sizes; coarse pointers have larger control floors.

Compact menus use `--menu-inset`, `--menu-item-gap`, and `--menu-shell-radius`. The shell radius combines the inner radius with the inset to keep nested corners related. Panel and dialog overlays use their own radius role. Reusing one numeric radius on every padded layer does not produce the same geometry. Click-open overlay cards — context menus, permission pickers, and select panels — use `--font-menu` (one step below `--font-ui`) for item labels, with `--weight-medium`. Group labels and secondary hints use `--font-xs`. Compact composer chips and triggers share `--font-sm` with that overlay step. Question cards above the composer stay on `--font-ui`; they are reading surfaces.

Public plugin theme tokens are a smaller contract than all internal CSS variables. Consult the [theme reference](../customize/theme-surface-matrix.md) before exposing a new token or telling plugin authors to depend on an internal variable.

## Background images

Preview the global background with `/dev/three-pane/?background`; omit `background` to check the same image across the three panes, and add `empty` to check the empty-session canvas and the dock strip under the composer. Import and processed images stay in the local desktop profile, and raster work runs in a worker rather than during layout. Only main canvases reveal the image; menus, inputs, editors, and overlapping drawers retain their theme surfaces.

Plugin page roots should leave the canvas to their host instead of painting another opaque canvas. This lets primary, settings, and workspace pages share the wallpaper while overlay and auxiliary hosts retain their solid surfaces. Preview the real Automation plugin with `/dev/automation/?region=primary`; `workspace`, `settings`, `overlay`, and `auxiliary` exercise the other containers.

With Vite running on port 5189, run `npm --prefix desktop run test:e2e:background-image` (or set `WUU_FIXTURE_ORIGIN` for another port). It uses an isolated profile to check import failures, transaction rollback, effects, persistence, cross-window updates, and the bundled worker under the production CSP. Rendered pixel checks cover the real plugin across those containers, including opaque overlays. It also captures light/dark, default/large-font, wide/narrow previews in `artifacts/background-image/`. Review those captures separately; pixel and geometry checks do not establish visual acceptance.

## Sidebar folds

The Collaboration, Pinned, Folders, and Workspace headings support mouse drag reordering. The desktop profile remembers their order; adding Collaboration to an older profile preserves its existing group order. Run `cd desktop && npx electron scripts/sidebar-collaboration-sort-e2e.cjs` for isolated rendered drag, cancellation, and reload checks. It also captures theme/font variants in `artifacts/sidebar-collaboration-sort/` for separate visual review.

Use `SidebarCollapseBody` for sidebar sections and nested groups. It animates intrinsic height and the heading gap together, retains rows until closing finishes, and prevents hidden rows from receiving focus. Avoid inherited measured-height variables or descendant animation rules that change a nested fold when its parent toggles.

Preview `/dev/sidebar-collapse/` with optional `theme=dark`, `size=20`, and `width=240` query parameters. Run `npm --prefix desktop run test:e2e:sidebar-collapse` for Electron geometry checks covering nested folds, reversals, changing content, and reduced motion. These checks do not replace visual acceptance.

## Motion

Motion tokens live in one place: the ladder in [`base.css`](../../../desktop/src/renderer/styles/base.css). Frame-driven code never copies a duration or a curve out of that ladder. [`motion.ts`](../../../desktop/src/renderer/motion.ts) is the only JS bridge — `motionDurationMs` reads a duration token, `motionEasing` evaluates the cubic-bezier a token names, and `messageMotionTime` is the shared document clock that frame loops and WAAPI entrances both read. Keep it that way: a hand-rolled `1 - (1 - p) ** 3` next to a `cubic-bezier()` token can drift away from the transition it was meant to match.

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

[`scroll-fade.css`](../../../desktop/src/renderer/styles/scroll-fade.css) provides opt-in fading for bounded tool/reasoning inspection strips and navigation lists. Add the attribute to the existing vertical scroll owner:

```tsx
<div className="existing-scroll-region" data-scroll-fade="compact" ref={scrollRef}>
  {content}
</div>
```

Use `compact` for dense inspection strips and an empty value for navigation lists. Keep ordinary clipping on primary reading surfaces such as messages, settings, and documents. Inputs, terminals, editors, image/PDF canvases, and horizontal scrollers are not intended targets. Fixed headers, composers, and menus should remain outside the masked owner.

The utility uses self-scroll timelines and an alpha mask, with no overlay or React scroll updates. An edge fades only when more content lies beyond it; no overflow means no fade. Nested scroll owners remain independent, and each edge is capped at half the viewport. Unsupported engines, reduced motion, forced colors, and print fall back to ordinary clipping. Check for existing `animation` or `mask-image` declarations before opting in, because the utility owns both.

## Inspect the affected states

Check light and dark themes, default and large fonts, wide and narrow windows, empty and long content, and keyboard focus. Combine states that can coexist, such as selected, running, unread, hovered, disabled, and dragging. Look for clipping, overlaps, moving click targets, and labels displaced by hidden actions or placeholders.

For scrolling changes, inspect the top, middle, and bottom with both short and overflowing content. Append streaming content, scroll away from the bottom, close and reopen folds, switch sessions, and resize. Confirm follow/pause behavior, text selection, menus, and scrollbars remain usable. Report the conditions actually inspected; one screenshot or a passing unit suite is not full visual acceptance.
