# Heatmap idle scenes

From `desktop`, run `node_modules/.bin/vite dev/empty-home --host 127.0.0.1 --port 5188`.
The fixture renders the real greeting and usage card with synthetic data, without
the product bridge. Buttons play each scene immediately; leaving it alone also
exercises the production idle scheduler.

Query parameters: `theme=dark`, `font=20`, `empty`, and `long`. Resize the window
to inspect clipped weeks. The manual buttons bypass idle scheduling so individual
scenes can be inspected without waiting; use the unattended page for reduced
motion and input-interruption checks.

With the server running, `node_modules/.bin/electron dev/empty-home/capture.cjs`
plays every scene at two widths, in both themes, and at two font sizes. It
checks that each scene restores the heatmap, that input interrupts a scene,
that idle play recovers and rotates without repeats, and that reduced motion
suppresses it, then writes screenshots to `artifacts/empty-home`.
