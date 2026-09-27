import * as React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ExtensionInventoryRecord, WuuDesktopApi } from "../../shared/protocol";
import { DesktopPluginRuntime } from "./DesktopPluginRuntime";
import { PluginHost } from "./PluginHost";

const originalWuu = window.wuu;

afterEach(() => {
  window.wuu = originalWuu;
});

describe("DesktopPluginRuntime", () => {
  it("loads approved generations once and unloads disabled plugins", async () => {
    const load = vi.fn(async () => ({
      activate: (api: { registerStyle(style: { id: string; css: string }): unknown }) => {
        api.registerStyle({ id: "theme", css: ".plugin-theme {}" });
      },
    }));
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
        id,
        fingerprint,
        digest: "a".repeat(64),
        url: "wuu-plugin://module/" + "a".repeat(64) + ".js",
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, load);
    const plugin = inventoryPlugin();

    expect(await runtime.sync([plugin])).toEqual([]);
    expect(await runtime.sync([plugin])).toEqual([]);
    expect(load).toHaveBeenCalledTimes(1);
    expect(document.head.querySelector("style[data-wuu-plugin-id='user:demo']")).not.toBeNull();

    await runtime.sync([{ ...plugin, enabled: false }]);
    expect(document.head.querySelector("style[data-wuu-plugin-id='user:demo']")).toBeNull();
  });

  it("suppresses desktop execution in safe mode and unloads prior contributions", async () => {
    const cleanup = vi.fn();
    const activate = vi.fn((api: { registerCleanup(fn: () => void): void }) => {
      api.registerCleanup(cleanup);
    });
    const load = vi.fn(async () => ({ activate }));
    const read = vi.fn<WuuDesktopApi["loadPluginDesktopModule"]>(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js",
    }));
    installDesktopModuleLoader(read);
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    const plugin = { ...inventoryPlugin(), runtime_state: "inactive" as const };

    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(read).not.toHaveBeenCalled();
    expect(activate).not.toHaveBeenCalled();

    // A desktop-only plugin does not need a running backend to activate.
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(read).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(2);
    await runtime.sync([]);
  });

  it("does not activate an in-flight module after entering safe mode", async () => {
    let finishRead!: (value: Awaited<ReturnType<WuuDesktopApi["loadPluginDesktopModule"]>>) => void;
    let started!: () => void;
    const reading = new Promise<void>((resolve) => { started = resolve; });
    installDesktopModuleLoader(vi.fn<WuuDesktopApi["loadPluginDesktopModule"]>()
      .mockImplementationOnce(() => {
        started();
        return new Promise((resolve) => { finishRead = resolve; });
      })
      .mockImplementation(async ({ id, fingerprint }) => ({
        id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js",
      })));

    const activate = vi.fn();
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async () => ({ activate }));
    const plugin = inventoryPlugin();
    const pending = runtime.sync([plugin]);
    await reading;
    expect(await runtime.sync([plugin], true)).toEqual([]);
    finishRead({ id: plugin.id, fingerprint: plugin.fingerprint!, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js" });
    expect(await pending).toEqual([]);
    expect(activate).not.toHaveBeenCalled();
  });

  it("keeps activation failures isolated", async () => {
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
        id,
        fingerprint,
        digest: "b".repeat(64),
        url: "wuu-plugin://module/" + "b".repeat(64) + ".js",
    })));
    const runtime = new DesktopPluginRuntime(
      new PluginHost({ react: React }),
      async () => ({ activate: () => { throw new Error("activation failed"); } }),
    );

    const failures = await runtime.sync([inventoryPlugin()]);
    expect(failures).toEqual([
      expect.objectContaining({ pluginId: "user:demo", fingerprint: "fingerprint-one" }),
    ]);
  });

  it("enforces manifest declarations for desktop UI registrations", async () => {
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id,
      fingerprint,
      digest: "c".repeat(64),
      url: "wuu-plugin://module/" + "c".repeat(64) + ".js",
    })));
    const runtime = new DesktopPluginRuntime(
      new PluginHost({ react: React }),
      async () => ({
        activate: (api: { registerSlot(target: string, contribution: { id: string; render(): null }): unknown }) => {
          api.registerSlot("composer.above", { id: "undeclared", render: () => null });
        },
      }),
    );

    const failures = await runtime.sync([inventoryPlugin()]);
    expect(failures[0]?.error).toEqual(expect.objectContaining({
      message: expect.stringContaining("is not declared in the manifest"),
    }));
  });
});

function inventoryPlugin(): ExtensionInventoryRecord {
  return {
    id: "user:demo",
    name: "demo",
    kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user" },
    state: "granted",
    approval_state: "granted",
    enabled: true,
    fingerprint: "fingerprint-one",
    desktop: { entry: "desktop.js" },
  };
}

function installDesktopModuleLoader(
  loader: WuuDesktopApi["loadPluginDesktopModule"],
): void {
  const api = { ...(window.wuu ?? {}) } as WuuDesktopApi;
  api.loadPluginDesktopModule = loader;
  window.wuu = api;
}
