import type { SetStateAction } from "react";
import type { ApplicationSettingsActionMapV1, ApplicationSettingsViewV1, SettingsReadonly } from "../../../../packages/plugin-sdk/src/application-settings";
import type { EngineListResult } from "../../shared/protocol";
import { activeThreadForState, isThreadRunning, mergeListedThreads, sameRuntimeContext,
  threadBelongsToWorkspace, type AppState } from "../AppState";
import type { CodexModelLoadState } from "../ComposerTypes";
import { createRuntimeSettingsActions } from "../RuntimeSettingsActions";
import { isCodexSubscription, isGrokBuild } from "../SubscriptionSources";
import { hostSupports } from "../HostCapabilities";
import { translateCurrent } from "../i18n";

const actions = new Set<keyof ApplicationSettingsActionMapV1>([
  "settings.save-provider", "settings.remove-provider", "settings.refresh-model-catalog",
  "settings.reconcile-connections", "settings.reconcile-account-models", "settings.refresh-engines",
  "settings.update-engine", "settings.update-advanced", "settings.update-general", "settings.read-usage",
  "settings.read-pets", "settings.update-pets", "settings.list-archived", "settings.delete-archived", "settings.unarchive",
]);
export function isApplicationSettingsAction(action: string): action is keyof ApplicationSettingsActionMapV1 {
  return actions.has(action as keyof ApplicationSettingsActionMapV1);
}

function immutable<T>(value: T): SettingsReadonly<T> {
  const copy = structuredClone(value);
  function freeze(item: unknown): void {
    if (!item || typeof item !== "object" || Object.isFrozen(item)) return;
    for (const child of Object.values(item)) freeze(child);
    Object.freeze(item);
  }
  freeze(copy);
  return copy as SettingsReadonly<T>;
}

/** Host-internal state access never crosses the public application handle. */
export class ApplicationSettings {
  private engines?: { context: string; value: SettingsReadonly<EngineListResult> };
  private engineConfiguration?: AppState["initialized"];
  private engineConnectionGeneration?: number;
  private engineRevision = 0;
  private configurationSource?: AppState["initialized"];
  private configuration?: ApplicationSettingsViewV1["configuration"];
  private snapshot?: ApplicationSettingsViewV1;
  private snapshotRows = "";
  private engineError = { context: "", message: "" };
  private engineRequest = 0;
  private codexModels: CodexModelLoadState = { loading: false, error: "", models: [] };
  constructor(private readonly host: {
    getState: () => AppState;
    updateState: (update: SetStateAction<AppState>) => void;
    changed: () => void;
    isNavigationPending: () => boolean;
    deleteArchivedThread: (threadId: string) => Promise<void>;
    unarchiveThread: (threadId: string) => Promise<void>;
  }) {}

  private engineContext(): { key: string; cwd?: string } {
    const state = this.host.getState();
    const cwd = activeThreadForState(state)?.cwd || state.activeContext?.cwd;
    if (this.engineConfiguration?.extension_inventory !== state.initialized?.extension_inventory || this.engineConnectionGeneration !== state.runtimeConnectionGeneration) this.engineRevision++;
    this.engineConfiguration = state.initialized; this.engineConnectionGeneration = state.runtimeConnectionGeneration;
    return { key: JSON.stringify([state.activeContext, cwd, this.engineRevision]), cwd };
  }

  getSnapshot(): ApplicationSettingsViewV1 {
    const state = this.host.getState();
    const initialized = state.initialized;
    if (initialized !== this.configurationSource) {
      this.configurationSource = initialized;
      this.configuration = initialized && immutable({
      protocol_version: initialized.protocol_version, provider: initialized.provider,
      model: initialized.model, effort: initialized.effort, variant: initialized.variant, workspace_root: initialized.workspace_root, core: initialized.core,
      providers: initialized.providers, catalog_providers: initialized.catalog_providers,
      model_aliases: initialized.model_aliases, project_models: initialized.project_models,
      advanced_settings: initialized.advanced_settings, general_settings: initialized.general_settings,
      features: initialized.features,
      extension_inventory: initialized.extension_inventory?.map(plugin => ({
        id: plugin.id, name: plugin.name, description: plugin.description, kind: plugin.kind,
        package_source: plugin.package_source, enabled_override: plugin.enabled_override, icon: plugin.icon,
        provenance: plugin.provenance, state: plugin.state, fingerprint: plugin.fingerprint, enabled: plugin.enabled, approval_state: plugin.approval_state,
        contributions: plugin.contributions && { settings: plugin.contributions.settings },
      })),
      });
    }
    const configuration = this.configuration;
    const runningProviderNames = [...new Set([state.thread, state.secondaryThread, ...state.threads]
      .filter(thread => thread && isThreadRunning(thread)).map(thread => thread!.model_provider.trim()).filter(Boolean))];
    const archivedThreads = state.threads.filter(thread => thread.archived).map(thread => {
      const project = state.projects.find(candidate => threadBelongsToWorkspace(thread, candidate));
      return { id: thread.id, title: thread.title, updated_at: thread.updated_at,
        archive_project_id: project?.id ?? "", archive_project_name: project?.name ?? translateCurrent("appState.noWorkspace") };
    });
    const context = this.engineContext().key;
    const engineInventory = this.engines?.context === context ? this.engines.value : undefined;
    const engineInventoryError = this.engineError.context === context ? this.engineError.message : "";
    const runtimeSwitchPending = this.host.isNavigationPending();
    const rows = JSON.stringify([runningProviderNames, archivedThreads]);
    const previous = this.snapshot;
    if (previous && previous.configuration === configuration && previous.engineInventory === engineInventory
      && previous.engineInventoryError === engineInventoryError && previous.runtimeSwitchPending === runtimeSwitchPending
      && rows === this.snapshotRows) return previous;
    this.snapshotRows = rows;
    this.snapshot = Object.freeze({ configuration, engineInventory, engineInventoryError, runtimeSwitchPending,
      runningProviderNames: Object.freeze(runningProviderNames), archivedThreads: Object.freeze(archivedThreads.map(thread => Object.freeze(thread))),
    });
    return this.snapshot;
  }

  async invoke<Action extends keyof ApplicationSettingsActionMapV1>(action: Action,
    input: ApplicationSettingsActionMapV1[Action]["input"], signal?: AbortSignal,
  ): Promise<ApplicationSettingsActionMapV1[Action]["result"]> {
    signal?.throwIfAborted();
    const source = this.host.getState();
    const currentContext = (): boolean => sameRuntimeContext(source.activeContext, this.host.getState().activeContext);
    const updateState = (update: SetStateAction<AppState>): void => {
      signal?.throwIfAborted();
      if (!currentContext()) return;
      this.host.updateState(update);
    };
    // Reuse the existing runtime policy. Its composer-only menu callbacks are
    // outside these settings commands; remove this adapter after that factory splits.
    const runtime = createRuntimeSettingsActions({ getAppState: this.host.getState, setAppState: updateState,
      getViewContextSwitchPending: this.host.isNavigationPending, getCodexModels: () => this.codexModels,
      setCodexModels: update => { this.codexModels = typeof update === "function" ? update(this.codexModels) : update; },
      setRuntimeMenuOpen: () => {}, setAccessMenuOpen: () => {}, setBranchMenuOpen: () => {}, setCodexRuntimeMenu: () => {},
      clearThreadPendingComposerMessages: () => {}, variantByModel: new Map(), pendingRuntimeSelections: new Map(),
    });
    let result: unknown;
    switch (action) {
      case "settings.save-provider": {
        const value = input as ApplicationSettingsActionMapV1["settings.save-provider"]["input"];
        await runtime.updateProviderSettings(value.provider, value.model, value.effort, value.connection, value.variant); break;
      }
      case "settings.remove-provider": {
        const value = input as ApplicationSettingsActionMapV1["settings.remove-provider"]["input"];
        await runtime.removeProvider(value.provider, { fallbackProvider: value.fallbackProvider, fallbackModel: value.fallbackModel }); break;
      }
      case "settings.update-advanced":
        await runtime.updateAdvancedSettings(input as ApplicationSettingsActionMapV1["settings.update-advanced"]["input"]); break;
      case "settings.update-general":
        await runtime.updateGeneralSettings(input as ApplicationSettingsActionMapV1["settings.update-general"]["input"]); break;
      case "settings.refresh-model-catalog": {
        const next = await window.wuu.refreshModelCatalog();
        updateState(state => state.initialized ? { ...state, initialized: { ...state.initialized,
          providers: next.providers, catalog_providers: next.catalog_providers } } : state);
        if (currentContext()) for (const provider of next.providers) {
          if (["openai-codex", "codex-subscription", "chatgpt-codex"].includes(provider.type.replaceAll("_", "-").toLowerCase())) {
            await runtime.loadCodexModelsForProvider(provider.name, true);
          }
        }
        result = immutable({ modelCount: next.model_count }); break;
      }
      case "settings.reconcile-connections": {
        const { providers } = input as ApplicationSettingsActionMapV1["settings.reconcile-connections"]["input"];
        updateState(state => state.initialized ? { ...state, initialized: { ...state.initialized,
          providers: state.initialized.providers?.map(provider => {
            if (isCodexSubscription(provider.type) || isGrokBuild(provider.type)) {
              const match = providers.find(item => item.name === provider.name && item.type === provider.type && item.base_url === provider.base_url);
              return match ? { ...provider, auth_status: match.auth_status, api_key_configured: match.api_key_configured,
                codex_credential_source: match.codex_credential_source, reuse_codex_credentials: match.reuse_codex_credentials,
                models: match.models ? structuredClone(match.models) as typeof provider.models : provider.models } : provider;
            }
            if (!provider.chatgpt_account_id || provider.type !== "chatgpt") return provider;
            const match = providers.find(item => item.type === "chatgpt" && item.name === provider.name && item.chatgpt_account_id === provider.chatgpt_account_id);
            return match ? { ...provider, auth_status: match.auth_status, api_key_configured: match.api_key_configured,
              models: match.auth_status === "connected" ? structuredClone(match.models ?? []) as typeof provider.models : [] } : provider;
          }) } } : state); break;
      }
      case "settings.reconcile-account-models": {
        const value = input as ApplicationSettingsActionMapV1["settings.reconcile-account-models"]["input"];
        updateState(state => state.initialized ? { ...state, initialized: { ...state.initialized,
          providers: state.initialized.providers?.map(provider => provider.name === value.provider
            && provider.chatgpt_account_id === value.accountId && provider.auth_status === "connected"
            ? { ...provider, models: structuredClone(value.models) as typeof provider.models } : provider) } } : state); break;
      }
      case "settings.refresh-engines": result = await this.refreshEngines(signal); break;
      case "settings.update-engine": {
        const context = this.engineContext().key;
        ++this.engineRequest;
        const next = await window.wuu.updateEngines(input as ApplicationSettingsActionMapV1["settings.update-engine"]["input"]);
        signal?.throwIfAborted();
        result = context === this.engineContext().key ? await this.refreshEngines(signal) ?? immutable(next) : immutable(next); break;
      }
      case "settings.read-usage":
        if (!window.wuu.getSettingsUsage) throw new Error(translateCurrent("settings.usageUnsupported"));
        try { result = immutable(await window.wuu.getSettingsUsage()); }
        catch { throw new Error(translateCurrent("settings.usageLoadFailed")); }
        break;
      case "settings.read-pets":
        if (!window.wuu.listCodexPets || !hostSupports("listCodexPets")) throw new Error(translateCurrent("settings.pets.unsupported"));
        result = immutable(await window.wuu.listCodexPets()); break;
      case "settings.update-pets":
        if (!window.wuu.updateCodexPetSettings || !hostSupports("updateCodexPetSettings")) throw new Error(translateCurrent("settings.pets.unsupported"));
        result = immutable(await window.wuu.updateCodexPetSettings(input as ApplicationSettingsActionMapV1["settings.update-pets"]["input"])); break;
      case "settings.list-archived": {
        const { threads } = await window.wuu.listArchivedThreads();
        signal?.throwIfAborted();
        this.host.updateState(state => ({ ...state, threads: mergeListedThreads(state.threads,
          [...state.threads.filter(thread => !thread.archived), ...threads]) }));
        result = immutable(threads.map(thread => ({ id: thread.id, archived: thread.archived }))); break;
      }
      case "settings.delete-archived": {
        const { threadId } = input as ApplicationSettingsActionMapV1["settings.delete-archived"]["input"];
        await this.host.deleteArchivedThread(threadId); break;
      }
      case "settings.unarchive": {
        const { threadId } = input as ApplicationSettingsActionMapV1["settings.unarchive"]["input"];
        await this.host.unarchiveThread(threadId); break;
      }
    }
    signal?.throwIfAborted();
    return result as ApplicationSettingsActionMapV1[Action]["result"];
  }

  private async refreshEngines(signal?: AbortSignal): Promise<SettingsReadonly<EngineListResult> | undefined> {
    if (!window.wuu.listEngines) return undefined;
    const context = this.engineContext();
    const request = ++this.engineRequest;
    try {
      const value = await window.wuu.listEngines({ cwd: context.cwd, refresh_models: true });
      signal?.throwIfAborted();
      if (request === this.engineRequest && context.key === this.engineContext().key) {
        this.engines = { context: context.key, value: immutable(value) }; this.engineError = { context: context.key, message: "" }; this.host.changed();
      }
      return immutable(value);
    } catch (error) {
      if (!signal?.aborted && request === this.engineRequest && context.key === this.engineContext().key) {
        this.engineError = { context: context.key, message: error instanceof Error ? error.message : String(error) }; this.host.changed();
      }
      throw error;
    }
  }
}
