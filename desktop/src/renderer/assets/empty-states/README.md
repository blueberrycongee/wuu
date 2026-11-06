# Empty-state illustrations

Four original SVGs, each with a separately drawn dark-theme variant. The
renderer picks the variant from `data-theme` in `EmptyStateIllustration.tsx`.

They share a 192 px canvas, one 3.2 px ink outline, flat paper and surface
fills, and a single clay accent. Light and dark variants use separate palettes
instead of filters so each theme keeps its intended contrast.

- `files` — an empty workspace file tree, or no file selected.
- `automation` — no scheduled automations.
- `plugins` — no plugins or skills.
- `connection` — no model service or external engine connected.

Display them at 160 × 160 px when the illustration is the only visible
empty-state content. Use 120 × 120 px or 96 × 96 px when a narrow surface
needs a more compact treatment. They are decorative, so the surrounding title
or description carries the meaning and may stay available to assistive
technology only. Do not use them as 16–24 px toolbar icons.
