import * as React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PRESENTATION_TARGETS, type PluginCommandActionContext, type ComposerInlineCompletionProviderHandle, type ComposerInlineCompletionInteraction } from "../../shared/workbench";

import {
  PluginHost,
  type Disposable,
  type PluginGenerationApi,
} from "./PluginHost";

afterEach(() => {
  vi.useRealTimers();
  for (const style of document.head.querySelectorAll("style[data-wuu-plugin-id]")) {
    style.remove();
  }
});

describe("PluginHost", () => {
  it("orders registrations by order, plugin id, and contribution id", async () => {
    const host = new PluginHost({ react: React });
    let injectedReact: typeof React | undefined;

    await host.activateGeneration({
      pluginId: "zeta",
      generation: "one",
      register(api) {
        injectedReact = api.react;
        api.registerUIContribution({ target: "fixture/composer.toolbar", ...contribution("z-last", 10) });
        api.registerUIContribution({ target: "fixture/composer.toolbar", ...contribution("z-first", 0) });
        api.registerCommand(command("z-command", 10));
        api.registerLocale({ id: "base", locale: "en", entries: { shared: "zeta", zeta: "Zeta" } });
        api.registerStyle({ id: "z-style", css: ".zeta {}", order: 10 });
      },
    });
    await host.activateGeneration({
      pluginId: "alpha",
      generation: "one",
      register(api) {
        api.registerUIContribution({ target: "fixture/composer.toolbar", ...contribution("b", 0) });
        api.registerUIContribution({ target: "fixture/composer.toolbar", ...contribution("a", 0) });
        api.registerCommand(command("alpha-command", 0));
        api.registerLocale({ id: "base", locale: "en", entries: { alpha: "Alpha", shared: "alpha" } });
        api.registerStyle({ id: "alpha-style", css: ".alpha {}", order: 0 });
      },
    });

    expect(injectedReact).toBe(React);
    expect(host.getUIContributions("fixture/composer.toolbar").map(qualifiedId)).toEqual([
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
          api.registerPresenter({ target: "conversation.item",
            id: `${pluginId}-main`,
            mode: "replace",
            render: ({ fallback }) => fallback,
          });
        },
      });
    }

    expect(host.getPresenters("conversation.item").at(-1)?.pluginId).toBe("zeta");
    expect(host.getConflicts()).toEqual([
      expect.objectContaining({
        key: "presenter:conversation.item:",
        kind: "presenter",
        target: "conversation.item",
        winnerPluginId: "zeta",
      }),
    ]);

    host.setConflictPreference("presenter:conversation.item:", "alpha");
    expect(host.getPresenters("conversation.item").at(-1)?.pluginId).toBe("alpha");
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
    let completion!: ComposerInlineCompletionProviderHandle;
    const interactions: ComposerInlineCompletionInteraction[] = [];
    const interaction: ComposerInlineCompletionInteraction = { type: "shown", interactionId: "presentation-1",
      suggestionId: "snippet-1", source: { composerId: "input-1", revision: 1, kind: "main" } };
    const oldHandle = await host.activateGeneration({
      pluginId: "notes",
      generation: "old",
      register(api) {
        registerCompleteGeneration(api, "old");
        api.registerCleanup(() => cleanup.push("old"));
        completion = api.registerComposerInlineCompletionProvider({ id: "snippets",
          provideCompletion: () => ({ id: "snippet-1", text: " suffix" }),
          onDidInteract: (event) => { interactions.push(event); },
        });
      },
    });

    const retained = host.getComposerInlineCompletionProviders()[0];
    retained.onDidInteract?.(interaction);
    expect(interactions).toHaveLength(1);
    let refreshes = 0;
    retained.subscribeRefresh(() => { refreshes += 1; });
    completion.refresh();

    const newHandle = await host.activateGeneration({
      pluginId: "notes",
      generation: "new",
      register(api) {
        registerCompleteGeneration(api, "new");
        api.registerCleanup(() => cleanup.push("new"));
      },
    });

    expect(cleanup).toEqual(["old"]);
    expect(host.getUIContributions("fixture/workspace.header").map((item) => item.generation)).toEqual(["new"]);
    expect(host.getCommands().map((item) => item.id)).toEqual(["new-command"]);
    expect(host.getLocaleEntries("en")).toEqual({ label: "new" });
    expect(document.head.querySelector("style[data-wuu-plugin-generation=old]")).toBeNull();
    expect(document.head.querySelector("style[data-wuu-plugin-generation=new]")).not.toBeNull();

    retained.onDidInteract?.(interaction);
    completion.refresh();
    expect(interactions).toHaveLength(1);
    expect(refreshes).toBe(0);
    expect(host.getComposerInlineCompletionProviders()).toEqual([]);
    oldHandle.dispose();
    expect(host.getCommands()).toHaveLength(1);

    newHandle.dispose();
    expect(cleanup).toEqual(["old", "new"]);
    expect(host.getUIContributions("fixture/workspace.header")).toEqual([]);
    expect(host.getCommands()).toEqual([]);
    expect(host.getLocaleEntries("en")).toEqual({});
    expect(document.head.querySelector("style[data-wuu-plugin-id=notes]")).toBeNull();
  });

  it.each(["unload", "replace", "abort"])("settles and cleans up a pending generation on %s", async (action) => {
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    let api!: PluginGenerationApi;
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => { finish = resolve; });
    const controller = new AbortController();
    const pending = host.activateGeneration({
      pluginId: "pending", generation: "one", signal: controller.signal,
      async register(value) {
        api = value;
        api.registerCleanup(() => { cleanup.push("one"); });
        await gate;
      },
    });
    const rejected = expect(pending).rejects.toThrow("superseded");
    if (action === "unload") host.unload("pending");
    else if (action === "abort") controller.abort();
    else await host.activateGeneration({ pluginId: "pending", generation: "two", register() {} });

    await rejected;
    expect(api.signal.aborted).toBe(true);
    expect(cleanup).toEqual(["one"]);
    expect(() => api.registerCommand(command("late"))).toThrow("no longer registering");
    expect(() => api.onHostEvent(() => {})).toThrow("no longer active");
    finish();
    await Promise.resolve();
    expect(cleanup).toEqual(["one"]);
    expect(host.getGenerationDiagnostics("pending", "one")).toEqual([]);
    host.unload("pending");
  });

  it.each(["runtime", "service", "workspaces", "threads"])("rejects stale %s results even when the replacement reuses the same fingerprint", async (operation) => {
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
      : operation === "service" ? api.invokeService({ service: "catalog", method: "read" })
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
      api.registerUIContribution({ target: "fixture/workspace.header", id: "label", render: () => "old" });
    } });
    const retained = host.getCommands()[0]!;
    await host.activateGeneration({ pluginId: "commands", generation: "same", register(api) {
      api.registerCommand({ id: "run", title: "Run", execute: () => { executed.push("new"); } });
      api.registerUIContribution({ target: "fixture/workspace.header", id: "label", render: () => "new" });
    } });
    expect(() => retained.execute()).toThrow("no longer active");
    host.getCommands()[0]!.execute();
    expect(executed).toEqual(["new"]);

    host.unload("commands");
  });

  it("resolves command placements without changing unplaced commands and contains predicate failures", async () => {
    const host = new PluginHost({ react: React });
    const context: PluginCommandActionContext = { contractVersion: 1, target: "view.title", viewId: "v", viewTypeId: "notes", viewPluginId: "notes", region: "auxiliary" };
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "plain", title: "Plain", execute() {} });
      api.registerCommand({ id: "hidden", title: "Hidden", placements: ["view.title"], when: () => false, execute() {} });
      api.registerCommand({ id: "broken", title: "Broken", placements: ["view.title"], when: () => { throw new Error("predicate failed"); }, execute() {} });
      api.registerCommand({ id: "disabled", title: "Disabled", placements: ["view.title"], enabled: () => false, order: 2, execute() {} });
      api.registerCommand({ id: "first", title: "First", placements: ["view.title"], order: 1, execute() {} });
    } });
    const resolved = host.resolveCommandActions(context);
    expect(resolved.actions.map(({ command, enabled }) => [command.id, enabled])).toEqual([["first", true], ["disabled", false]]);
    expect(resolved.failures).toHaveLength(1);
    expect(host.getCommands()).toHaveLength(5);
    host.unload("notes");
    expect(host.resolveCommandActions(context).actions).toEqual([]);
  });

  it("rechecks action enablement and exact run identity at invocation", async () => {
    const host = new PluginHost({ react: React });
    const context: PluginCommandActionContext = { contractVersion: 1, target: "view.title", viewId: "v", viewTypeId: "notes", viewPluginId: "notes", region: "auxiliary" };
    let enabled = true;
    const calls: unknown[] = [];
    const register = (api: PluginGenerationApi): void => { api.registerCommand({ id: "refresh", title: "Refresh", placements: ["view.title"], enabled: () => enabled, execute: (input) => { calls.push(input); } }); };
    await host.activateGeneration({ pluginId: "notes", generation: "same", register });
    const old = host.getCommands()[0]!;
    enabled = false;
    await expect(host.executeCommandAction(old, context)).rejects.toThrow("disabled");
    enabled = true;
    await host.executeCommandAction(old, context);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toEqual(context);
    expect(Object.isFrozen(calls[0])).toBe(true);
    await host.activateGeneration({ pluginId: "notes", generation: "same", register });
    await expect(host.executeCommandAction(old, context)).rejects.toThrow("no longer active");
    expect(calls).toHaveLength(1);
    host.unload("notes");
  });

  it("retains command identity during unrelated registry changes and freezes nested action data", async () => {
    const host = new PluginHost({ react: React });
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => { finish = resolve; });
    const item = { contractVersion: 1 as const, id: "item", kind: "assistant-message" as const, content: [{ type: "plain-text" as const, text: "original" }] };
    const context: PluginCommandActionContext = { contractVersion: 1, target: "conversation.message.actions", turnId: "turn", item };
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "save", title: "Save", placements: ["conversation.message.actions"], execute: async (input) => {
        const received = input as Extract<PluginCommandActionContext, { target: "conversation.message.actions" }>;
        expect(Object.isFrozen(received.item.content![0])).toBe(true);
        await gate;
        return "saved";
      } });
    } });
    const command = host.getCommands()[0]!;
    const pending = host.executeCommandAction(command, context);
    await host.activateGeneration({ pluginId: "other", generation: "one", register(api) { api.registerCommand({ id: "other", title: "Other", execute() {} }); } });
    expect(host.getCommands().find((row) => row.pluginId === "notes")).toBe(command);
    expect(Object.isFrozen(item)).toBe(false);
    finish();
    await expect(pending).resolves.toBe("saved");
    host.unload("notes"); host.unload("other");
  });

  it("fails closed when supplied command predicates do not return booleans", async () => {
    const host = new PluginHost({ react: React });
    const context: PluginCommandActionContext = { contractVersion: 1, target: "view.title", viewId: "v", viewTypeId: "notes", viewPluginId: "notes", region: "auxiliary" };
    await host.activateGeneration({ pluginId: "notes", generation: "one", register(api) {
      api.registerCommand({ id: "invalid", title: "Invalid", placements: ["view.title"], when: (() => undefined) as unknown as () => boolean, execute() {} });
    } });
    const result = host.resolveCommandActions(context);
    expect(result.actions).toEqual([]);
    expect(result.failures).toHaveLength(1);
    host.unload("notes");
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

  it.each(["throw", "timeout"])("rolls back partial registration on %s and preserves the active generation", async (failure) => {
    vi.useFakeTimers();
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

    let candidate!: PluginGenerationApi;
    let finish!: () => void;
    const activation = host.activateGeneration({
      pluginId: "review",
      generation: "broken",
      async register(api) {
        candidate = api;
        api.registerCleanup(() => cleanup.push("first"));
        api.registerUIContribution({ target: "fixture/sidebar.footer", ...contribution("partial") });
        api.registerCleanup(() => cleanup.push("second"));
        api.registerStyle({ id: "partial", css: ".partial {}" });
        if (failure === "throw") throw new Error("registration stopped");
        await new Promise<void>((resolve) => { finish = resolve; });
        api.registerCommand(command("too-late"));
      },
    });

    const rejected = expect(activation).rejects.toThrow(failure === "throw" ? "registration stopped" : "timed out");
    await vi.advanceTimersByTimeAsync(30_000);
    await rejected;
    expect(candidate.signal.aborted).toBe(true);
    finish?.();
    await Promise.resolve();
    expect(cleanup).toEqual(["second", "first"]);
    expect(host.getCommands().map((item) => item.id)).toEqual(["stable"]);
    expect(host.getUIContributions("fixture/sidebar.footer")).toEqual([]);
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
        disposables.push(api.registerUIContribution({ target: "fixture/sidebar.footer", ...contribution("footer") }));
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

    expect(host.getUIContributions("fixture/sidebar.footer")).toEqual([]);
    expect(host.getCommands()).toEqual([]);
    expect(host.getLocaleEntries("en")).toEqual({});
    expect(document.head.querySelector("style[data-wuu-plugin-id=removable]")).toBeNull();
    expect(cleanup).toEqual(["cleanup"]);
  });

  it("disables a plugin by removing every host-owned registration", async () => {
    vi.useFakeTimers();
    let lifetime!: AbortSignal;
    const controller = new AbortController();
    const host = new PluginHost({ react: React });
    const cleanup: string[] = [];
    await host.activateGeneration({
      pluginId: "disable-me",
      generation: "one",
      signal: controller.signal,
      register(api) {
        lifetime = api.signal;
        registerCompleteGeneration(api, "one");
        api.registerCleanup(() => cleanup.push("disabled"));
      },
    });

    controller.abort();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(lifetime.aborted).toBe(false);
    expect(host.isGenerationActive("disable-me", "one")).toBe(true);
    host.disable("disable-me");
    expect(lifetime.aborted).toBe(true);

    expect(host.getUIContributions("fixture/workspace.header")).toEqual([]);
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
      contributions: { presenters: [] },
      register(api) {
        api.registerPresenter({ id: "status", target: "conversation.item", mode: "wrap", render: () => null });
      },
    })).rejects.toThrow("presenter registration status is not declared");
  });

  it("rejects manifest UI contributions that activation does not register", async () => {
    const host = new PluginHost({ react: React });

    await expect(host.activateGeneration({
      pluginId: "missing-registration",
      generation: "one",
      contributions: {
        presenters: [{ id: "frame", target: "conversation.item", mode: "wrap" }],
      },
      register() {},
    })).rejects.toThrow("Manifest presenter contribution frame was not registered");
  });

  it("keeps candidates private, swaps replacements atomically, and preserves the active presenter on failure", async () => {
    const host = new PluginHost({ react: React });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const activation = host.activateGeneration({
      pluginId: "owner",
      generation: "one",
      async register(api) {
        api.registerUIContribution({ id: "echo", target: "owner/tool", mode: "wrap", render: () => "one" });
        await gate;
      },
    });
    expect(host.getUIContributions("owner/tool")).toEqual([]);
    release();
    await activation;
    expect(host.getUIContributions("owner/tool")[0]?.generation).toBe("one");

    await host.activateGeneration({
      pluginId: "owner",
      generation: "two",
      register(api) {
        api.registerUIContribution({ id: "echo", target: "owner/tool", mode: "wrap", render: () => "two" });
      },
    });
    expect(host.getUIContributions("owner/tool")[0]?.generation).toBe("two");

    await expect(host.activateGeneration({
      pluginId: "owner",
      generation: "broken",
      register(api) {
        api.registerUIContribution({ id: "echo", target: "owner/tool", mode: "wrap", render: () => "broken" });
        throw new Error("stop");
      },
    })).rejects.toThrow("stop");
    expect(host.getUIContributions("owner/tool")[0]?.generation).toBe("two");
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
        registration = api.registerUIContribution({ id: "echo", target: "owner/tool", mode: "wrap", render: () => null });
      },
    });
    const published = host.getUICompositionSnapshot();
    expect(Object.isFrozen(published)).toBe(true);
    expect(Object.isFrozen(published[0])).toBe(true);
    registration.dispose();
    expect(host.getUIContributions("owner/tool")).toEqual([]);
    expect(notifications).toHaveLength(2);

    await host.activateGeneration({
      pluginId: "owner",
      generation: "two",
      register(api) {
        api.registerUIContribution({ id: "echo", target: "owner/tool", mode: "wrap", render: () => null });
      },
    });
    host.unload("owner");
    expect(host.getUIContributions("owner/tool")[0]).toBeUndefined();
  });

  it.each(["conversation.composer", "navigation.primary"])("rejects retired presenter %s instead of silently ignoring it", async target => {
    const host = new PluginHost({ react: React });
    await expect(host.activateGeneration({ pluginId: "retired-presenter", generation: "one", register(api) {
      api.registerPresenter({ id: "retired", target, render: () => null });
    } })).rejects.toThrow("registerUIContribution");
  });

  it("publishes stable exact-match presenter snapshots in deterministic order", async () => {
    expect(PRESENTATION_TARGETS).toEqual([
      "conversation.item", "conversation.process",
      "header.conversation", "header.workspace",
      "app.status", "content.preview", "settings",
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

  it.each(["runtime", "service"])("binds %s requests to the captured plugin and graph generation", async (kind) => {
    const invoke = vi.fn(async (_request: unknown) => ({ ok: true }));
    const host = new PluginHost({ react: React, invokeRuntime: invoke });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "consumer", generation: "same", graphGeneration: "graph-one", register(value) { api = value; } });
    const request = {
      service: "catalog", method: "read", params: { item: "one" },
      pluginId: "forged", generation: "forged", graphGeneration: "forged", execution_id: "forged", provider: "forged",
    };
    const options = { workspaceId: "workspace", workspaceRoot: "/workspace" };
    await expect(kind === "runtime" ? api.invokeRuntime("read", request.params, options) : api.invokeService(request, options)).resolves.toEqual({ ok: true });
    expect(invoke).toHaveBeenCalledExactlyOnceWith({
      pluginId: "consumer", generation: "same", graphGeneration: "graph-one",
      ...(kind === "service" ? { service: "catalog" } : {}),
      method: "read", input: request.params, ...options, signal: expect.any(AbortSignal),
    });
    await host.activateGeneration({ pluginId: "consumer", generation: "same", graphGeneration: "graph-two", register() {} });
    await expect(kind === "runtime" ? api.invokeRuntime("read") : api.invokeService(request)).rejects.toThrow("no longer active");
    expect(invoke).toHaveBeenCalledTimes(1);
    host.unload("consumer");
  });

  it.each(["runtime", "service"])("cancels %s calls and removes settled caller abort listeners", async (kind) => {
    let finish!: (result: unknown) => void;
    const invoke = vi.fn((_request: { signal?: AbortSignal }) => new Promise(resolve => { finish = resolve; }));
    const host = new PluginHost({ react: React, invokeRuntime: invoke });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "consumer", generation: "one", register(value) { api = value; } });
    const call = (signal: AbortSignal) => kind === "runtime"
      ? api.invokeRuntime("read", undefined, { signal })
      : api.invokeService({ service: "catalog", method: "read" }, { signal });
    const controller = new AbortController();
    controller.abort(new Error("already cancelled"));
    await expect(call(controller.signal)).rejects.toThrow("already cancelled");
    expect(invoke).not.toHaveBeenCalled();
    const pendingController = new AbortController();
    const pending = call(pendingController.signal);
    pendingController.abort(new Error("caller cancelled"));
    expect(invoke.mock.calls[0][0].signal?.aborted).toBe(true);
    finish({ ok: true });
    await expect(pending).rejects.toThrow("caller cancelled");
    const settledController = new AbortController();
    const settled = call(settledController.signal);
    finish({ ok: true });
    await expect(settled).resolves.toEqual({ ok: true });
    settledController.abort();
    expect(invoke.mock.calls[1][0].signal?.aborted).toBe(false);
    const closing = call(new AbortController().signal);
    host.unload("consumer");
    expect(invoke.mock.calls[2][0].signal?.aborted).toBe(true);
    finish({ ok: true });
    await expect(closing).rejects.toThrow("no longer active");
  });

  it("preserves service failures without retrying or falling back to a private runtime call", async () => {
    const error = Object.assign(new Error("Dependency is unavailable"), { code: "service_unavailable" });
    const invoke = vi.fn(async (_request: unknown) => { throw error; });
    const host = new PluginHost({ react: React, invokeRuntime: invoke });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "consumer", generation: "one", register(value) { api = value; } });
    await expect(api.invokeService({ service: "catalog", method: "read" })).rejects.toBe(error);
    expect(invoke).toHaveBeenCalledTimes(1);
    await expect(api.invokeService({ service: " ", method: "read" })).rejects.toThrow("must not be empty");
    await expect(api.invokeService({ service: "catalog", method: " " })).rejects.toThrow("must not be empty");
    expect(invoke).toHaveBeenCalledTimes(1);
    host.unload("consumer");
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
      api.registerUIContribution({ target: "fixture/composer.above", ...contribution("status") });
    };
    await host.activateGeneration({ pluginId: "render", generation: "one", register });
    const retired = host.observeGenerationRender("render", "one");
    await host.activateGeneration({ pluginId: "render", generation, register });
    retired.failed(new Error("late failure"));
    retired.dispose();
    expect(host.getGenerationDiagnostics("render", "one")).toEqual([]);
    const current = host.observeGenerationRender("render", generation);
    current.failed(new Error("current failure"));
    current.dispose();
    expect(host.getGenerationDiagnostics("render", generation)).toHaveLength(1);
    host.unload("render");
  });

  it("deduplicates repeated diagnostics and clears them after a successful reactivation", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "recoverable",
      generation: "one",
      register(api) { api.registerUIContribution({ target: "fixture/composer.above", ...contribution("status") }); },
    });
    const observer = host.observeGenerationRender("recoverable", "one");
    observer.failed(new Error("render boom"));
    observer.failed(new Error("render boom"));
    expect(host.getGenerationDiagnostics("recoverable", "one")).toHaveLength(1);
    observer.dispose();

    await host.activateGeneration({
      pluginId: "recoverable",
      generation: "one",
      register(api) { api.registerUIContribution({ target: "fixture/composer.above", ...contribution("status") }); },
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
  api.registerUIContribution({ target: "fixture/workspace.header", ...contribution(`${label}-slot`) });
  api.registerCommand(command(`${label}-command`));
  api.registerStyle({ id: `${label}-style`, css: `.${label} {}` });
  api.registerLocale({ id: `${label}-copy`, locale: "en", entries: { label } });
}

it("owns central notices by generation and rejects notices from retired handles", async () => {
  const toast = await import("../Toast");
  const show = vi.spyOn(toast, "showToast").mockReturnValue(71);
  const dismiss = vi.spyOn(toast, "dismissToast");
  const host = new PluginHost({ react: React });
  let api!: PluginGenerationApi;
  try {
    await host.activateGeneration({ pluginId: "notices", generation: "one", register: value => { api = value; } });
    api.showNotice({ message: "Could not refresh. Try again.", tone: "error", key: "refresh" });
    expect(show).toHaveBeenCalledWith({ message: "Could not refresh. Try again.", tone: "error", dedupeKey: "plugin:notices:refresh" });
    host.unload("notices");
    expect(dismiss).toHaveBeenCalledWith(71);
    expect(() => api.showNotice({ message: "stale" })).toThrow();
  } finally { await host.unloadAndWait("notices"); show.mockRestore(); dismiss.mockRestore(); }
});

it.each(["resolve", "reject"])("classifies a late %s from a retired runtime scope without publishing a transport fault", async outcome => {
  let resolve!: (value: unknown) => void, reject!: (error: unknown) => void;
  const host = new PluginHost({ react: React, invokeRuntime: () => new Promise((yes, no) => { resolve = yes; reject = no; }) });
  let api!: PluginGenerationApi;
  await host.activateGeneration({ pluginId: "scope", generation: "a", register(value) { api = value; } });
  const result = api.invokeRuntime("read").catch(error => error);
  host.unload("scope");
  if (outcome === "resolve") resolve({ stale: true });
  else reject(new Error("plugin graph generation is no longer active"));
  expect(await result).toMatchObject({ name: "AbortError", code: "WUU_PLUGIN_SCOPE_RETIRED" });
  expect(host.getGenerationDiagnostics("scope", "a")).toEqual([]);
});

it("preserves real runtime errors for the currently active generation", async () => {
  const failure = new Error("current workspace backend failed");
  const host = new PluginHost({ react: React, invokeRuntime: async () => { throw failure; } });
  let api!: PluginGenerationApi;
  await host.activateGeneration({ pluginId: "scope", generation: "b", register(value) { api = value; } });
  await expect(api.invokeRuntime("read", { secret: "not-for-diagnostics" })).rejects.toBe(failure);
  expect(host.getGenerationDiagnostics("scope", "b")).toEqual([
    expect.objectContaining({ kind: "runtime", message: "Plugin runtime request failed: runtime/read", cause: failure }),
  ]);
  expect(host.getGenerationDiagnostics("scope", "b")[0].message).not.toContain("not-for-diagnostics");
  const publicDiagnostics = api.contributions.getGenerationDiagnostics("scope", "b");
  expect(publicDiagnostics).toEqual([{ kind: "runtime", message: "Plugin runtime request failed: runtime/read" }]);
  expect(api.contributions.getGenerationDiagnostics("scope", "b")).toBe(publicDiagnostics);
  host.unload("scope");
});

it("fences a rejected application action after its consumer scope retires", async () => {
  let reject!: (error: unknown) => void;
  const application = { invoke: () => new Promise((_, no) => { reject = no; }) };
  const host = new PluginHost({ react: React, application: application as never });
  let api!: PluginGenerationApi;
  await host.activateGeneration({ pluginId: "consumer", generation: "a", register(value) { api = value; } });
  const result = api.application.invoke("navigation.cancel", {}).catch(error => error);
  host.unload("consumer");
  reject(new DOMException("signal is aborted without reason", "AbortError"));
  expect(await result).toMatchObject({ code: "WUU_PLUGIN_SCOPE_RETIRED" });
});
