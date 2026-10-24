import type { ApplicationComposerPresentationV1 } from "./ui-composer-presentation.js";
import type { ApplicationEnvironmentV1, EnvironmentCallOptionsV1 } from "./ui-environment.js";
import type { ApplicationArtifactsV1, ArtifactCallOptionsV1 } from "./ui-artifacts.js";
import type { ApplicationDesktopControlsV1 } from "./ui-desktop.js";
import type { ApplicationWebWorkspaceV1 } from "./ui-web-workspace.js";
import type { ApplicationOnboardingV1, OnboardingCallOptionsV1 } from "./ui-onboarding.js";
import type { ApplicationBrowserV1, BrowserActivityV1, BrowserDockTargetV1, BrowserOperationOptionsV1, BrowserSurfaceHandleV1 } from "./ui-browser.js";
import type { ApplicationSideConversationActionMapV1, ApplicationSideConversationViewV1 } from "./ui-side-conversation.js";
import type { ApplicationFileSelectionServiceV1, FileSelectionCallOptionsV1 } from "./ui-file-selection.js";
import type { ApplicationProjectsV1, ApplicationProjectCallOptionsV1 } from "./ui-projects.js";
import type { ApplicationTerminalsV1 } from "./ui-terminals.js";
export { scopeApplicationTerminals } from "./ui-terminals.js";
import type { ApplicationSearchActionMapV1, ApplicationSearchViewV1 } from "./ui-search.js";
import type { ApplicationConversationActionMapV1, ApplicationConversationActionsViewV1 } from "./ui-conversation-actions.js";
import type { ApplicationSubmissionActionMapV1, ApplicationSubmissionSnapshotV1 } from "./ui-submission.js";
import type { ApplicationComposerRuntimeActionMapV1, ApplicationComposerRuntimeViewV1 } from "./ui-composer-runtime.js";
import type { ApplicationCatalogV1, CatalogCallOptionsV1, CatalogThreadSkillsInputV1, CatalogPackageUpdateV1 } from "./ui-catalog.js";
import type { ApplicationPreviewContentV1, PreviewFileTargetV1, PreviewContentOptionsV1 } from "./ui-preview-content.js";
import type { ApplicationConversationThreadItemV1, ApplicationConversationDetailsActionMapV1, ApplicationConversationDetailV1, ApplicationConversationWorkspaceV1, ApplicationConversationTextV1 } from "./ui-conversation.js";
import type { ApplicationNavigationActionMapV1, ApplicationNavigationViewV1 } from "./ui-navigation.js";
import type { ApplicationSettingsControlsV1 } from "./ui-settings-controls.js";
import type { ApplicationPreviewActionMapV1, ApplicationPreviewWorkspaceV1 } from "./ui-preview.js";
import type { SettingsReadonly, ApplicationSettingsUsageResponseV1, ApplicationSettingsActionMapV1, ApplicationSettingsViewV1 } from "./application-settings.js";
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
export interface ApplicationActivityV1 extends BrowserActivityV1 {
  readonly plugin_id?: string; readonly process_id?: number; readonly window_id?: number;
  readonly controller: "agent" | "user" | "none"; readonly preview?: string; readonly error?: string;
  readonly interaction?: { readonly kind: "click" | "drag" | "scroll" | "type"; readonly x: number; readonly y: number;
    readonly to_x?: number; readonly to_y?: number; readonly direction?: string; readonly revision: number };
  readonly created_at: string; readonly updated_at: string;
}

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
    readonly navigationRevision: number;
    readonly activePane: "primary" | "secondary";
    readonly primaryThreadId?: string;
    readonly secondaryThreadId?: string;
    readonly canSubmit: boolean;
    readonly items: readonly ApplicationTabV1[];
    readonly pending?: { readonly kind: "tab" | "workspace" | "thread" | "runtime"; readonly targetId: string; readonly requestId?: number; readonly contextSwitching?: boolean; readonly contentReady?: boolean };
  };
  readonly conversationDetails: readonly ApplicationConversationDetailV1[];
  readonly conversationWorkspace: ApplicationConversationWorkspaceV1;
  readonly conversation: {
    readonly threadId?: string;
    readonly title?: string;
    readonly turns: readonly ApplicationTurnV1[];
    readonly loading: boolean;
    readonly error?: string;
  };
  readonly composer: ComposerSnapshotV1;
  readonly activities: readonly ApplicationActivityV1[];
  readonly browserDockTarget?: BrowserDockTargetV1;
  readonly sideConversation: ApplicationSideConversationViewV1;
  readonly search: ApplicationSearchViewV1;
  readonly conversationActions: ApplicationConversationActionsViewV1;
  readonly submission: ApplicationSubmissionSnapshotV1;
  readonly composerRuntime: ApplicationComposerRuntimeViewV1;
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
export interface ApplicationActionMapV1 extends ApplicationSideConversationActionMapV1, ApplicationSearchActionMapV1, ApplicationConversationActionMapV1, ApplicationSubmissionActionMapV1, ApplicationComposerRuntimeActionMapV1, ApplicationConversationDetailsActionMapV1, ApplicationNavigationActionMapV1, ApplicationComposerActionMapV1, ApplicationSettingsActionMapV1, ApplicationPreviewActionMapV1 {
  /** Open an existing worktree in a draft, importing its workspace when needed. */
  "workspace.open-worktree": { input: { readonly path: string }; result: void };
  "workspace.retarget-draft": { input: { readonly workspaceId?: string }; result: void };
  "workspace.start-draft": { input: { readonly workspaceId?: string }; result: { readonly activated: boolean; readonly tabId?: string } };
  "workspace.create": { input: { readonly kind: "blank" | "folder" }; result: void };
  /** Remove a workspace after the caller has confirmed the user's intent. */
  "workspace.remove": { input: { readonly workspaceId: string }; result: void };
  "workspace.relocate": { input: { readonly workspaceId: string }; result: void };
  "navigation.cancel": { input: { readonly requestId?: number }; result: void };
  "conversation.load-earlier": { input: { readonly threadId: string; readonly cursor: string }; result: void };
  "conversation.continue": { input: { readonly threadId: string; readonly turnId?: string; readonly text: string }; result: { readonly accepted: boolean } };
  "conversation.read-usage": { input: { readonly timezone: string }; result: SettingsReadonly<Pick<ApplicationSettingsUsageResponseV1, "total_sessions" | "metrics" | "days">> };
  "conversation.load-item": { input: { readonly threadId: string; readonly turnId: string; readonly itemId: string }; result: ApplicationConversationThreadItemV1 };
  "conversation.open-split": { input: { readonly threadId: string }; result: void };
  "conversation.activate-pane": { input: { readonly pane: "primary" | "secondary" }; result: void };
  "conversation.close-pane": { input: { readonly pane: "primary" | "secondary" }; result: void };
  "thread.activate-child": { input: { readonly threadId: string }; result: void };
  "thread.activate": { input: { readonly threadId: string; readonly workspaceId?: string }; result: void };
  "draft.start-with-content": { input: { readonly text: string; readonly sourceTabId: string; readonly navigationRevision: number }; result: { readonly activated: boolean; readonly tabId?: string } };
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
  "workspace.select": { input: { readonly workspaceId: string; readonly resumeLatestThread?: boolean }; result: void };
  "thread.select": { input: { readonly threadId: string }; result: void };
  "thread.create": { input: { readonly workspaceId?: string }; result: { readonly threadId: string } };
  "thread.rename": { input: { readonly threadId: string; readonly title: string }; result: void };
  "thread.archive": { input: { readonly threadId: string; readonly archived: boolean; readonly force?: boolean }; result: void };
  "composer.set-draft": { input: { readonly text: string; readonly ownerTabId?: string }; result: void };
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
  readonly conversationText: ApplicationConversationTextV1;
  readonly previewContent: ApplicationPreviewContentV1;
  readonly environment: ApplicationEnvironmentV1;
  readonly artifacts: ApplicationArtifactsV1;
  readonly composerPresentation: ApplicationComposerPresentationV1;
  readonly desktop: ApplicationDesktopControlsV1;
  readonly webWorkspace: ApplicationWebWorkspaceV1;
  readonly onboarding: ApplicationOnboardingV1;
  readonly browser: ApplicationBrowserV1;
  readonly fileSelection: ApplicationFileSelectionServiceV1;
  readonly projects: ApplicationProjectsV1;
  readonly terminals: ApplicationTerminalsV1;
  readonly catalog: ApplicationCatalogV1;
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
  /** Canonical owner prefix for declared UI slots, independent of installation identity. */
  readonly manifestId: string;
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
  const subscribe = <Args extends unknown[]>(register: (listener: (...args: Args) => void) => () => void,
    listener: (...args: Args) => void): (() => void) => {
    assertActive(); signal.throwIfAborted();
    const stop = register((...args) => { if (!signal.aborted) listener(...args); });
    let stopped = false;
    const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
    signal.addEventListener("abort", dispose, { once: true });
    if (signal.aborted) dispose();
    return dispose;
  };
  const connectionDisplay = Object.freeze<ApplicationSettingsControlsV1["connectionDisplay"]>({
    subscribe: listener => subscribe(callback => source().connectionDisplay.subscribe(callback), listener),
    getSnapshot: () => read(() => source().connectionDisplay.getSnapshot()),
    get: key => read(() => source().connectionDisplay.get(key)),
    label: (key, fallback) => read(() => source().connectionDisplay.label(key, fallback)),
    orderModels: (key, models) => read(() => source().connectionDisplay.orderModels(key, models)),
    visibleModels: (key, models, retainedIDs) => read(() => source().connectionDisplay.visibleModels(key, models, retainedIDs)),
    setAlias: (key, alias) => read(() => source().connectionDisplay.setAlias(key, alias)),
    setModelOrder: (key, ids) => read(() => source().connectionDisplay.setModelOrder(key, ids)),
    moveModel: (key, models, id, direction) => read(() => source().connectionDisplay.moveModel(key, models, id, direction)),
    setModelVisible: (key, id, visible) => read(() => source().connectionDisplay.setModelVisible(key, id, visible)),
  });
  return Object.freeze({
    get connectionDisplay() { return read(() => connectionDisplay); },
    get initialMessageFlowFontSize() { return read(() => source().initialMessageFlowFontSize); },
    get initialThemePreference() { return read(() => source().initialThemePreference); },
    get appearanceDefaults() { return read(() => source().appearanceDefaults); },
    readBackground: (...args: Parameters<ApplicationSettingsControlsV1["readBackground"]>) => invoke(() => source().readBackground(...args)),
    loadSkinImage: (...args: Parameters<ApplicationSettingsControlsV1["loadSkinImage"]>) => invoke(() => source().loadSkinImage(...args)),
    listLocalFonts: (...args: Parameters<ApplicationSettingsControlsV1["listLocalFonts"]>) => invoke(() => source().listLocalFonts(...args)),
    getSystemPermissions: (...args: Parameters<ApplicationSettingsControlsV1["getSystemPermissions"]>) => invoke(() => source().getSystemPermissions(...args)),
    requestSystemPermission: (...args: Parameters<ApplicationSettingsControlsV1["requestSystemPermission"]>) => invoke(() => source().requestSystemPermission(...args)),
    closeSystemPermissionGuide: (...args: Parameters<ApplicationSettingsControlsV1["closeSystemPermissionGuide"]>) => invoke(() => source().closeSystemPermissionGuide(...args)),
    getMessageFlowFontSize: (...args: Parameters<ApplicationSettingsControlsV1["getMessageFlowFontSize"]>) => invoke(() => source().getMessageFlowFontSize(...args)),
    setMessageFlowFontSize: (...args: Parameters<ApplicationSettingsControlsV1["setMessageFlowFontSize"]>) => invoke(() => source().setMessageFlowFontSize(...args)),
    getThemePreference: (...args: Parameters<ApplicationSettingsControlsV1["getThemePreference"]>) => invoke(() => source().getThemePreference(...args)),
    setThemePreference: (...args: Parameters<ApplicationSettingsControlsV1["setThemePreference"]>) => invoke(() => source().setThemePreference(...args)),
    getActivityPreviewAutoShow: (...args: Parameters<ApplicationSettingsControlsV1["getActivityPreviewAutoShow"]>) => invoke(() => source().getActivityPreviewAutoShow(...args)),
    setActivityPreviewAutoShow: (...args: Parameters<ApplicationSettingsControlsV1["setActivityPreviewAutoShow"]>) => invoke(() => source().setActivityPreviewAutoShow(...args)),
    getAppContextState: (...args: Parameters<ApplicationSettingsControlsV1["getAppContextState"]>) => invoke(() => source().getAppContextState(...args)),
    updateAppContextSettings: (...args: Parameters<ApplicationSettingsControlsV1["updateAppContextSettings"]>) => invoke(() => source().updateAppContextSettings(...args)),
    setAppContextShortcutRecording: (...args: Parameters<ApplicationSettingsControlsV1["setAppContextShortcutRecording"]>) => invoke(() => source().setAppContextShortcutRecording(...args)),
    listChatGPTAccounts: (...args: Parameters<ApplicationSettingsControlsV1["listChatGPTAccounts"]>) => invoke(() => source().listChatGPTAccounts(...args)),
    signOutChatGPTAccount: (...args: Parameters<ApplicationSettingsControlsV1["signOutChatGPTAccount"]>) => invoke(() => source().signOutChatGPTAccount(...args)),
    startChatGPTLogin: (...args: Parameters<ApplicationSettingsControlsV1["startChatGPTLogin"]>) => invoke(() => source().startChatGPTLogin(...args)),
    pollChatGPTLogin: (...args: Parameters<ApplicationSettingsControlsV1["pollChatGPTLogin"]>) => invoke(() => source().pollChatGPTLogin(...args)),
    cancelChatGPTLogin: (...args: Parameters<ApplicationSettingsControlsV1["cancelChatGPTLogin"]>) => invoke(() => source().cancelChatGPTLogin(...args)),
    listEngineAuthMethods: (...args: Parameters<ApplicationSettingsControlsV1["listEngineAuthMethods"]>) => invoke(() => source().listEngineAuthMethods(...args)),
    authenticateEngine: (...args: Parameters<ApplicationSettingsControlsV1["authenticateEngine"]>) => invoke(() => source().authenticateEngine(...args)),
    cancelEngineAuth: (...args: Parameters<ApplicationSettingsControlsV1["cancelEngineAuth"]>) => invoke(() => source().cancelEngineAuth(...args)),
    getShellSkins: (...args: Parameters<ApplicationSettingsControlsV1["getShellSkins"]>) => read(() => source().getShellSkins(...args)),
    selectShellSkin: (...args: Parameters<ApplicationSettingsControlsV1["selectShellSkin"]>) => read(() => source().selectShellSkin(...args)),
    readDraftEngineMemory: (...args: Parameters<ApplicationSettingsControlsV1["readDraftEngineMemory"]>) => read(() => source().readDraftEngineMemory(...args)),
    writeDraftEngineMemory: (...args: Parameters<ApplicationSettingsControlsV1["writeDraftEngineMemory"]>) => read(() => source().writeDraftEngineMemory(...args)),
    clearDraftEngineMemory: (...args: Parameters<ApplicationSettingsControlsV1["clearDraftEngineMemory"]>) => read(() => source().clearDraftEngineMemory(...args)),
    currentAppliedTheme: (...args: Parameters<ApplicationSettingsControlsV1["currentAppliedTheme"]>) => read(() => source().currentAppliedTheme(...args)),
    availableExtensionThemes: (...args: Parameters<ApplicationSettingsControlsV1["availableExtensionThemes"]>) => read(() => source().availableExtensionThemes(...args)),
    selectedExtensionThemeKey: (...args: Parameters<ApplicationSettingsControlsV1["selectedExtensionThemeKey"]>) => read(() => source().selectedExtensionThemeKey(...args)),
    selectExtensionTheme: (...args: Parameters<ApplicationSettingsControlsV1["selectExtensionTheme"]>) => read(() => source().selectExtensionTheme(...args)),
    readAppearance: (...args: Parameters<ApplicationSettingsControlsV1["readAppearance"]>) => read(() => source().readAppearance(...args)),
    saveAppearance: (...args: Parameters<ApplicationSettingsControlsV1["saveAppearance"]>) => read(() => source().saveAppearance(...args)),
    applyMessageFlowFontSize: (...args: Parameters<ApplicationSettingsControlsV1["applyMessageFlowFontSize"]>) => read(() => source().applyMessageFlowFontSize(...args)),
    applyThemePreference: (...args: Parameters<ApplicationSettingsControlsV1["applyThemePreference"]>) => read(() => source().applyThemePreference(...args)),
    updateSystemPermissionGuide: (...args: Parameters<ApplicationSettingsControlsV1["updateSystemPermissionGuide"]>) => read(() => source().updateSystemPermissionGuide(...args)),
    observeBackground: (listener: Parameters<ApplicationSettingsControlsV1["observeBackground"]>[0]) => subscribe(callback => source().observeBackground(callback), listener),
    subscribeShellSkins: (listener: Parameters<ApplicationSettingsControlsV1["subscribeShellSkins"]>[0]) => subscribe(callback => source().subscribeShellSkins(callback), listener),
    observeAppliedTheme: (listener: Parameters<ApplicationSettingsControlsV1["observeAppliedTheme"]>[0]) => subscribe(callback => source().observeAppliedTheme(callback), listener),
    observeContentThemeSelection: (listener: Parameters<ApplicationSettingsControlsV1["observeContentThemeSelection"]>[0]) => subscribe(callback => source().observeContentThemeSelection(callback), listener),
    observeAppearance: (listener: Parameters<ApplicationSettingsControlsV1["observeAppearance"]>[0]) => subscribe(callback => source().observeAppearance(callback), listener),
    onSystemPermissionGuideChanged: (listener: Parameters<ApplicationSettingsControlsV1["onSystemPermissionGuideChanged"]>[0]) => subscribe(callback => source().onSystemPermissionGuideChanged(callback), listener),
    onThemePreferenceChange: (listener: Parameters<ApplicationSettingsControlsV1["onThemePreferenceChange"]>[0]) => subscribe(callback => source().onThemePreferenceChange(callback), listener),
    onActivityPreviewAutoShowChange: (listener: Parameters<ApplicationSettingsControlsV1["onActivityPreviewAutoShowChange"]>[0]) => subscribe(callback => source().onActivityPreviewAutoShowChange(callback), listener),
    onAppContextChanged: (listener: Parameters<ApplicationSettingsControlsV1["onAppContextChanged"]>[0]) => subscribe(callback => source().onAppContextChanged(callback), listener),
    updateBackground: (update: Parameters<ApplicationSettingsControlsV1["updateBackground"]>[0]) => invoke(() => source().updateBackground(current => read(() => update(current)))),
    processBackground: (image: Blob, purpose: "import" | "light" | "dark", requestSignal?: AbortSignal) => invoke(() => source().processBackground(image, purpose,
      requestSignal ? AbortSignal.any([signal, requestSignal]) : signal)),
    discardAppContextSnapshot: (...args: Parameters<ApplicationSettingsControlsV1["discardAppContextSnapshot"]>) => invoke(() => source().discardAppContextSnapshot(...args)),
    loadPluginIconURL: (...args: Parameters<ApplicationSettingsControlsV1["loadPluginIconURL"]>) => invoke(() => source().loadPluginIconURL(...args)),
    get capabilities() { return read(() => source().capabilities); },
    subscribe: (listener: () => void) => subscribe(callback => source().subscribe(callback), listener),
    subscribeRemote: (listener: () => void) => subscribe(callback => source().subscribeRemote(callback), listener),
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

/** Bind live conversation reads to the selected UI generation. */
export function scopeApplicationConversationText(source: () => ApplicationConversationTextV1,
  signal: AbortSignal, assertActive: () => void): ApplicationConversationTextV1 {
  return Object.freeze<ApplicationConversationTextV1>({
    getSnapshot: target => { assertActive(); signal.throwIfAborted(); return source().getSnapshot(target); },
    subscribe: (target, listener) => {
      assertActive(); signal.throwIfAborted();
      const stop = source().subscribe(target, () => { if (!signal.aborted) listener(); });
      let stopped = false;
      const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
      signal.addEventListener("abort", dispose, { once: true });
      if (signal.aborted) dispose();
      return dispose;
    },
  });
}

/** Fence native file operations without transferring their ownership to a UI. */
export function scopeApplicationPreviewContent(source: () => ApplicationPreviewContentV1,
  signal: AbortSignal, assertActive: () => void): ApplicationPreviewContentV1 {
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: PreviewContentOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze({
    get capabilities() { assertActive(); signal.throwIfAborted(); return source().capabilities; },
    resolveResourceURL: (target: { readonly source: string; readonly cwd?: string }) => { assertActive(); signal.throwIfAborted(); return source().resolveResourceURL(target); },
    openFile: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().openFile(target, { signal }), options),
    listDirectory: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().listDirectory(target, { signal }), options),
    readFile: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().readFile(target, { signal }), options),
    revealItem: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().revealItem(target, { signal }), options),
    showItemMenu: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().showItemMenu(target, { signal }), options),
    exportFile: (target: PreviewFileTargetV1, options?: PreviewContentOptionsV1) => invoke(signal => source().exportFile(target, { signal }), options),
  });
}

async function invokeApplicationOperation<T>(operation: (signal: AbortSignal) => Promise<T>,
  signal: AbortSignal, assertActive: () => void, requestSignal?: AbortSignal): Promise<T> {
  assertActive(); signal.throwIfAborted(); requestSignal?.throwIfAborted();
  const lifetime = new AbortController();
  const abort = () => lifetime.abort(signal.aborted ? signal.reason : requestSignal?.reason);
  signal.addEventListener("abort", abort, { once: true });
  requestSignal?.addEventListener("abort", abort, { once: true });
  let rejectAbort!: () => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    rejectAbort = () => reject(lifetime.signal.reason ?? new Error("Application generation was disposed"));
    lifetime.signal.addEventListener("abort", rejectAbort, { once: true });
  });
  try {
    const result = await Promise.race([operation(lifetime.signal), cancelled]);
    assertActive(); lifetime.signal.throwIfAborted(); return result;
  } finally {
    signal.removeEventListener("abort", abort); requestSignal?.removeEventListener("abort", abort);
    lifetime.signal.removeEventListener("abort", rejectAbort);
  }
}

/** Catalog effects retain one source and one generation; never replay on replacement. */
export function scopeApplicationCatalog(source: () => ApplicationCatalogV1,
  signal: AbortSignal, assertActive: () => void): ApplicationCatalogV1 {
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: CatalogCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze({
    getSnapshot: () => { assertActive(); signal.throwIfAborted(); return source().getSnapshot(); },
    listSkillsForThread: (input: CatalogThreadSkillsInputV1, options?: CatalogCallOptionsV1) => invoke(signal => source().listSkillsForThread(input, { signal }), options),
    listSkills: (options?: CatalogCallOptionsV1) => invoke(signal => source().listSkills({ signal }), options),
    readSkill: (input: { readonly name: string; readonly source: string }, options?: CatalogCallOptionsV1) => invoke(signal => source().readSkill(input, { signal }), options),
    refresh: (options?: CatalogCallOptionsV1) => invoke(signal => source().refresh({ signal }), options),
    update: (input: CatalogPackageUpdateV1, options?: CatalogCallOptionsV1) => invoke(signal => source().update(input, { signal }), options),
    install: (options?: CatalogCallOptionsV1) => invoke(signal => source().install({ signal }), options),
    remove: (id: string, options?: CatalogCallOptionsV1) => invoke(signal => source().remove(id, { signal }), options),
    loadIcon: (id: string, fingerprint: string, path: string, options?: CatalogCallOptionsV1) => invoke(signal => source().loadIcon(id, fingerprint, path, { signal }), options),
  });
}

/** Ledger observation ends with its UI scope; project execution stays in the core. */
export function scopeApplicationProjects(source: () => ApplicationProjectsV1,
  signal: AbortSignal, assertActive: () => void): ApplicationProjectsV1 {
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: ApplicationProjectCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze<ApplicationProjectsV1>({
    getSnapshot: projectId => { assertActive(); signal.throwIfAborted(); return source().getSnapshot(projectId); },
    subscribe: (projectId, listener) => {
      assertActive(); signal.throwIfAborted();
      const stop = source().subscribe(projectId, () => { if (!signal.aborted) listener(); });
      let stopped = false;
      const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
      signal.addEventListener("abort", dispose, { once: true });
      if (signal.aborted) dispose();
      return dispose;
    },
    refresh: (projectId, options) => invoke(signal => source().refresh(projectId, { signal }), options),
    control: (input, options) => invoke(signal => source().control(input, { signal }), options),
    releaseSession: (input, options) => invoke(signal => source().releaseSession(input, { signal }), options),
  });
}

/** File references mutate the shared composer under the captured UI generation. */
export function scopeApplicationFileSelection(source: () => ApplicationFileSelectionServiceV1,
  signal: AbortSignal, assertActive: () => void): ApplicationFileSelectionServiceV1 {
  const read = <T>(operation: () => T): T => { assertActive(); signal.throwIfAborted(); return operation(); };
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: FileSelectionCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze<ApplicationFileSelectionServiceV1>({
    getSnapshot: target => read(() => source().getSnapshot(target)),
    subscribe: listener => read(() => {
      const stop = source().subscribe(() => { if (!signal.aborted) listener(); });
      let stopped = false;
      const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
      signal.addEventListener("abort", dispose, { once: true });
      if (signal.aborted) dispose();
      return dispose;
    }),
    quote: input => read(() => source().quote(input)), comment: input => read(() => source().comment(input)),
    removeComment: input => read(() => source().removeComment(input)),
    edit: (input, options) => invoke(signal => source().edit(input, { signal }), options),
    openPdf: (input, options) => invoke(signal => source().openPdf(input, { signal }), options),
    openFile: (input, options) => invoke(signal => source().openFile(input, { signal }), options),
    askSide: (input, options) => invoke(signal => source().askSide(input, { signal }), options),
  });
}

/** A UI owns presentation leases and listeners, never the shared native tab registry. */
export function scopeApplicationBrowser(source: () => ApplicationBrowserV1,
  signal: AbortSignal, assertActive: () => void): ApplicationBrowserV1 {
  const cleanup = new Set<() => void>();
  const check = () => { assertActive(); signal.throwIfAborted(); };
  const subscribe = <T>(register: (listener: (value: T) => void) => () => void, listener: (value: T) => void,
    local?: Set<() => void>): (() => void) => {
    check(); const off = register(value => { if (!signal.aborted) listener(value); });
    let stopped = false;
    const stop = () => { if (stopped) return; stopped = true; cleanup.delete(stop); local?.delete(stop); off(); };
    cleanup.add(stop); local?.add(stop);
    if (signal.aborted) stop();
    return stop;
  };
  signal.addEventListener("abort", () => { for (const dispose of [...cleanup]) dispose(); }, { once: true });
  return Object.freeze<ApplicationBrowserV1>({
    get available() { check(); return source().available; },
    acquireSurface: target => {
      check(); const handle = source().acquireSurface(target), local = new Set<() => void>();
      let disposed = false;
      const checkLease = () => { check(); if (disposed) throw new Error("Browser presentation lease is disposed"); };
      const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: BrowserOperationOptionsV1) =>
        invokeApplicationOperation(operation, signal, checkLease, options?.signal);
      const dispose = () => { if (disposed) return; disposed = true; cleanup.delete(dispose); for (const stop of [...local]) stop(); handle.dispose(); };
      cleanup.add(dispose);
      const scoped: BrowserSurfaceHandleV1 = {
        target: handle.target,
        read: options => invoke(signal => handle.read({ signal }), options),
        command: (input, options) => invoke(signal => handle.command(input, { signal }), options),
        takeOver: (activity, generation, options) => invoke(signal => handle.takeOver(activity, generation, { signal }), options),
        onSurface: listener => { checkLease(); return subscribe(callback => handle.onSurface(callback), listener, local); },
        onTabAdopted: listener => { checkLease(); return subscribe(callback => handle.onTabAdopted(callback), listener, local); },
        onUserInput: listener => { checkLease(); return subscribe(callback => handle.onUserInput(callback), listener, local); },
        reportBounds: (rect, force) => { checkLease(); handle.reportBounds(rect, force); },
        suppressOverlay: suppressed => { checkLease(); handle.suppressOverlay(suppressed); }, dispose,
      };
      return Object.freeze(scoped);
    },
    onDock: listener => subscribe(callback => source().onDock(callback), listener),
    onInvalidate: listener => subscribe(callback => source().onInvalidate(callback), listener),
    openURL: (input, options) => invokeApplicationOperation(signal => source().openURL(input, { signal }), signal, assertActive, options?.signal),
    openExternal: (url, options) => invokeApplicationOperation(signal => source().openExternal(url, { signal }), signal, assertActive, options?.signal),
    consumeNavigation: requestId => { check(); source().consumeNavigation(requestId); },
  });
}

/** Setup state survives renderer replacement; observations and commands retain their caller lifetime. */
export function scopeApplicationOnboarding(source: () => ApplicationOnboardingV1,
  signal: AbortSignal, assertActive: () => void): ApplicationOnboardingV1 {
  const read = <T>(operation: () => T): T => { assertActive(); signal.throwIfAborted(); return operation(); };
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: OnboardingCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze<ApplicationOnboardingV1>({
    getSnapshot: () => read(() => source().getSnapshot()),
    checkpoint: input => read(() => source().checkpoint(input)),
    run: operation => invoke(() => source().run(operation)),
    subscribe: listener => read(() => {
      const stop = source().subscribe(() => { if (!signal.aborted) listener(); });
      let stopped = false;
      const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
      signal.addEventListener("abort", dispose, { once: true });
      if (signal.aborted) dispose();
      return dispose;
    }),
    discoverWorkspaces: options => invoke(signal => source().discoverWorkspaces({ signal }), options),
    chooseWorkspace: options => invoke(signal => source().chooseWorkspace({ signal }), options),
    complete: (input, options) => invoke(signal => source().complete(input, { signal }), options),
  });
}

/** Resource leases keep their source and cancellation links until release, even after acquisition settles. */
export function scopeApplicationArtifacts(source: () => ApplicationArtifactsV1,
  signal: AbortSignal, assertActive: () => void): ApplicationArtifactsV1 {
  const leases = new Map<string, () => void>();
  const check = () => { assertActive(); signal.throwIfAborted(); };
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: ArtifactCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze<ApplicationArtifactsV1>({
    get capabilities() { check(); return source().capabilities; },
    resolveSource: resource => { check(); return source().resolveSource(resource); },
    acquireSource: async (resource, options) => {
      check(); options?.signal?.throwIfAborted();
      const owner = source(), lifetime = new AbortController();
      let leaseId: string | undefined;
      const detach = () => { signal.removeEventListener("abort", abort); options?.signal?.removeEventListener("abort", abort); };
      const release = () => { if (leaseId) { leases.delete(leaseId); owner.releaseSource(leaseId); leaseId = undefined; } detach(); };
      const abort = () => { lifetime.abort(signal.aborted ? signal.reason : options?.signal?.reason); release(); };
      signal.addEventListener("abort", abort, { once: true }); options?.signal?.addEventListener("abort", abort, { once: true });
      try {
        const result = await owner.acquireSource(resource, { signal: lifetime.signal });
        leaseId = result.leaseId;
        check(); lifetime.signal.throwIfAborted();
        if (leaseId) leases.set(leaseId, release); else detach();
        return result;
      } catch (error) { release(); throw error; }
    },
    releaseSource: leaseId => { leases.get(leaseId)?.(); },
    readAttachment: (input, options) => invoke(signal => source().readAttachment(input, { signal }), options),
    readItem: (input, options) => invoke(signal => source().readItem(input, { signal }), options),
    readText: (resource, options) => invoke(signal => source().readText(resource, { signal }), options),
    save: (resource, options) => invoke(signal => source().save(resource, { signal }), options),
    showItemMenu: (resource, options) => invoke(signal => source().showItemMenu(resource, { signal }), options),
    openFile: (resource, options) => invoke(signal => source().openFile(resource, { signal }), options),
  });
}

/** Git operations observe the same worktree; each observer's queued refresh retains its lifetime. */
export function scopeApplicationEnvironment(source: () => ApplicationEnvironmentV1,
  signal: AbortSignal, assertActive: () => void): ApplicationEnvironmentV1 {
  const check = () => { assertActive(); signal.throwIfAborted(); };
  const invoke = <T>(operation: (signal: AbortSignal) => Promise<T>, options?: EnvironmentCallOptionsV1) =>
    invokeApplicationOperation(operation, signal, assertActive, options?.signal);
  return Object.freeze<ApplicationEnvironmentV1>({
    getSnapshot: () => { check(); return source().getSnapshot(); },
    subscribe: listener => {
      check(); const stop = source().subscribe(() => { if (!signal.aborted) listener(); });
      let stopped = false;
      const dispose = () => { if (stopped) return; stopped = true; signal.removeEventListener("abort", dispose); stop(); };
      signal.addEventListener("abort", dispose, { once: true }); if (signal.aborted) dispose(); return dispose;
    },
    checkoutBranch: (branch, options) => invoke(signal => source().checkoutBranch(branch, { signal }), options),
    createAndCheckoutBranch: (branch, options) => invoke(signal => source().createAndCheckoutBranch(branch, { signal }), options),
    commit: (input, options) => invoke(signal => source().commit(input, { signal }), options),
    generateCommitMessage: (input, options) => invoke(signal => source().generateCommitMessage(input, { signal }), options),
    createPullRequest: (input, options) => invoke(signal => source().createPullRequest(input, { signal }), options),
    listChanges: (input, options) => invoke(signal => source().listChanges(input, { signal }), options),
    readDiff: (input, options) => invoke(signal => source().readDiff(input, { signal }), options),
    scheduleStatusRefresh: (delayMs, options) => {
      check(); options?.signal?.throwIfAborted();
      source().scheduleStatusRefresh(delayMs, { signal: options?.signal ? AbortSignal.any([signal, options.signal]) : signal });
    },
  });
}
