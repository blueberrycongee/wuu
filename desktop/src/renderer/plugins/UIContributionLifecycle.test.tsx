import * as Toast from "../Toast";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PluginHost, type Disposable } from "./PluginHost";
import { createUIComposition } from "./ApplicationShell";

describe("UI contribution lifecycle", () => {
  let container: HTMLDivElement;
  let root: Root;

  let host: PluginHost;
  let slots: ReturnType<typeof createUIComposition>;

  beforeEach(async () => {
    host = new PluginHost({ react: React });
    slots = createUIComposition(host);
    await host.activateGeneration({ pluginId: "fixture", generation: "one", register: api => {
      for (const id of ["composer.above", "composer.toolbar", "conversation.header", "sidebar.primary"]) api.registerUISlot({ id, cardinality: "many" });
    } });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    host.unload("fixture");
  });

  it("rerenders from the external store when a generation activates and unloads", async () => {
    act(() => root.render(<slots.Slot name="fixture/composer.above" context={{ label: "ready" }} />));
    expect(container.textContent).toBe("");

    await act(async () => {
      await host.activateGeneration({
        pluginId: "status",
        generation: "one",
        register(api) {
          api.registerUIContribution({ target: "fixture/composer.above",
            id: "state",
            render: ({ context }) => api.react.createElement("span", null, String(context.label)),
          });
        },
      });
    });
    expect(container.textContent).toBe("ready");


    act(() => host.unload("status"));
    expect(container.textContent).toBe("");
  });

  it("adds the active locale and plugin translations to every slot context", async () => {
    await host.activateGeneration({
      pluginId: "localized",
      generation: "one",
      register(api) {
        api.registerLocale({
          id: "localized-zh",
          locale: "zh-CN",
          entries: { "localized.label": "插件中文" },
        });
        api.registerUIContribution({ target: "fixture/composer.toolbar",
          id: "localized-action",
          render: ({ locale, translate }) => api.react.createElement(
            "span",
            null,
            `${locale}:${translate("localized.label")}`,
          ),
        });
      },
    });

    act(() => root.render(<slots.Slot name="fixture/composer.toolbar" />));

    expect(container.textContent).toBe("zh-CN:插件中文");
  });

  it("isolates one contribution render failure and records it in generation diagnostics", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const notice = vi.spyOn(Toast, "showErrorToast").mockReturnValue(undefined);
    try {
      await host.activateGeneration({
        pluginId: "broken-view",
        generation: "one",
        register(api) {
          api.registerUIContribution({ target: "fixture/conversation.header",
            id: "broken",
            order: 0,
            render: () => {
              throw new Error("view failed");
            },
          });
          api.registerUIContribution({ target: "fixture/conversation.header",
            id: "healthy",
            order: 1,
            render: () => api.react.createElement("span", null, "Still available"),
          });
        },
      });

      act(() => root.render(<slots.Slot name="fixture/conversation.header" />));

      expect(container.querySelector("span")?.textContent).toBe("Still available");
      expect(container.querySelector('[role="alert"]')).toBeNull();
      expect(container.textContent).not.toContain("view failed");
      expect(container.querySelector('[role="status"]')).not.toBeNull();
      expect(notice).not.toHaveBeenCalled();
      expect(host.getGenerationDiagnostics("broken-view", "one")).toEqual([
        expect.objectContaining({
          kind: "render",
          message: expect.stringContaining("view failed"),
        }),
      ]);
    } finally {
      consoleError.mockRestore();
      notice.mockRestore();
    }
  });

  it("rerenders when an individual contribution disposable removes its item", async () => {
    let contributionDisposable: Disposable | undefined;
    await host.activateGeneration({
      pluginId: "temporary",
      generation: "one",
      register(api) {
        contributionDisposable = api.registerUIContribution({ target: "fixture/sidebar.primary",
          id: "temporary-item",
          render: () => api.react.createElement("span", null, "Temporary"),
        });
      },
    });
    act(() => root.render(<slots.Slot name="fixture/sidebar.primary" />));
    expect(container.textContent).toBe("Temporary");

    act(() => contributionDisposable?.dispose());

    expect(container.textContent).toBe("");
  });
});
