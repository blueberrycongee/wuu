// @vitest-environment jsdom
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { PluginPresentation } from "./PluginPresentation";
import { PluginSlot } from "./PluginSlot";
import { PluginSurface } from "./PluginSurface";
import { PluginInspectorSections } from "./PluginInspector";
import { PluginConversationCards } from "./PluginConversationCards";
import { WorkbenchController } from "./Workbench";

let root: Root | undefined;
let container: HTMLElement | undefined;

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  root = undefined;
  container = undefined;
});

describe("PluginPresentation", () => {
  it.each(["slot", "surface", "presenter", "inspector", "card"] as const)("recovers a mounted %s after same-code reactivation without remounting ordinary updates", async (kind) => {
    const { host, controller } = setup();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    let broken = true;
    let attempts = 0;
    let mounts = 0;
    function Content(): React.ReactNode {
      React.useEffect(() => { mounts++; }, []);
      attempts++;
      if (broken) throw new Error("activation failed to render");
      return <span data-recovered>recovered</span>;
    }
    // The same function can be registered by separate activations of one module.
    const content = () => <Content />;
    const register = (api: PluginGenerationApi): void => {
      if (kind === "slot") api.registerSlot("composer.above", { id: "item", render: content });
      else if (kind === "surface") api.registerSurface("conversation.message", { id: "item", mode: "replace", render: content });
      else if (kind === "presenter") api.registerPresenter({ id: "item", target: "content.preview", render: content });
      else if (kind === "inspector") api.registerInspectorSection({ id: "item", title: "Item", render: Content });
      else api.showConversationCard({ id: "item", threadId: "thread", title: "Item", render: content });
    };
    const element = (): React.ReactNode => {
      if (kind === "slot") return <PluginSlot host={host} id="composer.above" context={{}} />;
      if (kind === "surface") return <PluginSurface host={host} id="conversation.message" fallback={<span>native</span>} />;
      if (kind === "presenter") return <PluginPresentation host={host} controller={controller} target="content.preview" snapshot={{}} fallback={<span>native</span>} />;
      if (kind === "inspector") return <PluginInspectorSections host={host} controller={controller} snapshot={{ contractVersion: 1, session: { status: "idle" } }} />;
      return <PluginConversationCards host={host} threadId="thread" />;
    };
    try {
      await host.activateGeneration({ pluginId: "repair", generation: "same", register });
      render(element());
      expect(container?.querySelector("[data-recovered]")).toBeNull();
      const failedAttempts = attempts;
      await act(async () => host.activateGeneration({ pluginId: "unrelated", generation: "one", register() {} }));
      render(element());
      expect(attempts).toBe(failedAttempts);
      broken = false;
      await act(async () => host.activateGeneration({ pluginId: "repair", generation: "same", register }));
      expect(container?.querySelector("[data-recovered]")?.textContent).toBe("recovered");
      expect(mounts).toBe(1);
      const node = container?.querySelector("[data-recovered]");
      render(element());
      await act(async () => host.activateGeneration({ pluginId: "unrelated", generation: "two", register() {} }));
      expect(container?.querySelector("[data-recovered]")).toBe(node);
      expect(mounts).toBe(1);
    } finally {
      consoleError.mockRestore();
      controller.dispose();
    }
  });

  it("selects the highest replacement and composes wrappers in registry order", async () => {
    const { host, controller } = setup();
    await host.activateGeneration({ pluginId: "zeta", generation: "one", register(api) {
      api.registerPresenter({ id: "replacement", target: "conversation.item", key: "message", priority: 10, render: ({ key }) => <b data-key={key}>replacement</b> });
      api.registerPresenter({ id: "outer", target: "conversation.item", key: "message", mode: "wrap", priority: 20, render: ({ fallback }) => <section data-outer>{fallback}</section> });
    } });
    await host.activateGeneration({ pluginId: "alpha", generation: "one", register(api) {
      api.registerPresenter({ id: "ignored", target: "conversation.item", key: "message", priority: 0, render: () => <i>ignored</i> });
      api.registerPresenter({ id: "inner", target: "conversation.item", key: "message", mode: "wrap", priority: 0, render: ({ fallback }) => <div data-inner>{fallback}</div> });
    } });
    render(<PluginPresentation host={host} controller={controller} target="conversation.item" presentationKey="message" snapshot={Object.freeze({})} fallback={<span>native</span>} />);
    expect(container?.querySelector("[data-outer] [data-inner] b")?.textContent).toBe("replacement");
    expect(container?.querySelector("b")?.getAttribute("data-key")).toBe("message");
  });

  it("validates actions, dispatches supported input, and rejects stale hosts", async () => {
    const { host, controller } = setup();
    let presentationHost: import("../../shared/workbench").PresentationHost | undefined;
    const dispatch = vi.fn(async (_action: string, input?: unknown) => input);
    await host.activateGeneration({ pluginId: "actions", generation: "one", register(api) {
      api.registerPresenter({ id: "actions", target: "app.status", render: ({ host: apiHost }) => {
        presentationHost = apiHost;
        return <span>ready</span>;
      } });
    } });
    render(<PluginPresentation host={host} controller={controller} target="app.status" snapshot={{}} fallback={null} actions={["retry"]} dispatchAction={dispatch} />);
    await expect(presentationHost?.invoke("missing")).rejects.toThrow("not supported");
    await expect(presentationHost?.invoke("retry", { id: 1 })).resolves.toEqual({ id: 1 });
    expect(dispatch).toHaveBeenCalledWith("retry", { id: 1 });
    const unavailable = controller.createPresentationHostAPI("actions", "one", ["retry"]);
    await expect(unavailable.invoke("retry")).rejects.toThrow("dispatcher is unavailable");
    act(() => host.unload("actions"));
    await expect(presentationHost?.invoke("retry")).rejects.toThrow("no longer active");
  });

  it("falls back locally after a throw and recovers when the generation is replaced", async () => {
    const { host, controller } = setup();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await host.activateGeneration({ pluginId: "repair", generation: "broken", register(api) {
      api.registerPresenter({ id: "card", target: "content.preview", key: "text", render: () => { throw new Error("broken"); } });
    } });
    render(<PluginPresentation host={host} controller={controller} target="content.preview" presentationKey="text" snapshot={{}} fallback={<span data-native>native</span>} />);
    expect(container?.querySelector("[data-native]")).toBeTruthy();
    expect(host.getGenerationDiagnostics("repair", "broken")).toHaveLength(1);
    await act(async () => host.activateGeneration({ pluginId: "repair", generation: "good", register(api) {
      api.registerPresenter({ id: "card", target: "content.preview", key: "text", render: () => <span data-repaired>repaired</span> });
    } }));
    expect(container?.querySelector("[data-repaired]")?.textContent).toBe("repaired");
    consoleError.mockRestore();
  });
});

function setup(): { host: PluginHost; controller: WorkbenchController } {
  const host = new PluginHost({ react: React });
  const controller = new WorkbenchController(host);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  return { host, controller };
}

function render(node: React.ReactNode): void {
  act(() => root?.render(node));
}
