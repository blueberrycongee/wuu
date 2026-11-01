import * as React from "react";
import { act } from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { webcrypto } from "node:crypto";
import { expect, it, vi } from "vitest";
import { activate } from "../../../../internal/plugin/bundled/generative-ui/desktop.js";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";

it.each(["chart", "disclosure", "source-disclosure-keys"] as const)("persists a %s view's open state before an immediate unmount", async (type) => {
  vi.stubGlobal("crypto", webcrypto);
  const host = new PluginHost({ react: React });
  const saved = new Map<string, string>();
  let readDone: () => void;
  const controller = new WorkbenchController(host, {
    getStorage: async (_plugin, _generation, key) => { readDone(); return saved.get(key) ?? null; },
    setStorage: async (_plugin, _generation, key, value) => { saved.set(key, value); },
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    await host.activateGeneration({ pluginId: "generative-ui", generation: type, register: api => activate(api as unknown as Parameters<typeof activate>[0]) });
    const Renderer = controller.getRenderer("tool-result", "application/vnd.wuu.ui+json")!.render;
    const node = type === "chart"
      ? { id: "view", type, chartType: "area", points: { op: "literal", value: [{ label: "One", value: 1 }] } }
      : { id: "view", type, title: "Details", children: [{ id: "text", type: "text", text: "Local details" }] };
    const content = { text: JSON.stringify(type === "source-disclosure-keys"
      ? { version: 3, title: "Durable disclosure", fallback: "Independent local details", source: '<Stack><Disclosure key="a/b" title="First details">First</Disclosure><Stack key="a"><Disclosure key="b" title="Second details">Second</Disclosure></Stack></Stack>' }
      : { version: 2, title: "Durable disclosure", fallback: "Local details", state: [], body: [node] }) };
    const props = { content, metadata: { threadId: "thread", itemId: "item", partId: "part", artifactId: type }, host: controller.createPresentationHostAPI("generative-ui", type, []) };
    async function mount() {
      const read = new Promise<void>(resolve => { readDone = resolve; });
      await act(async () => {
        flushSync(() => root.render(<Renderer {...props} />));
        await read;
      });
      expect(container.querySelector('[data-wuu-component="generated-ui"]')?.getAttribute("aria-busy")).toBe("false");
    }
    await mount();
    for (const open of [true, false]) {
      const details = container.querySelector("details")!;
      const toggle = vi.fn();
      details.addEventListener("toggle", toggle);
      act(() => details.querySelector("summary")!.click());
      expect(details.open).toBe(open);
      if (type === "source-disclosure-keys") expect(container.querySelectorAll("details")[1].open).toBe(false);
      expect(toggle).not.toHaveBeenCalled();
      // Navigation may unmount the view before the browser queues its toggle event.
      act(() => root.render(null));
      await mount();
      expect(container.querySelector("details")!.open).toBe(open);
      if (type === "source-disclosure-keys") expect(container.querySelectorAll("details")[1].open).toBe(false);
    }
  } finally {
    act(() => root.unmount());
    controller.dispose();
    host.unload("generative-ui");
    container.remove();
    vi.unstubAllGlobals();
  }
});
