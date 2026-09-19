# wuu documentation

[简体中文](zh-cn/index.md) · [English](en/index.md)

Start with the introduction and quick start. Add detail when a reader needs it to finish a task. Use plain language, keep pages short, and check instructions against the current app or code.

Write standard Markdown with relative links. Keep English and Chinese pages aligned; label links that lead to another language. Only pages listed in [site.json](site.json) appear on the documentation site. **Every Git-tracked file is public**, whether it appears on the site or not. Private research, unpublished plans and draft articles belong outside the repository, not in an `archive/` or `private/` subdirectory.

## Maintenance

The contributor changing a feature owns its documentation update in the same change; reviewers check facts against code, configuration and build commands. Put user, development and protocol contracts in `en/` and `zh-cn/` and register them in `site.json`. English-only API and release references are explicitly labelled in the Chinese navigation. Module READMEs retain local setup and provenance, with links to those contracts. Marketing content belongs in `landing/`; reusable evaluation cases and reproducible evidence follow [evals/README.md](../evals/README.md).

Do not add branch journals, task checklists, raw generated reports or presentation bundles. Merge still-useful instructions into maintained pages and delete the working record. Historical module or vendored documentation must state its version or retirement date and replacement. Keep runtime prompts, skills, fixtures and license notices with their consuming code. Required generated assets need an identified generator and regeneration command; build output and temporary screenshots stay ignored and use synthetic data. Follow [Contributing](../CONTRIBUTING.md#documentation) for public-suitability review and external distribution.

`make docs-policy-check` checks tracked documentation placement and local Markdown references, including links to code and images. It does not treat ignored local files as published evidence, and does not replace human fact, licensing or sensitive-content review. `make build-docs` also checks the rendered site's local links and resources.

Before staging new pages or assets, include their repository-relative paths explicitly with `make docs-policy-check DOCS_NEW_FILES="docs/en/example.md docs/zh-cn/example.md"`. Runtime instruction examples and vendored historical links are outside this Markdown link gate; their consuming code and upstream provenance remain the relevant checks.

## Preview and build

Use the Node version in [`.node-version`](../.node-version):

```bash
npm ci --prefix docs-site
make docs-dev
```

Run `make build-docs` before submitting changes. The site uses Astro Starlight in `docs-site/`; GitHub Actions builds and deploys documentation changes on `main` to GitHub Pages.

The `customize/theme-surface-matrix.md` pages are generated references. Update them with `make generate-theme-surface-matrix`, not by hand.
