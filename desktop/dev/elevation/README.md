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

Light recipes live in `src/renderer/styles/base.css`. Dark recipes in `theme.css`
use a 0.5px inset highlight for small controls and a 1px inset highlight for
other raised surfaces. Menus, dialogs and edge drawers also retain black ambient
shadows to separate overlapping content. Shadows must not derive from text/ink
colors or become an outer glow.

| Token | Use |
| --- | --- |
| `--shadow-soft` | Small controls |
| `--shadow-composer` | Input frames with a faint ambient edge in light mode and an inset highlight in dark mode |
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
