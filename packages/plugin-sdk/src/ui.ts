import type { ApplicationNavigationActionMapV1, ApplicationNavigationViewV1 } from "./ui-navigation.js";
import type { ApplicationSettingsControlsV1 } from "./ui-settings-controls.js";
import type { ApplicationPreviewActionMapV1, ApplicationPreviewWorkspaceV1 } from "./ui-preview.js";
import type { ApplicationSettingsActionMapV1, ApplicationSettingsViewV1 } from "./application-settings.js";
import type { ApplicationComposerActionMapV1, ApplicationDraftsSnapshotV1, ApplicationPendingComposerThreadV1 } from "./ui-composer.js";
import type { ComponentType, ReactNode } from "react";
import type {
  ComposerSnapshotV1, ComposerSubmissionModeV1, ConversationItemSnapshotV1,
  ConversationProcessSnapshotV1, Disposable, FilePreviewSnapshotV1,
  NavigationSnapshotV1, PluginWorkspaceSnapshot, SettingsSnapshotV1,
  SettingsValueMapV1, StatusSnapshotV1,
} from "./index.js";

/** Declare this service in plugin.json desktop.services to offer an application shell. */
export const APPLICATION_SHELL_SERVICE = {
  name: "ui.shell", version: "1.0.0",
  methods: [{ name: "describe", input_schema: "ui.shell.describe.input.v1", output_schema: "ui.shell.describe.output.v1" }],
} satisfies import("./index.js").ServiceDescriptor;

/** Immutable public application state. Snapshots retain identity until a change. */
export interface ApplicationSnapshotV1 {
  readonly contractVersion: 1;
  readonly revision: number;
  readonly phase: "starting" | "ready" | "error";
  readonly connection: { readonly status: "connecting" | "connected" | "disconnected" | "error"; readonly message?: string };
  readonly workspaces: PluginWorkspaceSnapshot;
  readonly navigation: NavigationSnapshotV1 & {
    readonly section: "conversations" | "catalog";
    readonly catalogSection: "plugins" | "skills";
    readonly catalogPluginId: string;
  };
  readonly navigationWorkspace: ApplicationNavigationViewV1;
  readonly tabs: {
    readonly activeTabId: string;
    readonly activePane: "primary" | "secondary";
    readonly primaryThreadId?: string;
    readonly secondaryThreadId?: string;
    readonly canSubmit: boolean;
    readonly items: readonly ApplicationTabV1[];
    readonly pending?: { readonly kind: "tab" | "workspace" | "thread" | "runtime"; readonly targetId: string; readonly requestId?: number; readonly contextSwitching?: boolean; readonly contentReady?: boolean };
  };
  readonly conversation: {
    readonly threadId?: string;
    readonly title?: string;
    readonly turns: readonly ApplicationTurnV1[];
    readonly loading: boolean;
    readonly error?: string;
  };
  readonly composer: ComposerSnapshotV1;
  readonly drafts: ApplicationDraftsSnapshotV1;
  readonly composerPending: readonly ApplicationPendingComposerThreadV1[];
  readonly settings: SettingsSnapshotV1 & ApplicationSettingsViewV1 & { readonly open: boolean };
  readonly preview: FilePreviewSnapshotV1 | null;
  readonly previewWorkspace: ApplicationPreviewWorkspaceV1;
  readonly appearance: ApplicationAppearanceV1;
  readonly locale: string;
  readonly status: StatusSnapshotV1;
}

export interface ApplicationTabV1 {
  readonly id: string;
  readonly kind: "draft" | "thread" | "skills";
  readonly title: string;
  readonly workspaceId?: string;
  readonly workspaceRoot: string;
  readonly threadId?: string;
  readonly project: boolean;
  readonly draftText: string;
  readonly hasAttachments: boolean;
}

export interface ApplicationTurnV1 {
  readonly id: string;
  readonly status: "pending" | "running" | "completed" | "failed" | "cancelled";
  readonly items: readonly ConversationItemSnapshotV1[];
  readonly process?: ConversationProcessSnapshotV1;
}

export interface ApplicationAppearanceV1 {
  readonly theme: "system" | "light" | "dark";
  readonly resolvedTheme: "light" | "dark";
  readonly uiFontSize: number;
  readonly codeFontSize: number;
}

/** Commands are scoped to the generation that captured this application handle. */
export interface ApplicationActionMapV1 extends ApplicationNavigationActionMapV1, ApplicationComposerActionMapV1, ApplicationSettingsActionMapV1, ApplicationPreviewActionMapV1 {
  "workspace.retarget-draft": { input: { readonly workspaceId?: string }; result: void };
  "workspace.start-draft": { input: { readonly workspaceId?: string }; result: { readonly activated: boolean; readonly tabId?: string } };
  "workspace.create": { input: { readonly kind: "blank" | "folder" }; result: void };
  /** Remove a workspace after the caller has confirmed the user's intent. */
  "workspace.remove": { input: { readonly workspaceId: string }; result: void };
  "workspace.relocate": { input: { readonly workspaceId: string }; result: void };
  "conversation.activate-pane": { input: { readonly pane: "primary" | "secondary" }; result: void };
  "conversation.close-pane": { input: { readonly pane: "primary" | "secondary" }; result: void };
  "thread.activate-child": { input: { readonly threadId: string }; result: void };
  "thread.activate": { input: { readonly threadId: string; readonly workspaceId?: string }; result: void };
  "draft.start": { input: Record<string, never>; result: { readonly tabId: string } };
  "tab.pop-out": { input: { readonly tabId: string }; result: void };
  "navigation.reveal-complete": { input: { readonly requestId: number }; result: void };
  "tab.select": { input: { readonly tabId: string }; result: void };
  "tab.close": { input: { readonly tabId: string }; result: void };
  "tab.reorder": { input: { readonly tabId: string; readonly beforeTabId: string }; result: void };
  "tab.new": { input: { readonly workspaceId?: string; readonly project?: boolean }; result: { readonly tabId: string } };
  "draft.rename": { input: { readonly tabId: string; readonly title: string }; result: void };
  "draft.set-project-mode": { input: { readonly project: boolean }; result: void };
  "navigation.show-section": { input: { readonly section: "conversations" | "catalog" }; result: void };
  "catalog.select-section": { input: { readonly section: "plugins" | "skills" }; result: void };
  "catalog.select-plugin": { input: { readonly pluginId: string }; result: void };
  "workspace.select": { input: { readonly workspaceId: string }; result: void };
  "thread.select": { input: { readonly threadId: string }; result: void };
  "thread.create": { input: { readonly workspaceId?: string }; result: { readonly threadId: string } };
  "thread.rename": { input: { readonly threadId: string; readonly title: string }; result: void };
  "thread.archive": { input: { readonly threadId: string; readonly archived: boolean; readonly force?: boolean }; result: void };
  "composer.set-draft": { input: { readonly text: string; readonly ownerTabId?: string }; result: void };
  "composer.submit": { input: { readonly mode?: ComposerSubmissionModeV1; readonly ownerTabId?: string }; result: void };
  "composer.stop": { input: Record<string, never>; result: void };
  "composer.configure": { input: { readonly modelId?: string; readonly runtimeId?: string; readonly permissionId?: string }; result: void };
  "settings.open": { input: { readonly pageId?: string }; result: void };
  "settings.close": { input: Record<string, never>; result: void };
  "settings.update-value": { input: { [K in keyof SettingsValueMapV1]: { readonly key: K; readonly value: SettingsValueMapV1[K] } }[keyof SettingsValueMapV1]; result: void };
  "appearance.set": { input: Partial<Pick<ApplicationAppearanceV1, "theme" | "uiFontSize" | "codeFontSize">>; result: void };
  "preview.open": { input: { readonly resourceId: string }; result: void };
  "preview.close": { input: Record<string, never>; result: void };
  "runtime.refresh": { input: Record<string, never>; result: void };
}

export interface ApplicationServiceV1 {
  readonly contractVersion: 1;
  readonly settingsControls: ApplicationSettingsControlsV1;
  getSnapshot(): ApplicationSnapshotV1;
  /** Unsubscribe is idempotent; generation disposal also removes every listener. */
  subscribe(listener: () => void): () => void;
  /** A rejection reports failure; the caller must not automatically replay effects. */
  invoke<Action extends keyof ApplicationActionMapV1>(
    action: Action,
    input: ApplicationActionMapV1[Action]["input"],
    options?: { readonly signal?: AbortSignal },
  ): Promise<ApplicationActionMapV1[Action]["result"]>;
}

/** Slots are qualified as `${ownerPluginId}/${localId}`. */
export interface UISlotDefinition {
  readonly id: string;
  readonly cardinality: "one" | "many";
  /** Select a single surface through the committed service graph, without fallback. */
  readonly service?: string;
}
export interface UISlotProps {
  readonly name: string;
  readonly context?: Readonly<Record<string, unknown>>;
  readonly fallback?: ReactNode;
}
export interface UICompositionV1 {
  /** Nested contributions use the same component; recursive slot cycles fail visibly. */
  readonly Slot: ComponentType<UISlotProps>;
}
export interface UISlotRenderProps {
  readonly application: ApplicationServiceV1;
  readonly slots: UICompositionV1;
  readonly context: Readonly<Record<string, unknown>>;
}
export interface UIContributionDefinition {
  readonly id: string;
  readonly target: string;
  readonly order?: number;
  readonly render: ComponentType<UISlotRenderProps>;
}
export interface ApplicationShellRenderProps {
  readonly application: ApplicationServiceV1;
  readonly slots: UICompositionV1;
}
export interface ApplicationShellDefinition {
  /** Local provider ID used with the owning plugin ID and generation in ui.shell binding. */
  readonly id: string;
  readonly render: ComponentType<ApplicationShellRenderProps>;
}
export interface ApplicationGenerationAPI {
  readonly application: ApplicationServiceV1;
  registerApplicationShell(definition: ApplicationShellDefinition): Disposable;
  registerUISlot(definition: UISlotDefinition): Disposable;
  registerUIContribution(definition: UIContributionDefinition): Disposable;
}

/** Apply the same lifecycle boundary in the desktop and standalone preview hosts. */
export function scopeApplicationSettingsControls(source: () => ApplicationSettingsControlsV1,
  signal: AbortSignal, assertActive: () => void): ApplicationSettingsControlsV1 {
  const read = <T>(operation: () => T): T => { assertActive(); signal.throwIfAborted(); return operation(); };
  const invoke = async <T>(operation: () => Promise<T>): Promise<T> => {
    assertActive(); signal.throwIfAborted();
    let abort!: () => void;
    const aborted = new Promise<never>((_resolve, reject) => {
      abort = () => reject(signal.reason ?? new Error("Settings generation was disposed"));
      signal.addEventListener("abort", abort, { once: true });
    });
    try {
      const result = await Promise.race([operation(), aborted]);
      assertActive(); signal.throwIfAborted(); return result;
    } finally { signal.removeEventListener("abort", abort); }
  };
  const subscribe = (kind: "subscribe" | "subscribeRemote", listener: () => void): (() => void) => {
    assertActive(); signal.throwIfAborted();
    const stop = source()[kind](() => { if (!signal.aborted) listener(); });
    let stopped = false;
    const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
    signal.addEventListener("abort", dispose, { once: true });
    return dispose;
  };
  return Object.freeze({
    get capabilities() { return read(() => source().capabilities); },
    subscribe: (listener: () => void) => subscribe("subscribe", listener),
    subscribeRemote: (listener: () => void) => subscribe("subscribeRemote", listener),
    getPages: () => read(() => source().getPages()),
    getConflicts: () => read(() => source().getConflicts()),
    getDiagnostics: (...args: Parameters<ApplicationSettingsControlsV1["getDiagnostics"]>) => read(() => source().getDiagnostics(...args)),
    renderPage: (input: Parameters<ApplicationSettingsControlsV1["renderPage"]>[0]) => read(() => source().renderPage({ ...input,
      onFailure: () => { if (!signal.aborted) input.onFailure(); },
      settings: { contractVersion: 1, getValue: key => read(() => input.settings.getValue(key)),
        updateValue: (key, value) => invoke(() => input.settings.updateValue(key, value)),
        listAvailableModels: input.settings.listAvailableModels && (() => read(() => input.settings.listAvailableModels!())) },
    })),
    renderPresentation: (input: Parameters<ApplicationSettingsControlsV1["renderPresentation"]>[0]) => read(() => source().renderPresentation({ ...input,
      onOpenPage: id => { if (!signal.aborted) input.onOpenPage(id); },
      onAdvancedSave: value => invoke(() => input.onAdvancedSave(value)),
      onGeneralSave: value => invoke(() => input.onGeneralSave(value)), onRefresh: () => invoke(() => input.onRefresh()),
    })),
    chooseConflict: (...args: Parameters<ApplicationSettingsControlsV1["chooseConflict"]>) => invoke(() => source().chooseConflict(...args)),
    getBuildInfo: (...args: Parameters<ApplicationSettingsControlsV1["getBuildInfo"]>) => invoke(() => source().getBuildInfo(...args)),
    useCodexCredentials: (...args: Parameters<ApplicationSettingsControlsV1["useCodexCredentials"]>) => invoke(() => source().useCodexCredentials(...args)),
    loadCodexModels: (...args: Parameters<ApplicationSettingsControlsV1["loadCodexModels"]>) => invoke(() => source().loadCodexModels(...args)),
    refreshModelCatalog: (...args: Parameters<ApplicationSettingsControlsV1["refreshModelCatalog"]>) => invoke(() => source().refreshModelCatalog(...args)),
    listCatalogProviders: (...args: Parameters<ApplicationSettingsControlsV1["listCatalogProviders"]>) => invoke(() => source().listCatalogProviders(...args)),
    listEngines: (...args: Parameters<ApplicationSettingsControlsV1["listEngines"]>) => invoke(() => source().listEngines(...args)),
    getPluginSetting: (...args: Parameters<ApplicationSettingsControlsV1["getPluginSetting"]>) => invoke(() => source().getPluginSetting(...args)),
    setPluginSetting: (...args: Parameters<ApplicationSettingsControlsV1["setPluginSetting"]>) => invoke(() => source().setPluginSetting(...args)),
    getPluginDiagnostics: (...args: Parameters<ApplicationSettingsControlsV1["getPluginDiagnostics"]>) => invoke(() => source().getPluginDiagnostics(...args)),
    listMCPServers: (...args: Parameters<ApplicationSettingsControlsV1["listMCPServers"]>) => invoke(() => source().listMCPServers(...args)),
    connectMCPServer: (...args: Parameters<ApplicationSettingsControlsV1["connectMCPServer"]>) => invoke(() => source().connectMCPServer(...args)),
    disconnectMCPServer: (...args: Parameters<ApplicationSettingsControlsV1["disconnectMCPServer"]>) => invoke(() => source().disconnectMCPServer(...args)),
    refreshMCPServer: (...args: Parameters<ApplicationSettingsControlsV1["refreshMCPServer"]>) => invoke(() => source().refreshMCPServer(...args)),
    startMCPAuth: (...args: Parameters<ApplicationSettingsControlsV1["startMCPAuth"]>) => invoke(() => source().startMCPAuth(...args)),
    finishMCPAuth: (...args: Parameters<ApplicationSettingsControlsV1["finishMCPAuth"]>) => invoke(() => source().finishMCPAuth(...args)),
    removeMCPAuth: (...args: Parameters<ApplicationSettingsControlsV1["removeMCPAuth"]>) => invoke(() => source().removeMCPAuth(...args)),
    listChatGPTModels: (...args: Parameters<ApplicationSettingsControlsV1["listChatGPTModels"]>) => invoke(() => source().listChatGPTModels(...args)),
    startXAILogin: (...args: Parameters<ApplicationSettingsControlsV1["startXAILogin"]>) => invoke(() => source().startXAILogin(...args)),
    pollXAILogin: (...args: Parameters<ApplicationSettingsControlsV1["pollXAILogin"]>) => invoke(() => source().pollXAILogin(...args)),
    cancelXAILogin: (...args: Parameters<ApplicationSettingsControlsV1["cancelXAILogin"]>) => invoke(() => source().cancelXAILogin(...args)),
    listBrowserProfiles: (...args: Parameters<ApplicationSettingsControlsV1["listBrowserProfiles"]>) => invoke(() => source().listBrowserProfiles(...args)),
    importBrowserCookies: (...args: Parameters<ApplicationSettingsControlsV1["importBrowserCookies"]>) => invoke(() => source().importBrowserCookies(...args)),
    listBrowserExtensions: (...args: Parameters<ApplicationSettingsControlsV1["listBrowserExtensions"]>) => invoke(() => source().listBrowserExtensions(...args)),
    addBrowserExtension: (...args: Parameters<ApplicationSettingsControlsV1["addBrowserExtension"]>) => invoke(() => source().addBrowserExtension(...args)),
    setBrowserExtensionEnabled: (...args: Parameters<ApplicationSettingsControlsV1["setBrowserExtensionEnabled"]>) => invoke(() => source().setBrowserExtensionEnabled(...args)),
    reloadBrowserExtension: (...args: Parameters<ApplicationSettingsControlsV1["reloadBrowserExtension"]>) => invoke(() => source().reloadBrowserExtension(...args)),
    openBrowserExtensionOptions: (...args: Parameters<ApplicationSettingsControlsV1["openBrowserExtensionOptions"]>) => invoke(() => source().openBrowserExtensionOptions(...args)),
    removeBrowserExtension: (...args: Parameters<ApplicationSettingsControlsV1["removeBrowserExtension"]>) => invoke(() => source().removeBrowserExtension(...args)),
    getRemoteControlSnapshot: (...args: Parameters<ApplicationSettingsControlsV1["getRemoteControlSnapshot"]>) => invoke(() => source().getRemoteControlSnapshot(...args)),
    setRemoteHostEnabled: (...args: Parameters<ApplicationSettingsControlsV1["setRemoteHostEnabled"]>) => invoke(() => source().setRemoteHostEnabled(...args)),
    startRemotePairing: (...args: Parameters<ApplicationSettingsControlsV1["startRemotePairing"]>) => invoke(() => source().startRemotePairing(...args)),
    removeRemoteDevice: (...args: Parameters<ApplicationSettingsControlsV1["removeRemoteDevice"]>) => invoke(() => source().removeRemoteDevice(...args)),
    openExternal: (...args: Parameters<ApplicationSettingsControlsV1["openExternal"]>) => invoke(() => source().openExternal(...args)),
  });
}
