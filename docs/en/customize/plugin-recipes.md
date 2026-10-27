# Desktop plugin recipes

These examples build on the [desktop quickstart](desktop-plugin-quickstart.md). Each TypeScript block is a complete desktop entry; use it as `src/index.ts` in a package with `desktop.entry` pointing to `dist/index.js`.

## Add a review prompt to the draft

A composer contribution reads the public application snapshot and updates the owning draft. The native composer stays in place so the user can review the text before sending it.

```ts
import type { PluginGenerationApi, UISlotRenderProps } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  function ReviewPrompt({ application }: UISlotRenderProps) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
    const [error, setError] = React.useState("");
    const draft = snapshot.drafts[snapshot.drafts.activePane];
    const ownerTabId = draft.ownerTabId;
    async function append() {
      if (!ownerTabId) return;
      try {
        const text = draft.text.trimEnd();
        await application.invoke("composer.set-draft", {
          ownerTabId, text: `${text}${text ? "\n\n" : ""}Review the current changes for concrete bugs.`,
        });
        setError("");
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    return React.createElement("div", null,
      React.createElement("button", { disabled: !ownerTabId || snapshot.composer.readOnly, onClick: append }, "Add review prompt"),
      error ? React.createElement("p", { role: "alert" }, error) : null);
  }
  api.registerUIContribution({
    id: "review-prompt", target: "wuu.default-composer/composer.above", render: ReviewPrompt,
  });
}
```

The application validates the draft owner and generation lifetime. Use the same operation for selection-to-draft features; remove selection listeners when the contribution unmounts.

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

## Add a website page

A website uses the existing View and navigation contracts. This optional Notion example opens only after the user chooses its navigation entry; change the URL to a Feishu or another HTTP(S) website for a different extension.

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const WebPage = api.ui.WebPage as unknown as
    (props: Readonly<Record<string, unknown>>) => unknown;
  api.registerViewType({
    id: "website",
    title: "Notion",
    defaultRegion: "primary",
    persistence: "durable",
    render: () => api.react.createElement(WebPage, {
      url: "https://www.notion.so/",
      title: "Notion",
    }),
  });
}
```

Use the ordinary navigation contribution in the package's `plugin.json`:

```json
{
  "contributes": {
    "navigation": [
      { "id": "website-entry", "view": "website", "title": "Notion" }
    ]
  }
}
```

For a single local TypeScript file without a manifest, add `api.registerViewPlacement({ id: "website-entry", view: "website", region: "primary" })` during activation to open the same View. The host owns its ribbon entry, placement, browser session, and cleanup. The site uses the same imported browser cookies as other Wuu browser pages; sign in when the session does not already contain a valid login. Ordinary workbench view switches hide and retain the same live page. Explicitly closing the View, unmounting its WebPage component, disabling the plugin, or reloading its generation closes the page and popups. No site is installed by this recipe. Older hosts can be detected with `typeof api.ui.WebPage === "function"`. This component works only in the main desktop workbench window; popped-out conversation windows and remote web/mobile clients are unsupported. Non-desktop clients show the unavailable-browser state.

## Connect a page to its runtime

`api.invokeRuntime(method, input, { workspaceId })` calls the same plugin's active runtime generation. The runtime must implement the `plugin.client.request` capability; the method name is your plugin's own dispatch key, not an arbitrary app-server method. Check workspace availability with `listWorkspaces`, handle request failures in the UI, and use `onHostEvent` when changes should refresh a view.

Keep persistent business state in runtime storage rather than in two independent copies in the renderer and runtime. Register cleanup for subscriptions and timers. A temporary progress card can use `showConversationCard`; its `update`, `dismiss`, and `dispose` methods do not turn it into saved conversation history.

## Change appearance without replacing behavior

For colors and syntax highlighting, declare a theme under `contributes.themes`; see [themes and settings](themes-settings.md) and the [theme contract](theme-surface-matrix.md). For tool result styling, choose a renderer for the body or a presenter for execution summaries. Do not duplicate host-placed result bodies.

Before distributing a recipe, build and validate the package, then inspect it in Wuu Desktop. Verify its action, error state, keyboard focus, narrow layout, both themes, larger text, disable, and reload. `wuu plugin test` does not perform this rendered acceptance.
