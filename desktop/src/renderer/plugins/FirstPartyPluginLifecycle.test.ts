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
} from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";

interface FirstPartyManifest {
  id: string;
  desktop: { entry: string };
  contributes?: {
    slots?: PluginContributionDeclarations["slots"];
    surfaces?: PluginContributionDeclarations["surfaces"];
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
const firstPartyPluginIds = ["subagent", "automation", "memory", "dream", "todo"] as const;

afterEach(() => {
  for (const style of document.head.querySelectorAll("style[data-wuu-plugin-id]")) {
    style.remove();
  }
});

describe("first-party desktop plugin lifecycle", () => {
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
    expect(host.getSlotSnapshot("composer.toolbar")).toEqual([]);
    expect(host.getViewTypes().map((item) => `${item.pluginId}:${item.id}`)).toEqual([
      "automation:automation.catalog",
      "dream:dream.settings",
      "memory:memory.settings",
      "subagent:subagent.settings",
    ]);
    expect(host.getNavigationEntries().map((item) => `${item.pluginId}:${item.view}`)).toEqual([
      "automation:automation.catalog",
    ]);
    expect(host.getSettingsPages().map((item) => `${item.pluginId}:${item.view}`)).toEqual([
      "memory:memory.settings",
      "subagent:subagent.settings",
      "dream:dream.settings",
    ]);
    expect(host.getInspectorSections().map((item) => `${item.pluginId}:${item.id}`)).toEqual([
      "todo:current-todo",
    ]);
    expect(host.getPresenters("conversation.tool-activity", "todo").at(-1)?.pluginId).toBe("todo");
    expect(document.head.querySelectorAll("style[data-wuu-plugin-id]")).toHaveLength(5);

    for (const pluginId of firstPartyPluginIds) {
      host.disable(pluginId);
    }

    expect(host.getSlotSnapshot("composer.above")).toEqual([]);
    expect(host.getSlotSnapshot("composer.toolbar")).toEqual([]);
    expect(host.getComposerStatusSources()).toEqual([]);
    expect(host.getViewTypes()).toEqual([]);
    expect(host.getNavigationEntries()).toEqual([]);
    expect(host.getSettingsPages()).toEqual([]);
    expect(host.getInspectorSections()).toEqual([]);
    expect(host.getPresenters("conversation.tool-activity", "todo")).toEqual([]);
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
      ...host.getSlotSnapshot("composer.toolbar"),
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
    ["automation", "automation.catalog", "还没有自动化任务"],
    ["memory", "memory.settings", "记忆概览"],
    ["dream", "dream.settings", "后台记忆整合"],
  ] as const)("renders the real %s view with localized host UI", async (pluginId, viewTypeId, expectedText) => {
    const host = new PluginHost({
      react: React,
      invokeRuntime: async ({ method }) => firstPartyRuntimeResponse(method),
    });
    const { manifest, module } = await loadFirstPartyPlugin(pluginId);
    await host.activateGeneration({
      pluginId,
      generation: "render-test",
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
    } finally {
      act(() => root.unmount());
      container.remove();
      controller.dispose();
    }
  });
});

function firstPartyRuntimeResponse(method: string): unknown {
  switch (method) {
    case "automation.list":
      return { tasks: [] };
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
  const executableSource = source.replace(
    "export async function activate(api)",
    "return async function activate(api)",
  );
  if (executableSource === source) {
    throw new Error(`First-party plugin ${pluginId} does not export activate(api)`);
  }
  const activate: unknown = Function(executableSource)();
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
    slots: contributions.slots ?? [],
    surfaces: contributions.surfaces ?? [],
    presenters: contributions.presenters ?? [],
    navigation: contributions.navigation ?? [],
    workspace_tools: contributions.workspaceTools ?? [],
    settings_pages: contributions.settingsPages ?? [],
  };
}
