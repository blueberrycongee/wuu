import * as React from "react";
import { useEffect } from "react";

import type { ExtensionInventoryRecord } from "../../shared/protocol";
import { syncExtensionTheme } from "../Theme";
import { syncShellSkins } from "../background/ShellSkin";
import {
  PluginHost,
  PluginGenerationSupersededError,
  type PluginContributionDeclarations,
  type PluginGenerationApi,
} from "./PluginHost";
import { settleOnAbort } from "./activation";
import { getDesktopApplication } from "./ApplicationController";
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
  reportActivation: params => window.wuu.reportPluginDesktopActivation(params),
  react: React,
  application: {
    contractVersion: 1,
    get onboarding() { return getDesktopApplication().onboarding; },
    get browser() { return getDesktopApplication().browser; },
    get fileSelection() { return getDesktopApplication().fileSelection; },
    get projects() { return getDesktopApplication().projects; },
    get terminals() { return getDesktopApplication().terminals; },
    get catalog() { return getDesktopApplication().catalog; },
    get previewContent() { return getDesktopApplication().previewContent; },
    get conversationText() { return getDesktopApplication().conversationText; },
    get settingsControls() { return getDesktopApplication().settingsControls; },
    getSnapshot: () => getDesktopApplication().getSnapshot(),
    subscribe: listener => getDesktopApplication().subscribe(listener),
    invoke: (action, input, options) => getDesktopApplication().invoke(action, input, options),
  },
  invokeRuntime: async ({ pluginId, generation, graphGeneration, service, method, input, workspaceId, workspaceRoot, signal }) => {
    signal?.throwIfAborted();
    const bridge = window.wuu;
    if (!bridge?.requestPluginRuntime || !bridge.cancelPluginRuntime) {
      throw new Error("Plugin runtime requests are unavailable");
    }
    // Desktop activation requires a committed graph before admitting requests.
    const identity = { id: pluginId, fingerprint: generation, graph_generation: graphGeneration!, request_id: crypto.randomUUID() };
    const response = bridge.requestPluginRuntime({
      ...identity,
      ...(service !== undefined ? { service } : {}),
      method,
      input,
      ...(workspaceId ? { workspace_id: workspaceId } : {}),
      ...(workspaceRoot ? { workspace_root: workspaceRoot } : {}),
    });
    const cancel = () => {
      void bridge.cancelPluginRuntime(identity).catch((error: unknown) => {
        console.error("Plugin runtime cancellation failed", error);
      });
    };
    signal?.addEventListener("abort", cancel, { once: true });
    if (signal?.aborted) cancel();
    try {
      const result = await response;
      signal?.throwIfAborted();
      return result.result;
    } finally {
      signal?.removeEventListener("abort", cancel);
    }
  },
  readVisibleHistory: async (request) => {
    if (!window.wuu?.readPluginVisibleHistory) throw new Error("Visible conversation history is unavailable");
    return window.wuu.readPluginVisibleHistory(request);
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
      activeRoot: result.active_context?.cwd,
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
  private readonly pendingDependencies = new Map<string, { signature: string }>();
  private syncEpoch = 0;
  private syncController?: AbortController;
  private connectionGeneration = 0;
  private connectionReset?: Promise<void>;

  constructor(
    readonly host: PluginHost,
    private readonly loadModule: ModuleLoader = importDesktopPluginModule,
  ) {}

  async sync(
    inventory: readonly ExtensionInventoryRecord[],
    safeMode = false,
    connectionGeneration = 0,
  ): Promise<readonly DesktopPluginFailure[]> {
    const epoch = ++this.syncEpoch;
    this.syncController?.abort();
    const controller = new AbortController();
    this.syncController = controller;
    const unload = (id: string): Promise<void> | undefined => {
      const cleanup = this.host.unloadAndWait(id);
      this.activeGenerations.delete(id);
      this.activeDependencies.delete(id);
      this.pendingDependencies.delete(id);
      return cleanup;
    };
    if (this.connectionGeneration !== connectionGeneration) {
      this.connectionGeneration = connectionGeneration;
      this.host.setInventoryGraphs([]);
      const previousReset = this.connectionReset;
      this.connectionReset = (async () => {
        await previousReset;
        // Overlapping restores must share teardown, including retained React consumers.
        const previousOrder = dependencyOrder(this.desiredPlugins).reverse();
        for (const pluginId of new Set([...previousOrder, ...this.activeGenerations.keys(), ...this.pendingDependencies.keys()])) {
          const cleanup = unload(pluginId);
          if (cleanup) await cleanup;
        }
        // Drain obsolete ACKs while no graph is admitted on the new connection.
        await this.host.flushActivationReports();
      })();
    }
    if (this.connectionReset) {
      const reset = this.connectionReset;
      await reset;
      if (epoch !== this.syncEpoch) return [];
      if (this.connectionReset === reset) this.connectionReset = undefined;
    }
    this.host.setInventoryGraphs(inventory);
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
        const available = !plugin.desktop || (live ? generation === desktopGeneration(plugin) : generation !== undefined);
        graph.set(provider, [provider, available ? desktopGeneration(plugin) : null, providers]);
        providers.forEach(visit);
      };
      const providers = dependencies.get(id) ?? [];
      providers.forEach(visit);
      return JSON.stringify([providers, [...graph].sort(([left], [right]) => left.localeCompare(right)).map(([, node]) => node)]);
    };
    const dependenciesChanged = (id: string, live = false): boolean => {
      const active = this.activeDependencies.get(id);
      const pending = this.pendingDependencies.get(id)?.signature;
      if (active === undefined && pending === undefined) return false;
      const signature = dependencySignature(id, live);
      return (active !== undefined && active !== signature) || (pending !== undefined && pending !== signature);
    };

    const previousDesired = this.desiredPlugins;
    this.desiredPlugins = desired;
    // Dependents release resources before a provider can replace or dispose them.
    // Keep a plugin's own last good generation live while its replacement stages.
    const previousOrder = dependencyOrder(previousDesired);
    const knownPluginIds = new Set([...previousOrder.reverse(), ...this.activeGenerations.keys()]);
    for (const pluginId of knownPluginIds) {
      const plugin = desired.get(pluginId);
      if (!plugin?.desktop || dependenciesChanged(pluginId)) {
        const cleanup = unload(pluginId);
        if (cleanup) await cleanup;
        if (epoch !== this.syncEpoch) return [];
      }
    }

    // Disabling a plugin must take effect even while unrelated preferences are loading.
    try {
      const preferences = await settleOnAbort(() => window.wuu?.getPluginConflictPreferences?.(), controller.signal);
      if (epoch !== this.syncEpoch) return [];
      if (preferences) this.host.setConflictPreferences(preferences);
    } catch (error: unknown) {
      if (controller.signal.aborted) return [];
      throw error;
    }

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
          if (plugin.desktop) {
            const error = new Error("Required desktop dependency failed to activate");
            try {
              await this.host.activateGeneration({ pluginId: id, generation: plugin.fingerprint!, graphGeneration: plugin.graph_generation, register: () => { throw error; } });
            } catch (failure: unknown) {
              failures.push({ pluginId: id, fingerprint: plugin.fingerprint!, error: failure });
            }
          }
          return false;
        }
        if (!plugin.desktop) return true;
        const fingerprint = plugin.fingerprint;
        if (!fingerprint || this.activeGenerations.get(plugin.id) === desktopGeneration(plugin)) {
          return true;
        }
        // Staging callbacks can already own provider resources before activation commits.
        const pending = { signature: dependencySignature(plugin.id, true) };
        this.pendingDependencies.set(plugin.id, pending);
        try {
          if (epoch !== this.syncEpoch) return false;
          await this.host.activateGeneration({
            pluginId: plugin.id,
            manifestId: plugin.provenance.plugin_id ?? plugin.id,
            generation: fingerprint,
            graphGeneration: plugin.graph_generation,
            contributions: desktopContributionDeclarations(plugin),
            signal: controller.signal,
            register: async (api) => {
              const loaded = await window.wuu?.loadPluginDesktopModule?.({ id: plugin.id, fingerprint });
              api.signal.throwIfAborted();
              if (!loaded || loaded.id !== plugin.id || loaded.fingerprint !== fingerprint) {
                throw new Error("Desktop plugin module identity mismatch");
              }
              const loadedModule = await this.loadModule(loaded.url);
              api.signal.throwIfAborted();
              const module = requireDesktopPluginModule(loadedModule);
              await module.activate(api);
              api.signal.throwIfAborted();
            },
          });
          if (epoch === this.syncEpoch) {
            this.activeGenerations.set(plugin.id, desktopGeneration(plugin));
            this.activeDependencies.set(plugin.id, dependencySignature(plugin.id, true));
            // Recovery changes availability without changing inventory. Fence
            // every affected consumer in reverse order before any can restart.
            for (const dependent of dependencyOrder(desired).reverse()) {
              if (dependenciesChanged(dependent, true)) {
                const cleanup = unload(dependent);
                if (cleanup) await cleanup;
                if (epoch !== this.syncEpoch) return false;
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
        } finally {
          if (this.pendingDependencies.get(plugin.id) === pending) {
            this.pendingDependencies.delete(plugin.id);
          }
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

function desktopGeneration(plugin: ExtensionInventoryRecord): string {
  return JSON.stringify([plugin.graph_generation, plugin.fingerprint]);
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
  connectionGeneration = 0,
): void {
  useEffect(() => window.wuu?.onServerEvent?.((event) => {
    desktopPluginHost.publishHostEvent(event);
  }), []);
  useEffect(() => {
    syncExtensionTheme(safeMode ? [] : inventory);
    syncShellSkins(inventory, safeMode);
    const syncThemeFromOtherWindow = (): void => syncExtensionTheme(safeMode ? [] : inventory);
    window.addEventListener("storage", syncThemeFromOtherWindow);
    void desktopPluginRuntime.sync(inventory ?? [], safeMode, connectionGeneration).then((failures) => {
      for (const failure of failures) {
        console.error(`Desktop plugin ${failure.pluginId} failed to activate`, failure.error);
      }
    }).catch((error: unknown) => console.error("Desktop plugin synchronization failed", error));
    return () => window.removeEventListener("storage", syncThemeFromOtherWindow);
  }, [inventory, safeMode, connectionGeneration]);
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
  if (url === "wuu-builtin:default-shell") return import("./DefaultApplicationShell");
  if (url === "wuu-builtin:default-settings") return import("../modules/DefaultSettingsSurface");
  if (url === "wuu-builtin:default-navigation") return import("../modules/DefaultNavigationSurface");
  return import(/* @vite-ignore */ url);
}
