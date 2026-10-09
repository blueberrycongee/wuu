import * as React from "react";
import { afterEach, describe, expect, it } from "vitest";

import { PRESENTATION_TARGETS } from "../../shared/workbench";

import {
  PLUGIN_SLOT_IDS,
  PluginHost,
  type Disposable,
  type PluginGenerationApi,
} from "./PluginHost";

afterEach(() => {
  for (const style of document.head.querySelectorAll("style[data-wuu-plugin-id]")) {
    style.remove();
  }
});

describe("PluginHost", () => {
  it("publishes the fixed slots and orders registrations by order, plugin id, and contribution id", async () => {
    expect(PLUGIN_SLOT_IDS).toEqual([
      "sidebar.primary",
      "sidebar.footer",
      "workspace.header",
      "conversation.header",
      "conversation.message.before",
      "conversation.message.after",
      "composer.above",
      "composer.toolbar",
    ]);

    const host = new PluginHost({ react: React });
    let injectedReact: typeof React | undefined;

    await host.activateGeneration({
      pluginId: "zeta",
      generation: "one",
      register(api) {
        injectedReact = api.react;
        api.registerSlot("composer.toolbar", contribution("z-last", 10));
        api.registerSlot("composer.toolbar", contribution("z-first", 0));
        api.registerCommand(command("z-command", 10));
        api.registerLocale({ id: "base", locale: "en", entries: { shared: "zeta", zeta: "Zeta" } });
        api.registerStyle({ id: "z-style", css: ".zeta {}", order: 10 });
      },
    });
    await host.activateGeneration({
      pluginId: "alpha",
      generation: "one",
      register(api) {
        api.registerSlot("composer.toolbar", contribution("b", 0));
        api.registerSlot("composer.toolbar", contribution("a", 0));
        api.registerCommand(command("alpha-command", 0));
        api.registerLocale({ id: "base", locale: "en", entries: { alpha: "Alpha", shared: "alpha" } });
        api.registerStyle({ id: "alpha-style", css: ".alpha {}", order: 0 });
      },
    });

    expect(injectedReact).toBe(React);
    expect(host.getSlotSnapshot("composer.toolbar").map(qualifiedId)).toEqual([
      "alpha:a",
      "alpha:b",
      "zeta:z-first",
      "zeta:z-last",
    ]);
    expect(host.getCommands().map(qualifiedId)).toEqual(["alpha:alpha-command", "zeta:z-command"]);
    expect(host.getLocaleEntries("en")).toEqual({ alpha: "Alpha", shared: "zeta", zeta: "Zeta" });
    expect([...document.head.querySelectorAll<HTMLStyleElement>("style[data-wuu-plugin-id]")]
      .map((style) => `${style.dataset.wuuPluginId}:${style.dataset.wuuPluginStyle}`))
      .toEqual(["alpha:alpha-style", "zeta:z-style"]);
  });

  it("tracks the active conversation thread id and trims it", () => {
    const host = new PluginHost({ react: React });
    expect(host.getActiveConversationThreadId()).toBeUndefined();
    host.setActiveConversationThread(" thread-123 ");
    expect(host.getActiveConversationThreadId()).toBe("thread-123");
    host.setActiveConversationThread(undefined);
    expect(host.getActiveConversationThreadId()).toBeUndefined();
  });

  it("lists threads through the host capability while the generation is active", async () => {
    const seen: string[] = [];
    const host = new PluginHost({
      react: React,
      listThreads: async (root) => {
        seen.push(root);
        return [{ id: "t-1", title: "Daily brief", updatedAt: "2026-09-01T00:00:00Z" }];
      },
    });
    let api: PluginGenerationApi | undefined;
    const disposable = await host.activateGeneration({
      pluginId: "zeta",
      generation: "one",
      register(generationApi) {
        api = generationApi;
      },
    });
    await expect(api?.listThreads("/tmp/ws")).resolves.toEqual([
      { id: "t-1", title: "Daily brief", updatedAt: "2026-09-01T00:00:00Z" },
    ]);
    expect(seen).toEqual(["/tmp/ws"]);
    disposable.dispose();
    await expect(api?.listThreads("/tmp/ws")).rejects.toThrow("no longer active");
  });

  it("rejects thread listing when the host provides no thread lister", async () => {
    const host = new PluginHost({ react: React });
    let api: PluginGenerationApi | undefined;
    await host.activateGeneration({
      pluginId: "zeta",
      generation: "one",
      register(generationApi) {
        api = generationApi;
      },
    });
    await expect(api?.listThreads("/tmp/ws")).rejects.toThrow("Thread listing is unavailable");
  });

  it("records replace conflicts, preserves the default winner, and applies a user preference", async () => {
    const host = new PluginHost({ react: React });
    for (const pluginId of ["alpha", "zeta"]) {
      await host.activateGeneration({
        pluginId,
        generation: "one",
        register(api) {
          api.registerSurface("conversation.timeline", {
            id: `${pluginId}-main`,
            mode: "replace",
            render: (_context, fallback) => fallback,
          });
        },
      });
    }

    expect(host.getSurfaceSnapshot("conversation.timeline").at(-1)?.pluginId).toBe("zeta");
    expect(host.getConflicts()).toEqual([
      expect.objectContaining({
        key: "surface:conversation.timeline",
        kind: "surface",
        target: "conversation.timeline",
        winnerPluginId: "zeta",
      }),
    ]);

    host.setConflictPreference("surface:conversation.timeline", "alpha");
    expect(host.getSurfaceSnapshot("conversation.timeline").at(-1)?.pluginId).toBe("alpha");
    expect(host.getConflicts()[0]?.winnerPluginId).toBe("alpha");
  });

  it("arbitrates presenter replacements per target and key without treating wrappers as conflicts", async () => {
    const host = new PluginHost({ react: React });
    for (const pluginId of ["alpha", "zeta"]) {
      await host.activateGeneration({
        pluginId,
        generation: "one",
        register(api) {
          api.registerPresenter({
            id: `${pluginId}-replace`, target: "conversation.item", key: "message",
            mode: "replace", priority: pluginId === "zeta" ? 10 : 0, render: () => null,
          });
          api.registerPresenter({
            id: `${pluginId}-wrap`, target: "conversation.item", key: "message",
            mode: "wrap", render: ({ fallback }) => fallback,
          });
        },
      });
    }

    const conflict = host.getConflicts().find((item) => item.kind === "presenter");
    expect(conflict).toEqual(expect.objectContaining({
      key: "presenter:conversation.item:message",
      winnerPluginId: "zeta",
    }));
    expect(conflict?.candidates).toHaveLength(2);
    host.setConflictPreference("presenter:conversation.item:message", "alpha");
    expect(host.getPresenters("conversation.item", "message").filter((item) => item.mode === "replace").at(-1)?.pluginId).toBe("alpha");
  });

  it("replaces a generation and makes stale generation handles harmless", async () => {
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    const oldHandle = await host.activateGeneration({
      pluginId: "notes",
      generation: "old",
      register(api) {
        registerCompleteGeneration(api, "old");
        api.registerCleanup(() => cleanup.push("old"));
      },
    });

    const newHandle = await host.activateGeneration({
      pluginId: "notes",
      generation: "new",
      register(api) {
        registerCompleteGeneration(api, "new");
        api.registerCleanup(() => cleanup.push("new"));
      },
    });

    expect(cleanup).toEqual(["old"]);
    expect(host.getSlotSnapshot("workspace.header").map((item) => item.generation)).toEqual(["new"]);
    expect(host.getCommands().map((item) => item.id)).toEqual(["new-command"]);
    expect(host.getLocaleEntries("en")).toEqual({ label: "new" });
    expect(document.head.querySelector("style[data-wuu-plugin-generation=old]")).toBeNull();
    expect(document.head.querySelector("style[data-wuu-plugin-generation=new]")).not.toBeNull();

    oldHandle.dispose();
    expect(host.getCommands()).toHaveLength(1);

    newHandle.dispose();
    expect(cleanup).toEqual(["old", "new"]);
    expect(host.getSlotSnapshot("workspace.header")).toEqual([]);
    expect(host.getCommands()).toEqual([]);
    expect(host.getLocaleEntries("en")).toEqual({});
    expect(document.head.querySelector("style[data-wuu-plugin-id=notes]")).toBeNull();
  });

  it.each(["unload", "replace"])("immediately cleans up a pending generation on %s", async (action) => {
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    let api!: PluginGenerationApi;
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => { finish = resolve; });
    const pending = host.activateGeneration({
      pluginId: "pending", generation: "one",
      async register(value) {
        api = value;
        api.registerCleanup(() => { cleanup.push("one"); });
        await gate;
      },
    });
    if (action === "unload") host.unload("pending");
    else await host.activateGeneration({ pluginId: "pending", generation: "two", register() {} });

    expect(cleanup).toEqual(["one"]);
    expect(() => api.registerCommand(command("late"))).toThrow("no longer registering");
    expect(() => api.onHostEvent(() => {})).toThrow("no longer active");
    finish();
    await expect(pending).rejects.toThrow("superseded");
    expect(cleanup).toEqual(["one"]);
    expect(host.getGenerationDiagnostics("pending", "one")).toEqual([]);
    host.unload("pending");
  });

  it.each(["runtime", "workspaces", "threads"])("rejects stale %s results even when the replacement reuses the same fingerprint", async (operation) => {
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => { finish = resolve; });
    const host = new PluginHost({
      react: React,
      invokeRuntime: async () => { await gate; return { ok: true }; },
      listWorkspaces: async () => { await gate; return { workspaces: [] }; },
      listThreads: async () => { await gate; return []; },
    });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "async", generation: "same", register(value) { api = value; } });
    const request = operation === "runtime" ? api.invokeRuntime("read")
      : operation === "workspaces" ? api.listWorkspaces() : api.listThreads("/workspace");
    await host.activateGeneration({ pluginId: "async", generation: "same", register() {} });
    finish();
    await expect(request).rejects.toThrow("no longer active");
    host.unload("async");
  });

  it("rejects retained command callbacks after their generation is replaced", async () => {
    const host = new PluginHost({ react: React });
    const executed: string[] = [];
    await host.activateGeneration({ pluginId: "commands", generation: "same", register(api) {
      api.registerCommand({ id: "run", title: "Run", execute: () => { executed.push("old"); } });
      api.registerSlot("workspace.header", { id: "label", render: () => "old" });
    } });
    const retained = host.getCommands()[0]!;
    await host.activateGeneration({ pluginId: "commands", generation: "same", register(api) {
      api.registerCommand({ id: "run", title: "Run", execute: () => { executed.push("new"); } });
      api.registerSlot("workspace.header", { id: "label", render: () => "new" });
    } });
    expect(() => retained.execute()).toThrow("no longer active");
    host.getCommands()[0]!.execute();
    expect(executed).toEqual(["new"]);
    expect(host.getSlotSnapshot("workspace.header")[0]!.render({})).toBe("new");
    host.unload("commands");
  });

  it("keeps a staged replacement private until its registration callback completes", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "atomic",
      generation: "old",
      register(api) {
        api.registerCommand(command("old"));
      },
    });

    let finishRegistration: (() => void) | undefined;
    const registrationGate = new Promise<void>((resolve) => {
      finishRegistration = resolve;
    });
    const replacement = host.activateGeneration({
      pluginId: "atomic",
      generation: "new",
      async register(api) {
        api.registerCommand(command("new"));
        await registrationGate;
      },
    });

    expect(host.getCommands().map((item) => item.id)).toEqual(["old"]);
    finishRegistration?.();
    await replacement;
    expect(host.getCommands().map((item) => item.id)).toEqual(["new"]);
  });

  it("rolls back partial registration in reverse order and preserves the active generation", async () => {
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    await host.activateGeneration({
      pluginId: "review",
      generation: "stable",
      register(api) {
        api.registerCommand(command("stable"));
        api.registerCleanup(() => cleanup.push("stable"));
      },
    });

    const activation = host.activateGeneration({
      pluginId: "review",
      generation: "broken",
      register(api) {
        api.registerCleanup(() => cleanup.push("first"));
        api.registerSlot("sidebar.footer", contribution("partial"));
        api.registerCleanup(() => cleanup.push("second"));
        api.registerStyle({ id: "partial", css: ".partial {}" });
        throw new Error("registration stopped");
      },
    });

    await expect(activation).rejects.toThrow("registration stopped");
    expect(cleanup).toEqual(["second", "first"]);
    expect(host.getCommands().map((item) => item.id)).toEqual(["stable"]);
    expect(host.getSlotSnapshot("sidebar.footer")).toEqual([]);
    expect(document.head.querySelector("style[data-wuu-plugin-generation=broken]")).toBeNull();
    expect(host.getGenerationDiagnostics("review", "broken").map((item) => item.kind)).toEqual(["activation"]);
  });

  it("removes individual active registrations through idempotent disposables", async () => {
    const host = new PluginHost({ react: React });
    const disposables: Disposable[] = [];
    const cleanup: string[] = [];
    await host.activateGeneration({
      pluginId: "removable",
      generation: "one",
      register(api) {
        disposables.push(api.registerSlot("sidebar.footer", contribution("footer")));
        disposables.push(api.registerCommand(command("action")));
        disposables.push(api.registerStyle({ id: "theme", css: ".theme {}" }));
        disposables.push(api.registerLocale({ id: "copy", locale: "en", entries: { copy: "Copy" } }));
        disposables.push(api.registerCleanup(() => cleanup.push("cleanup")));
      },
    });

    for (const disposable of disposables) {
      disposable.dispose();
      disposable.dispose();
    }

    expect(host.getSlotSnapshot("sidebar.footer")).toEqual([]);
    expect(host.getCommands()).toEqual([]);
    expect(host.getLocaleEntries("en")).toEqual({});
    expect(document.head.querySelector("style[data-wuu-plugin-id=removable]")).toBeNull();
    expect(cleanup).toEqual(["cleanup"]);
  });

  it("disables a plugin by removing every host-owned registration", async () => {
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    await host.activateGeneration({
      pluginId: "disable-me",
      generation: "one",
      register(api) {
        registerCompleteGeneration(api, "one");
        api.registerCleanup(() => cleanup.push("disabled"));
      },
    });

    host.disable("disable-me");

    expect(host.getSlotSnapshot("workspace.header")).toEqual([]);
    expect(host.getCommands()).toEqual([]);
    expect(host.getLocaleEntries("en")).toEqual({});
    expect(document.head.querySelector("style[data-wuu-plugin-id=disable-me]")).toBeNull();
    expect(cleanup).toEqual(["disabled"]);
  });

  it("publishes workbench registrations atomically and removes them with their generation", async () => {
    const host = new PluginHost({ react: React });
    const notifications: number[] = [];
    host.subscribe(() => notifications.push(notifications.length + 1));

    await host.activateGeneration({
      pluginId: "workbench",
      generation: "one",
      register(api) {
        api.registerViewType({
          id: "dashboard",
          title: "Dashboard",
          defaultRegion: "primary",
          persistence: "durable",
          render: () => null,
        });
        api.registerViewPlacement({
          id: "dashboard-pane",
          region: "primary",
          view: "dashboard",
        });
        api.registerRenderer({
          id: "result",
          category: "tool-result",
          match: "application/example",
          priority: 20,
          render: () => null,
        });
        api.registerThemeTokens({
          theme: "night",
          base: "dark",
          tokens: { "--wuu-paper": "#111" },
        });
        api.registerCSSSnippet({ id: "density", css: ":root { --plugin-density: 1; }" });
        api.registerStatusItem({ id: "ready", label: "Ready", priority: 10 });
      },
    });

    expect(host.getViewTypes().map(qualifiedId)).toEqual(["workbench:dashboard"]);
    expect(host.getViewPlacements().map(qualifiedId)).toEqual(["workbench:dashboard-pane"]);
    expect(host.getRenderers("tool-result").map(qualifiedId)).toEqual(["workbench:result"]);
    expect(host.getThemeTokens("night").map(qualifiedId)).toEqual(["workbench:theme:night"]);
    expect(host.getStatusItems().map(qualifiedId)).toEqual(["workbench:ready"]);
    expect(document.head.querySelector("style[data-wuu-plugin-css-snippet=density]")?.textContent)
      .toContain("--plugin-density");
    expect(notifications).toHaveLength(1);

    host.disable("workbench");

    expect(host.getViewTypes()).toEqual([]);
    expect(host.getViewPlacements()).toEqual([]);
    expect(host.getRenderers()).toEqual([]);
    expect(host.getThemeTokens()).toEqual([]);
    expect(host.getStatusItems()).toEqual([]);
    expect(document.head.querySelector("style[data-wuu-plugin-css-snippet]")).toBeNull();
    expect(notifications).toHaveLength(2);
  });

  it("validates stable placement regions and orders defaults by priority", async () => {
    const host = new PluginHost({ react: React });

    await host.activateGeneration({
      pluginId: "placements",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "views.high", title: "High", render: () => null });
        api.registerViewType({ id: "views.low", title: "Low", render: () => null });
        api.registerViewPlacement({
          id: "high",
          view: "views.high",
          region: "primary",
          priority: 20,
        });
        api.registerViewPlacement({
          id: "low",
          view: "views.low",
          region: "primary",
          priority: 10,
        });
      },
    });

    expect(host.getViewPlacements().map((placement) => placement.id)).toEqual(["low", "high"]);
    expect(host.getViewPlacements().every((placement) => placement.region === "primary")).toBe(true);

    await expect(host.activateGeneration({
      pluginId: "invalid-placement",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "views.overlay", title: "Overlay", render: () => null });
        api.registerViewPlacement({
          id: "overlay",
          view: "views.overlay",
          region: "physical-right" as never,
        });
      },
    })).rejects.toThrow("Unsupported plugin View placement region: physical-right");

    await expect(host.activateGeneration({
      pluginId: "missing-placement-view",
      generation: "one",
      register(api) {
        api.registerViewPlacement({
          id: "missing",
          view: "views.missing",
          region: "primary",
        });
      },
    })).rejects.toThrow(
      "Plugin View placement missing references an unregistered View: views.missing",
    );
  });

  it("publishes manifest View entries only after their View is registered", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "workbench-product",
      generation: "one",
      contributions: {
        navigation: [{ id: "nav", view: "dashboard", title: "Dashboard", order: 20 }],
        workspace_tools: [{ id: "tool", view: "dashboard", title: "Inspector", order: 10 }],
        settings_pages: [{ id: "settings", view: "dashboard", title: "Advanced", order: 30 }],
      },
      register(api) {
        api.registerViewType({ id: "dashboard", title: "Dashboard", render: () => null });
        api.registerPresenter({
          id: "legacy-presenter",
          target: "app.status",
          mode: "wrap",
          render: ({ fallback }) => fallback,
        });
      },
    });

    expect(host.getNavigationEntries()).toEqual([
      expect.objectContaining({ pluginId: "workbench-product", id: "nav", view: "dashboard" }),
    ]);
    expect(host.getWorkspaceTools()[0]).toMatchObject({ id: "tool", title: "Inspector" });
    expect(host.getSettingsPages()[0]).toMatchObject({ id: "settings", title: "Advanced" });

    await expect(host.activateGeneration({
      pluginId: "missing-entry-view",
      generation: "one",
      contributions: {
        navigation: [{ id: "missing", view: "not-registered", title: "Missing" }],
      },
      register() {},
    })).rejects.toThrow(
      "Manifest View entry missing references an unregistered View: not-registered",
    );
  });

  it("rejects unpublished global theme tokens during candidate activation", async () => {
    const host = new PluginHost({ react: React });

    await expect(host.activateGeneration({
      pluginId: "unsafe-theme",
      generation: "one",
      register(api) {
        api.registerThemeTokens({
          theme: "dark",
          base: "dark",
          tokens: { "--wuu-private-component-color": "red" } as never,
        });
      },
    })).rejects.toThrow("Unsupported plugin theme token");

    expect(host.getThemeTokens()).toEqual([]);
  });

  it("rejects UI registrations that are missing from the manifest declarations", async () => {
    const host = new PluginHost({ react: React });

    await expect(host.activateGeneration({
      pluginId: "undeclared",
      generation: "one",
      contributions: { slots: [] },
      register(api) {
        api.registerSlot("composer.above", contribution("status"));
      },
    })).rejects.toThrow("slot registration status is not declared");
  });

  it("rejects manifest UI contributions that activation does not register", async () => {
    const host = new PluginHost({ react: React });

    await expect(host.activateGeneration({
      pluginId: "missing-registration",
      generation: "one",
      contributions: {
        surfaces: [{ id: "frame", target: "conversation.timeline", mode: "wrap" }],
      },
      register() {},
    })).rejects.toThrow("Manifest surface contribution frame was not registered");
  });

  it("validates keyed tool activity presenters and rejects another plugin's active key", async () => {
    const host = new PluginHost({ react: React });
    for (const definition of [
      { id: "", key: "tool.echo", render: () => null },
      { id: "echo", key: "", render: () => null },
      { id: "echo", key: "tool.echo", render: null },
    ]) {
      await expect(host.activateGeneration({
        pluginId: `invalid-${definition.id || definition.key}`,
        generation: "one",
        register(api) { api.registerToolActivityPresenter(definition as never); },
      })).rejects.toThrow();
    }

    await host.activateGeneration({
      pluginId: "owner",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => null });
      },
    });
    await expect(host.activateGeneration({
      pluginId: "candidate",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({ id: "other", key: "tool.echo", render: () => null });
      },
    })).rejects.toThrow("already owned by another plugin");
    expect(host.getToolActivityPresenter("tool.echo")?.pluginId).toBe("owner");
  });

  it("keeps candidates private, swaps replacements atomically, and preserves the active presenter on failure", async () => {
    const host = new PluginHost({ react: React });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const activation = host.activateGeneration({
      pluginId: "owner",
      generation: "one",
      async register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => "one" });
        await gate;
      },
    });
    expect(host.getToolActivityPresenters()).toEqual([]);
    await expect(host.activateGeneration({
      pluginId: "other",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => "other" });
      },
    })).rejects.toThrow("already owned by another plugin");
    release();
    await activation;
    expect(host.getToolActivityPresenter("tool.echo")?.generation).toBe("one");

    await host.activateGeneration({
      pluginId: "owner",
      generation: "two",
      register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => "two" });
      },
    });
    expect(host.getToolActivityPresenter("tool.echo")?.generation).toBe("two");

    await expect(host.activateGeneration({
      pluginId: "owner",
      generation: "broken",
      register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => "broken" });
        throw new Error("stop");
      },
    })).rejects.toThrow("stop");
    expect(host.getToolActivityPresenter("tool.echo")?.generation).toBe("two");
  });

  it("updates subscribers for presenter disposal and unload", async () => {
    const host = new PluginHost({ react: React });
    const notifications: number[] = [];
    let registration!: Disposable;
    host.subscribe(() => notifications.push(notifications.length));
    await host.activateGeneration({
      pluginId: "owner",
      generation: "one",
      register(api) {
        registration = api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => null });
      },
    });
    const published = host.getToolActivityPresenters();
    expect(Object.isFrozen(published)).toBe(true);
    expect(Object.isFrozen(published[0])).toBe(true);
    registration.dispose();
    expect(host.getToolActivityPresenters()).toEqual([]);
    expect(notifications).toHaveLength(2);

    await host.activateGeneration({
      pluginId: "owner",
      generation: "two",
      register(api) {
        api.registerToolActivityPresenter({ id: "echo", key: "tool.echo", render: () => null });
      },
    });
    host.unload("owner");
    expect(host.getToolActivityPresenter("tool.echo")).toBeUndefined();
  });

  it("publishes stable exact-match presenter snapshots in deterministic order", async () => {
    expect(PRESENTATION_TARGETS).toEqual([
      "conversation.item", "conversation.process", "conversation.tool-activity",
      "conversation.composer", "header.conversation", "header.workspace",
      "navigation.primary", "app.status", "content.preview", "settings",
    ]);
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "zeta", generation: "one", register(api) {
        api.registerPresenter({ id: "high", target: "conversation.item", key: "message", priority: 10, render: () => null });
        api.registerPresenter({ id: "other-key", target: "conversation.item", key: "other", render: () => null });
      },
    });
    await host.activateGeneration({
      pluginId: "alpha", generation: "one", register(api) {
        api.registerPresenter({ id: "low", target: "conversation.item", key: "message", priority: 0, mode: "wrap", render: () => null });
      },
    });
    const snapshot = host.getPresenters("conversation.item", "message");
    expect(snapshot.map(qualifiedId)).toEqual(["alpha:low", "zeta:high"]);
    expect(host.getPresenters("conversation.item", "message")).toBe(snapshot);
    expect(host.getPresenters("conversation.process", "message")).toEqual([]);
    expect(host.getPresenters("conversation.item")).toEqual([]);
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot[0])).toBe(true);
  });

  it("validates generalized presenter registrations", async () => {
    const host = new PluginHost({ react: React });
    await expect(host.activateGeneration({ pluginId: "invalid", generation: "one", register(api) {
      api.registerPresenter({ id: "bad", target: "conversation.item", mode: "append" as never, render: () => null });
    } })).rejects.toThrow("Unsupported presenter mode");
    await expect(host.activateGeneration({ pluginId: "invalid", generation: "two", register(api) {
      api.registerPresenter({ id: "bad", target: " ", render: () => null });
    } })).rejects.toThrow("target must not be empty");
    expect(host.getPresenters("conversation.item")).toEqual([]);
  });

  it("binds runtime requests to the active plugin generation", async () => {
    const requests: unknown[] = [];
    const host = new PluginHost({
      react: React,
      invokeRuntime: async (request) => {
        requests.push(request);
        return { ok: true };
      },
    });
    let firstApi: Parameters<Parameters<typeof host.activateGeneration>[0]["register"]>[0] | undefined;
    await host.activateGeneration({
      pluginId: "runtime-owner",
      generation: "one",
      register(api) { firstApi = api; },
    });
    await expect(firstApi?.invokeRuntime("summary.get", { threadId: "thread-1" })).resolves.toEqual({ ok: true });
    expect(requests).toEqual([{
      pluginId: "runtime-owner",
      generation: "one",
      method: "summary.get",
      input: { threadId: "thread-1" },
    }]);

    await host.activateGeneration({
      pluginId: "runtime-owner",
      generation: "two",
      register() {},
    });
    await expect(firstApi?.invokeRuntime("summary.get")).rejects.toThrow("no longer active");
  });

  it("delivers host events only to the active generation", async () => {
    const host = new PluginHost({ react: React });
    const received: unknown[] = [];
    let firstApi: PluginGenerationApi | undefined;
    await host.activateGeneration({
      pluginId: "event-owner",
      generation: "one",
      register(api) { firstApi = api; },
    });
    const subscription = firstApi?.onHostEvent((event) => received.push(event));
    host.publishHostEvent({ kind: "notification", method: "turn/completed" });
    expect(received).toEqual([{ kind: "notification", method: "turn/completed" }]);

    subscription?.dispose();
    host.publishHostEvent({ kind: "notification", method: "turn/ignored" });
    expect(received).toHaveLength(1);

    await host.activateGeneration({
      pluginId: "event-owner",
      generation: "two",
      register() {},
    });
    expect(() => firstApi?.onHostEvent(() => {})).toThrow("no longer active");
    host.publishHostEvent({ kind: "notification", method: "turn/started" });
    expect(received).toHaveLength(1);
  });

  it.each(["one", "two"])("ignores delayed render reports from a retired run when replacing with %s", async (generation) => {
    const host = new PluginHost({ react: React });
    const register = (api: PluginGenerationApi) => {
      api.registerSlot("composer.above", contribution("status"));
    };
    await host.activateGeneration({ pluginId: "render", generation: "one", register });
    const retired = host.getSlotSnapshot("composer.above")[0]!;
    await host.activateGeneration({ pluginId: "render", generation, register });
    host.recordRenderFailure(retired, { slotId: "composer.above" }, new Error("late failure"));
    expect(host.getGenerationDiagnostics("render", "one")).toEqual([]);
    const current = host.getSlotSnapshot("composer.above")[0]!;
    host.recordRenderFailure(current, { slotId: "composer.above" }, new Error("current failure"));
    expect(host.getGenerationDiagnostics("render", generation)).toHaveLength(1);
    host.unload("render");
  });

  it("deduplicates repeated diagnostics and clears them after a successful reactivation", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "recoverable",
      generation: "one",
      register(api) { api.registerSlot("composer.above", contribution("status")); },
    });
    const contributionRecord = host.getSlotSnapshot("composer.above")[0];
    host.recordRenderFailure(contributionRecord, { slotId: "composer.above" }, new Error("render boom"));
    host.recordRenderFailure(contributionRecord, { slotId: "composer.above" }, new Error("render boom"));
    expect(host.getGenerationDiagnostics("recoverable", "one")).toHaveLength(1);

    await host.activateGeneration({
      pluginId: "recoverable",
      generation: "one",
      register(api) { api.registerSlot("composer.above", contribution("status")); },
    });
    expect(host.getGenerationDiagnostics("recoverable", "one")).toEqual([]);
  });
});

function contribution(id: string, order = 0) {
  return {
    id,
    order,
    render: () => React.createElement("span", null, id),
  };
}

function command(id: string, order = 0) {
  return {
    id,
    order,
    title: id,
    execute: () => id,
  };
}

function qualifiedId(item: { pluginId: string; id: string }): string {
  return `${item.pluginId}:${item.id}`;
}

function registerCompleteGeneration(api: PluginGenerationApi, label: string): void {
  api.registerSlot("workspace.header", contribution(`${label}-slot`));
  api.registerCommand(command(`${label}-command`));
  api.registerStyle({ id: `${label}-style`, css: `.${label} {}` });
  api.registerLocale({ id: `${label}-copy`, locale: "en", entries: { label } });
}
