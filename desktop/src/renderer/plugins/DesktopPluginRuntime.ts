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
import { PluginHostService, createDesktopCompositionRoot } from "./composition";

interface DesktopPluginModule {
  activate?(api: PluginGenerationApi): void | Promise<void>;
  activatePresentation?: import("../../../../packages/plugin-sdk/src/ui").ApplicationPresentationModule["activatePresentation"];
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
        latestCompletedTurnId: thread.latest_completed_turn_id,
        pinned: thread.pinned === true,
      }));
  },
});
export const desktopCompositionRoot = createDesktopCompositionRoot();
void desktopCompositionRoot.plugin(PluginHostService, desktopPluginHost);
export class DesktopPluginRuntime {
  private readonly activeGenerations = new Map<string, string>();
  private desiredPlugins = new Map<string, ExtensionInventoryRecord>();
  private readonly activeDependencies = new Map<string, string>();
  private readonly pendingDependencies = new Map<string, { signature: string }>();
  private syncEpoch = 0;
  private syncController?: AbortController;
  private connectionGeneration = 0;
  private connectionReset?: Promise<void>;
  private readonly synchronizations = new Set<Promise<readonly DesktopPluginFailure[]>>();
  private shutdownPromise?: Promise<void>;

  constructor(
    readonly host: PluginHost,
    private readonly loadModule: ModuleLoader = importDesktopPluginModule,
  ) {}

  sync(
    inventory: readonly ExtensionInventoryRecord[],
    safeMode = false,
    connectionGeneration = 0,
  ): Promise<readonly DesktopPluginFailure[]> {
    if (this.shutdownPromise) return Promise.resolve([]);
    const pending = this.synchronize(inventory, safeMode, connectionGeneration);
    this.synchronizations.add(pending);
    return pending.finally(() => this.synchronizations.delete(pending));
  }

  /** Call after unmounting React consumers, while the runtime bridge is alive. */
  shutdown(): Promise<void> {
    if (this.shutdownPromise) return this.shutdownPromise;
    this.shutdownPromise = Promise.resolve().then(async () => {
      ++this.syncEpoch;
      this.syncController?.abort();
      await this.connectionReset;
      const order = dependencyOrder(this.desiredPlugins).reverse();
      for (const id of new Set([...order, ...this.activeGenerations.keys(), ...this.pendingDependencies.keys()])) {
        await this.host.unloadAndWait(id);
      }
      await Promise.allSettled([...this.synchronizations]);
      await this.host.flushActivationReports();
      this.activeGenerations.clear();
      this.activeDependencies.clear();
      this.pendingDependencies.clear();
      this.desiredPlugins.clear();
      const failure = this.host.getActivationReportFailure();
      if (failure) throw new Error(failure);
    });
    return this.shutdownPromise;
  }

  private async synchronize(
    inventory: readonly ExtensionInventoryRecord[],
    safeMode: boolean,
    connectionGeneration: number,
  ): Promise<readonly DesktopPluginFailure[]> {
    const epoch = ++this.syncEpoch;
    this.syncController?.abort();
    const controller = new AbortController();
    this.syncController = controller;
    let preparedApplication: { id: string; stage: Awaited<ReturnType<PluginHost["prepareGeneration"]>> } | undefined;
    const unload = (id: string): Promise<void> | undefined => {
      const cleanup = this.host.unloadAndWait(id, preparedApplication?.id === id);
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
      plugin.id, (plugin.resolved_required_dependencies ?? plugin.requires ?? []).map((id) => packages.get(id)),
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

    const activationOptions = (plugin: ExtensionInventoryRecord) => ({
      pluginId: plugin.id, manifestId: plugin.provenance.plugin_id ?? plugin.id,
      generation: plugin.fingerprint!, graphGeneration: plugin.graph_generation,
      contributions: desktopContributionDeclarations(plugin), signal: controller.signal,
      register: async (api: PluginGenerationApi) => {
        const loaded = await window.wuu?.loadPluginDesktopModule?.({ id: plugin.id, fingerprint: plugin.fingerprint! });
        api.signal.throwIfAborted();
        if (!loaded || loaded.id !== plugin.id || loaded.fingerprint !== plugin.fingerprint) throw new Error("Desktop plugin module identity mismatch");
        const loadedModule = await this.loadModule(loaded.url);
        api.signal.throwIfAborted();
        const module = requireDesktopPluginModule(loadedModule);
        if (module.activatePresentation) await this.host.registerPresentation(api, module.activatePresentation);
        else await module.activate!(api);
        api.signal.throwIfAborted();
      },
    });
    const selectedApplication = this.host.getApplicationBinding();
    const applicationPlugin = selectedApplication && desired.get(selectedApplication.pluginId);
    if (applicationPlugin?.desktop && this.activeGenerations.get(applicationPlugin.id) !== desktopGeneration(applicationPlugin)) {
      try {
        const stage = await this.host.prepareGeneration(activationOptions(applicationPlugin));
        if (epoch !== this.syncEpoch) { stage.dispose(); return []; }
        preparedApplication = { id: applicationPlugin.id, stage };
      } catch (error) {
        const effective = this.host.applicationProviders.current;
        if (effective?.graphGeneration && applicationPlugin.graph_generation && effective.graphGeneration !== applicationPlugin.graph_generation) {
          // The Go commit cannot be rolled back by retaining JavaScript objects. In
          // particular, native bridge calls must not let an old writer enter the new graph.
          try { await this.host.applicationProviders.select(undefined); }
          catch (cleanupError) { error = new AggregateError([error, cleanupError], "Renderer preparation and previous-writer suspension failed"); }
          this.host.applicationProviders.reportFailure(`Go graph ${applicationPlugin.graph_generation} is committed, but renderer activation failed: ${String(error)}. Previous business writer suspended; repair or restore the composition to resume`);
        } else this.host.applicationProviders.reportFailure(`Renderer preparation failed; retained the previous local activation: ${String(error)}`);
        this.host.reportApplicationActivationFailure(applicationPlugin.id, applicationPlugin.fingerprint!, applicationPlugin.graph_generation, error);
        return [{ pluginId: applicationPlugin.id, fingerprint: applicationPlugin.fingerprint!, error }];
      }
    }

    // Refresh presentation while the source is still current, after potentially
    // slow preparation and before any consumer scope can observe retirement.
    const finishRetirement = preparedApplication && await this.host.applicationProviders.prepareRetirement(controller.signal);
    try {
      if (epoch !== this.syncEpoch) return [];
      const previousDesired = this.desiredPlugins;
      this.desiredPlugins = desired;
      // Dependents release resources before a provider can replace or dispose them.
      // Keep a plugin's own last good generation live while its replacement stages.
      const previousOrder = dependencyOrder(previousDesired);
      const knownPluginIds = new Set([...previousOrder.reverse(), ...this.activeGenerations.keys()]);
      for (const pluginId of knownPluginIds) {
        const plugin = desired.get(pluginId);
        if (!plugin?.desktop || dependenciesChanged(pluginId) && !this.host.canRebindPresentation(pluginId, plugin.fingerprint!)) {
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
                if (selectedApplication?.pluginId === id) finishRetirement?.();
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
            if (preparedApplication?.id === plugin.id) await preparedApplication.stage.activate();
            else if (this.host.canRebindPresentation(plugin.id, fingerprint)) this.host.rebindPresentation(plugin.id, fingerprint, plugin.graph_generation);
            else await this.host.activateGeneration(activationOptions(plugin));
            if (epoch === this.syncEpoch) {
              this.activeGenerations.set(plugin.id, desktopGeneration(plugin));
              this.activeDependencies.set(plugin.id, dependencySignature(plugin.id, true));
              // Recovery changes availability without changing inventory. Fence
              // every affected consumer in reverse order before any can restart.
              for (const dependent of dependencyOrder(desired).reverse()) {
                if (dependenciesChanged(dependent, true) && !this.host.canRebindPresentation(dependent, desired.get(dependent)!.fingerprint!)) {
                  const cleanup = unload(dependent);
                  if (cleanup) await cleanup;
                  if (epoch !== this.syncEpoch) return false;
                }
              }
            }
          } catch (error: unknown) {
            if (selectedApplication?.pluginId === plugin.id) finishRetirement?.();
            if (epoch !== this.syncEpoch || error instanceof PluginGenerationSupersededError) {
              return false;
            }
            if (selectedApplication?.pluginId === plugin.id) {
              this.host.reportApplicationActivationFailure(plugin.id, fingerprint!, plugin.graph_generation, error);
            }
            // Module registration can survive a failed candidate; business writers obey
            // the stronger committed-graph suspension boundary reported above.
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
    } finally { finishRetirement?.(); }
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
    presenters: declarations.presenters ?? [],
  };
}

export const desktopPluginRuntime = new DesktopPluginRuntime(desktopPluginHost);

export function useDesktopPluginRuntime(
  inventory: readonly ExtensionInventoryRecord[] | undefined,
  safeMode = false,
  connectionGeneration = 0,
): boolean {
  const [settled, setSettled] = React.useState<{ inventory: typeof inventory; safeMode: boolean; connectionGeneration: number }>();
  useEffect(() => window.wuu?.onServerEvent?.((event) => {
    desktopPluginHost.publishHostEvent(event);
  }), []);
  useEffect(() => {
    if (inventory === undefined) return;
    let current = true;
    syncExtensionTheme(safeMode ? [] : inventory);
    syncShellSkins(inventory, safeMode);
    const syncThemeFromOtherWindow = (): void => syncExtensionTheme(safeMode ? [] : inventory);
    window.addEventListener("storage", syncThemeFromOtherWindow);
    void desktopPluginRuntime.sync(inventory ?? [], safeMode, connectionGeneration).then((failures) => {
      for (const failure of failures) {
        console.error(`Desktop plugin ${failure.pluginId} failed to activate`, failure.error);
      }
    }).catch((error: unknown) => console.error("Desktop plugin synchronization failed", error))
      .finally(() => { if (current) setSettled({ inventory, safeMode, connectionGeneration }); });
    return () => { current = false; window.removeEventListener("storage", syncThemeFromOtherWindow); };
  }, [inventory, safeMode, connectionGeneration]);
  // The first render with a new inventory precedes the effect that loads it.
  // An older synchronization must not turn that pending generation into an error.
  return inventory !== undefined && (settled?.inventory !== inventory || settled.safeMode !== safeMode || settled.connectionGeneration !== connectionGeneration);
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
  if (typeof value !== "object" || value === null) throw new Error("Desktop plugin module must export activate(api) or activatePresentation(api)");
  const activatePresentation = Reflect.get(value, "activatePresentation");
  if (typeof activatePresentation === "function") return { activatePresentation: api => Reflect.apply(activatePresentation, value, [api]) as void | Promise<void> };
  const activate = Reflect.get(value, "activate");
  if (typeof activate !== "function") throw new Error("Desktop plugin module must export activate(api) or activatePresentation(api)");
  return { activate: api => Reflect.apply(activate, value, [api]) as void | Promise<void> };
}

async function importDesktopPluginModule(url: string): Promise<unknown> {
  if (url === "wuu-builtin:default-application") return import("./DefaultApplicationProvider");
  if (url === "wuu-builtin:default-shell") return import("./DefaultApplicationShell");
  if (url === "wuu-builtin:default-settings") return import("../modules/DefaultSettingsSurface");
  if (url === "wuu-builtin:default-navigation") return import("../modules/DefaultNavigationSurface");
  if (url === "wuu-builtin:default-conversation") return import("../modules/DefaultConversationSurface");
  if (url === "wuu-builtin:default-composer") return import("../modules/DefaultComposerSurface");
  if (url === "wuu-builtin:default-preview") return import("../modules/DefaultPreviewSurface");
  return import(/* @vite-ignore */ url);
}
