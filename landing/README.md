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
