import { hostSupports } from "./HostCapabilities";
import { isTouchWebShell } from "./ComposerFocus";
import {
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  Archive,
  BarChart3,
  Bot,
  Check,
  Copy,
  Folder,
  Gauge,
  KeyRound,
  LayoutDashboard,
  Loader2,
  LogOut,
  Monitor,
  Plug,
  PlugZap,
  RefreshCw,
  RotateCcw,
  Search,
  Settings,
  Smartphone,
  X,
  type IconComponent
} from "./WuuIcons";
import type {
  CodexPetSettingsUpdate,
  EngineListResult,
  EngineUpdateParams,
  RuntimeAdvancedSettingsUpdate,
  RuntimeGeneralSettingsUpdate,
} from "../shared/protocol";
import {
  type CSSProperties,
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
import { sidebarDrawerExitMs, sidebarMotionMs } from "./AppLayoutState";
import { SelectMenu } from "./SelectMenu";
import type {
  CodexPetsSnapshot,
  DesktopBuildInfo,
  ExtensionInventoryRecord,
  InitializeResult,
  MCPAuthStartResult,
  MCPServerStatus,
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
import { ENABLE_PTC_SETTINGS, ENABLE_REMOTE_CONTROL, ENABLE_SUBSCRIPTIONS } from "./FeatureFlags";
import { AppearanceTypography } from "./AppearanceTypography";
import { BackgroundSettings } from "./background/BackgroundSettings";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsPageHeader, SettingsSection, type SettingsStatusTone } from "./SettingsSection";
import { toastErrorMessage } from "./Toast";
import { EngineSettingsSection } from "./EngineSettingsSection";
import { ModelServicesPage } from "./ModelServicesPage";
import { SubscriptionDashboard } from "./SubscriptionDashboard";
import { SettingsRemotePage } from "./SettingsRemotePage";
import { ThemePreferenceControl } from "./ThemePreferenceSection";
import { LanguagePreferenceControl } from "./LanguagePreferenceSection";
import { formatCurrentNumber, useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import {
  buildUsageHeatmap,
  buildUsageTrend,
  formatCompactUsageNumber,
  usageTokenTotal,
  type UsageHeatmapCell,
} from "./UsageActivity";
import { SettingsPresentation } from "./plugins/SettingsPresentation";
import {
  desktopPluginHost,
  desktopWorkbenchController,
} from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import { PluginViewContent, type WorkbenchController } from "./plugins/Workbench";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { PluginIcon } from "./PublicIcon";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import type { SettingsPageHostAPI, SettingsPageSummaryV1, SettingsValueMapV1 } from "../shared/workbench";

type NativeSettingsPage =
  | "providers"
  | "agents"
  | "subscriptions"
  | "advanced"
  | "general"
  | "appearance"
  | "remote"
  | "mcp"
  | "usage"
  | "archive";

export type SettingsPage =
  | NativeSettingsPage
  | `plugin-settings:${string}`
  | `plugin-view:${string}:${string}`;

// Page ids are part of the plugin settings snapshot, so they stay stable when
// labels or grouping change ("advanced" is the runtime page).
const NATIVE_PAGE_ICONS: Record<NativeSettingsPage, IconComponent> = {
  providers: KeyRound,
  agents: Bot,
  subscriptions: LayoutDashboard,
  advanced: Gauge,
  general: Settings,
  appearance: Monitor,
  remote: Smartphone,
  mcp: Plug,
  usage: BarChart3,
  archive: Archive,
};

function remoteControlAvailable(): boolean {
  return ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot");
}

function nativeSettingsGroups(): { label: TranslationKey; pages: NativeSettingsPage[] }[] {
  return [
    {
      label: "settings.groupAgent",
      pages: ["providers", "agents", ...(ENABLE_SUBSCRIPTIONS ? ["subscriptions" as const] : []), "advanced"],
    },
    {
      label: "settings.groupApp",
      pages: ["general", "appearance", ...(remoteControlAvailable() ? ["remote" as const] : [])],
    },
    { label: "settings.groupExtensions", pages: ["mcp"] },
    { label: "settings.groupData", pages: ["usage", "archive"] },
  ];
}

type CopyState = "idle" | "copying" | "copied";

const COPY_RESET_MS = 1500;

function availableSettingsPage(page: SettingsPage | undefined): SettingsPage {
  const next = page ?? "providers";
  if (next === "subscriptions" && !ENABLE_SUBSCRIPTIONS) {
    return "providers";
  }
  if (next === "remote" && !remoteControlAvailable()) {
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
  onUnarchiveThread,
  // The settings rail shares the main sidebar's state and handlers wholesale:
  // same persisted width + collapse flag, same drag-to-collapse resize
  // session, same toggle motion.
  sidebarCollapsed,
  sidebarAnimating,
  onToggleSidebar,
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
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
  sidebarCollapsed: boolean;
  sidebarAnimating: boolean;
  onToggleSidebar: () => void;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
}): JSX.Element {
  const { t } = useI18n();
  const providers = initialized?.providers ?? [];
  const runningProviderNameSet = useMemo(
    () => new Set((runningProviderNames ?? []).map((name) => name.trim()).filter(Boolean)),
    [runningProviderNames],
  );
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
  const [maxStepsDraft, setMaxStepsDraft] = useState("");
  const [temperatureDraft, setTemperatureDraft] = useState("");
  // Runtime errors stay beside the field that failed to validate or save.
  const [advancedError, setAdvancedError] = useState<{ field: AdvancedField; message: string } | null>(null);
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
  useEffect(() => {
    const advanced = initialized?.advanced_settings;
    const synced = {
      compactThreshold: formatPercentDraft(advanced?.compact_threshold_pct),
      compactKeepRecent: formatOptionalNumberDraft(advanced?.compact_keep_recent_tokens),
      providerContextWindow: formatOptionalNumberDraft(advanced?.provider_context_window),
      maxContextTokens: formatOptionalNumberDraft(advanced?.max_context_tokens),
      maxSteps: formatOptionalNumberDraft(advanced?.max_steps),
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
    setAdvancedError(null);
  }, [initialized?.advanced_settings, initialized?.provider, initialized?.model]);

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
    setAdvancedError(null);
    void onAdvancedSave({ disable_auto_compact: !next }).catch((saveError: unknown) => {
      setAutoCompactDraft(!next);
      setAdvancedError({ field: "autoCompact", message: saveError instanceof Error ? saveError.message : t("settings.saveFailed") });
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
      setAdvancedError(validationError ? { field, message: validationError } : null);
      return;
    }
    setAdvancedError(null);
    try {
      await onAdvancedSave(update);
      advancedCommittedRef.current[field] = draft;
    } catch (saveError) {
      setAdvancedError({ field, message: saveError instanceof Error ? saveError.message : t("settings.saveFailed") });
    }
  }

  const shellStyle = {
    // Same variables as the main app shell (`--sidebar-width` collapses to 0,
    // `--sidebar-open-width` remembers the open width for the hover drawer)
    // so sidebar.css and settings.css read one vocabulary for both shells.
    "--sidebar-width": `${sidebarCollapsed ? 0 : sidebarWidth}px`,
    "--sidebar-open-width": `${sidebarWidth}px`
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
    motionMs: sidebarDrawerExitMs,
    dockingMotionMs: sidebarMotionMs,
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

  const pluginPageTitle = activePluginSettingsRecord?.name ?? activeCustomPluginPage?.title;
  const navigationGroups = nativeSettingsGroups();
  const availablePages = useMemo<readonly SettingsPageSummaryV1[]>(() => Object.freeze([
    ...navigationGroups.flatMap((group) => group.pages.map((page) => Object.freeze({
      id: page,
      label: settingsPageTitle(page, t),
    }))),
    ...pluginSettingsRecords.map((plugin) => Object.freeze({
      id: pluginSettingsPageId(plugin.id),
      label: plugin.name,
    })),
    ...customPluginSettingsPages.map((entry) => Object.freeze({
      id: pluginViewSettingsPageId(entry.pluginId, entry.id),
      label: entry.title,
    })),
    // navigationGroups is derived from build flags and host capabilities,
    // which do not change while the page is mounted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  ]), [customPluginSettingsPages, pluginSettingsRecords, t]);

  const sidebarToggle = (
    <button
      type="button"
      className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle settings-sidebar-toggle"
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
  );

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
          <div className="traffic-spacer">{!sidebarCollapsed ? sidebarToggle : null}</div>
          <button className="settings-back-button" type="button" onClick={onBack}>
            <ArrowLeft className="icon" />
            <span>{t("settings.backToApp")}</span>
          </button>
          <nav
            className="settings-nav"
            data-wuu-component="settings-navigation"
            aria-label={t("settings.navigation")}
          >
            {navigationGroups.map((group) => (
              <div className="settings-nav-group" key={group.label}>
                <div className="settings-nav-group-label">{t(group.label)}</div>
                {group.pages.map((page) => {
                  const Icon = NATIVE_PAGE_ICONS[page];
                  return (
                    <SettingsNavItem key={page} icon={<Icon className="icon-lg" />} active={activePage === page} onClick={() => setActivePage(page)}>
                      {settingsPageTitle(page, t)}
                    </SettingsNavItem>
                  );
                })}
                {group.label === "settings.groupExtensions" && (pluginSettingsRecords.length > 0 || customPluginSettingsPages.length > 0) ? (
                  <div className="settings-nav-plugins" data-wuu-component="plugin-settings-navigation">
                    {pluginSettingsRecords.map((plugin) => {
                      const pageId = pluginSettingsPageId(plugin.id);
                      return (
                        <SettingsNavItem
                          key={pageId}
                          icon={<PluginBlocksIcon className="icon-lg" />}
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
              </div>
            ))}
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
          {/* Match the main shell's docked/collapsed slots. Keep the collapsed
           * toggle inside the drag strip so native hit testing honors no-drag. */}
          {sidebarCollapsed ? sidebarToggle : null}
        </div>
        <div ref={settingsScrollRef} className="settings-scroll">
          <div
            className="settings-page"
            data-wuu-component="settings-page"
            data-wuu-page={activePage}
            key={activePage}
          >
            {activePluginSettingsRecord ? (
              <>
                <SettingsPageHeader title={activePluginSettingsRecord.name} />
                <PluginSettingsEditor plugin={activePluginSettingsRecord} />
              </>
            ) : activeCustomPluginPage ? (
              <>
                <SettingsPageHeader title={pluginPageTitle ?? activeCustomPluginPage.title} />
                <PluginViewContent
                  controller={workbenchController}
                  pluginId={activeCustomPluginPage.pluginId}
                  viewTypeId={activeCustomPluginPage.view}
                  context={Object.freeze({ surface: "settings" })}
                  settings={settingsPageHost}
                  onFailure={() => setActivePage("providers")}
                />
              </>
            ) : activePage === "subscriptions" && ENABLE_SUBSCRIPTIONS ? (
              <SubscriptionDashboard
                inventory={engineInventory}
                providers={providers}
                onSelectBuiltinModel={(provider, model) => onSave(provider, model)}
              />
            ) : activePage === "providers" ? (
              <ModelServicesPage
                initialized={initialized}
                running={running}
                runningProviderNames={runningProviderNameSet}
                onSave={onSave}
                onRemoveProvider={onRemoveProvider}
                onRefreshModelCatalog={onRefreshModelCatalog}
              />
            ) : activePage === "agents" ? (
              <EngineSettingsSection
                result={engineInventory}
                loadError={engineInventoryError}
                onRefresh={onRefreshEngineInventory}
                onUpdate={onUpdateEngineInventory}
              />
            ) : activePage === "advanced" ? (
              <SettingsRuntimePage
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
                onGeneralSave={onGeneralSave}
              />
            ) : activePage === "general" ? (
              <SettingsGeneralPage
                desktopBuild={desktopBuild}
                codexPets={codexPets}
                codexPetsLoading={codexPetsLoading}
                codexPetsError={codexPetsError}
                onCodexPetsRefresh={onCodexPetsRefresh}
                onCodexPetsUpdate={onCodexPetsUpdate}
                copyState={copyState}
                onCopyVersion={copyVersionInfo}
              />
            ) : activePage === "appearance" ? (
              <SettingsAppearancePage />
            ) : activePage === "mcp" ? (
              <SettingsMCPPage
                initialized={initialized}
                running={running}
                mcpServers={mcpServers}
                mcpLoading={mcpLoading}
                mcpError={mcpError}
                mcpBusyServer={mcpBusyServer}
                onGeneralSave={onGeneralSave}
                onMCPAction={runMCPAction}
                onMCPAuthStart={startMCPAuth}
                onMCPAuthFinish={finishMCPAuth}
                onMCPAuthRemove={removeMCPAuth}
              />
            ) : activePage === "remote" && remoteControlAvailable() ? (
              <>
                <SettingsPageHeader title={t("settings.remote")} />
                <SettingsRemotePageContainer />
              </>
            ) : activePage === "archive" ? (
              <SettingsArchivePage
                archivedThreads={archivedThreads ?? []}
                onUnarchiveThread={onUnarchiveThread}
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
      hasError={Boolean(advancedError || usageError || mcpError || codexPetsError)}
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

/* -------------------------------------------------------------------------- */
/*  Runtime page ("advanced" page id)                                          */
/* -------------------------------------------------------------------------- */

type AdvancedNumericField =
  | "compactThreshold"
  | "compactKeepRecent"
  | "providerContextWindow"
  | "maxContextTokens"
  | "maxSteps"
  | "temperature";

type AdvancedField = AdvancedNumericField | "autoCompact";

function SettingsRuntimePage({
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
  onCommitField,
  onGeneralSave
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
  error: { field: AdvancedField; message: string } | null;
  onAutoCompactToggle: () => void;
  onCompactThresholdChange: (value: string) => void;
  onCompactKeepRecentChange: (value: string) => void;
  onProviderContextWindowChange: (value: string) => void;
  onMaxContextTokensChange: (value: string) => void;
  onMaxStepsChange: (value: string) => void;
  onTemperatureChange: (value: string) => void;
  onCommitField: (field: AdvancedNumericField) => void;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const ptc = initialized?.general_settings?.ptc ?? { enabled: false };
  const [ptcBusy, setPTCBusy] = useState(false);
  const [ptcError, setPTCError] = useState("");
  const [ptcFamily, setPTCFamily] = useState("gpt");
  const ptcFamilyValue = ptc.families?.[ptcFamily];
  async function savePTC(next: NonNullable<RuntimeGeneralSettingsUpdate["ptc"]>): Promise<void> {
    setPTCBusy(true);
    setPTCError("");
    try { await onGeneralSave({ ptc: next }); }
    catch (error) { setPTCError(error instanceof Error ? error.message : t("settings.saveFailed")); }
    finally { setPTCBusy(false); }
  }
  const [gitAttributionBusy, setGitAttributionBusy] = useState(false);
  const [gitAttributionError, setGitAttributionError] = useState("");
  const gitAttributionEnabled = initialized?.general_settings?.git_attribution_enabled ?? true;
  const fieldsDisabled = running || !initialized;
  const fieldError = (field: AdvancedField): string | undefined => (error?.field === field ? error.message : undefined);

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
    } catch (saveError) {
      setGitAttributionError(
        toastErrorMessage(saveError, t("settings.saveGitAttributionFailed")),
      );
    } finally {
      setGitAttributionBusy(false);
    }
  }

  // Enter and blur both commit through onCommitField; the ref-guard inside
  // makes the blur that follows Enter a no-op, so there is one effective
  // commit per edit. A unit inside the field replaces a description line
  // that only named it.
  const numericInput = (
    field: AdvancedNumericField,
    value: string,
    onChange: (value: string) => void,
    options: { label: string; placeholder?: string; inputMode?: "numeric" | "decimal"; unit?: string },
  ): JSX.Element => {
    const input = (
      <input
        className="settings-input settings-input-num"
        aria-label={options.label}
        value={value}
        aria-invalid={error?.field === field || undefined}
        inputMode={options.inputMode ?? "numeric"}
        placeholder={options.placeholder}
        onChange={(event) => onChange(event.target.value)}
        onBlur={() => onCommitField(field)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            onCommitField(field);
            event.currentTarget.blur();
          }
        }}
        disabled={fieldsDisabled}
      />
    );
    // A numeric placeholder (the default) keeps its unit; a word ("Auto") does not.
    return options.unit ? (
      <span className="settings-input-unit" data-unit={options.unit}
        data-unit-placeholder={/^[\d.,\s]+$/.test(options.placeholder ?? "") || undefined}
        style={{ "--settings-unit-chars": options.unit.length } as CSSProperties}>{input}</span>
    ) : input;
  };

  return (
    <>
      <SettingsPageHeader title={t("settings.runtime")} description={t("settings.runtimeDescription")} />
      <SettingsSection title={t("settings.sectionCompaction")} testID="settings-advanced">
        <SettingsGroup>
          <SettingsRow
            title={t("settings.autoCompact")}
            error={fieldError("autoCompact")}
          >
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-checked={autoCompact}
              disabled={fieldsDisabled}
              onClick={onAutoCompactToggle}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
              <span className="sr-only">{autoCompact ? t("settings.disableAutoCompact") : t("settings.enableAutoCompact")}</span>
            </button>
          </SettingsRow>
          <SettingsRow
            title={t("settings.compactThreshold")}
            error={fieldError("compactThreshold")}
          >
            {numericInput("compactThreshold", compactThreshold, onCompactThresholdChange, {
              label: t("settings.compactThreshold"),
              placeholder: t("settings.automatic"),
              unit: "%",
            })}
          </SettingsRow>
          <SettingsRow
            title={t("settings.keepRecentContext")}
            error={fieldError("compactKeepRecent")}
          >
            {numericInput("compactKeepRecent", compactKeepRecent, onCompactKeepRecentChange, {
              label: t("settings.keepRecentContext"),
              placeholder: "20,000",
              unit: t("settings.unitTokens"),
            })}
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
      <SettingsSection title={t("settings.sectionContextWindow")}>
        <SettingsGroup>
          <SettingsRow
            title={t("settings.providerContextLimit")}
            description={`${providerContextWindowSource}${
              providerContextWindowCurrent ? `；${t("settings.currentTokenLimit", { count: providerContextWindowCurrent })}` : ""
            }`}
            error={fieldError("providerContextWindow")}
          >
            {numericInput("providerContextWindow", providerContextWindow, onProviderContextWindowChange, {
              label: t("settings.providerContextLimit"),
              placeholder: t("settings.detectAutomatically"),
              unit: t("settings.unitTokens"),
            })}
          </SettingsRow>
          <SettingsRow
            title={t("settings.unknownModelLimit")}
            hint={t("settings.unknownModelLimitDescription")}
            error={fieldError("maxContextTokens")}
          >
            {numericInput("maxContextTokens", maxContextTokens, onMaxContextTokensChange, {
              label: t("settings.unknownModelLimit"),
              placeholder: t("settings.automatic"),
              unit: t("settings.unitTokens"),
            })}
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
      <SettingsSection title={t("settings.sectionExecution")}>
        <SettingsGroup>
          <SettingsRow
            title={t("settings.maxSteps")}
            error={fieldError("maxSteps")}
          >
            {numericInput("maxSteps", maxSteps, onMaxStepsChange, { label: t("settings.maxSteps"), placeholder: t("settings.unlimited") })}
          </SettingsRow>
          <SettingsRow title={t("settings.temperature")} hint={t("settings.temperatureRange")} error={fieldError("temperature")}>
            {numericInput("temperature", temperature, onTemperatureChange, {
              label: t("settings.temperature"),
              placeholder: t("settings.automatic"),
              inputMode: "decimal",
            })}
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
      {ENABLE_PTC_SETTINGS && <SettingsSection title={t("settings.ptcTitle")} testID="settings-ptc">
        <SettingsGroup>
          <SettingsRow title={t("settings.ptcEnabled")} description={t("settings.ptcDescription")}>
            <button className="settings-switch" type="button" role="switch"
              aria-label={t("settings.ptcEnabled")} aria-checked={ptc.enabled}
              data-testid="settings-ptc-enabled" disabled={!initialized || running || ptcBusy}
              onClick={() => void savePTC({ ...ptc, enabled: !ptc.enabled })}>
              <span className="settings-switch-thumb" aria-hidden="true" />
            </button>
          </SettingsRow>
          <SettingsRow title={t("settings.ptcFamily")} description={t("settings.ptcFamilyHint")}>
            <SelectMenu triggerClassName="settings-select-trigger" ariaLabel={t("settings.ptcFamily")}
              value={ptcFamily} onChange={setPTCFamily}
              options={[
                ["gpt", "GPT"], ["codex", "Codex"], ["claude", "Claude"], ["gemini", "Gemini"],
                ["deepseek", "DeepSeek"], ["kimi", "Kimi"], ["qwen", "Qwen"],
                ["local", t("settings.ptcLocal")], ["portable", t("settings.ptcOther")],
              ].map(([value, label]) => ({ value, label }))} />
            <SelectMenu triggerClassName="settings-select-trigger" ariaLabel={t("settings.ptcFamilyMode")}
              dataTestid="settings-ptc-family-mode"
              value={ptcFamilyValue === undefined ? "inherit" : ptcFamilyValue ? "on" : "off"}
              disabled={!initialized || running || ptcBusy}
              onChange={(value) => {
                const families = { ...ptc.families };
                if (value === "inherit") delete families[ptcFamily];
                else families[ptcFamily] = value === "on";
                void savePTC({ ...ptc, families });
              }}
              options={[
                { value: "inherit", label: t("settings.ptcInherit") },
                { value: "on", label: t("settings.ptcOn") },
                { value: "off", label: t("settings.ptcOff") },
              ]} />
          </SettingsRow>
          {ptcError ? <p className="settings-error" role="alert">{ptcError}</p> : null}
        </SettingsGroup>
      </SettingsSection>}
      <SettingsSection title={t("settings.sectionGit")} testID="settings-git">
        <SettingsGroup>
          <SettingsRow
            title={t("settings.gitAttribution")}
            hint={t("settings.gitAttributionDescription")}
            error={gitAttributionError}
          >
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-checked={gitAttributionEnabled}
              data-testid="settings-git-attribution"
              disabled={!initialized || gitAttributionBusy}
              onClick={() => void toggleGitAttribution()}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
              <span className="sr-only">
                {gitAttributionEnabled ? t("settings.disableGitAttribution") : t("settings.enableGitAttribution")}
              </span>
            </button>
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  General page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsGeneralPage({
  desktopBuild,
  codexPets,
  codexPetsLoading,
  codexPetsError,
  onCodexPetsRefresh,
  onCodexPetsUpdate,
  copyState,
  onCopyVersion
}: {
  desktopBuild: DesktopBuildInfo | undefined;
  codexPets: CodexPetsSnapshot | undefined;
  codexPetsLoading: boolean;
  codexPetsError: string;
  onCodexPetsRefresh: () => Promise<CodexPetsSnapshot>;
  onCodexPetsUpdate: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
  copyState: CopyState;
  onCopyVersion: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [codexPetBusy, setCodexPetBusy] = useState(false);
  const [codexPetLocalError, setCodexPetLocalError] = useState("");
  const codexPetOptions = codexPets?.pets ?? [];
  const codexPetSelectedID = codexPets?.selected_id ?? "";
  const codexPetEnabled = Boolean(codexPets?.enabled);
  const codexPetStatus = codexPetLocalError || codexPetsError;

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
      <SettingsPageHeader title={t("settings.general")} />
      <SettingsSection testID="settings-general">
        <SettingsGroup>
          <SettingsRow title={t("settings.language")}>
            <LanguagePreferenceControl />
          </SettingsRow>
          {hostSupports("listCodexPets") ? <>
          <SettingsRow title={t("settings.codexPet")}>
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
              <span className="settings-row-control-value">{t("settings.noLocalPets")}</span>
            )}
            {/* The folder it reads belongs with the action that reads it. */}
            <button
              className="settings-button settings-button-ghost settings-icon-button"
              type="button"
              title={isTouchWebShell() ? t("settings.refreshPets") : t("settings.petSource", { path: codexPets?.home ?? "~/.wuu/pets" })}
              aria-label={t("settings.refreshPets")}
              disabled={codexPetsLoading || codexPetBusy}
              onClick={() => void refreshCodexPets()}
            >
              <RefreshCw className="icon" aria-hidden="true" />
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
          codexPetOptions.length === 0 ||
          codexPets?.errors.length ||
          codexPetStatus ? (
            <div className="settings-row settings-row-block settings-row-note">
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
        </SettingsGroup>
      </SettingsSection>

      <SettingsSection title={t("settings.about")} testID="settings-about">
        <SettingsGroup>
          <SettingsRow title={t("settings.version")}>
            <span className="settings-row-control-value">
              {desktopBuild ? versionLabel(desktopBuild.version) : t("settings.loading")}
            </span>
            <button
              className="settings-button settings-button-ghost settings-icon-button"
              type="button"
              aria-label={t("settings.copyVersion")}
              title={t(copyState === "copied" ? "settings.copied" : "settings.copyVersion")}
              onClick={() => void onCopyVersion()}
              disabled={!desktopBuild || copyState === "copying"}
            >
              {copyState === "copied" ? <Check className="icon" aria-hidden="true" /> : <Copy className="icon" aria-hidden="true" />}
            </button>
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Appearance page                                                            */
/* -------------------------------------------------------------------------- */

function SettingsAppearancePage(): JSX.Element {
  const { t } = useI18n();
  return (
    <>
      <SettingsPageHeader title={t("settings.appearance")} />
      {/* The background image is part of the look the theme sets. */}
      <SettingsSection title={t("settings.sectionTheme")} testID="settings-appearance">
        <ThemePreferenceControl />
        {isTouchWebShell() ? null : (
          <div className="settings-theme-background" data-testid="settings-background">
            <SettingsGroup><BackgroundSettings /></SettingsGroup>
          </div>
        )}
      </SettingsSection>
      <AppearanceTypography section="text" />
      <AppearanceTypography section="motion" />
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  MCP servers page                                                           */
/* -------------------------------------------------------------------------- */

function SettingsMCPPage({
  initialized,
  running,
  mcpServers,
  mcpLoading,
  mcpError,
  mcpBusyServer,
  onGeneralSave,
  onMCPAction,
  onMCPAuthStart,
  onMCPAuthFinish,
  onMCPAuthRemove,
}: {
  initialized: InitializeResult | undefined;
  running: boolean;
  mcpServers: MCPServerStatus[];
  mcpLoading: boolean;
  mcpError: string;
  mcpBusyServer: string;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onMCPAction: (name: string, action: "connect" | "disconnect" | "refresh") => Promise<void>;
  onMCPAuthStart: (name: string) => Promise<MCPAuthStartResult | undefined>;
  onMCPAuthFinish: (name: string, state: string, code: string) => Promise<boolean>;
  onMCPAuthRemove: (name: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const configuredMCPEnabled = initialized?.general_settings?.mcp_server_enabled ?? {};
  const configuredMCPKey = stableBoolRecordSignature(configuredMCPEnabled);
  const [mcpEnabledDraft, setMCPEnabledDraft] = useState<Record<string, boolean>>(() => ({ ...configuredMCPEnabled }));
  const [mcpToggleBusy, setMCPToggleBusy] = useState("");
  const [mcpToggleError, setMCPToggleError] = useState("");
  const [mcpAuthStates, setMCPAuthStates] = useState<Record<string, string>>({});
  const [mcpAuthCodes, setMCPAuthCodes] = useState<Record<string, string>>({});

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
      setMCPToggleError(toastErrorMessage(toggleError, t("settings.saveFailed")));
    } finally {
      setMCPToggleBusy("");
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

  return (
    <>
      <SettingsPageHeader title={t("settings.mcpServers")} description={t("settings.mcpDescription")} />
      <SettingsSection testID="settings-mcp">
        <SettingsGroup>
          {mcpLoading && mcpRowNames.length === 0 ? (
            <p className="settings-group-empty">{t("settings.loading")}</p>
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
                  description={server ? (
                    <>
                      <MCPStateMark state={server.state} />
                      {formatMCPServerMeta(server, t)}
                    </>
                  ) : undefined}
                  error={server?.error}
                >
                  {server ? (
                    oauthPending ? (
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
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          title={t("mcp.finishLogin")}
                          aria-label={t("mcp.finishLoginNamed", { name })}
                          disabled={busy || oauthCode.trim() === ""}
                          onClick={() => void completeMCPAuth(name)}
                        >
                          <Check className="icon" aria-hidden="true" />
                        </button>
                        <button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          title={t("mcp.cancelLogin")}
                          aria-label={t("mcp.cancelLoginNamed", { name })}
                          disabled={busy}
                          onClick={() => {
                            setMCPAuthStates((states) => withoutRecordKey(states, name));
                            setMCPAuthCodes((codes) => withoutRecordKey(codes, name));
                          }}
                        >
                          <X className="icon" aria-hidden="true" />
                        </button>
                      </>
                    ) : (
                      <div className="settings-row-actions">
                        {server.auth_status === "not_logged_in" ? (
                          <button
                            className="settings-button settings-button-ghost settings-icon-button"
                            type="button"
                            title={t("mcp.oauthLogin")}
                            aria-label={t("mcp.loginNamed", { name })}
                            disabled={busy || disabledByConfig}
                            onClick={() => void beginMCPAuth(name)}
                          >
                            <KeyRound className="icon" aria-hidden="true" />
                          </button>
                        ) : server.auth_status === "oauth" ? (
                          <button
                            className="settings-button settings-button-ghost settings-icon-button"
                            type="button"
                            title={t("mcp.removeLogin")}
                            aria-label={t("mcp.removeLoginNamed", { name })}
                            disabled={busy}
                            onClick={() => void onMCPAuthRemove(name)}
                          >
                            <LogOut className="icon" aria-hidden="true" />
                          </button>
                        ) : null}
                        <button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          title={t("mcp.refresh")}
                          aria-label={t("mcp.refreshNamed", { name })}
                          disabled={busy || disabledByConfig}
                          onClick={() => void onMCPAction(name, "refresh")}
                        >
                          <RefreshCw className="icon" aria-hidden="true" />
                        </button>
                        <button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          title={disabledByConfig ? t("mcp.disabledByConfig") : connected ? t("mcp.disconnect") : t("mcp.connect")}
                          aria-label={`${connected ? t("mcp.disconnect") : t("mcp.connect")} ${name}`}
                          disabled={busy || disabledByConfig}
                          onClick={() => void onMCPAction(name, connected ? "disconnect" : "connect")}
                        >
                          {connected ? <PlugZap className="icon" aria-hidden="true" /> : <Plug className="icon" aria-hidden="true" />}
                        </button>
                      </div>
                    )
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
                </SettingsRow>
              );
            })
          ) : (
            <p className="settings-group-empty">{t("settings.noMcpServers")}</p>
          )}
        </SettingsGroup>
        {mcpError ? <p className="settings-error" role="alert">{mcpError}</p> : null}
        {mcpToggleError ? <p className="settings-error" role="alert">{mcpToggleError}</p> : null}
      </SettingsSection>
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Archive page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsArchivePage({
  archivedThreads,
  onUnarchiveThread,
}: {
  archivedThreads: readonly ArchivedSessionView[];
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [query, setQuery] = useState("");
  const [workspaceFilter, setWorkspaceFilter] = useState("all");
  const sortedThreads = useMemo(
    () => [...archivedThreads].sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [archivedThreads],
  );
  const workspaceOptions = useMemo(() => {
    const seen = new Set<string>();
    return sortedThreads.flatMap((thread) => {
      const workspaceID = archiveWorkspaceID(thread);
      if (seen.has(workspaceID)) {
        return [];
      }
      seen.add(workspaceID);
      return [{ value: workspaceID, label: archiveWorkspaceName(thread, t("settings.noWorkspace")) }];
    });
  }, [sortedThreads, t]);
  const groups = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase();
    const grouped = new Map<
      string,
      { projectName: string; threads: ArchivedSessionView[] }
    >();
    for (const thread of sortedThreads) {
      const workspaceID = archiveWorkspaceID(thread);
      const title = archiveThreadTitle(thread, t("settings.untitledConversation"));
      if (workspaceFilter !== "all" && workspaceID !== workspaceFilter) {
        continue;
      }
      if (normalizedQuery && !title.toLocaleLowerCase().includes(normalizedQuery)) {
        continue;
      }
      const group = grouped.get(workspaceID) ?? {
        projectName: archiveWorkspaceName(thread, t("settings.noWorkspace")),
        threads: [],
      };
      group.threads.push(thread);
      grouped.set(workspaceID, group);
    }
    return Array.from(grouped, ([workspaceID, group]) => ({ workspaceID, ...group }));
  }, [workspaceFilter, query, sortedThreads, t]);
  const noMatches = sortedThreads.length > 0 && groups.length === 0;

  return (
    <>
      <SettingsPageHeader title={t("settings.archive")} />
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
            triggerClassName="settings-select-trigger"
            value={workspaceFilter}
            onChange={setWorkspaceFilter}
            ariaLabel={t("settings.archiveWorkspaceFilter")}
            options={[{ value: "all", label: t("settings.allWorkspaces") }, ...workspaceOptions]}
            flip
          />
        </div>
        {sortedThreads.length === 0 || noMatches ? (
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
            {groups.map((group) => (
              <section className="settings-archive-group" key={group.workspaceID}>
                <header className="settings-archive-group-header">
                  <div className="settings-archive-group-name">
                    <Folder className="icon" aria-hidden="true" />
                    <span>{group.projectName}</span>
                  </div>
                  <span
                    className="settings-archive-group-count"
                    aria-label={t("settings.conversationCount", { count: group.threads.length })}
                  >
                    {group.threads.length}
                  </span>
                </header>
                <div className="settings-group settings-archive-list">
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
                          className="settings-button settings-button-ghost settings-icon-button settings-archive-restore"
                          aria-label={t("settings.restoreConversation", { title })}
                          title={t("settings.restore")}
                          onClick={() => onUnarchiveThread(thread)}
                        >
                          <RotateCcw className="icon" aria-hidden="true" />
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
    </>
  );
}

function archiveThreadTitle(thread: ArchivedSessionView, fallback: string): string {
  return (thread.title ?? "").trim() || fallback;
}

function archiveWorkspaceID(thread: ArchivedSessionView): string {
  return thread.archive_project_id?.trim() || "no-project";
}

function archiveWorkspaceName(thread: ArchivedSessionView, fallback: string): string {
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
  // The year only appears once it differs from this one.
  return formatter(date, {
    ...(date.getFullYear() === new Date().getFullYear() ? {} : { year: "numeric" }),
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
  const header = <SettingsPageHeader title={t("settings.usage")} />;
  if (loading) {
    return (
      <>
        {header}
        <div className="settings-usage-page settings-usage-loading" data-testid="settings-usage" aria-busy="true">
          <SettingsUsageSkeleton />
        </div>
      </>
    );
  }
  if (!usage) {
    return (
      <>
        {header}
        <div className="settings-usage-page" data-testid="settings-usage">
          <div className="settings-empty" role={error ? "alert" : undefined}>
            {error || t("settings.noUsage")}
          </div>
        </div>
      </>
    );
  }
  return (
    <>
    {header}
    <div className="settings-usage-page" data-testid="settings-usage">
      <div className="settings-group settings-usage-stats">
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

      <section className="settings-section settings-usage-chart" aria-labelledby="settings-usage-trend-title">
        <header className="settings-section-header">
          <h2 id="settings-usage-trend-title" className="settings-section-title">
            {t("settings.usageTrend")}
          </h2>
          <span className="settings-section-meta">{t("settings.last30Days")}</span>
        </header>
        <div className="settings-group settings-usage-card">
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
        </div>
      </section>

      <section className="settings-section" aria-labelledby="settings-usage-heatmap-title">
        <header className="settings-section-header">
          <h2 id="settings-usage-heatmap-title" className="settings-section-title">
            {t("settings.usageHeatmap")}
          </h2>
        </header>
        <div className="settings-group settings-usage-card settings-heatmap-panel">
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
      </section>

      <section className="settings-section settings-skill-usage" aria-labelledby="settings-skill-usage-title">
        <header className="settings-section-header">
          <h2 id="settings-skill-usage-title" className="settings-section-title">
            {t("settings.skillUsage")}
          </h2>
          <span className="settings-section-meta">{t("settings.skillUsageCount")}</span>
        </header>
        <div className="settings-group settings-usage-card">
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
            <p className="settings-group-empty">{t("settings.noSkillUsage")}</p>
          )}
        </div>
      </section>

      {modelChart.length > 0 ? (
        <section className="settings-section settings-model-chart" aria-labelledby="settings-model-chart-title">
          <header className="settings-section-header">
            <h2 id="settings-model-chart-title" className="settings-section-title">
              {t("settings.modelDistribution")}
            </h2>
            <span className="settings-section-meta">{t("settings.tokenShare")}</span>
          </header>
          <div className="settings-group settings-usage-card">
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
          </div>
        </section>
      ) : null}

      <section className="settings-section" aria-labelledby="settings-model-usage-title">
        <header className="settings-section-header">
          <h2 id="settings-model-usage-title" className="settings-section-title">{t("settings.modelUsage")}</h2>
        </header>
        {usage.model_breakdowns.length > 0 ? (
          <div className="settings-group settings-usage-table-wrap">
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
                      <td className="settings-usage-num" data-label={t("settings.usageInput")}>
                        <Tooltip content={formatUsageValue(b.input_tokens)}>
                          <span className="settings-usage-number">
                            {formatCompactUsageNumber(b.input_tokens, locale)}
                          </span>
                        </Tooltip>
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.usageOutput")}>
                        <Tooltip content={formatUsageValue(b.output_tokens)}>
                          <span className="settings-usage-number">
                            {formatCompactUsageNumber(b.output_tokens, locale)}
                          </span>
                        </Tooltip>
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.hitRate")}>
                        <span className="settings-usage-number">{formatPercent(rate)}</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="settings-group">
            <p className="settings-group-empty">{t("settings.noUsage")}</p>
          </div>
        )}
      </section>
    </div>
    </>
  );
}

function SettingsUsageSkeleton(): JSX.Element {
  return (
    <>
      <div className="settings-group settings-usage-stats settings-usage-skeleton-stats" aria-hidden="true">
        {[0, 1, 2, 3].map((item) => (
          <div className="settings-usage-stat" key={item}>
            <span className={`settings-usage-skeleton-line settings-usage-skeleton-stat-value settings-usage-skeleton-stat-value-${item}`} />
            <span className="settings-usage-skeleton-line settings-usage-skeleton-stat-label" />
          </div>
        ))}
      </div>
      <section className="settings-section" aria-hidden="true">
        <div className="settings-section-header">
          <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
          <span className="settings-usage-skeleton-line settings-usage-skeleton-period" />
        </div>
        <div className="settings-group settings-usage-card">
          <div className="settings-usage-skeleton-trend">
            {[24, 28, 34, 30, 38, 44, 50, 46, 40, 34, 38, 46, 54, 62, 56, 48, 42, 46, 52, 60, 68, 62, 54, 48, 42, 46, 54, 60, 56, 50].map((height, index) => (
              <i className="settings-usage-skeleton-trend-day" key={index} style={{ height: `${height}%` }} />
            ))}
          </div>
          <div className="settings-usage-skeleton-axis">
            <span className="settings-usage-skeleton-line" />
            <span className="settings-usage-skeleton-line" />
          </div>
        </div>
      </section>
      <section className="settings-section" aria-hidden="true">
        <div className="settings-section-header">
          <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
        </div>
        <div className="settings-group settings-usage-card settings-usage-skeleton-heatmap">
          <div className="settings-usage-skeleton-months">
            {[0, 1, 2, 3].map((item) => <span className="settings-usage-skeleton-line" key={item} />)}
          </div>
          <div className="settings-usage-skeleton-grid">
            {Array.from({ length: 53 * 7 }, (_, index) => <i key={index} />)}
          </div>
        </div>
      </section>
      <section className="settings-section" aria-hidden="true">
        <div className="settings-section-header">
          <span className="settings-usage-skeleton-line settings-usage-skeleton-heading" />
          <span className="settings-usage-skeleton-line settings-usage-skeleton-period" />
        </div>
        <div className="settings-group settings-usage-card settings-usage-skeleton-list">
          {[0, 1, 2, 3].map((item) => (
            <div className="settings-usage-skeleton-row" key={item}>
              <span className="settings-usage-skeleton-line" />
              <span className="settings-usage-skeleton-line" />
              <span className="settings-usage-skeleton-line" />
            </div>
          ))}
        </div>
      </section>
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

function formatUsageNumber(value: number | undefined, formatNumber: (value: number) => string): string {
  return value === undefined || !Number.isFinite(value) ? "—" : formatNumber(value);
}


/* -------------------------------------------------------------------------- */
/*  Helpers (kept at module scope, no behavior change)                         */
/* -------------------------------------------------------------------------- */

type Translate = ReturnType<typeof useI18n>["t"];

function settingsPageTitle(page: NativeSettingsPage, t: Translate): string {
  switch (page) {
    case "providers":
      return t("settings.providers");
    case "agents":
      return t("settings.agents");
    case "subscriptions":
      return t("settings.subscriptions");
    case "advanced":
      return t("settings.runtime");
    case "general":
      return t("settings.general");
    case "appearance":
      return t("settings.appearance");
    case "remote":
      return t("settings.remote");
    case "mcp":
      return t("settings.mcpServers");
    case "usage":
      return t("settings.usage");
    case "archive":
      return t("settings.archive");
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

// A connected or idle server shows only its tools; the connect control and
// the switch already say whether it runs. Work in progress and states that
// need a look get one mark named by the state.
function MCPStateMark({ state }: { state: string }): JSX.Element | null {
  const { t } = useI18n();
  const tone = mcpStateTone(state);
  const busy = state === "starting" || state === "connecting" || state === "reconnecting";
  if (!busy && tone !== "danger" && tone !== "warning") return null;
  const label = mcpStateLabel(state, t);
  return (
    <span className="settings-row-attention" data-tone={busy ? "neutral" : tone} role="img" aria-label={label} title={label}>
      {busy ? <Loader2 className="icon-sm settings-spin" aria-hidden="true" />
        : tone === "danger" ? <AlertCircle className="icon-sm" aria-hidden="true" />
        : <AlertTriangle className="icon-sm" aria-hidden="true" />}
    </span>
  );
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

function mcpStateTone(state: string): SettingsStatusTone {
  switch (state) {
    case "ready":
    case "connected":
      return "success";
    case "error":
    case "failed":
      return "danger";
    case "auth_required":
    case "needs_auth":
    case "needs_client_registration":
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
