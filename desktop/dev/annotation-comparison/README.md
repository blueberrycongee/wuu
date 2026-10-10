# Annotation comparison

This developer-only configuration builds the full desktop app twice. `baseline`
keeps its current presentation; `refined` replaces only the selection menu,
comment input, draft quote card, and sent reference card imports. Both use the
same production main process, preload, PDF viewer, composer, core, and behavior.
The shipping build and user preferences have no comparison switch.

From the repository root, install the desktop dependencies and build a core:

```sh
npm ci --prefix desktop
go build -o /tmp/wuu-annotation-core ./cmd/wuu
for variant in baseline refined; do
  (cd desktop && WUU_ANNOTATION_VARIANT="$variant" node_modules/.bin/electron-vite build --config dev/annotation-comparison/electron.vite.config.ts)
done
```

Run the same real-app journey against each build in a graphical desktop or Xvfb:

```sh
for variant in baseline refined; do
  WUU_ANNOTATION_VARIANT="$variant" \
  WUU_DOCUMENT_BUILD_DIR="$PWD/desktop/out-dev/annotation-comparison/$variant" \
  WUU_DOCUMENT_CORE=/tmp/wuu-annotation-core \
  WUU_DOCUMENT_COMMIT="$(git rev-parse HEAD)" \
  WUU_DOCUMENT_OUTPUT="$PWD/desktop/out/e2e/annotation-comparison/$variant" \
  xvfb-run --auto-servernum --server-args='-screen 0 1600x1200x24' \
  desktop/node_modules/.bin/electron --no-sandbox \
  desktop/scripts/e2e/annotation-comparison/run.mjs
done
node desktop/scripts/e2e/annotation-comparison/compare.mjs desktop/out/e2e/annotation-comparison
```

The fixture uses a disposable profile, synthetic PDF and local synthetic model
provider. No installed account, credential, external model, or user document is
used. The full document-context journey still verifies immutable PDF provenance,
workspace replacement, renderer reload and split-conversation draft ownership.

The opt-in matrix adds light/dark, wide/compact and 14/18 px text scenarios. It
uses native mouse drag/click and key input for the main interaction; subsequent
full-journey ranges use the existing programmatic text-layer selection. IME
checks dispatch composing DOM events and do not claim operating-system IME
verification. Every saved image is captured from the actual Electron window.
Two representative journeys record actual frames with timestamps; `ffmpeg`
assembles them into GIFs without synthesized or interpolated frames. The review
page and JSON distinguish captured media from comparison labels/composition.

The `Annotation comparison` workflow runs this path independently of unit and
integration suites. Passing scripts and builds do not constitute visual review
or user acceptance. Inspect the captured menus, comment growth, card expansion,
source return, focus, clipping and panel/composer relationship before choosing a
presentation.
