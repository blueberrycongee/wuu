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

Display them at 120 × 120 px in the current empty states. Use 96 × 96 px or
64 × 64 px when a particularly narrow surface needs a more compact treatment.
They are decorative, so the surrounding title or description carries the
meaning. Do not use them as 16–24 px toolbar icons.
