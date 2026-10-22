# Feishu Workspace

This package is not bundled with Wuu. From the repository root:

```sh
wuu plugin install ./plugins/feishu-workspace/package
wuu plugin approve feishu-workspace
```

The checked-in runtime allows installation without a build. Rebuild explicitly after changing its source.

[简体中文](README_zh.md) · [User guide](../../docs/en/customize/feishu-workspace.md)

Optional, disabled-by-default Feishu website view and document tools using the
public Extension API. Runtime source lives in `src/`; canonical package metadata,
website view and artwork live in `plugins/feishu-workspace/package/`.
There is no Feishu CLI dependency and no browser-cookie API authentication.

```sh
node scripts/build-bundled-plugins.mjs --plugin=feishu-workspace
node plugins/feishu-workspace/scripts/api-acceptance.mjs
```

Run from the repository root with Node 22.7+. The acceptance script drives the
built JSONL subprocess through a synthetic host credential transport. It covers
user/tenant separation, paginated search and reads, document URL validation,
create/append/update wire bodies, revisions, stable idempotency keys, authorization
failures, rate limits, uncertain writes and cancellation. Results and protocol
frames are saved under `.tmp/e2e/feishu-workspace/`. It makes no external requests,
uses no credentials, and is not a live Feishu account or desktop visual test.

## Artwork

The monochrome SVGs adapt the Feishu mark from the official
[Feishu Open Platform favicon](https://lf-package-cn.feishucdn.com/obj/feishu-static/lark/open/doc/frontend/favicon-logo.svg),
retrieved 2026-10-19. The white tile was removed, the paths scaled into a 24-unit
view box, and fills replaced by Wuu's light/dark monochrome values. The source
publishes no open-source artwork license; the Feishu name and mark remain the
rights of their respective owners and are not relicensed under Wuu's MIT license.
They identify the connected service and do not imply affiliation or endorsement.
