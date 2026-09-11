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

All recipes live in `src/renderer/styles/base.css`. Dark mode changes only their
black opacity in `theme.css`; shadows must not derive from text/ink colors.

| Token | Use |
| --- | --- |
| `--shadow-soft` | Small controls |
| `--shadow-composer` | Input frames with a faint ambient shadow around the outer edge |
| `--shadow-card` | Cards, tooltips, floating status controls |
| `--shadow-pop` / `--menu-shadow` | Menus, popovers, dragged items |
| `--shadow-modal` | Dialogs, command/search panels, immersive previews |
| `--shadow-tray` | Attached trays that open upwards |
| `--shadow-drawer-left/right` | Shadow cast toward the named side by an edge drawer |

In-flow surfaces can use `none`. Existing borders define edges. Keep focus,
selection and inset keylines separate from elevation. Hover and expansion do
not need a larger shadow. Use a shared recipe instead of component-specific
blur/opacity values. The public `--wuu-elevation-panel` and
`--wuu-elevation-overlay` overrides continue to control their respective groups,
including `none`. After changing consumers, regenerate the theme surface matrix
with `make generate-theme-surface-matrix`.
