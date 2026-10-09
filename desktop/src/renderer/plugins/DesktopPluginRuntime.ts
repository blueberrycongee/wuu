import * as React from "react";
import { useEffect } from "react";

import type { ExtensionInventoryRecord } from "../../shared/protocol";
import { syncExtensionTheme } from "../Theme";
import {
  PluginHost,
  PluginGenerationSupersededError,
  type PluginContributionDeclarations,
  type PluginGenerationApi,
} from "./PluginHost";
import { WorkbenchController } from "./Workbench";
import { PluginHostService, WorkbenchService, createDesktopCompositionRoot } from "./composition";

interface DesktopPluginModule {
  activate(api: PluginGenerationApi): void | Promise<void>;
}

type ModuleLoader = (url: string) => Promise<unknown>;

export interface DesktopPluginFailure {
  pluginId: string;
  fingerprint: string;
  error: unknown;
}

export const desktopPluginHost = new PluginHost({
  react: React,
  invokeRuntime: async ({ pluginId, generation, method, input, workspaceId }) => {
    const response = await window.wuu?.requestPluginRuntime?.({
      id: pluginId,
      fingerprint: generation,
      method,
      input,
      ...(workspaceId ? { workspace_id: workspaceId } : {}),
    });
    if (!response) throw new Error("Plugin runtime requests are unavailable");
    return response.result;
  },
  listWorkspaces: async () => {
    const result = await window.wuu?.listProjects?.();
    if (!result) throw new Error("Workspace listing is unavailable");
    return {
      workspaces: result.projects.map((project) => ({
        id: project.id,
        name: project.name,
        root: project.path,
        available: project.missing !== true,
      })),
      activeWorkspaceId: result.active_project_id,
    };
  },
  listThreads: async (workspaceRoot) => {
    const result = await window.wuu?.listThreads?.(workspaceRoot);
    if (!result) throw new Error("Thread listing is unavailable");
    return result.threads
      .filter((thread) => thread.ephemeral !== true && thread.archived !== true)
      .map((thread) => ({
        id: thread.id,
        title: thread.title || thread.preview || thread.id,
        updatedAt: thread.updated_at,
        pinned: thread.pinned === true,
      }));
  },
});
export const desktopCompositionRoot = createDesktopCompositionRoot();
void desktopCompositionRoot.plugin(PluginHostService, desktopPluginHost);
export const desktopWorkbenchController = new WorkbenchController(desktopPluginHost);
void desktopCompositionRoot.plugin(WorkbenchService, desktopWorkbenchController);

export class DesktopPluginRuntime {
  private readonly activeGenerations = new Map<string, string>();
  private desiredGenerations = new Map<string, string>();
  private syncEpoch = 0;

  constructor(
    readonly host: PluginHost,
    private readonly loadModule: ModuleLoader = importDesktopPluginModule,
  ) {}

  async sync(
    inventory: readonly ExtensionInventoryRecord[],
    safeMode = false,
  ): Promise<readonly DesktopPluginFailure[]> {
    const epoch = ++this.syncEpoch;
    const desired = new Map(
      (safeMode ? [] : inventory)
        .filter(isLoadableDesktopPlugin)
        .map((plugin) => [plugin.id, plugin] as const),
    );

    const previousDesired = this.desiredGenerations;
    this.desiredGenerations = new Map(
      [...desired.values()].map((plugin) => [plugin.id, plugin.fingerprint ?? ""]),
    );

    const knownPluginIds = new Set([
      ...this.activeGenerations.keys(),
      ...previousDesired.keys(),
    ]);
    for (const pluginId of knownPluginIds) {
      if (!desired.has(pluginId)) {
        this.host.unload(pluginId);
        this.activeGenerations.delete(pluginId);
      }
    }

    // Disabling a plugin must take effect even while unrelated preferences are loading.
    const preferences = await window.wuu?.getPluginConflictPreferences?.();
    if (epoch !== this.syncEpoch) return [];
    if (preferences) this.host.setConflictPreferences(preferences);

    // One extension's asynchronous startup must not block unrelated extensions.
    const failures = await Promise.all([...desired.values()].map(async (plugin): Promise<DesktopPluginFailure | undefined> => {
      const fingerprint = plugin.fingerprint;
      if (!fingerprint || this.activeGenerations.get(plugin.id) === fingerprint) {
        return undefined;
      }
      try {
        if (epoch !== this.syncEpoch) return undefined;
        const loaded = await window.wuu?.loadPluginDesktopModule?.({ id: plugin.id, fingerprint });
        if (epoch !== this.syncEpoch) return undefined;
        if (!loaded || loaded.id !== plugin.id || loaded.fingerprint !== fingerprint) {
          throw new Error("Desktop plugin module identity mismatch");
        }
        await this.host.activateGeneration({
          pluginId: plugin.id,
          generation: fingerprint,
          contributions: desktopContributionDeclarations(plugin),
          register: async (api) => {
            const module = requireDesktopPluginModule(await this.loadModule(loaded.url));
            if (epoch !== this.syncEpoch) {
              throw new PluginGenerationSupersededError(plugin.id, fingerprint);
            }
            await module.activate(api);
            if (epoch !== this.syncEpoch) {
              throw new PluginGenerationSupersededError(plugin.id, fingerprint);
            }
          },
        });
        if (epoch === this.syncEpoch) {
          this.activeGenerations.set(plugin.id, fingerprint);
        }
      } catch (error: unknown) {
        if (epoch !== this.syncEpoch || error instanceof PluginGenerationSupersededError) {
          return undefined;
        }
        // Failed staging leaves the previous generation live; only inventory removal disables it.
        return { pluginId: plugin.id, fingerprint, error };
      }
      return undefined;
    }));
    return failures.filter((failure): failure is DesktopPluginFailure => failure !== undefined);
  }
}

function desktopContributionDeclarations(
  plugin: ExtensionInventoryRecord,
): PluginContributionDeclarations {
  const declarations = (plugin.contributions ?? {}) as PluginContributionDeclarations;
  return {
    ...declarations,
    // Inventory omits empty manifest arrays. Desktop-loaded code still has a
    // manifest contract, so absence means no executable contribution was
    // declared rather than opting out of declaration enforcement.
    slots: declarations.slots ?? [],
    surfaces: declarations.surfaces ?? [],
    presenters: declarations.presenters ?? [],
  };
}

export const desktopPluginRuntime = new DesktopPluginRuntime(desktopPluginHost);

export function useDesktopPluginRuntime(
  inventory: readonly ExtensionInventoryRecord[] | undefined,
  safeMode = false,
): void {
  useEffect(() => window.wuu?.onServerEvent?.((event) => {
    desktopPluginHost.publishHostEvent(event);
  }), []);
  useEffect(() => {
    syncExtensionTheme(inventory);
    const syncThemeFromOtherWindow = (): void => syncExtensionTheme(inventory);
    window.addEventListener("storage", syncThemeFromOtherWindow);
    void desktopPluginRuntime.sync(inventory ?? [], safeMode).then((failures) => {
      for (const failure of failures) {
        console.error(`Desktop plugin ${failure.pluginId} failed to activate`, failure.error);
      }
    });
    return () => window.removeEventListener("storage", syncThemeFromOtherWindow);
  }, [inventory, safeMode]);
}

function isLoadableDesktopPlugin(plugin: ExtensionInventoryRecord): boolean {
  const approved = plugin.approval_state === "granted" || plugin.approval_state === "official";
  const active = plugin.state === "granted" || plugin.state === "active";
  return plugin.kind === "plugin"
    && plugin.desktop !== undefined
    && plugin.enabled !== false
    && approved
    && active
    && plugin.runtime_state !== "failed"
    && typeof plugin.fingerprint === "string"
    && plugin.fingerprint.length > 0;
}

function requireDesktopPluginModule(value: unknown): DesktopPluginModule {
  if (typeof value !== "object" || value === null || !("activate" in value)) {
    throw new Error("Desktop plugin module must export activate(api)");
  }
  const activate = Reflect.get(value, "activate");
  if (typeof activate !== "function") {
    throw new Error("Desktop plugin module must export activate(api)");
  }
  return { activate: (api) => Reflect.apply(activate, value, [api]) as void | Promise<void> };
}

async function importDesktopPluginModule(url: string): Promise<unknown> {
  return import(/* @vite-ignore */ url);
}
