# Wuu promo

A 64-second wordless film starring the app-icon mascot. Every frame is drawn
on a 1920 × 1080 canvas as a pure function of time, so the preview and the
exported video match frame for frame.

| Time | Shot | File |
| --- | --- | --- |
| 0–5 s | Eyes open, lights come on, then Wuu moves continuously from the icon pose to the desk corner | `bookends.ts` |
| 5–31 s | Four harnesses in four windows wear Wuu out; Wuu opens one app, every window hops into its sidebar, and each session keeps its own engine. The setting dissolves around a matched Wuu pose | `desk.ts` |
| 31–59 s | A rocket too big to build alone; six sessions build it together, one helps another, then the camera follows liftoff into a porthole close-up | `stage.ts` |
| 59–64 s | The same Wuu portrait carries through as the rocket fades, then folds into the app icon and holds | `bookends.ts` |

`film.ts` sequences the shots. `art.ts` uses the production icon source,
blobatar's canonical silhouettes and palette, the desktop avatar hue wheel,
and engine logos. Characters retain different shapes and colours, with flat
fills and shared eye proportions. The film uses charcoal, off-white and muted
sage for the set and props, with no simulated surface highlights. A simple
studio floor replaces the outdoor scenery; the clock alone conveys time.
The plan clears before collaboration begins, and the session windows share
one host treatment. Eye-lines and deliberate holds carry the handoffs instead
of sparkle trails, impact bursts or continuous bouncing.

`assets/rocket.svg` is the original, editable source for all six rocket parts,
including their colour fields, outlines and assembly bounds. Canvas rendering uses
the same SVG groups for the blueprint, in-session work, assembly and launch.
Preview and capture wait for those images to decode before rendering a frame.
`cast.ts` holds the shared cast, camera and props, and `motion.ts` holds easing
and expression helpers. No remote assets are loaded.

Preview from `desktop`:

```sh
./node_modules/.bin/vite dev/promo --host 127.0.0.1 --port 5179
```

Space plays or pauses, the arrow keys step one frame (Shift: one second), and
`?t=42.5` opens at a moment.

Render from the repository root. Encoding uses AVFoundation through `swift`, so
the export runs on macOS without ffmpeg:

```sh
desktop/node_modules/.bin/electron desktop/dev/promo/capture.cjs
```

The video is written to `desktop/out-dev/promo/wuu-promo.mp4` (H.264, 60 fps,
no audio). `PROMO_RANGE="31,59"` renders part of the film, `PROMO_STILLS` writes
single frames, and `PROMO_SHEETS` writes 3 × 3 contact sheets for review; see
the header of `capture.cjs`.
