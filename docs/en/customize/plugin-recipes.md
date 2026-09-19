# Desktop plugin recipes

These examples build on the [desktop quickstart](desktop-plugin-quickstart.md). Each TypeScript block is a complete desktop entry; use it as `src/index.ts` in a package with `desktop.entry` pointing to `dist/index.js`.

## Add a review prompt to the draft

A composer presenter can read the public draft snapshot and invoke a host action. This wrapper keeps the native composer and lets the user review the new text before sending it.

```ts
import type { ComposerSnapshotV1, PluginGenerationApi, PresenterProps } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const Button = api.ui.Button as unknown as
    (props: Readonly<Record<string, unknown>>) => unknown;
  const action = "conversation.composer.set-draft";

  function ReviewPrompt(input: Readonly<Record<string, unknown>>) {
    const props = input.presenter as PresenterProps;
    const snapshot = props.snapshot as ComposerSnapshotV1;
    const [error, setError] = React.useState("");
    const disabled = snapshot.readOnly || !props.host.actions.includes(action);
    async function append() {
      try {
        const draft = snapshot.draftText?.trimEnd() ?? "";
        await props.host.invoke(action,
          `${draft}${draft ? "\n\n" : ""}Review the current changes for concrete bugs.`);
        setError("");
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    return React.createElement("div", null,
      props.fallback,
      React.createElement(Button, { disabled, onClick: append }, "Add review prompt"),
      error ? React.createElement("p", { role: "alert" }, error) : null);
  }

  api.registerPresenter({
    id: "review-prompt", target: "conversation.composer", mode: "wrap",
    render: props => React.createElement(ReviewPrompt, { presenter: props }),
  });
}
```

The action accepts a string, not a DOM event or a reference to the textarea. The host still checks read-only state. For a selection-to-draft feature, obtain plain text with the browser Selection API, verify it belongs to a public message anchor, then pass the text through the same action. Remove selection listeners on unmount, preserve the user's selection while clicking, and account for scrolling and viewport edges.

## Add a workspace page

Register a view and expose it through a manifest entry. The host owns opening the page and its surrounding navigation.

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  api.registerViewType({
    id: "dashboard",
    title: "Dashboard",
    defaultRegion: "auxiliary",
    persistence: "durable",
    render: () => api.react.createElement("p", null, "Workspace dashboard"),
  });
}
```

Merge this contribution into the package's `plugin.json`:

```json
{
  "contributes": {
    "workspaceTools": [
      { "id": "dashboard-entry", "view": "dashboard", "title": "Dashboard" }
    ]
  }
}
```

`view` must match the registered ID in the same plugin. Use `settingsPages` for a settings page or `navigation` for a navigation entry. Add `registerViewPlacement` only when an initial open placement is useful, rather than forcing every optional tool onto the workbench.

## Connect a page to its runtime

`api.invokeRuntime(method, input, { workspaceId })` calls the same plugin's active runtime generation. The runtime must implement the `plugin.client.request` capability; the method name is your plugin's own dispatch key, not an arbitrary app-server method. Check workspace availability with `listWorkspaces`, handle request failures in the UI, and use `onHostEvent` when changes should refresh a view.

Keep persistent business state in runtime storage rather than in two independent copies in the renderer and runtime. Register cleanup for subscriptions and timers. A temporary progress card can use `showConversationCard`; its `update`, `dismiss`, and `dispose` methods do not turn it into saved conversation history.

## Change appearance without replacing behavior

For colors and syntax highlighting, declare a theme under `contributes.themes`; see [themes and settings](themes-settings.md) and the [theme contract](theme-surface-matrix.md). For tool result styling, choose a renderer for the body or a presenter for execution summaries. Do not duplicate host-placed result bodies.

Before distributing a recipe, build and validate the package, then inspect it in Wuu Desktop. Verify its action, error state, keyboard focus, narrow layout, both themes, larger text, disable, and reload. `wuu plugin test` does not perform this rendered acceptance.
