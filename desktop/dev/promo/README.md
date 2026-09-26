# Wuu promo

A 64-second film starring the app-icon mascot, told as a paper collage and a
comic page. Every frame is drawn on a 1920 × 1080 canvas as a pure function of
time, so the preview and the exported video match frame for frame.

| Beats (s) | Shot | File |
| --- | --- | --- |
| 0–6 (0–3.2) | In the dark a lamp clicks on; Wuu wakes and says hi | `desk.ts` |
| 6–24 (3.2–12.8) | The lights come up on a desk pasted with harness clippings. Each calls and the cursor answers, faster and faster | `desk.ts` |
| 24–32 (12.8–17.1) | The page splits into 2, 4, 8 and 16 panels until nothing keeps up | `desk.ts` |
| 32–36 (17.1–19.2) | One silent panel: a marble drops. Wuu thinks of scissors | `desk.ts` |
| 36–60 (19.2–32) | Wuu rides the scissors around every clipping and pastes them into one app, then switches sessions and adds one on another engine | `desk.ts` |
| 60–84 (32–44.8) | The cursor pulls down a job as big as the page. Wuu stacks, snips and deals eight copies of itself; the job tears into a 3 × 3 comic page and each copy is pasted over as a different harness | `crew.ts` |
| 84–108 (44.8–57.6) | Every session collages its part of the outline. One gets stuck and a neighbour leaps the gutter to help. The panels are stamped done, Wuu grows into the middle and the pieces close into a giant Wuu | `crew.ts` |
| 108–120 (57.6–64) | The page closes around the giant until it is the app icon, the name is pasted under it, and the lamp clicks off | `cover.ts` |

`film.ts` sequences the shots; every change of shot is a hard cut on a cue.
`paper.ts` is the collage kit: paper grain, cut-out stickers with a white
scissor margin and a hard shadow, torn edges, tape, Ben-Day dots, bursts,
speed and focus lines, ransom-note lettering and comic balloons. `art.ts`
draws the characters from the production icon source, blobatar's silhouettes
and palette, the desktop avatar hue wheel and the engine logos, now as paper
cut-outs. `cast.ts` holds the shared cast, camera and props, and `motion.ts`
the easing and expression helpers. No remote assets are loaded.

## One clock

`cues.json` is the sync contract for a score. It sets the tempo (112.5 BPM, so
a beat is exactly 32 frames at 60 fps) and lists every sounding event on the
beat grid with its kind: `cut`, `click` or `hit`. The shots read their times
from it through `timeline.ts`, so every cue falls on a frame boundary.

## Preview and render

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
no audio). `PROMO_RANGE="32,58"` renders part of the film, `PROMO_STILLS` writes
single frames, and `PROMO_SHEETS` writes 3 × 3 contact sheets for review; see
the header of `capture.cjs`.
