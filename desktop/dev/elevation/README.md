# Elevation review

Start `cd desktop && npx vite --host 127.0.0.1` and open `/dev/elevation/`.
This development-only entry uses the production project picker and renderer
styles. Fixture CSS only positions samples and styles the review controls.
It is not a production build input.

Check light/dark themes, narrow windows, project search and selection, keyboard
focus, hover, and the public theme override toggle. Compare the menu edge and
composer against the surrounding canvas; neither should have a broad grey halo.
Focus rings must remain visible even when the theme disables elevation.

## Choosing a shadow

Recipes live in `src/renderer/styles/base.css`, with shared geometry for both
themes. Following [Beautiful Shadows](https://github.com/alexwidua/figma-beautiful-shadows),
a soft overhead light produces a close contact shadow followed by wider, fainter
layers. More elevated surfaces have a longer falloff, not a darker outer band.
`theme.css` adjusts opacity and adds faint inset highlights for dark surfaces.
Shadows must not derive from text/ink colors or become an outer glow.

| Token | Use |
| --- | --- |
| `--shadow-soft` | Small controls |
| `--shadow-message` | Reading surfaces with minimal lift from the canvas |
| `--shadow-composer` | Input frames with a faint contact shadow and soft falloff |
| `--shadow-card` | Cards, tooltips, floating status controls |
| `--shadow-pop` / `--menu-shadow` | Menus, popovers, dragged items |
| `--shadow-modal` | Dialogs, command/search panels, immersive previews |
| `--shadow-tray` | Attached trays that open upwards |
| `--shadow-drawer-left/right` | Shadow cast toward the named side by an edge drawer |

In-flow surfaces can use `none`. Existing borders define edges. Keep focus and
selection rings separate from elevation. Hover and expansion do
not need a larger shadow. Use a shared recipe instead of component-specific
blur/opacity values. The public `--wuu-elevation-panel` and
`--wuu-elevation-overlay` overrides continue to control their respective groups,
including `none`. After changing consumers, regenerate the theme surface matrix
with `make generate-theme-surface-matrix`.
