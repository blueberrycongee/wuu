# Vendored Blobatar

Wuu vendors Blobatar because the mascot is rendered at 24–32 px and needs a
larger shared eye footprint and coherent face-sphere perspective than upstream
`v0.2.0` provides.

- Upstream: https://github.com/Alain00/blobatar (MIT, see `LICENSE`)
- Baseline: `v0.2.0`, plus Wuu changes: enlarged shared eye geometry, and a
  `perspective` option that projects the eye pair onto a turned sphere —
  positions, foreshortening, surface rotation, and a per-path bend, so the
  eyes read as painted on a ball rather than taped to a disc.

This is vendored **as source**, not as a tarball: `package.json`'s `exports`
point at `src/*.ts` and the desktop renderer bundles the TypeScript directly
through the `file:vendor/blobatar` dependency. There is no dist and no build
step here — editing `src/` and restarting the desktop dev build is the whole
integration loop. Package managers that copy local dependencies must refresh
that copy too; restart Vite with `--force` after changing vendored modules to
discard prebundled dependencies.

## Wuu shape contract

`SHAPES` from `blobatar/blob` is the shared selector catalogue: circle, rounded
square, horizontal capsule, rounded triangle, and rounded diamond. Contours are
fixed across seeds, with per-shape optical sizing: tapered contours are wider
than the rounded square, and the diamond sits horizontally. The former irregular
contours and their body/decorative trait controls are retired. This deliberately
changes seeded silhouettes; persisted Wuu identities migrate known old shape
IDs while retaining color and supported headwear.

Layout exposes `body.path` as the painted contour and `face` as the inscribed
projection surface. Consumers drawing the body must use its path; recreating a
superellipse from its bounds loses the selected shape. Static and animated eye
projection both use `face`, so turns stay inside sloping triangle/diamond sides.

## Layout

- `src/` — the library. `test/` — its bun test suite (`bun test`).
- `demo/` — the mascot workbench. From `desktop/`, run `npm run lab:mascot`,
  then open http://localhost:3001/. It opens on the real product `WuuMascot`
  activity, accessory, and size states; the preserved low-level tuning view
  covers traits, expressions, and sphere perspective against a grid of seeds.
- `docs/` — design notes and ADRs. `CONTEXT.md` — the glossary; worth reading
  before changing anything.
- `scripts/probe-compose.ts` — manual browser gate (`bun run probe`, needs
  Chrome or Firefox) that checks a statically baked pose agrees with the CSS
  composition. Run it after touching `expression.ts` or `motion.css`.

The desktop's own typecheck and unit tests cover the import path end to end.
