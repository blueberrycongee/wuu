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
  private desiredPlugins = new Map<string, ExtensionInventoryRecord>();
  private readonly activeDependencies = new Map<string, string>();
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
    const candidates = new Map(
      (safeMode ? [] : inventory).filter(isAvailablePlugin).map((plugin) => [plugin.id, plugin] as const),
    );
    const packages = new Map([...candidates.values()].map((plugin) => [plugin.provenance.plugin_id, plugin.id]));
    const requiredDependencies = new Map([...candidates.values()].map((plugin) => [
      plugin.id, (plugin.requires ?? []).map((id) => packages.get(id)),
    ] as const));
    const valid = new Map<string, boolean>();
    const visiting = new Set<string>();
    const isValid = (id: string): boolean => {
      if (valid.has(id)) return valid.get(id)!;
      if (visiting.has(id)) return false;
      visiting.add(id);
      const available = (requiredDependencies.get(id) ?? []).every((dependency) => dependency !== undefined && isValid(dependency));
      visiting.delete(id);
      valid.set(id, available);
      return available;
    };
    const desired = new Map([...candidates].filter(([id]) => isValid(id)));
    // Core resolves versions and optional-cycle pruning; the renderer consumes that plan.
    const dependencies = new Map([...desired.values()].map((plugin) => [
      plugin.id, (plugin.resolved_dependencies ?? plugin.requires ?? [])
        .map((id) => packages.get(id)).filter((id): id is string => id !== undefined && desired.has(id)),
    ] as const));
    const dependencySignature = (id: string, live = false): string => {
      const graph = new Map<string, readonly unknown[]>();
      const visit = (provider: string): void => {
        if (graph.has(provider)) return;
        const plugin = desired.get(provider)!;
        const providers = dependencies.get(provider) ?? [];
        const generation = this.activeGenerations.get(provider);
        // A never-started optional provider may keep failing without disturbing
        // consumers. Replacing a live provider still fences its consumers first.
        const available = !plugin.desktop || (live ? generation === plugin.fingerprint : generation !== undefined);
        graph.set(provider, [provider, available ? plugin.fingerprint : null, providers]);
        providers.forEach(visit);
      };
      const providers = dependencies.get(id) ?? [];
      providers.forEach(visit);
      return JSON.stringify([providers, [...graph].sort(([left], [right]) => left.localeCompare(right)).map(([, node]) => node)]);
    };

    const unload = (id: string): void => {
      this.host.unload(id);
      this.activeGenerations.delete(id);
      this.activeDependencies.delete(id);
    };
    const previousDesired = this.desiredPlugins;
    this.desiredPlugins = desired;
    // Dependents release resources before a provider can replace or dispose them.
    // Keep a plugin's own last good generation live while its replacement stages.
    const previousOrder = dependencyOrder(previousDesired);
    const knownPluginIds = new Set([...previousOrder.reverse(), ...this.activeGenerations.keys()]);
    for (const pluginId of knownPluginIds) {
      const plugin = desired.get(pluginId);
      if (!plugin?.desktop || (this.activeDependencies.has(pluginId)
        && this.activeDependencies.get(pluginId) !== dependencySignature(pluginId))) {
        unload(pluginId);
      }
    }

    // Disabling a plugin must take effect even while unrelated preferences are loading.
    const preferences = await window.wuu?.getPluginConflictPreferences?.();
    if (epoch !== this.syncEpoch) return [];
    if (preferences) this.host.setConflictPreferences(preferences);

    // Each branch waits only for its providers, never for unrelated startup work.
    const activations = new Map<string, Promise<boolean>>();
    const failures: DesktopPluginFailure[] = [];
    const activate = (id: string): Promise<boolean> => {
      const existing = activations.get(id);
      if (existing) return existing;
      const plugin = desired.get(id)!;
      const pending = (async (): Promise<boolean> => {
        const providerIds = dependencies.get(id) ?? [];
        const ready = await Promise.all(providerIds.map(activate));
        if (epoch !== this.syncEpoch) return false;
        if (providerIds.some((provider, index) => !ready[index] && requiredDependencies.get(id)?.includes(provider))) {
          if (plugin.desktop) failures.push({ pluginId: id, fingerprint: plugin.fingerprint!, error: new Error("Required desktop dependency failed to activate") });
          return false;
        }
        if (!plugin.desktop) return true;
        const fingerprint = plugin.fingerprint;
        if (!fingerprint || this.activeGenerations.get(plugin.id) === fingerprint) {
          return true;
        }
        try {
          if (epoch !== this.syncEpoch) return false;
          const loaded = await window.wuu?.loadPluginDesktopModule?.({ id: plugin.id, fingerprint });
          if (epoch !== this.syncEpoch) return false;
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
            this.activeDependencies.set(plugin.id, dependencySignature(plugin.id, true));
            // Recovery changes availability without changing inventory. Fence
            // every affected consumer in reverse order before any can restart.
            for (const dependent of dependencyOrder(desired).reverse()) {
              if (this.activeDependencies.has(dependent)
                && this.activeDependencies.get(dependent) !== dependencySignature(dependent, true)) {
                unload(dependent);
              }
            }
          }
        } catch (error: unknown) {
          if (epoch !== this.syncEpoch || error instanceof PluginGenerationSupersededError) {
            return false;
          }
          // Failed staging leaves the previous generation live; only inventory removal disables it.
          failures.push({ pluginId: plugin.id, fingerprint, error });
          return false;
        }
        return true;
      })();
      activations.set(id, pending);
      return pending;
    };
    await Promise.all([...desired.keys()].sort().map(activate));
    return failures.sort((left, right) => left.pluginId.localeCompare(right.pluginId));
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

function isAvailablePlugin(plugin: ExtensionInventoryRecord): boolean {
  const approved = plugin.approval_state === "granted" || plugin.approval_state === "official";
  const active = plugin.state === "granted" || plugin.state === "active";
  return plugin.kind === "plugin"
    && plugin.enabled !== false
    && approved
    && active
    && (plugin.runtime_state === undefined || plugin.runtime_state === "active")
    && !(plugin.activation_issues ?? []).some((issue) => issue.kind === "missing_requirement" || issue.kind === "version_mismatch")
    && typeof plugin.fingerprint === "string"
    && plugin.fingerprint.length > 0;
}

function dependencyOrder(plugins: ReadonlyMap<string, ExtensionInventoryRecord>): string[] {
  const packages = new Map([...plugins.values()].map((plugin) => [plugin.provenance.plugin_id, plugin.id]));
  const visited = new Set<string>();
  const order: string[] = [];
  const visit = (id: string): void => {
    if (visited.has(id)) return;
    visited.add(id);
    for (const requirement of plugins.get(id)?.resolved_dependencies ?? plugins.get(id)?.requires ?? []) {
      const dependency = packages.get(requirement);
      if (dependency) visit(dependency);
    }
    order.push(id);
  };
  [...plugins.keys()].sort().forEach(visit);
  return order;
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
