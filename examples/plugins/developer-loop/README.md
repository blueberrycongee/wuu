# Developer Loop Example

This independently buildable plugin uses only the published `@wuu/plugin-sdk` package. It keeps the
SDK v3 request transform and tool while contributing a durable workbench View placement, complete
theme token sample, a two-view destination, a separate command ribbon item, status item, locales, and the conversation-header slot. The View is
built from the host-owned `api.ui` primitives, so another appearance plugin can theme it without
knowing this plugin exists. It uses the React instance owned by the host; React is neither a
dependency nor part of the bundle. It reads all four setting kinds and restores its counter from
plugin-namespaced storage.

The top-level manifest icon uses package-contained SVG artwork in the plugin catalog. Host-owned
entries declare their own semantic or package-contained icon instead of inheriting brand artwork.

Its presenter registrations are deliberately layout-neutral: they observe versioned snapshots and
retain the native fallback instead of replacing navigation, titlebars, messages, or status chrome.
The example can therefore stay enabled while testing another appearance plugin without changing
the product layout.

Run the complete developer loop from this directory:

```sh
npm install
npm run build
npm test
wuu plugin test .
wuu plugin dev .
```

Install the built directory directly, or create a portable package first:

```sh
wuu plugin install .
wuu plugin pack .
wuu plugin install ./developer-loop-example-1.0.0.zip
```

`npm test` runs type checking, builds both entries, and then performs static contract checks. Those
checks reject private Wuu source imports and React bundling, and verify every acceptance
contribution. `wuu plugin test` starts the executable runtime and checks protocol negotiation, the
v3 capability descriptor, and executable tool registration. `wuu plugin dev` builds, validates,
and publishes isolated development generations; a failed refresh keeps the previous generation.

## Trust boundary

The manifest and renderer are untrusted plugin inputs. Wuu owns React, workbench lifecycle,
settings, namespaced storage, contribution disposal, and generation swaps. This plugin receives
only the public SDK API and view-scoped host methods. It cannot import Desktop or core internals,
and it must not assume access to host classes, global storage keys, or private DOM structure. The
executable runtime is a separate Node process speaking JSONL over standard input/output; only its
declared request transform, static prompt section, and tool cross that boundary. It intentionally
does not register the experimental compaction capability as a pass-through consumer.

## Destination acceptance

The Developer Loop ribbon item selects `acceptance-counter` in the main area and
`acceptance-navigation` in the destination sidebar together. Switch to Conversations and back
to verify the host restores the destination instances. The separate Open Counter Panel ribbon
item executes the plugin command without changing the selected destination. Workspace-tool and
Settings entries still open the counter in their own surfaces. Disable or reload the plugin to
verify that both ribbon items follow the same generation lifecycle as its views.
