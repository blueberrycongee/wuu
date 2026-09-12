import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PluginHost, type Disposable } from "./PluginHost";
import { PluginSlot } from "./PluginSlot";

describe("PluginSlot", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("rerenders from the external store when a generation activates and unloads", async () => {
    const host = new PluginHost({ react: React });
    act(() => root.render(<PluginSlot host={host} id="composer.above" context={{ label: "ready" }} />));
    expect(container.textContent).toBe("");

    await act(async () => {
      await host.activateGeneration({
        pluginId: "status",
        generation: "one",
        register(api) {
          api.registerSlot("composer.above", {
            id: "state",
            render: (context) => api.react.createElement("span", null, String(context.label)),
          });
        },
      });
    });
    expect(container.textContent).toBe("ready");
    expect(container.querySelector(
      '[data-wuu-component="plugin-contribution"][data-wuu-plugin="status"][data-wuu-slot="composer.above"]',
    )?.textContent).toBe("ready");

    act(() => host.unload("status"));
    expect(container.textContent).toBe("");
  });

  it("adds the active locale and plugin translations to every slot context", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "localized",
      generation: "one",
      register(api) {
        api.registerLocale({
          id: "localized-zh",
          locale: "zh-CN",
          entries: { "localized.label": "插件中文" },
        });
        api.registerSlot("composer.toolbar", {
          id: "localized-action",
          render: (context) => api.react.createElement(
            "span",
            null,
            `${String(context.locale)}:${(context.translate as (key: string) => string)("localized.label")}`,
          ),
        });
      },
    });

    act(() => root.render(<PluginSlot host={host} id="composer.toolbar" />));

    expect(container.textContent).toBe("zh-CN:插件中文");
  });

  it("isolates one contribution render failure and records it in generation diagnostics", async () => {
    const host = new PluginHost({ react: React });
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      await host.activateGeneration({
        pluginId: "broken-view",
        generation: "one",
        register(api) {
          api.registerSlot("conversation.header", {
            id: "broken",
            order: 0,
            render: () => {
              throw new Error("view failed");
            },
          });
          api.registerSlot("conversation.header", {
            id: "healthy",
            order: 1,
            render: () => api.react.createElement("span", null, "Still available"),
          });
        },
      });

      act(() => root.render(<PluginSlot host={host} id="conversation.header" />));

      expect(container.textContent).toBe("Still available");
      expect(host.getGenerationDiagnostics("broken-view", "one")).toEqual([
        expect.objectContaining({
          contributionId: "broken",
          kind: "render",
          message: expect.stringContaining("view failed"),
          slotId: "conversation.header",
        }),
      ]);
    } finally {
      consoleError.mockRestore();
    }
  });

  it("rerenders when an individual contribution disposable removes its item", async () => {
    const host = new PluginHost({ react: React });
    let contributionDisposable: Disposable | undefined;
    await host.activateGeneration({
      pluginId: "temporary",
      generation: "one",
      register(api) {
        contributionDisposable = api.registerSlot("sidebar.primary", {
          id: "temporary-item",
          render: () => api.react.createElement("span", null, "Temporary"),
        });
      },
    });
    act(() => root.render(<PluginSlot host={host} id="sidebar.primary" />));
    expect(container.textContent).toBe("Temporary");

    act(() => contributionDisposable?.dispose());

    expect(container.textContent).toBe("");
  });
});
