# Desktop UI maintenance

Use the shared renderer components and design variables for equivalent controls.
The [contributor instructions](../../../AGENTS.md#ui-design-and-visual-acceptance)
define typography, spacing, accessibility and actual-rendering acceptance.
Update previews and documentation with the behavior they demonstrate; do not keep
branch-specific change journals as current design specifications.

## Reusable previews

The [desktop preview directory](../../../desktop/dev/) contains development-only
entries with synthetic data. These support repeatable checks of real components
and states; they are not product entry points or evidence that a release passed.
Run `npx vite --host 127.0.0.1` from `desktop/` and open the relevant `/dev/` path,
using the port Vite reports. The [mascot preview](../../../desktop/dev/mascot/README.md)
uses `npm run lab:mascot`; the isolated onboarding window uses
`npm run dev:onboarding`. Neither needs real account credentials.

Keep previews useful when changing their components. Store temporary captures in
ignored artifact directories, not beside the fixture source. Inspect light/dark
themes, default/large fonts, narrow/wide windows, long/empty content, scrolling,
menus and keyboard focus. Automated component tests do not replace rendered UI
inspection; report which conditions were actually checked.

## Nested corners

Use the radius and menu variables from
[base.css](../../../desktop/src/renderer/styles/base.css). Concentric nested
corners generally need an outer radius close to the inner radius plus the inset;
using the same radius on both padded layers does not make them concentric.
Prefer the existing compact-menu recipe to new per-component constants. Check
the rendered relationship rather than treating the formula as visual acceptance.

## Scroll-edge fading

[scroll-fade.css](../../../desktop/src/renderer/styles/scroll-fade.css) is opt-in
for bounded tool/reasoning inspection strips and navigation lists. Put the
attribute on the existing vertical scroll owner:

```tsx
<div className="existing-scroll-region" data-scroll-fade="compact" ref={scrollRef}>
  {content}
</div>
```

Use `compact` for dense inspection strips and an empty value for navigation lists.
Primary reading surfaces, including message streams, settings and documents, keep
ordinary clipping. Do not apply the mask to input fields, terminals, editors,
image/PDF canvases or horizontal scrollers. Keep fixed headers, composers and
menus outside the masked scroll owner. The utility owns `animation` and
`mask-image`; check for conflicts before opting in.

Native self-scroll timelines fade only edges with more content beyond them;
non-overflowing regions do not fade, and tiny viewports cap each edge at half the
viewport. Nested scroll owners remain independent. The alpha mask adds no input
overlay and preserves native scrolling, selection and auto-follow. Unsupported
engines, reduced motion, forced colors and print use ordinary clipping.

Inspect short and overflowing content at the top, middle and bottom. Stream into
an open fold, scroll away, collapse/reopen it, switch sessions and resize. Verify
follow/pause behavior, readable text, keyboard navigation, usable menus and
scrollbars, and no fade at primary reading-surface edges. jsdom does not evaluate
scroll timelines, so passing unit tests is not visual approval.
