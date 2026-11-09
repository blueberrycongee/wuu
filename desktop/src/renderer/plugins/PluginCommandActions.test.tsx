import * as React from "react";
import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UIContributionRegistryV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
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

  it.each([false, true])("keeps overflow keyboard navigation usable when all actions disabled=%s", async (disabled) => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({ pluginId: "keyboard", generation: "one", register(api) {
      for (let index = 0; index < 4; index++) api.registerCommand({
        id: `action-${index}`, title: `Action ${index}`, placements: ["view.title"], enabled: () => !disabled, execute() {},
      });
    } });
    act(() => root.render(<PluginCommandActions host={host} context={context} />));
    const trigger = container.querySelector<HTMLButtonElement>('[aria-haspopup="dialog"]')!;
    trigger.focus();
    act(() => trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true })));
    await act(async () => { await new Promise<void>((resolve) => requestAnimationFrame(() => resolve())); });
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(document.activeElement).toBe(disabled ? dialog : dialog?.querySelector('[aria-label="Action 3"]'));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    act(() => trigger.click());
    await act(async () => { await new Promise<void>((resolve) => requestAnimationFrame(() => resolve())); });
    const reopened = document.querySelector<HTMLElement>('[role="dialog"]');
    expect(document.activeElement).toBe(disabled ? reopened : reopened?.querySelector('[aria-label="Action 2"]'));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true })));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

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

  it.each([true, false])("treats only proven command-scope cancellation as quiet (cancelled=%s)", async cancelled => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "refresh", title: "Refresh", placements: ["view.title"], execute: async () => {
        throw cancelled ? new PluginLifecycleCancellationError() : new Error("command failed");
      } });
    } });
    await act(async () => root.render(<PluginCommandActions host={host} context={context} />));
    await act(async () => container.querySelector<HTMLButtonElement>("button")!.click());
    expect(host.getGenerationDiagnostics("notes", "one")).toHaveLength(cancelled ? 0 : 1);
    expect(container.querySelector("button")?.getAttribute("aria-label") === "Refresh").toBe(cancelled);
  });

  it("releases a retained command row for a successor registry without letting the old completion touch it", async () => {
    const host = new PluginHost({ react: React });
    let first!: UIContributionRegistryV1, next!: UIContributionRegistryV1;
    let rejectOld!: (error: Error) => void, resolveNext!: () => void;
    const oldResult = new Promise<void>((_, reject) => { rejectOld = reject; });
    const nextResult = new Promise<void>(resolve => { resolveNext = resolve; });
    const execute = vi.fn().mockReturnValueOnce(oldResult).mockReturnValueOnce(nextResult);
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "refresh", title: "Refresh", placements: ["view.title"], execute });
    } });
    await host.activateGeneration({ pluginId: "first-consumer", generation: "one", register(api) { first = api.contributions; } });
    await host.activateGeneration({ pluginId: "next-consumer", generation: "one", register(api) { next = api.contributions; } });
    await act(async () => root.render(<PluginCommandActions host={first} context={context} />));
    const button = container.querySelector<HTMLButtonElement>("button")!;
    await act(async () => button.click());
    await act(async () => root.render(<PluginCommandActions host={next} context={context} />));
    expect(container.querySelector("button")).toBe(button);
    expect(button.disabled).toBe(false);
    await act(async () => button.click());
    expect(execute).toHaveBeenCalledTimes(2);
    await act(async () => {
      host.unload("first-consumer");
      rejectOld(new Error("retired command failed"));
      await oldResult.catch(() => {});
    });
    expect(button.disabled).toBe(true);
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await act(async () => { resolveNext(); await nextResult; });
    expect(button.disabled).toBe(false);
    expect(host.getGenerationDiagnostics("notes", "one")).toHaveLength(0);
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
