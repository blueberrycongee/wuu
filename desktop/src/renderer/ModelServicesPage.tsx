import {
  type FormEvent as ReactFormEvent,
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
  ProviderModelSummary,
  ProviderSummary,
  RuntimeConnectionUpdate,
} from "../shared/protocol";
import {
  AlertTriangle,
  Check,
  ChevronLeft,
  ChevronRight,
  Code2,
  EyeOff,
  MoreHorizontal,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
} from "./WuuIcons";
import { CatalogSearchField } from "./CatalogSearchField";
import { EngineIcon } from "./EngineIcons";
import { hostSupports } from "./HostCapabilities";
import { isTouchWebShell } from "./ComposerFocus";
import { Modal } from "./Modal";
import { ProviderMark } from "./ProviderMarks";
import {
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

// Model services: which model new conversations use, the services the user
// connected with their own keys, and the providers they can connect next.
// Settings save a service without choosing it; only the default model card
// and "Make default" change what new conversations start with.

type SaveProvider = (
  provider: string,
  model: string,
  effort?: string,
  connection?: RuntimeConnectionUpdate,
  variant?: string,
) => Promise<void>;

export type ModelServicesPageProps = {
  initialized?: InitializeResult;
  running: boolean;
  runningProviderNames: ReadonlySet<string>;
  onSave: SaveProvider;
  onRemoveProvider: (provider: string) => Promise<void>;
  onRefreshModelCatalog: () => Promise<void>;
};

type ConnectTarget =
  | { kind: "catalog"; provider: CatalogProviderSummary }
  | { kind: "custom" }
  | { kind: "subscription"; type: "xai-subscription" | "grok-build" };

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

type CatalogState = { providers?: CatalogProviderSummary[]; failed: boolean };

export function useCatalogProviders(version: number): CatalogState {
  const [state, setState] = useState<CatalogState>({ failed: false });
  useEffect(() => {
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
  }, [version]);
  return state;
}

export function ModelServicesPage({
  initialized,
  running,
  runningProviderNames,
  onSave,
  onRemoveProvider,
  onRefreshModelCatalog,
}: ModelServicesPageProps): JSX.Element {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  const providers = initialized?.providers ?? [];
  const labels = useMemo(() => serviceLabels(providers, t), [providers, t]);
  const [openService, setOpenService] = useState("");
  const [catalogVersion, setCatalogVersion] = useState(0);
  const [catalogRefreshing, setCatalogRefreshing] = useState(false);
  const catalog = useCatalogProviders(catalogVersion);
  const detailProvider = providers.find((provider) => provider.name === openService);

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

  async function refreshCatalog(): Promise<void> {
    setCatalogRefreshing(true);
    try {
      await onRefreshModelCatalog();
      setCatalogVersion((value) => value + 1);
    } finally {
      setCatalogRefreshing(false);
    }
  }

  return (
    <div className="model-services" ref={rootRef} data-testid="settings-providers">
      {detailProvider ? (
        <ServiceDetail
          provider={detailProvider}
          label={labels.get(detailProvider.name) ?? detailProvider.name}
          initialized={initialized}
          running={running}
          inUse={runningProviderNames.has(detailProvider.name)}
          onBack={() => setOpenService("")}
          onSave={onSave}
          onRemove={async () => {
            await onRemoveProvider(detailProvider.name);
            setOpenService("");
          }}
        />
      ) : (
        <ServicesOverview
          providers={providers}
          labels={labels}
          initialized={initialized}
          running={running}
          catalog={catalog}
          defaultOn={!defaultUsable}
          catalogRefreshing={catalogRefreshing}
          onRefreshCatalog={() => void refreshCatalog()}
          onOpenService={setOpenService}
          onSave={onSave}
        />
      )}
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
}: {
  providers: readonly ProviderSummary[];
  running: boolean;
  catalog: CatalogState;
  defaultOn: boolean;
  onSave: SaveProvider;
  onConnected: (name: string) => void;
}): JSX.Element {
  const { t, locale } = useI18n();
  const [connectTarget, setConnectTarget] = useState<ConnectTarget | null>(null);
  const [browsing, setBrowsing] = useState(false);
  const featuredIDs = FEATURED_SERVICES[locale.startsWith("zh") ? "zh" : "en"];
  const featured = featuredIDs
    .map((id) => catalog.providers?.find((provider) => provider.id === id))
    .filter((provider): provider is CatalogProviderSummary => Boolean(provider));
  const local = (catalog.providers ?? []).filter(isLocalCatalogService);

  function startConnect(target: ConnectTarget): void {
    setBrowsing(false);
    setConnectTarget(target);
  }

  return (
    <>
      <div className="model-service-catalog" data-testid="settings-provider-tiles">
        <CatalogServiceGroups
          catalogProviders={[...featured, ...local.filter((provider) => !featuredIDs.includes(provider.id))]}
          pendingCatalogTiles={catalog.providers === undefined && !catalog.failed ? featuredIDs.length : 0}
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
      {catalog.failed ? <p className="settings-section-note">{t("provider.catalogUnavailable")}</p> : null}
      {browsing ? (
        <BrowseServicesDialog
          catalog={catalog}
          providers={providers}
          disabled={running}
          onClose={() => setBrowsing(false)}
          onConnect={startConnect}
        />
      ) : null}
      {connectTarget ? (
        <ConnectServiceDialog
          target={connectTarget}
          providers={providers}
          running={running}
          defaultOn={defaultOn}
          onClose={() => setConnectTarget(null)}
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
  labels,
  initialized,
  running,
  catalog,
  defaultOn,
  catalogRefreshing,
  onRefreshCatalog,
  onOpenService,
  onSave,
}: {
  providers: readonly ProviderSummary[];
  labels: ReadonlyMap<string, string>;
  initialized?: InitializeResult;
  running: boolean;
  catalog: CatalogState;
  defaultOn: boolean;
  catalogRefreshing: boolean;
  onRefreshCatalog: () => void;
  onOpenService: (name: string) => void;
  onSave: SaveProvider;
}): JSX.Element {
  const { t } = useI18n();

  return (
    <>
      <SettingsPageHeader
        title={t("settings.providers")}
        actions={
          <button
            className="settings-button settings-button-ghost settings-icon-button"
            type="button"
            data-testid="settings-model-catalog-refresh"
            aria-label={t("settings.modelCatalogUpdate")}
            title={t("settings.modelCatalogUpdate")}
            aria-busy={catalogRefreshing}
            disabled={catalogRefreshing}
            onClick={onRefreshCatalog}
          >
            <RefreshCw className={`icon${catalogRefreshing ? " settings-spin" : ""}`} aria-hidden="true" />
          </button>
        }
      />

      {providers.length > 0 ? (
        <DefaultModelCard providers={providers} labels={labels} initialized={initialized} running={running} onSave={onSave} />
      ) : (
        // The default model's place says what is missing; the services to
        // add follow directly below.
        <SettingsSection title={t("provider.defaultModel")}>
          <SettingsGroup>
            <p className="settings-group-empty" data-testid="settings-providers-empty">{t("provider.emptyTitle")}</p>
          </SettingsGroup>
        </SettingsSection>
      )}

      {providers.length > 0 ? (
        <SettingsSection title={t("provider.connectedSection")}>
          <div className="model-service-grid" role="list" aria-label={t("provider.connectedSection")}>
            {providers.map((provider) => {
              const identity = serviceIdentity(provider, t);
              const label = labels.get(provider.name) ?? identity.label;
              const isDefault = provider.name === initialized?.provider;
              const count = providerModels(provider).length;
              const meta = identity.attention ?? t("provider.modelCount", { count });
              return (
                <div role="listitem" key={provider.name}>
                  <button
                    type="button"
                    className="model-service-card"
                    data-testid="settings-provider-card"
                    data-provider={provider.name}
                    aria-label={label}
                    onClick={() => onOpenService(provider.name)}
                  >
                    <ServiceMark identity={identity} />
                    <span className="model-service-card-copy">
                      <span className="model-service-card-title">{label}</span>
                      <span className="model-service-card-meta" data-attention={identity.attention ? "" : undefined}>
                        {identity.attention ? <AlertTriangle className="icon-sm" aria-hidden="true" /> : null}
                        {meta}
                      </span>
                    </span>
                    {isDefault ? <span className="model-service-badge">{t("provider.defaultBadge")}</span> : null}
                    <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />
                  </button>
                </div>
              );
            })}
          </div>
        </SettingsSection>
      ) : null}

      <SettingsSection title={t("provider.addSection")}>
        <ServiceConnector
          providers={providers}
          running={running}
          catalog={catalog}
          defaultOn={defaultOn}
          onSave={onSave}
          onConnected={onOpenService}
        />
      </SettingsSection>
    </>
  );
}

// The one choice most people make here: what a new conversation uses. The
// model name is the control, so it is not repeated beside a picker.
function DefaultModelCard({
  providers,
  labels,
  initialized,
  running,
  onSave,
}: {
  providers: readonly ProviderSummary[];
  labels: ReadonlyMap<string, string>;
  initialized?: InitializeResult;
  running: boolean;
  onSave: SaveProvider;
}): JSX.Element {
  const { t } = useI18n();
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
      options: providerModels(item).map((model) => ({
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

  return (
    <SettingsSection title={t("provider.defaultModel")}>
      <div className="model-default-card" data-testid="settings-default-model">
        {identity ? <ServiceMark identity={identity} /> : <span className="provider-mark is-quiet" aria-hidden="true" />}
        <div className="model-default-copy">
          <SelectMenu
            className="model-default-select"
            triggerClassName="model-default-trigger"
            ariaLabel={t("provider.chooseDefaultModel")}
            dataTestid="settings-default-model-select"
            value={`${providerName}\n${modelID}`}
            placeholder={modelID || t("provider.chooseDefaultModel")}
            groups={groups}
            searchable={optionCount > MODEL_SEARCH_THRESHOLD}
            searchPlaceholder={t("provider.searchModels")}
            emptyMessage={t("provider.noModelMatches")}
            disabled={running || pending !== null || optionCount === 0}
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
          <span className="model-default-service">
            {identity ? labels.get(providerName) ?? identity.label : ""}
            {identity?.attention ? (
              <span className="model-default-attention">
                <AlertTriangle className="icon-sm" aria-hidden="true" />
                {identity.attention}
              </span>
            ) : null}
          </span>
        </div>
        {effortOptions.length > 1 ? (
          <div className="model-default-effort">
            <span id="model-default-effort-label">{t("provider.reasoningEffort")}</span>
            <SelectMenu
              triggerClassName="settings-select-trigger"
              ariaLabel={t("provider.reasoningEffort")}
              value={variant}
              align="right"
              disabled={running || pending !== null}
              options={effortOptions.map((option) => ({ value: option, label: variantLabel(option) }))}
              onChange={(next) => void commit({ provider: providerName, model: modelID, variant: next })}
            />
          </div>
        ) : null}
      </div>
      {error ? <p className="settings-error" role="alert">{error}</p> : null}
    </SettingsSection>
  );
}

/* -------------------------------------------------------------------------- */
/*  Service detail                                                              */
/* -------------------------------------------------------------------------- */

function ServiceDetail({
  provider,
  label,
  initialized,
  running,
  inUse,
  onBack,
  onSave,
  onRemove,
}: {
  provider: ProviderSummary;
  label: string;
  initialized?: InitializeResult;
  running: boolean;
  inUse: boolean;
  onBack: () => void;
  onSave: SaveProvider;
  onRemove: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const identity = serviceIdentity(provider, t);
  const isDefault = provider.name === initialized?.provider;
  // The workspace model is the default service's model.
  const serviceModel = isDefault ? initialized?.model ?? provider.model : provider.model;
  const currentVariant = initialized?.variant ?? initialized?.effort ?? "";
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  const removable = !provider.auto_discovered && !isCodexType(provider.type);
  const disabled = running || busy;

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

  // Edits keep the default where it is unless they are the default's model.
  const saveConnection = (connection: RuntimeConnectionUpdate): Promise<boolean> =>
    run(() => onSave(provider.name, serviceModel, undefined, { ...connection, keep_selection: true }));

  function chooseModel(model: string): void {
    if (model === serviceModel) return;
    void run(() => isDefault
      ? onSave(provider.name, model, undefined, undefined, normalizedVariantForProviderModel(currentVariant, provider, model))
      : onSave(provider.name, model, undefined, { keep_selection: true }));
  }

  return (
    <>
      <nav className="settings-page-back" aria-label={t("settings.providers")}>
        <button type="button" onClick={onBack} data-testid="settings-provider-back">
          <ChevronLeft className="icon" aria-hidden="true" />
          {t("settings.providers")}
        </button>
      </nav>

      <header className="model-service-hero">
        <ServiceMark identity={identity} />
        <div className="model-service-hero-copy">
          <h1>{label}</h1>
          {isDefault ? <span className="model-service-badge">{t("provider.defaultBadge")}</span> : null}
        </div>
        <div className="settings-detail-actions model-service-hero-actions">
          {removable ? (
            <button
              type="button"
              className="settings-button settings-button-ghost settings-icon-button"
              aria-label={t("provider.moreActions", { name: label })}
              aria-haspopup="menu"
              disabled={disabled}
              onClick={(event) => {
                const bounds = event.currentTarget.getBoundingClientRect();
                setMenu({ x: bounds.right, y: bounds.bottom + 4 });
              }}
            >
              <MoreHorizontal className="icon" aria-hidden="true" />
            </button>
          ) : null}
          {isDefault ? null : (
            <button
              type="button"
              className="settings-button"
              data-testid="settings-provider-make-default"
              disabled={disabled}
              onClick={() => void run(() => onSave(
                provider.name,
                provider.model,
                undefined,
                undefined,
                normalizedVariantForProviderModel(currentVariant, provider, provider.model),
              ))}
            >
              {t("provider.makeDefault")}
            </button>
          )}
        </div>
      </header>

      {error ? <p className="settings-error" role="alert">{error}</p> : null}

      <SettingsSection title={t("provider.connectionSection")}>
        <SettingsGroup>
          <ConnectionRows provider={provider} identity={identity} disabled={disabled} onSave={saveConnection} onRun={run} />
        </SettingsGroup>
      </SettingsSection>

      <ModelChoices
        provider={provider}
        serviceModel={serviceModel}
        disabled={disabled}
        onChoose={chooseModel}
        onHide={(model) => void saveConnection({ remove_model: model })}
        onAdd={(model) => saveConnection({ add_model: model })}
      />

      {menu ? (
        <ThreadContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[{
            label: t("provider.removeAction"),
            danger: true,
            onSelect: () => {
              if (inUse) {
                setError(t("provider.inUse"));
                return;
              }
              setConfirmingRemove(true);
            },
          }]}
        />
      ) : null}
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
              disabled={busy}
              onClick={() => {
                void run(onRemove).then(() => setConfirmingRemove(false));
              }}
            >
              {t("provider.removeAction")}
            </button>
          </>}
        />
      ) : null}
    </>
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
        <SettingsRow title={t("provider.chatgptLogin")}>
          <StatusValue
            ok={Boolean(provider.api_key_configured)}
            okLabel={t(provider.codex_credential_source === "explicit" ? "settings.codexSourceExplicit" : provider.reuse_codex_credentials ? "settings.codexSourceLocal" : "settings.codexSourceSaved")}
            missingLabel={identity.attention ?? ""}
          />
        </SettingsRow>
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
  onChoose,
  onHide,
  onAdd,
}: {
  provider: ProviderSummary;
  serviceModel: string;
  disabled: boolean;
  onChoose: (model: string) => void;
  onHide: (model: string) => void;
  onAdd: (model: string) => Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");
  const [adding, setAdding] = useState(false);
  const models = providerModels(provider);
  const hidden = provider.hidden_models ?? [];
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
        <button
          type="button"
          className="settings-button settings-button-ghost"
          data-testid="settings-provider-add-model-open"
          disabled={disabled || adding}
          onClick={() => setAdding(true)}
        >
          <Plus className="icon" aria-hidden="true" />
          {t("provider.addModel")}
        </button>
      </>}
    >
      <div className="settings-group model-choice-list" role="group" aria-label={t("provider.modelsSection")}>
        {visible.map((model) => {
          const name = providerModelDisplayName(model);
          const selected = model.id === serviceModel;
          return (
            <div className="model-choice" key={model.id} data-selected={selected ? "" : undefined}>
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
                <span className="model-choice-name">{name}</span>
                {name !== model.id ? <code className="model-choice-id">{model.id}</code> : null}
              </button>
              {selected ? <span className="model-choice-check" aria-hidden="true"><Check className="icon" /></span> : (
                <button
                  type="button"
                  className="icon-button model-choice-hide"
                  aria-label={t("provider.hideModel", { model: name })}
                  title={t("provider.hideModel", { model: name })}
                  disabled={disabled}
                  onClick={() => onHide(model.id)}
                >
                  <EyeOff className="icon" aria-hidden="true" />
                </button>
              )}
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
      {hidden.length > 0 ? (
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
                  className="icon-button model-choice-hide"
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
  pendingCatalogTiles = 0,
  providers,
  query = "",
  grouped,
  disabled,
  onConnect,
}: {
  catalogProviders: readonly CatalogProviderSummary[];
  pendingCatalogTiles?: number;
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

  const subscriptionTiles = subscriptions.map((entry) => (
    <button
      key={entry.type}
      type="button"
      className="model-service-tile"
      data-subscription={entry.type}
      disabled={disabled}
      onClick={() => onConnect({ kind: "subscription", type: entry.type })}
    >
      <span className="provider-mark" aria-hidden="true"><EngineIcon engine="grok" className="provider-mark-engine" /></span>
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
        {entry.ambiguous ? <small>{entry.host}</small> : null}
      </span>
    </button>
  ));
  const empty = entries.length === 0 && subscriptions.length === 0 && !pendingCatalogTiles
    ? <p className="settings-group-empty">{t("provider.noServiceMatches")}</p>
    : null;

  if (!grouped) {
    return <>
      <div className="model-service-tiles">
        {subscriptionTiles}
        {groups.flatMap(catalogTiles)}
        {Array.from({ length: pendingCatalogTiles }, (_, index) => (
          <div key={`pending-${index}`} className="model-service-tile model-service-skeleton" aria-hidden="true">
            <span className="model-service-skeleton-mark" />
            <span className="model-service-skeleton-name" />
          </div>
        ))}
      </div>
      {pendingCatalogTiles ? <span className="sr-only" role="status">{t("settings.loading")}</span> : null}
      {empty}
    </>;
  }

  return <>
    {subscriptions.length ? (
      <section className="model-catalog-group" aria-label={t("provider.catalogSubscription")}>
        <div className="model-catalog-heading"><h3>{t("provider.catalogSubscription")}</h3></div>
        <div className="model-service-tiles">{subscriptionTiles}</div>
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
  catalog,
  providers,
  disabled,
  onClose,
  onConnect,
}: {
  catalog: CatalogState;
  providers: readonly ProviderSummary[];
  disabled: boolean;
  onClose: () => void;
  onConnect: (target: ConnectTarget) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  return (
    <Modal
      ariaLabel={t("provider.browseTitle")}
      title={t("provider.browseTitle")}
      panelClassName="model-dialog model-browse-dialog"
      onClose={onClose}
      footer={
        <button type="button" className="settings-button settings-button-ghost model-service-custom-action" disabled={disabled} onClick={() => onConnect({ kind: "custom" })}>
          <Plus className="icon" aria-hidden="true" />{t("provider.customEndpoint")}
        </button>
      }
    >
      <CatalogSearchField value={query} placeholder={t("provider.searchServices")} onValueChange={setQuery} />
      <div className="model-browse-list" data-scroll-fade="">
        <CatalogServiceGroups catalogProviders={catalog.providers ?? []} providers={providers} query={query} grouped disabled={disabled} onConnect={onConnect} />
      </div>
    </Modal>
  );
}

function ConnectServiceDialog({
  target,
  providers,
  running,
  defaultOn,
  onClose,
  onSave,
  onConnected,
}: {
  target: ConnectTarget;
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
  const title = catalogProvider
    ? catalogServiceName(catalogProvider.id, catalogProvider.name, t)
    : subscriptionType === "grok-build"
      ? t("provider.grokBuild")
      : subscriptionType
        ? t("provider.xaiSubscription")
        : t("provider.customEndpoint");
  const heading = target.kind === "custom" ? t("provider.connectCustomTitle") : t("provider.connectTitle", { name: title });
  const [models, setModels] = useState<CatalogModelSummary[] | undefined>(undefined);
  const [apiKey, setAPIKey] = useState("");
  const [model, setModel] = useState(catalogProvider?.default_model ?? (subscriptionType === "grok-build" ? "grok-4.5" : subscriptionType ? "grok-4.7" : ""));
  const [baseURL, setBaseURL] = useState(catalogProvider?.base_url ?? "");
  const [protocol, setProtocol] = useState(catalogProvider?.type ?? "openai-compatible");
  const [name, setName] = useState(() => uniqueProviderName(catalogProvider?.id ?? subscriptionType ?? "custom", providers));
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

  const nameTaken = providers.some((provider) => provider.name === name.trim());
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
        type: subscriptionType ?? protocol,
        create_provider: true,
        keep_selection: !makeDefault,
        ...(subscriptionType ? {} : { base_url: baseURL.trim() }),
        ...(needsKey ? { api_key: apiKey.trim() } : {}),
      };
      await onSave(name.trim(), model.trim(), undefined, connection);
      onConnected(name.trim());
    } catch (saveError) {
      setError(errorMessage(saveError, t));
    } finally {
      setLoginCode("");
      setPending(false);
    }
  }

  return (
    <Modal
      ariaLabel={heading}
      icon={catalogProvider ? <ProviderMark id={catalogProvider.id} label={title} />
        : subscriptionType ? <span className="provider-mark" aria-hidden="true"><EngineIcon engine="grok" className="provider-mark-engine" /></span>
          : <span className="provider-mark is-quiet" aria-hidden="true"><Code2 className="icon" /></span>}
      title={heading}
      subtitle={loginCode ? t("provider.xaiLoginCode", { code: loginCode }) : undefined}
      panelClassName="model-dialog model-connect-dialog"
      closeDisabled={pending}
      onClose={onClose}
      asForm
      onSubmit={() => void submit()}
      footer={<>
        <button type="button" className="settings-button settings-button-ghost" disabled={pending} onClick={onClose}>
          {t("common.cancel")}
        </button>
        <button type="submit" className="settings-button settings-button-primary" data-testid="settings-provider-connect" disabled={!canSubmit}>
          {pending ? t("provider.connecting") : t("provider.connectAction")}
        </button>
      </>}
    >
      <div className="model-connect-body">
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
    </Modal>
  );
}
