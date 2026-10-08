import { readFileSync } from "node:fs";
import { basename, resolve } from "node:path";

import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";

import {
  PluginHost,
  type PluginContributionDeclarations,
  type PluginGenerationApi,
} from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";

const repositoryRoot = basename(process.cwd()) === "desktop"
  ? resolve(process.cwd(), "..")
  : process.cwd();
const bundledRoot = resolve(repositoryRoot, "internal/plugin/bundled");

interface FirstPartyManifest {
  id: string;
  desktop: { entry: string };
  contributes?: {
    slots?: PluginContributionDeclarations["slots"];
    surfaces?: PluginContributionDeclarations["surfaces"];
    presenters?: PluginContributionDeclarations["presenters"];
    workspaceTools?: PluginContributionDeclarations["workspace_tools"];
    settingsPages?: PluginContributionDeclarations["settings_pages"];
  };
}

function contributionDeclarations(manifest: FirstPartyManifest): PluginContributionDeclarations {
  const contributions = manifest.contributes ?? {};
  return {
    slots: contributions.slots ?? [],
    surfaces: contributions.surfaces ?? [],
    presenters: contributions.presenters ?? [],
    workspace_tools: contributions.workspaceTools ?? [],
    settings_pages: contributions.settingsPages ?? [],
  };
}

async function loadMemoryPlugin(): Promise<{ manifest: FirstPartyManifest; activate: (api: PluginGenerationApi) => void | Promise<void> }> {
  const pluginRoot = resolve(bundledRoot, "memory");
  const manifest = JSON.parse(readFileSync(resolve(pluginRoot, "plugin.json"), "utf8")) as FirstPartyManifest;
  const source = readFileSync(resolve(pluginRoot, manifest.desktop.entry), "utf8");
  const executableSource = source.replace(
    "export async function activate(api)",
    "return async function activate(api)",
  );
  const activate = Function(executableSource)() as (api: PluginGenerationApi) => void | Promise<void>;
  return { manifest, activate };
}

describe("memory settings panel overview failure", () => {
  it("renders the raw notebook even when the LLM overview fails", async () => {
    const host = new PluginHost({
      react: React,
      invokeRuntime: async ({ method }) => {
        switch (method) {
          case "memory.overview.start":
            return { id: "overview-job" };
          case "memory.job.get":
            return { state: "failed", error: "provider unavailable" };
          case "memory.read":
            return {
              index_raw: "- [One](feedback_one.md)",
              files: [
                { name: "feedback_one.md", type: "feedback", description: "first", content: "---\ntype: feedback\n---\nfirst memory" },
              ],
            };
          default:
            throw new Error(`Unexpected memory runtime method: ${method}`);
        }
      },
    });
    const { manifest, activate } = await loadMemoryPlugin();
    await host.activateGeneration({
      pluginId: "memory",
      generation: "overview-failure-test",
      contributions: contributionDeclarations(manifest),
      register: activate,
    });

    const controller = new WorkbenchController(host);
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    try {
      await act(async () => {
        root.render(React.createElement(PluginViewContent, {
          controller,
          pluginId: "memory",
          viewTypeId: "memory.settings",
        }));
      });
      // The overview failed, but the raw notebook must still be readable.
      expect(container.textContent).toContain("MEMORY.md");
      expect(container.textContent).toContain("feedback_one.md");
      expect(container.textContent).toContain("first memory");
      expect(container.querySelectorAll(".plugin-memory-file")).toHaveLength(2);
    } finally {
      act(() => root.unmount());
      container.remove();
      controller.dispose();
    }
  });
});
