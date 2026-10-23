import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { ApplicationShellOutlet } from "./ApplicationShell";

function fixture() {
  const listeners = new Set<() => void>();
  const service: ApplicationServiceV1 = {
    contractVersion: 1,
    getSnapshot: vi.fn(),
    subscribe: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    invoke: vi.fn(async () => undefined) as ApplicationServiceV1["invoke"],
  };
  return { host: new PluginHost({ react: React, application: service }), service, listeners };
}

// Failure cases: no implicit root fallback, stale handles after same-fingerprint
// reload, child ownership/unload, single-slot conflicts, and recursive slots.
describe("application shell assembly", () => {
  it("renders only the exact selected provider and removes its UI on unload", async () => {
    const { host } = fixture();
    for (const id of ["default", "research"]) await host.activateGeneration({
      pluginId: id, generation: "v1", register: api => {
        api.registerApplicationShell({ id: "ui.shell", render: () => <main>{id}</main> });
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "research", generation: "v1", id: "ui.shell" }} />));
      expect(container.querySelector("main")?.textContent).toBe("research");
      act(() => host.unload("research"));
      expect(container.querySelector("main")).toBeNull();
      expect(container.textContent).not.toContain("default");
      expect(container.querySelector('[role="alert"]')).not.toBeNull();
    } finally { act(() => root.unmount()); }
  });

  it("disposes subscriptions and fences old application handles even after same-code reactivation", async () => {
    const { host, service, listeners } = fixture();
    let old!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => { old = api; } });
    old.application.subscribe(() => {});
    expect(listeners.size).toBe(1);
    await old.application.invoke("composer.set-draft", { text: "hello" });
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: () => {} });
    expect(listeners.size).toBe(0);
    await expect(old.application.invoke("composer.submit", {})).rejects.toThrow(/no longer active/);
    expect(service.invoke).toHaveBeenCalledTimes(1);
  });

  it("composes nested third-party contributions and removes only their owner", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <main><slots.Slot name="shell/body" /></main> });
    } });
    await host.activateGeneration({ pluginId: "panel", generation: "v1", register: api => {
      api.registerUISlot({ id: "children", cardinality: "many" });
      api.registerUIContribution({ id: "panel", target: "shell/body", render: ({ slots }) => <section>Panel<slots.Slot name="panel/children" /></section> });
    } });
    await host.activateGeneration({ pluginId: "evidence", generation: "v1", register: api => {
      api.registerUIContribution({ id: "panel", target: "panel/children", render: () => <span>Evidence</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "v1", id: "ui.shell" }} />));
      expect(container.textContent).toBe("PanelEvidence");
      act(() => host.unload("evidence"));
      expect(container.textContent).toBe("Panel");
      act(() => host.unload("panel"));
      expect(container.textContent).toBe("");
    } finally { act(() => root.unmount()); }
  });

  it("rejects conflicting single-slot staging without disposing the previous provider", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "owner", generation: "v1", register: api => {
      api.registerUISlot({ id: "only", cardinality: "one" });
    } });
    const render = () => <span>One</span>;
    await host.activateGeneration({ pluginId: "one", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } });
    await expect(host.activateGeneration({ pluginId: "two", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } })).rejects.toThrow(/conflict/i);
    expect(host.getUIContributions("owner/only").map(item => item.pluginId)).toEqual(["one"]);
  });
});
