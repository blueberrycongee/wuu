import { useSettingsControls, SettingsControlsProvider } from "./SettingsControlsContext";
import { SettingsMCPPage } from "./SettingsMCPPage";
import { isTouchWebShell } from "./ComposerFocus";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import {
  ArrowLeft,
  Archive,
  BarChart3,
  Bot,
  Check,
  Copy,
  Folder,
  Globe,
  KeyRound,
  LayoutDashboard,
  Loader2,
  Monitor,
  Plug,
  RefreshCw,
  RotateCcw,
  Search,
  Settings,
  Smartphone,
  Trash2,
  type IconComponent
} from "./WuuIcons";
import { DESKTOP_AUTO_ARCHIVE_DAYS, normalizeProxyURL } from "../shared/protocol";
import type {
  CodexPetSettingsUpdate,
  DesktopAutoArchiveDays,
  DesktopFolder,
  DesktopPreferences,
  DesktopUpdateState,
  EngineListResult,
  ExtensionInventoryRecord,
  EngineUpdateParams,
  RuntimeAdvancedSettingsUpdate,
  RuntimeGeneralSettingsUpdate,
  SystemPermissionsState,
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
import { isConfigurablePlugin } from "./SettingsPageAvailability";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { useSidebarDrawerState, type SidebarDrawerPhase } from "./SidebarDrawerState";
import { sidebarDrawerExitMs, sidebarMotionMs } from "./AppLayoutState";
import { SelectMenu } from "./SelectMenu";
import { providerModelVariantOptions, variantLabel } from "./RuntimeHelpers";
import type {
  CodexPetsSnapshot,
  DesktopBuildInfo,
  InitializeResult,
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
import { AppearanceTypography } from "./AppearanceTypography";
import { ShellSkinSettings } from "./background/ShellSkinSettings";
import { ShellSkinGallery } from "./background/ShellSkinGallery";
import { SettingsRow } from "./SettingsRow";
import { SettingsEmptyState, SettingsGroup, SettingsPageHeader, SettingsSection, SettingsTitlebarSlot } from "./SettingsSection";
import { pluginSourceLabel } from "./SkillsCatalog";
import { AppContextSettingsSection } from "./AppContextSettingsSection";
import { Switch } from "./ui/Switch";
import { SegmentedControl } from "./ui/SegmentedControl";
import { updateDesktopPreferences, useDesktopPreferences } from "./desktopPreferences";
import { BrowserSettingsSection } from "./BrowserSettingsSection";
import { ENABLE_APP_SNAPSHOTS } from "../shared/featureFlags";
import { SystemPermissionsSection } from "./SystemPermissions";
import { toastErrorMessage } from "./Toast";
import type { ArchiveDeletionState } from "./useArchiveDeletion";
import { ModelServicesPage } from "./ModelServicesPage";
import { SettingsRemotePageContainer } from "./SettingsRemotePageContainer";
import { ContentThemeControl, ThemePreferenceControl } from "./ThemePreferenceSection";
import { LanguagePreferenceControl } from "./LanguagePreferenceSection";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import {
  buildUsageHeatmap,
  buildUsageTrend,
  formatCompactUsageNumber,
  usageTokenTotal,
  type UsageHeatmapCell,
} from "./UsageActivity";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { PluginIcon } from "./PublicIcon";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import type { SettingsPageHostAPI, SettingsPageSummaryV1, SettingsValueMapV1 } from "../shared/workbench";

type NativeSettingsPage =
  | "providers"
  | "agents"
  | "subscriptions"
  | "general"
  | "appearance"
  | "browser"
  | "remote"
  | "mcp"
  | "usage"
  | "archive";

export type SettingsPage =
  | NativeSettingsPage
  // Legacy links resolve to General after the built-in agent page was removed.
  | "advanced"
  | `plugin-settings:${string}`
  | `plugin-view:${string}:${string}`;

// Page ids are part of the plugin settings snapshot, so they stay stable when
// labels or grouping change.
const NATIVE_PAGE_ICONS: Record<NativeSettingsPage, IconComponent> = {
  providers: KeyRound,
  agents: Bot,
  subscriptions: LayoutDashboard,
  general: Settings,
  appearance: Monitor,
  browser: Globe,
  remote: Smartphone,
  mcp: Plug,
  usage: BarChart3,
  archive: Archive,
};

// Page-level discovery for the rail search. The terms cover the setting names
// inside each page, not only the page title, so "font" or "Git" still
// finds the right page.
const NATIVE_PAGE_SEARCH_KEY: Record<NativeSettingsPage, Parameters<Translate>[0]> = {
  providers: "settings.searchKeywords.providers",
  agents: "settings.searchKeywords.agents",
  subscriptions: "settings.searchKeywords.subscriptions",
  general: "settings.searchKeywords.general",
  appearance: "settings.searchKeywords.appearance",
  browser: "settings.searchKeywords.browser",
  remote: "settings.searchKeywords.remote",
  mcp: "settings.searchKeywords.mcp",
  usage: "settings.searchKeywords.usage",
  archive: "settings.searchKeywords.archive",
};

// App-level preferences stay at the top of the rail; less frequent setup follows
// under labeled groups. Page IDs stay flat for plugin snapshots and deep links.
type NativeSettingsGroup = {
  id: string;
  labelKey?: Parameters<Translate>[0];
  pages: NativeSettingsPage[];
};

function nativeSettingsGroups(browserSettingsAllowed: boolean, capabilities: import("../../../packages/plugin-sdk/src/ui-settings-controls").SettingsControlsCapabilitiesV1): NativeSettingsGroup[] {
  return [
    {
      id: "app",
      pages: ["general", "appearance"],
    },
    {
      id: "connections",
      labelKey: "settings.navConnections",
      pages: [
        "providers",
        "mcp",
        ...(browserSettingsAllowed && capabilities.browser ? ["browser" as const] : []),
        ...(capabilities.remote ? ["remote" as const] : []),
      ],
    },
    {
      id: "data",
      labelKey: "settings.navData",
      pages: ["usage", "archive"],
    },
  ];
}

type CopyState = "idle" | "copying" | "copied";

const COPY_RESET_MS = 1500;

function SettingsViewContent({
  browserSettingsAllowed = true,
  embedded = false,
  floating = false,
  embeddedDrawerPhase,
  onSidebarPointerEnter,
  onSidebarPointerLeave,
  initialized,
  initialPage,
  page,
  onPageChange,
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
  onProvidersChanged,
  onProviderModelsChanged,
  onRefreshEngineInventory,
  onUpdateEngineInventory,
  onAdvancedSave,
  onGeneralSave,
  onCodexPetsRefresh,
  onCodexPetsUpdate,
  onSidebarResizeStart,
  onSidebarResizerPointer,
  onSidebarSeparatorKey,
  archivedThreads,
  onUnarchiveThread,
  archiveDeletion,
  onDeleteAllArchivedThreads,
  onRetryArchiveDeletion,
  // The settings rail shares the main sidebar's state and handlers wholesale:
  // same persisted width + collapse flag, same drag-to-collapse resize
  // session, same toggle motion.
  sidebarCollapsed,
  sidebarAnimating,
  onToggleSidebar,
}: {
  browserSettingsAllowed?: boolean;
  embedded?: boolean;
  floating?: boolean;
  embeddedDrawerPhase?: SidebarDrawerPhase;
  onSidebarPointerEnter?: () => void;
  onSidebarPointerLeave?: (event: ReactPointerEvent<HTMLElement>) => void;
  initialized?: InitializeResult;
  initialPage?: SettingsPage;
  page?: SettingsPage;
  onPageChange?: (page: SettingsPage) => void;
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
  onSave: (provider: string, model: string, effort?: string, connection?: RuntimeConnectionUpdate, variant?: string, permissionMode?: string) => Promise<void>;
  onRemoveProvider: (provider: string, options?: { fallbackProvider?: string; fallbackModel?: string }) => Promise<void>;
  onRefreshModelCatalog: () => Promise<void>;
  onProvidersChanged?: (providers: NonNullable<InitializeResult["providers"]>) => void;
  onProviderModelsChanged?: (name: string, accountId: string, models: NonNullable<NonNullable<InitializeResult["providers"]>[number]["models"]>) => void;
  onRefreshEngineInventory: () => Promise<EngineListResult | undefined>;
  onUpdateEngineInventory: (params: EngineUpdateParams) => Promise<EngineListResult>;
  onAdvancedSave: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onCodexPetsRefresh: () => Promise<CodexPetsSnapshot>;
  onCodexPetsUpdate: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
  onSidebarResizeStart: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onSidebarResizerPointer?: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onSidebarSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
  // The archive catalog spans all workspaces; filtering only changes the view.
  archivedThreads?: readonly ArchivedSessionView[];
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
  archiveDeletion?: ArchiveDeletionState;
  onDeleteAllArchivedThreads?: () => void;
  onRetryArchiveDeletion?: () => void;
  sidebarCollapsed: boolean;
  sidebarAnimating: boolean;
  onToggleSidebar: () => void;
}): JSX.Element {
  const controls = useSettingsControls();
  const loadPluginIcon = useMemo(() => controls.loadPluginIconURL.bind(controls), [controls]);
  const { t } = useI18n();
  const providers = initialized?.providers ?? [];
  const runningProviderNameSet = useMemo(
    () => new Set((runningProviderNames ?? []).map((name) => name.trim()).filter(Boolean)),
    [runningProviderNames],
  );
  const [desktopBuild, setDesktopBuild] = useState<DesktopBuildInfo | undefined>();
  // The page subtree remounts on navigation. Keep the last permission read
  // so returning to General does not blank the rows before the refresh lands.
  const permissionStateCache = useMemo<RefObject<SystemPermissionsState | undefined>>(() => ({ current: undefined }), [controls]);
  const [localPage, setActivePage] = useState<SettingsPage>(() =>
    availableSettingsPage(initialPage, browserSettingsAllowed, controls.capabilities),
  );
  const activePage = availableSettingsPage(page ?? localPage, browserSettingsAllowed, controls.capabilities);
  const [navQuery, setNavQuery] = useState("");
  const customPluginSettingsPages = useSyncExternalStore(
    (listener) => controls.subscribe(listener),
    () => controls.getPages(),
    () => controls.getPages(),
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
    const availableModels = Object.freeze((initialized?.providers ?? []).flatMap((provider) =>
      [...new Set([provider.model, ...(provider.models ?? []).map((model) => model.id)].filter(Boolean))].map((model) => {
        const entry = (provider.models ?? []).find((candidate) => candidate.id === model);
        return {
          provider: provider.name, model, label: entry?.display_name || model,
          supportsTools: entry?.capabilities?.tools,
          variants: (entry?.variants?.length ? entry.variants.map((variant) => variant.id) : entry?.supported_efforts ?? []).filter(Boolean),
        };
      }),
    ));
    return Object.freeze({
      contractVersion: 1 as const,
      listAvailableModels: () => availableModels,
      getValue: (key: "runtime.modelAliases") => {
        if (key !== "runtime.modelAliases") throw new Error(`Unsupported settings value: ${key}`);
        return modelAliases;
      },
      updateValue: async (key: "runtime.modelAliases", value: SettingsValueMapV1["runtime.modelAliases"]) => {
        if (key !== "runtime.modelAliases") throw new Error(`Unsupported settings value: ${key}`);
        await onAdvancedSave({ model_aliases: value });
      },
    });
  }, [initialized?.model_aliases, initialized?.providers, onAdvancedSave]);
  const [mcpStatus, setMCPStatus] = useState({ busy: false, hasError: false });
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const settingsScrollRef = useRef<HTMLDivElement>(null);
  const [titlebarSlot, setTitlebarSlot] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    setActivePage(availableSettingsPage(initialPage, browserSettingsAllowed, controls.capabilities));
  }, [initialPage, browserSettingsAllowed]);

  useLayoutEffect(() => {
    if (page !== undefined && activePage !== page) onPageChange?.(activePage);
  }, [activePage, page, onPageChange]);

  useLayoutEffect(() => {
    const missingGeneratedPage = activePage.startsWith("plugin-settings:")
      && activePluginSettingsRecord === undefined;
    const missingCustomPage = activePage.startsWith("plugin-view:")
      && activeCustomPluginPage === undefined;
    if (missingGeneratedPage || missingCustomPage) {
      setActivePage("providers");
      onPageChange?.("providers");
    }
  }, [activeCustomPluginPage, activePage, activePluginSettingsRecord, onPageChange]);

  useLayoutEffect(() => {
    if (settingsScrollRef.current) {
      settingsScrollRef.current.scrollTop = 0;
    }
  }, [activePage]);

  useEffect(() => {
    let cancelled = false;
    if (!controls.capabilities.build) return;
    void controls.getBuildInfo().then((info) => {
      if (!cancelled) {
        setDesktopBuild(info.desktop);
      }
    }).catch((error: unknown) => {
      if (!cancelled && !isPluginLifecycleCancellation(error)) console.error("Failed to read desktop build information", error);
    });
    return () => {
      cancelled = true;
    };
  }, [controls]);

  const core = initialized?.core;
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

  // Mirror the main view's sidebar collapse/hover logic so the settings shell
  // behaves the same way: a persistent `sidebarCollapsed` flag hides the rail
  // and only the left-edge hover zone can reopen it as a drawer overlay.
  // The toggle explicitly pins or collapses it. Navigating between settings pages does not close it;
  // pointer exit and window-level dismissal remain the close signals.
  const fallbackShellRef = useRef<HTMLDivElement>(null);
  const effectiveShellRef = shellRef ?? fallbackShellRef;
  const {
    sidebarDrawerPhase: standaloneDrawerPhase,
    sidebarHoverZoneRef,
    scheduleSidebarDrawerOpen,
    cancelSidebarDrawerOpen,
    openSidebarDrawer,
    closeSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave
  } = useSidebarDrawerState({
    appShellRef: effectiveShellRef,
    // A floating settings panel always keeps its own navigation visible; the
    // outer window's rail state must not turn the panel rail into a drawer.
    sidebarCollapsed: !embedded && !floating && sidebarCollapsed,
    resizingSidebar,
    motionMs: sidebarDrawerExitMs,
    dockingMotionMs: sidebarMotionMs,
    closeOnWindowResize: true
  });
  const sidebarDrawerPhase = floating ? "closed" : embedded ? embeddedDrawerPhase : standaloneDrawerPhase;
  const railCollapsed = floating ? false : sidebarCollapsed;
  const shellStyle = {
    // Same variables as the main app shell (`--sidebar-width` collapses to 0,
    // `--sidebar-open-width` remembers the open width for the hover drawer)
    // so sidebar.css and settings.css read one vocabulary for both shells.
    "--sidebar-width": `${floating ? 248 : railCollapsed ? 0 : sidebarWidth}px`,
    "--sidebar-open-width": `${floating ? 248 : sidebarWidth}px`
  } as CSSProperties;
  const shellClassName = `settings-shell${embedded ? " settings-embedded" : ""}${floating ? " settings-floating" : ""}${resizingSidebar ? " resizing-sidebar" : ""}${
    railCollapsed ? " sidebar-collapsed" : ""
  }${
    railCollapsed && sidebarDrawerPhase === "open" ? " sidebar-drawer-open" : ""
  }${
    railCollapsed && sidebarDrawerPhase === "closing"
      ? " sidebar-drawer-closing"
      : ""
  }${
    !railCollapsed && sidebarDrawerPhase === "docking"
      ? " sidebar-drawer-docking"
      : ""
  }${sidebarAnimating && !floating ? " sidebar-animating" : ""}`;

  const pluginPageTitle = activePluginSettingsRecord?.name ?? activeCustomPluginPage?.title;
  const crumbTitle = pluginPageTitle
    ?? (activePage in NATIVE_PAGE_SEARCH_KEY
      ? settingsPageTitle(activePage as NativeSettingsPage, t)
      : "");
  const nativeGroups = nativeSettingsGroups(browserSettingsAllowed, controls.capabilities);
  const nativePages = nativeGroups.flatMap((group) => group.pages);
  const normalizedNavQuery = navQuery.trim().toLocaleLowerCase();
  const pluginSearchTerms = `${t("settings.navPlugins")} plugin 插件`.toLocaleLowerCase();
  const pageMatchesNavQuery = (page: NativeSettingsPage): boolean => {
    if (!normalizedNavQuery) {
      return true;
    }
    const connectionTerms = page === "providers"
      ? `${t(NATIVE_PAGE_SEARCH_KEY.agents)} ${t(NATIVE_PAGE_SEARCH_KEY.subscriptions)}` : "";
    return `${settingsPageTitle(page, t)} ${t(NATIVE_PAGE_SEARCH_KEY[page])} ${connectionTerms}`
      .toLocaleLowerCase()
      .includes(normalizedNavQuery);
  };
  const visibleNativeGroups = nativeGroups
    .map((group) => ({ ...group, pages: group.pages.filter(pageMatchesNavQuery) }))
    .filter((group) => group.pages.length > 0);
  const visiblePluginSettingsRecords = normalizedNavQuery
    ? pluginSettingsRecords.filter((plugin) =>
        `${plugin.name} ${pluginSearchTerms}`.toLocaleLowerCase().includes(normalizedNavQuery),
      )
    : pluginSettingsRecords;
  const visibleCustomPluginSettingsPages = normalizedNavQuery
    ? customPluginSettingsPages.filter((entry) =>
        `${entry.title} ${pluginSearchTerms}`.toLocaleLowerCase().includes(normalizedNavQuery),
      )
    : customPluginSettingsPages;
  const hasVisiblePluginPages = visiblePluginSettingsRecords.length > 0
    || visibleCustomPluginSettingsPages.length > 0;
  const hasVisiblePages = visibleNativeGroups.length > 0 || hasVisiblePluginPages;
  const availablePages = useMemo<readonly SettingsPageSummaryV1[]>(() => Object.freeze([
    ...nativePages.map((page) => Object.freeze({
      id: page,
      label: settingsPageTitle(page, t),
    })),
    ...pluginSettingsRecords.map((plugin) => Object.freeze({
      id: pluginSettingsPageId(plugin.id),
      label: plugin.name,
    })),
    ...customPluginSettingsPages.map((entry) => Object.freeze({
      id: pluginViewSettingsPageId(entry.pluginId, entry.id),
      label: entry.title,
    })),
    // Apart from the window's browser-settings scope, nativePages is derived
    // from build flags and host capabilities that stay fixed while mounted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  ]), [browserSettingsAllowed, customPluginSettingsPages, pluginSettingsRecords, t]);

  function selectPage(nextPage: SettingsPage): void {
    const next = availableSettingsPage(nextPage, browserSettingsAllowed, controls.capabilities);
    if (next === activePage) return;
    setActivePage(next);
    onPageChange?.(next);
  }

  // The floating panel is a modal surface. Capture the opener, move focus into
  // the active destination, and return focus when the panel leaves. This also
  // keeps keyboard users inside the panel while its scrim covers the app.
  const floatingOpenerRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!floating) return;
    const shell = effectiveShellRef.current;
    if (!shell) return;
    const opener = document.activeElement;
    floatingOpenerRef.current = opener instanceof HTMLElement ? opener : null;
    const focusTarget = shell.querySelector<HTMLElement>(".settings-nav-item.active") ?? shell;
    const frame = window.requestAnimationFrame(() => focusTarget.focus({ preventScroll: true }));
    return () => {
      window.cancelAnimationFrame(frame);
      const previous = floatingOpenerRef.current;
      floatingOpenerRef.current = null;
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    };
  }, [floating]);

  function handleFloatingKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (!floating) return;
    if (event.key === "Escape") {
      if (event.defaultPrevented) return;
      const shell = effectiveShellRef.current;
      if (!shell) return;
      // Let menus and nested dialogs consume Escape first. Their state is
      // rendered in the same panel or in the shared overlay portal.
      if (shell.querySelector('[role="dialog"], .select-menu-panel, [data-floating-menu-owner]')) return;
      if ([...document.querySelectorAll('[aria-modal="true"]')].some(
        (element) => element !== shell && !shell.contains(element),
      )) return;
      event.preventDefault();
      onBack();
      return;
    }
    if (event.key !== "Tab") return;
    const shell = effectiveShellRef.current;
    if (!shell) return;
    if ([...document.querySelectorAll('[aria-modal="true"]')].some(
      (element) => element !== shell && !shell.contains(element),
    )) return;
    const focusable = [...shell.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    )].filter((element) => element.getAttribute("aria-hidden") !== "true" && !element.closest("[inert]"));
    if (focusable.length === 0) {
      event.preventDefault();
      shell.focus({ preventScroll: true });
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && (document.activeElement === first || document.activeElement === shell)) {
      event.preventDefault();
      last.focus({ preventScroll: true });
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus({ preventScroll: true });
    }
  }

  const sidebarToggle = (
    <button
      type="button"
      className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle settings-sidebar-toggle"
      data-wuu-component="sidebar-toggle"
      aria-label={sidebarCollapsed ? t("settings.expandSidebar") : t("settings.collapseSidebar")}
      aria-pressed={!sidebarCollapsed}
      onClick={() => { closeSidebarDrawer(); onToggleSidebar(); }}
      onPointerLeave={(event) =>
        scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
      }
    >
      <SidePanelToggleIcon side="left" open={!sidebarCollapsed} />
    </button>
  );

  const nativeSettings = (
    <div
      ref={effectiveShellRef}
      className={shellClassName}
      style={shellStyle}
      data-wuu-component="settings-shell"
      {...(floating ? {
        role: "dialog",
        "aria-modal": true,
        "aria-label": t("settings.navigation"),
        "data-settings-floating": "",
        tabIndex: -1,
        onKeyDown: handleFloatingKeyDown,
      } : {})}
    >
      {!embedded && !floating ? <div
        ref={sidebarHoverZoneRef}
        className="sidebar-hover-zone"
        aria-hidden="true"
        onPointerEnter={scheduleSidebarDrawerOpen}
        onPointerLeave={cancelSidebarDrawerOpen}
      /> : null}
      <aside
        className="settings-sidebar"
        data-wuu-component="settings-sidebar"
        inert={embedded && !floating && sidebarCollapsed && sidebarDrawerPhase !== "open"}
        onPointerEnter={floating ? undefined : embedded ? onSidebarPointerEnter : openSidebarDrawer}
        onPointerLeave={floating ? undefined : embedded ? onSidebarPointerLeave : (event) =>
          scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
        }
      >
        {/*
          * 与主侧栏一致的内层 .sidebar-content：折叠动画期间列宽收窄时，
          * 内容保持 --sidebar-open-width 的固定宽度被裁切（而不是被压扁），
          * 淡出/位移也复用 sidebar.css 里同一组规则。
          */}
        <div className="sidebar-content">
          {!embedded && !floating ? <div className="traffic-spacer">{!railCollapsed ? sidebarToggle : null}</div> : null}
          {embedded || floating ? <h2 className="sidebar-section-title settings-navigation-title">{t("settings.navigation")}</h2> : null}
          {!embedded && !floating ? <button className="settings-back-button" type="button" onClick={onBack}>
            <ArrowLeft className="icon" />
            <span>{t("settings.backToApp")}</span>
          </button> : null}
          <div className="settings-search" role="search">
            <Search className="icon" aria-hidden="true" />
            <input
              type="search"
              value={navQuery}
              placeholder={t("settings.searchSettings")}
              aria-label={t("settings.searchSettings")}
              onChange={(event) => setNavQuery(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape" && navQuery) {
                  event.preventDefault();
                  setNavQuery("");
                }
              }}
            />
          </div>
          <nav
            className="settings-nav"
            data-wuu-component="settings-navigation"
            aria-label={t("settings.navigation")}
          >
            {visibleNativeGroups.map((group) => (
              <div
                key={group.id}
                className="settings-nav-group"
                role={group.labelKey ? "group" : undefined}
                aria-labelledby={group.labelKey ? `settings-nav-${group.id}` : undefined}
              >
                {group.labelKey ? (
                  <h2 className="settings-nav-group-label" id={`settings-nav-${group.id}`}>
                    {t(group.labelKey)}
                  </h2>
                ) : null}
                {group.pages.map((page) => {
                  const Icon = NATIVE_PAGE_ICONS[page];
                  return (
                    <SettingsNavItem key={page} pageID={page} icon={<Icon className="icon-lg" />} active={activePage === page} onClick={() => selectPage(page)}>
                      {settingsPageTitle(page, t)}
                    </SettingsNavItem>
                  );
                })}
              </div>
            ))}
            {hasVisiblePluginPages ? (
              <div className="settings-nav-group" data-wuu-component="plugin-settings-navigation" role="group" aria-labelledby="settings-nav-plugins">
                <h2 className="settings-nav-group-label" id="settings-nav-plugins">{t("settings.navPlugins")}</h2>
                {visiblePluginSettingsRecords.map((plugin) => {
                  const pageId = pluginSettingsPageId(plugin.id);
                  return (
                    <SettingsNavItem
                      key={pageId}
                      pageID={pageId}
                      icon={<PluginBlocksIcon className="icon-lg" />}
                      active={activePage === pageId}
                      onClick={() => selectPage(pageId)}
                    >
                      {plugin.name}
                    </SettingsNavItem>
                  );
                })}
                {visibleCustomPluginSettingsPages.map((entry) => {
                  const pageId = pluginViewSettingsPageId(entry.pluginId, entry.id);
                  return (
                    <SettingsNavItem
                      key={pageId}
                      pageID={pageId}
                      icon={<PluginIcon loadIcon={loadPluginIcon} icon={entry.icon} pluginId={entry.pluginId} fingerprint={entry.generation} className="icon-lg" />}
                      active={activePage === pageId}
                      onClick={() => selectPage(pageId)}
                    >
                      {entry.title}
                    </SettingsNavItem>
                  );
                })}
              </div>
            ) : null}
            {normalizedNavQuery && !hasVisiblePages ? (
              <p className="settings-nav-empty" role="status">{t("settings.searchEmpty")}</p>
            ) : null}
          </nav>
        </div>
      </aside>
      {railCollapsed || embedded || floating ? null : (
        <div
          className="sidebar-resizer"
          role="separator"
          aria-label={t("settings.resizeSidebar")}
          aria-orientation="vertical"
          aria-valuemin={sidebarMinWidth}
          aria-valuemax={sidebarMaxWidth}
          aria-valuenow={sidebarWidth}
          tabIndex={0}
          onPointerEnter={onSidebarResizerPointer}
          onPointerMove={onSidebarResizerPointer}
          onPointerDown={onSidebarResizeStart}
          onDoubleClick={onToggleSidebar}
          onKeyDown={onSidebarSeparatorKey}
        />
      )}
      <main className="settings-main" data-wuu-component="settings-content">
        <SettingsTitlebarSlot.Provider value={titlebarSlot}>
          {floating ? null : <div className="settings-titlebar">
            {isTouchWebShell() && !floating && <button type="button" className="settings-phone-back" aria-label={t("common.back")} onClick={onBack}><ArrowLeft size={22} /></button>}
            {/* Match the main shell's docked/collapsed slots. Keep the collapsed
             * toggle inside the drag strip so native hit testing honors no-drag. */}
            {railCollapsed && !embedded && !floating ? sidebarToggle : null}
            <div ref={setTitlebarSlot} className="settings-titlebar-slot">
              <span className="settings-crumb" aria-hidden="true">
                <span className="settings-crumb-root">{t("settings.navigation")}</span>
                <span className="settings-crumb-sep">/</span>
                <span className="settings-crumb-page">{crumbTitle}</span>
              </span>
            </div>
          </div>}
          <div ref={settingsScrollRef} className="settings-scroll">
            <div
              className="settings-page"
              data-wuu-component="settings-page"
              data-wuu-page={activePage}
              key={activePage}
            >
              {activePluginSettingsRecord ? (
                <>
                  <SettingsPageHeader
                    title={activePluginSettingsRecord.name}
                    description={activePluginSettingsRecord.description}
                    meta={<>
                      <span>{pluginSourceLabel(activePluginSettingsRecord, t)}</span>
                      {activePluginSettingsRecord.developer ? <span>{activePluginSettingsRecord.developer}</span> : null}
                    </>}
                  />
                  <PluginSettingsEditor plugin={activePluginSettingsRecord} workspaceRoot={initialized?.workspace_root} />
                </>
              ) : activeCustomPluginPage ? (
                <>
                  <SettingsPageHeader title={pluginPageTitle ?? activeCustomPluginPage.title} />
                  {controls.renderPage({pluginId:activeCustomPluginPage.pluginId,view:activeCustomPluginPage.view,settings:settingsPageHost,onFailure:()=>selectPage("providers")})}
                </>
              ) : activePage === "providers" ? (
                <ModelServicesPage
                  inventory={engineInventory}
                  inventoryError={engineInventoryError}
                  onRefreshEngines={onRefreshEngineInventory}
                  onUpdateEngines={onUpdateEngineInventory}
                  initialized={initialized}
                  running={running}
                  runningProviderNames={runningProviderNameSet}
                  onSave={onSave}
                  onRemoveProvider={onRemoveProvider}
                  onRefreshModelCatalog={onRefreshModelCatalog}
                  onProvidersChanged={onProvidersChanged}
                  onProviderModelsChanged={onProviderModelsChanged}
                />
              ) : activePage === "general" ? (
                <SettingsGeneralPage
                  initialized={initialized}
                  onGeneralSave={onGeneralSave}
                  onSave={onSave}
                  desktopBuild={desktopBuild}
                  permissionStateCache={permissionStateCache}
                  copyState={copyState}
                  onCopyVersion={copyVersionInfo}
                />
              ) : activePage === "browser" ? (
                <BrowserSettingsSection />
              ) : activePage === "appearance" ? (
                <SettingsAppearancePage
                  inventory={initialized?.extension_inventory}
                  codexPets={codexPets}
                  codexPetsLoading={codexPetsLoading}
                  codexPetsError={codexPetsError}
                  onCodexPetsRefresh={onCodexPetsRefresh}
                  onCodexPetsUpdate={onCodexPetsUpdate}
                />
              ) : activePage === "mcp" ? (
                <SettingsMCPPage
                  initialized={initialized}
                  running={running}
                  onGeneralSave={onGeneralSave}
                  onStatusChange={setMCPStatus}
                />
              ) : activePage === "remote" && controls.capabilities.remote ? (
                <>
                  <SettingsPageHeader title={t("settings.remote")} />
                  <SettingsRemotePageContainer />
                </>
              ) : activePage === "archive" ? (
                <SettingsArchivePage
                  archivedThreads={archivedThreads ?? []}
                  onUnarchiveThread={onUnarchiveThread}
                  deletion={archiveDeletion}
                  onDeleteAll={onDeleteAllArchivedThreads}
                  onRetry={onRetryArchiveDeletion}
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
        </SettingsTitlebarSlot.Provider>
      </main>
    </div>
  );
  return <>{controls.renderPresentation({initialized,activePageId:activePage,availablePages,runningProviderNames,
    busy:running || usageLoading || codexPetsLoading || (activePage === "mcp" && mcpStatus.busy),
    hasError:Boolean(usageError || (activePage === "mcp" && mcpStatus.hasError) || codexPetsError),fallback:nativeSettings,
    onOpenPage:pageId=>selectPage(pageId as SettingsPage),onAdvancedSave,onGeneralSave,onRefresh:onRefreshModelCatalog})}</>;
}

/* -------------------------------------------------------------------------- */
/*  Shared primitives                                                          */
/* -------------------------------------------------------------------------- */

function SettingsNavItem({
  pageID,
  icon,
  active,
  onClick,
  children
}: {
  pageID?: SettingsPage;
  icon: ReactNode;
  active: boolean;
  onClick: () => void;
  children: string;
}): JSX.Element {
  return (
    <button
      className={`settings-nav-item${active ? " active" : ""}`}
      data-wuu-component="settings-navigation-item"
      data-wuu-page={pageID}
      type="button"
      aria-current={active ? "page" : undefined}
      onClick={onClick}
    >
      {icon}
      {/* Plugin page names can outgrow the rail; a clipped one shows in full on hover. */}
      <TruncatedText className="settings-nav-label" text={children} />
    </button>
  );
}

/* -------------------------------------------------------------------------- */
/*  General page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsGeneralPage({
  initialized,
  onGeneralSave,
  onSave,
  permissionStateCache,
  desktopBuild,
  copyState,
  onCopyVersion
}: {
  initialized: InitializeResult | undefined;
  permissionStateCache: RefObject<SystemPermissionsState | undefined>;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onSave: Parameters<typeof SettingsViewContent>[0]["onSave"];
  desktopBuild: DesktopBuildInfo | undefined;
  copyState: CopyState;
  onCopyVersion: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [gitAttributionBusy, setGitAttributionBusy] = useState(false);
  const [gitAttributionError, setGitAttributionError] = useState("");
  const gitAttributionEnabled = initialized?.general_settings?.git_attribution_enabled ?? true;

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

  return (
    <>
      <SettingsPageHeader title={t("settings.general")} />
      <SettingsSection testID="settings-general">
        <SettingsGroup>
          <SettingsRow title={t("settings.language")}>
            <LanguagePreferenceControl />
          </SettingsRow>
          <div className="settings-inline-row-anchor" data-testid="settings-about">
            <span className="sr-only">{t("settings.about")}</span>
            <SettingsRow title={t("settings.version")}>
              <span className="settings-row-control-value">
                {desktopBuild ? versionLabel(desktopBuild.version) : t("settings.loading")}
              </span>
              <Tooltip asChild content={copyState === "copied" ? t("settings.copied") : undefined}><button
                className="settings-button settings-button-ghost settings-icon-button"
                type="button"
                aria-label={t("settings.copyVersion")}
                onClick={() => void onCopyVersion()}
                disabled={!desktopBuild || copyState === "copying"}
              >
                {copyState === "copied" ? <Check className="icon" aria-hidden="true" /> : <Copy className="icon" aria-hidden="true" />}
              </button></Tooltip>
            </SettingsRow>
          </div>
        </SettingsGroup>
      </SettingsSection>

      <DesktopPreferencesSections />
      {ENABLE_APP_SNAPSHOTS && <AppContextSettingsSection />}
      <SystemPermissionsSection stateCache={permissionStateCache} />
      <NewThreadDefaultsSection initialized={initialized} onSave={onSave} />

      <SettingsSection title={t("settings.sectionGit")} testID="settings-git">
        <SettingsGroup>
          <SettingsRow
            title={t("settings.gitAttribution")}
            hideDescriptionWhenNarrow
            hint={t("settings.gitAttributionDescription")}
            error={gitAttributionError}
          >
            <Switch
              checked={gitAttributionEnabled}
              data-testid="settings-git-attribution"
              disabled={!initialized || gitAttributionBusy}
              onClick={() => void toggleGitAttribution()}
              label={gitAttributionEnabled ? t("settings.disableGitAttribution") : t("settings.enableGitAttribution")}
            />
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
    </>
  );
}

const PERMISSION_MODE_OPTIONS = [
  { value: "standard", label: "runtime.permission.standard", hint: "runtime.permission.standardHint" },
  { value: "read_only", label: "runtime.permission.readOnly", hint: "runtime.permission.readOnlyHint" },
  { value: "unconfined", label: "runtime.permission.unconfined", hint: "runtime.permission.unconfinedHint" },
] as const;

function NewThreadDefaultsSection({ initialized, onSave }: {
  initialized: InitializeResult | undefined;
  onSave: Parameters<typeof SettingsViewContent>[0]["onSave"];
}): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const providerName = initialized?.provider ?? "";
  const model = initialized?.model ?? "";
  const provider = initialized?.providers?.find((row) => row.name === providerName);
  const effortOptions = providerModelVariantOptions(provider, model, "");
  const effort = initialized?.variant ?? initialized?.effort ?? "";
  const permissionMode = initialized?.permissions?.mode || "standard";
  const disabled = !providerName || !model || busy;

  async function save(change: { permissionMode?: string; variant?: string }): Promise<void> {
    setBusy(true);
    setError("");
    try {
      await onSave(providerName, model, undefined, undefined, change.variant, change.permissionMode);
    } catch (err) {
      setError(toastErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsSection title={t("settings.sectionNewThreads")} testID="settings-new-threads">
      <SettingsGroup>
        <SettingsRow title={t("settings.defaultPermissionMode")} hint={t("settings.newThreadDefaultsHint")} hideDescriptionWhenNarrow error={error}>
          <SelectMenu
            triggerClassName="settings-select-trigger"
            ariaLabel={t("settings.defaultPermissionMode")}
            dataTestid="settings-default-permission-mode"
            value={permissionMode}
            disabled={disabled}
            align="right"
            onChange={(next) => void save({ permissionMode: next })}
            options={PERMISSION_MODE_OPTIONS.map((option) => ({ value: option.value, label: t(option.label), hint: t(option.hint) }))}
          />
        </SettingsRow>
        {effortOptions.length > 1 && (
          <SettingsRow title={t("settings.defaultReasoningEffort")}>
            <SelectMenu
              triggerClassName="settings-select-trigger"
              ariaLabel={t("settings.defaultReasoningEffort")}
              dataTestid="settings-default-reasoning-effort"
              value={effortOptions.includes(effort) ? effort : ""}
              disabled={disabled}
              align="right"
              onChange={(next) => void save({ variant: next })}
              options={effortOptions.map((option) => ({ value: option, label: variantLabel(option) }))}
            />
          </SettingsRow>
        )}
      </SettingsGroup>
    </SettingsSection>
  );
}

function DesktopPreferencesSections(): JSX.Element | null {
  const { t } = useI18n();
  const preferences = useDesktopPreferences();
  const [updateState, setUpdateState] = useState<DesktopUpdateState>({ kind: "unsupported" });
  const [checking, setChecking] = useState(false);
  const [errors, setErrors] = useState<Partial<Record<keyof DesktopPreferences | "update" | DesktopFolder, string>>>({});
  const [proxyDraft, setProxyDraft] = useState<string>();
  const isMac = navigator.platform.toLowerCase().includes("mac");

  useEffect(() => {
    const api = window.wuu;
    if (!api?.getDesktopUpdateState) return;
    let active = true;
    const unsubscribe = api.onDesktopUpdateChanged?.(next => { if (active) setUpdateState(next); });
    void api.getDesktopUpdateState().then(next => { if (active) setUpdateState(next); }, () => {});
    return () => { active = false; unsubscribe?.(); };
  }, []);

  if (!preferences) return null;

  async function save<K extends keyof DesktopPreferences>(key: K, value: DesktopPreferences[K]): Promise<void> {
    setErrors(current => ({ ...current, [key]: "" }));
    try {
      await updateDesktopPreferences({ [key]: value });
    } catch (error) {
      setErrors(current => ({ ...current, [key]: toastErrorMessage(error, t("settings.saveFailed")) }));
    }
  }

  async function checkForUpdates(): Promise<void> {
    setChecking(true);
    setErrors(current => ({ ...current, update: "" }));
    try {
      await window.wuu.checkDesktopUpdate?.();
    } catch (error) {
      setErrors(current => ({ ...current, update: toastErrorMessage(error, t("desktopUpdate.failed")) }));
    } finally {
      setChecking(false);
    }
  }

  async function openFolder(folder: DesktopFolder): Promise<void> {
    setErrors(current => ({ ...current, [folder]: "" }));
    try {
      await window.wuu.openDesktopFolder?.(folder);
    } catch (error) {
      setErrors(current => ({ ...current, [folder]: toastErrorMessage(error, t("settings.openFolderFailed")) }));
    }
  }

  const toggle = (key: "notify_turn_completed" | "notify_questions" | "notification_sound" | "auto_check_updates" | "open_at_login", title: string, hint?: string) => (
    <SettingsRow title={title} hint={hint} hideDescriptionWhenNarrow error={errors[key]}>
      <Switch
        checked={preferences[key] === true}
        data-testid={`settings-${key.replaceAll("_", "-")}`}
        onClick={() => void save(key, !preferences[key])}
        label={title}
      />
    </SettingsRow>
  );
  function commitProxyURL(): void {
    if (proxyDraft === undefined || !preferences) return;
    const url = proxyDraft.trim() ? normalizeProxyURL(proxyDraft) : "";
    if (url === undefined) {
      setErrors(current => ({ ...current, proxy_url: t("settings.proxyURLInvalid") }));
      return;
    }
    setProxyDraft(undefined);
    if (url !== preferences.proxy_url) void save("proxy_url", url);
  }

  const updatesSupported = updateState.kind !== "unsupported";
  const updateBusy = checking || updateState.kind === "checking" || updateState.kind === "prompting";

  return (
    <>
      <SettingsSection title={t("settings.sectionNotifications")} testID="settings-notifications">
        <SettingsGroup>
          {toggle("notify_turn_completed", t("settings.notifyTurnCompleted"), t("settings.notificationsBackgroundHint"))}
          {toggle("notify_questions", t("settings.notifyQuestions"))}
          {toggle("notification_sound", t("settings.notificationSound"))}
        </SettingsGroup>
      </SettingsSection>
      <SettingsSection title={t("settings.sectionInput")} testID="settings-input">
        <SettingsGroup>
          <SettingsRow title={t("settings.sendShortcut")} hint={t("settings.sendShortcutHint")} hideDescriptionWhenNarrow error={errors.send_shortcut}>
            <SegmentedControl
              label={t("settings.sendShortcut")}
              value={preferences.send_shortcut}
              options={[
                { value: "enter", label: "Enter", testID: "settings-send-shortcut-enter" },
                { value: "mod_enter", label: isMac ? "⌘ Enter" : "Ctrl+Enter", testID: "settings-send-shortcut-mod-enter" },
              ]}
              onChange={value => void save("send_shortcut", value)}
            />
          </SettingsRow>
        </SettingsGroup>
      </SettingsSection>
      {(preferences.open_at_login !== undefined || updatesSupported) && (
        <SettingsSection title={t("settings.sectionStartupUpdates")} testID="settings-startup-updates">
          <SettingsGroup>
            {preferences.open_at_login !== undefined && toggle("open_at_login", t("settings.openAtLogin"))}
            {updatesSupported && toggle("auto_check_updates", t("settings.autoCheckUpdates"))}
            {updatesSupported && (
              <SettingsRow title={t("settings.checkUpdates")} error={errors.update}>
                <button className="settings-button" type="button" data-testid="settings-check-updates"
                  disabled={updateBusy} onClick={() => void checkForUpdates()}>
                  {updateBusy ? t("settings.checkingUpdates") : t("settings.checkNow")}
                </button>
              </SettingsRow>
            )}
          </SettingsGroup>
        </SettingsSection>
      )}
      <SettingsSection title={t("settings.sectionNetwork")} testID="settings-network">
        <SettingsGroup>
          <SettingsRow title={t("settings.proxy")} hint={t("settings.proxyRestartHint")} hideDescriptionWhenNarrow error={errors.proxy_mode}>
            <SegmentedControl
              label={t("settings.proxy")}
              value={preferences.proxy_mode}
              options={[
                { value: "system", label: t("settings.proxySystem"), testID: "settings-proxy-system" },
                { value: "manual", label: t("settings.proxyManual"), testID: "settings-proxy-manual" },
                { value: "none", label: t("settings.proxyNone"), testID: "settings-proxy-none" },
              ]}
              onChange={value => void save("proxy_mode", value)}
            />
          </SettingsRow>
          {preferences.proxy_mode === "manual" && (
            <SettingsRow title={t("settings.proxyURL")} error={errors.proxy_url}>
              <input
                className="settings-input"
                data-testid="settings-proxy-url"
                aria-label={t("settings.proxyURL")}
                value={proxyDraft ?? preferences.proxy_url}
                placeholder="http://127.0.0.1:7890"
                spellCheck={false}
                autoComplete="off"
                onChange={event => {
                  const value = event.currentTarget.value;
                  setProxyDraft(value);
                  setErrors(current => ({ ...current, proxy_url: "" }));
                }}
                onBlur={commitProxyURL}
                onKeyDown={event => { if (event.key === "Enter") commitProxyURL(); }}
              />
            </SettingsRow>
          )}
        </SettingsGroup>
      </SettingsSection>
      <SettingsSection title={t("settings.sectionData")} testID="settings-data">
        <SettingsGroup>
          <SettingsRow title={t("settings.dataFolder")} hint={t("settings.dataFolderHint")} hideDescriptionWhenNarrow error={errors.data}>
            <button className="settings-button" type="button" data-testid="settings-open-data-folder" onClick={() => void openFolder("data")}>
              {t("settings.openFolder")}
            </button>
          </SettingsRow>
          <SettingsRow title={t("settings.logFolder")} error={errors.logs}>
            <button className="settings-button" type="button" data-testid="settings-open-log-folder" onClick={() => void openFolder("logs")}>
              {t("settings.openFolder")}
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

type CodexPetSettingsProps = {
  codexPets: CodexPetsSnapshot | undefined;
  codexPetsLoading: boolean;
  codexPetsError: string;
  onCodexPetsRefresh: () => Promise<CodexPetsSnapshot>;
  onCodexPetsUpdate: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
};

function SettingsAppearancePage({ inventory, ...petProps }: CodexPetSettingsProps & { inventory?: readonly ExtensionInventoryRecord[] }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const [skinGalleryOpen, setSkinGalleryOpen] = useState(false);
  if (skinGalleryOpen) {
    return <ShellSkinGallery onBack={() => setSkinGalleryOpen(false)} />;
  }
  return (
    <>
      <SettingsPageHeader title={t("settings.appearance")} />
      <SettingsSection title={t("settings.sectionTheme")} testID="settings-appearance">
        <ThemePreferenceControl inventory={inventory} />
        <SettingsGroup>
          {isTouchWebShell() ? null : <ShellSkinSettings onOpenGallery={() => setSkinGalleryOpen(true)} />}
          <ContentThemeControl inventory={inventory} />
        </SettingsGroup>
      </SettingsSection>
      <AppearanceTypography section="text" />
      <AppearanceTypography section="motion" />
      {controls.capabilities.pets ? <CodexPetSection {...petProps} /> : null}
    </>
  );
}

function CodexPetSection({
  codexPets,
  codexPetsLoading,
  codexPetsError,
  onCodexPetsRefresh,
  onCodexPetsUpdate,
}: CodexPetSettingsProps): JSX.Element {
  const { t } = useI18n();
  const controls = useSettingsControls();
  const authority = useMemo(() => ({ active: true }), [controls]);
  const [codexPetBusy, setCodexPetBusy] = useState(false);
  const [codexPetLocalError, setCodexPetLocalError] = useState("");
  useEffect(() => {
    authority.active = true;
    setCodexPetBusy(false);
    setCodexPetLocalError("");
    return () => { authority.active = false; };
  }, [authority]);
  const codexPetOptions = codexPets?.pets ?? [];
  const codexPetSelectedID = codexPets?.selected_id ?? "";
  const codexPetEnabled = Boolean(codexPets?.enabled);
  const codexPetStatus = codexPetLocalError || codexPetsError;
  const codexPetsHome = codexPets?.home ?? "~/.wuu/pets";

  async function refreshCodexPets(): Promise<void> {
    if (!authority.active) return;
    setCodexPetBusy(true);
    setCodexPetLocalError("");
    try {
      await onCodexPetsRefresh();
    } catch (error) {
      if (authority.active && !isPluginLifecycleCancellation(error)) setCodexPetLocalError(error instanceof Error ? error.message : t("settings.refreshFailed"));
    } finally {
      if (authority.active) setCodexPetBusy(false);
    }
  }

  async function updateCodexPets(settings: CodexPetSettingsUpdate): Promise<void> {
    if (!authority.active) return;
    setCodexPetBusy(true);
    setCodexPetLocalError("");
    try {
      await onCodexPetsUpdate(settings);
    } catch (error) {
      if (authority.active && !isPluginLifecycleCancellation(error)) setCodexPetLocalError(error instanceof Error ? error.message : t("settings.saveFailed"));
    } finally {
      if (authority.active) setCodexPetBusy(false);
    }
  }

  // The row names what it has found and where to add more; the refresh
  // action rereads that folder.
  return (
    <SettingsSection testID="settings-pet">
      <SettingsGroup>
        <SettingsRow
          title={t("settings.codexPet")}
          description={!codexPetsLoading && codexPetOptions.length === 0 ? t("settings.petsNotFound", { path: codexPetsHome }) : undefined}
          error={codexPetStatus || codexPets?.errors[0]}
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
          ) : null}
          <Tooltip asChild content={isTouchWebShell() ? t("settings.refreshPets") : t("settings.petSource", { path: codexPetsHome })}><button
            className="settings-button settings-button-ghost settings-icon-button"
            type="button"
            aria-label={t("settings.refreshPets")}
            disabled={codexPetsLoading || codexPetBusy}
            onClick={() => void refreshCodexPets()}
          >
            <RefreshCw className="icon" aria-hidden="true" />
          </button></Tooltip>
          <Switch
            checked={codexPetEnabled}
            data-testid="settings-codex-pet-enabled"
            disabled={codexPetsLoading || codexPetBusy || codexPetOptions.length === 0}
            onClick={() => void updateCodexPets({ enabled: !codexPetEnabled })}
            label={codexPetEnabled ? t("settings.disablePet") : t("settings.enablePet")}
          />
        </SettingsRow>
      </SettingsGroup>
    </SettingsSection>
  );
}

/* -------------------------------------------------------------------------- */
/*  Archive page                                                               */
/* -------------------------------------------------------------------------- */

function SettingsArchivePage({
  archivedThreads,
  onUnarchiveThread,
  deletion,
  onDeleteAll,
  onRetry,
}: {
  archivedThreads: readonly ArchivedSessionView[];
  onUnarchiveThread: (thread: ArchivedSessionView) => void;
  deletion?: ArchiveDeletionState;
  onDeleteAll?: () => void;
  onRetry?: () => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const preferences = useDesktopPreferences();
  const [autoArchiveError, setAutoArchiveError] = useState("");
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

  async function saveAutoArchive(days: DesktopAutoArchiveDays): Promise<void> {
    setAutoArchiveError("");
    try {
      await updateDesktopPreferences({ auto_archive_days: days });
    } catch (error) {
      setAutoArchiveError(toastErrorMessage(error, t("settings.saveFailed")));
    }
  }

  return (
    <>
      <SettingsPageHeader
        title={t("settings.archive")}
        // Deleting nothing is not an action; a running deletion keeps its progress.
        actions={sortedThreads.length > 0 || deletion?.pending ? (
          <button
            type="button"
            className="settings-button settings-button-danger settings-archive-delete-all"
            disabled={!onDeleteAll || deletion?.pending}
            onClick={onDeleteAll}
          >
            {deletion?.pending ? <Loader2 className="icon spin" aria-hidden="true" /> : <Trash2 className="icon" aria-hidden="true" />}
            {deletion?.progress
              ? t("settings.deletingArchived", deletion.progress)
              : t("settings.deleteAllArchived")}
          </button>
        ) : undefined}
      />
      <div className="settings-archive-page" aria-busy={deletion?.pending || undefined}>
        {preferences ? (
          <SettingsGroup>
            <SettingsRow title={t("settings.autoArchive")} hint={t("settings.autoArchiveHint")} hideDescriptionWhenNarrow error={autoArchiveError}>
              <SelectMenu
                triggerClassName="settings-select-trigger"
                ariaLabel={t("settings.autoArchive")}
                dataTestid="settings-auto-archive"
                value={String(preferences.auto_archive_days)}
                align="right"
                onChange={(next) => void saveAutoArchive(Number(next) as DesktopAutoArchiveDays)}
                options={DESKTOP_AUTO_ARCHIVE_DAYS.map((days) => ({
                  value: String(days),
                  label: days === 0 ? t("settings.autoArchiveOff") : t("settings.autoArchiveAfterDays", { count: days }),
                }))}
              />
            </SettingsRow>
          </SettingsGroup>
        ) : null}
        {deletion?.result || deletion?.error ? (
          <div className="settings-archive-delete-result" role={deletion.error || deletion.result?.failed ? "alert" : "status"}>
            {deletion.result ? <p>{t("settings.deleteArchivedResult", deletion.result)}</p> : null}
            {deletion.error ? <p className="settings-error">{deletion.error}</p> : null}
            {deletion.failedIDs.length > 0 ? (
              <button type="button" className="settings-button settings-button-ghost settings-archive-delete-retry"
                disabled={deletion.pending} onClick={onRetry}>
                {t("settings.retryArchiveDeletion", { count: deletion.failedIDs.length })}
              </button>
            ) : null}
          </div>
        ) : null}
        {/* Nothing archived means nothing to search or filter. */}
        {sortedThreads.length > 0 ? <div className="settings-archive-toolbar" role="search" aria-label={t("settings.archiveFilter")}>
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
        </div> : null}
        {sortedThreads.length === 0 || noMatches ? (
          <div className="settings-group">
            <SettingsEmptyState
              title={noMatches ? t("settings.noArchiveMatches") : t("settings.noArchivedItems")}
              description={noMatches ? undefined : t("settings.archiveHint")}
            />
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
                          className="settings-button settings-button-ghost settings-archive-restore"
                          aria-label={t("settings.restoreConversation", { title })}
                          disabled={deletion?.pending}
                          onClick={() => onUnarchiveThread(thread)}
                        >
                          <RotateCcw className="icon" aria-hidden="true" />
                          <span>{t("settings.restore")}</span>
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
  const { locale, t, formatNumber, formatDate } = useI18n();
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
  const modelTotal = (model: SettingsUsageResponse["model_breakdowns"][number]): number =>
    model.input_tokens + model.output_tokens + model.cache_creation_tokens + model.cache_read_tokens;
  const allModelTotal = (usage?.model_breakdowns ?? []).reduce((total, model) => total + modelTotal(model), 0);
  const engineSumTotal = (sum?: { input_tokens: number; output_tokens: number; cache_creation_tokens: number; cache_read_tokens: number }): number =>
    (sum?.input_tokens ?? 0) + (sum?.output_tokens ?? 0) + (sum?.cache_creation_tokens ?? 0) + (sum?.cache_read_tokens ?? 0);
  const heatmapCols = heatmap.length > 0 ? Math.ceil(heatmap.length / 7) : 12;

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
  // The totals cover every recorded day, so the page says since when.
  const firstDay = usage?.metrics.date_range[0];
  const since = firstDay ? t("settings.usageSince", { date: formatUsageSinceDate(firstDay, formatDate) }) : undefined;
  if (!usage) {
    // Retained snapshots render straight into the data branch below; the
    // skeleton only fills the cold-load window when there is nothing to show.
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
    return (
      <>
        {header}
        <div className="settings-usage-page" data-testid="settings-usage">
          <div className="settings-group">
            <p className="settings-group-empty" role={error ? "alert" : undefined} data-error={error ? "" : undefined}>
              {error || t("settings.noUsage")}
            </p>
          </div>
        </div>
      </>
    );
  }
  return (
    <>
    <SettingsPageHeader title={t("settings.usage")} description={since} />
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
            {t("settings.last30Days")}
          </h2>
        </header>
        <div className="settings-group settings-usage-card">
          {/* The plot's top edge is the busiest day; its value is the scale. */}
          <span className="settings-usage-chart-scale" aria-hidden="true">
            {maxTrendTotal > 0 ? formatCompactUsageValue(maxTrendTotal) : null}
          </span>
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
            {t("settings.pastYear")}
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
            className="settings-usage-heatmap"
            aria-label={t("settings.usageHeatmap")}
            role="grid"
            style={{ "--heatmap-cols": heatmapCols } as CSSProperties}
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

      <section className="settings-section" aria-labelledby="settings-model-usage-title">
        <header className="settings-section-header">
          <h2 id="settings-model-usage-title" className="settings-section-title">{t("settings.usageByModel")}</h2>
        </header>
        {usage.model_breakdowns.length > 0 ? (
          <div className="settings-group settings-usage-table-wrap">
            <table className="settings-usage-table">
              <thead>
                <tr>
                  <th scope="col">{t("settings.model")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.tokenShare")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageInput")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageOutput")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.hitRate")}</th>
                </tr>
              </thead>
              <tbody>
                {usage.model_breakdowns.map((b) => {
                  const prompt = b.input_tokens + b.cache_read_tokens;
                  const rate = prompt > 0 ? b.cache_read_tokens / prompt : undefined;
                  const total = modelTotal(b);
                  return (
                    <tr key={`${b.provider}\n${b.model}`}>
                      <td>
                        <div className="settings-usage-model">
                          <strong>{b.model || t("settings.unknownModel")}</strong>
                          <small>{b.provider || t("settings.unknownProvider")}</small>
                        </div>
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.tokenShare")}>
                        <Tooltip content={formatCompactUsageValue(total)}>
                          <span className="settings-usage-number settings-usage-share">
                            {formatPercent(allModelTotal > 0 ? total / allModelTotal : undefined)}
                          </span>
                        </Tooltip>
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

      <section className="settings-section" aria-labelledby="settings-engine-usage-title">
        <header className="settings-section-header">
          <h2 id="settings-engine-usage-title" className="settings-section-title">{t("settings.usageByEngine")}</h2>
        </header>
        {(usage.engine_breakdowns ?? []).length > 0 ? (
          <div className="settings-group settings-usage-table-wrap">
            <table className="settings-usage-table settings-engine-usage-table">
              <thead>
                <tr>
                  <th scope="col">{t("settings.engine")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageInWuu")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageOutsideWuu")}</th>
                  <th scope="col" className="settings-usage-num">{t("settings.usageSessions")}</th>
                </tr>
              </thead>
              <tbody>
                {(usage.engine_breakdowns ?? []).map((b) => {
                  const inWuuTotal = engineSumTotal(b.in_wuu);
                  const outsideTotal = b.outside ? engineSumTotal(b.outside) : undefined;
                  return (
                    <tr key={b.engine}>
                      <td>
                        <div className="settings-usage-model">
                          <strong>{b.name || b.engine}</strong>
                          <small>{b.engine}</small>
                        </div>
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.usageInWuu")}>
                        <Tooltip content={formatUsageValue(inWuuTotal)}>
                          <span className="settings-usage-number">
                            {formatCompactUsageNumber(inWuuTotal, locale)}
                          </span>
                        </Tooltip>
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.usageOutsideWuu")}>
                        {outsideTotal === undefined ? (
                          <Tooltip content={t("settings.usageNoLedger")}>
                            <span className="settings-usage-number">—</span>
                          </Tooltip>
                        ) : (
                          <Tooltip content={formatUsageValue(outsideTotal)}>
                            <span className="settings-usage-number">
                              {formatCompactUsageNumber(outsideTotal, locale)}
                            </span>
                          </Tooltip>
                        )}
                      </td>
                      <td className="settings-usage-num" data-label={t("settings.usageSessions")}>
                        <span className="settings-usage-number">
                          {formatCompactUsageNumber((b.in_wuu.sessions ?? 0) + (b.outside?.sessions ?? 0), locale)}
                        </span>
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

      <section className="settings-section settings-skill-usage" aria-labelledby="settings-skill-usage-title">
        <header className="settings-section-header">
          <h2 id="settings-skill-usage-title" className="settings-section-title">
            {t("settings.skillCalls")}
          </h2>
        </header>
        <div className="settings-group settings-usage-card">
          {skillUsage.length ? (
            <div className="settings-skill-usage-list">
              {skillUsage.slice(0, 8).map((skill) => {
                const count = Number.isFinite(skill.count) ? Math.max(0, skill.count) : undefined;
                const width = count !== undefined && maxSkillCount > 0 ? Math.max(6, (count / maxSkillCount) * 100) : 0;
                return (
                  <div className="settings-skill-usage-row" key={skill.name}>
                    <span className="settings-skill-usage-label">{skill.name}</span>
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
        </div>
        <div className="settings-group settings-usage-card">
          <span className="settings-usage-chart-scale" />
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
    case "general":
      return t("settings.general");
    case "appearance":
      return t("settings.appearance");
    case "browser":
      return t("settings.browser");
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

function pluginSettingsPageId(pluginId: string): `plugin-settings:${string}` {
  return `plugin-settings:${pluginId}`;
}

function pluginViewSettingsPageId(
  pluginId: string,
  entryId: string,
): `plugin-view:${string}:${string}` {
  return `plugin-view:${pluginId}:${entryId}`;
}

function formatPercent(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) {
    return "—";
  }
  return `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`;
}

function formatUsageSinceDate(
  date: string,
  formatDate: (value: Date | number | string, options?: Intl.DateTimeFormatOptions) => string,
): string {
  const day = new Date(`${date}T12:00:00`);
  if (Number.isNaN(day.getTime())) return date;
  return formatDate(day, {
    ...(day.getFullYear() === new Date().getFullYear() ? {} : { year: "numeric" }),
    month: "long",
    day: "numeric",
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
  const outsideTotal = outsideDayTotal(day);
  if (outsideTotal !== undefined) {
    return t("settings.usageDayWithOutside", {
      date: day.date,
      total: formatNumber(usageTokenTotal(day)),
      inWuu: formatNumber(inWuuDayTotal(day)),
      outside: formatNumber(outsideTotal),
    });
  }
  return t("settings.usageTrendOnDate", {
    date: day.date,
    total: formatNumber(usageTokenTotal(day)),
    input: formatNumber(day.input_tokens + day.cache_read_tokens + day.cache_creation_tokens),
    output: formatNumber(day.output_tokens),
  });
}

function inWuuDayTotal(day: SettingsUsageDay): number {
  return day.input_tokens + day.output_tokens + day.cache_creation_tokens + day.cache_read_tokens;
}

function outsideDayTotal(day: SettingsUsageDay): number | undefined {
  const outside = day.outside;
  if (!outside) {
    return undefined;
  }
  const total = outside.input_tokens + outside.output_tokens + outside.cache_creation_tokens + outside.cache_read_tokens;
  return total > 0 || outside.sessions > 0 ? total : undefined;
}

function hasUsageDayData(day: SettingsUsageDay): boolean {
  return (
    day.input_tokens > 0 ||
    day.output_tokens > 0 ||
    day.cache_creation_tokens > 0 ||
    day.cache_read_tokens > 0 ||
    day.turns > 0 ||
    day.agents > 0 ||
    outsideDayTotal(day) !== undefined
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
  const outsideTotal = outsideDayTotal(day);
  if (outsideTotal !== undefined) {
    return t("settings.usageDayWithOutside", {
      date: day.date,
      total: formatNumber(usageTokenTotal(day)),
      inWuu: formatNumber(inWuuDayTotal(day)),
      outside: formatNumber(outsideTotal),
    });
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

// Resolve legacy links before rendering or publishing the selected page.
function availableSettingsPage(
  page: SettingsPage | undefined,
  browserAllowed: boolean,
  capabilities: import("../../../packages/plugin-sdk/src/ui-settings-controls").SettingsControlsCapabilitiesV1,
): SettingsPage {
  const next = page ?? "general";
  if (next === "advanced") return "general";
  if (next === "subscriptions" || next === "agents"
    || (next === "remote" && !capabilities.remote)
    || (next === "browser" && (!browserAllowed || !capabilities.browser))) return "providers";
  return next;
}

export function SettingsView({application,...props}:Parameters<typeof SettingsViewContent>[0] & {application:{readonly settingsControls:import("../../../packages/plugin-sdk/src/ui-settings-controls").ApplicationSettingsControlsV1}}):JSX.Element {
 return <SettingsControlsProvider controls={application.settingsControls}><SettingsViewContent {...props}/></SettingsControlsProvider>;
}
