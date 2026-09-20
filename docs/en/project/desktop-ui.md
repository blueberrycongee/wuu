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

Compact menus use `--menu-inset`, `--menu-item-gap`, and `--menu-shell-radius`. The shell radius combines the inner radius with the inset to keep nested corners related. Panel and dialog overlays use their own radius role. Reusing one numeric radius on every padded layer does not produce the same geometry.

Public plugin theme tokens are a smaller contract than all internal CSS variables. Consult the [theme reference](../customize/theme-surface-matrix.md) before exposing a new token or telling plugin authors to depend on an internal variable.

## Sidebar folds

Use `SidebarCollapseBody` for sidebar sections and nested groups. It animates intrinsic height and the heading gap together, retains rows until closing finishes, and prevents hidden rows from receiving focus. Avoid inherited measured-height variables or descendant animation rules that change a nested fold when its parent toggles.

Preview `/dev/sidebar-collapse/` with optional `theme=dark`, `size=20`, and `width=240` query parameters. Run `npm --prefix desktop run test:e2e:sidebar-collapse` for Electron geometry checks covering nested folds, reversals, changing content, and reduced motion. These checks do not replace visual acceptance.

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
