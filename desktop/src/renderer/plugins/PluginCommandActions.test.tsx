import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PluginCommandActionContext } from "../../shared/workbench";
import { PluginHost } from "./PluginHost";
import { PluginCommandActions } from "./PluginCommandActions";

const context: PluginCommandActionContext = Object.freeze({ contractVersion: 1, target: "view.title", viewId: "view", viewTypeId: "notes", viewPluginId: "notes", region: "auxiliary" });

describe("PluginCommandActions", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(() => { act(() => root.unmount()); container.remove(); });

  it("adds nothing without placements, renders semantic icons, isolates predicates, and removes actions on unload", async () => {
    const host = new PluginHost({ react: React });
    act(() => root.render(<PluginCommandActions host={host} context={context} />));
    expect(container.childNodes).toHaveLength(0);
    await act(async () => { await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "plain", title: "Plain", execute() {} });
      api.registerCommand({ id: "broken", title: "Broken", placements: ["view.title"], when() { throw new Error("bad predicate"); }, execute() {} });
      api.registerCommand({ id: "refresh", title: "Refresh", icon: "wrench", placements: ["view.title"], execute() {} });
      api.registerCommand({ id: "unavailable", title: "Unavailable", placements: ["view.title"], enabled: () => false, execute() {} });
    } }); });
    expect([...container.querySelectorAll("button")].map((node) => node.getAttribute("aria-label"))).toEqual(["Refresh", "Unavailable"]);
    expect(container.querySelector('[data-icon="wrench"]')).not.toBeNull();
    expect(container.querySelector<HTMLButtonElement>('[aria-label="Unavailable"]')!.disabled).toBe(true);
    expect(host.getGenerationDiagnostics("notes", "one")).toHaveLength(1);
    act(() => host.unload("notes"));
    expect(container.childNodes).toHaveLength(0);
  });

  it("bounds direct actions and closes overflow when the target changes or plugins unload", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      for (let index = 0; index < 6; index++) api.registerCommand({ id: `a${index}`, title: `Action ${index}`, placements: ["view.title"], execute() {} });
    } });
    act(() => root.render(<PluginCommandActions host={host} context={context} />));
    expect(container.querySelectorAll("button")).toHaveLength(3);
    act(() => container.querySelector<HTMLButtonElement>('[aria-haspopup="dialog"]')!.click());
    expect(document.querySelector('[data-floating-menu-owner="plugin-command-actions"]')).not.toBeNull();
    act(() => root.render(<PluginCommandActions host={host} context={{ ...context, viewId: "other" }} />));
    expect(document.querySelector('[data-floating-menu-owner="plugin-command-actions"]')).toBeNull();
    act(() => container.querySelector<HTMLButtonElement>('[aria-haspopup="dialog"]')!.click());
    act(() => host.unload("notes"));
    expect(container.childNodes).toHaveLength(0);
    expect(document.querySelector('[data-floating-menu-owner="plugin-command-actions"]')).toBeNull();
  });

  it("keeps an overflow command pending across closing and reopening its menu", async () => {
    const host = new PluginHost({ react: React });
    let finish!: () => void;
    const pending = new Promise<void>((resolve) => { finish = resolve; });
    const execute = vi.fn(() => pending);
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      for (let index = 0; index < 3; index++) api.registerCommand({ id: `a${index}`, title: `Action ${index}`, placements: ["view.title"], execute: index === 2 ? execute : () => undefined });
    } });
    act(() => root.render(<PluginCommandActions host={host} context={context} />));
    const trigger = container.querySelector<HTMLButtonElement>('[aria-haspopup="dialog"]')!;
    act(() => trigger.click());
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Action 2"]')!.click());
    act(() => trigger.click());
    act(() => trigger.click());
    const reopened = document.querySelector<HTMLButtonElement>('[aria-label="Action 2"]')!;
    expect(reopened.disabled).toBe(true);
    act(() => reopened.click());
    expect(execute).toHaveBeenCalledTimes(1);
    await act(async () => { finish(); await pending; });
  });

  it("prevents duplicate clicks while pending and does not surface retired-run failures", async () => {
    const host = new PluginHost({ react: React });
    let fail!: (error: Error) => void;
    const pending = new Promise<void>((_, reject) => { fail = reject; });
    const execute = vi.fn(() => pending);
    await host.activateGeneration({ pluginId: "notes", generation: "same", register(api) {
      api.registerCommand({ id: "refresh", title: "Refresh", placements: ["view.title"], execute });
    } });
    act(() => root.render(<PluginCommandActions host={host} context={context} />));
    const button = container.querySelector("button")!;
    act(() => { button.click(); button.click(); });
    expect(execute).toHaveBeenCalledTimes(1);
    await act(async () => { await host.activateGeneration({ pluginId: "notes", generation: "same", register(api) {
      api.registerCommand({ id: "refresh", title: "Fresh", placements: ["view.title"], execute() {} });
    } }); });
    await act(async () => { fail(new Error("old failed")); await pending.catch(() => undefined); });
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(container.querySelector<HTMLButtonElement>("button")!.disabled).toBe(false);
    expect(container.querySelector("button")!.getAttribute("aria-label")).toBe("Fresh");
  });
});
