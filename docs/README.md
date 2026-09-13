# wuu documentation

[简体中文](zh-cn/index.md) · [English](en/index.md)

Start with the introduction and quick start. Add detail when a reader needs it to finish a task. Use plain language, keep pages short, and check instructions against the current app or code.

Write standard Markdown with relative links. Keep English and Chinese pages aligned; label links that lead to another language. Only pages listed in [site.json](site.json) are published. Keep research and future plans out of the public docs.

## Preview and build

Use the Node version in [`.node-version`](../.node-version):

```bash
npm ci --prefix docs-site
make docs-dev
```

Run `make build-docs` before submitting changes. The site uses Astro Starlight in `docs-site/`; GitHub Actions builds and deploys documentation changes on `main` to GitHub Pages.

The `customize/theme-surface-matrix.md` pages are generated references. Update them with `make generate-theme-surface-matrix`, not by hand.
