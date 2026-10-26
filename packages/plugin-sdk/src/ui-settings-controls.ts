import type { ApplicationSettingsChatGPTAuthStatusV1 as ChatGPTAuthStatus } from "./application-settings.js";
import type { ReactNode } from "react";
import type { SettingsPageHostAPI } from "./index.js";
import type { ApplicationSettingsCoreBuildInfoV1 as CoreBuildInfo } from "./application-settings.js";
import type { ApplicationSettingsProviderSummaryV1 as ProviderSummary } from "./application-settings.js";
import type { ApplicationSettingsCatalogProviderSummaryV1 as CatalogProviderSummary } from "./application-settings.js";
import type { ApplicationSettingsEngineListResultV1 as EngineListResult } from "./application-settings.js";
import type { ApplicationSettingsProviderModelSummaryV1 as ProviderModelSummary } from "./application-settings.js";
export interface SettingsControlsCapabilitiesV1 {
  readonly systemPermissions: boolean;
  readonly permissionGuide: boolean;
  readonly permissionGuideUpdate: boolean;
  readonly permissionGuideClose: boolean;
  readonly themeChanges: boolean;
  readonly appContext: boolean;
  readonly appContextChanges: boolean;
  readonly appContextRecording: boolean;

  readonly chatGPTLogin: boolean;
  readonly engineLogin: boolean;
  readonly browser: boolean;
  readonly browserReload: boolean;
  readonly browserOptions: boolean;
  readonly remote: boolean;
  readonly build: boolean;
  readonly catalog: boolean;
  readonly engines: boolean;
  readonly pets: boolean;
  readonly diagnostics: boolean;
  readonly platform?: string;
}
export interface SettingsControlsPageV1 {
  readonly pluginId: string;
  readonly generation: string;
  readonly id: string;
  readonly view: string;
  readonly title: string;
  readonly description?: string;
  readonly order?: number;
  readonly icon?: {
    name: string;
    path?: never;
    light?: never;
    dark?: never;
  } | {
    name?: never;
    path: string;
    light?: never;
    dark?: never;
  } | {
    name?: never;
    path?: never;
    light: string;
    dark: string;
  };
}
export interface SettingsControlsConflictV1 {
  readonly key: string;
  readonly kind: "surface" | "presenter";
  readonly target: string;
  readonly winnerPluginId: string;
  readonly candidates: readonly {
    readonly pluginId: string;
    readonly generation: string;
    readonly contributionId: string;
    readonly title?: string;
  }[];
}
export interface SettingsControlsDiagnosticV1 {
  readonly kind: "activation" | "cleanup" | "render" | "runtime";
  readonly message: string;
}
/** Public settings mechanisms. Operation results are caller-owned; snapshots are immutable. */
export interface ApplicationSettingsControlsV1 {
  discardAppContextSnapshot: (id?: string) => Promise<AppContextState>;
  loadPluginIconURL(pluginId: string, fingerprint: string, path: string): Promise<string>;
  readBackground(): Promise<SettingsBackgroundV1 | null>;
  updateBackground(update: (current: SettingsBackgroundV1 | null) => SettingsBackgroundV1 | null): Promise<void>;
  observeBackground(listener: () => void): () => void;
  processBackground(image: Blob, purpose: "import" | "light" | "dark", signal?: AbortSignal): Promise<Blob>;
  subscribeShellSkins(listener: () => void): () => void;
  getShellSkins(): { readonly skins: readonly SettingsShellSkinV1[]; readonly selected: string };
  selectShellSkin(key: string): void;
  loadSkinImage(input: { id: string; fingerprint: string; path: string }): Promise<string>;

  readonly connectionDisplay: SettingsConnectionDisplayV1;
  readDraftEngineMemory(): SettingsDraftEngineMemoryV1 | undefined;
  writeDraftEngineMemory(memory: SettingsDraftEngineMemoryV1): void;
  clearDraftEngineMemory(): void;

  currentAppliedTheme(): "light" | "dark";
  observeAppliedTheme(listener: (theme: "light" | "dark") => void): () => void;
  observeContentThemeSelection(listener: () => void): () => void;
  availableExtensionThemes(inventory: readonly import("./application-settings.js").ApplicationSettingsExtensionInventoryRecordV1[] | undefined): { key: string; legacyKeys: string[]; name: string; base: "light" | "dark" }[];
  selectedExtensionThemeKey(mode: "light" | "dark"): string;
  selectExtensionTheme(mode: "light" | "dark", key: string): void;

  readonly initialMessageFlowFontSize: number;
  readonly initialThemePreference: "system" | "light" | "dark";
  readonly appearanceDefaults: Readonly<SettingsAppearanceV1>;
  readAppearance(): SettingsAppearanceV1;
  saveAppearance(preferences: SettingsAppearanceV1): void;
  observeAppearance(listener: () => void): () => void;
  listLocalFonts(): Promise<{ names: string[]; monospace: string[] }>;
  applyMessageFlowFontSize(size: number): void;
  applyThemePreference(preference: "system" | "light" | "dark"): void;

  getSystemPermissions: () => Promise<SystemPermissionsState>;
  requestSystemPermission: (permission: SystemPermission, presentation?: SystemPermissionPresentation) => Promise<void>;
  updateSystemPermissionGuide: (presentation: SystemPermissionPresentation) => void;
  onSystemPermissionGuideChanged: (handler: (state: SystemPermissionGuideState) => void) => () => void;
  closeSystemPermissionGuide: (requestId?: string) => Promise<void>;
  getMessageFlowFontSize: () => Promise<MessageFlowFontSize>;
  setMessageFlowFontSize: (
    fontSize: MessageFlowFontSize,
  ) => Promise<{ ok: boolean; fontSize: MessageFlowFontSize }>;
  getThemePreference: () => Promise<ThemePreference>;
  setThemePreference: (
    theme: ThemePreference,
  ) => Promise<{ ok: boolean; theme: ThemePreference }>;
  onThemePreferenceChange: (
    handler: (theme: ThemePreference) => void,
  ) => () => void;
  getAppContextState: () => Promise<AppContextState>;
  updateAppContextSettings: (settings: AppContextSettings) => Promise<AppContextState>;
  setAppContextShortcutRecording: (recording: boolean) => Promise<AppContextState>;
  onAppContextChanged: (handler: () => void) => () => void;
  listChatGPTAccounts: () => Promise<{ accounts: ChatGPTAccount[] }>;
  signOutChatGPTAccount: (accountId: string) => Promise<{ ok: boolean; revocation_confirmed: boolean; providers: ProviderSummary[] }>;
  startChatGPTLogin: (accountId?: string, enablePlanUsage?: boolean) => Promise<AuthChatGPTLoginResult>;
  pollChatGPTLogin: (loginId: string) => Promise<AuthChatGPTLoginResult>;
  cancelChatGPTLogin: (loginId: string) => Promise<{ ok: boolean }>;
  listEngineAuthMethods: (engineID: string) => Promise<EngineAuthResult>;
  authenticateEngine: (engineID: string, methodID: string) => Promise<EngineAuthResult>;
  cancelEngineAuth: (engineID: string) => Promise<{ ok: boolean }>;
  renderPresentation(input: SettingsControlsPresentationV1): ReactNode;
  readonly capabilities: SettingsControlsCapabilitiesV1;
  subscribe(listener: () => void): () => void;
  getPages(): readonly SettingsControlsPageV1[];
  getConflicts(): readonly SettingsControlsConflictV1[];
  getDiagnostics(pluginId: string, generation: string): readonly SettingsControlsDiagnosticV1[];
  chooseConflict(key: string, pluginId: string): Promise<void>;
  subscribeRemote(listener: () => void): () => void;
  renderPage(input: {
    pluginId: string;
    view: string;
    settings: SettingsPageHostAPI;
    onFailure: () => void;
  }): ReactNode;
  getBuildInfo: () => Promise<BuildInfoResult>;
  useCodexCredentials: (provider: string) => Promise<{
    providers: ProviderSummary[];
  }>;
  loadCodexModels: (provider?: string) => Promise<ConfigCodexModelsResult>;
  refreshModelCatalog: () => Promise<ConfigModelCatalogRefreshResult>;
  listCatalogProviders: (provider?: string) => Promise<ConfigModelCatalogProvidersResult>;
  listEngines: (options?: {
    include_quota?: boolean;
    refresh_models?: boolean;
    cwd?: string;
  }, context?: RuntimeContext) => Promise<EngineListResult>;
  getPluginSetting: (params: PluginSettingGetParams) => Promise<PluginSettingResult>;
  setPluginSetting: (params: PluginSettingSetParams) => Promise<PluginSettingResult>;
  getPluginDiagnostics: (params: PluginIdentityParams) => Promise<PluginDiagnosticsResult>;
  listMCPServers: () => Promise<MCPListResult>;
  connectMCPServer: (name: string) => Promise<MCPServerActionResult>;
  disconnectMCPServer: (name: string) => Promise<MCPServerActionResult>;
  refreshMCPServer: (name: string) => Promise<MCPServerActionResult>;
  startMCPAuth: (name: string) => Promise<MCPAuthStartResult>;
  finishMCPAuth: (name: string, state: string, code: string) => Promise<MCPAuthFinishResult>;
  removeMCPAuth: (name: string) => Promise<MCPAuthRemoveResult>;
  listChatGPTModels: (accountId: string) => Promise<{
    models: (ProviderModelSummary & {
      name: string;
      account_descriptor?: Record<string, unknown>;
    })[];
    providers?: ProviderSummary[];
  }>;
  startXAILogin: () => Promise<AuthXAILoginStartResult>;
  pollXAILogin: (loginId: string) => Promise<AuthXAILoginPollResult>;
  cancelXAILogin: (loginId: string) => Promise<{
    ok: boolean;
  }>;
  listBrowserProfiles: () => Promise<BrowserProfile[]>;
  importBrowserCookies: (profilePath: string) => Promise<BrowserCookieImportResult>;
  listBrowserExtensions: () => Promise<BrowserExtensionRecord[]>;
  addBrowserExtension: () => Promise<BrowserExtensionRecord[]>;
  setBrowserExtensionEnabled: (id: string, enabled: boolean) => Promise<BrowserExtensionRecord[]>;
  reloadBrowserExtension: (id: string) => Promise<BrowserExtensionRecord[]>;
  openBrowserExtensionOptions: (id: string) => Promise<void>;
  removeBrowserExtension: (id: string) => Promise<BrowserExtensionRecord[]>;
  getRemoteControlSnapshot: () => Promise<RemoteControlSnapshot>;
  setRemoteHostEnabled: (enabled: boolean) => Promise<RemoteControlSnapshot>;
  startRemotePairing: () => Promise<RemoteControlSnapshot>;
  removeRemoteDevice: (fingerprintOrPub: string) => Promise<RemoteControlSnapshot>;
  openExternal: (url: string) => Promise<void>;
}
type BuildInfoResult = {
  core: CoreBuildInfo | undefined;
  desktop: DesktopBuildInfo;
};
type DesktopBuildInfo = {
  version: string;
  date: string;
};
type ConfigCodexModelsResult = {
  providers: ProviderSummary[];
  provider: string;
  model: string;
  effort?: string;
  variant?: string;
  models: CodexModelSummary[];
};
type CodexModelSummary = {
  slug: string;
  display_name?: string;
  default_reasoning_level?: string;
  supported_reasoning?: string[];
  supported_in_api: boolean;
};
type ConfigModelCatalogRefreshResult = {
  provider_count: number;
  model_count: number;
  providers: ProviderSummary[];
  catalog_providers?: CatalogProviderSummary[];
};
type ConfigModelCatalogProvidersResult = {
  providers: CatalogProviderSummary[];
};
type RuntimeContext = {
  kind: "project";
  project_id: string;
  cwd: string;
} | {
  kind: "no_project";
  cwd: string;
};
interface PluginSettingGetParams extends PluginIdentityParams {
  key: string;
}
interface PluginIdentityParams {
  id: string;
  fingerprint: string;
}
interface PluginSettingResult {
  /** Secret values are always blank; this only indicates presence in the OS credential store. */
  configured?: boolean;
  id: string;
  key: string;
  scope: PluginValueScope;
  value: boolean | string | number;
}
type PluginValueScope = "user" | "workspace";
interface PluginSettingSetParams extends PluginSettingGetParams {
  value: boolean | string | number;
}
interface PluginDiagnosticsResult {
  id: string;
  diagnostics: PluginContributionDiagnostic[];
}
interface PluginContributionDiagnostic {
  contribution: string;
  message: string;
}
type MCPListResult = {
  servers: MCPServerStatus[];
};
type MCPServerStatus = {
  name: string;
  state: string;
  auth_status?: string;
  connected: boolean;
  tool_count: number;
  error?: string;
};
type MCPServerActionResult = {
  status: MCPServerStatus;
};
type MCPAuthStartResult = {
  authorization_url: string;
  state: string;
  scopes?: string[];
};
type MCPAuthFinishResult = {
  auth: MCPAuthStatusResult;
  server: MCPServerStatus;
};
type MCPAuthStatusResult = {
  name: string;
  authenticated: boolean;
  expires_at?: string;
  scopes?: string[];
};
type MCPAuthRemoveResult = {
  auth: MCPAuthStatusResult;
  server: MCPServerStatus;
};
type AuthXAILoginStartResult = {
  login_id: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete?: string;
  expires_in: number;
  interval_ms: number;
};
type AuthXAILoginPollResult = {
  status: "pending" | "success" | "denied" | "expired" | "failed" | string;
  interval_ms?: number;
  error?: string;
  providers?: ProviderSummary[];
};
type BrowserProfile = {
  /** Stable host-side browser adapter id. */
  browserId: string;
  appName: string;
  profileDirectoryName: string;
  profileName: string;
  profilePath: string;
  hasCookies: boolean;
  hasPasswords: boolean;
  userName?: string;
  /** Last profile activity reported by the browser, as Unix milliseconds. */
  lastUsedAt?: number;
};
type BrowserCookieImportResult = {
  appName: string;
  profileName: string;
  profilePath: string;
  discovered: number;
  imported: number;
  skippedExpired: number;
  skippedInvalid: number;
  failed: number;
};
type BrowserExtensionRecord = {
  id: string;
  name: string;
  version: string;
  path: string;
  enabled: boolean;
  loaded: boolean;
  hasOptions?: boolean;
  manifest?: BrowserExtensionManifest;
  needsReview?: boolean;
  error?: string;
};
type BrowserExtensionManifest = {
  name: string;
  version: string;
  manifestVersion: 2 | 3;
  description?: string;
  permissions: string[];
  sites: string[];
  optionalPermissions: string[];
  optionalSites: string[];
  unsupportedPermissions: string[];
  compatibility: ("serviceWorker" | "action" | "options" | "browserUI" | "devtools")[];
};
type RemoteControlSnapshot = {
  status: RemoteControlStatus | null;
  status_error?: string;
  host_running: boolean;
  /** Saved access preference, including when startup failed or is still pending. */
  host_enabled?: boolean;
  pair_uri: string | null;
  web_url?: string | null;
};
type RemoteControlStatus = {
  fingerprint: string;
  account_server?: string;
  host_name?: string;
  relay_url?: string;
  store: string;
  devices: RemoteControlDevice[];
};
type RemoteControlDevice = {
  pub: string;
  fingerprint: string;
  name?: string;
  added_at: string;
};
export interface SettingsControlsPresentationV1 {
  initialized?: import("./application-settings.js").ApplicationSettingsConfigurationV1 & {
    protocol_version: string;
    workspace_root: string;
    runtime_host?: {
      kind: "local" | "cloud";
      instance_id?: string;
    };
    status?: "needs_setup" | "ready";
  };
  activePageId: string;
  availablePages: readonly import("./index.js").SettingsPageSummaryV1[];
  runningProviderNames?: readonly string[];
  busy: boolean;
  hasError: boolean;
  fallback: ReactNode;
  onOpenPage(pageId: string): void;
  onAdvancedSave(input: import("./application-settings.js").ApplicationSettingsRuntimeAdvancedSettingsUpdateV1): Promise<void>;
  onGeneralSave(input: import("./application-settings.js").ApplicationSettingsRuntimeGeneralSettingsUpdateV1): Promise<void>;
  onRefresh(): Promise<void>;
}

type ChatGPTAccount = { id: string; email?: string; status: ChatGPTAuthStatus };
type AuthChatGPTLoginResult = {
  providers?: ProviderSummary[];
  login_id: string;
  status: "pending" | "success" | "failed" | "expired" | "cancelled";
  // Go-to-main only. Main opens the browser and removes this before renderer IPC.
  authorization_url?: string;
  account_id?: string;
  interval_ms: number;
  expires_in: number;
  error?: string;
};
type EngineAuthResult = {
  methods: { id: string; name: string; description?: string; type?: string }[];
  authenticated: boolean;
};

type MessageFlowFontSize = number;
type ThemePreference = "system" | "light" | "dark";

export interface SettingsAppearanceV1 {
  messageStyle: "bubble" | "im";
  codeSize: number;
  uiFont: string;
  contentFont: string;
  codeFont: string;
  fontSmoothing: "grayscale" | "system";
  motion: "system" | "reduce";
}

type SystemPermission = "screen" | "accessibility";

/** Source row in renderer viewport coordinates, before Electron zoom. */
type SystemPermissionPresentation = {
  requestId: string;
  sourceRect: { x: number; y: number; width: number; height: number } | null;
  reducedMotion: boolean;
  /** Actual source appearance; onboarding can differ from the saved preference. */
  theme: "light" | "dark";
};

type SystemPermissionGuideState = {
  requestId: string;
  phase: "lifting" | "guiding" | "returning" | "closed";
  granted: boolean;
};

/** Current macOS grants, checked without requesting additional access. */
type SystemPermissionsState = {
  available: boolean;
  screen: boolean;
  accessibility: boolean;
};

/** Explicit, read-only local app snapshots. These never grant Computer Use. */
type AppContextSettings = {
  enabled: boolean;
  shortcut: string;
  include_text: boolean;
};

type AppContextSnapshot = {
  id: string;
  captured_at: string;
  app_name: string;
  bundle_id: string;
  window_title: string;
  window_id: number;
  width: number;
  height: number;
  image_base64: string;
  available_text: string;
  text_status: "included" | "truncated" | "not_requested" | "permission_missing" | "unavailable";
};

type AppContextState = {
  available: boolean;
  settings: AppContextSettings;
  shortcut_registered: boolean;
  phase: "idle" | "capturing" | "ready" | "error";
  app_name?: string;
  error?: string;
  snapshot?: AppContextSnapshot;
};

export interface SettingsConnectionDisplayV1 {
  subscribe(listener: () => void): () => void;
  getSnapshot(): string;
  get(key: string): { alias?: string; modelOrder: string[]; hiddenModels: string[] };
  label(key: string, fallback: string): string;
  orderModels<T extends { id: string }>(key: string, models: readonly T[]): T[];
  visibleModels<T extends { id: string }>(key: string, models: readonly T[], retainedIDs?: readonly string[]): T[];
  setAlias(key: string, alias: string): void;
  setModelOrder(key: string, ids: readonly string[]): void;
  moveModel<T extends { id: string }>(key: string, models: readonly T[], id: string, direction: -1 | 1): void;
  setModelVisible(key: string, id: string, visible: boolean): void;
}
export interface SettingsDraftEngineMemoryV1 {
  engine: string;
  model: string;
  effort: string;
  speed?: string;
  model_options?: Record<string, string>;
}

export interface SettingsBackgroundV1 { image: Blob; imageID: string; name: string; }
export interface SettingsShellSkinV1 {
 readonly id: string;
 readonly name: string;
 readonly key: string;
 readonly pluginId: string;
 readonly fingerprint: string;
 readonly pluginName: string;
 readonly light: SettingsSkinVariantV1;
 readonly dark: SettingsSkinVariantV1;
}
export interface SettingsSkinVariantV1 {
 readonly background: string;
 readonly foreground: string;
 readonly image?: string;
 readonly position?: "center" | "top" | "bottom" | "left" | "right";
 readonly overlay?: string;
 readonly hover?: string;
 readonly active?: string;
}
