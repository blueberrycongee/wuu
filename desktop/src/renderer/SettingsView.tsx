import { hostSupports } from "./HostCapabilities";
import { isTouchWebShell } from "./ComposerFocus";
import {
  ArrowLeft,
  Archive,
  BarChart3,
  Check,
  Folder,
  Hash,
  KeyRound,
  Plug,
  PlugZap,
  Plus,
  RefreshCw,
  Search,
  Settings,
  SlidersHorizontal,
  Smartphone,
  X
} from "lucide-react";
import type {
  CodexPetSettingsUpdate,
  EngineListResult,
  EngineUpdateParams,
  RuntimeAdvancedSettingsUpdate,
  RuntimeGeneralSettingsUpdate,
} from "../shared/protocol";
import {
  type CSSProperties,
  type FormEvent as ReactFormEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore
} from "react";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { useSidebarDrawerState } from "./SidebarDrawerState";
import { SIDEBAR_DRAWER_EXIT_MS, SIDEBAR_MOTION_MS } from "./AppLayoutState";
import { SelectMenu } from "./SelectMenu";
import type {
  CodexPetsSnapshot,
  DesktopBuildInfo,
  ExtensionInventoryRecord,
  InitializeResult,
  MCPAuthStartResult,
  MCPServerStatus,
  ProviderSummary,
  RemoteControlSnapshot,
  RuntimeConnectionUpdate,
  SettingsUsageDay,
  SettingsUsageResponse
} from "../shared/protocol";

// 设置 → 归档页只读侧边栏归档会话的最小字段。
// `state.threads` 是 `Thread[]`（含 `title?: string`），而 ThreadSummary
// 额外要求 `turns` / `turn_count` 等计算字段，渲染层并不关心。
// 用结构性子集既兼容 Thread，也避免把 ThreadSummary 的派生语义
// 漏到 SettingsView。归档页只用于识别和恢复会话，不展示工作目录。
export type ArchivedSessionView = {
  id: string;
  title?: string;
  updated_at: string;
  archive_project_id?: string;
  archive_project_name?: string;
};
export type ArchivedRoomView = {
  id: string;
  name: string;
  created_at: string;
};
import { normalizedVariantForProviderModel, providerModelReasoningMode, providerModelVariantOptions, variantLabel } from "./RuntimeHelpers";
import { ENABLE_REMOTE_CONTROL, ENABLE_VOICE_INPUT } from "./FeatureFlags";
import { AppearanceTypography } from "./AppearanceTypography";
import { SettingsRow } from "./SettingsRow";
import { EngineSettingsSection } from "./EngineSettingsSection";
import { SettingsRemotePage } from "./SettingsRemotePage";
import { ThemePreferenceControl } from "./ThemePreferenceSection";
import { LanguagePreferenceControl } from "./LanguagePreferenceSection";
import { formatCurrentNumber, useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import { VoiceInputSettingsSection } from "./VoiceInputSettingsSection";
import { SettingsPresentation } from "./plugins/SettingsPresentation";
import {
  desktopPluginHost,
  desktopWorkbenchController,
} from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import { PluginViewContent, type WorkbenchController } from "./plugins/Workbench";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { PluginIcon } from "./PublicIcon";
import type { SettingsPageHostAPI, SettingsPageSummaryV1, SettingsValueMapV1 } from "../shared/workbench";

export type SettingsPage =
  | "providers"
  | "collaboration"
  | "general"
  | "advanced"
  | "usage"
  | "remote"
  | "archive"
  | `plugin-settings:${string}`
  | `plugin-view:${string}:${string}`;

type CopyState = "idle" | "copying" | "copied";

const COPY_RESET_MS = 1500;

function availableSettingsPage(page: SettingsPage | undefined): SettingsPage {
  const next = page ?? "providers";
  if (next === "remote" && (!ENABLE_REMOTE_CONTROL || !hostSupports("getRemoteControlSnapshot"))) {
    return "providers";
  }
  return next;
}

export function SettingsView({
  initialized,
  initialPage,
  running,
  usage,
  usageLoading = false,
  usageError = "",
  engineInventory,
  engineInventoryError = "",
  runningProviderNames,
  codexPets,
  codexPetsLoading,
  codexPetsError,
  sidebarWidth,
  sidebarMinWidth,
  sidebarMaxWidth,
  resizingSidebar,
  shellRef,
  onBack,
  onSave,
  onRemoveProvider,
  onRefreshModelCatalog,
  onRefreshEngineInventory,
  onUpdateEngineInventory,
  onAdvancedSave,
  onGeneralSave,
  onCodexPetsRefresh,
  onCodexPetsUpdate,
  onSidebarResizeStart,
  onSidebarSeparatorKey,
  archivedThreads,
  archivedRooms,
  onUnarchiveThread,
  onUnarchiveRoom,
  // The settings rail shares the main sidebar's state and handlers wholesale:
  // same persisted width + collapse flag, same drag-to-collapse resize
  // session, same toggle motion.
  sidebarCollapsed,
  sidebarAnimating,
  onToggleSidebar,
  sidebarMotionMs,
  pluginHost = desktopPluginHost,
  workbenchController = desktopWorkbenchController,
}: {
  initialized?: InitializeResult;
  initialPage?: SettingsPage;
  running: boolean;
  usage?: SettingsUsageResponse;
  usageLoading?: boolean;
  usageError?: string;
  engineInventory?: EngineListResult;
  engineInventoryError?: string;
  runningProviderNames?: readonly string[];
  codexPets?: CodexPetsSnapshot;
  codexPetsLoading: boolean;
  codexPetsError: string;
  sidebarWidth: number;
  sidebarMinWidth: number;
  sidebarMaxWidth: number;
  resizingSidebar: boolean;
  shellRef?: RefObject<HTMLDivElement | null>;
  onBack: () => void;
  onSave: (provider: string, model: string, effort?: string, connection?: RuntimeConnectionUpdate, variant?: string) => Promise<void>;
  onRemoveProvider: (provider: string) => Promise<void>;
  onRefreshModelCatalog: () => Promise<void>;
  onRefreshEngineInventory: () => Promise<EngineListResult | undefined>;
  onUpdateEngineInventory: (params: EngineUpdateParams) => Promise<EngineListResult>;
  onAdvancedSave: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onCodexPetsRefresh: () => Promise<CodexPetsSnapshot>;
  onCodexPetsUpdate: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
  onSidebarResizeStart: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onSidebarSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
  // 归档页只读侧边栏归档清单 + 恢复回调。列表为空时渲染空态卡片。
  archivedThreads?: readonly ArchivedSessionView[];
  archivedRooms?: readonly ArchivedRoomView[];
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
  onUnarchiveRoom?: (room: ArchivedRoomView) => void;
  sidebarCollapsed: boolean;
  sidebarAnimating: boolean;
  onToggleSidebar: () => void;
  sidebarMotionMs: number;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
}): JSX.Element {
  const { t } = useI18n();
  const providers = initialized?.providers ?? [];
  const runningProviderNameSet = useMemo(
    () => new Set((runningProviderNames ?? []).map((name) => name.trim()).filter(Boolean)),
    [runningProviderNames],
  );
  const [providerDraft, setProviderDraft] = useState(initialized?.provider ?? "");
  const [modelDraft, setModelDraft] = useState(initialized?.model ?? "");
  const [variantDraft, setVariantDraft] = useState(initialized?.variant ?? initialized?.effort ?? "");
  const [baseURLDraft, setBaseURLDraft] = useState(initialized?.providers?.find((item) => item.name === initialized.provider)?.base_url ?? "");
  const [apiKeyDraft, setAPIKeyDraft] = useState("");
  // Draft for the protocol type of a brand-new provider (only used while
  // addingProvider is true). Defaults to "openai-compatible" to preserve
  // the historical behavior; the user can switch to "anthropic" before
  // saving.
  const [providerTypeDraft, setProviderTypeDraft] = useState("openai-compatible");
  const [addingProvider, setAddingProvider] = useState(false);
  const [error, setError] = useState("");
  const [xaiLogin, setXAILogin] = useState<{
    loginId: string;
    userCode: string;
    url: string;
  } | null>(null);
  const [xaiLoginBusy, setXAILoginBusy] = useState(false);
  const [desktopBuild, setDesktopBuild] = useState<DesktopBuildInfo | undefined>();
  const [activePage, setActivePage] = useState<SettingsPage>(() =>
    availableSettingsPage(initialPage),
  );
  const customPluginSettingsPages = useSyncExternalStore(
    (listener) => pluginHost.subscribe(listener),
    () => pluginHost.getSettingsPages(),
    () => pluginHost.getSettingsPages(),
  );
  const pluginSettingsRecords = useMemo(
    () => (initialized?.extension_inventory ?? []).filter(isConfigurablePlugin),
    [initialized?.extension_inventory],
  );
  const activePluginSettingsRecord = activePage.startsWith("plugin-settings:")
    ? pluginSettingsRecords.find((plugin) => pluginSettingsPageId(plugin.id) === activePage)
    : undefined;
  const activeCustomPluginPage = activePage.startsWith("plugin-view:")
    ? customPluginSettingsPages.find((entry) => pluginViewSettingsPageId(entry.pluginId, entry.id) === activePage)
    : undefined;
  const settingsPageHost = useMemo<SettingsPageHostAPI>(() => {
    const modelAliases = Object.freeze(Object.fromEntries(
      Object.entries(initialized?.model_aliases ?? {}).map(([name, alias]) => [name, Object.freeze({ ...alias })]),
    ));
    return Object.freeze({
      contractVersion: 1 as const,
      getValue: (key: "runtime.modelAliases") => {
        if (key !== "runtime.modelAliases") throw new Error(`Unsupported settings value: ${key}`);
        return modelAliases;
      },
      updateValue: async (key: "runtime.modelAliases", value: SettingsValueMapV1["runtime.modelAliases"]) => {
        if (key !== "runtime.modelAliases") throw new Error(`Unsupported settings value: ${key}`);
        await onAdvancedSave({ model_aliases: value });
      },
    });
  }, [initialized?.model_aliases, onAdvancedSave]);
  const [mcpServers, setMCPServers] = useState<MCPServerStatus[]>([]);
  const [mcpLoading, setMCPLoading] = useState(false);
  const [mcpError, setMCPError] = useState("");
  const [mcpBusyServer, setMCPBusyServer] = useState("");
  const [autoCompactDraft, setAutoCompactDraft] = useState(true);
  const [compactThresholdDraft, setCompactThresholdDraft] = useState("");
  const [compactKeepRecentDraft, setCompactKeepRecentDraft] = useState("");
  const [providerContextWindowDraft, setProviderContextWindowDraft] = useState("");
  const [maxContextTokensDraft, setMaxContextTokensDraft] = useState("");
  const [maxStepsDraft, setMaxStepsDraft] = useState("0");
  const [temperatureDraft, setTemperatureDraft] = useState("");
  const [advancedError, setAdvancedError] = useState("");
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const settingsScrollRef = useRef<HTMLDivElement>(null);
  // Last persisted draft of each numeric advanced field, recorded when
  // initialized state syncs in and after every successful commit. Blurring
  // an untouched field is a no-op instead of a redundant IPC round-trip.
  const advancedCommittedRef = useRef<Record<string, string>>({});

  useEffect(() => {
    setActivePage(availableSettingsPage(initialPage));
  }, [initialPage]);

  useLayoutEffect(() => {
    const missingGeneratedPage = activePage.startsWith("plugin-settings:")
      && activePluginSettingsRecord === undefined;
    const missingCustomPage = activePage.startsWith("plugin-view:")
      && activeCustomPluginPage === undefined;
    if (missingGeneratedPage || missingCustomPage) setActivePage("providers");
  }, [activeCustomPluginPage, activePage, activePluginSettingsRecord]);

  useLayoutEffect(() => {
    if (settingsScrollRef.current) {
      settingsScrollRef.current.scrollTop = 0;
    }
  }, [activePage]);

  useEffect(() => {
    let cancelled = false;
    if (!hostSupports("getBuildInfo")) return;
    void window.wuu.getBuildInfo().then((info) => {
      if (!cancelled) {
        setDesktopBuild(info.desktop);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setMCPLoading(true);
    setMCPError("");
    void window.wuu
      .listMCPServers()
      .then((result) => {
        if (!cancelled) {
          setMCPServers(result.servers ?? []);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setMCPError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setMCPLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const core = initialized?.core;
  const selectedProvider = addingProvider ? undefined : providers.find((item) => item.name === providerDraft);
  const providerLabels = useMemo(() => providerDisplayLabels(providers, t), [providers, t]);
  const connectionLocked = !addingProvider && (selectedProvider?.connection_locked ?? false);
  const variantOptions = providerModelVariantOptions(selectedProvider, modelDraft, variantDraft);
  const providerNameTaken = addingProvider && providers.some((item) => item.name === providerDraft.trim());

  useEffect(() => {
    // Inventory refreshes and session notifications must not reset the editor.
    if (addingProvider || providers.some((item) => item.name === providerDraft)) return;
    const summary = providers.find((item) => item.name === initialized?.provider) ?? providers[0];
    setProviderDraft(summary?.name ?? "");
    setModelDraft(summary?.model ?? "");
    setVariantDraft(normalizedVariantForProviderModel("", summary, summary?.model ?? ""));
    setBaseURLDraft(summary?.base_url ?? "");
    setAPIKeyDraft("");
    setAddingProvider(false);
    setError("");
  }, [initialized?.provider, initialized?.model, initialized?.variant, initialized?.effort, initialized?.providers, addingProvider, providerDraft]);

  useEffect(() => {
    const advanced = initialized?.advanced_settings;
    const synced = {
      compactThreshold: formatPercentDraft(advanced?.compact_threshold_pct),
      compactKeepRecent: formatOptionalNumberDraft(advanced?.compact_keep_recent_tokens),
      providerContextWindow: formatOptionalNumberDraft(advanced?.provider_context_window),
      maxContextTokens: formatOptionalNumberDraft(advanced?.max_context_tokens),
      maxSteps: String(advanced?.max_steps ?? 0),
      temperature: formatTemperatureDraft(advanced?.temperature)
    };
    setAutoCompactDraft(!(advanced?.disable_auto_compact ?? false));
    setCompactThresholdDraft(synced.compactThreshold);
    setCompactKeepRecentDraft(synced.compactKeepRecent);
    setProviderContextWindowDraft(synced.providerContextWindow);
    setMaxContextTokensDraft(synced.maxContextTokens);
    setMaxStepsDraft(synced.maxSteps);
    setTemperatureDraft(synced.temperature);
    advancedCommittedRef.current = synced;
    setAdvancedError("");
  }, [initialized?.advanced_settings, initialized?.provider, initialized?.model]);

  // Browsing providers is local UI state; it never changes a session or defaults.
  function selectProvider(provider: string): void {
    setAddingProvider(false);
    // Reset the type draft: it is only meaningful when creating a provider,
    // and leaving add mode via card click should drop any pending type pick.
    setProviderTypeDraft("openai-compatible");
    setError("");
    const summary = providers.find((item) => item.name === provider);
    if (!summary) {
      return;
    }
    const variant = normalizedVariantForProviderModel(
      initialized?.variant ?? initialized?.effort ?? "",
      summary,
      summary.model,
    );
    setProviderDraft(provider);
    setModelDraft(summary.model);
    setVariantDraft(variant);
    setBaseURLDraft(summary.base_url ?? "");
    setAPIKeyDraft("");
  }

  function startAddingProvider(): void {
    setAddingProvider(true);
    setProviderDraft(nextCustomProviderName(providers));
    setProviderTypeDraft("openai-compatible");
    setModelDraft("");
    setVariantDraft("");
    setBaseURLDraft("");
    setAPIKeyDraft("");
    setXAILogin(null);
    setError("");
  }

  function changeProviderTypeDraft(type: string): void {
    setProviderTypeDraft(type);
    if (isGrokBuildType(type)) {
      if (!providerDraft.trim() || providerDraft.startsWith("custom-")) {
        setProviderDraft(providers.some((item) => item.name === "grok-build") ? nextCustomProviderName(providers) : "grok-build");
      }
      setModelDraft("grok-4.5");
      setBaseURLDraft("https://cli-chat-proxy.grok.com/v1");
      setAPIKeyDraft("");
      return;
    }
    if (!isXAISubscriptionType(type)) {
      return;
    }
    if (!providerDraft.trim() || providerDraft.startsWith("custom-")) {
      setProviderDraft(providers.some((item) => item.name === "xai-subscription") ? nextCustomProviderName(providers) : "xai-subscription");
    }
    if (!modelDraft.trim()) {
      setModelDraft("grok-4.6");
    }
    setBaseURLDraft("https://api.x.ai/v1");
    setAPIKeyDraft("");
  }

  async function startXAILogin(): Promise<void> {
    if (xaiLoginBusy || typeof window.wuu.startXAILogin !== "function") {
      return;
    }
    setError("");
    setXAILoginBusy(true);
    try {
      const start = await window.wuu.startXAILogin();
      const url = start.verification_uri_complete || start.verification_uri;
      setXAILogin({ loginId: start.login_id, userCode: start.user_code, url });
      if (url) {
        await window.wuu.openExternal(url);
      }
      const deadline = Date.now() + Math.max(30, start.expires_in || 300) * 1000;
      let interval = Math.max(1000, start.interval_ms || 5000);
      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, interval));
        const poll = await window.wuu.pollXAILogin(start.login_id);
        if (poll.status === "pending") {
          interval = Math.max(1000, poll.interval_ms || interval);
          continue;
        }
        if (poll.status !== "success") {
          throw new Error(poll.error || t("error.oauthFailed"));
        }
        setXAILogin(null);
        if (addingProvider) {
          const connection: RuntimeConnectionUpdate = {
            base_url: baseURLDraft.trim() || "https://api.x.ai/v1",
            type: "xai-subscription",
            create_provider: true
          };
          await onSave(providerDraft.trim() || "xai-subscription", modelDraft.trim() || "grok-4.6", undefined, connection, variantDraft);
          setAddingProvider(false);
        } else {
          await onSave(providerDraft, modelDraft, undefined, undefined, variantDraft);
        }
        return;
      }
      throw new Error(t("error.oauthFailed"));
    } catch (loginError) {
      setXAILogin(null);
      setError(loginError instanceof Error ? loginError.message : t("error.oauthFailed"));
    } finally {
      setXAILoginBusy(false);
    }
  }

  function cancelAddingProvider(): void {
    setAddingProvider(false);
    setXAILogin(null);
    setProviderDraft(initialized?.provider ?? "");
    setProviderTypeDraft("openai-compatible");
    setModelDraft(initialized?.model ?? "");
    setVariantDraft(initialized?.variant ?? initialized?.effort ?? "");
    const summary = initialized?.providers?.find((item) => item.name === initialized.provider);
    setBaseURLDraft(summary?.base_url ?? "");
    setAPIKeyDraft("");
    setError("");
  }

  // Field edits persist against the selected provider, independently and
  // instantly: text inputs commit on blur or Enter, the effort select on
  // change. An emptied required field snaps back to the persisted value;
  // an empty API key field means "keep the current secret" and never
  // round-trips.
  async function commitModelName(selection?: string): Promise<void> {
    if (addingProvider) {
      return;
    }
    const model = (selection ?? modelDraft).trim();
    if (selection !== undefined) setModelDraft(model);
    if (!model) {
      setModelDraft(selectedProvider?.model ?? "");
      return;
    }
    if (model === (selectedProvider?.model ?? "")) {
      return;
    }
    const variant = normalizedVariantForProviderModel(variantDraft, selectedProvider, model);
    const previousVariant = variantDraft;
    setVariantDraft(variant);
    setError("");
    try {
      await onSave(providerDraft, model, undefined, undefined, variant);
    } catch (saveError) {
      setModelDraft(selectedProvider?.model ?? "");
      setVariantDraft(previousVariant);
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function changeVariant(variant: string): Promise<void> {
    const previous = variantDraft;
    setVariantDraft(variant);
    if (addingProvider) {
      return;
    }
    setError("");
    try {
      await onSave(providerDraft, modelDraft, undefined, undefined, variant);
    } catch (saveError) {
      setVariantDraft(previous);
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function removeModel(model: string): Promise<void> {
    if (addingProvider || !selectedProvider || running) return;
    const remaining = [...new Set([selectedProvider.model, ...(selectedProvider.models ?? []).map((item) => item.id)])]
      .filter((id) => id && id !== model);
    if (!remaining.length) return;
    const nextModel = selectedProvider.model === model ? remaining[0] : selectedProvider.model;
    const variant = normalizedVariantForProviderModel(variantDraft, selectedProvider, nextModel);
    setError("");
    try {
      await onSave(providerDraft, nextModel, undefined, { remove_model: model }, variant);
      setModelDraft(nextModel);
      setVariantDraft(variant);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function commitBaseURL(): Promise<void> {
    if (addingProvider || connectionLocked) {
      return;
    }
    const baseURL = baseURLDraft.trim();
    if (!baseURL) {
      setBaseURLDraft(selectedProvider?.base_url ?? "");
      return;
    }
    if (baseURL === (selectedProvider?.base_url ?? "")) {
      return;
    }
    setError("");
    try {
      await onSave(providerDraft, modelDraft, undefined, { base_url: baseURL }, variantDraft);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function commitAPIKey(): Promise<void> {
    if (addingProvider || connectionLocked) {
      return;
    }
    const apiKey = apiKeyDraft.trim();
    if (!apiKey) {
      return;
    }
    const connection: RuntimeConnectionUpdate = {
      base_url: baseURLDraft.trim() || selectedProvider?.base_url || ""
    };
    connection.api_key = apiKey;
    setError("");
    try {
      await onSave(providerDraft, modelDraft, undefined, connection, variantDraft);
      setAPIKeyDraft("");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function submit(event: ReactFormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!addingProvider) {
      return;
    }
    setError("");
    try {
      const connection: RuntimeConnectionUpdate = {
        base_url: baseURLDraft.trim(),
        type: providerTypeDraft,
        create_provider: true
      };
      if (!isXAISubscriptionType(providerTypeDraft) && !isGrokBuildType(providerTypeDraft)) {
        connection.api_key = apiKeyDraft.trim();
      }
      await onSave(providerDraft, modelDraft, undefined, connection, variantDraft);
      setAddingProvider(false);
      setAPIKeyDraft("");
      setXAILogin(null);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : t("provider.saveFailed"));
    }
  }

  async function requestRemoveProvider(name: string): Promise<void> {
    if (running || !name) {
      return;
    }
    setError("");
    try {
      await onRemoveProvider(name);
      // The parent's state update will refresh initialized.providers
      // and active provider/model; sync the local drafts so the form
      // does not show the now-deleted provider as selected.
      setAddingProvider(false);
    } catch (removeError) {
      setError(
        removeError instanceof Error
          ? removeError.message
          : t("provider.removeFailed"),
      );
    }
  }

  async function runMCPAction(name: string, action: "connect" | "disconnect" | "refresh"): Promise<void> {
    setMCPBusyServer(name);
    setMCPError("");
    try {
      const result =
        action === "connect"
          ? await window.wuu.connectMCPServer(name)
          : action === "disconnect"
            ? await window.wuu.disconnectMCPServer(name)
            : await window.wuu.refreshMCPServer(name);
      setMCPServers((servers) => upsertMCPServerStatus(servers, result.status));
    } catch (err) {
      setMCPError(err instanceof Error ? err.message : String(err));
    } finally {
      setMCPBusyServer("");
    }
  }

  async function startMCPAuth(name: string): Promise<MCPAuthStartResult | undefined> {
    setMCPBusyServer(name);
    setMCPError("");
    try {
      const result = await window.wuu.startMCPAuth(name);
      await window.wuu.openExternal(result.authorization_url);
      return result;
    } catch (err) {
      setMCPError(err instanceof Error ? err.message : String(err));
      return undefined;
    } finally {
      setMCPBusyServer("");
    }
  }

  async function finishMCPAuth(name: string, state: string, code: string): Promise<boolean> {
    setMCPBusyServer(name);
    setMCPError("");
    try {
      const result = await window.wuu.finishMCPAuth(name, state, code);
      setMCPServers((servers) => upsertMCPServerStatus(servers, result.server));
      return true;
    } catch (err) {
      setMCPError(err instanceof Error ? err.message : String(err));
      return false;
    } finally {
      setMCPBusyServer("");
    }
  }

  async function removeMCPAuth(name: string): Promise<void> {
    setMCPBusyServer(name);
    setMCPError("");
    try {
      const result = await window.wuu.removeMCPAuth(name);
      setMCPServers((servers) => upsertMCPServerStatus(servers, result.server));
    } catch (err) {
      setMCPError(err instanceof Error ? err.message : String(err));
    } finally {
      setMCPBusyServer("");
    }
  }

  async function copyVersionInfo(): Promise<void> {
    if (!desktopBuild || copyState === "copying") {
      return;
    }
    setCopyState("copying");
    const pieces = [`wuu ${versionLabel(desktopBuild.version)}`];
    if (desktopBuild.date) {
      pieces.push(formatBuildDate(desktopBuild.date));
    }
    if (core?.version) {
      pieces.push(`core ${versionLabel(core.version)}`);
    }
    const text = pieces.join(" · ");
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const fallback = document.createElement("textarea");
        fallback.value = text;
        fallback.setAttribute("readonly", "");
        fallback.style.position = "absolute";
        fallback.style.left = "-9999px";
        document.body.appendChild(fallback);
        fallback.select();
        document.execCommand("copy");
        document.body.removeChild(fallback);
      }
      setCopyState("copied");
      window.setTimeout(() => setCopyState("idle"), COPY_RESET_MS);
    } catch {
      setCopyState("idle");
    }
  }

  // Instant-apply: the switch persists immediately (rolling back on
  // failure); numeric drafts persist on blur/Enter after per-field
  // validation. No Save button, no "saved" confirmation — the control
  // staying put is the confirmation, consistent with the MCP toggles.
  function toggleAutoCompact(): void {
    const next = !autoCompactDraft;
    setAutoCompactDraft(next);
    setAdvancedError("");
    void onAdvancedSave({ disable_auto_compact: !next }).catch((saveError: unknown) => {
      setAutoCompactDraft(!next);
      setAdvancedError(saveError instanceof Error ? saveError.message : t("settings.saveFailed"));
    });
  }

  async function commitAdvancedField(field: AdvancedNumericField): Promise<void> {
    const drafts: Record<AdvancedNumericField, string> = {
      compactThreshold: compactThresholdDraft,
      compactKeepRecent: compactKeepRecentDraft,
      providerContextWindow: providerContextWindowDraft,
      maxContextTokens: maxContextTokensDraft,
      maxSteps: maxStepsDraft,
      temperature: temperatureDraft
    };
    const draft = drafts[field];
    if (advancedCommittedRef.current[field] === draft) {
      return;
    }
    let update: RuntimeAdvancedSettingsUpdate | undefined;
    let validationError = "";
    switch (field) {
      case "compactThreshold": {
        const parsed = parseOptionalNumber(draft, t("settings.compactThreshold"), t);
        if (parsed.error) {
          validationError = parsed.error;
        } else if (parsed.value >= 100) {
          validationError = t("validation.compactThreshold");
        } else {
          update = { compact_threshold_pct: parsed.value > 0 ? parsed.value / 100 : 0 };
        }
        break;
      }
      case "compactKeepRecent": {
        const parsed = parseOptionalInteger(draft, t("settings.keepRecentContext"), t);
        if (parsed.error) validationError = parsed.error;
        else update = { compact_keep_recent_tokens: parsed.value };
        break;
      }
      case "providerContextWindow": {
        const parsed = parseOptionalInteger(draft, t("settings.providerContextLimit"), t);
        if (parsed.error) validationError = parsed.error;
        else update = { provider_context_window: parsed.value };
        break;
      }
      case "maxContextTokens": {
        const parsed = parseOptionalInteger(draft, t("settings.unknownModelLimit"), t);
        if (parsed.error) validationError = parsed.error;
        else update = { max_context_tokens: parsed.value };
        break;
      }
      case "maxSteps": {
        const parsed = parseOptionalInteger(draft, t("settings.maxSteps"), t);
        if (parsed.error) validationError = parsed.error;
        else update = { max_steps: parsed.value };
        break;
      }
      case "temperature": {
        const parsed = parseTemperatureDraft(draft, t);
        if (parsed.error) validationError = parsed.error;
        else update = { temperature: parsed.value };
        break;
      }
    }
    if (validationError || !update) {
      setAdvancedError(validationError);
      return;
    }
    setAdvancedError("");
    try {
      await onAdvancedSave(update);
      advancedCommittedRef.current[field] = draft;
    } catch (saveError) {
      setAdvancedError(saveError instanceof Error ? saveError.message : t("settings.saveFailed"));
    }
  }

  // The create-transaction submit is the only explicit action left: every
  // other field on the page applies instantly on commit.
  const addingCredentiallessProvider = addingProvider &&
    (isXAISubscriptionType(providerTypeDraft) || isGrokBuildType(providerTypeDraft));
  const addSubmitDisabled =
    running ||
    !providerDraft.trim() ||
    providerNameTaken ||
    !modelDraft.trim() ||
    !baseURLDraft.trim() ||
    (!addingCredentiallessProvider && !apiKeyDraft.trim());
  const shellStyle = {
    // Same variables as the main app shell (`--sidebar-width` collapses to 0,
    // `--sidebar-open-width` remembers the open width for the hover drawer)
    // so sidebar.css and settings.css read one vocabulary for both shells.
    "--sidebar-width": `${sidebarCollapsed ? 0 : sidebarWidth}px`,
    "--sidebar-open-width": `${sidebarWidth}px`,
    "--sidebar-motion-duration": `${sidebarMotionMs}ms`
  } as CSSProperties;

  // Mirror the main view's sidebar collapse/hover logic so the settings shell
  // behaves the same way: a persistent `sidebarCollapsed` flag hides the rail
  // and only the left-edge hover zone + the toggle button can reopen it as a
  // drawer overlay. Navigating between settings pages does not close it;
  // pointer exit and window-level dismissal remain the close signals.
  const fallbackShellRef = useRef<HTMLDivElement>(null);
  const effectiveShellRef = shellRef ?? fallbackShellRef;
  const {
    sidebarDrawerPhase,
    sidebarHoverZoneRef,
    scheduleSidebarDrawerOpen,
    cancelSidebarDrawerOpen,
    openSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave
  } = useSidebarDrawerState({
    appShellRef: effectiveShellRef,
    sidebarCollapsed,
    resizingSidebar,
    motionMs: SIDEBAR_DRAWER_EXIT_MS,
    dockingMotionMs: SIDEBAR_MOTION_MS,
    closeOnWindowResize: true
  });
  const shellClassName = `settings-shell${resizingSidebar ? " resizing-sidebar" : ""}${
    sidebarCollapsed ? " sidebar-collapsed" : ""
  }${
    sidebarCollapsed && sidebarDrawerPhase === "open" ? " sidebar-drawer-open" : ""
  }${
    sidebarCollapsed && sidebarDrawerPhase === "closing"
      ? " sidebar-drawer-closing"
      : ""
  }${
    !sidebarCollapsed && sidebarDrawerPhase === "docking"
      ? " sidebar-drawer-docking"
      : ""
  }${sidebarAnimating ? " sidebar-animating" : ""}`;

  const pageTitle = activePluginSettingsRecord?.name
    ?? activeCustomPluginPage?.title
    ?? settingsPageTitle(activePage, t);
  const availablePages = useMemo<readonly SettingsPageSummaryV1[]>(() => Object.freeze([
    Object.freeze({ id: "providers", label: settingsPageTitle("providers", t) }),
    Object.freeze({ id: "collaboration", label: settingsPageTitle("collaboration", t) }),
    Object.freeze({ id: "advanced", label: settingsPageTitle("advanced", t) }),
    Object.freeze({ id: "general", label: settingsPageTitle("general", t) }),
    ...(ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot")
      ? [Object.freeze({ id: "remote", label: settingsPageTitle("remote", t) })]
      : []),
    Object.freeze({ id: "usage", label: settingsPageTitle("usage", t) }),
    Object.freeze({ id: "archive", label: settingsPageTitle("archive", t) }),
    ...pluginSettingsRecords.map((plugin) => Object.freeze({
      id: pluginSettingsPageId(plugin.id),
      label: plugin.name,
    })),
    ...customPluginSettingsPages.map((entry) => Object.freeze({
      id: pluginViewSettingsPageId(entry.pluginId, entry.id),
      label: entry.title,
    })),
  ]), [customPluginSettingsPages, pluginSettingsRecords, t]);

  const nativeSettings = (
    <div ref={effectiveShellRef} className={shellClassName} style={shellStyle} data-wuu-component="settings-shell">
      <div
        ref={sidebarHoverZoneRef}
        className="sidebar-hover-zone"
        aria-hidden="true"
        onPointerEnter={scheduleSidebarDrawerOpen}
        onPointerLeave={cancelSidebarDrawerOpen}
      />
      <aside
        className="settings-sidebar"
        data-wuu-component="settings-sidebar"
        onPointerEnter={openSidebarDrawer}
        onPointerLeave={(event) =>
          scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
        }
      >
        {/*
          * 与主侧栏一致的内层 .sidebar-content：折叠动画期间列宽收窄时，
          * 内容保持 --sidebar-open-width 的固定宽度被裁切（而不是被压扁），
          * 淡出/位移也复用 sidebar.css 里同一组规则。
          */}
        <div className="sidebar-content">
          <div className="traffic-spacer" />
          <button className="settings-back-button" type="button" onClick={onBack}>
            <ArrowLeft className="icon" />
            <span>{t("settings.backToApp")}</span>
          </button>
          <nav
            className="settings-nav"
            data-wuu-component="settings-navigation"
            aria-label={t("settings.navigation")}
          >
            <div className="settings-nav-group">
              <div className="settings-nav-group-label">{t("settings.groupModel")}</div>
              <SettingsNavItem icon={<KeyRound className="icon-lg" />} active={activePage === "providers"} onClick={() => setActivePage("providers")}>
                {t("settings.providers")}
              </SettingsNavItem>
              <SettingsNavItem icon={<Hash className="icon-lg" />} active={activePage === "collaboration"} onClick={() => setActivePage("collaboration")}>
                {t("settings.collaboration")}
              </SettingsNavItem>
              <SettingsNavItem icon={<SlidersHorizontal className="icon-lg" />} active={activePage === "advanced"} onClick={() => setActivePage("advanced")}>
                {t("settings.advanced")}
              </SettingsNavItem>
            </div>
            <div className="settings-nav-group">
              <div className="settings-nav-group-label">{t("settings.groupApp")}</div>
              <SettingsNavItem icon={<Settings className="icon-lg" />} active={activePage === "general"} onClick={() => setActivePage("general")}>
                {t("settings.general")}
              </SettingsNavItem>
              {ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot") ? (
                <SettingsNavItem icon={<Smartphone className="icon-lg" />} active={activePage === "remote"} onClick={() => setActivePage("remote")}>
                  {t("settings.remote")}
                </SettingsNavItem>
              ) : null}
            </div>
            <div className="settings-nav-group">
              <div className="settings-nav-group-label">{t("settings.groupData")}</div>
              <SettingsNavItem icon={<BarChart3 className="icon-lg" />} active={activePage === "usage"} onClick={() => setActivePage("usage")}>
                {t("settings.usage")}
              </SettingsNavItem>
              <SettingsNavItem icon={<Archive className="icon-lg" />} active={activePage === "archive"} onClick={() => setActivePage("archive")}>
                {t("settings.archive")}
              </SettingsNavItem>
            </div>
            {pluginSettingsRecords.length > 0 || customPluginSettingsPages.length > 0 ? (
              <div
                className="settings-nav-group"
                data-wuu-component="plugin-settings-navigation"
              >
                <div className="settings-nav-group-label">{t("skills.sectionPlugins")}</div>
                {pluginSettingsRecords.map((plugin) => {
                  const pageId = pluginSettingsPageId(plugin.id);
                  return (
                    <SettingsNavItem
                      key={pageId}
                      icon={<Plug className="icon-lg" />}
                      active={activePage === pageId}
                      onClick={() => setActivePage(pageId)}
                    >
                      {plugin.name}
                    </SettingsNavItem>
                  );
                })}
                {customPluginSettingsPages.map((entry) => {
                  const pageId = pluginViewSettingsPageId(entry.pluginId, entry.id);
                  return (
                    <SettingsNavItem
                      key={pageId}
                      icon={<PluginIcon icon={entry.icon} pluginId={entry.pluginId} fingerprint={entry.generation} className="icon-lg" />}
                      active={activePage === pageId}
                      onClick={() => setActivePage(pageId)}
                    >
                      {entry.title}
                    </SettingsNavItem>
                  );
                })}
              </div>
            ) : null}
          </nav>
        </div>
      </aside>
      {sidebarCollapsed ? null : (
        <div
          className="sidebar-resizer"
          role="separator"
          aria-label={t("settings.resizeSidebar")}
          aria-orientation="vertical"
          aria-valuemin={sidebarMinWidth}
          aria-valuemax={sidebarMaxWidth}
          aria-valuenow={sidebarWidth}
          tabIndex={0}
          onPointerDown={onSidebarResizeStart}
          onDoubleClick={onToggleSidebar}
          onKeyDown={onSidebarSeparatorKey}
        />
      )}
      <main className="settings-main" data-wuu-component="settings-content">
        <div className="settings-titlebar">
          {isTouchWebShell() && <button type="button" className="settings-phone-back" aria-label={t("common.back")} onClick={onBack}><ArrowLeft size={22} /></button>}
          {/* The toggle must stay a descendant of the titlebar drag strip:
           * a no-drag element only gets carved out of a drag region when it
           * belongs to it — as a shell-level sibling positioned over the
           * strip it kept its DOM hit target but real clicks were swallowed
           * as window drags. The hover-drawer lift is shared with the
           * conversation titlebar via sidebar.css (z-index 150 over the
           * drawer's 140). */}
          <button
            type="button"
            className="icon-button side-panel-toggle-button sidebar-toggle-button settings-sidebar-toggle"
            data-wuu-component="sidebar-toggle"
            aria-label={sidebarCollapsed ? t("settings.expandSidebar") : t("settings.collapseSidebar")}
            aria-pressed={!sidebarCollapsed}
            onClick={onToggleSidebar}
            onPointerEnter={scheduleSidebarDrawerOpen}
            onPointerLeave={(event) =>
              scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
            }
          >
            <SidePanelToggleIcon side="left" open={!sidebarCollapsed} />
          </button>
        </div>
        <div ref={settingsScrollRef} className="settings-scroll">
          <div
            className={`settings-page${activePage === "archive" ? " settings-page-archive" : ""}${activePage === "providers" ? " settings-page-providers" : ""}`}
            data-wuu-component="settings-page"
            data-wuu-page={activePage}
            key={activePage}
          >
            <header className="settings-page-header">
              <h1 className="settings-page-title">{pageTitle}</h1>
            </header>
  
            {activePluginSettingsRecord ? (
              <PluginSettingsEditor plugin={activePluginSettingsRecord} variant="page" />
            ) : activeCustomPluginPage ? (
              <PluginViewContent
                controller={workbenchController}
                pluginId={activeCustomPluginPage.pluginId}
                viewTypeId={activeCustomPluginPage.view}
                context={Object.freeze({ surface: "settings" })}
                settings={settingsPageHost}
                onFailure={() => setActivePage("providers")}
              />
            ) : activePage === "providers" ? (
              <>
                <EngineSettingsSection
                  result={engineInventory}
                  loadError={engineInventoryError}
                  onRefresh={onRefreshEngineInventory}
                  onUpdate={onUpdateEngineInventory}
                />
                <SettingsProvidersPage
                  providers={providers}
                  providerLabels={providerLabels}
                  running={running}
                  providerDraft={providerDraft}
                  providerTypeDraft={providerTypeDraft}
                  modelDraft={modelDraft}
                  variantDraft={variantDraft}
                  baseURLDraft={baseURLDraft}
                  apiKeyDraft={apiKeyDraft}
                  addingProvider={addingProvider}
                  error={error}
                  selectedProvider={selectedProvider}
                  connectionLocked={connectionLocked}
                  variantOptions={variantOptions}
                  providerNameTaken={Boolean(providerNameTaken)}
                  onProviderChange={selectProvider}
                  onStartAddingProvider={startAddingProvider}
                  onCancelAddingProvider={cancelAddingProvider}
                  onProviderDraftChange={setProviderDraft}
                  onProviderTypeDraftChange={changeProviderTypeDraft}
                  onModelDraftChange={(value) => {
                    setModelDraft(value);
                    setVariantDraft("");
                  }}
                  onVariantDraftChange={changeVariant}
                  onBaseURLDraftChange={setBaseURLDraft}
                  onAPIKeyDraftChange={setAPIKeyDraft}
                  onCommitModel={commitModelName}
                  onRemoveModel={removeModel}
                  onCommitBaseURL={commitBaseURL}
                  onCommitAPIKey={commitAPIKey}
                  onSubmit={submit}
                  onRemoveProvider={requestRemoveProvider}
                  onRefreshModelCatalog={onRefreshModelCatalog}
                  runningProviderNames={runningProviderNameSet}
                  disabled={addSubmitDisabled}
                  xaiLogin={xaiLogin}
                  xaiLoginBusy={xaiLoginBusy}
                  onStartXAILogin={() => void startXAILogin()}
                />
              </>
            ) : activePage === "collaboration" ? (
              <SettingsCollaborationPage
                initialized={initialized}
                running={running}
                onSave={onAdvancedSave}
              />
            ) : activePage === "advanced" ? (
              <>
                <SettingsAdvancedPage
                  initialized={initialized}
                  running={running}
                  autoCompact={autoCompactDraft}
                  compactThreshold={compactThresholdDraft}
                  compactKeepRecent={compactKeepRecentDraft}
                  providerContextWindow={providerContextWindowDraft}
                  providerContextWindowCurrent={formatOptionalTokenCount(
                    initialized?.advanced_settings?.context_window_tokens,
                  )}
                  providerContextWindowSource={advancedContextSourceLabel(
                    initialized?.advanced_settings?.context_window_source,
                    t,
                  )}
                  maxContextTokens={maxContextTokensDraft}
                  maxSteps={maxStepsDraft}
                  temperature={temperatureDraft}
                  error={advancedError}
                  onAutoCompactToggle={toggleAutoCompact}
                  onCompactThresholdChange={setCompactThresholdDraft}
                  onCompactKeepRecentChange={setCompactKeepRecentDraft}
                  onProviderContextWindowChange={setProviderContextWindowDraft}
                  onMaxContextTokensChange={setMaxContextTokensDraft}
                  onMaxStepsChange={setMaxStepsDraft}
                  onTemperatureChange={setTemperatureDraft}
                  onCommitField={commitAdvancedField}
                />
              </>
            ) : activePage === "general" ? (
              <SettingsGeneralPage
                initialized={initialized}
                running={running}
                desktopBuild={desktopBuild}
                mcpServers={mcpServers}
                mcpLoading={mcpLoading}
                mcpError={mcpError}
                mcpBusyServer={mcpBusyServer}
                codexPets={codexPets}
                codexPetsLoading={codexPetsLoading}
                codexPetsError={codexPetsError}
                onGeneralSave={onGeneralSave}
                onMCPAction={runMCPAction}
                onMCPAuthStart={startMCPAuth}
                onMCPAuthFinish={finishMCPAuth}
                onMCPAuthRemove={removeMCPAuth}
                onCodexPetsRefresh={onCodexPetsRefresh}
                onCodexPetsUpdate={onCodexPetsUpdate}
                copyState={copyState}
                onCopyVersion={copyVersionInfo}
              />
            ) : activePage === "remote" && ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot") ? (
              <SettingsRemotePageContainer />
            ) : activePage === "archive" ? (
              <SettingsArchivePage
                archivedThreads={archivedThreads ?? []}
                archivedRooms={archivedRooms ?? []}
                onUnarchiveThread={onUnarchiveThread}
                onUnarchiveRoom={onUnarchiveRoom ?? (() => {})}
              />
            ) : (
              <SettingsUsagePage
                usage={usage}
                loading={usageLoading}
                error={usageError}
              />
            )}
          </div>
        </div>
      </main>
    </div>
  );
  return (
    <SettingsPresentation
      initialized={initialized}
      activePageId={activePage}
      availablePages={availablePages}
      runningProviderNames={runningProviderNames}
      busy={running || usageLoading || mcpLoading || codexPetsLoading || Boolean(mcpBusyServer)}
      hasError={Boolean(error || advancedError || usageError || mcpError || codexPetsError)}
      fallback={nativeSettings}
      onOpenPage={(pageId) => setActivePage(pageId as SettingsPage)}
      onAdvancedSave={onAdvancedSave}
      onGeneralSave={onGeneralSave}
      onRefresh={onRefreshModelCatalog}
    />
  );
}

/* -------------------------------------------------------------------------- */
/*  Shared primitives                                                          */
/* -------------------------------------------------------------------------- */

function SettingsNavItem({
  icon,
  active,
  onClick,
  children
}: {
  icon: ReactNode;
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}): JSX.Element {
  return (
    <button
      className={`settings-nav-item${active ? " active" : ""}`}
      data-wuu-component="settings-navigation-item"
      type="button"
      aria-current={active ? "page" : undefined}
      onClick={onClick}
    >
      {icon}
      <span>{children}</span>
    </button>
  );
}

// 小节只有一个安静的小标签；描述性文字一律省略——语义由行本身携带,
// 只在个别行的 description 里保留单位、约束或禁用原因。
function SettingsSection({
  title,
  description,
  testID,
  children
}: {
  title?: string;
  description?: string;
  testID?: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <section
      className="settings-section"
      data-wuu-component="settings-section"
      {...(testID ? { "data-testid": testID } : {})}
    >
      {title || (description && !isTouchWebShell()) ? (
        <header className="settings-section-header">
          {title ? <h2 className="settings-section-title">{title}</h2> : null}
          {description && !isTouchWebShell() ? <p className="settings-section-description">{description}</p> : null}
        </header>
      ) : null}
      {children}
    </section>
  );
}

function SettingsCard({ children }: { children: ReactNode }): JSX.Element {
  return <div className="settings-group" data-wuu-component="settings-group">{children}</div>;
}




/* -------------------------------------------------------------------------- */
/*  Providers page                                                             */
/* -------------------------------------------------------------------------- */

function SettingsProvidersPage({
  providers,
  providerLabels,
  running,
  providerDraft,
  providerTypeDraft,
  modelDraft,
  variantDraft,
  baseURLDraft,
  apiKeyDraft,
  addingProvider,
  error,
  selectedProvider,
  connectionLocked,
  variantOptions,
  providerNameTaken,
  onProviderChange,
  onStartAddingProvider,
  onCancelAddingProvider,
  onProviderDraftChange,
  onProviderTypeDraftChange,
  onModelDraftChange,
  onVariantDraftChange,
  onBaseURLDraftChange,
  onAPIKeyDraftChange,
  onCommitModel,
  onRemoveModel,
  onCommitBaseURL,
  onCommitAPIKey,
  onSubmit,
  onRemoveProvider,
  onRefreshModelCatalog,
  runningProviderNames,
  disabled,
  xaiLogin,
  xaiLoginBusy,
  onStartXAILogin
}: {
  providers: ProviderSummary[];
  providerLabels: Map<string, string>;
  running: boolean;
  providerDraft: string;
  providerTypeDraft: string;
  modelDraft: string;
  variantDraft: string;
  baseURLDraft: string;
  apiKeyDraft: string;
  addingProvider: boolean;
  error: string;
  selectedProvider: ProviderSummary | undefined;
  connectionLocked: boolean;
  variantOptions: string[];
  providerNameTaken: boolean;
  onProviderChange: (provider: string) => void;
  onStartAddingProvider: () => void;
  onCancelAddingProvider: () => void;
  onProviderDraftChange: (value: string) => void;
  onProviderTypeDraftChange: (value: string) => void;
  onModelDraftChange: (value: string) => void;
  onVariantDraftChange: (value: string) => void;
  onBaseURLDraftChange: (value: string) => void;
  onAPIKeyDraftChange: (value: string) => void;
  onCommitModel: (selection?: string) => void;
  onRemoveModel: (model: string) => Promise<void>;
  onCommitBaseURL: () => void;
  onCommitAPIKey: () => void;
  onSubmit: (event: ReactFormEvent<HTMLFormElement>) => Promise<void>;
  onRemoveProvider?: (provider: string) => Promise<void> | void;
  onRefreshModelCatalog: () => Promise<void>;
  runningProviderNames: ReadonlySet<string>;
  disabled: boolean;
  xaiLogin: { loginId: string; userCode: string; url: string } | null;
  xaiLoginBusy: boolean;
  onStartXAILogin: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [catalogRefreshing, setCatalogRefreshing] = useState(false);
  const [removingModel, setRemovingModel] = useState(false);
  function modelIDs(provider: ProviderSummary | undefined): string[] {
    return [...new Set([provider?.model ?? "", ...(provider?.models ?? []).map((model) => model.id)].filter(Boolean))];
  }
  const reasoningMode = providerModelReasoningMode(selectedProvider, modelDraft);
  const authFieldLabel = t("provider.apiKey");
  const xaiType = addingProvider
    ? isXAISubscriptionType(providerTypeDraft)
    : isXAISubscriptionType(selectedProvider?.type);
  const grokBuildType = addingProvider
    ? isGrokBuildType(providerTypeDraft)
    : isGrokBuildType(selectedProvider?.type);
  const oauthLocked = connectionLocked || xaiType || grokBuildType;
  // Text fields commit on blur, or on Enter — except while creating a
  // provider, where Enter submits the create transaction instead.
  const commitOnEnter =
    (commit: () => void) =>
    (event: ReactKeyboardEvent<HTMLInputElement>): void => {
      if (event.key !== "Enter" || addingProvider) {
        return;
      }
      event.preventDefault();
      commit();
      event.currentTarget.blur();
    };
  return (
    <SettingsSection testID="settings-providers">
      <header className="settings-services-header">
        <div>
          <h2 className="settings-section-title">{t("settings.providerServices")}</h2>
        </div>
        <div className="settings-services-actions">
          <button
            className="settings-button settings-button-ghost"
            type="button"
            data-testid="settings-model-catalog-refresh"
            disabled={catalogRefreshing}
            onClick={() => {
              setCatalogRefreshing(true);
              void onRefreshModelCatalog().finally(() => setCatalogRefreshing(false));
            }}
          >
            <RefreshCw className={`icon${catalogRefreshing ? " settings-spin" : ""}`} />
            {catalogRefreshing ? t("settings.modelCatalogUpdating") : t("settings.modelCatalogUpdate")}
          </button>
          <button
            className="settings-button"
            type="button"
            data-testid="settings-provider-add-card"
            disabled={running || addingProvider}
            onClick={onStartAddingProvider}
          >
            <Plus className="icon" />
            {t("provider.add")}
          </button>
        </div>
      </header>
      <div className="settings-services-layout">
      {providers.length > 0 ? (
        <div className="settings-provider-overview" data-testid="settings-provider-overview" role="group" aria-label={t("settings.providerServices")}>
          {providers.map((provider) => (
            <div className="settings-provider-card" key={provider.name}>
              <button
                className={`settings-provider-button${!addingProvider && providerDraft === provider.name ? " active" : ""}`}
                type="button"
                aria-pressed={!addingProvider && providerDraft === provider.name}
                disabled={running}
                onClick={() => onProviderChange(provider.name)}
              >
                <span className="settings-provider-copy">
                  <strong>{providerLabels.get(provider.name) ?? providerServiceLabel(provider, t)}</strong>
                  <small>{t("provider.modelCount", { count: modelIDs(provider).length })} · {provider.model ? t("provider.selectedModel", { model: provider.model }) : t("provider.noModel")}</small>
                </span>
              </button>
              {onRemoveProvider && !provider.auto_discovered && (!provider.connection_locked || isXAISubscriptionType(provider.type) || isGrokBuildType(provider.type)) ? (
                <button
                  className="settings-provider-remove"
                  type="button"
                  aria-label={t("provider.removeNamed", { name: providerServiceLabel(provider, t) })}
                  title={t("provider.removeTitle")}
                  disabled={running}
                  onClick={(event) => {
                    event.stopPropagation();
                    if (runningProviderNames.has(provider.name.trim())) {
                      window.alert(t("provider.inUse"));
                      return;
                    }
                    if (
                      typeof window !== "undefined" &&
                      typeof window.confirm === "function" &&
                      !window.confirm(
                        t("provider.removeConfirm", { name: providerServiceLabel(provider, t) }),
                      )
                    ) {
                      return;
                    }
                    void onRemoveProvider(provider.name);
                  }}
                >
                  <X className="icon" />
                </button>
              ) : null}
            </div>
          ))}
        </div>
      ) : <p className="settings-muted-line">{t("provider.none")}</p>}
      <form className="settings-provider-editor" onSubmit={onSubmit}>
        <header className="settings-provider-editor-header">
          <div>
            <h3>{addingProvider ? t("provider.add") : selectedProvider ? providerServiceLabel(selectedProvider, t) : t("provider.none")}</h3>
            {selectedProvider && !addingProvider ? <p>{providerConnectionStatus(selectedProvider, t)}</p> : null}
          </div>
        </header>
        {addingProvider ? (
          <SettingsRow title={t("provider.type")}>
            <SelectMenu
              triggerClassName="settings-select-trigger"
              ariaLabel={t("provider.type")}
              dataTestid="settings-provider-type-select"
              value={providerTypeDraft}
              onChange={onProviderTypeDraftChange}
              disabled={running}
              options={[
                { value: "openai-compatible", label: t("provider.openaiCompatible") },
                { value: "anthropic", label: t("provider.anthropicCompatible") },
                { value: "xai-subscription", label: t("provider.xaiSubscription") },
                { value: "grok-build", label: t("provider.grokBuild") }
              ]}
            />
          </SettingsRow>
        ) : null}
        {addingProvider ? (
          <SettingsRow
            title={t("provider.identifier")}
            description={providerNameTaken ? t("provider.nameExists") : undefined}
          >
            <input
              className="settings-input"
              aria-label={t("provider.identifier")}
              value={providerDraft}
              onChange={(event) => onProviderDraftChange(event.target.value)}
              disabled={running}
            />
          </SettingsRow>
        ) : null}
        <section className="settings-provider-form-section">
          {!addingProvider && modelIDs(selectedProvider).length > 0 && <SettingsRow title={t("provider.availableModels")} block>
            <div className="settings-provider-model-list" role="group" aria-label={t("provider.availableModels")}>
              {modelIDs(selectedProvider).map((model) => <span key={model} className="settings-provider-model-tag"><button
                type="button"
                className="settings-button"
                aria-pressed={modelDraft === model}
                disabled={running || removingModel}
                onClick={() => onCommitModel(model)}
              >{model}</button><button
                type="button"
                className="settings-provider-model-remove"
                aria-label={t("provider.removeModel", { model })}
                title={modelIDs(selectedProvider).length < 2 ? t("provider.keepOneModel") : t("provider.removeModel", { model })}
                disabled={running || removingModel || modelIDs(selectedProvider).length < 2}
                onClick={() => {
                  setRemovingModel(true);
                  void onRemoveModel(model).finally(() => setRemovingModel(false));
                }}
              ><X className="icon" /></button></span>)}
            </div>
          </SettingsRow>}
          <div className="settings-provider-model-fields">
        <SettingsRow title={t("provider.modelName")} block>
          <input
            className="settings-input"
            aria-label={t("provider.modelName")}
            value={modelDraft}
            onChange={(event) => onModelDraftChange(event.target.value)}
            onBlur={() => onCommitModel()}
            onKeyDown={commitOnEnter(onCommitModel)}
            disabled={running}
          />
        </SettingsRow>
        <SettingsRow
          title={t("provider.reasoningEffort")}
          description={reasoningMode === "off" ? t("provider.reasoningUnsupported") : undefined}
          block
        >
          <SelectMenu
            triggerClassName="settings-select-trigger"
            ariaLabel={t("provider.reasoningEffort")}
            value={variantDraft}
            onChange={onVariantDraftChange}
            disabled={running || reasoningMode === "off"}
            options={variantOptions.map((variant) => ({
              value: variant,
              label: variantLabel(variant)
            }))}
          />
        </SettingsRow>
          </div>
        </section>
        <section className="settings-provider-form-section">
        <SettingsRow
          title={t("settings.baseURL")}
          block
        >
          {oauthLocked ? (
            <span className="settings-managed-value">{baseURLDraft || (xaiType ? t("provider.xaiOAuthManaged") : grokBuildType ? t("provider.grokBuildManaged") : t("provider.oauthManaged"))}</span>
          ) : (
          <input
            className="settings-input"
            aria-label={t("settings.baseURL")}
            value={baseURLDraft}
            placeholder={oauthLocked ? (xaiType ? t("provider.xaiOAuthManaged") : grokBuildType ? t("provider.grokBuildManaged") : t("provider.oauthManaged")) : "https://api.openai.com/v1"}
            onChange={(event) => onBaseURLDraftChange(event.target.value)}
            onBlur={() => onCommitBaseURL()}
            onKeyDown={commitOnEnter(onCommitBaseURL)}
            disabled={running || oauthLocked}
          />
          )}
        </SettingsRow>
        {xaiType ? (
          <SettingsRow title={t("provider.xaiLogin")} hint={t("provider.xaiLoginHint")} block>
            <div className="settings-xai-login">
              <button
                className="settings-button settings-button-primary"
                type="button"
                onClick={onStartXAILogin}
                disabled={running || xaiLoginBusy}
              >
                {xaiLoginBusy ? t("provider.xaiLoggingIn") : selectedProvider?.api_key_configured ? t("provider.xaiLoggedIn") : t("provider.xaiLogin")}
              </button>
              {xaiLogin ? (
                <p className="settings-hint">
                  {t("provider.xaiLoginCode", { code: xaiLogin.userCode })}
                </p>
              ) : null}
            </div>
          </SettingsRow>
        ) : grokBuildType ? (
          (addingProvider || !selectedProvider?.api_key_configured) && !isTouchWebShell() ? (
            <p className="settings-hint">{t("provider.grokBuildLoginHint")}</p>
          ) : null
        ) : connectionLocked ? null : (
        <SettingsRow title={authFieldLabel} block>
          <input
            className="settings-input"
            aria-label={authFieldLabel}
            value={apiKeyDraft}
            type="password"
            autoComplete="new-password"
            placeholder={
              connectionLocked
                ? t("provider.authNotNeeded", { field: authFieldLabel })
                : addingProvider
                  ? t("provider.enterAuth", { field: authFieldLabel })
                  : selectedProvider?.api_key_configured
                    ? t("provider.keepCurrentAuth")
                    : t("provider.enterAuth", { field: authFieldLabel })
            }
            onChange={(event) => onAPIKeyDraftChange(event.target.value)}
            onBlur={() => onCommitAPIKey()}
            onKeyDown={commitOnEnter(onCommitAPIKey)}
            disabled={running || connectionLocked}
          />
        </SettingsRow>
        )}
        </section>
        {addingProvider ? (
          <div className="settings-row settings-row-footer">
            {error ? <div className="settings-error">{error}</div> : null}
            <button
              className="settings-button settings-button-ghost"
              type="button"
              onClick={onCancelAddingProvider}
              disabled={running}
            >
              {t("common.cancel")}
            </button>
            <button
              className="settings-button settings-button-primary"
              type="submit"
              disabled={disabled}
            >
              {t("provider.addAction")}
            </button>
          </div>
        ) : error ? (
          <div className="settings-row settings-row-footer">
            <div className="settings-error">{error}</div>
          </div>
        ) : null}
      </form>
      </div>
    </SettingsSection>
  );
}

/* -------------------------------------------------------------------------- */
/*  Advanced page                                                              */
/* -------------------------------------------------------------------------- */

type AdvancedNumericField =
  | "compactThreshold"
  | "compactKeepRecent"
  | "providerContextWindow"
  | "maxContextTokens"
  | "maxSteps"
  | "temperature";

function SettingsCollaborationPage({
  initialized,
  running,
  onSave,
}: {
  initialized?: InitializeResult;
  running: boolean;
  onSave: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const options = useMemo(() => {
    const result = [{ value: "", label: t("settings.collaborationInheritDefault") }];
    for (const provider of initialized?.providers ?? []) {
      const models = provider.models?.length
        ? provider.models
        : [{ id: provider.model, display_name: provider.model }];
      for (const model of models) {
        if (!model.id) continue;
        result.push({
          value: `${provider.name}\u0000${model.id}`,
          label: `${provider.name} · ${model.display_name || model.id}`,
        });
      }
    }
    return result;
  }, [initialized?.providers, t]);
  const roleValue = (name: string): string => {
    const role = initialized?.model_roles?.find((candidate) => candidate.role === name);
    return role && !role.inherited ? `${role.provider}\u0000${role.model}` : "";
  };
  const saveRole = (field: "coordination_model" | "verification_model", value: string): void => {
    const [provider = "", model = ""] = value.split("\u0000");
    void onSave({ [field]: { provider, model } });
  };
  return (
    <SettingsSection testID="settings-collaboration">
      <SettingsCard>
        <SettingsRow
          title={t("settings.coordinationModel")}
          hint={t("settings.coordinationModelDescription")}
        >
          <SelectMenu
            triggerClassName="settings-select-trigger"
            ariaLabel={t("settings.coordinationModel")}
            value={roleValue("coordination")}
            options={options}
            disabled={running || !initialized}
            onChange={(value) => saveRole("coordination_model", value)}
          />
        </SettingsRow>
        <SettingsRow
          title={t("settings.verificationModel")}
          hint={t("settings.verificationModelDescription")}
        >
          <SelectMenu
            triggerClassName="settings-select-trigger"
            ariaLabel={t("settings.verificationModel")}
            value={roleValue("verification")}
            options={options}
            disabled={running || !initialized}
            onChange={(value) => saveRole("verification_model", value)}
          />
        </SettingsRow>
      </SettingsCard>
    </SettingsSection>
  );
}

function SettingsAdvancedPage({
  initialized,
  running,
  autoCompact,
  compactThreshold,
  compactKeepRecent,
  providerContextWindow,
  providerContextWindowCurrent,
  providerContextWindowSource,
  maxContextTokens,
  maxSteps,
  temperature,
  error,
  onAutoCompactToggle,
  onCompactThresholdChange,
  onCompactKeepRecentChange,
  onProviderContextWindowChange,
  onMaxContextTokensChange,
  onMaxStepsChange,
  onTemperatureChange,
  onCommitField
}: {
  initialized: InitializeResult | undefined;
  running: boolean;
  autoCompact: boolean;
  compactThreshold: string;
  compactKeepRecent: string;
  providerContextWindow: string;
  providerContextWindowCurrent: string;
  providerContextWindowSource: string;
  maxContextTokens: string;
  maxSteps: string;
  temperature: string;
  error: string;
  onAutoCompactToggle: () => void;
  onCompactThresholdChange: (value: string) => void;
  onCompactKeepRecentChange: (value: string) => void;
  onProviderContextWindowChange: (value: string) => void;
  onMaxContextTokensChange: (value: string) => void;
  onMaxStepsChange: (value: string) => void;
  onTemperatureChange: (value: string) => void;
  onCommitField: (field: AdvancedNumericField) => void;
}): JSX.Element {
  const { t } = useI18n();
  // Enter and blur both commit through onCommitField; the ref-guard inside
  // makes the blur that follows Enter a no-op, so there is one effective
  // commit per edit.
  const commitOnEnter =
    (field: AdvancedNumericField) =>
    (event: ReactKeyboardEvent<HTMLInputElement>): void => {
      if (event.key === "Enter") {
        onCommitField(field);
        event.currentTarget.blur();
      }
    };
  return (
    <SettingsSection testID="settings-advanced">
      <div className="settings-group">
        <SettingsRow
          title={t("settings.autoCompact")}
          hint={t("settings.autoCompactDescription")}
        >
          <button
            className="settings-switch"
            type="button"
            role="switch"
            aria-checked={autoCompact}
            disabled={running || !initialized}
            onClick={onAutoCompactToggle}
          >
            <span className="settings-switch-thumb" aria-hidden="true" />
            <span className="sr-only">{autoCompact ? t("settings.disableAutoCompact") : t("settings.enableAutoCompact")}</span>
          </button>
        </SettingsRow>
        <SettingsRow
          title={t("settings.compactThreshold")}
          hint={t("settings.compactThresholdDescription")}
        >
          <input
            className="settings-input settings-input-num"
            value={compactThreshold}
            inputMode="numeric"
            placeholder={t("settings.automatic")}
            onChange={(event) => onCompactThresholdChange(event.target.value)}
            onBlur={() => onCommitField("compactThreshold")}
            onKeyDown={commitOnEnter("compactThreshold")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        <SettingsRow
          title={t("settings.keepRecentContext")}
          hint={t("settings.keepRecentContextDescription")}
        >
          <input
            className="settings-input settings-input-num"
            value={compactKeepRecent}
            inputMode="numeric"
            placeholder="20,000"
            onChange={(event) => onCompactKeepRecentChange(event.target.value)}
            onBlur={() => onCommitField("compactKeepRecent")}
            onKeyDown={commitOnEnter("compactKeepRecent")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        <SettingsRow
          title={t("settings.providerContextLimit")}
          description={`${providerContextWindowSource}${
            providerContextWindowCurrent ? `；${t("settings.currentTokenLimit", { count: providerContextWindowCurrent })}` : ""
          }`}
        >
          <input
            className="settings-input settings-input-num"
            value={providerContextWindow}
            inputMode="numeric"
            placeholder={t("settings.detectAutomatically")}
            onChange={(event) => onProviderContextWindowChange(event.target.value)}
            onBlur={() => onCommitField("providerContextWindow")}
            onKeyDown={commitOnEnter("providerContextWindow")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        <SettingsRow
          title={t("settings.unknownModelLimit")}
          hint={t("settings.unknownModelLimitDescription")}
        >
          <input
            className="settings-input settings-input-num"
            value={maxContextTokens}
            inputMode="numeric"
            placeholder={t("settings.automatic")}
            onChange={(event) => onMaxContextTokensChange(event.target.value)}
            onBlur={() => onCommitField("maxContextTokens")}
            onKeyDown={commitOnEnter("maxContextTokens")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        <SettingsRow
          title={t("settings.maxSteps")}
          hint={t("settings.unlimitedAtZero")}
        >
          <input
            className="settings-input settings-input-num"
            value={maxSteps}
            inputMode="numeric"
            onChange={(event) => onMaxStepsChange(event.target.value)}
            onBlur={() => onCommitField("maxSteps")}
            onKeyDown={commitOnEnter("maxSteps")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        <SettingsRow title={t("settings.temperature")} hint={t("settings.temperatureRange")}>
          <input
            className="settings-input settings-input-num"
            value={temperature}
            inputMode="decimal"
            placeholder={t("settings.automatic")}
            onChange={(event) => onTemperatureChange(event.target.value)}
            onBlur={() => onCommitField("temperature")}
            onKeyDown={commitOnEnter("temperature")}
            disabled={running || !initialized}
          />
        </SettingsRow>
        {error ? (
          <div className="settings-row settings-row-footer">
            <div className="settings-error">{error}</div>
          </div>
        ) : null}
      </div>
    </SettingsSection>
  );
}

/* -------------------------------------------------------------------------- */
/*  General page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsGeneralPage({
  initialized,
  running,
  desktopBuild,
  mcpServers,
  mcpLoading,
  mcpError,
  mcpBusyServer,
  codexPets,
  codexPetsLoading,
  codexPetsError,
  onGeneralSave,
  onMCPAction,
  onMCPAuthStart,
  onMCPAuthFinish,
  onMCPAuthRemove,
  onCodexPetsRefresh,
  onCodexPetsUpdate,
  copyState,
  onCopyVersion
}: {
  initialized: InitializeResult | undefined;
  running: boolean;
  desktopBuild: DesktopBuildInfo | undefined;
  mcpServers: MCPServerStatus[];
  mcpLoading: boolean;
  mcpError: string;
  mcpBusyServer: string;
  codexPets: CodexPetsSnapshot | undefined;
  codexPetsLoading: boolean;
  codexPetsError: string;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onMCPAction: (name: string, action: "connect" | "disconnect" | "refresh") => Promise<void>;
  onMCPAuthStart: (name: string) => Promise<MCPAuthStartResult | undefined>;
  onMCPAuthFinish: (name: string, state: string, code: string) => Promise<boolean>;
  onMCPAuthRemove: (name: string) => Promise<void>;
  onCodexPetsRefresh: () => Promise<CodexPetsSnapshot>;
  onCodexPetsUpdate: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
  copyState: CopyState;
  onCopyVersion: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const generalSettings = initialized?.general_settings;
  const configuredMCPEnabled = generalSettings?.mcp_server_enabled ?? {};
  const configuredMCPKey = stableBoolRecordSignature(configuredMCPEnabled);
  const [mcpEnabledDraft, setMCPEnabledDraft] = useState<Record<string, boolean>>(() => ({ ...configuredMCPEnabled }));
  const [mcpToggleBusy, setMCPToggleBusy] = useState("");
  const [mcpToggleError, setMCPToggleError] = useState("");
  const [mcpAuthStates, setMCPAuthStates] = useState<Record<string, string>>({});
  const [mcpAuthCodes, setMCPAuthCodes] = useState<Record<string, string>>({});
  const [codexPetBusy, setCodexPetBusy] = useState(false);
  const [codexPetLocalError, setCodexPetLocalError] = useState("");
  const [gitAttributionBusy, setGitAttributionBusy] = useState(false);
  const [gitAttributionError, setGitAttributionError] = useState("");

  useEffect(() => {
    setMCPEnabledDraft({ ...configuredMCPEnabled });
  }, [configuredMCPKey]);

  async function toggleMCPServer(name: string, enabled: boolean): Promise<void> {
    const previous = mcpEnabledDraft;
    const next = { ...previous, [name]: enabled };
    setMCPEnabledDraft(next);
    setMCPToggleBusy(name);
    setMCPToggleError("");
    try {
      await onGeneralSave({ mcp_enabled_toggles: next });
    } catch (toggleError) {
      setMCPEnabledDraft(previous);
      setMCPToggleError(toggleError instanceof Error ? toggleError.message : t("settings.saveFailed"));
    } finally {
      setMCPToggleBusy("");
    }
  }

  async function toggleGitAttribution(): Promise<void> {
    if (!initialized || gitAttributionBusy) {
      return;
    }
    setGitAttributionBusy(true);
    setGitAttributionError("");
    try {
      await onGeneralSave({
        git_attribution_enabled: !gitAttributionEnabled,
      });
    } catch (error) {
      setGitAttributionError(
        error instanceof Error ? error.message : t("settings.saveGitAttributionFailed"),
      );
    } finally {
      setGitAttributionBusy(false);
    }
  }

  async function beginMCPAuth(name: string): Promise<void> {
    const result = await onMCPAuthStart(name);
    if (!result) {
      return;
    }
    setMCPAuthStates((states) => ({ ...states, [name]: result.state }));
    setMCPAuthCodes((codes) => ({ ...codes, [name]: "" }));
  }

  async function completeMCPAuth(name: string): Promise<void> {
    const state = mcpAuthStates[name]?.trim() ?? "";
    const code = mcpAuthCodes[name]?.trim() ?? "";
    if (!state || !code) {
      return;
    }
    if (await onMCPAuthFinish(name, state, code)) {
      setMCPAuthStates((states) => withoutRecordKey(states, name));
      setMCPAuthCodes((codes) => withoutRecordKey(codes, name));
    }
  }

  const mcpServerByName = new Map(mcpServers.map((server) => [server.name, server]));
  const mcpRowNames = Array.from(
    new Set([...mcpServers.map((server) => server.name), ...Object.keys(mcpEnabledDraft)]),
  ).sort((a, b) => a.localeCompare(b));
  const codexPetOptions = codexPets?.pets ?? [];
  const codexPetSelectedID = codexPets?.selected_id ?? "";
  const codexPetEnabled = Boolean(codexPets?.enabled);
  const codexPetStatus = codexPetLocalError || codexPetsError;
  const gitAttributionEnabled = generalSettings?.git_attribution_enabled ?? true;

  async function refreshCodexPets(): Promise<void> {
    setCodexPetBusy(true);
    setCodexPetLocalError("");
    try {
      await onCodexPetsRefresh();
    } catch (error) {
      setCodexPetLocalError(error instanceof Error ? error.message : t("settings.refreshFailed"));
    } finally {
      setCodexPetBusy(false);
    }
  }

  async function updateCodexPets(settings: CodexPetSettingsUpdate): Promise<void> {
    setCodexPetBusy(true);
    setCodexPetLocalError("");
    try {
      await onCodexPetsUpdate(settings);
    } catch (error) {
      setCodexPetLocalError(error instanceof Error ? error.message : t("settings.saveFailed"));
    } finally {
      setCodexPetBusy(false);
    }
  }

  return (
    <>
      <SettingsSection title={t("settings.appearance")} testID="settings-appearance">
        <div className="settings-theme-field">
          <span className="settings-row-label-title">{t("settings.theme")}</span>
          <ThemePreferenceControl />
        </div>
      </SettingsSection>
      <SettingsSection title={t("settings.typography")} testID="settings-typography">
        <SettingsCard><AppearanceTypography /></SettingsCard>
      </SettingsSection>
      <SettingsSection title={t("settings.fonts")} testID="settings-fonts">
        <SettingsCard><AppearanceTypography section="fonts" /></SettingsCard>
      </SettingsSection>
      <SettingsSection title={t("settings.personalization")} testID="settings-personalization">
        <SettingsCard>
          <AppearanceTypography section="motion" />
          <SettingsRow title={t("settings.language")}>
            <LanguagePreferenceControl />
          </SettingsRow>
          {hostSupports("listCodexPets") ? <>
          <SettingsRow
            title={t("settings.codexPet")}
            description={isTouchWebShell() ? undefined : t("settings.petSource", { path: codexPets?.home ?? "~/.wuu/pets" })}
          >
            {codexPetOptions.length > 0 ? (
              <SelectMenu
                className="settings-codex-pet-select"
                triggerClassName="settings-select-trigger"
                ariaLabel={t("settings.selectPet")}
                dataTestid="settings-codex-pet-select"
                value={codexPetSelectedID}
                disabled={codexPetsLoading || codexPetBusy || !codexPetEnabled}
                onChange={(next) => void updateCodexPets({ selected_id: next })}
                options={codexPetOptions.map((pet) => ({
                  value: pet.id,
                  label: pet.display_name
                }))}
              />
            ) : (
              <span className="settings-inline-flag">{t("settings.noLocalPets")}</span>
            )}
            <button
              className="settings-button settings-icon-button"
              type="button"
              title={t("settings.refreshPets")}
              aria-label={t("settings.refreshPets")}
              disabled={codexPetsLoading || codexPetBusy}
              onClick={() => void refreshCodexPets()}
            >
              <RefreshCw size={15} aria-hidden="true" />
            </button>
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-checked={codexPetEnabled}
              data-testid="settings-codex-pet-enabled"
              disabled={codexPetsLoading || codexPetBusy || codexPetOptions.length === 0}
              onClick={() => void updateCodexPets({ enabled: !codexPetEnabled })}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
              <span className="sr-only">{codexPetEnabled ? t("settings.disablePet") : t("settings.enablePet")}</span>
            </button>
          </SettingsRow>
          {codexPetsLoading ||
          (!codexPetsLoading && codexPetOptions.length === 0) ||
          codexPets?.errors.length ||
          codexPetStatus ? (
            <div className="settings-row settings-row-block">
              {codexPetsLoading ? <small className="settings-muted-line">{t("settings.loadingPets")}</small> : null}
              {!codexPetsLoading && codexPetOptions.length === 0 ? (
                <small className="settings-muted-line">
                  {t("settings.petInstallHint")}
                </small>
              ) : null}
              {codexPets?.errors.length ? (
                <small className="settings-muted-line settings-error">
                  {codexPets.errors[0]}
                </small>
              ) : null}
              {codexPetStatus ? <small className="settings-muted-line settings-error">{codexPetStatus}</small> : null}
            </div>
          ) : null}
          </> : null}
        </SettingsCard>
      </SettingsSection>

      {ENABLE_VOICE_INPUT && hostSupports("startSpeechRecognition") ? (
        <VoiceInputSettingsSection
          polishAvailable={Boolean(
            initialized && initialized.status !== "needs_setup",
          )}
        />
      ) : null}

      <SettingsSection title={t("settings.behavior")} testID="settings-general">
        <SettingsCard>
          <SettingsRow
            title={t("settings.gitAttribution")}
            hint={t("settings.gitAttributionDescription")}
          >
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-checked={gitAttributionEnabled}
              data-testid="settings-git-attribution"
              disabled={running || !initialized || gitAttributionBusy}
              onClick={() => void toggleGitAttribution()}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
              <span className="sr-only">
                {gitAttributionEnabled ? t("settings.disableGitAttribution") : t("settings.enableGitAttribution")}
              </span>
            </button>
            {gitAttributionError ? (
              <small className="settings-muted-line settings-error">
                {gitAttributionError}
              </small>
            ) : null}
          </SettingsRow>
        </SettingsCard>
      </SettingsSection>

      <SettingsSection title={t("settings.mcpServers")} testID="settings-mcp">
        <SettingsCard>
          {mcpLoading && mcpRowNames.length === 0 ? (
            <div className="settings-mcp-empty">{t("settings.loading")}</div>
          ) : mcpRowNames.length > 0 ? (
            mcpRowNames.map((name) => {
              const server = mcpServerByName.get(name);
              const busy = mcpBusyServer === name || mcpToggleBusy === name;
              const connected = server ? server.connected || server.state === "connected" || server.state === "ready" : false;
              const disabledByConfig = server?.state === "disabled";
              const enabled = mcpEnabledDraft[name] ?? !disabledByConfig;
              const oauthPending = Boolean(mcpAuthStates[name]);
              const oauthCode = mcpAuthCodes[name] ?? "";
              return (
                <SettingsRow
                  key={name}
                  title={name}
                  description={server ? formatMCPServerMeta(server, t) : undefined}
                >
                  {server ? (
                    <span className="settings-row-control-value">
                      <span className={`settings-status-pill ${mcpStateTone(server.state)}`}>
                        {mcpStateLabel(server.state, t)}
                      </span>
                    </span>
                  ) : null}
                  {server ? (
                    <>
                      {oauthPending ? (
                        <>
                          <input
                            className="settings-input settings-mcp-code-input"
                            aria-label={t("mcp.authCodeNamed", { name })}
                            autoComplete="off"
                            placeholder={t("mcp.authCode")}
                            value={oauthCode}
                            disabled={busy}
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setMCPAuthCodes((codes) => ({ ...codes, [name]: value }));
                            }}
                          />
                          <button
                            className="settings-button settings-icon-button"
                            type="button"
                            title={t("mcp.finishLogin")}
                            aria-label={t("mcp.finishLoginNamed", { name })}
                            disabled={busy || oauthCode.trim() === ""}
                            onClick={() => void completeMCPAuth(name)}
                          >
                            <Check size={15} aria-hidden="true" />
                          </button>
                          <button
                            className="settings-button settings-icon-button"
                            type="button"
                            title={t("mcp.cancelLogin")}
                            aria-label={t("mcp.cancelLoginNamed", { name })}
                            disabled={busy}
                            onClick={() => {
                              setMCPAuthStates((states) => withoutRecordKey(states, name));
                              setMCPAuthCodes((codes) => withoutRecordKey(codes, name));
                            }}
                          >
                            <X size={15} aria-hidden="true" />
                          </button>
                        </>
                      ) : server.auth_status === "not_logged_in" ? (
                        <button
                          className="settings-button settings-icon-button"
                          type="button"
                          title={t("mcp.oauthLogin")}
                          aria-label={t("mcp.loginNamed", { name })}
                          disabled={busy || disabledByConfig}
                          onClick={() => void beginMCPAuth(name)}
                        >
                          <KeyRound size={15} aria-hidden="true" />
                        </button>
                      ) : server.auth_status === "oauth" ? (
                        <button
                          className="settings-button settings-icon-button"
                          type="button"
                          title={t("mcp.removeLogin")}
                          aria-label={t("mcp.removeLoginNamed", { name })}
                          disabled={busy}
                          onClick={() => void onMCPAuthRemove(name)}
                        >
                          <X size={15} aria-hidden="true" />
                        </button>
                      ) : null}
                      <button
                        className="settings-button settings-icon-button"
                        type="button"
                        title={t("mcp.refresh")}
                        aria-label={t("mcp.refreshNamed", { name })}
                        disabled={busy || disabledByConfig}
                        onClick={() => void onMCPAction(name, "refresh")}
                      >
                        <RefreshCw size={15} aria-hidden="true" />
                      </button>
                      <button
                        className="settings-button settings-icon-button"
                        type="button"
                        title={disabledByConfig ? t("mcp.disabledByConfig") : connected ? t("mcp.disconnect") : t("mcp.connect")}
                        aria-label={`${connected ? t("mcp.disconnect") : t("mcp.connect")} ${name}`}
                        disabled={busy || disabledByConfig}
                        onClick={() => void onMCPAction(name, connected ? "disconnect" : "connect")}
                      >
                        {connected ? <PlugZap size={15} aria-hidden="true" /> : <Plug size={15} aria-hidden="true" />}
                      </button>
                    </>
                  ) : null}
                  <button
                    className="settings-switch"
                    type="button"
                    role="switch"
                    aria-checked={enabled}
                    data-testid={`settings-mcp-enabled-${name}`}
                    disabled={running || !initialized || busy}
                    onClick={() => void toggleMCPServer(name, !enabled)}
                  >
                    <span className="settings-switch-thumb" aria-hidden="true" />
                    <span className="sr-only">{enabled ? t("mcp.disableNamed", { name }) : t("mcp.enableNamed", { name })}</span>
                  </button>
                  {server?.error ? (
                    <small className="settings-mcp-error">{server.error}</small>
                  ) : null}
                </SettingsRow>
              );
            })
          ) : (
            <div className="settings-mcp-empty">{t("settings.noMcpServers")}</div>
          )}
          {mcpError ? <div className="settings-mcp-empty settings-mcp-error">{mcpError}</div> : null}
          {mcpToggleError ? <div className="settings-mcp-empty settings-mcp-error">{mcpToggleError}</div> : null}
        </SettingsCard>
      </SettingsSection>

      <SettingsSection title={t("settings.about")} testID="settings-about">
        <SettingsCard>
          <SettingsRow title={t("settings.version")}>
            <span className="settings-row-control-value">
              {desktopBuild ? versionLabel(desktopBuild.version) : t("settings.loading")}
            </span>
            <button
              className="settings-button"
              type="button"
              aria-label={t("settings.copyVersion")}
              onClick={() => void onCopyVersion()}
              disabled={!desktopBuild || copyState === "copying"}
            >
              {copyState === "copied" ? t("settings.copied") : t("settings.copy")}
            </button>
          </SettingsRow>
        </SettingsCard>
      </SettingsSection>
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Archive page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsArchivePage({
  archivedThreads,
  archivedRooms,
  onUnarchiveThread,
  onUnarchiveRoom,
}: {
  archivedThreads: readonly ArchivedSessionView[];
  archivedRooms: readonly ArchivedRoomView[];
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
  onUnarchiveRoom: (room: ArchivedRoomView) => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [query, setQuery] = useState("");
  const [projectFilter, setProjectFilter] = useState("all");
  const sortedThreads = useMemo(
    () => [...archivedThreads].sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [archivedThreads],
  );
  const sortedRooms = useMemo(
    () => [...archivedRooms].sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [archivedRooms],
  );
  const projectOptions = useMemo(() => {
    const seen = new Set<string>();
    return sortedThreads.flatMap((thread) => {
      const projectID = archiveProjectID(thread);
      if (seen.has(projectID)) {
        return [];
      }
      seen.add(projectID);
      return [{ value: projectID, label: archiveProjectName(thread, t("settings.noProject")) }];
    });
  }, [sortedThreads, t]);
  const groups = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase();
    const grouped = new Map<
      string,
      { projectName: string; threads: ArchivedSessionView[] }
    >();
    for (const thread of sortedThreads) {
      const projectID = archiveProjectID(thread);
      const title = archiveThreadTitle(thread, t("settings.untitledConversation"));
      if (projectFilter !== "all" && projectID !== projectFilter) {
        continue;
      }
      if (normalizedQuery && !title.toLocaleLowerCase().includes(normalizedQuery)) {
        continue;
      }
      const group = grouped.get(projectID) ?? {
        projectName: archiveProjectName(thread, t("settings.noProject")),
        threads: [],
      };
      group.threads.push(thread);
      grouped.set(projectID, group);
    }
    return Array.from(grouped, ([projectID, group]) => ({ projectID, ...group }));
  }, [projectFilter, query, sortedThreads, t]);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredRooms = sortedRooms.filter(
    (room) =>
      projectFilter === "all" &&
      (!normalizedQuery || room.name.toLocaleLowerCase().includes(normalizedQuery)),
  );
  const archivedItemCount = sortedThreads.length + sortedRooms.length;
  const noMatches = archivedItemCount > 0 && groups.length === 0 && filteredRooms.length === 0;

  return (
    <div className="settings-archive-page">
      <div className="settings-archive-toolbar" role="search" aria-label={t("settings.archiveFilter")}>
        <label className="settings-archive-search">
          <Search className="icon" aria-hidden="true" />
          <span className="sr-only">{t("settings.archiveSearch")}</span>
          <input
            type="search"
            value={query}
            placeholder={t("settings.archiveSearch")}
            onChange={(event) => setQuery(event.currentTarget.value)}
          />
        </label>
        <SelectMenu
          className="settings-archive-project-filter"
          triggerClassName="settings-archive-filter-trigger"
          value={projectFilter}
          onChange={setProjectFilter}
          ariaLabel={t("settings.archiveProjectFilter")}
          options={[{ value: "all", label: t("settings.allProjects") }, ...projectOptions]}
          flip
        />
      </div>
      {archivedItemCount === 0 || noMatches ? (
        <div className="settings-archive-empty" role="status">
          <Archive className="settings-archive-empty-icon" aria-hidden="true" />
          <p className="settings-archive-empty-title">
            {noMatches ? t("settings.noArchiveMatches") : t("settings.noArchivedItems")}
          </p>
          {noMatches || isTouchWebShell() ? null : (
            <p className="settings-archive-empty-hint">
              {t("settings.archiveHint")}
            </p>
          )}
        </div>
      ) : (
        <div className="settings-archive-groups" aria-label={t("settings.archivedList")}>
          {filteredRooms.length > 0 ? (
            <section className="settings-archive-group" data-archive-kind="rooms">
              <header className="settings-archive-group-header">
                <div className="settings-archive-group-name">
                  <Hash className="icon" aria-hidden="true" />
                  <span>{t("settings.archivedRooms")}</span>
                </div>
                <span className="settings-archive-group-count">
                  {t("settings.roomCount", { count: filteredRooms.length })}
                </span>
              </header>
              <div className="settings-archive-list">
                {filteredRooms.map((room) => (
                  <div className="settings-archive-row" key={room.id}>
                    <div className="settings-archive-row-copy">
                      <TruncatedText className="settings-archive-title" text={room.name} />
                      <time className="settings-archive-time" dateTime={room.created_at}>
                        {formatArchiveTime(room.created_at, formatDate)}
                      </time>
                    </div>
                    <button
                      type="button"
                      className="settings-button settings-archive-restore"
                      aria-label={t("settings.restoreRoom", { title: room.name })}
                      onClick={() => onUnarchiveRoom(room)}
                    >
                      <Archive className="icon-sm" aria-hidden="true" />
                      {t("settings.restore")}
                    </button>
                  </div>
                ))}
              </div>
            </section>
          ) : null}
          {groups.map((group) => (
            <section className="settings-archive-group" key={group.projectID}>
              <header className="settings-archive-group-header">
                <div className="settings-archive-group-name">
                  <Folder className="icon" aria-hidden="true" />
                  <span>{group.projectName}</span>
                </div>
                <span className="settings-archive-group-count">
                  {t("settings.conversationCount", { count: group.threads.length })}
                </span>
              </header>
              <div className="settings-archive-list">
                {group.threads.map((thread) => {
                  const title = archiveThreadTitle(thread, t("settings.untitledConversation"));
                  return (
                    <div className="settings-archive-row" key={thread.id}>
                      <div className="settings-archive-row-copy">
                        <TruncatedText className="settings-archive-title" text={title} />
                        <time className="settings-archive-time" dateTime={thread.updated_at}>
                          {formatArchiveTime(thread.updated_at, formatDate)}
                        </time>
                      </div>
                      <button
                        type="button"
                        className="settings-button settings-archive-restore"
                        aria-label={t("settings.restoreConversation", { title })}
                        onClick={() => onUnarchiveThread(thread)}
                      >
                        <Archive className="icon-sm" aria-hidden="true" />
                        {t("settings.restore")}
                      </button>
                    </div>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

function archiveThreadTitle(thread: ArchivedSessionView, fallback: string): string {
  return (thread.title ?? "").trim() || fallback;
}

function archiveProjectID(thread: ArchivedSessionView): string {
  return thread.archive_project_id?.trim() || "no-project";
}

function archiveProjectName(thread: ArchivedSessionView, fallback: string): string {
  return thread.archive_project_name?.trim() || fallback;
}

function formatArchiveTime(
  iso: string,
  formatter: (value: Date | number | string, options?: Intl.DateTimeFormatOptions) => string =
    (value, options) => new Intl.DateTimeFormat("zh-CN", options).format(new Date(value)),
): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }
  return formatter(date, {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

/* -------------------------------------------------------------------------- */
/*  Usage page                                                                 */
/* -------------------------------------------------------------------------- */

function SettingsUsagePage({
  usage,
  loading,
  error,
}: {
  usage: SettingsUsageResponse | undefined;
  loading: boolean;
  error: string;
}): JSX.Element {
  const { locale, t, formatNumber } = useI18n();
  const formatUsageValue = (value: number, options?: Intl.NumberFormatOptions): string =>
    Number.isFinite(value) ? formatNumber(value, options) : "—";
  const formatCompactUsageValue = (value: number): string => formatCompactUsageNumber(value, locale);
  const heatmap = usage ? buildUsageHeatmap(usage.days) : [];
  const skillUsage = (usage?.skill_usage ?? []).filter(
    (skill) => skill && typeof skill.name === "string" && skill.name.trim(),
  );
  const maxSkillCount = skillUsage.reduce((max, skill) => {
    const count = Number.isFinite(skill.count) ? Math.max(0, skill.count) : 0;
    return Math.max(max, count);
  }, 0);
  const usageTrend = buildUsageTrend(usage?.days ?? []);
  const maxTrendTotal = usageTrend.reduce((max, day) => Math.max(max, usageTokenTotal(day)), 0);
  const modelChart = (usage?.model_breakdowns ?? []).slice(0, 6).map((model) => ({
    ...model,
    total: model.input_tokens + model.output_tokens + model.cache_creation_tokens + model.cache_read_tokens,
  }));
  const maxModelTotal = modelChart.reduce((max, model) => Math.max(max, model.total), 0);
  const allModelTotal = (usage?.model_breakdowns ?? []).reduce(
    (total, model) => total + model.input_tokens + model.output_tokens + model.cache_creation_tokens + model.cache_read_tokens,
    0,
  );
  const heatmapCols = heatmap.length > 0 ? Math.ceil(heatmap.length / 7) : 12;

  // Keep grid height = 7 × cell-size so cells stay square as panel resizes
  const heatmapRef = useRef<HTMLDivElement>(null);
  const [heatmapHeight, setHeatmapHeight] = useState<number | undefined>(undefined);
  useEffect(() => {
    const el = heatmapRef.current;
    if (!el) return;
    const GAP = 3;
    const update = () => {
      const cellW = (el.offsetWidth - (heatmapCols - 1) * GAP) / heatmapCols;
      setHeatmapHeight(7 * cellW + 6 * GAP);
    };
    update();
    if (typeof ResizeObserver === "undefined") {
      return;
    }
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, [heatmapCols]);

  // Build month label positions: for each column (week), find the first day;
  // when the month changes from the previous column, record (colIndex, monthLabel).
  const monthLabels: { col: number; label: string }[] = [];
  if (heatmap.length > 0) {
    let prevMonth = -1;
    for (let col = 0; col < heatmapCols; col++) {
      const firstDayIdx = col * 7;
      if (firstDayIdx < heatmap.length) {
        const d = new Date(heatmap[firstDayIdx].date);
        const month = d.getMonth();
        if (month !== prevMonth) {
          // Skip the very first column if it's at the left edge — avoid label clipping
          if (col > 0) {
            monthLabels.push({
              col,
              label: new Intl.DateTimeFormat(locale, { month: "short" }).format(d),
            });
          }
          prevMonth = month;
        }
      }
    }
  }
  if (loading) {
    return (
      <div className="settings-usage-page settings-usage-loading" data-testid="settings-usage" aria-busy="true">
        <SettingsUsageSkeleton />
      </div>
    );
  }
  if (!usage) {
    return (
      <div className="settings-usage-page" data-testid="settings-usage">
        <div className="settings-empty" role={error ? "alert" : undefined}>
          {error || t("settings.noUsage")}
        </div>
      </div>
    );
  }
  return (
    <div className="settings-usage-page" data-testid="settings-usage">
      {usage && (
        <div className="settings-usage-stats">
          <UsageStat
            label={t("settings.usageInput")}
            value={formatCompactUsageNumber(usage.metrics.input_tokens, locale)}
            title={formatUsageValue(usage.metrics.input_tokens)}
          />
          <UsageStat
            label={t("settings.usageContext")}
            value={formatCompactUsageNumber(usage.metrics.context_tokens, locale)}
            title={formatUsageValue(usage.metrics.context_tokens)}
          />
          <UsageStat
            label={t("settings.usageOutput")}
            value={formatCompactUsageNumber(usage.metrics.output_tokens, locale)}
            title={formatUsageValue(usage.metrics.output_tokens)}
          />
          <UsageStat label={t("settings.cacheHitRate")} value={formatPercent(usage.metrics.cache_hit_rate)} />
        </div>
      )}

      <section className="settings-usage-chart" aria-labelledby="settings-usage-trend-title">
        <div className="settings-usage-chart-header">
          <h2 id="settings-usage-trend-title" className="settings-usage-table-title">
            {t("settings.usageTrend")}
          </h2>
          <span>{t("settings.last30Days")}</span>
        </div>
        <div className="settings-usage-trend" role="list" aria-label={t("settings.usageTrend")}>
          {usageTrend.map((day) => {
            const total = usageTokenTotal(day);
            const height = maxTrendTotal > 0 && total > 0 ? Math.max(3, (total / maxTrendTotal) * 100) : 0;
            return (
              <Tooltip content={formatUsageDayTitle(day, t, formatCompactUsageValue)} key={day.date}>
                <span
                  className="settings-usage-trend-day"
                  role="listitem"
                  aria-label={formatUsageDayTitle(day, t, formatCompactUsageValue)}
                >
                  <i style={{ height: `${height}%` }} />
                </span>
              </Tooltip>
            );
          })}
        </div>
        <div className="settings-usage-chart-axis" aria-hidden="true">
          <span>{formatUsageChartDate(usageTrend[0]?.date, locale)}</span>
          <span>{formatUsageChartDate(usageTrend.at(-1)?.date, locale)}</span>
        </div>
      </section>

      <div className="settings-heatmap-panel">
        {/* Month labels row */}
        <div
          className="settings-heatmap-months"
          aria-hidden="true"
          style={{ "--heatmap-cols": heatmapCols } as CSSProperties}
        >
          {monthLabels.map(({ col, label }) => (
            <span
              key={col}
              className="settings-heatmap-month-label"
              style={{ gridColumn: col + 1 } as CSSProperties}
            >
              {label}
            </span>
          ))}
        </div>
        {/* Grid */}
        <div
          ref={heatmapRef}
          className="settings-usage-heatmap"
          aria-label={t("settings.usageHeatmap")}
          role="grid"
          style={{
            "--heatmap-cols": heatmapCols,
            ...(heatmapHeight !== undefined ? { height: `${heatmapHeight}px` } : {})
          } as CSSProperties}
        >
          {heatmap.map((day) => (
            <Tooltip content={formatHeatmapTitle(day, t, formatCompactUsageValue)} key={day.date}>
              <span
                className="settings-usage-heatmap-cell"
                data-level={day.level}
                role="gridcell"
                aria-label={formatHeatmapTitle(day, t, formatCompactUsageValue)}
              />
            </Tooltip>
          ))}
        </div>
        <div className="settings-heatmap-legend" aria-hidden="true">
          <span>{t("settings.less")}</span>
          {[0, 1, 2, 3, 4].map((level) => (
            <i className="settings-heatmap-legend-cell" data-level={level} key={level} />
          ))}
          <span>{t("settings.more")}</span>
        </div>
      </div>

      <section className="settings-skill-usage" aria-labelledby="settings-skill-usage-title">
        <div className="settings-skill-usage-header">
          <h2 id="settings-skill-usage-title" className="settings-usage-table-title">
            {t("settings.skillUsage")}
          </h2>
          <span className="settings-skill-usage-count">{t("settings.skillUsageCount")}</span>
        </div>
        {skillUsage.length ? (
          <div className="settings-skill-usage-list">
            {skillUsage.slice(0, 8).map((skill, index) => {
              const count = Number.isFinite(skill.count) ? Math.max(0, skill.count) : undefined;
              const width = count !== undefined && maxSkillCount > 0 ? Math.max(6, (count / maxSkillCount) * 100) : 0;
              return (
                <div className="settings-skill-usage-row" key={skill.name}>
                  <div className="settings-skill-usage-label">
                    <span className="settings-skill-usage-rank">{String(index + 1).padStart(2, "0")}</span>
                    <strong>{skill.name}</strong>
                  </div>
                  <div className="settings-skill-usage-bar" aria-hidden="true">
                    <span style={{ width: `${width}%` }} />
                  </div>
                  <span className="settings-skill-usage-value">{formatUsageNumber(count, formatNumber)}</span>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="settings-skill-usage-empty">{t("settings.noSkillUsage")}</div>
        )}
      </section>

      {modelChart.length > 0 ? (
        <section className="settings-model-chart" aria-labelledby="settings-model-chart-title">
          <div className="settings-usage-chart-header">
            <h2 id="settings-model-chart-title" className="settings-usage-table-title">
              {t("settings.modelDistribution")}
            </h2>
            <span>{t("settings.tokenShare")}</span>
          </div>
          <div className="settings-model-chart-list">
            {modelChart.map((model) => {
              const width = maxModelTotal > 0 ? Math.max(2, (model.total / maxModelTotal) * 100) : 0;
              const share = allModelTotal > 0 ? model.total / allModelTotal : 0;
              return (
                <div className="settings-model-chart-row" key={`${model.provider}\n${model.model}`}>
                  <div className="settings-model-chart-label">
                    <strong>{model.model || t("settings.unknownModel")}</strong>
                    <small>{model.provider || t("settings.unknownProvider")}</small>
                  </div>
                  <div className="settings-model-chart-bar" aria-hidden="true">
                    <span style={{ width: `${width}%` }} />
                  </div>
                  <Tooltip content={formatCompactUsageValue(model.total)}>
                    <span className="settings-model-chart-share">{formatPercent(share)}</span>
                  </Tooltip>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}

      {usage.model_breakdowns.length > 0 ? (
          <div className="settings-group settings-usage-table-wrap">
            <h2 className="settings-usage-table-title">{t("settings.modelUsage")}</h2>
            <table className="settings-usage-table">
              <thead>
                <tr>
                  <th scope="col">{t("settings.model")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageInput")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageOutput")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.hitRate")}</th>
                </tr>
              </thead>
              <tbody>
                {usage.model_breakdowns.map((b) => {
                  const prompt = b.input_tokens + b.cache_read_tokens;
                  const rate = prompt > 0 ? b.cache_read_tokens / prompt : undefined;
                  return (
                    <tr key={`${b.provider}\n${b.model}`}>
                      <td>
                        <div className="settings-usage-model">
                          <strong>{b.provider || t("settings.unknownProvider")}</strong>
                          <small>{b.model || t("settings.unknownModel")}</small>
                        </div>
                      </td>
                      <td className="settings-usage-num">
                        <Tooltip content={formatUsageValue(b.input_tokens)}>
                          <span className="settings-usage-number">
                            {formatCompactUsageNumber(b.input_tokens, locale)}
                          </span>
                        </Tooltip>
                      </td>
                      <td className="settings-usage-num">
                        <Tooltip content={formatUsageValue(b.output_tokens)}>
                          <span className="settings-usage-number">
                            {formatCompactUsageNumber(b.output_tokens, locale)}
                          </span>
                        </Tooltip>
                      </td>
                      <td className="settings-usage-num">
                        <span className="settings-usage-number">{formatPercent(rate)}</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="settings-empty">{t("settings.noUsage")}</div>
        )}
    </div>
  );
}

function SettingsUsageSkeleton(): JSX.Element {
  return (
    <>
      <div className="settings-usage-skeleton-stats" aria-hidden="true">
        {[0, 1, 2, 3].map((item) => (
          <div className="settings-usage-skeleton-stat" key={item}>
            <span className={`settings-usage-skeleton-line settings-usage-skeleton-stat-value settings-usage-skeleton-stat-value-${item}`} />
            <span className="settings-usage-skeleton-line settings-usage-skeleton-stat-label" />
          </div>
        ))}
      </div>
      <section className="settings-usage-skeleton-chart" aria-hidden="true">
        <div className="settings-usage-skeleton-chart-header">
          <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
          <span className="settings-usage-skeleton-line settings-usage-skeleton-period" />
        </div>
        <div className="settings-usage-skeleton-trend">
          {[28, 42, 34, 58, 48, 72, 38, 64, 52, 82, 46, 68, 36, 56, 76, 44, 62, 50, 88, 54, 70, 40, 60, 78, 48, 66, 36, 74, 52, 68].map((height, index) => (
            <i className="settings-usage-skeleton-trend-day" key={index} style={{ height: `${height}%` }} />
          ))}
        </div>
        <div className="settings-usage-skeleton-axis">
          <span className="settings-usage-skeleton-line" />
          <span className="settings-usage-skeleton-line" />
        </div>
      </section>
      <div className="settings-usage-skeleton-heatmap" aria-hidden="true">
        <div className="settings-usage-skeleton-months">
          {[0, 1, 2, 3].map((item) => <span className="settings-usage-skeleton-line" key={item} />)}
        </div>
        <div className="settings-usage-skeleton-grid">
          {Array.from({ length: 84 }, (_, index) => <i key={index} />)}
        </div>
        <div className="settings-usage-skeleton-legend">
          <span className="settings-usage-skeleton-line" />
          <span className="settings-usage-skeleton-line" />
        </div>
      </div>
      <section className="settings-usage-skeleton-list" aria-hidden="true">
        <div className="settings-usage-skeleton-list-header">
          <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
          <span className="settings-usage-skeleton-line settings-usage-skeleton-period" />
        </div>
        {[0, 1, 2, 3].map((item) => (
          <div className="settings-usage-skeleton-row" key={item}>
            <span className="settings-usage-skeleton-line" />
            <span className="settings-usage-skeleton-line" />
            <span className="settings-usage-skeleton-line" />
          </div>
        ))}
      </section>
      <div className="settings-usage-skeleton-footer" aria-hidden="true">
        <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
      </div>
    </>
  );
}

function UsageStat({ label, value, title }: { label: string; value: string; title?: string }): JSX.Element {
  return (
    <div className="settings-usage-stat">
      <Tooltip content={title}>
        <span className="settings-usage-stat-value">{value}</span>
      </Tooltip>
      <span className="settings-usage-stat-label">{label}</span>
    </div>
  );
}

function formatCompactUsageNumber(value: number, locale: string): string {
  if (!Number.isFinite(value)) {
    return "—";
  }
  const units = [
    { threshold: 1_000, suffix: "k" },
    { threshold: 1_000_000, suffix: "M" },
    { threshold: 1_000_000_000, suffix: "B" },
  ];
  const absoluteValue = Math.abs(value);
  if (absoluteValue < units[0].threshold) {
    return new Intl.NumberFormat(locale).format(value);
  }

  let unitIndex = 0;
  while (unitIndex < units.length - 1 && absoluteValue >= units[unitIndex + 1].threshold) {
    unitIndex += 1;
  }

  let scaled = value / units[unitIndex].threshold;
  if (Math.abs(Math.round(scaled * 10) / 10) >= 1_000 && unitIndex < units.length - 1) {
    unitIndex += 1;
    scaled = value / units[unitIndex].threshold;
  }

  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(scaled)}${units[unitIndex].suffix}`;
}

function formatUsageNumber(value: number | undefined, formatNumber: (value: number) => string): string {
  return value === undefined || !Number.isFinite(value) ? "—" : formatNumber(value);
}


/* -------------------------------------------------------------------------- */
/*  Helpers (kept at module scope, no behavior change)                         */
/* -------------------------------------------------------------------------- */

type Translate = ReturnType<typeof useI18n>["t"];

function settingsPageTitle(page: SettingsPage, t: Translate): string {
  switch (page) {
    case "providers":
      return t("settings.providers");
    case "collaboration":
      return t("settings.collaboration");
    case "advanced":
      return t("settings.advanced");
    case "general":
      return t("settings.general");
    case "usage":
      return t("settings.usage");
    case "remote":
      return t("settings.remote");
    case "archive":
      return t("settings.archive");
    default:
      return t("skills.sectionPlugins");
  }
}

function isConfigurablePlugin(
  extension: ExtensionInventoryRecord,
): boolean {
  const approved = extension.approval_state === "official"
    || extension.approval_state === "granted";
  return extension.kind === "plugin"
    && approved
    && extension.enabled !== false
    && (extension.contributions?.settings?.length ?? 0) > 0;
}

function pluginSettingsPageId(pluginId: string): `plugin-settings:${string}` {
  return `plugin-settings:${pluginId}`;
}

function pluginViewSettingsPageId(
  pluginId: string,
  entryId: string,
): `plugin-view:${string}:${string}` {
  return `plugin-view:${pluginId}:${entryId}`;
}

function stableBoolRecordSignature(record: Record<string, boolean>): string {
  return Object.keys(record)
    .sort((a, b) => a.localeCompare(b))
    .map((key) => `${key}:${record[key] ? "1" : "0"}`)
    .join("|");
}

function parseOptionalInteger(raw: string, label: string, t: Translate): { value: number; error?: string } {
  const parsed = parseOptionalNumber(raw, label, t);
  if (parsed.error) {
    return parsed;
  }
  if (!Number.isInteger(parsed.value)) {
    return { value: 0, error: t("validation.integer", { field: label }) };
  }
  return parsed;
}

function parseOptionalNumber(raw: string, label: string, t: Translate): { value: number; error?: string } {
  const value = raw.trim();
  if (value === "") {
    return { value: 0 };
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return { value: 0, error: t("validation.nonNegative", { field: label }) };
  }
  return { value: parsed };
}

function parseTemperatureDraft(raw: string, t: Translate): { value: number; error?: string } {
  const value = raw.trim();
  if (value === "" || value.toLowerCase() === "auto") {
    return { value: 0 };
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0 || parsed > 2) {
    return { value: 0, error: t("validation.temperature") };
  }
  return { value: parsed };
}

function formatPercentDraft(value: number | undefined): string {
  if (!value || !Number.isFinite(value) || value <= 0) {
    return "";
  }
  return String(Math.round(value * 100));
}

function formatOptionalNumberDraft(value: number | undefined): string {
  if (!value || !Number.isFinite(value) || value <= 0) {
    return "";
  }
  return String(value);
}

function formatTemperatureDraft(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value) || value <= 0) {
    return "";
  }
  return String(value);
}

function advancedContextSourceLabel(source: string | undefined, t: Translate): string {
  switch (source) {
    case "provider_context_window":
      return t("advanced.sourceProviderOverride");
    case "provider_model_limit":
      return t("advanced.sourceModelConfig");
    case "provider_input_limit":
      return t("advanced.sourceInputLimit");
    case "agent_max_context_tokens":
      return t("advanced.sourceManualLimit");
    case "unknown":
    case "":
    case undefined:
      return t("advanced.sourceUnknown");
    default:
      return source;
  }
}

function providerConnectionStatus(provider: ProviderSummary, t: Translate): string {
  if (isGrokBuildType(provider.type)) {
    return provider.api_key_configured ? t("provider.grokBuildLoggedIn") : t("provider.grokBuildLoginRequired");
  }
  if (isXAISubscriptionType(provider.type)) {
    return provider.api_key_configured ? t("provider.xaiLoggedIn") : t("provider.xaiLogin");
  }
  if (provider.connection_locked) {
    return "OAuth";
  }
  const label = isAnthropicProviderType(provider.type)
    ? t("provider.authToken")
    : t("provider.apiKey");
  return provider.api_key_configured
    ? t("provider.authConfigured", { field: label })
    : t("provider.authMissing", { field: label });
}

function isAnthropicProviderType(type: string | undefined): boolean {
  const normalized = (type ?? "").trim().toLowerCase().replaceAll("_", "-");
  return normalized === "anthropic" || normalized === "claude" || normalized === "anthropic-official";
}

function isXAISubscriptionType(type: string | undefined): boolean {
  const normalized = (type ?? "").trim().toLowerCase().replaceAll("_", "-");
  return (
    normalized === "xai-subscription" ||
    normalized === "xai-oauth" ||
    normalized === "grok-subscription" ||
    normalized === "supergrok"
  );
}

function isGrokBuildType(type: string | undefined): boolean {
  const normalized = (type ?? "").trim().toLowerCase().replaceAll("_", "-");
  return normalized === "grok-build" || normalized === "xai-grok-build" || normalized === "grok-cli";
}

type UsageHeatmapCell = SettingsUsageDay & {
  level: number;
};

function formatTokenCount(value: number): string {
  return formatCurrentNumber(Math.max(0, value));
}

function formatOptionalTokenCount(value: number | undefined): string {
  if (!value || !Number.isFinite(value) || value <= 0) {
    return "";
  }
  return formatTokenCount(value);
}

function formatPercent(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) {
    return "—";
  }
  return `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`;
}

function buildUsageHeatmap(days: SettingsUsageDay[]): UsageHeatmapCell[] {
  const byDate = new Map(days.map((day) => [day.date, day]));
  const end = startOfLocalDay(new Date());
  const start = startOfWeek(addDays(end, -364));
  const startKey = localDateKey(start);
  const endKey = localDateKey(end);
  const activeTotals = days
    .filter((day) => day.date >= startKey && day.date <= endKey)
    .map(usageTokenTotal)
    .filter((total) => total > 0)
    .sort((a, b) => a - b);
  const cells: UsageHeatmapCell[] = [];
  for (let cursor = start; cursor.getTime() <= end.getTime(); cursor = addDays(cursor, 1)) {
    const date = localDateKey(cursor);
    const day = byDate.get(date) ?? {
      date,
      input_tokens: 0,
      output_tokens: 0,
      cache_creation_tokens: 0,
      cache_read_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
    };
    cells.push({
      ...day,
      level: usageHeatmapLevel(day, activeTotals),
    });
  }
  return cells;
}

function usageHeatmapLevel(day: SettingsUsageDay, activeTotals: number[]): number {
  const total = usageTokenTotal(day);
  if (total <= 0 || activeTotals.length === 0) {
    return 0;
  }
  const upperRank = activeTotals.findLastIndex((candidate) => candidate <= total) + 1;
  return Math.min(4, Math.max(1, Math.ceil((upperRank / activeTotals.length) * 4)));
}

function usageTokenTotal(day: SettingsUsageDay): number {
  return (
    Math.max(0, day.input_tokens) +
    Math.max(0, day.output_tokens) +
    Math.max(0, day.cache_creation_tokens) +
    Math.max(0, day.cache_read_tokens)
  );
}

function buildUsageTrend(days: SettingsUsageDay[], length = 30): SettingsUsageDay[] {
  const byDate = new Map(days.map((day) => [day.date, day]));
  const end = startOfLocalDay(new Date());
  return Array.from({ length }, (_, index) => {
    const date = localDateKey(addDays(end, index - length + 1));
    return byDate.get(date) ?? {
      date,
      input_tokens: 0,
      output_tokens: 0,
      cache_creation_tokens: 0,
      cache_read_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
    };
  });
}

function formatUsageChartDate(date: string | undefined, locale: string): string {
  if (!date) {
    return "";
  }
  return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(new Date(`${date}T12:00:00`));
}

function formatUsageDayTitle(
  day: SettingsUsageDay,
  t: Translate,
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string,
): string {
  if (!hasUsageDayData(day)) {
    return t("settings.noUsageOnDate", { date: day.date });
  }
  return t("settings.usageTrendOnDate", {
    date: day.date,
    total: formatNumber(usageTokenTotal(day)),
    input: formatNumber(day.input_tokens + day.cache_read_tokens + day.cache_creation_tokens),
    output: formatNumber(day.output_tokens),
  });
}

function hasUsageDayData(day: SettingsUsageDay): boolean {
  return (
    day.input_tokens > 0 ||
    day.output_tokens > 0 ||
    day.cache_creation_tokens > 0 ||
    day.cache_read_tokens > 0 ||
    day.turns > 0 ||
    day.agents > 0
  );
}

function formatHeatmapTitle(
  day: UsageHeatmapCell,
  t: Translate,
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string,
): string {
  if (!hasUsageDayData(day)) {
    return t("settings.noUsageOnDate", { date: day.date });
  }
  return t("settings.usageOnDate", {
    date: day.date,
    input: formatNumber(day.input_tokens),
    output: formatNumber(day.output_tokens),
    rate: formatPercent(day.cache_hit_rate),
  });
}



function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

function startOfWeek(date: Date): Date {
  const day = date.getDay();
  return addDays(startOfLocalDay(date), -day);
}

function addDays(date: Date, days: number): Date {
  const out = new Date(date);
  out.setDate(out.getDate() + days);
  return out;
}

function localDateKey(date: Date): string {
  const year = date.getFullYear();
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function versionLabel(version: string): string {
  const trimmed = version.trim();
  if (!trimmed) {
    return trimmed;
  }
  return trimmed.toLowerCase().startsWith("v") ? trimmed : `v${trimmed}`;
}

function formatBuildDate(iso: string): string {
  // The build date is a UTC ISO timestamp; render in a compact local form
  // so the user can correlate it with their clock without doing TZ math.
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) {
    return iso;
  }
  return parsed.toISOString().replace("T", " ").replace(/\.\d{3}Z$/, "Z");
}

function upsertMCPServerStatus(servers: MCPServerStatus[], status: MCPServerStatus): MCPServerStatus[] {
  const next = [...servers];
  const index = next.findIndex((item) => item.name === status.name);
  if (index >= 0) {
    next[index] = status;
  } else {
    next.push(status);
  }
  next.sort((a, b) => a.name.localeCompare(b.name));
  return next;
}

function withoutRecordKey(values: Record<string, string>, key: string): Record<string, string> {
  const next = { ...values };
  delete next[key];
  return next;
}

function formatMCPServerMeta(server: MCPServerStatus, t: Translate): string {
  const pieces = [t("mcp.toolCount", { count: server.tool_count ?? 0 })];
  if (server.auth_status && server.auth_status !== "unsupported") {
    pieces.push(mcpAuthLabel(server.auth_status, t));
  }
  return pieces.join(" · ");
}

function mcpStateLabel(state: string, t: Translate): string {
  switch (state) {
    case "ready":
    case "connected":
      return t("mcp.connected");
    case "starting":
    case "connecting":
      return t("mcp.connecting");
    case "error":
    case "failed":
      return t("mcp.failed");
    case "disabled":
      return t("mcp.disconnected");
    case "auth_required":
    case "needs_auth":
      return t("mcp.needsAuth");
    case "needs_client_registration":
      return t("mcp.needsRegistration");
    case "reconnecting":
      return t("mcp.reconnecting");
    case "stopped":
    case "configured":
      return t("mcp.configured");
    default:
      return state || t("mcp.unknown");
  }
}

function mcpStateTone(state: string): string {
  switch (state) {
    case "ready":
    case "connected":
      return "success";
    case "error":
    case "failed":
    case "auth_required":
    case "needs_auth":
    case "needs_client_registration":
      return "danger";
    case "starting":
    case "reconnecting":
    case "connecting":
      return "warning";
    default:
      return "neutral";
  }
}

function mcpAuthLabel(status: string, t: Translate): string {
  switch (status) {
    case "bearer_token":
      return t("mcp.headerAuth");
    case "not_logged_in":
      return t("mcp.notLoggedIn");
    case "oauth":
      return "OAuth";
    default:
      return status;
  }
}

function providerDisplayLabels(providers: ProviderSummary[], t: Translate): Map<string, string> {
  const baseLabels = new Map<string, string>();
  const counts = new Map<string, number>();
  providers.forEach((provider) => {
    const label = providerServiceLabel(provider, t);
    baseLabels.set(provider.name, label);
    counts.set(label, (counts.get(label) ?? 0) + 1);
  });
  return new Map(
    providers.map((provider) => {
      const label = baseLabels.get(provider.name) ?? provider.name;
      if ((counts.get(label) ?? 0) > 1) {
        return [provider.name, `${label} · ${provider.name}`];
      }
      return [provider.name, label];
    })
  );
}

function nextCustomProviderName(providers: ProviderSummary[]): string {
  const existing = new Set(providers.map((provider) => provider.name));
  let index = 1;
  while (existing.has(`custom-${index}`)) {
    index += 1;
  }
  return `custom-${index}`;
}

function providerServiceLabel(provider: ProviderSummary, t: Translate): string {
  const type = provider.type.trim().toLowerCase().replaceAll("_", "-");
  if (isXAISubscriptionType(type)) {
    return t("provider.xaiSubscription");
  }
  if (isGrokBuildType(type)) {
    return t("provider.grokBuild");
  }
  if (provider.connection_locked || type === "openai-codex" || type === "codex-subscription" || type === "chatgpt-codex") {
    return "OpenAI OAuth";
  }
  const baseURLLabel = serviceLabelFromBaseURL(provider.base_url, t);
  if (baseURLLabel) {
    return baseURLLabel;
  }
  if (isAnthropicProviderType(type)) {
    return "Anthropic";
  }
  if (type === "openai" || type === "codex") {
    return "OpenAI API";
  }
  if (type === "openai-compatible") {
    return serviceLabelFromBaseURL(provider.base_url, t) || "OpenAI-compatible";
  }
  return type || t("provider.genericService");
}

function serviceLabelFromBaseURL(baseURL: string | undefined, t: Translate): string {
  const host = hostFromBaseURL(baseURL);
  if (!host) return "";
  if (host.includes("api.openai.com")) return "OpenAI API";
  if (host.includes("api.anthropic.com")) return "Anthropic";
  if (host.includes("openrouter.ai")) return "OpenRouter";
  if (host.includes("moonshot") || host.includes("kimi")) return "Kimi";
  if (host.includes("bigmodel") || host.includes("zhipu")) return t("provider.zhipu");
  if (host.includes("deepseek")) return "DeepSeek";
  if (host.includes("generativelanguage.googleapis.com") || host.includes("googleapis.com")) return "Google Gemini";
  if (host.includes("dashscope") || host.includes("aliyuncs.com")) return t("provider.alibabaBailian");
  if (host.includes("volces") || host.includes("ark.cn-beijing.volces.com")) return t("provider.volcengineArk");
  if (host.includes("siliconflow")) return t("provider.siliconFlow");
  if (host === "localhost" || host === "127.0.0.1" || host === "::1") return t("provider.localService");
  return host;
}

function hostFromBaseURL(baseURL?: string): string {
  if (!baseURL) return "";
  try {
    return new URL(baseURL).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

/* -------------------------------------------------------------------------- */
/*  远程控制                                                                    */
/* -------------------------------------------------------------------------- */

/** Data wiring for the remote-control page: pulls the snapshot from the
 *  main-process RemoteHostManager, re-pulls on every remote event (pairing
 *  URI shown, phone paired, host exit), and maps panel actions to IPC. */
function SettingsRemotePageContainer(): JSX.Element {
  const [snapshot, setSnapshot] = useState<RemoteControlSnapshot | null>(null);
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      window.wuu
        .getRemoteControlSnapshot()
        .then((snap) => {
          if (!cancelled) {
            setSnapshot(snap);
            if (snap.pair_uri) setActionError("");
          }
        })
        .catch(() => {});
    };
    refresh();
    const off = window.wuu.onRemoteControlEvent(() => refresh());
    return () => {
      cancelled = true;
      off();
    };
  }, []);

  const run = (action: () => Promise<RemoteControlSnapshot>) => {
    setBusy(true);
    setActionError("");
    action()
      .then(setSnapshot)
      .catch((err: unknown) => {
        setActionError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => setBusy(false));
  };

  return (
    <SettingsRemotePage
      status={snapshot?.status ?? null}
      statusError={actionError || snapshot?.status_error || ""}
      hostRunning={snapshot?.host_running ?? false}
      hostEnabled={snapshot?.host_enabled ?? snapshot?.host_running ?? false}
      pairUri={snapshot?.pair_uri ?? null}
      webUrl={snapshot?.web_url ?? null}
      busy={busy}
      onToggleHost={(enabled) => run(() => window.wuu.setRemoteHostEnabled(enabled))}
      onOpenPairing={() => run(() => window.wuu.startRemotePairing())}
      onRemoveDevice={(device) => run(() => window.wuu.removeRemoteDevice(device.fingerprint))}
    />
  );
}
