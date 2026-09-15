# Scroll-edge fading (Desktop)

`desktop/src/renderer/styles/scroll-fade.css` is the shared, opt-in vertical
scroll treatment for **bounded inspection strips and navigation lists**.
Primary reading surfaces (message streams, settings and documents) keep
ordinary clipping: fading those large areas makes the content look obscured
rather than helping users discover a small nested scroll region.
Put `data-scroll-fade` on the **existing scroll owner**:

```tsx
<div className="existing-scroll-region" data-scroll-fade="compact" ref={scrollRef}>
  {content}
</div>
```

Use `compact` (12px) for dense inspection strips and an empty value (16px) for
navigation and local lists. A local `--scroll-fade-size`
override is possible but should be exceptional. There is no React wrapper,
event listener, observer, dependency, or change to scroll ownership.

## Behavior

Two native CSS `scroll(self y)` timelines animate non-inheriting mask lengths.
The fade grows only over the first/last fade-sized number of pixels, so a
long list does not produce a long, barely visible ramp. Top means content
above; bottom means content below. At either boundary that edge's length is
zero. No overflow means an inactive timeline and both lengths stay zero.
Tiny viewports cap each fade at half the viewport. Nested folds own their
own timelines, including when content grows, is replaced, or is reopened.

The alpha mask uses no surface color, so it also works on the translucent
sidebar and in light/dark themes. It adds no hit-testing overlay. Native
scrollbars, selection, keyboard scrolling and existing auto-follow handlers
remain in charge; the mask is a paint effect, not a new input surface.

The desktop's Electron 44 supports these CSS features. Unsupported web
engines keep ordinary clipping via `@supports`; there is deliberately no JS
polyfill. Reduced-motion (OS or the app's Appearance setting), forced-colors
and print turn the effect off.

## Adopted surfaces

| Area | Scroll owner | Size |
| --- | --- | --- |
| Aggregated tool calls / compaction details | `.process-surface-body` | compact |
| Standalone reasoning | `.turn-reasoning-scroll` | compact |
| Harness sidebar / unread view | `.sidebar-main`, `.sidebar-unread-view` | default |
| Collaboration sidebar / compact navigation | `.collaboration-sidebar-main`, `.mobile-sidebar-scroll` | default |
| Channel session list | `.channel-sessions-body` | default |

Main, split, side-thread and channel message streams, session inspector
history, the catalog and settings content intentionally have **no** fade.
Tool/reasoning folds inside a message stream still keep their local fade.

Do not blanket-apply to `overflow: hidden`, ellipsized labels, input fields,
terminals, Monaco, PDF/image canvases, or horizontal tab/code scrollers.
Do not mask an outer panel containing a fixed header, composer, or menu;
keep those siblings outside the scroll owner and floating menus in portals.
The utility owns `animation` and `mask-image` on its node: inspect existing
animations/masks before opting in. Removing the attribute opts out.

## Verification

`scrollFade.test.ts` checks the CSS contract and reading-surface exclusions;
component tests check adoption
on scroll owners and keep existing auto-follow coverage. jsdom cannot
evaluate scroll timelines, so these tests are **not** a visual approval.
The user owns final inspection in the current Desktop runtime:

- Short content: no fading. Long content: bottom only at the top, both in
  the middle, top only at the bottom; first/last rows remain fully readable.
- Stream into an open tool fold, scroll away, collapse/reopen it, switch
  sessions, and resize the window. Existing follow/pause behavior must hold.
- Check both sidebar views, light/dark themes, and compact widths. Headers,
  composers, context menus and scrollbars must stay usable.
- Main/split/side-thread/channel message streams and settings must not fade
  at their viewport edges; expanded tool folds should still fade locally.
- Check text selection and keyboard navigation, then reduced motion and
  high contrast (no fade).
