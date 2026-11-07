# Deep Field

A canvas-placement shell skin example: your own picture (or the bundled
space artwork) rendered as a pixel screen behind the main canvas, a solid
frame and sidebar, frosted floating cards, paired opaque dark and light themes,
and a dashboard-style welcome view with recent sessions.

    wuu plugin install ./examples/plugins/deep-field

Select **Settings → Appearance → Shell skin → Deep Field**, then
**Background image → Choose image** to use your own picture; **Remove** returns
to the bundled artwork. The matching **Deep Field** / **Deep Field Light**
themes are optional. The welcome dashboard appears only while the Deep Field
skin is selected; with any other skin the native welcome is unchanged.

## What this demonstrates

- `contributes.skins` with `placement: "canvas"`, `userImage: true`, `veil`,
  and a square `dots` screen. The host samples one color per cell from the
  artwork, so any picture turns into the same pixel texture. Each destination
  picks how much of it shows through the veil.
- `contributes.themes`: opaque dark and light theme tokens that pair with the
  skin. The host derives translucency from canvas placement, not from theme
  colors.
- `registerUIContribution` wrapping the `wuu.default-conversation/welcome`
  slot. While this skin is selected (read through
  `settingsControls.subscribeShellSkins`), it arranges `context.greeting` and
  `context.usage` next to an `api.ui.Card` listing recent conversations, with
  `api.showNotice` for failures and `api.registerLocale` for copy.
- Disabling the plugin restores the default frame and welcome. A saved
  background image stays saved and returns to the default frame.
