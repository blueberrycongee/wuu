# Conversation metrics

An independently installable desktop addon, not a replacement product. It uses
only `@wuu/plugin-sdk` public types and the host React instance.

- Adds reported duration and token usage after each turn. Unknown values stay
  absent; reported zero is displayed.
- Adds an **Inspect reported result** action to disclosed tool details.
- Targets both default conversation and preview owners, in bubble or IM style.
- Coexists with other addons and tool skins. Disabling it removes its UI without
  remounting native content; re-enabling starts its own local disclosure state.

Install the directory with `wuu plugin install ./examples/plugins/conversation-metrics`,
or use `wuu plugin dev ./examples/plugins/conversation-metrics` for local work.
Installation needs no build or runtime service; directory development runs only the included JavaScript syntax check. It requires a host exposing the additive
slots documented in [Desktop UI plugins](../../../docs/en/customize/desktop-plugins.md).
It does not estimate per-tool duration or infer measurement data from text.

Validation:

```sh
node --test packages/plugin-sdk/test/conversation-ui-package.test.mjs
cd desktop
node_modules/.bin/vitest run src/renderer/modules/DefaultConversationComposition.test.tsx
# After electron-vite build, in a graphical session (CI uses Xvfb):
node_modules/.bin/electron scripts/conversation-addons-e2e.cjs
```

The browser acceptance uses the production renderer and plugin runtime with
synthetic backend inventory and history. It saves `results.json` and captures
under `desktop/out/e2e/conversation-addons/`; it does not test a real model or Go
backend. CI runs it in **Desktop / shell** and retains evidence on failure.
