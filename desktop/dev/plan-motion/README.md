# Plan progress motion preview

This fixture renders the production `ConversationStatusCluster` and styles with
local TODO data in Electron. It does not call the Go core, load user settings or
run an agent. The surrounding message and composer are preview scenery.

## Interaction

The capsule's ring represents completed items divided by total items. It moves
only when those values change. Each row keeps the same SVG: a pending circle
becomes a rotating open ring while running, then fades into a drawn check when
completed. Revisions can reverse the transition. Initial completed items render
settled, and opening the card does not replay completion. A fully completed or
empty plan disappears according to the existing product behavior.

The progress/check transitions use Wuu's `--motion-slow` (280 ms) and `--ease-out`;
the running marker uses `--motion-spin` (900 ms). These are Wuu adaptations, not
measurements of the reference. The marker pauses while the card is closed. Both
OS and application reduced-motion preferences disable the loop and transitions.
Pointer hover and keyboard focus open the same card; the row anchors the card so
adjacent capsules cannot push it outside a narrow window.

## Reference and rights

Inspired by Motionbook's [Agent plan](https://github.com/blueberrycongee/motionbook/tree/main/examples/agent-plan),
original concept by [@jeetnirnejak](https://x.com/jeetnirnejak/status/2051294751694192794).
The inspected reference shows task-local running and completion markers. This
adaptation retains that spatial continuity and adds a count-driven capsule ring.
Wuu retains its own typography, monochrome palette and compact status layout.
The SVG geometry and CSS here are independently authored; no reference code,
fonts, footage or other assets are incorporated. See the reference's
[provenance](https://github.com/blueberrycongee/motionbook/blob/main/examples/agent-plan/PROVENANCE.md)
for its separate rights and evidence limits.

## Run and capture

From `desktop/`, in separate terminals:

```sh
npx vite --config dev/plan-motion/vite.config.ts
./node_modules/.bin/electron dev/plan-motion/capture.cjs
```

Open `http://127.0.0.1:5216/dev/plan-motion/` for manual interaction. Query options:
`theme=dark`, `size=20`, `motion=reduce`, `long`, and `crowded`.

The manual review script writes `artifacts/plan-motion/results.json`, screenshots
and renderer frame captures. It covers light/dark, 14/20 px, 390/900 px widths,
long scrollable plans, keyboard focus, pointer travel into the card, progress
updates, fast reversal, empty/completed plans, closed-loop suspension and both
reduced-motion settings. It checks live geometry and state, not stylesheet text
or golden images. This is component runtime evidence, not full agent E2E coverage.

From the repository root, encode a sampled renderer preview:

```sh
ffmpeg -y -framerate 30 -i artifacts/plan-motion/frames/%03d.png \
  -vf 'crop=900:470:310:590,scale=720:-2' \
  -c:v libx264 -pix_fmt yuv420p artifacts/plan-motion/preview.mp4
```

The crop assumes the capture machine's 2× display scale. Captures wait two
animation frames per sample, plus screenshot overhead. Fixed-rate playback is a
visual demonstration, not a timing or performance measurement; reproduce the
live fixture to assess timing. No offline recreation drives the preview.
