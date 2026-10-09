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
  it.each([false, true])("activates independent plugins while another is pending, then safe mode=%s", async (safeMode) => {
    let release!: () => void;
    let signalStarted!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const started = new Promise<void>((resolve) => { signalStarted = resolve; });
    const healthy = vi.fn();
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `wuu-plugin://module/${id}.js`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      activate: url.includes("slow") ? async () => { signalStarted(); await gate; } : healthy,
    }));
    const pending = runtime.sync([
      { ...inventoryPlugin(), id: "user:slow" },
      { ...inventoryPlugin(), id: "user:healthy" },
    ]);
    try {
      await started;
      await vi.waitFor(() => expect(host.isGenerationActive("user:healthy", "fingerprint-one")).toBe(true), { timeout: 100 });
      expect(healthy).toHaveBeenCalledTimes(1);
      expect(host.isGenerationActive("user:slow", "fingerprint-one")).toBe(false);
      if (safeMode) {
        await runtime.sync([], true);
        expect(host.isGenerationActive("user:healthy", "fingerprint-one")).toBe(false);
      }
      release();
      expect(await pending).toEqual([]);
      expect(host.isGenerationActive("user:slow", "fingerprint-one")).toBe(!safeMode);
    } finally {
      release();
      await pending;
      await runtime.sync([]);
    }
  });

  it("orders dependency activation, reloads unchanged consumers, and cleans up in reverse", async () => {
    const events: string[] = [];
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (url) => ({
      activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(`start:${url}`);
        api.registerCleanup(() => events.push(`stop:${url}`));
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = dependencyPlugin("consumer", ["provider"]);
    expect(await runtime.sync([consumer, provider])).toEqual([]);
    expect(events).toEqual(["start:user:provider/fingerprint-one", "start:user:consumer/fingerprint-one"]);
    events.length = 0;
    await runtime.sync([consumer, { ...provider, fingerprint: "two" }]);
    expect(events.indexOf("stop:user:consumer/fingerprint-one")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
    expect(events.at(-1)).toBe("start:user:consumer/fingerprint-one");
    events.length = 0;
    await runtime.sync([consumer, { ...provider, enabled: false }]);
    expect(events).toEqual(["stop:user:consumer/fingerprint-one", "stop:user:provider/two"]);
  });

  it.each(["required", "transitive", "optional"])("cleans up a pending %s consumer before replacing its provider", async (relationship) => {
    const events: string[] = [];
    let release!: () => void;
    let signalStarted!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const started = new Promise<void>((resolve) => { signalStarted = resolve; });
    let consumerStarts = 0;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      async activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(`start:${url}`);
        api.registerCleanup(() => events.push(`stop:${url}`));
        if (url.startsWith("user:consumer/") && ++consumerStarts === 1) {
          signalStarted();
          await gate;
        }
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = relationship === "optional"
      ? { ...dependencyPlugin("consumer"), resolved_dependencies: ["provider"] }
      : dependencyPlugin("consumer", [relationship === "transitive" ? "bridge" : "provider"]);
    const bridge = relationship === "transitive" ? [dependencyPlugin("bridge", ["provider"])] : [];
    const pending = runtime.sync([consumer, ...bridge, provider]);
    try {
      await started;
      expect(await runtime.sync([consumer, ...bridge, { ...provider, fingerprint: "two" }])).toEqual([]);
      expect(events).toContain("stop:user:consumer/fingerprint-one");
      expect(events).toContain("stop:user:provider/fingerprint-one");
      expect(events.indexOf("stop:user:consumer/fingerprint-one")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
      expect(host.isGenerationActive(consumer.id, consumer.fingerprint!)).toBe(true);
      expect(host.isGenerationActive(provider.id, "two")).toBe(true);
    } finally {
      release();
      await pending;
      await runtime.sync([]);
    }
  });

  it("preserves the current pending consumer across same-fingerprint supersession without blocking independent plugins", async () => {
    const events: string[] = [];
    const releases: (() => void)[] = [];
    const gates = [0, 1].map(() => new Promise<void>((resolve) => { releases.push(resolve); }));
    const signalStarted: (() => void)[] = [];
    const started = [0, 1].map(() => new Promise<void>((resolve) => { signalStarted.push(resolve); }));
    let consumerStarts = 0;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      async activate(api: { registerCleanup(fn: () => void): void }) {
        if (url.startsWith("user:consumer/")) {
          const index = consumerStarts++;
          events.push(`start:consumer:${index}`);
          api.registerCleanup(() => events.push(`stop:consumer:${index}`));
          signalStarted[index]?.();
          await gates[index];
        } else {
          events.push(`start:${url}`);
          api.registerCleanup(() => events.push(`stop:${url}`));
        }
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = dependencyPlugin("consumer", ["provider"]);
    const inventory = [consumer, provider];
    const first = runtime.sync(inventory);
    let second: ReturnType<DesktopPluginRuntime["sync"]> | undefined;
    try {
      await started[0];
      second = runtime.sync([...inventory, dependencyPlugin("independent")]);
      await started[1];
      expect(host.isGenerationActive("user:independent", "fingerprint-one")).toBe(true);
      releases[0]!();
      await first;
      expect(events.filter((event) => event === "start:user:provider/fingerprint-one")).toHaveLength(1);
      expect(events).not.toContain("stop:user:provider/fingerprint-one");
      expect(await runtime.sync([consumer, { ...provider, fingerprint: "two" }, dependencyPlugin("independent")])).toEqual([]);
      expect(events).toContain("stop:consumer:1");
      expect(events).toContain("stop:user:provider/fingerprint-one");
      expect(events.indexOf("stop:consumer:1")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
      expect(host.isGenerationActive(consumer.id, consumer.fingerprint!)).toBe(true);
    } finally {
      releases.forEach((release) => release());
      await Promise.all([first, second]);
      await runtime.sync([]);
    }
  });

  it("blocks missing, cyclic, inactive and transitively unavailable required dependencies", async () => {
    const load = vi.fn(async () => ({ activate() {} }));
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    await runtime.sync([
      dependencyPlugin("missing", ["absent"]), dependencyPlugin("transitive", ["missing"]),
      dependencyPlugin("cycle-a", ["cycle-b"]), dependencyPlugin("cycle-b", ["cycle-a"]),
      { ...dependencyPlugin("inactive"), runtime_state: "inactive" },
      dependencyPlugin("inactive-consumer", ["inactive"]),
    ]);
    expect(load).not.toHaveBeenCalled();
  });

  it("honors core version mismatches without reimplementing version matching in the renderer", async () => {
    const load = vi.fn(async () => ({ activate() {} }));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    const plugin = { ...dependencyPlugin("consumer"), activation_issues: [{ kind: "version_mismatch" as const, related_plugin_id: "provider" }] };
    const read = vi.fn(async ({ id, fingerprint }: { id: string; fingerprint: string }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    }));
    installDesktopModuleLoader(read);
    await runtime.sync([plugin]);
    expect(read).not.toHaveBeenCalled();
    expect(load).not.toHaveBeenCalled();
  });

  it("cascades desktop startup failure while independent plugins still activate", async () => {
    const activated: string[] = [];
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (id) => ({
      activate() {
        if (id === "user:provider") throw new Error("provider failed");
        activated.push(id);
      },
    }));
    const failures = await runtime.sync([
      dependencyPlugin("consumer", ["provider"]), dependencyPlugin("provider"), dependencyPlugin("independent"),
    ]);
    expect(activated).toEqual(["user:independent"]);
    expect(failures.map((failure) => failure.pluginId).sort()).toEqual(["user:consumer", "user:provider"]);
    await runtime.sync([]);
  });

  it("keeps optional failure nonblocking and stable, then rebinds on provider recovery", async () => {
    const events: string[] = [];
    let fail = true;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (id) => ({
      activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(id);
        if (id === "user:provider" && fail) throw new Error("optional provider failed");
        api.registerCleanup(() => events.push(`stop:${id}`));
      },
    }));
    const consumer = { ...dependencyPlugin("consumer"), resolved_dependencies: ["provider"] };
    const inventory = [consumer, dependencyPlugin("provider")];
    const failures = await runtime.sync(inventory);
    expect(events).toEqual(["user:provider", "user:consumer"]);
    expect(failures.map((failure) => failure.pluginId)).toEqual(["user:provider"]);
    events.length = 0;
    await runtime.sync(inventory);
    expect(events).toEqual(["user:provider"]);
    fail = false;
    events.length = 0;
    await runtime.sync(inventory);
    expect(events).toEqual(["user:provider", "stop:user:consumer", "user:consumer"]);
    events.length = 0;
    await runtime.sync(inventory);
    expect(events).toEqual([]);
    await runtime.sync([]);
  });

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
    const plugin = inventoryPlugin();

    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(read).not.toHaveBeenCalled();
    expect(activate).not.toHaveBeenCalled();

    // Core marks desktop-only plugins active even without a backend process.
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(read).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(2);
    await runtime.sync([]);
  });

  it.each(["disable", "safe-mode"])("removes active contributions before conflict preferences settle on %s", async (action) => {
    const cleanup = vi.fn();
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js",
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async () => ({
      activate(api: { registerCleanup(fn: () => void): void }) { api.registerCleanup(cleanup); },
    }));
    const plugin = inventoryPlugin();
    await runtime.sync([plugin]);
    let finish!: (value: Record<string, string>) => void;
    window.wuu!.getPluginConflictPreferences = vi.fn(() => new Promise<Record<string, string>>((resolve) => { finish = resolve; }));
    const pending = runtime.sync(action === "disable" ? [{ ...plugin, enabled: false }] : [plugin], action === "safe-mode");
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(host.isGenerationActive(plugin.id, plugin.fingerprint!)).toBe(false);
    finish({});
    expect(await pending).toEqual([]);
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

  it.each(["import", "register"])("keeps the last working generation after replacement %s fails", async (failure) => {
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `wuu-plugin://module/${fingerprint}.js`,
    })));
    const cleanup = vi.fn();
    const load = vi.fn(async (url: string) => {
      if (url.includes("fingerprint-two")) {
        if (failure === "import") throw new Error("replacement import failed");
        return { activate() { throw new Error("replacement registration failed"); } };
      }
      return { activate(api: {
        registerCleanup(fn: () => void): void;
        registerCommand(command: { id: string; title: string; execute(): string }): unknown;
      }) {
        api.registerCleanup(cleanup);
        api.registerCommand({ id: "working", title: "Working", execute: () => "last good" });
      } };
    });
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, load);
    const plugin = inventoryPlugin();
    expect(await runtime.sync([plugin])).toEqual([]);
    const replacement = { ...plugin, fingerprint: "fingerprint-two" };
    expect(await runtime.sync([replacement])).toEqual([
      expect.objectContaining({ pluginId: plugin.id, fingerprint: "fingerprint-two" }),
    ]);
    expect(cleanup).not.toHaveBeenCalled();
    expect(host.isGenerationActive(plugin.id, plugin.fingerprint!)).toBe(true);
    expect(host.getCommands()[0]!.execute()).toBe("last good");
    // Returning to the working inventory does not activate an already live run again.
    expect(await runtime.sync([plugin])).toEqual([]);
    expect(load).toHaveBeenCalledTimes(2);
    await runtime.sync([replacement], true);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(host.getCommands()).toEqual([]);
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
    runtime_state: "active",
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

function dependencyPlugin(id: string, requires: string[] = []): ExtensionInventoryRecord {
  const plugin = inventoryPlugin();
  return { ...plugin, id: `user:${id}`, provenance: { ...plugin.provenance, plugin_id: id }, requires };
}
