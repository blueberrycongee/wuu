# Production annotation acceptance

This path exercises the ordinary desktop build. There is no alternate import
adapter, presentation variant or replacement preload in the PDF journey.

From the repository root:

```sh
npm ci --prefix desktop
(cd desktop && node_modules/.bin/electron-vite build)
go build -o /tmp/wuu-annotation-core ./cmd/wuu
WUU_DOCUMENT_CORE=/tmp/wuu-annotation-core \
WUU_DOCUMENT_COMMIT="$(git rev-parse HEAD)" \
WUU_DOCUMENT_OUTPUT="$PWD/desktop/out/e2e/annotation-acceptance/production" \
xvfb-run --auto-servernum --server-args='-screen 0 1600x1200x24' \
desktop/node_modules/.bin/electron --no-sandbox \
desktop/scripts/e2e/annotation-acceptance/run.mjs
WUU_E2E_OUTPUT="$PWD/desktop/out/e2e/annotation-acceptance/non-pdf" \
xvfb-run --auto-servernum --server-args='-screen 0 1600x1200x24' \
desktop/node_modules/.bin/electron --no-sandbox desktop/scripts/file-selection-e2e.cjs
node desktop/scripts/e2e/annotation-acceptance/evidence.mjs desktop/out/e2e/annotation-acceptance
```

The full document-context journey verifies immutable PDF provenance, workspace
replacement, renderer reload and split-conversation draft ownership with a real
Electron main/preload/renderer and Go core. It uses a disposable profile,
synthetic PDF and local synthetic provider. No installed account, credential,
external model or user document is used.

The additional eight scenarios cover light/dark, wide/compact and 14/18 px text.
They use native mouse drag/click and key input for selection, comment, unsent
reference, comment editing, source return, removal and cancellation. Later ranges
in the original document-context journey are established programmatically in the
rendered text layer. Composing-key checks dispatch DOM events; they do not verify
an operating-system IME. Persistent source highlighting is checked both as an
exact live range and as changed background pixels in captured PNGs.

Every saved image is captured from the actual Electron window. Two representative
journeys record real frames with timestamps; ffmpeg assembles them into GIFs
without synthesized or interpolated motion. The evidence assembler validates
frame hashes, decoded GIF diversity and duration. Screenshots still need visual
review; passing checks do not constitute user acceptance or native macOS testing.

The `Annotation acceptance` workflow also runs the existing non-PDF selection E2E
against the same production renderer. That separate regression fixture uses an
in-memory preload and is labeled independently from the full-app PDF journey.

The earlier current-style/candidate comparison is retained in Git history at
`83c8cf95f2db81f3744571c799905440372320c9`; no comparison switch ships in the app.
