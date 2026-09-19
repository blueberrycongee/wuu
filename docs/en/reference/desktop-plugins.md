# Desktop plugin API

Desktop modules export `activate(api)` and use the generation-scoped `PluginGenerationApi`. Obtain types from the matching Wuu checkout's `packages/plugin-sdk`; use the host's `api.react` and `api.ui` at runtime.

| Contract | Guide |
| --- | --- |
| Slots, surfaces, presenters, views, cards, and status sources | [Desktop UI plugins](../customize/desktop-plugins.md) |
| A complete local package and build commands | [Desktop quickstart](../customize/desktop-plugin-quickstart.md) |
| Draft actions, workspace pages, and runtime calls | [Desktop recipes](../customize/plugin-recipes.md) |
| Manifest, runtime lifecycle, storage, and distribution | [Plugin authoring](../customize/plugin-authoring.md) |
| Public theme tokens and semantic anchors | [Theme surface matrix](../customize/theme-surface-matrix.md) |

Registration methods return disposable handles and belong to the active generation. Actions advertised by a presentation host are specific to that render, not a global command permission. Private renderer modules, DOM nesting, and React internals are outside the public API.
