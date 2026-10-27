import { readFileSync } from "node:fs";
import { basename, resolve } from "node:path";

import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";

import type { ThemeTokens } from "../../shared/workbench";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { createUIComposition } from "./ApplicationShell";
import { ApplicationController } from "./ApplicationController.test-support";
import { registerConversationSlots } from "../ConversationSlots";

interface ExampleManifest {
  id: string;
  desktop: { entry: string };
  contributes?: {
    themes?: readonly (Omit<ThemeTokens, "theme"> & { id: string })[];
  };
}

const repositoryRoot = basename(process.cwd()) === "desktop"
  ? resolve(process.cwd(), "..")
  : process.cwd();

afterEach(() => {
  for (const style of document.head.querySelectorAll("style[data-wuu-plugin-id]")) {
    style.remove();
  }
});

describe("third-party feature and theme composition", () => {
  it("keeps the feature surface intact when an independently installed theme is disabled", async () => {
    const feature = loadExample("deep-ui");
    const theme = loadExample("manga-studio");
    window.wuu = {} as never;
    const host = new PluginHost({ react: React, application: new ApplicationController() });
    await host.activateGeneration({ pluginId: "wuu.default-conversation", generation: "one", register: registerConversationSlots });
    const composition = createUIComposition(host);

    await host.activateGeneration({
      pluginId: feature.manifest.id,
      generation: "feature-one",
      register: feature.activate,
    });
    await host.activateGeneration({
      pluginId: theme.manifest.id,
      generation: "theme-one",
      register: async (api) => {
        for (const tokens of theme.manifest.contributes?.themes ?? []) {
          api.registerThemeTokens({
            theme: tokens.id,
            base: tokens.base,
            tokens: tokens.tokens,
            syntax: tokens.syntax,
          });
        }
        await theme.activate(api);
      },
    });

    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    try {
      act(() => root.render(
        <composition.Slot
          name="wuu.default-conversation/conversation.turn"
          fallback={<span>Built in timeline</span>}
        />,
      ));

      expect(container.textContent).toBe("Built in timeline");
      expect(container.querySelector(".deep-ui-timeline")).not.toBeNull();
      expect(host.getThemeTokens("manga-paper")).toEqual([
        expect.objectContaining({ pluginId: "manga-studio", base: "light" }),
      ]);
      expect(document.head.querySelector('style[data-wuu-plugin-id="deep-ui-example"]')).not.toBeNull();
      expect(document.head.querySelector('style[data-wuu-plugin-id="manga-studio"]')).not.toBeNull();

      await act(async () => host.unload("manga-studio"));

      expect(host.getThemeTokens("manga-paper")).toEqual([]);
      expect(document.head.querySelector('style[data-wuu-plugin-id="manga-studio"]')).toBeNull();
      expect(container.textContent).toBe("Built in timeline");
      expect(container.querySelector(".deep-ui-timeline")).not.toBeNull();
      expect(document.head.querySelector('style[data-wuu-plugin-id="deep-ui-example"]')).not.toBeNull();
    } finally {
      act(() => root.unmount());
      container.remove();
      await host.unload("deep-ui-example");
      host.unload("wuu.default-conversation");
    }
  });
});

function loadExample(name: string): {
  manifest: ExampleManifest;
  activate(api: PluginGenerationApi): void | Promise<void>;
} {
  const root = resolve(repositoryRoot, "examples/plugins", name);
  const manifest = JSON.parse(readFileSync(resolve(root, "plugin.json"), "utf8")) as ExampleManifest;
  const source = readFileSync(resolve(root, manifest.desktop.entry), "utf8");
  const executable = source.replace(
    "export async function activate(api)",
    "return async function activate(api)",
  );
  if (executable === source) {
    throw new Error(`Example plugin ${name} does not export activate(api)`);
  }
  const activate: unknown = Function(executable)();
  if (typeof activate !== "function") {
    throw new Error(`Example plugin ${name} has an invalid activate export`);
  }
  return { manifest, activate: activate as (api: PluginGenerationApi) => void | Promise<void> };
}
