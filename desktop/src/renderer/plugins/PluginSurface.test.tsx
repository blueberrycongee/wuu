import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PluginHost } from "./PluginHost";
import { PluginSurface } from "./PluginSurface";

describe("PluginSurface", () => {
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

  it("uses the highest ordered replacement and composes wrappers", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "layout",
      generation: "one",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "low",
          mode: "replace",
          order: 0,
          render: () => api.react.createElement("span", null, "Low"),
        });
        api.registerSurface("conversation.timeline", {
          id: "high",
          mode: "replace",
          order: 10,
          render: (context) => api.react.createElement("span", null, String(context.label)),
        });
        api.registerSurface("conversation.timeline", {
          id: "frame",
          mode: "wrap",
          render: (_context, fallback) => api.react.createElement("section", { "data-frame": true }, fallback),
        });
      },
    });

    act(() => root.render(
      <PluginSurface
        host={host}
        id="conversation.timeline"
        context={{ label: "High" }}
        fallback={<span>Built in</span>}
      />,
    ));

    expect(container.textContent).toBe("High");
    expect(container.querySelector(
      '[data-wuu-component="plugin-contribution"][data-wuu-plugin="layout"][data-wuu-surface="conversation.timeline"]',
    )?.textContent).toBe("High");
    expect(container.querySelector("section[data-frame=true]")).not.toBeNull();
  });

  it("falls back when a replacement fails and records the surface diagnostic", async () => {
    const host = new PluginHost({ react: React });
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      await host.activateGeneration({
        pluginId: "broken-layout",
        generation: "one",
        register(api) {
          api.registerSurface("conversation.message", {
            id: "broken",
            mode: "replace",
            render: () => {
              throw new Error("surface failed");
            },
          });
        },
      });

      act(() => root.render(
        <PluginSurface
          host={host}
          id="conversation.message"
          fallback={<span>Built in conversation</span>}
        />,
      ));

      expect(container.textContent).toBe("Built in conversation");
      expect(host.getGenerationDiagnostics("broken-layout", "one")).toEqual([
        expect.objectContaining({
          contributionId: "broken",
          kind: "render",
          surfaceId: "conversation.message",
        }),
      ]);
    } finally {
      consoleError.mockRestore();
    }
  });
});
