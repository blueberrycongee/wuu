# Deep Field

A canvas-placement shell skin example: dot-matrix space artwork inside the main
canvas, a solid frame and sidebar, frosted floating cards, paired opaque dark
and light themes, and a dashboard-style welcome view with recent sessions.

    wuu plugin install ./examples/plugins/deep-field

Select **Settings → Appearance → Deep Field** as the shell skin, and the
matching **Deep Field** / **Deep Field Light** themes. With no skin selected,
everything is unchanged.

## What this demonstrates

- `contributes.skins` with `placement: "canvas"`, `veil`, and `dots` — the
  artwork lives behind the main pane under a host dot screen, and each
  destination picks how much of it shows.
- `contributes.themes` — opaque dark and light theme tokens that pair with the
  skin; the host derives translucency from canvas placement, not from theme
  colors.
- `registerUIContribution` on the `wuu.default-conversation/welcome` slot — a
  custom dashboard arranging `context.greeting` and `context.usage` next to an
  `api.ui.Card` listing recent conversations (`api.showNotice` on failures,
  `api.registerLocale` for copy).
- Disabling the plugin restores the default frame artwork and the default
  welcome; nothing leaves residue behind.
