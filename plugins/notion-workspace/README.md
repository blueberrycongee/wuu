# Notion workspace plugin

This package is not bundled with Wuu. From the repository root:

```sh
wuu plugin install ./plugins/notion-workspace/package
wuu plugin approve notion-workspace
```

The checked-in runtime allows installation without a build. Rebuild explicitly after changing its source.

Optional, disabled-by-default example using public Wuu extension contracts.

- [User guide and API limits](../../docs/en/customize/notion-workspace.md)
- [简体中文](../../docs/zh-cn/customize/notion-workspace.md)

Build from this directory with `npm run build`; run the built-runtime mocked-API acceptance flow with `npm run test:e2e`. The canonical manifest, desktop entry, and licensed SVG assets live in `plugins/notion-workspace/package`; `dist/` and the local development manifest are generated.
