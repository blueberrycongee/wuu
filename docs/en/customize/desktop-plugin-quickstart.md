# Desktop plugin quickstart

This tutorial adds a small toggle to the composer toolbar. It demonstrates local component state and host UI registration; it does not change model behavior or save a setting. You need Wuu Desktop, the `wuu` CLI, Node.js 22 or later, and the public SDK tarball matching your Wuu build. The author directory does not need a Wuu source checkout.

## Create the package

Replace the SDK tarball path, then run from the directory where you want the plugin project:

```bash
wuu plugin create --type desktop toolbar-demo
cd toolbar-demo
npm install --save-dev /absolute/path/to/wuu-plugin-sdk-0.1.0.tgz
```

The generated `plugin.json` points `desktop.entry` to `dist/index.js`. The desktop module exports `activate(api)`, called when the host starts its generation. This example imports only SDK types, so TypeScript produces a self-contained ESM entry without runtime imports.

## Add the toggle

Replace `src/index.ts` with:

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const Toggle = api.ui.ToolbarToggle;

  function DemoToggle() {
    const [enabled, setEnabled] = React.useState(false);
    return React.createElement(Toggle, {
      pressed: enabled,
      "aria-label": "Toggle demo state",
      onClick: () => setEnabled(value => !value),
    }, enabled ? "Demo on" : "Demo off");
  }

  api.registerSlot("composer.toolbar", {
    id: "demo-toggle",
    order: 20,
    render: () => React.createElement(DemoToggle, null),
  });
}
```

`api.react` is the host's React instance. `api.ui.ToolbarToggle` supplies the shared control, while `composer.toolbar` places it without depending on the composer's private DOM. Register components through the API rather than bundling another React runtime.

## Build and load

```bash
npm run build
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

For a desktop-only package, `test` checks the package and reports that runtime initialization was skipped. It does not import or render the desktop module. The host requires a self-contained entry: if you later add runtime imports, bundle them rather than shipping unresolved imports beside this file.

`dev` authorizes the supplied directory and publishes rebuilt development generations as files change. A failed build or package check keeps the last published generation; publication can wait for active executions. Check the desktop plugin status for activation errors. Desktop registration has its own lifecycle. If you add a native runtime entry, its post-commit activation failure does not roll back the package change; see [plugin architecture](plugin-system.md#runtime-generations).

In Wuu Desktop, click the toggle and confirm its label and pressed state change. Test keyboard focus, both themes, a narrow window, and a larger UI font. Disabling the plugin should remove the control. React state is local to the mounted component and is not durable plugin storage.

## Package it

```bash
wuu plugin pack .
wuu plugin install ./toolbar-demo-0.1.0.zip
wuu plugin approve toolbar-demo
```

The current CLI separates staging from the trust decision that enables execution. Development authorization stays local and does not travel with the zip. Desktop plugins are trusted renderer code, not sandboxed web content.

Use the [UI extension map](desktop-plugins.md) to choose a larger boundary, the [recipes](plugin-recipes.md) for actions and views, and the [authoring reference](plugin-authoring.md) for package and lifecycle contracts.

For a single TSX component, a complete shell and nested contributions, see [direct TSX preview and the public UI SDK](ui-sdk.md).
