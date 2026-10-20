import { ConnectionTitle } from "./ConnectionTitle";
import { useConnectionDisplayPreferences } from "./ConnectionDisplayPreferences";
import { GrokLogin } from "./GrokLogin";
import {
  type FormEvent as ReactFormEvent,
  type ReactNode,
  lazy,
  Suspense,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  CatalogModelSummary,
  CatalogProviderSummary,
  InitializeResult,
  EngineListResult,
  EngineUpdateParams,
  EngineBinarySettings,
  ProviderModelSummary,
  ProviderSummary,
  RuntimeConnectionUpdate,
  SubscriptionQuota,
} from "../shared/protocol";
import {
  ArrowUp,
  ArrowDown,
  GripHorizontal,
  AlertTriangle,
  Check,
  ChevronRight,
  Code2,
  MoreHorizontal,
  Plus,
  RotateCcw,
  Search,
  Trash2,
} from "./WuuIcons";
import { SubscriptionDashboard } from "./SubscriptionDashboard";
import { hasBuiltInSubscriptionControls } from "./SubscriptionSources";
import { ENABLE_SUBSCRIPTIONS } from "./FeatureFlags";
import { CatalogSearchField } from "./CatalogSearchField";
import { EngineSettingsSection } from "./EngineSettingsSection";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import { hostSupports } from "./HostCapabilities";
import { isTouchWebShell } from "./ComposerFocus";
import { Modal } from "./Modal";
import { ConnectionPage } from "./ConnectionPage";
import { ConnectionUsageSummary } from "./ConnectionUsageSummary";
import { ProviderMark } from "./ProviderMarks";
import {
  chatGPTConnectionModels,
  isChatGPTProvider,
  normalizedVariantForProviderModel,
  providerModelDisplayName,
  providerModelVariantOptions,
  variantLabel,
} from "./RuntimeHelpers";
import { SelectMenu, type SelectMenuGroup } from "./SelectMenu";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";

type Translate = ReturnType<typeof useI18n>["t"];

// Authentication UI is needed only after opening a ChatGPT connection, not
// during ordinary settings, composer or first-run catalog initialization.
const ChatGPTAccountRows = lazy(() => import("./ChatGPTAccountRows").then(module => ({ default: module.ChatGPTAccountRows })));
const ChatGPTConnectionDialog = lazy(() => import("./ChatGPTConnectionDialog").then(module => ({ default: module.ChatGPTConnectionDialog })));

// Model connections keep vendor identity separate from authentication method.
// Account status and usage live with the connection that owns them.
// Settings save a service without choosing it; only the default model card
// and "Make default" change what new conversations start with.

export type SaveProvider = (
  provider: string,
  model: string,
  effort?: string,
  connection?: RuntimeConnectionUpdate,
  variant?: string,
) => Promise<void>;

export type ModelServicesPageProps = {
  inventory?: EngineListResult;
  inventoryError?: string;
  onRefreshEngines?: () => Promise<EngineListResult | undefined>;
  onUpdateEngines?: (params: EngineUpdateParams) => Promise<EngineListResult>;
  initialized?: InitializeResult;
  running: boolean;
  runningProviderNames: ReadonlySet<string>;
  onSave: SaveProvider;
  onRemoveProvider: (provider: string, options?: { fallbackProvider?: string; fallbackModel?: string }) => Promise<void>;
  onRefreshModelCatalog: () => Promise<void>;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  onProviderModelsChanged?: (name: string, accountId: string, models: ProviderModelSummary[]) => void;
};

type ConnectTarget =
  | { kind: "chatgpt" }
  | { kind: "catalog"; provider: CatalogProviderSummary }
  | { kind: "custom" }
  | { kind: "subscription"; type: "openai-codex" | "xai-subscription" | "grok-build" };

// Catalog names are English and regional ("Moonshot AI (China)"); these are
// the names people use for the services, localized where they differ.
const SERVICE_NAME_KEYS: Readonly<Record<string, TranslationKey>> = {
  moonshotai: "provider.kimi",
  "moonshotai-cn": "provider.kimi",
  zhipuai: "provider.zhipu",
  alibaba: "provider.alibabaBailian",
  "alibaba-cn": "provider.alibabaBailian",
  volcengine: "provider.volcengineArk",
  siliconflow: "provider.siliconFlow",
  "siliconflow-cn": "provider.siliconFlow",
};

const SERVICE_NAMES: Readonly<Record<string, string>> = {
  "minimax-cn": "MiniMax",
  minimax: "MiniMax",
  zai: "Z.AI",
  "kimi-code-plan-cn": "Kimi Code",
  "kimi-code-plan-global": "Kimi Code",
};

// Frequently used services come first for each language. The complete
// catalog remains one click away, with existing connections marked in place.
const FEATURED_SERVICES: Readonly<Record<"zh" | "en", readonly string[]>> = {
  zh: ["deepseek", "moonshotai-cn", "zhipuai", "alibaba-cn", "volcengine", "siliconflow-cn", "minimax-cn", "openrouter", "openai", "anthropic"],
  en: ["openai", "anthropic", "openrouter", "deepseek", "xai", "moonshotai", "zai", "minimax", "alibaba", "siliconflow"],
};
// Keep gateway discovery separate from direct vendor APIs. Unrecognized
// catalog services remain available in the general API group.
const GATEWAY_SERVICES = new Set([
  "openrouter", "siliconflow", "siliconflow-cn", "togetherai", "fireworks-ai",
  "deepinfra", "huggingface", "302ai", "aihubmix", "opencode", "opencode-go",
  "cline-pass", "modelscope", "nvidia", "vercel", "helicone", "llmgateway",
]);

function isLocalCatalogService(provider: CatalogProviderSummary): boolean {
  return ["localhost", "127.0.0.1", "[::1]"].includes(hostFromBaseURL(provider.base_url));
}
const MODEL_SEARCH_THRESHOLD = 8;

export function catalogServiceName(id: string, fallback: string, t: Translate): string {
  const key = SERVICE_NAME_KEYS[id];
  return key ? t(key) : SERVICE_NAMES[id] ?? fallback;
}

function normalizedType(type: string | undefined): string {
  return (type ?? "").trim().toLowerCase().replaceAll("_", "-");
}

export function isXAISubscriptionType(type: string | undefined): boolean {
  return ["xai-subscription", "xai-oauth", "grok-subscription", "supergrok"].includes(normalizedType(type));
}

export function isGrokBuildType(type: string | undefined): boolean {
  return ["grok-build", "xai-grok-build", "grok-cli"].includes(normalizedType(type));
}

function isCodexType(type: string | undefined): boolean {
  return ["openai-codex", "codex-subscription", "chatgpt-codex"].includes(normalizedType(type));
}

function isAnthropicType(type: string | undefined): boolean {
  return ["anthropic", "claude", "anthropic-official"].includes(normalizedType(type));
}

function hostFromBaseURL(baseURL?: string): string {
  if (!baseURL) return "";
  try {
    return new URL(baseURL).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

type ServiceIdentity = {
  label: string;
  markID?: string;
  engineMark?: string;
  subscription: boolean;
  // Why the service cannot answer yet; absent when it can.
  attention?: string;
};

export function serviceIdentity(provider: ProviderSummary, t: Translate): ServiceIdentity {
  const host = hostFromBaseURL(provider.base_url);
  if (isXAISubscriptionType(provider.type) || isGrokBuildType(provider.type)) {
    return {
      label: isGrokBuildType(provider.type) ? t("provider.grokBuild") : t("provider.xaiSubscription"),
      engineMark: "grok",
      subscription: true,
      attention: provider.api_key_configured ? undefined
        : isGrokBuildType(provider.type) ? t("provider.grokBuildLoginRequired") : t("provider.loginRequired"),
    };
  }
  if (isChatGPTProvider(provider)) {
    return { label: "ChatGPT", markID: "openai", subscription: true,
      attention: provider.auth_status === "connected" ? undefined : t(provider.auth_status === "permission_required" ? "provider.chatgptPermissionRequired" : "provider.chatgptReconnectRequired") };
  }
  if (isCodexType(provider.type) || provider.connection_locked) {
    return {
      label: "ChatGPT",
      markID: "openai",
      subscription: true,
      attention: provider.api_key_configured ? undefined : t("provider.loginRequired"),
    };
  }
  const local = host === "localhost" || host === "127.0.0.1" || host === "::1";
  const label = provider.catalog_id
    ? catalogServiceName(provider.catalog_id, provider.catalog_name || provider.name, t)
    : local
      ? t("provider.localService")
      : host || provider.name || t("provider.genericService");
  return {
    label,
    markID: provider.catalog_id,
    subscription: false,
    attention: provider.api_key_configured ? undefined : t("provider.keyMissing"),
  };
}

// Two connections to the same vendor keep their configured names visible.
function serviceLabels(providers: readonly ProviderSummary[], t: Translate): Map<string, string> {
  const base = new Map(providers.map((provider) => [provider.name, serviceIdentity(provider, t).label]));
  const counts = new Map<string, number>();
  for (const label of base.values()) counts.set(label, (counts.get(label) ?? 0) + 1);
  return new Map(providers.map((provider) => {
    const label = base.get(provider.name) ?? provider.name;
    return [provider.name, (counts.get(label) ?? 0) > 1 ? `${label} · ${provider.name}` : label];
  }));
}

export function ServiceMark({ identity }: { identity: ServiceIdentity }): JSX.Element {
  if (identity.engineMark) {
    return (
      <span className="provider-mark" data-mark={identity.engineMark} aria-hidden="true">
        <EngineIcon engine={identity.engineMark} className="provider-mark-engine" />
      </span>
    );
  }
  return <ProviderMark id={identity.markID} label={identity.label} />;
}

function providerModels(provider: ProviderSummary): ProviderModelSummary[] {
  if (isChatGPTProvider(provider)) return chatGPTConnectionModels(provider);
  const models = provider.models?.length ? [...provider.models] : provider.model ? [{ id: provider.model }] : [];
  // Positions stay put when the selection changes.
  return models.sort((left, right) => {
    const a = providerModelDisplayName(left).toLocaleLowerCase();
    const b = providerModelDisplayName(right).toLocaleLowerCase();
    return a.localeCompare(b) || left.id.localeCompare(right.id);
  });
}

function modelLabel(provider: ProviderSummary | undefined, modelID: string): string {
  const model = provider?.models?.find((item) => item.id === modelID);
  return model ? providerModelDisplayName(model) : modelID;
}

function uniqueProviderName(base: string, providers: readonly ProviderSummary[]): string {
  const taken = new Set(providers.map((provider) => provider.name));
  const stem = base.trim().toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "") || "custom";
  if (!taken.has(stem)) return stem;
  let index = 2;
  while (taken.has(`${stem}-${index}`)) index += 1;
  return `${stem}-${index}`;
}

function errorMessage(error: unknown, t: Translate): string {
  return error instanceof Error && error.message ? error.message : t("provider.saveFailed");
}

type AccountMethod = "login" | "local";
function AccountMethods({ selected, disabled, onSelect }: { selected: AccountMethod | undefined; disabled: boolean; onSelect: (method: AccountMethod) => void }): JSX.Element {
  const { t } = useI18n();
  return <div className="connection-auth-methods" role="group" aria-label={t("provider.authMethod")}>
    {(["login", "local"] as const).map(method => <button key={method} type="button" className="settings-button settings-button-ghost"
      data-testid={`settings-auth-method-${method}`} aria-pressed={selected === method} disabled={disabled}
      onClick={() => onSelect(method)}>{t(method === "login" ? "provider.authSignIn" : "provider.authUseLocal")}</button>)}
  </div>;
}

type CatalogState = { providers?: CatalogProviderSummary[]; failed: boolean };

export function useCatalogProviders(version: number, snapshot?: CatalogProviderSummary[]): CatalogState {
  const [state, setState] = useState<CatalogState>({ failed: false });
  useEffect(() => {
    if (snapshot) return;
    // Older hosts do not include the directory in initialization.
    if (!hostSupports("listCatalogProviders") || typeof window.wuu?.listCatalogProviders !== "function") {
      setState({ failed: true });
      return;
    }
    let active = true;
    void window.wuu.listCatalogProviders().then(
      (result) => { if (active) setState({ providers: result.providers, failed: false }); },
      () => { if (active) setState({ failed: true }); },
    );
    return () => { active = false; };
  }, [version, snapshot]);
  return snapshot ? { providers: snapshot, failed: false } : state;
}

export function ModelServicesPage({
  inventory,
  inventoryError,
  onRefreshEngines,
  onUpdateEngines,
  initialized,
  running,
  runningProviderNames,
  onSave,
  onRemoveProvider,
  onProvidersChanged,
  onProviderModelsChanged,
}: ModelServicesPageProps): JSX.Element {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  const addButtonRef = useRef<HTMLButtonElement>(null);
  const providers = initialized?.providers ?? [];
  const displayPreferences = useConnectionDisplayPreferences();
  const labels = new Map(providers.map(provider => [provider.name, displayPreferences.label(`provider:${provider.name}`, serviceIdentity(provider, t).label)]));
  const [quotaSnapshot, setQuotaSnapshot] = useState<EngineListResult>();
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaFailed, setQuotaFailed] = useState(false);
  const [quotaVersion, setQuotaVersion] = useState(0);
  const [quotaNow, setQuotaNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setQuotaNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (!ENABLE_SUBSCRIPTIONS || !hostSupports("listEngines")) return;
    let active = true;
    setQuotaLoading(true);
    void window.wuu.listEngines({ include_quota: true }).then(snapshot => {
      if (active) { setQuotaSnapshot(snapshot); setQuotaFailed(false); setQuotaNow(Date.now()); }
    }, () => { if (active) setQuotaFailed(true); })
      .finally(() => { if (active) setQuotaLoading(false); });
    return () => { active = false; };
  }, [quotaVersion]);
  const observedQuota = (quota: SubscriptionQuota | undefined): SubscriptionQuota | undefined =>
    quotaFailed && quota?.status === "available" ? { ...quota, status: "stale" } : quota;
  const overviewProviders = providers.map(provider => {
    const snapshot = quotaSnapshot?.subscription_providers?.find(item => item.name === provider.name
      && item.type === provider.type && item.chatgpt_account_id === provider.chatgpt_account_id
      && item.base_url === provider.base_url);
    return { ...provider, quota: observedQuota(snapshot?.quota ?? provider.quota) };
  });
  const overviewInventory = inventory ? { ...inventory, engines: inventory.engines.map(engine => {
    const snapshot = quotaSnapshot?.engines.find(item => item.id === engine.id && item.binary_path === engine.binary_path);
    const currentPath = (inventory.settings as Record<string, EngineBinarySettings | undefined> | undefined)?.[engine.id]?.binary_path ?? "";
    const observedPath = (quotaSnapshot?.settings as Record<string, EngineBinarySettings | undefined> | undefined)?.[engine.id]?.binary_path ?? "";
    return { ...engine, quota: observedQuota(currentPath === observedPath ? snapshot?.quota ?? engine.quota : engine.quota) };
  }) } : inventory;
  const [openService, setOpenService] = useState("");
  const [adding, setAdding] = useState(false);
  const [methodOrigin, setMethodOrigin] = useState<{ name: string; target: ConnectTarget }>();
  const [selectedEngine, setSelectedEngine] = useState<string>();
  const focusEngine = useRef("");
  useEffect(() => {
    if (!selectedEngine && focusEngine.current) {
      const id = focusEngine.current;
      focusEngine.current = "";
      document.querySelector<HTMLButtonElement>(`[data-testid="${adding ? `settings-connection-catalog-engine-${id}` : `settings-engine-${id}-status`}"]`)?.focus();
    }
  }, [selectedEngine, adding]);
  const focusAdd = useRef(false);
  const closeAdding = () => { focusAdd.current = true; setAdding(false); setMethodOrigin(undefined); };
  useEffect(() => {
    if (!adding && focusAdd.current) { focusAdd.current = false; addButtonRef.current?.focus(); }
  }, [adding]);
  const focusProvider = useRef("");
  const closeService = () => { focusProvider.current = openService; setOpenService(""); };
  useEffect(() => {
    if (!openService && focusProvider.current) {
      const name = focusProvider.current;
      focusProvider.current = "";
      if (adding) { document.querySelector<HTMLInputElement>(".connection-page.model-browse-dialog input")?.focus(); return; }
      document.querySelectorAll<HTMLButtonElement>("[data-testid=\"settings-provider-card\"]").forEach(button => {
        if (button.dataset.provider === name) button.focus();
      });
    }
  }, [openService, adding]);
  const [catalogVersion, setCatalogVersion] = useState(0);
  const catalog = useCatalogProviders(catalogVersion, initialized?.catalog_providers);
  const detailProvider = overviewProviders.find((provider) => provider.name === openService);

  useLayoutEffect(() => {
    const scroller = rootRef.current?.closest(".settings-scroll");
    if (scroller) scroller.scrollTop = 0;
  }, [openService]);

  // A removed or renamed service returns the page to the overview.
  useEffect(() => {
    if (openService && !detailProvider) setOpenService("");
  }, [openService, detailProvider]);

  const defaultProvider = providers.find((provider) => provider.name === initialized?.provider);
  const defaultUsable = Boolean(defaultProvider && !serviceIdentity(defaultProvider, t).attention);


  return (
    <div className="model-services" data-testid="settings-providers" ref={rootRef}>
      <SettingsPageHeader title={t("settings.providers")} actions={!adding && !detailProvider && !selectedEngine ?
        <button type="button" className="settings-button settings-button-ghost settings-icon-button"
          data-testid="settings-connection-add" ref={addButtonRef} disabled={running}
          aria-label={t("provider.addConnection")} title={t("provider.addConnection")}
          onClick={() => setAdding(true)}><Plus className="icon" aria-hidden="true" /></button> : undefined} />
      {!adding && !detailProvider && !selectedEngine ? <ServicesOverview providers={overviewProviders} initialized={initialized} quotaLoading={quotaLoading} now={quotaNow}
        onOpenService={setOpenService} /> : null}
      {onRefreshEngines && onUpdateEngines && !detailProvider && (!adding || selectedEngine) ? <EngineSettingsSection embedded
        result={overviewInventory} quotaFailed={quotaFailed} quotaLoading={quotaLoading} quotaNow={quotaNow} loadError={inventoryError} onRefresh={onRefreshEngines} onUpdate={onUpdateEngines}
        selectedEngineID={selectedEngine} onSelectEngine={setSelectedEngine}
        onCloseEngine={() => { focusEngine.current = selectedEngine ?? ""; setSelectedEngine(undefined); }} /> : null}
      {adding ? <div hidden={!!selectedEngine || !!detailProvider}>
        <ServiceConnector key={methodOrigin?.name ?? "catalog"} providers={providers} running={running} catalog={catalog}
          defaultOn={!defaultUsable} onSave={onSave} onProvidersChanged={onProvidersChanged} catalogDialog inline
          initialTarget={methodOrigin?.target}
          onCloseConfiguration={methodOrigin ? () => { setOpenService(methodOrigin.name); setAdding(false); setMethodOrigin(undefined); } : undefined}
          onDismiss={closeAdding} onConnected={name => { setAdding(false); setMethodOrigin(undefined); setOpenService(name); }}
          onOpenExisting={setOpenService}
          catalogExtras={query => {
            const engines = inventory?.engines.filter(engine => engine.id !== "wuu" && `${engine.id} ${engineLabel(engine.id, engine)}`.toLowerCase().includes(query.trim().toLowerCase()));
            return onUpdateEngines && (!inventory || engines?.length) ? <SettingsSection title={t("provider.engineConnections")}>
            <SettingsGroup>
              {engines?.map(engine =>
                <button key={engine.id} type="button" className="connection-catalog-row" data-testid={`settings-connection-catalog-engine-${engine.id}`}
                  onClick={() => setSelectedEngine(engine.id)}>
                  <EngineIcon engine={engine.id} /><span>{engineLabel(engine.id, engine)}</span>
                  <ChevronRight className="icon" aria-hidden="true" />
                </button>)}
              {!inventory ? <p role="status">{inventoryError || t("settings.engineDetecting")}</p> : null}
            </SettingsGroup>
          </SettingsSection> : null; }} />
      </div> : null}
      {detailProvider ? <ServiceDetail key={detailProvider.name} provider={detailProvider}
        onChooseMethod={method => {
          const candidates = providers.filter(provider => method === "login" ? isChatGPTProvider(provider) : isCodexType(provider.type));
          if (candidates.length === 1) { setOpenService(candidates[0].name); return; }
          setMethodOrigin({ name: detailProvider.name, target: method === "login" ? { kind: "chatgpt" } : { kind: "subscription", type: "openai-codex" } });
          setOpenService(""); setAdding(true);
        }}
        onRefreshConnection={async () => {
          const result = await window.wuu.refreshModelCatalog();
          const refreshed = result.providers.find(item => item.name === detailProvider.name
            && item.type === detailProvider.type && item.base_url === detailProvider.base_url);
          if (!refreshed?.api_key_configured) throw new Error("Login is not available to this connection");
          onProvidersChanged?.([refreshed]);
          setQuotaSnapshot(current => current ? { ...current,
            subscription_providers: current.subscription_providers?.filter(item => item.name !== detailProvider.name),
          } : current);
          setCatalogVersion(value => value + 1);
          setQuotaVersion(value => value + 1);
        }}
        onQuotaLoaded={snapshot => { setQuotaSnapshot(snapshot); setQuotaFailed(false); setQuotaNow(Date.now()); }}
        onProvidersChanged={onProvidersChanged} onProviderModelsChanged={onProviderModelsChanged}
        label={labels.get(detailProvider.name) ?? detailProvider.name} initialized={initialized} running={running}
        inUse={runningProviderNames.has(detailProvider.name)} onBack={closeService} onSave={onSave}
        onRemove={async options => { await onRemoveProvider(detailProvider.name, options); closeService(); }} /> : null}
    </div>
  );
}

/**
 * The providers a person can connect and the dialogs that connect them.
 * Model services lists these under its services; first-run onboarding offers
 * the same choices, so a connection is made the same way in both places.
 */
export function ServiceConnector({
  providers,
  running,
  catalog,
  defaultOn,
  onSave,
  onConnected,
  onOpenExisting,
  onProvidersChanged,
  catalogDialog = false,
  inline = false,
  catalogExtras,
  onDismiss,
  initialTarget,
  onCloseConfiguration,
}: {
  providers: readonly ProviderSummary[];
  running: boolean;
  catalog: CatalogState;
  defaultOn: boolean;
  onSave: SaveProvider;
  onConnected: (name: string) => void;
  onOpenExisting?: (name: string) => void;
  catalogDialog?: boolean;
  inline?: boolean;
  catalogExtras?: (query: string) => ReactNode;
  onDismiss?: () => void;
  initialTarget?: ConnectTarget;
  onCloseConfiguration?: () => void;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
}): JSX.Element {
  const { t, locale } = useI18n();
  const [connectTarget, setConnectTarget] = useState<ConnectTarget | null>(initialTarget ?? null);
  const [browsing, setBrowsing] = useState(false);
  const restoreCatalogFocus = useRef(false);
  useEffect(() => {
    if (!connectTarget && restoreCatalogFocus.current) {
      restoreCatalogFocus.current = false;
      document.querySelector<HTMLInputElement>(".connection-page.model-browse-dialog input")?.focus();
    }
  }, [connectTarget]);
  const closeConfiguration = () => { if (onCloseConfiguration) { onCloseConfiguration(); return; } restoreCatalogFocus.current = true; setConnectTarget(null); };
  const featuredIDs = FEATURED_SERVICES[locale.startsWith("zh") ? "zh" : "en"];
  const featured = featuredIDs
    .map((id) => catalog.providers?.find((provider) => provider.id === id))
    .filter((provider): provider is CatalogProviderSummary => Boolean(provider));
  const local = (catalog.providers ?? []).filter(isLocalCatalogService);

  function startConnect(target: ConnectTarget): void {
    setBrowsing(false);
    const existing = target.kind === "subscription" && target.type === "openai-codex"
      ? providers.find((provider) => isCodexType(provider.type)) : undefined;
    if (existing && onOpenExisting) { setConnectTarget(null); onOpenExisting(existing.name); }
    else setConnectTarget(target);
  }

  return (
    <>
      {!catalogDialog ? <><div className="model-service-catalog" data-testid="settings-provider-tiles">
        <CatalogServiceGroups
          catalogProviders={[...featured, ...local.filter((provider) => !featuredIDs.includes(provider.id))]}
          providers={providers}
          grouped={false}
          disabled={running}
          onConnect={startConnect}
        />
        <div className="model-service-catalog-actions">
          <button
            type="button"
            className="settings-button settings-button-ghost model-service-custom-action"
            data-testid="settings-provider-custom"
            disabled={running}
            onClick={() => startConnect({ kind: "custom" })}
          >
            <Plus className="icon" aria-hidden="true" />
            {t("provider.customEndpoint")}
          </button>
          {catalog.providers?.length ? (
            <button
              type="button"
              className="settings-button settings-button-ghost"
              data-testid="settings-provider-browse"
              disabled={running}
              onClick={() => setBrowsing(true)}
            >
              <Search className="icon" aria-hidden="true" />
              {t("provider.moreServices")}
              <ChevronRight className="icon-sm" aria-hidden="true" />
            </button>
          ) : null}
        </div>
      </div>
      {catalog.failed ? <p className="settings-section-note">{t("provider.catalogUnavailable")}</p> : null}</> : null}
      {(browsing || catalogDialog) && (inline || !connectTarget) ? (
        <div hidden={!!connectTarget}><BrowseServicesDialog
          inline={inline} catalogExtras={catalogExtras}
          catalog={catalog}
          providers={providers}
          disabled={running}
          onClose={() => { setBrowsing(false); onDismiss?.(); }}
          onConnect={startConnect}
        /></div>
      ) : null}
      {connectTarget?.kind === "chatgpt" ? (
        <Suspense fallback={
          <p role="status">
            {t("settings.loading")}
          </p>
        }>
          <ChatGPTConnectionDialog renderAuthMethods={disabled => <AccountMethods selected="login" disabled={disabled} onSelect={method => { if (method === "local") startConnect({ kind: "subscription", type: "openai-codex" }); }} />} inline={inline} onProvidersChanged={onProvidersChanged} providers={providers} running={running} defaultOn={defaultOn}
            onSave={onSave} onClose={closeConfiguration} onConnected={(name) => { setConnectTarget(null); onConnected(name); }} />
        </Suspense>
      ) : connectTarget ? (
        <ConnectServiceDialog
          renderAuthMethods={connectTarget.kind === "subscription" && connectTarget.type === "openai-codex" ? disabled => <AccountMethods selected="local" disabled={disabled} onSelect={method => { if (method === "login") setConnectTarget({ kind: "chatgpt" }); }} /> : undefined}
          inline={inline}
          target={connectTarget}
          providers={providers}
          running={running}
          defaultOn={defaultOn}
          onClose={closeConfiguration}
          onSave={onSave}
          onConnected={(name) => {
            setConnectTarget(null);
            onConnected(name);
          }}
        />
      ) : null}
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Overview                                                                    */
/* -------------------------------------------------------------------------- */

function ServicesOverview({
  providers,
  initialized,
  onOpenService,
  quotaLoading,
  now,
}: {
  providers: readonly ProviderSummary[];
  initialized?: InitializeResult;
  onOpenService: (name: string) => void;
  quotaLoading: boolean;
  now: number;
}): JSX.Element {
  const { t } = useI18n();
  const summaryID = useId();

  return (
    <>

      {providers.length > 0 ? (
        <SettingsSection title={t("provider.connectionModels")}>
          <div className="model-service-grid" role="list" aria-label={t("provider.connectionModels")}>
            {providers.map((provider) => {
              const identity = serviceIdentity(provider, t);
              const label = identity.label;
              const isDefault = provider.name === initialized?.provider;
              const attention = identity.attention ?? (provider.quota?.status === "sign_in" ? t("settings.subscriptionSignIn") : undefined);
              const status = attention ?? t("settings.engineReady");
              const plan = provider.quota?.plan?.trim() || (identity.subscription ? t("settings.subscriptionKindSubscription") : "API");
              return (
                <div role="listitem" key={provider.name}>
                  <button
                    type="button"
                    className="model-service-card connection-summary-row"
                    data-testid="settings-provider-card"
                    data-provider={provider.name}
                    data-default={isDefault ? "" : undefined}
                    aria-label={label}
                    aria-describedby={`${summaryID}-${encodeURIComponent(provider.name)} ${summaryID}-${encodeURIComponent(provider.name)}-quota`}
                    onClick={() => onOpenService(provider.name)}
                  >
                    <span className="connection-summary-icon"><ServiceMark identity={identity} /></span>
                    <span id={`${summaryID}-${encodeURIComponent(provider.name)}`} className="model-service-card-copy connection-summary-copy">
                      <span className="model-service-card-title">{label}</span>
                      <span className="connection-summary-meta"><span>{plan}</span><span aria-hidden="true"> · </span>
                        <span className="connection-summary-status" data-attention={attention ? "" : undefined}>{status}</span>
                      </span>
                    </span>
                    {!attention ? <ConnectionUsageSummary id={`${summaryID}-${encodeURIComponent(provider.name)}-quota`} quota={provider.quota} loading={quotaLoading} now={now} /> : null}
                    <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />
                  </button>
                </div>
              );
            })}
          </div>
        </SettingsSection>
      ) : <SettingsSection title={t("provider.connectionModels")}><p className="settings-muted-line">{t("provider.noService")}</p></SettingsSection>}

    </>
  );
}

// The one choice most people make here: what a new conversation uses. Model
// and effort are ordinary labeled rows; the service the model comes from is
// the model row's description, since one model name can come from several.
/**
 * The built-in agent's default model and reasoning effort as settings rows,
 * for the group that sets how new conversations start. Without a connected
 * service the model row points to where one is added.
 */
export function DefaultModelRows({
  initialized,
  running,
  onSave,
  onAddService,
}: {
  initialized?: InitializeResult;
  running: boolean;
  onSave: SaveProvider;
  onAddService: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const providers = initialized?.providers ?? [];
  const displayPreferences = useConnectionDisplayPreferences();
  const labels = new Map(providers.map(provider => [provider.name, displayPreferences.label(`provider:${provider.name}`, serviceIdentity(provider, t).label)]));
  const [error, setError] = useState("");
  const [pending, setPending] = useState<{ provider: string; model: string; variant: string } | null>(null);
  const providerName = pending?.provider ?? initialized?.provider ?? "";
  const modelID = pending?.model ?? initialized?.model ?? "";
  const variant = pending?.variant ?? initialized?.variant ?? initialized?.effort ?? "";
  const provider = providers.find((item) => item.name === providerName);
  const identity = provider ? serviceIdentity(provider, t) : undefined;
  const effortOptions = providerModelVariantOptions(provider, modelID, variant);
  const groups: SelectMenuGroup[] = providers.map((item) => {
    const itemIdentity = serviceIdentity(item, t);
    const label = labels.get(item.name) ?? itemIdentity.label;
    return {
      label,
      options: displayPreferences.visibleModels(`provider:${item.name}`, providerModels(item), item.name === initialized?.provider ? [initialized.model] : [item.model]).map((model) => ({
        value: `${item.name}\n${model.id}`,
        label: providerModelDisplayName(model),
        keywords: [label, item.name, model.id],
      })),
    };
  });
  const optionCount = groups.reduce((sum, group) => sum + group.options.length, 0);

  async function commit(next: { provider: string; model: string; variant: string }): Promise<void> {
    setError("");
    setPending(next);
    try {
      await onSave(next.provider, next.model, undefined, undefined, next.variant);
    } catch (saveError) {
      setError(errorMessage(saveError, t));
    } finally {
      setPending(null);
    }
  }

  if (providers.length === 0) {
    return (
      <SettingsRow title={t("provider.modelLabel")} description={t("provider.noService")}>
        <button type="button" className="settings-button" data-testid="settings-providers-empty" onClick={onAddService}>
          {t("provider.addSection")}
        </button>
      </SettingsRow>
    );
  }

  return (
    <>
      <SettingsRow
        title={t("provider.modelLabel")}
        error={error || undefined}
        description={identity ? (
          <span className="model-default-service">
            {labels.get(providerName) ?? identity.label}
            {identity.attention ? (
              <span className="model-default-attention">
                <AlertTriangle className="icon-sm" aria-hidden="true" />
                {identity.attention}
              </span>
            ) : null}
          </span>
        ) : undefined}
      >
        <SelectMenu
          triggerClassName="settings-select-trigger"
          ariaLabel={t("provider.chooseDefaultModel")}
          dataTestid="settings-default-model-select"
          value={`${providerName}\n${modelID}`}
          placeholder={modelID || t("provider.chooseDefaultModel")}
          groups={groups}
          searchable={optionCount > MODEL_SEARCH_THRESHOLD}
          searchPlaceholder={t("provider.searchModels")}
          emptyMessage={t("provider.noModelMatches")}
          disabled={running || pending !== null || optionCount === 0}
          align="right"
          flip
          onChange={(value) => {
            const [nextProvider = "", nextModel = ""] = value.split("\n");
            const target = providers.find((item) => item.name === nextProvider);
            void commit({
              provider: nextProvider,
              model: nextModel,
              variant: normalizedVariantForProviderModel(variant, target, nextModel),
            });
          }}
        />
      </SettingsRow>
      {effortOptions.length > 1 ? (
        <SettingsRow title={t("provider.reasoningEffort")}>
          <SelectMenu
            triggerClassName="settings-select-trigger"
            ariaLabel={t("provider.reasoningEffort")}
            value={variant}
            align="right"
            disabled={running || pending !== null}
            options={effortOptions.map((option) => ({ value: option, label: variantLabel(option) }))}
            onChange={(next) => void commit({ provider: providerName, model: modelID, variant: next })}
          />
        </SettingsRow>
      ) : null}
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Service detail                                                              */
/* -------------------------------------------------------------------------- */

function ServiceDetail({
  provider: configuredProvider,
  onProvidersChanged,
  onProviderModelsChanged,
  onQuotaLoaded,
  onRefreshConnection,
  onChooseMethod,
  label,
  initialized,
  running,
  inUse,
  onBack,
  onSave,
  onRemove,
}: {
  provider: ProviderSummary;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  onProviderModelsChanged?: (name: string, accountId: string, models: ProviderModelSummary[]) => void;
  onQuotaLoaded: (snapshot: EngineListResult) => void;
  onRefreshConnection: () => Promise<void>;
  onChooseMethod: (method: AccountMethod) => void;
  label: string;
  initialized?: InitializeResult;
  running: boolean;
  inUse: boolean;
  onBack: () => void;
  onSave: SaveProvider;
  onRemove: (options?: { fallbackProvider?: string; fallbackModel?: string }) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [liveModels, setLiveModels] = useState<ProviderModelSummary[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [catalogRevision, setCatalogRevision] = useState(0);
  const direct = isChatGPTProvider(configuredProvider);
  const provider = direct ? { ...configuredProvider, models: liveModels } : configuredProvider;
  const identity = serviceIdentity(provider, t);
  const detectedLocalCodex = ["codex-cli", "codex-cli-readonly"].includes(provider.codex_credential_source ?? "");
  const usingLocalCodex = Boolean(provider.reuse_codex_credentials) && provider.codex_credential_source !== "explicit";
  const authSource = direct ? "wuu-siwc" : usingLocalCodex ? "codex-local" : detectedLocalCodex ? "codex-local-available" : provider.codex_credential_source === "explicit" ? "codex-explicit" : "codex-saved";
  const authSourceLabel = {
    "wuu-siwc": "provider.authAppSource", "codex-local": "provider.authCodexSource", "codex-local-available": "provider.authCodexAvailable",
    "codex-explicit": "settings.codexSourceExplicit", "codex-saved": "settings.codexSourceSaved",
  } as const;
  const attention = provider.quota?.status === "sign_in" ? t("provider.loginRequired") : identity.attention;
  const [authBusy, setAuthBusy] = useState(false);
  const isDefault = provider.name === initialized?.provider;
  // The workspace model is the default service's model.
  const serviceModel = isDefault ? initialized?.model ?? provider.model : provider.model;
  const currentVariant = initialized?.variant ?? initialized?.effort ?? "";
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  const [fallbackProvider, setFallbackProvider] = useState("");
  const removable = !provider.auto_discovered && !isCodexType(provider.type);
  const fallbackProviders = (initialized?.providers ?? []).filter((item) => !item.auto_discovered && item.name !== provider.name);
  const fallback = fallbackProviders.find((item) => item.name === fallbackProvider);
  const lastProvider = fallbackProviders.length === 0;
  const disabled = running || busy || authBusy;
  const canMakeDefault = !isDefault && !attention && (!direct || liveModels.some(model => model.id === provider.model));

  async function run(action: () => Promise<void>): Promise<boolean> {
    setError("");
    setBusy(true);
    try {
      await action();
      return true;
    } catch (saveError) {
      setError(errorMessage(saveError, t));
      return false;
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!direct || !configuredProvider.chatgpt_account_id) return;
    let active = true;
    setLiveModels([]);
    setModelsLoading(false);
    if (configuredProvider.auth_status !== "connected") return;
    setModelsLoading(true);
    void window.wuu.listChatGPTModels(configuredProvider.chatgpt_account_id).then(result => {
      if (!active) return;
      const models = result.providers?.find(item => item.name === configuredProvider.name)?.models ?? result.models.map(item => ({ id: item.id, display_name: item.name, account_descriptor: item.account_descriptor, capabilities: item.capabilities, behavior: item.behavior }));
      setLiveModels(models);
      onProviderModelsChanged?.(configuredProvider.name, configuredProvider.chatgpt_account_id!, models);
    }, cause => { if (active) setError(errorMessage(cause, t)); }).finally(() => { if (active) setModelsLoading(false); });
    return () => { active = false; };
  }, [direct, configuredProvider.chatgpt_account_id, configuredProvider.auth_status, catalogRevision]);

  // Edits keep the default where it is unless they are the default's model.
  const saveConnection = (connection: RuntimeConnectionUpdate): Promise<boolean> =>
    run(() => onSave(provider.name, serviceModel, undefined, { ...connection, keep_selection: true }));

  function chooseModel(model: string): void {
    if (model === serviceModel) return;
    void run(() => isDefault
      ? onSave(provider.name, model, undefined, undefined, normalizedVariantForProviderModel(currentVariant, provider, model))
      : onSave(provider.name, model, undefined, { keep_selection: true }));
  }

  function removeModel(model: string, replacement: string): Promise<boolean> {
    return run(() => onSave(
      provider.name,
      replacement,
      undefined,
      { remove_model: model, keep_selection: !isDefault || replacement === serviceModel },
      isDefault && replacement !== serviceModel
        ? normalizedVariantForProviderModel(currentVariant, provider, replacement)
        : undefined,
    ));
  }

  return (
    <ConnectionPage title={<ConnectionTitle connectionKey={`provider:${provider.name}`} fallback={identity.label} disabled={disabled} />} icon={<ServiceMark identity={identity} />}
      subtitle={<span className="connection-status-row">
        {provider.quota?.plan ? <span>{t("connection.subscriptionPlan", { provider: identity.label, plan: provider.quota.plan })}</span> : null}
        <span className="connection-status-pill" data-attention={Boolean(attention)}>{attention || t("settings.subscriptionReady")}</span>
      </span>}
      actions={<details className="connection-credential-menu">
        <summary className="settings-button">{t(direct ? "provider.authSignIn" : isCodexType(provider.type) || isGrokBuildType(provider.type) ? "connection.credentials" : "connection.connectionSettings")}</summary>
        <div className="connection-credential-panel">
      {direct || isCodexType(provider.type) ? <>
        <AccountMethods selected={direct ? "login" : usingLocalCodex ? "local" : undefined} disabled={disabled}
          onSelect={method => {
            if (method === "local" && isCodexType(provider.type)) {
              void run(async () => {
                const reused = await window.wuu.useCodexCredentials(provider.name);
                onProvidersChanged?.(reused.providers);
                const checked = await window.wuu.loadCodexModels(provider.name);
                onProvidersChanged?.(checked.providers);
              });
            } else onChooseMethod(method);
          }} />
        {direct || usingLocalCodex || provider.codex_credential_source ? <small className="connection-auth-source" data-auth-source={authSource}>{t(authSourceLabel[authSource])}</small> : null}
        {isCodexType(provider.type) ? <button type="button" className="settings-button settings-button-ghost" disabled={disabled} data-testid="settings-check-codex-login" onClick={() => void run(async () => { const result = await window.wuu.loadCodexModels(provider.name); onProvidersChanged?.(result.providers); })}>{t("settings.codexCheckLogin")}</button> : null}
      </> : null}
        {isGrokBuildType(provider.type) ? <GrokLogin disabled={running || busy} onBusyChange={setAuthBusy} onAuthenticated={onRefreshConnection} onReuseLocal={onRefreshConnection} /> : null}
      {!isGrokBuildType(provider.type) ? <SettingsSection title={t("provider.connectionSection")}>
        <div className="connection-fields">
          {direct ? <Suspense fallback={<p className="settings-muted-line" role="status">{t("settings.loading")}</p>}>
            <ChatGPTAccountRows onBusyChange={setAuthBusy} provider={provider} disabled={disabled} onProvidersChanged={onProvidersChanged} onRefresh={() => setCatalogRevision(value => value + 1)} />
          </Suspense> : <ConnectionRows provider={provider} identity={identity} disabled={disabled} onSave={saveConnection} onRun={run} />}
        </div>
      </SettingsSection> : null}

        </div>
      </details>}
      closeDisabled={busy || authBusy} onClose={onBack} backTestId="settings-provider-back">
      <div className="connection-detail-body">
      {error && !confirmingRemove ? <p className="settings-error" role="alert">{error}</p> : null}

      {ENABLE_SUBSCRIPTIONS && !(isGrokBuildType(provider.type) && attention) && (provider.quota || hasBuiltInSubscriptionControls(provider)) ? <SubscriptionDashboard detailOnly scope={{ kind: "provider", name: provider.name }} providers={[provider]} onProvidersChanged={onProvidersChanged} onInventoryLoaded={onQuotaLoaded} onSelectBuiltinModel={(name, model) => onSave(name, model)} /> : null}

      <ModelChoices
        provider={provider}
        serviceModel={serviceModel}
        disabled={disabled || modelsLoading || (direct && !!identity.attention)}
        error={error}
        onChoose={chooseModel}
        onRemove={removeModel}
        onAdd={(model) => saveConnection({ add_model: model })}
      />

      <details className="connection-engine-advanced">
        <summary>{t("connection.advanced")}</summary>
        <p className="settings-muted-line">{t("connection.localPreferences")}</p>
        {canMakeDefault ? <button type="button" className="settings-button" data-testid="settings-provider-make-default" disabled={disabled}
          onClick={() => void run(() => onSave(provider.name, provider.model, undefined, undefined, normalizedVariantForProviderModel(currentVariant, provider, provider.model)))}>{t("provider.makeDefault")}</button> : null}
        {removable ? <button type="button" className="settings-button settings-button-danger" data-testid="settings-provider-remove" disabled={disabled}
          onClick={() => { if (inUse) { setError(t("provider.inUse")); return; } setError(""); setFallbackProvider(""); setConfirmingRemove(true); }}>{t("provider.removeAction")}</button> : null}
      </details>
      {confirmingRemove ? (
        <Modal
          ariaLabel={t("provider.removeTitle", { name: label })}
          title={t("provider.removeTitle", { name: label })}
          panelClassName="model-dialog model-service-remove-dialog"
          closeDisabled={busy}
          onClose={() => setConfirmingRemove(false)}
          footer={<>
            <button type="button" className="settings-button settings-button-ghost" disabled={busy} onClick={() => setConfirmingRemove(false)}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              className="settings-button settings-button-danger"
              data-testid="settings-provider-remove-confirm"
              disabled={busy || lastProvider || (isDefault && !fallback)}
              onClick={() => {
                void run(() => onRemove(isDefault && fallback
                  ? { fallbackProvider: fallback.name, fallbackModel: fallback.model }
                  : undefined)).then((removed) => { if (removed) setConfirmingRemove(false); });
              }}
            >
              {t("provider.removeAction")}
            </button>
          </>}
        >
          <p>{t(lastProvider ? "provider.lastProvider" : "provider.removeDescription")}</p>
          {isDefault && !lastProvider ? (
            <div className="model-connect-field">
              <span>{t("provider.chooseDefaultModel")}</span>
              <SelectMenu
                triggerClassName="settings-select-trigger"
                ariaLabel={t("provider.chooseDefaultModel")}
                dataTestid="settings-provider-removal-fallback"
                value={fallbackProvider}
                placeholder={t("provider.chooseDefaultModel")}
                options={fallbackProviders.map((item) => ({ value: item.name, label: `${serviceIdentity(item, t).label} · ${modelLabel(item, item.model)}` }))}
                disabled={busy}
                onChange={setFallbackProvider}
                flip
              />
            </div>
          ) : null}
          {error ? <p className="settings-error" role="alert">{error}</p> : null}
        </Modal>
      ) : null}
      </div>
    </ConnectionPage>
  );
}

function ConnectionRows({
  provider,
  identity,
  disabled,
  onSave,
  onRun,
}: {
  provider: ProviderSummary;
  identity: ServiceIdentity;
  disabled: boolean;
  onSave: (connection: RuntimeConnectionUpdate) => Promise<boolean>;
  onRun: (action: () => Promise<void>) => Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  if (isXAISubscriptionType(provider.type)) {
    return (
      <>
        <XAILoginRow signedIn={Boolean(provider.api_key_configured)} disabled={disabled} onRun={onRun} onSignedIn={() => onSave({})} />
        <SettingsRow title={t("settings.baseURL")}>
          <span className="settings-row-control-value model-service-value">{provider.base_url || t("provider.xaiOAuthManaged")}</span>
        </SettingsRow>
      </>
    );
  }
  if (isGrokBuildType(provider.type)) {
    return (
      <SettingsRow title={t("provider.grokBuildLogin")}>
        <StatusValue ok={Boolean(provider.api_key_configured)} okLabel={t("provider.grokBuildLoggedIn")} missingLabel={identity.attention ?? ""} />
      </SettingsRow>
    );
  }
  if (identity.subscription) {
    return (
      <>
        {provider.api_key_configured ? <SettingsRow title={t("provider.chatgptLogin")}>
          <StatusValue
            ok={Boolean(provider.api_key_configured)}
            okLabel={t(provider.codex_credential_source === "explicit" ? "settings.codexSourceExplicit" : provider.reuse_codex_credentials ? "settings.codexSourceLocal" : "settings.codexSourceSaved")}
            missingLabel={identity.attention ?? ""}
          />
        </SettingsRow> : null}
        {!ENABLE_SUBSCRIPTIONS ? <SettingsRow title={t("settings.codexUseLocal")}>
          <button type="button" className="settings-button" disabled={disabled} onClick={() => void onRun(async () => {
            await window.wuu.useCodexCredentials(provider.name);
            await onSave({});
          })}>{t("settings.codexUseLocal")}</button>
        </SettingsRow> : null}
        <SettingsRow title={t("settings.baseURL")}>
          <span className="settings-row-control-value model-service-value">{provider.base_url || t("provider.oauthManaged")}</span>
        </SettingsRow>
      </>
    );
  }
  const keyLabel = isAnthropicType(provider.type) ? t("provider.authToken") : t("provider.apiKey");
  return (
    <>
      <EditableRow
        title={keyLabel}
        testID="settings-provider-key"
        secret
        value={provider.api_key_configured ? t("provider.keySaved") : ""}
        emptyLabel={identity.attention ?? t("provider.keyNotSet")}
        attention={!provider.api_key_configured}
        actionLabel={provider.api_key_configured ? t("provider.replaceKey") : t("provider.setKey")}
        placeholder={t("provider.pasteKey")}
        disabled={disabled}
        onCommit={(value) => onSave({ base_url: provider.base_url ?? "", api_key: value })}
      />
      <EditableRow
        title={t("settings.baseURL")}
        testID="settings-provider-base-url"
        value={provider.base_url ?? ""}
        initialDraft={provider.base_url ?? ""}
        emptyLabel=""
        actionLabel={t("common.edit")}
        placeholder="https://api.example.com/v1"
        disabled={disabled}
        onCommit={(value) => onSave({ base_url: value })}
      />
    </>
  );
}

function StatusValue({ ok, okLabel, missingLabel }: { ok: boolean; okLabel: string; missingLabel: string }): JSX.Element {
  return (
    <span className="settings-row-control-value model-service-status" data-attention={ok ? undefined : ""}>
      {ok ? <Check className="icon-sm" aria-hidden="true" /> : <AlertTriangle className="icon-sm" aria-hidden="true" />}
      {ok ? okLabel : missingLabel}
    </span>
  );
}

// A saved value shown read-only, replaced in place by a field while editing.
function EditableRow({
  title,
  testID,
  value,
  initialDraft = "",
  emptyLabel,
  attention = false,
  secret = false,
  actionLabel,
  placeholder,
  disabled,
  onCommit,
}: {
  title: string;
  testID: string;
  value: string;
  initialDraft?: string;
  emptyLabel: string;
  attention?: boolean;
  secret?: boolean;
  actionLabel: string;
  placeholder: string;
  disabled: boolean;
  onCommit: (value: string) => Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing) inputRef.current?.focus();
  }, [editing]);

  async function submit(event: ReactFormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const next = draft.trim();
    if (!next || next === initialDraft.trim()) {
      setEditing(false);
      return;
    }
    if (await onCommit(next)) setEditing(false);
  }

  if (editing) {
    return (
      <SettingsRow title={title}>
        <form className="model-service-edit" onSubmit={(event) => void submit(event)}>
          <input
            ref={inputRef}
            className="settings-input"
            data-testid={`${testID}-input`}
            aria-label={title}
            type={secret ? "password" : "text"}
            autoComplete={secret ? "new-password" : "off"}
            spellCheck={false}
            value={draft}
            placeholder={placeholder}
            disabled={disabled}
            onChange={(event) => setDraft(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                setEditing(false);
              }
            }}
          />
          <button type="button" className="settings-button settings-button-ghost" disabled={disabled} onClick={() => setEditing(false)}>
            {t("common.cancel")}
          </button>
          <button type="submit" className="settings-button settings-button-primary" data-testid={`${testID}-save`} disabled={disabled || !draft.trim()}>
            {t("common.save")}
          </button>
        </form>
      </SettingsRow>
    );
  }
  return (
    <SettingsRow title={title}>
      <span className="settings-row-control-value model-service-value" data-attention={attention ? "" : undefined}>
        {attention ? <AlertTriangle className="icon-sm" aria-hidden="true" /> : null}
        <span className="model-service-value-text">{value || emptyLabel}</span>
      </span>
      <button
        type="button"
        className="settings-button"
        data-testid={`${testID}-edit`}
        disabled={disabled}
        onClick={() => {
          setDraft(initialDraft);
          setEditing(true);
        }}
      >
        {actionLabel}
      </button>
    </SettingsRow>
  );
}

function XAILoginRow({
  signedIn,
  disabled,
  onRun,
  onSignedIn,
}: {
  signedIn: boolean;
  disabled: boolean;
  onRun: (action: () => Promise<void>) => Promise<boolean>;
  onSignedIn: () => Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [code, setCode] = useState("");
  return (
    <SettingsRow
      title={t("provider.xaiSubscription")}
      description={code ? t("provider.xaiLoginCode", { code }) : undefined}
    >
      <StatusValue ok={signedIn} okLabel={t("provider.xaiLoggedIn")} missingLabel={t("provider.loginRequired")} />
      <button
        type="button"
        className="settings-button"
        disabled={disabled || Boolean(code)}
        onClick={() => void onRun(async () => {
          try {
            await runXAILogin(setCode, t);
          } finally {
            setCode("");
          }
          await onSignedIn();
        })}
      >
        {code ? t("provider.xaiLoggingIn") : t("provider.xaiLogin")}
      </button>
    </SettingsRow>
  );
}

async function runXAILogin(onCode: (code: string) => void, t: Translate): Promise<void> {
  const start = await window.wuu.startXAILogin();
  onCode(start.user_code);
  const url = start.verification_uri_complete || start.verification_uri;
  if (url) await window.wuu.openExternal(url);
  const deadline = Date.now() + Math.max(30, start.expires_in || 300) * 1000;
  let interval = Math.max(1000, start.interval_ms || 5000);
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, interval));
    const poll = await window.wuu.pollXAILogin(start.login_id);
    if (poll.status === "pending") {
      interval = Math.max(1000, poll.interval_ms || interval);
      continue;
    }
    if (poll.status !== "success") throw new Error(poll.error || t("error.oauthFailed"));
    return;
  }
  throw new Error(t("error.oauthFailed"));
}

function ModelChoices({
  provider,
  serviceModel,
  disabled,
  error,
  onChoose,
  onRemove,
  onAdd,
}: {
  provider: ProviderSummary;
  serviceModel: string;
  disabled: boolean;
  error: string;
  onChoose: (model: string) => void;
  onRemove: (model: string, replacement: string) => Promise<boolean>;
  onAdd: (model: string) => Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<ProviderModelSummary | null>(null);
  const [modelMenu, setModelMenu] = useState<{ model: ProviderModelSummary; x: number; y: number } | null>(null);
  const [replacement, setReplacement] = useState("");
  const [removeError, setRemoveError] = useState("");
  const preferences = useConnectionDisplayPreferences();
  const preferenceKey = `provider:${provider.name}`;
  const models = preferences.orderModels(preferenceKey, providerModels(provider));
  const [preferenceError, setPreferenceError] = useState("");
  function updatePreference(action: () => void): void { try { action(); setPreferenceError(""); } catch (cause) { setPreferenceError(errorMessage(cause, t)); } }
  const managedCatalog = isChatGPTProvider(provider);
  const hidden = provider.hidden_models ?? [];
  const removingSelected = removing?.id === serviceModel;
  const replacements = models.filter((model) => model.id !== removing?.id);
  const normalized = query.trim().toLocaleLowerCase();
  const visible = normalized
    ? models.filter((model) => `${providerModelDisplayName(model)} ${model.id}`.toLocaleLowerCase().includes(normalized))
    : models;

  return (
    <SettingsSection
      title={t("provider.modelsSection")}
      actions={<>
        {models.length > MODEL_SEARCH_THRESHOLD ? (
          <CatalogSearchField value={query} placeholder={t("provider.searchModels")} onValueChange={setQuery} />
        ) : null}
        {!managedCatalog ? <button
          type="button"
          className="settings-button settings-button-ghost"
          aria-label={t("provider.addModel")}
          title={t("provider.addModel")}
          data-testid="settings-provider-add-model-open"
          disabled={disabled || adding}
          onClick={() => setAdding(true)}
        >
          <Plus className="icon" aria-hidden="true" />{t("provider.addModel")}
        </button> : null}
      </>}
    >
      {modelMenu ? <ThreadContextMenu x={modelMenu.x} y={modelMenu.y} onClose={() => setModelMenu(null)} items={[{
        label: t("provider.removeModel", { model: providerModelDisplayName(modelMenu.model) }),
        icon: <Trash2 className="icon" aria-hidden="true" />, danger: true, disabled,
        onSelect: () => { setRemoving(modelMenu.model); setReplacement(""); setRemoveError(""); },
      }]} /> : null}
      {preferenceError ? <p role="alert" className="settings-error">{preferenceError}</p> : null}
      <div className="connection-model-table-heading"><span>{t("connection.modelName")}</span><span>{t("connection.modelID")}</span><span>{t("connection.enabled")}</span><span /></div>
      <div className="settings-group model-choice-list connection-model-table" role="group" aria-label={t("provider.modelsSection")}>
        {visible.map((model) => {
          const name = providerModelDisplayName(model);
          const selected = model.id === serviceModel;
          return (
            <div className="model-choice connection-model-row" key={model.id} data-selected={selected ? "" : undefined}>
              <button
                type="button"
                className="model-choice-main"
                data-testid="settings-provider-model"
                data-model={model.id}
                aria-pressed={selected}
                aria-label={t("provider.useModel", { model: name })}
                disabled={disabled}
                onClick={() => onChoose(model.id)}
              >
                <span className="model-choice-label"><span className="model-choice-name">{name}</span>{selected ? <Check className="icon model-choice-selected" aria-hidden="true" /> : null}</span>
              </button>
              <code className="connection-model-id">{model.id}</code>
              <button type="button" className="settings-switch connection-model-enabled" role="switch" aria-label={`${t("connection.enabled")} ${name}`} aria-checked={!preferences.get(preferenceKey).hiddenModels.includes(model.id)} disabled={disabled || selected}
                onClick={() => updatePreference(() => preferences.setModelVisible(preferenceKey, model.id, preferences.get(preferenceKey).hiddenModels.includes(model.id)))}><span className="settings-switch-thumb" /></button>
              <span className="connection-model-order">
                <GripHorizontal className="icon" aria-hidden="true" />
                <button type="button" className="settings-button settings-button-ghost settings-icon-button" aria-label={t("connection.moveUp", { name })} disabled={disabled || models[0]?.id === model.id} onClick={() => updatePreference(() => preferences.moveModel(preferenceKey, models, model.id, -1))}><ArrowUp className="icon" /></button>
                <button type="button" className="settings-button settings-button-ghost settings-icon-button" aria-label={t("connection.moveDown", { name })} disabled={disabled || models.at(-1)?.id === model.id} onClick={() => updatePreference(() => preferences.moveModel(preferenceKey, models, model.id, 1))}><ArrowDown className="icon" /></button>
              </span>
              {!managedCatalog ? <button
                type="button"
                className="icon-button model-choice-action"
                data-testid="settings-provider-model-actions"
                data-model={model.id}
                aria-label={t("provider.moreActions", { name })}
                title={t("provider.moreActions", { name })}
                aria-haspopup="menu"
                aria-expanded={modelMenu?.model.id === model.id}
                disabled={disabled}
                onClick={event => {
                  const bounds = event.currentTarget.getBoundingClientRect();
                  setModelMenu({ model, x: bounds.right, y: bounds.bottom + 4 });
                }}
              >
                <Trash2 className="icon" aria-hidden="true" />
              </button> : null}
            </div>
          );
        })}
        {visible.length === 0 ? <p className="settings-group-empty">{t("provider.noModelMatches")}</p> : null}
        {adding ? (
          <form
            className="model-choice-add"
            onSubmit={(event) => {
              event.preventDefault();
              const model = draft.trim();
              if (!model) return;
              void onAdd(model).then((added) => {
                if (!added) return;
                setDraft("");
                setAdding(false);
              });
            }}
          >
            <input
              className="settings-input"
              data-testid="settings-provider-add-model"
              aria-label={t("provider.addModelPlaceholder")}
              placeholder={t("provider.addModelPlaceholder")}
              spellCheck={false}
              autoFocus
              value={draft}
              disabled={disabled}
              onChange={(event) => setDraft(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key !== "Escape") return;
                event.preventDefault();
                event.stopPropagation();
                setDraft("");
                setAdding(false);
              }}
            />
            <button type="button" className="settings-button settings-button-ghost" disabled={disabled} onClick={() => { setDraft(""); setAdding(false); }}>
              {t("common.cancel")}
            </button>
            <button type="submit" className="settings-button settings-button-primary" disabled={disabled || !draft.trim()}>
              {t("provider.addModelAction")}
            </button>
          </form>
        ) : null}
      </div>
      {!managedCatalog && hidden.length > 0 ? (
        <details className="model-choice-hidden">
          <summary><ChevronRight className="icon-sm settings-disclosure-chevron" aria-hidden="true" />{t("provider.hiddenModels", { count: hidden.length })}</summary>
          <div className="settings-group">
            {hidden.map((model) => (
              <div className="model-choice" key={model}>
                <span className="model-choice-main is-static">
                  <span className="model-choice-name">{model}</span>
                </span>
                <button
                  type="button"
                  className="icon-button model-choice-action"
                  aria-label={t("provider.restoreModel", { model })}
                  title={t("provider.restoreModel", { model })}
                  disabled={disabled}
                  onClick={() => void onAdd(model)}
                >
                  <RotateCcw className="icon" aria-hidden="true" />
                </button>
              </div>
            ))}
          </div>
        </details>
      ) : null}
      {removing ? (
        <Modal
          ariaLabel={t("provider.removeModelTitle", { model: providerModelDisplayName(removing) })}
          title={t("provider.removeModelTitle", { model: providerModelDisplayName(removing) })}
          panelClassName="model-dialog model-remove-dialog"
          closeDisabled={disabled}
          onClose={() => setRemoving(null)}
          footer={<>
            <button type="button" className="settings-button settings-button-ghost" disabled={disabled} onClick={() => setRemoving(null)}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              className="settings-button settings-button-danger"
              data-testid="settings-provider-remove-model-confirm"
              disabled={disabled || (removingSelected && !replacement)}
              onClick={() => {
                void onRemove(removing.id, removingSelected ? replacement : serviceModel).then((removed) => {
                  if (removed) setRemoving(null);
                  else setRemoveError(t("provider.saveFailed"));
                });
              }}
            >
              {t("common.delete")}
            </button>
          </>}
        >
          <p>{t("provider.removeModelDescription")}</p>
          {removingSelected ? replacements.length ? (
            <div className="model-connect-field">
              <span>{t("provider.replacementModel")}</span>
              <SelectMenu
                triggerClassName="settings-select-trigger"
                ariaLabel={t("provider.replacementModel")}
                dataTestid="settings-provider-replacement-model"
                value={replacement}
                placeholder={t("provider.chooseReplacementModel")}
                options={replacements.map((model) => ({ value: model.id, label: providerModelDisplayName(model), keywords: [model.id] }))}
                searchable={replacements.length > MODEL_SEARCH_THRESHOLD}
                searchPlaceholder={t("provider.searchModels")}
                disabled={disabled}
                onChange={setReplacement}
                flip
              />
            </div>
          ) : <p>{t("provider.lastModel")}</p> : null}
          {removeError ? <p className="settings-error" role="alert">{error || removeError}</p> : null}
        </Modal>
      ) : null}
    </SettingsSection>
  );
}

/* -------------------------------------------------------------------------- */
/*  Connecting                                                                  */
/* -------------------------------------------------------------------------- */

// The full catalog is long enough to need its kinds named; the page's short
// featured list reads as one run in the same order.
function CatalogServiceGroups({
  catalogProviders,
  providers,
  query = "",
  grouped,
  disabled,
  onConnect,
}: {
  catalogProviders: readonly CatalogProviderSummary[];
  providers: readonly ProviderSummary[];
  query?: string;
  grouped: boolean;
  disabled: boolean;
  onConnect: (target: ConnectTarget) => void;
}): JSX.Element {
  const { t } = useI18n();
  const normalized = query.trim().toLocaleLowerCase();
  const names = catalogProviders.map((provider) => catalogServiceName(provider.id, provider.name, t));
  const entries = catalogProviders.map((provider, index) => ({
    provider,
    name: names[index],
    host: hostFromBaseURL(provider.base_url),
    ambiguous: names.indexOf(names[index]) !== names.lastIndexOf(names[index]),
    connected: providers.filter((item) => item.catalog_id === provider.id),
  })).filter((entry) => !normalized || `${entry.name} ${entry.provider.name} ${entry.provider.id} ${entry.host}`.toLocaleLowerCase().includes(normalized));
  const subscriptions = isTouchWebShell() ? [] : (["xai-subscription", "grok-build"] as const)
    .map((type) => ({
      type,
      name: type === "grok-build" ? t("provider.grokBuild") : t("provider.xaiSubscription"),
      connected: providers.filter((provider) => type === "grok-build" ? isGrokBuildType(provider.type) : isXAISubscriptionType(provider.type)),
    }))
    .filter((entry) => !normalized || `${entry.name} ${entry.type}`.toLocaleLowerCase().includes(normalized));
  const groups = [
    { id: "api", title: t("provider.catalogAPI"), hint: t("provider.catalogAPIHint"), entries: entries.filter((entry) => !isLocalCatalogService(entry.provider) && !GATEWAY_SERVICES.has(entry.provider.id)) },
    { id: "relay", title: t("provider.catalogRelay"), hint: t("provider.catalogRelayHint"), entries: entries.filter((entry) => !isLocalCatalogService(entry.provider) && GATEWAY_SERVICES.has(entry.provider.id)) },
    { id: "local", title: t("provider.catalogLocal"), hint: undefined, entries: entries.filter((entry) => isLocalCatalogService(entry.provider)) },
  ];

  function connectionState(connected: readonly ProviderSummary[]): JSX.Element | null {
    if (!connected.length) return null;
    const ready = connected.some((provider) => !serviceIdentity(provider, t).attention);
    const label = ready ? t("provider.connectedSection") : serviceIdentity(connected[0], t).attention;
    return <span className="model-catalog-state" data-ready={ready ? "" : undefined} role="img" aria-label={label} title={label}>
      <span className="model-catalog-state-dot" />
      {connected.length > 1 ? <span>{connected.length}</span> : null}
    </span>;
  }

  const directTile = !isTouchWebShell() && (!normalized || `chatgpt openai codex ${t("provider.authUseLocal")}`.toLocaleLowerCase().includes(normalized)) ? <button type="button" className="model-service-tile" data-connection="chatgpt" disabled={disabled} onClick={() => onConnect({ kind: "chatgpt" })}><ProviderMark id="openai" label="ChatGPT" /><span className="model-browse-copy"><span className="model-service-tile-name">ChatGPT</span><small>{t("provider.methodChatGPT")}</small></span></button> : null;
  const subscriptionTiles = subscriptions.map((entry) => (
    <button
      key={entry.type}
      type="button"
      className="model-service-tile"
      data-subscription={entry.type}
      disabled={disabled}
      onClick={() => onConnect({ kind: "subscription", type: entry.type })}
    >
      {<span className="provider-mark" aria-hidden="true"><EngineIcon engine="grok" className="provider-mark-engine" /></span>}
      <span className="model-catalog-name-line"><span className="model-service-tile-name">{entry.name}</span>{connectionState(entry.connected)}</span>
    </button>
  ));
  const catalogTiles = (group: (typeof groups)[number]) => group.entries.map((entry) => (
    <button
      key={entry.provider.id}
      type="button"
      className="model-service-tile"
      data-catalog={entry.provider.id}
      disabled={disabled}
      onClick={() => onConnect({ kind: "catalog", provider: entry.provider })}
    >
      <ProviderMark id={entry.provider.id} label={entry.name} />
      <span className="model-browse-copy">
        <span className="model-catalog-name-line"><span className="model-service-tile-name">{entry.name}</span>{connectionState(entry.connected)}</span>
        <small>{entry.ambiguous ? entry.host : t("provider.apiKey")}</small>
      </span>
    </button>
  ));
  const empty = entries.length === 0 && subscriptions.length === 0 && !directTile
    ? <p className="settings-group-empty">{t("provider.noServiceMatches")}</p>
    : null;

  if (!grouped) {
    return <>
      <div className="model-service-tiles">
        {directTile}
        {subscriptionTiles}
        {groups.flatMap(catalogTiles)}
      </div>
      {empty}
    </>;
  }

  return <>
    {subscriptions.length || directTile ? (
      <section className="model-catalog-group" aria-label={t("provider.catalogSubscription")}>
        <div className="model-catalog-heading"><h3>{t("provider.catalogSubscription")}</h3></div>
        <div className="model-service-tiles">{directTile}{subscriptionTiles}</div>
      </section>
    ) : null}
    {groups.filter((group) => group.entries.length > 0).map((group) => (
      <section key={group.id} className="model-catalog-group" data-catalog-group={group.id} aria-label={group.title}>
        <div className="model-catalog-heading"><h3>{group.title}</h3>{group.hint ? <span>{group.hint}</span> : null}</div>
        <div className="model-service-tiles">{catalogTiles(group)}</div>
      </section>
    ))}
    {empty}
  </>;
}

function BrowseServicesDialog({
  inline = false,
  catalogExtras,
  catalog,
  providers,
  disabled,
  onClose,
  onConnect,
}: {
  inline?: boolean;
  catalogExtras?: (query: string) => ReactNode;
  catalog: CatalogState;
  providers: readonly ProviderSummary[];
  disabled: boolean;
  onClose: () => void;
  onConnect: (target: ConnectTarget) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const Surface = inline ? ConnectionPage : Modal;
  return (
    <Surface
      ariaLabel={t("provider.browseTitle")}
      title={t(inline ? "provider.addConnection" : "provider.browseTitle")}
      panelClassName="model-dialog model-browse-dialog"
      onClose={onClose}
      footer={
        <button type="button" className="settings-button settings-button-ghost model-service-custom-action" data-testid="settings-provider-custom" disabled={disabled} onClick={() => onConnect({ kind: "custom" })}>
          <Plus className="icon" aria-hidden="true" />{t("provider.customEndpoint")}
        </button>
      }
    >
      <CatalogSearchField value={query} placeholder={t("provider.searchServices")} onValueChange={setQuery} />
      {catalogExtras?.(query)}
      <div className="model-browse-list" data-scroll-fade="">
        {catalog.failed ? <p className="settings-section-note">{t("provider.catalogUnavailable")}</p> : null}
      <SettingsSection title={inline ? t("provider.providerConnections") : undefined}><CatalogServiceGroups catalogProviders={catalog.providers ?? []} providers={providers} query={query} grouped={!inline} disabled={disabled} onConnect={onConnect} /></SettingsSection>
      </div>
    </Surface>
  );
}

function ConnectServiceDialog({
  inline = false,
  renderAuthMethods,
  target,
  providers,
  running,
  defaultOn,
  onClose,
  onSave,
  onConnected,
}: {
  inline?: boolean;
  target: Exclude<ConnectTarget, { kind: "chatgpt" }>;
  renderAuthMethods?: (disabled: boolean) => ReactNode;
  providers: readonly ProviderSummary[];
  running: boolean;
  defaultOn: boolean;
  onClose: () => void;
  onSave: SaveProvider;
  onConnected: (name: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const fieldID = useId();
  const catalogProvider = target.kind === "catalog" ? target.provider : undefined;
  const subscriptionType = target.kind === "subscription" ? target.type : undefined;
  const existingCodex = subscriptionType === "openai-codex" ? providers.find((provider) => isCodexType(provider.type)) : undefined;
  const title = catalogProvider
    ? catalogServiceName(catalogProvider.id, catalogProvider.name, t)
    : subscriptionType === "openai-codex" ? "ChatGPT" : subscriptionType === "grok-build"
      ? t("provider.grokBuild")
      : subscriptionType
        ? t("provider.xaiSubscription")
        : t("provider.customEndpoint");
  const heading = target.kind === "custom" ? t("provider.connectCustomTitle") : t("provider.connectTitle", { name: title });
  const [models, setModels] = useState<CatalogModelSummary[] | undefined>(undefined);
  const [apiKey, setAPIKey] = useState("");
  const [model, setModel] = useState(existingCodex?.model ?? catalogProvider?.default_model ?? (subscriptionType === "openai-codex" ? "gpt-6-astra" : subscriptionType === "grok-build" ? "grok-4.5" : subscriptionType ? "grok-4.7" : ""));
  const [baseURL, setBaseURL] = useState(catalogProvider?.base_url ?? "");
  const [protocol, setProtocol] = useState(catalogProvider?.type ?? "openai-compatible");
  const [name, setName] = useState(() => existingCodex?.name ?? uniqueProviderName(catalogProvider?.id ?? subscriptionType ?? "custom", providers));
  const [makeDefault, setMakeDefault] = useState(defaultOn);
  const [pending, setPending] = useState(false);
  const [loginCode, setLoginCode] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!catalogProvider) return;
    let active = true;
    void window.wuu.listCatalogProviders(catalogProvider.id).then(
      (result) => { if (active) setModels(result.providers[0]?.models ?? []); },
      () => { if (active) setModels([]); },
    );
    return () => { active = false; };
  }, [catalogProvider]);

  const nameTaken = providers.some((provider) => provider.name === name.trim() && provider.name !== existingCodex?.name);
  const needsKey = !subscriptionType;
  const canSubmit = !pending && !running && !nameTaken && Boolean(name.trim()) && Boolean(model.trim()) &&
    (!needsKey || Boolean(apiKey.trim())) && (Boolean(subscriptionType) || Boolean(baseURL.trim()));
  const modelOptions = (models ?? (catalogProvider ? [{ id: catalogProvider.default_model }] : []))
    .filter((item) => item.tool_call !== false || item.id === model)
    .map((item) => ({
      value: item.id,
      label: item.name || item.id,
      keywords: [item.id],
    }));

  async function submit(): Promise<void> {
    if (!canSubmit) return;
    setError("");
    setPending(true);
    try {
      if (subscriptionType === "xai-subscription") await runXAILogin(setLoginCode, t);
      const connection: RuntimeConnectionUpdate = {
        ...(existingCodex ? {} : { type: subscriptionType ?? protocol, create_provider: true }),
        keep_selection: !makeDefault,
        ...(subscriptionType ? {} : { base_url: baseURL.trim() }),
        ...(needsKey ? { api_key: apiKey.trim() } : {}),
        ...(subscriptionType === "openai-codex" ? { reuse_codex_credentials: true } : {}),
      };
      if (existingCodex) await window.wuu.useCodexCredentials(existingCodex.name);
      await onSave(name.trim(), model.trim(), undefined, connection);
      onConnected(name.trim());
    } catch (saveError) {
      setError(errorMessage(saveError, t));
    } finally {
      setLoginCode("");
      setPending(false);
    }
  }

  const Surface = inline ? ConnectionPage : Modal;
  return (
    <Surface
      ariaLabel={heading}
      icon={catalogProvider ? <ProviderMark id={catalogProvider.id} label={title} />
        : subscriptionType === "openai-codex" ? <ProviderMark id="openai" label="ChatGPT" /> : subscriptionType ? <span className="provider-mark" aria-hidden="true"><EngineIcon engine="grok" className="provider-mark-engine" /></span>
          : <span className="provider-mark is-quiet" aria-hidden="true"><Code2 className="icon" /></span>}
      title={heading}
      subtitle={loginCode ? t("provider.xaiLoginCode", { code: loginCode }) : undefined}
      panelClassName="model-dialog model-connect-dialog"
      closeDisabled={pending}
      onClose={onClose}
      asForm
      onSubmit={() => void submit()}
      footer={<>
        {!inline ? <button type="button" className="settings-button settings-button-ghost" disabled={pending} onClick={onClose}>
          {t("common.cancel")}
        </button> : null}
        <button type="submit" className="settings-button settings-button-primary" data-testid="settings-provider-connect" disabled={!canSubmit}>
          {pending ? t("provider.connecting") : t("provider.connectAction")}
        </button>
      </>}
    >
      <div className="model-connect-body">
        {renderAuthMethods?.(pending || running)}
        {subscriptionType === "openai-codex" ? <small className="connection-auth-source" data-auth-source="codex-local">{t("provider.authCodexSource")}</small> : null}
        {target.kind === "custom" ? (
          <div className="model-connect-field">
            <span>{t("provider.protocol")}</span>
            <div className="theme-segmented" role="group" aria-label={t("provider.protocol")}>
              {(["openai-compatible", "anthropic"] as const).map((value) => (
                <button key={value} type="button" aria-pressed={protocol === value} onClick={() => setProtocol(value)}>
                  {value === "anthropic" ? t("provider.anthropicCompatible") : t("provider.openaiCompatible")}
                </button>
              ))}
            </div>
          </div>
        ) : null}
        {target.kind === "custom" ? (
          <label className="model-connect-field">
            <span>{t("settings.baseURL")}</span>
            <input
              className="settings-input"
              data-testid="settings-provider-connect-base-url"
              value={baseURL}
              placeholder="https://api.example.com/v1"
              spellCheck={false}
              onChange={(event) => setBaseURL(event.currentTarget.value)}
            />
          </label>
        ) : null}
        {needsKey ? (
          <label className="model-connect-field">
            <span>{protocol === "anthropic" ? t("provider.authToken") : t("provider.apiKey")}</span>
            <input
              className="settings-input"
              data-testid="settings-provider-connect-key"
              type="password"
              autoComplete="new-password"
              spellCheck={false}
              value={apiKey}
              placeholder={t("provider.pasteKey")}
              onChange={(event) => setAPIKey(event.currentTarget.value)}
            />
          </label>
        ) : null}
        <div className="model-connect-field">
          <span id={`${fieldID}-model`}>{target.kind === "custom" ? t("provider.modelId") : t("provider.modelLabel")}</span>
          {catalogProvider ? (
            <SelectMenu
              triggerClassName="settings-select-trigger"
              ariaLabel={t("provider.modelLabel")}
              dataTestid="settings-provider-connect-model"
              value={model}
              options={modelOptions}
              searchable={modelOptions.length > MODEL_SEARCH_THRESHOLD}
              searchPlaceholder={t("provider.searchModels")}
              emptyMessage={t("provider.noModelMatches")}
              flip
              onChange={setModel}
            />
          ) : (
            <input
              className="settings-input"
              aria-labelledby={`${fieldID}-model`}
              data-testid="settings-provider-connect-model"
              spellCheck={false}
              value={model}
              onChange={(event) => setModel(event.currentTarget.value)}
            />
          )}
        </div>
        <div className="model-connect-toggle">
          <span id={`${fieldID}-default`}>{t("provider.useAsDefault")}</span>
          <button
            type="button"
            className="settings-switch"
            role="switch"
            aria-checked={makeDefault}
            aria-labelledby={`${fieldID}-default`}
            data-testid="settings-provider-connect-default"
            onClick={() => setMakeDefault((value) => !value)}
          >
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
        </div>
        <details className="model-connect-advanced">
          <summary><ChevronRight className="icon-sm settings-disclosure-chevron" aria-hidden="true" />{t("provider.advanced")}</summary>
          <div className="model-connect-advanced-body">
          {catalogProvider ? (
            <label className="model-connect-field">
              <span>{t("settings.baseURL")}</span>
              <input
                className="settings-input"
                value={baseURL}
                spellCheck={false}
                onChange={(event) => setBaseURL(event.currentTarget.value)}
              />
            </label>
          ) : null}
          <label className="model-connect-field">
            <span>{t("provider.identifier")}</span>
            <input
              className="settings-input"
              data-testid="settings-provider-connect-name"
              value={name}
              disabled={Boolean(existingCodex)}
              spellCheck={false}
              aria-invalid={nameTaken || undefined}
              onChange={(event) => setName(event.currentTarget.value)}
            />
            {nameTaken ? <small className="settings-error">{t("provider.nameExists")}</small> : null}
          </label>
          </div>
        </details>
        {error ? <p className="environment-dialog-error" role="alert">{error}</p> : null}
      </div>
    </Surface>
  );
}
