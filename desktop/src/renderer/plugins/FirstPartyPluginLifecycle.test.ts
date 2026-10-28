import { readFileSync } from "node:fs";
import { basename, resolve } from "node:path";

import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  PluginHost,
  type PluginContributionDeclarations,
  type PluginGenerationApi,
  type PluginHostOptions,
} from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";
import { ApplicationShellOutlet, createUIComposition } from "./ApplicationShell";
import { ToolActivityPresenter } from "./ToolActivityPresenter";
import { ConversationControlsProvider, ConversationTurnIdentityProvider } from "../ConversationControlsContext";
import { createConversationTestApplication } from "../ConversationControlsTestProvider";
import { registerConversationSlots } from "../ConversationSlots";

interface FirstPartyManifest {
  id: string;
  desktop: { entry: string };
  contributes?: {
    presenters?: PluginContributionDeclarations["presenters"];
    navigation?: PluginContributionDeclarations["navigation"];
    workspaceTools?: PluginContributionDeclarations["workspace_tools"];
    settingsPages?: PluginContributionDeclarations["settings_pages"];
  };
}

interface FirstPartyDesktopModule {
  activate(api: PluginGenerationApi): void | Promise<void>;
}

const repositoryRoot = basename(process.cwd()) === "desktop"
  ? resolve(process.cwd(), "..")
  : process.cwd();
const bundledRoot = resolve(repositoryRoot, "internal/plugin/bundled");
const firstPartyPluginIds = ["subagent", "automation-client", "memory", "dream", "todo", "generative-ui"] as const;

afterEach(() => {
  for (const style of document.head.querySelectorAll("style[data-wuu-plugin-id]")) {
    style.remove();
  }
});

describe("first-party desktop plugin lifecycle", () => {
  it("renders the real TODO module through the shell slot and removes its view on disable", async () => {
    const host = new PluginHost({ react: React });
    const { module } = await loadFirstPartyPlugin("todo");
    const snapshot = { todo: { items: [{ content: "Retain task evidence", status: "in_progress" }] } };
    await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "one", register(api) {
      api.registerUISlot({ id: "inspector.sections", cardinality: "many" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => React.createElement(slots.Slot, { name: "wuu.default-shell/inspector.sections", context: { snapshot } }) });
    } });
    await host.activateGeneration({ pluginId: "todo", generation: "one", register: module.activate });
    const container = document.createElement("div"); const root = createRoot(container);
    try {
      await act(async () => root.render(React.createElement(ApplicationShellOutlet, { host, binding: { pluginId: "wuu.default-shell", generation: "one", id: "shell" } })));
      expect(container.querySelector("h2")?.textContent).toBe("TODO");
      expect(container.querySelector("li")?.textContent).toBe("Retain task evidence");
      await act(async () => host.disable("todo"));
      expect(container.textContent).toBe("");
      snapshot.todo.items = [];
      await act(async () => { await host.activateGeneration({ pluginId: "todo", generation: "two", register: module.activate }); });
      expect(container.textContent).toBe("");
    } finally { act(() => root.unmount()); host.unload("todo"); host.unload("wuu.default-shell"); }
  });

  it.each(["wuu.default-conversation", "wuu.default-preview"])("renders TODO tool results in %s from the public leaf snapshot", async slotOwner => {
    const host = new PluginHost({ react: React });
    const { module } = await loadFirstPartyPlugin("todo");
    await host.activateGeneration({ pluginId: slotOwner, generation: "one", register: registerConversationSlots });
    await host.activateGeneration({ pluginId: "todo", generation: "one", register: module.activate });
    const slots = createUIComposition(host);
    const application = createConversationTestApplication();
    const container = document.createElement("div"); const root = createRoot(container);
    const render = (capability: string, argumentsText: string) => root.render(React.createElement(ConversationControlsProvider, {
      application, slots, slotOwner,
      children: React.createElement(ConversationTurnIdentityProvider, { threadId: "thread", turnId: "turn",
        children: React.createElement(ToolActivityPresenter, { item: { id: "call", type: "tool_call", name: "todo", status: "completed", arguments: argumentsText, display: { capability } }, fallback: React.createElement("span", null, "Native tool") }),
      }),
    }));
    try {
      await act(async () => render("todo", JSON.stringify({ todos: [{ content: "Retain tool evidence", status: "completed" }] })));
      expect(container.querySelector("li")?.textContent).toBe("Retain tool evidence");
      await act(async () => render("other", JSON.stringify({ todos: [{ content: "Foreign tool", status: "completed" }] })));
      expect(container.textContent).toBe("Native tool");
      await act(async () => render("todo", '{"todos":'));
      expect(container.textContent).toBe("Native tool");
      await act(async () => render("todo", JSON.stringify({ todos: [{ content: "Retain tool evidence", status: "completed" }] })));
      await act(async () => host.disable("todo"));
      expect(container.textContent).toBe("Native tool");
    } finally { act(() => root.unmount()); host.unload("todo"); host.unload(slotOwner); }
  });

  it("activates real bundled modules and removes every contribution on disable", async () => {
    const host = new PluginHost({ react: React });

    for (const pluginId of firstPartyPluginIds) {
      const { manifest, module } = await loadFirstPartyPlugin(pluginId);
      await host.activateGeneration({
        pluginId,
        generation: "generation-one",
        contributions: contributionDeclarations(manifest),
        register: module.activate,
      });
    }

    expect(host.getComposerStatusSources().map((item) => item.pluginId)).toEqual([
      "subagent",
    ]);
    expect(host.getUIContributions("wuu.default-composer/composer.toolbar")).toEqual([]);
    expect(host.getViewTypes().map((item) => `${item.pluginId}:${item.id}`)).toEqual([
      "automation-client:automation.catalog",
      "dream:dream.settings",
      "memory:memory.settings",
      "subagent:subagent.settings",
    ]);
    expect(host.getNavigationEntries().map((item) => `${item.pluginId}:${item.view}`)).toEqual([
      "automation-client:automation.catalog",
    ]);
    expect(host.getSettingsPages().map((item) => `${item.pluginId}:${item.view}`)).toEqual([
      "memory:memory.settings",
      "subagent:subagent.settings",
      "dream:dream.settings",
    ]);
    expect(host.getUIContributions("wuu.default-shell/inspector.sections").map((item) => `${item.pluginId}:${item.id}`)).toEqual([
      "todo:current-todo",
    ]);
    expect(host.getUIContributions("wuu.default-conversation/conversation.tool-activity").at(-1)?.pluginId).toBe("todo");
    expect(host.getUIContributions("wuu.default-preview/conversation.tool-activity").at(-1)?.pluginId).toBe("todo");
    expect(document.head.querySelectorAll("style[data-wuu-plugin-id]")).toHaveLength(6);

    for (const pluginId of firstPartyPluginIds) {
      host.disable(pluginId);
    }

    expect(host.getUIContributions("wuu.default-composer/composer.above")).toEqual([]);
    expect(host.getUIContributions("wuu.default-composer/composer.toolbar")).toEqual([]);
    expect(host.getComposerStatusSources()).toEqual([]);
    expect(host.getViewTypes()).toEqual([]);
    expect(host.getNavigationEntries()).toEqual([]);
    expect(host.getSettingsPages()).toEqual([]);
    expect(host.getUIContributions("wuu.default-shell/inspector.sections")).toEqual([]);
    expect(host.getUIContributions("wuu.default-conversation/conversation.tool-activity")).toEqual([]);
    expect(host.getUIContributions("wuu.default-preview/conversation.tool-activity")).toEqual([]);
    expect(document.head.querySelector("style[data-wuu-plugin-id]")).toBeNull();
  });

  it.each(firstPartyPluginIds)("atomically replaces the %s desktop generation", async (pluginId) => {
    const host = new PluginHost({ react: React });
    const { manifest, module } = await loadFirstPartyPlugin(pluginId);
    const contributions = contributionDeclarations(manifest);

    await host.activateGeneration({
      pluginId,
      generation: "old-generation",
      contributions,
      register: module.activate,
    });
    await host.activateGeneration({
      pluginId,
      generation: "new-generation",
      contributions,
      register: module.activate,
    });

    expect(host.isGenerationActive(pluginId, "old-generation")).toBe(false);
    expect(host.isGenerationActive(pluginId, "new-generation")).toBe(true);
    expect(document.head.querySelector(`style[data-wuu-plugin-id="${pluginId}"][data-wuu-plugin-generation="old-generation"]`)).toBeNull();
    expect(document.head.querySelectorAll(`style[data-wuu-plugin-id="${pluginId}"][data-wuu-plugin-generation="new-generation"]`)).toHaveLength(1);
    expect([
      ...host.getComposerStatusSources(),
      ...host.getUIContributions("wuu.default-composer/composer.toolbar"),
      ...host.getViewTypes(),
    ].filter((item) => item.pluginId === pluginId).every((item) => item.generation === "new-generation"))
      .toBe(true);
  });

  it("scopes structured subagent status snapshots to the active session", async () => {
    const pending = new Map<string, (value: unknown) => void>();
    let runtimeCalls = 0;
    const host = new PluginHost({
      react: React,
      invokeRuntime: ({ method, input }) => {
        if (method !== "status.list") {
          return Promise.reject(new Error(`Unexpected subagent runtime method: ${method}`));
        }
        runtimeCalls += 1;
        const threadId = String((input as { parent_session_id?: string })?.parent_session_id ?? "");
        return new Promise((resolve) => pending.set(threadId, resolve));
      },
    });
    const { manifest, module } = await loadFirstPartyPlugin("subagent");
    await host.activateGeneration({
      pluginId: "subagent",
      generation: "session-status-test",
      contributions: contributionDeclarations(manifest),
      register: module.activate,
    });
    const source = host.getComposerStatusSources()[0];
    if (!source) throw new Error("Missing subagent composer status source");
    const contextA = Object.freeze({ threadId: "thread-a", mainConversation: true as const });
    const contextB = Object.freeze({ threadId: "thread-b", mainConversation: true as const });
    let updates = 0;
    const cleanup = source.subscribe(contextA, () => { updates += 1; });
    try {
      act(() => {
        host.publishHostEvent({ kind: "notification", message: { method: "turn/event" } });
        host.publishHostEvent({ kind: "notification", message: { method: "turn/usage" } });
      });
      expect(runtimeCalls).toBe(1);
      await act(async () => {
        pending.get("thread-a")?.({ sessions: [{ session_id: "child-a", name: "from-a", state: "running" }] });
      });
      expect(updates).toBe(1);
      expect(source.getSnapshot(contextA)).toMatchObject([
        { id: "child-a", label: "from-a", state: "running", action: { kind: "open-session", sessionId: "child-a" } },
      ]);
      expect(source.getSnapshot(contextB)).toEqual([]);
    } finally {
      cleanup();
      host.disable("subagent");
    }
  });

  it("edits model aliases without losing advanced options and rejects duplicate names", async () => {
    const host = new PluginHost({ react: React });
    const { manifest, module } = await loadFirstPartyPlugin("subagent");
    await host.activateGeneration({ pluginId: "subagent", generation: "aliases-test", contributions: contributionDeclarations(manifest), register: module.activate });
    const controller = new WorkbenchController(host);
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    const updateValue = vi.fn(async () => {});
    const settings = {
      contractVersion: 1 as const,
      getValue: () => ({ research: { provider: "primary", model: "model-a", effort: "high", variant: "fast" } }),
      updateValue,
    };
    const click = async (text: string) => {
      const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text)!;
      await act(async () => button.click());
    };
    const fill = async (input: HTMLInputElement, value: string) => {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
    };
    try {
      await act(async () => root.render(React.createElement(PluginViewContent, { controller, pluginId: "subagent", viewTypeId: "subagent.settings", settings })));
      await fill(container.querySelectorAll("input")[2], "model-b");
      await click("保存别名");
      expect(updateValue).toHaveBeenLastCalledWith("runtime.modelAliases", { research: { provider: "primary", model: "model-b", effort: "high", variant: "fast" } });
      await click("添加别名");
      const inputs = container.querySelectorAll("input");
      await fill(inputs[5], "research");
      await fill(inputs[6], "primary");
      await fill(inputs[7], "model-c");
      await click("保存别名");
      expect(updateValue).toHaveBeenCalledTimes(1);
      expect(container.querySelector('[role="alert"]')).not.toBeNull();
      await click("删除别名");
      await click("保存别名");
      expect(updateValue).toHaveBeenLastCalledWith("runtime.modelAliases", { research: { provider: "primary", model: "model-c" } });
      await click("删除别名");
      await click("保存别名");
      expect(updateValue).toHaveBeenLastCalledWith("runtime.modelAliases", {});
    } finally {
      act(() => root.unmount());
      container.remove();
      controller.dispose();
    }
  });

  it.each([
    ["automation-client", "automation.catalog", "还没有自动化任务"],
    ["memory", "memory.settings", "笔记本原文"],
    ["dream", "dream.settings", "后台整合"],
  ] as const)("renders the real %s view with localized host UI", async (pluginId, viewTypeId, expectedText) => {
    const invokeRuntime = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ service, method, input, workspaceId }) => {
      if (service !== undefined) {
        expect(service).toBe("automation.scheduler");
        expect(method).toBe("request");
        return firstPartyRuntimeResponse((input as { method: string }).method, workspaceId);
      }
      return firstPartyRuntimeResponse(method);
    });
    const host = new PluginHost({
      react: React,
      invokeRuntime,
      listWorkspaces: async () => ({
        activeWorkspaceId: "workspace",
        workspaces: [{ id: "workspace", name: "Workspace", root: "/workspace", available: true }],
      }),
    });
    const { manifest, module } = await loadFirstPartyPlugin(pluginId);
    await host.activateGeneration({
      pluginId,
      generation: "render-test",
      graphGeneration: "localized-graph",
      contributions: contributionDeclarations(manifest),
      register: module.activate,
    });
    const controller = new WorkbenchController(host);
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    try {
      await act(async () => {
        root.render(React.createElement(PluginViewContent, {
          controller,
          pluginId,
          viewTypeId,
        }));
      });
      expect(container.querySelector('[data-wuu-component="plugin-ui-page"]')).not.toBeNull();
      expect(container.textContent).toContain(expectedText);
      expect([...container.querySelectorAll("input, textarea")].every((control) => control.closest("label"))).toBe(true);
      if (pluginId === "automation-client") {
        expect(invokeRuntime).toHaveBeenCalledWith(expect.objectContaining({
          pluginId,
          generation: "render-test",
          graphGeneration: "localized-graph",
          service: "automation.scheduler",
          method: "request",
          input: expect.objectContaining({ method: "automation.list" }),
          workspaceId: "workspace",
        }));
      }
    } finally {
      act(() => root.unmount());
      container.remove();
      controller.dispose();
    }
  });
});

function firstPartyRuntimeResponse(method: string, workspaceId?: string): unknown {
  switch (method) {
    case "automation.list":
      return { tasks: [], workspace: { id: workspaceId } };
    case "automation.run.list":
      return { runs: [] };
    case "dream.get":
      return {
        settings: { enabled: false, interval_days: 7, min_sessions: 5, model_alias: "" },
        candidates: {},
        last_status: "",
      };
    case "memory.overview.start":
      return { id: "overview-job" };
    case "memory.job.get":
      return { state: "completed", output: "" };
    case "memory.read":
      return { index_raw: "", files: [] };
    default:
      throw new Error(`Unexpected first-party runtime method: ${method}`);
  }
}

async function loadFirstPartyPlugin(
  pluginId: typeof firstPartyPluginIds[number],
): Promise<{ manifest: FirstPartyManifest; module: FirstPartyDesktopModule }> {
  const pluginRoot = resolve(bundledRoot, pluginId);
  const manifest = JSON.parse(readFileSync(resolve(pluginRoot, "plugin.json"), "utf8")) as FirstPartyManifest;
  const source = readFileSync(resolve(pluginRoot, manifest.desktop.entry), "utf8");
  if (!source.includes("export async function activate(api)")) {
    throw new Error(`First-party plugin ${pluginId} does not export activate(api)`);
  }
  const executableSource = source.replace(/export (?=(?:async )?function )/g, "");
  const activate: unknown = Function(`${executableSource}\nreturn activate;`)();
  if (typeof activate !== "function") {
    throw new Error(`First-party plugin ${pluginId} has an invalid activate export`);
  }
  return {
    manifest,
    module: { activate: (api) => Reflect.apply(activate, undefined, [api]) as void | Promise<void> },
  };
}

function contributionDeclarations(manifest: FirstPartyManifest): PluginContributionDeclarations {
  const contributions = manifest.contributes ?? {};
  return {
    presenters: contributions.presenters ?? [],
    navigation: contributions.navigation ?? [],
    workspace_tools: contributions.workspaceTools ?? [],
    settings_pages: contributions.settingsPages ?? [],
  };
}
