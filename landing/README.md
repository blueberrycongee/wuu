# Wuu website

The marketing site is plain HTML and CSS. `index.html` is the product homepage. A small progressive enhancement animates the mascot: it follows the pointer, hops and wiggles when left alone, and the homepage ball splits into a lingering trio on double-click — or on its own when bored — and merges back the same way. Content and navigation work without JavaScript; no external fonts are requested.

Preview from the repository root:

```sh
python3 -m http.server 4173 --bind 127.0.0.1 --directory landing
```

The documentation site renders these same marketing sources. Its preparation script copies the current brand assets, provider logos, blog images and stylesheets. Validate the complete hosted output with:

```sh
CI=true npm --prefix docs-site run build
```

The homepage uses an explicitly labelled workflow illustration, not a captured running session. Download links lead to GitHub Releases so version and packaging details remain current. Desktop application UI is outside this site's scope.

The site includes bilingual product and blog pages. Marketing HTML files are automatically exposed as matching directory routes by the documentation build. The blog page lists the published articles. Keep unpublished articles and illustrations outside this repository and its preview server root. A Git ignore or omission from navigation is not a confidentiality boundary. When an article is ready for public review, add both languages to the landing root, put reviewed images under `assets/blog/`, and link it from the blog page and site navigation. Product, blog, documentation, and download navigation expand on hover, click, or keyboard activation.

The contributor changing a product claim updates both languages and checks it against the current app or source. Preserve image provenance and licenses; use synthetic illustrations instead of real sessions, and review metadata before adding attachments. Temporary generation output and presentation decks do not belong here. See [documentation maintenance](../docs/README.md).

## README media

`assets/readme/` holds the screenshots and demo animations used by the repository READMEs. They are captured from the real desktop app and Go core, driven by a scripted local model provider through synthetic example projects, so no account, credential, or personal session appears in them. The generator draws the macOS traffic lights and sets every capture on a blurred backdrop from `assets/brand/` as a rounded window or panel with an edge and a drop shadow. The demos are animated WebP: a camera moves in on the prompt, the agent's work and the diff, with motion blur while it moves. The documentation site does not publish these files.

Regenerate them after a release changes what they show. Build the app being described, usually a checkout of the release tag, then run the generator from this checkout. It needs a display (use `xvfb-run` on Linux), `ffmpeg`, and `img2webp` and `webpmux` from libwebp:

```sh
(cd <app>/desktop && npm run build:core && npm run build)
node desktop/scripts/generate-readme-media.cjs <app>/desktop
```

The Computer Use demos, `cua-en.webp` and `cua-zh.webp`, are captured separately on macOS. The generator builds the disposable AppKit invoice app in `desktop/scripts/fixtures/` and records the real desktop window while the Computer Use helper edits and saves that app. It therefore needs Screen Recording and Accessibility permission, plus `swiftc`, `screencapture`, `ffmpeg`, and libwebp:

```sh
node desktop/scripts/generate-cua-readme-media.cjs <app>/desktop
```

Review both languages and both hero themes before committing new images.
