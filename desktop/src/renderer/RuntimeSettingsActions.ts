import type { SetStateAction } from "react";
import type {
  RuntimeAdvancedSettingsUpdate,
  RuntimeConnectionUpdate,
  RuntimeGeneralSettingsUpdate,
  Thread,
} from "../shared/protocol";
import {
  activeThreadForState,
  threadForPane,
  updateThreadByID,
  type AppState,
  type ConversationPaneID,
} from "./AppState";
import type {
  CodexModelLoadState,
  CodexRuntimeMenu,
  PermissionMode,
} from "./ComposerTypes";
import { lastEffortForRuntimeModel, writeDraftApproveForMeMemory, writeDraftPermissionMemory, writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { isCodexProvider, normalizedVariantForProviderModel } from "./RuntimeHelpers";
import { runtimeViewForSession } from "./SessionRuntimeState";
import { showErrorToast } from "./Toast";
import { translateCurrent } from "./i18n";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type RuntimeSettingsActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getViewContextSwitchPending: () => boolean;
  getCodexModels: () => CodexModelLoadState;
  setCodexModels: (update: SetStateAction<CodexModelLoadState>) => void;
  setRuntimeMenuOpen: (open: boolean) => void;
  setAccessMenuOpen: (open: boolean) => void;
  setBranchMenuOpen: (open: boolean) => void;
  setCodexRuntimeMenu: (update: SetStateAction<CodexRuntimeMenu>) => void;
  clearThreadPendingComposerMessages: (threadID: string) => void;
  markOptimisticTurnInterrupted?: (threadID: string) => void;
  variantByModel: Map<string, string>;
};

export type RuntimeSettingsActions = {
  updateRuntimeSettings: (
    provider: string,
    model: string,
    effort?: string,
    connection?: RuntimeConnectionUpdate,
    variant?: string,
    permissionMode?: string,
  ) => Promise<void>;
  updateProviderSettings: RuntimeSettingsActions["updateRuntimeSettings"];
  updateAdvancedSettings: (
    settings: RuntimeAdvancedSettingsUpdate,
  ) => Promise<void>;
  updateGeneralSettings: (
    settings: RuntimeGeneralSettingsUpdate,
  ) => Promise<void>;
  removeProvider: (
    provider: string,
    options?: { fallbackProvider?: string; fallbackModel?: string },
  ) => Promise<void>;
  toggleCodexRuntimeMenu: (menu: Exclude<CodexRuntimeMenu, null>) => void;
  loadCodexModelsForProvider: (provider: string, refresh?: boolean) => Promise<void>;
  selectRuntimeModel: (
    provider: string,
    model: string,
    variant?: string,
  ) => Promise<boolean>;
  selectRuntimeEffort: (nextVariant: string) => Promise<boolean>;
  selectPermissionMode: (mode: PermissionMode, approveForMe?: boolean) => Promise<void>;
  setApproveForMe: (enabled: boolean) => Promise<void>;
  interrupt: () => Promise<void>;
  interruptPane: (pane: ConversationPaneID) => Promise<void>;
};

type RuntimeSelectionUpdate = {
  provider?: string;
  model?: string;
  effort?: string;
  connection?: RuntimeConnectionUpdate;
  variant?: string;
  permissionMode?: string;
  approveForMe?: boolean;
};

export function createRuntimeSettingsActions(
  deps: RuntimeSettingsActionsDeps,
): RuntimeSettingsActions {
  function modelSelectionKey(
    scope: string,
    provider: string,
    model: string,
  ): string {
    return JSON.stringify([scope, provider.trim(), model.trim()]);
  }

  // Settings save workspace defaults; composer changes only the target
  // conversation (or a local selection before its first send).
  async function sendRuntimeSelection(
    update: RuntimeSelectionUpdate,
    scope: "session" | "workspace" = "session",
  ): Promise<void> {
    const state = deps.getAppState();
    if (!state.initialized) {
      return;
    }
    const targetThread = scope === "session" ? activeThreadForState(state) : undefined;
    if (scope === "session" && !targetThread && !update.connection) {
      // Before thread/start this is a window-local draft selection, not a
      // provider default. The first send passes it explicitly to thread/start.
      const nextProvider = update.provider ?? state.initialized.provider;
      const nextModel = update.model ?? state.initialized.model;
      const nextEffort = update.variant ?? update.effort
        ?? state.initialized.variant
        ?? state.initialized.effort
        ?? "";
      rememberDraftRuntime(nextProvider, nextModel, nextEffort);
      deps.setAppState((current) => ({
        ...current,
        initialized: current.initialized ? {
          ...current.initialized,
          provider: nextProvider,
          model: nextModel,
          variant: nextEffort,
          effort: nextEffort,
          permissions: {
            ...current.initialized.permissions,
            ...(update.permissionMode === undefined ? {} : { mode: update.permissionMode as PermissionMode }),
            ...(update.approveForMe === undefined ? {} : { approve_for_me: update.approveForMe }),
          },
        } : current.initialized,
      }));
      return;
    }
    let nextProvider = update.provider?.trim() || undefined;
    let nextModel = update.model?.trim() || undefined;
    const nextEffort =
      update.effort === undefined ? undefined : update.effort.trim();
    // An explicit empty variant resets the stored reasoning effort to the
    // model default (the server clears the selection), so '' must survive;
    // only undefined means "not part of this update".
    const nextVariant =
      update.variant === undefined ? undefined : update.variant.trim();
    const nextPermissionMode =
      update.permissionMode === undefined
        ? undefined
        : update.permissionMode.trim();
    if (!targetThread) {
      // Workspace-scoped update: the server requires an explicit model when
      // no thread is targeted.
      nextProvider = nextProvider ?? state.initialized.provider;
      nextModel = nextModel ?? state.initialized.model;
    }
    const connection = update.connection;
    const nextConnection =
      connection === undefined
        ? undefined
        : {
            ...(connection.base_url === undefined
              ? {}
              : { base_url: connection.base_url.trim() }),
            ...(connection.api_key === undefined
              ? {}
              : { api_key: connection.api_key.trim() }),
            ...(connection.auth_token === undefined
              ? {}
              : { auth_token: connection.auth_token.trim() }),
            ...(connection.type !== undefined && connection.type !== ""
              ? { type: connection.type }
              : {}),
            ...(connection.create_provider ? { create_provider: true } : {}),
            ...(connection.remove_model ? { remove_model: connection.remove_model } : {}),
            ...(connection.reuse_codex_credentials === undefined
              ? {}
              : { reuse_codex_credentials: connection.reuse_codex_credentials }),
          };
    const currentProvider = state.initialized.providers?.find(
      (item) => item.name === nextProvider,
    );
    const connectionChanged =
      Boolean(nextConnection?.remove_model) ||
      Boolean(nextConnection?.create_provider) ||
      Boolean(nextConnection?.api_key) ||
      Boolean(nextConnection?.auth_token) ||
      (nextConnection?.base_url !== undefined &&
        nextConnection.base_url !== (currentProvider?.base_url ?? "")) ||
      (nextConnection?.reuse_codex_credentials !== undefined &&
        nextConnection.reuse_codex_credentials !==
          (currentProvider?.reuse_codex_credentials === true));
    const providerChanged =
      nextProvider !== undefined &&
      nextProvider !==
        (targetThread?.model_provider ?? state.initialized.provider);
    const modelChanged =
      nextModel !== undefined &&
      nextModel !== (targetThread?.model ?? state.initialized.model);
    const effortChanged =
      nextEffort !== undefined &&
      nextEffort !==
        (targetThread?.model_effort ?? state.initialized.effort ?? "");
    const variantChanged =
      nextVariant !== undefined &&
      nextVariant !==
        (targetThread?.model_variant ?? state.initialized.variant ?? "");
    const permissionModeChanged =
      nextPermissionMode !== undefined &&
      nextPermissionMode !==
        (targetThread?.permission_mode ||
          state.initialized.permissions?.mode ||
          "");
    const approveForMeChanged =
      update.approveForMe !== undefined &&
      update.approveForMe !==
        (targetThread?.approve_for_me ?? state.initialized.permissions?.approve_for_me ?? false);
    if (
      !providerChanged &&
      !modelChanged &&
      !effortChanged &&
      !variantChanged &&
      !connectionChanged &&
      !permissionModeChanged &&
      !approveForMeChanged
    ) {
      return;
    }
    try {
      const connectionPayload = {
        ...nextConnection,
        ...(update.approveForMe === undefined ? {} : { approve_for_me: update.approveForMe }),
      };
      const updated = await window.wuu.updateRuntimeSettings(
        nextProvider,
        nextModel,
        nextEffort,
        Object.keys(connectionPayload).length > 0 ? connectionPayload : undefined,
        nextVariant,
        nextPermissionMode,
        targetThread?.id,
      );
      deps.setAppState((current) => {
        // The result is workspace-effective, so initialized takes it
        // wholesale.
        const initialized = current.initialized
          ? {
              ...current.initialized,
              provider: updated.provider,
              model: updated.model,
              effort: updated.effort ?? "",
              variant: updated.variant ?? "",
              permissions:
                updated.permissions ?? current.initialized.permissions,
              extension_trust:
                updated.extension_trust ?? current.initialized.extension_trust,
              providers: updated.providers ?? current.initialized.providers,
              advanced_settings:
                updated.advanced_settings ??
                current.initialized.advanced_settings,
            }
          : current.initialized;
        // Optimistic thread patch limited to the fields this call explicitly
        // changed; the server's thread/updated snapshot stays the authority
        // for resolved values.
        const threadPatch: Partial<Thread> = {
          ...(nextProvider === undefined
            ? {}
            : { model_provider: updated.provider }),
          ...(nextModel === undefined ? {} : { model: updated.model }),
          ...(nextVariant === undefined && nextEffort === undefined
            ? {}
            : {
                model_variant: nextVariant ?? nextEffort ?? "",
                model_effort: nextEffort ?? "",
              }),
          ...(nextPermissionMode === undefined
            ? {}
            : { permission_mode: nextPermissionMode }),
          ...(update.approveForMe === undefined
            ? {}
            : { approve_for_me: update.approveForMe }),
        };
        const next = updateThreadByID(
          { ...current, initialized },
          targetThread?.id,
          (thread) => ({ ...thread, ...threadPatch }),
        );
        return {
          ...next,
          status: scope === "workspace" ? current.status : "ready",
        };
      });
    } catch (error) {
      if (scope === "session") deps.setAppState((current) => ({
        ...current,
        status:
          error instanceof Error
            ? error.message
            : translateCurrent("runtime.settingsUpdateFailed"),
      }));
      throw error;
    }
  }

  async function updateRuntimeSettings(
    provider: string,
    model: string,
    effort?: string,
    connection?: RuntimeConnectionUpdate,
    variant?: string,
    permissionMode?: string,
  ): Promise<void> {
    const nextProvider = provider.trim();
    const nextModel = model.trim();
    if (!nextProvider || !nextModel) {
      return;
    }
    await sendRuntimeSelection({
      provider: nextProvider,
      model: nextModel,
      effort,
      connection,
      variant,
      permissionMode,
    });
  }

  // Settings edit workspace defaults and connection configuration. Never send
  // the active conversation ID or patch its pinned runtime selection.
  async function updateProviderSettings(
    provider: string,
    model: string,
    effort?: string,
    connection?: RuntimeConnectionUpdate,
    variant?: string,
  ): Promise<void> {
    if (!provider.trim() || !model.trim()) return;
    await sendRuntimeSelection({ provider, model, effort, connection, variant }, "workspace");
  }

  async function updateAdvancedSettings(
    settings: RuntimeAdvancedSettingsUpdate,
  ): Promise<void> {
    if (!deps.getAppState().initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    try {
      const updated = await window.wuu.updateAdvancedSettings(settings);
      deps.setAppState((current) => {
        const initialized = current.initialized
          ? {
              ...current.initialized,
              advanced_settings: updated.advanced_settings,
              model_aliases:
                updated.model_aliases ??
                settings.model_aliases ??
                current.initialized.model_aliases,
              model_roles: updated.model_roles ?? current.initialized.model_roles,
              providers: updated.providers ?? current.initialized.providers,
            }
          : current.initialized;
        return {
          ...current,
          initialized,
          status: current.status === "ready" ? current.status : "ready",
        };
      });
    } catch (error) {
      deps.setAppState((current) => ({
        ...current,
        status:
          error instanceof Error
            ? error.message
            : translateCurrent("runtime.advancedUpdateFailed"),
      }));
      throw error;
    }
  }

  async function updateGeneralSettings(
    settings: RuntimeGeneralSettingsUpdate,
  ): Promise<void> {
    if (!deps.getAppState().initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    try {
      const updated = await window.wuu.updateGeneralSettings(settings);
      deps.setAppState((current) => {
        const initialized = current.initialized
          ? {
              ...current.initialized,
              general_settings: updated.general_settings,
            }
          : current.initialized;
        return {
          ...current,
          initialized,
          status: current.status === "ready" ? current.status : "ready",
        };
      });
    } catch (error) {
      deps.setAppState((current) => ({
        ...current,
        status:
          error instanceof Error
            ? error.message
            : translateCurrent("runtime.generalUpdateFailed"),
      }));
      throw error;
    }
  }

  async function removeProvider(
    provider: string,
    options?: { fallbackProvider?: string; fallbackModel?: string },
  ): Promise<void> {
    const state = deps.getAppState();
    if (!state.initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    const target = provider.trim();
    if (!target) {
      return;
    }
    try {
      const updated = await window.wuu.removeProvider(target, options);
      deps.setAppState((current) => {
        const initialized = current.initialized
          ? {
              ...current.initialized,
              provider: updated.provider ?? current.initialized.provider,
              model: updated.model ?? current.initialized.model,
              effort: updated.effort ?? current.initialized.effort,
              variant: updated.variant ?? current.initialized.variant,
              permissions:
                updated.permissions ?? current.initialized.permissions,
              extension_trust:
                updated.extension_trust ?? current.initialized.extension_trust,
              providers: updated.providers ?? current.initialized.providers,
              advanced_settings:
                updated.advanced_settings ??
                current.initialized.advanced_settings,
            }
          : current.initialized;
        return {
          ...current,
          initialized,
          status: current.status === "ready" ? current.status : "ready",
        };
      });
      if (state.initialized) {
        void loadCodexModelsForProvider(updated.provider);
      }
    } catch (error) {
      deps.setAppState((current) => ({
        ...current,
        status:
          error instanceof Error ? error.message : translateCurrent("runtime.providerRemoveFailed"),
      }));
      throw error;
    }
  }

  function toggleCodexRuntimeMenu(
    menu: Exclude<CodexRuntimeMenu, null>,
  ): void {
    const state = deps.getAppState();
    if (!state.initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    deps.setRuntimeMenuOpen(false);
    deps.setAccessMenuOpen(false);
    deps.setBranchMenuOpen(false);
    deps.setCodexRuntimeMenu((current) => (current === menu ? null : menu));
    const runtime = runtimeViewForSession(
      state.initialized,
      activeThreadForState(state),
    );
    if (runtime && isCodexProvider(runtime)) {
      void loadCodexModelsForProvider(runtime.provider);
    }
  }

  async function loadCodexModelsForProvider(provider: string, refresh = false): Promise<void> {
    if (!provider) {
      return;
    }
    const codexModels = deps.getCodexModels();
    if (
      codexModels.provider === provider &&
      (codexModels.loading || (!refresh && codexModels.models.length > 0))
    ) {
      return;
    }
    deps.setCodexModels({ provider, loading: true, error: "", models: [] });
    const workspace = deps.getAppState().initialized?.workspace_root;
    try {
      const result = await window.wuu.loadCodexModels(provider);
      if (deps.getAppState().initialized?.workspace_root !== workspace) return;
      deps.setAppState((current) => ({
        ...current,
        initialized: current.initialized && result.providers
          ? { ...current.initialized, providers: result.providers }
          : current.initialized,
      }));
      deps.setCodexModels({
        provider: result.provider,
        loading: false,
        error: "",
        models: result.models,
      });
    } catch (error) {
      deps.setCodexModels({
        provider,
        loading: false,
        error: error instanceof Error ? error.message : translateCurrent("runtime.modelsLoadFailed"),
        models: [],
      });
    }
  }

  async function selectRuntimeModel(
    provider: string,
    model: string,
    variant?: string,
  ): Promise<boolean> {
    const state = deps.getAppState();
    if (!state.initialized || deps.getViewContextSwitchPending()) {
      return false;
    }
    const targetThread = activeThreadForState(state);
    const currentProvider =
      targetThread?.model_provider ?? state.initialized.provider;
    const currentModel = targetThread?.model ?? state.initialized.model;
    const currentVariant =
      targetThread?.model_variant ??
      targetThread?.model_effort ??
      state.initialized.variant ??
      state.initialized.effort ??
      "";
    const selectionScope = targetThread?.id ?? "workspace";
    deps.variantByModel.set(
      modelSelectionKey(selectionScope, currentProvider, currentModel),
      currentVariant,
    );
    const targetKey = modelSelectionKey(selectionScope, provider, model);
    const rememberedVariant = deps.variantByModel.has(targetKey)
      ? deps.variantByModel.get(targetKey)
      : (variant !== undefined ? variant : lastEffortForRuntimeModel(provider, model));
    const nextVariant = normalizedVariantForProviderModel(
      rememberedVariant ?? "",
      state.initialized.providers?.find((item) => item.name === provider),
      model,
    );
    // The model panel stays open after a selection: the row highlight and the
    // effort pills update optimistically inside the panel, so the user can
    // chain "switch model → tune effort" in one visit instead of reopening the
    // picker for every change. A rejection (for example a background agent
    // still running on the thread) surfaces as a toast — the status line
    // alone was too easy to miss against the optimistic highlight.
    try {
      await sendRuntimeSelection({ provider, model, variant: nextVariant });
      rememberDraftRuntime(provider, model, nextVariant);
      return true;
    } catch (error) {
      showErrorToast(error, translateCurrent("runtime.settingsUpdateFailed"));
      return false;
    }
  }

  async function selectRuntimeEffort(nextVariant: string): Promise<boolean> {
    const state = deps.getAppState();
    if (!state.initialized || deps.getViewContextSwitchPending()) {
      return false;
    }
    try {
      await sendRuntimeSelection({ variant: nextVariant });
      const targetThread = activeThreadForState(state);
      rememberDraftRuntime(
        targetThread?.model_provider ?? state.initialized.provider,
        targetThread?.model ?? state.initialized.model,
        nextVariant,
      );
      return true;
    } catch (error) {
      showErrorToast(error, translateCurrent("runtime.settingsUpdateFailed"));
      return false;
    }
    // Keep the panel open — see selectRuntimeModel.
  }

  async function selectPermissionMode(mode: PermissionMode, approveForMe?: boolean): Promise<void> {
    if (!deps.getAppState().initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    try {
      await sendRuntimeSelection({
        permissionMode: mode,
        ...(approveForMe === undefined ? {} : { approveForMe }),
      });
      // Remember the choice so the next new conversation (another tab or a
      // relaunch) starts with it instead of the workspace default.
      writeDraftPermissionMemory(mode);
      if (approveForMe !== undefined) {
        writeDraftApproveForMeMemory(approveForMe);
      }
    } catch {
      // Failure already surfaced through the status line.
    }
    deps.setAccessMenuOpen(false);
  }

  async function setApproveForMe(enabled: boolean): Promise<void> {
    if (!deps.getAppState().initialized || deps.getViewContextSwitchPending()) {
      return;
    }
    try {
      await sendRuntimeSelection({ approveForMe: enabled });
      writeDraftApproveForMeMemory(enabled);
    } catch {
      // Failure already surfaced through the status line.
    }
  }

  async function interrupt(): Promise<void> {
    const thread = activeThreadForState(deps.getAppState());
    if (!thread) {
      return;
    }
    deps.markOptimisticTurnInterrupted?.(thread.id);
    await window.wuu.interruptTurn(thread.id);
  }

  async function interruptPane(pane: ConversationPaneID): Promise<void> {
    const thread = threadForPane(deps.getAppState(), pane);
    if (!thread) {
      return;
    }
    deps.markOptimisticTurnInterrupted?.(thread.id);
    await window.wuu.interruptTurn(thread.id);
  }

  function rememberDraftRuntime(provider: string, model: string, effort: string): void {
    writeDraftRuntimeMemory({ provider, model, effort });
    const state = deps.getAppState();
    const scope = activeThreadForState(state)?.id ?? "workspace";
    deps.variantByModel.set(modelSelectionKey(scope, provider, model), effort);
  }

  return {
    updateRuntimeSettings,
    updateProviderSettings,
    updateAdvancedSettings,
    updateGeneralSettings,
    removeProvider,
    toggleCodexRuntimeMenu,
    loadCodexModelsForProvider,
    selectRuntimeModel,
    selectRuntimeEffort,
    selectPermissionMode,
    setApproveForMe,
    interrupt,
    interruptPane,
  };
}
