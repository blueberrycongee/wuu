# Wuu promo

A 64-second wordless film starring the app-icon mascot. Every frame is drawn
on a 1920 × 1080 canvas as a pure function of time, so the preview and the
exported video match frame for frame.

| Time | Shot | File |
| --- | --- | --- |
| 0–5 s | Eyes open in the dark, the lights come on, Wuu strikes the icon pose | `bookends.ts` |
| 5–31 s | Four harnesses in four windows wear Wuu out; Wuu opens one app, every window hops into its sidebar, and each session keeps its own engine | `desk.ts` |
| 31–59 s | A rocket too big to build alone; Wuu coordinates six sessions at once, one gets stuck and another helps, liftoff | `stage.ts` |
| 59–64 s | Wuu lands and the scene folds into the app icon | `bookends.ts` |

`film.ts` sequences the shots. `art.ts` draws the characters and props from
the production icon source, blobatar body geometry and engine logos. `cast.ts`
holds the shared cast, camera and props, and `motion.ts` holds the easing and
expression helpers.

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
