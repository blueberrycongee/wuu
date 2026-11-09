# Deep Field

A declarative canvas-placement shell skin: your own picture, or the bundled
space artwork, rendered as a pixel screen behind the main canvas, with a
solid frame and sidebar and frosted floating panels. No runtime or desktop
code.

    wuu plugin install ./examples/plugins/deep-field

Select **Settings → Appearance → Shell skin → Deep Field**. The
**Background image** row below it accepts your own picture; **Remove** returns
to the bundled artwork. Content colors keep following the **Color palette**.

## What this demonstrates

- `contributes.skins` with `placement: "canvas"`, `userImage: true`, `veil`,
  and a square `dots` screen. The host samples one color per cell from the
  artwork, so any picture turns into the same pixel texture, and each
  destination picks how much of it shows through the veil.
- The scope of a skin: it styles the frame and the background behind your
  work, and leaves content colors, layout, and the welcome view unchanged.
- Disabling the plugin restores the default frame. A saved background image
  stays saved and returns on the default frame.
