# Wuu brand

Source files, generators and assets for the Wuu brand system. The rules are summarised in
[the brand guidelines page](../docs/en/project/brand.md) ([简体中文](../docs/zh-cn/project/brand.md));
the full manual is [`manual/index.html`](manual/index.html) — open it in a browser.

This directory does not change the product. Adopting the identity in the desktop app, the
website or the documentation site is separate work that needs its own review.

## Layout

| Path | Contents |
| --- | --- |
| `tokens/tokens.json` | Source of truth: colours, type, motion, ball, wordmark and lockup geometry |
| `tokens/wuu-brand.css` | Generated CSS custom properties (`--wuu-brand-*`) |
| `src/geometry.mjs` | Construction of the ball, wordmark and lockups (SVG and PNG), plus the icon studies shown in the exploration pages |
| `src/build-assets.mjs` | Writes `assets/logo`, `assets/favicon` and the CSS |
| `src/templates.mjs` | Social image, README banner, release card and installer background templates |
| `src/manual/` | Manual pages; `src/build-manual.mjs` writes `manual/index.html` |
| `scripts/render.cjs` | Renders the PDF, page images, documentation previews and template PNGs |
| `assets/motion/` | Ball pose and motion reference (`wuu-ball.js`) and an interactive demo |
| `manual/reference/` | Captures of the current product and website used in the audit pages |
| `fonts/` | Hanken Grotesk and Fragment Mono with their licences |

## Regenerate

Use the Node version in [`.node-version`](../.node-version).

```bash
npm ci --prefix brand
npm --prefix brand run build      # logo, favicon, CSS tokens, manual HTML
npm ci --prefix desktop           # provides Electron for rendering
npm --prefix brand run render     # PDF and pages in artifacts/brand/, docs previews, template PNGs
```

Rendering needs Source Han Sans SC or Noto Sans CJK SC installed; the script stops if it is
missing. On Linux without a display, run the render command under `xvfb-run`. The PDF and page
images go to the ignored `artifacts/brand/` directory; attach them to a release or share them
directly rather than committing them.

The manual draws UI icons from `desktop/src/shared/iconArtwork.ts`, so it needs Node 22 or newer
(type stripping). Edit the sources, never the generated files: `manual/index.html`,
`tokens/wuu-brand.css`, `assets/**/*.svg|png` and `docs/en/assets/brand/*.png`.

`assets/motion/wuu-ball.js` carries its own copy of the optical eye sets so it runs from
`file://`; `src/geometry.mjs` refuses to build when that copy differs from `tokens.json`.

## Choosing an asset

- App icon: keep the approved artwork in `assets/app-icon-source.*` at the repository root and
  its generated files (`assets/app-icon.png`, `desktop/build/icon.*`). The brand does not redraw
  it; the manual references those files directly.

- Horizontal lockup: `wuu-lockup-horizontal-*.svg` above a 28 px x-height, `-small-` from 15 to
  28 px, `-micro-` at 14 px and below.
- Symbol: `wuu-symbol-*.svg` above 40 px, `-small-` from 21 to 40 px, `-micro-` at 20 px and
  below. Favicons already use the micro eyes.
- Colourways: `ink` on light grounds, `paper` on dark grounds, `black` and `white` are one-colour
  knockouts for print and partner backgrounds.

## Licences and provenance

- The ball, wordmark and all generated assets are original work in this repository and ship
  under the repository licence (MIT). MIT grants no trademark rights; terms for using the
  brand assets have not been set and need maintainer confirmation.
- Hanken Grotesk (The Hanken Grotesk Project Authors) and Fragment Mono (The Fragment-Mono
  Project Authors) are distributed unmodified under the SIL Open Font License 1.1; see the
  `OFL.txt` next to each font. Source: the google/fonts repository.
- Source Han Sans SC / Noto Sans CJK SC (Adobe, SIL OFL 1.1) is required but not distributed here.
- `manual/reference/` images were captured at commit ef1713674 from the development previews in
  `desktop/dev/` (synthetic data) and the public website in `landing/`. They document the brand
  before this redesign and are not brand assets.
- The current product avatars use blobatar geometry (MIT); the ball defined here does not.

## Not verified

No trademark search, user research, review of whether the kept app icon needs a variant below
32 px (its motion marks get dense there), print proofing or CMYK values, rendering on Windows or macOS (checked in
Chromium on Linux with Noto Sans CJK SC), or colour-vision simulation of the agent colours.
