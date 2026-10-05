import { ChevronRight, LoaderCircle } from "./WuuIcons";
import { type KeyboardEvent, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  EngineInfo,
  EngineListResult,
  EngineUpdateParams,
  ExtensionInventoryRecord,
  ExtensionPackageUpdateParams,
  ProviderSummary,
  RecentWorkspace,
  RecentWorkspacesResult,
  RuntimeConnectionUpdate,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import { ServiceConnector, ServiceMark, serviceIdentity, useCatalogProviders } from "./ModelServicesPage";
import { ONBOARDING_PLUGIN_ORDER, PLUGIN_DESCRIPTION_KEYS, RECOMMENDED_PLUGIN_IDS } from "./onboardingCatalog";
import { OnboardingMascotStage } from "./OnboardingMascotStage";
import { PREVIEW_PLUGINS, PREVIEW_WORKSPACES } from "./onboardingPreview";
import { ProviderMark } from "./ProviderMarks";
import { PluginIcon } from "./PublicIcon";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { applyThemePreference } from "./Theme";
import { toastErrorMessage } from "./Toast";

type OnboardingStep = "welcome" | "plugins" | "runtime" | "provider" | "ready";
type PluginPreset = "minimal" | "recommended" | "all" | "custom";

const STEP_ORDER: readonly OnboardingStep[] = ["welcome", "plugins", "runtime", "provider", "ready"];
const RECOMMENDED_ENGINE = "wuu";

export function bundledOnboardingPlugins(
  inventory: readonly ExtensionInventoryRecord[] | undefined,
): ExtensionInventoryRecord[] {
  if (!inventory) return [];
  const order = new Map<string, number>(
    ONBOARDING_PLUGIN_ORDER.map((id, index) => [id, index]),
  );
  return inventory
    .filter(
      (record) =>
        record.kind === "plugin" &&
        record.package_source === "bundled" &&
        record.provenance.official === true &&
        order.has(record.provenance.plugin_id ?? ""),
    )
    .sort(
      (left, right) =>
        (order.get(left.provenance.plugin_id ?? "") ?? 99) -
        (order.get(right.provenance.plugin_id ?? "") ?? 99),
    );
}

export function hasOnboardingProvider(
  providers: readonly ProviderSummary[] | undefined,
): boolean {
  return providers?.some((provider) => isConfiguredOnboardingProvider(provider)) ?? false;
}

export function discoveredCodexCredential(
  providers: readonly ProviderSummary[] | undefined,
): ProviderSummary | undefined {
  return providers?.find((provider) => isCodexSubscriptionProvider(provider) && Boolean(provider.codex_credential_source));
}

function isConfiguredOnboardingProvider(provider: ProviderSummary): boolean {
  if (provider.api_key_configured === true) return true;
  if (isCodexSubscriptionProvider(provider)) {
    return provider.codex_credential_source === "wuu-auth-store" || provider.reuse_codex_credentials === true;
  }
  return provider.connection_locked === true;
}

function isCodexSubscriptionProvider(provider: ProviderSummary): boolean {
  const type = provider.type.trim().toLowerCase().replaceAll("_", "-");
  return type === "openai-codex" || type === "codex-subscription" || type === "chatgpt-codex";
}

const STEP_TITLES: Readonly<Record<OnboardingStep, TranslationKey>> = {
  welcome: "onboarding.welcomeTitle",
  plugins: "onboarding.pluginsTitle",
  runtime: "onboarding.runtimeTitle",
  provider: "onboarding.providerTitle",
  ready: "onboarding.readyTitle",
};

export function FirstRunOnboarding({
  inventory: liveInventory,
  providers: liveProviders,
  engines,
  preview = false,
  onDismissPreview,
  onUpdateExtensionPackage,
  onSaveProvider,
  onUpdateEngines,
  onComplete,
}: {
  inventory?: readonly ExtensionInventoryRecord[];
  providers?: readonly ProviderSummary[];
  engines?: EngineListResult;
  preview?: boolean;
  onDismissPreview?: () => void;
  onUpdateExtensionPackage: (update: ExtensionPackageUpdateParams) => Promise<void>;
  onSaveProvider: (
    provider: string,
    model: string,
    connection: RuntimeConnectionUpdate,
  ) => Promise<void>;
  onUpdateEngines?: (params: EngineUpdateParams) => Promise<unknown>;
  onComplete: (paths?: string[]) => Promise<void>;
}): JSX.Element {
  const { t, locale } = useI18n();
  const titleID = useId();
  const titleRef = useRef<HTMLHeadingElement>(null);
  // Preview keeps connections isolated but uses real CLI discovery.
  const inventory = preview ? PREVIEW_PLUGINS : liveInventory;
  const providers = preview ? undefined : liveProviders;
  const bundledPlugins = useMemo(() => bundledOnboardingPlugins(inventory), [inventory]);
  const [step, setStep] = useState<OnboardingStep>("welcome");
  const [selectedPluginIDs, setSelectedPluginIDs] = useState<Set<string>>(
    () => recommendedPluginSubjectIDs(bundledPlugins),
  );
  const initializedPluginSelection = useRef(bundledPlugins.length > 0);
  const [applyingPlugins, setApplyingPlugins] = useState(false);
  const [savingProvider, setSavingProvider] = useState(false);
  // A preview connection is not saved, so the ready step learns of it here.
  const [connected, setConnected] = useState(false);
  const [savingRuntime, setSavingRuntime] = useState(false);
  const [finishing, setFinishing] = useState(false);
  const [workspaceScan, setWorkspaceScan] = useState<RecentWorkspacesResult>();
  const [scanningWorkspaces, setScanningWorkspaces] = useState(false);
  const [choosingWorkspace, setChoosingWorkspace] = useState(false);
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(() => new Set());
  const [scanAttempt, setScanAttempt] = useState(0);
  const scannedRevision = useRef(-1);
  const [error, setError] = useState<{ title: string; detail?: string } | null>(null);
  const [selectedEngine, setSelectedEngine] = useState(RECOMMENDED_ENGINE);
  const catalog = useCatalogProviders(0);
  const discoveredCodex = discoveredCodexCredential(providers);
  const providerReady = hasOnboardingProvider(providers);
  const preset = selectedPreset(selectedPluginIDs, bundledPlugins);
  const currentStepIndex = STEP_ORDER.indexOf(step);
  const busy = applyingPlugins || savingRuntime || savingProvider || finishing || choosingWorkspace;
  const wornPluginIDs = useMemo(() => bundledPlugins
    .filter((plugin) => selectedPluginIDs.has(plugin.id))
    .map((plugin) => plugin.provenance.plugin_id ?? "")
    .filter(Boolean), [bundledPlugins, selectedPluginIDs]);
  // Only agents that can run now are offered; the rest wait in Settings,
  // where they can be installed or turned on.
  const selectableEngines = useMemo(
    () => [
      { id: RECOMMENDED_ENGINE, label: engineLabel(RECOMMENDED_ENGINE) },
      ...(engines?.engines ?? [])
        .filter((engine: EngineInfo) => engine.id !== RECOMMENDED_ENGINE && engine.enabled && engine.binary_ok)
        .map((engine: EngineInfo) => ({ id: engine.id, label: engineLabel(engine.id, engine) })),
    ],
    [engines],
  );
  const externalEngine = selectedEngine === RECOMMENDED_ENGINE
    ? undefined
    : selectableEngines.find((engine) => engine.id === selectedEngine)?.label;

  useLayoutEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = "light";
    return () => {
      applyThemePreference(window.wuu?.initialThemePreference ?? "system");
    };
  }, []);

  useEffect(() => {
    if (initializedPluginSelection.current || bundledPlugins.length === 0) return;
    initializedPluginSelection.current = true;
    setSelectedPluginIDs(recommendedPluginSubjectIDs(bundledPlugins));
  }, [bundledPlugins]);

  useEffect(() => {
    setSelectedEngine((current) => {
      const stillReady = selectableEngines.some((engine) => engine.id === current);
      return stillReady ? current : RECOMMENDED_ENGINE;
    });
  }, [selectableEngines]);

  // Each step starts at its title: assistive technology announces the new
  // step, Tab continues into its choices, and Enter takes the primary action.
  useEffect(() => {
    setError(null);
    titleRef.current?.focus({ preventScroll: true });
  }, [step]);

  useEffect(() => {
    if (step !== "ready" || scannedRevision.current === scanAttempt) return;
    let current = true;
    setScanningWorkspaces(true);
    setError(null);
    const scan = preview
      ? Promise.resolve({ candidates: PREVIEW_WORKSPACES, incomplete: false })
      : window.wuu?.discoverRecentWorkspaces?.() ?? Promise.resolve({ candidates: [], incomplete: false });
    void scan.then((result) => {
      if (!current) return;
      const firstScan = scannedRevision.current < 0;
      scannedRevision.current = scanAttempt;
      const manual = (workspaceScan?.candidates ?? []).filter((item) => item.sources.length === 0 && !result.candidates.some((found) => found.path === item.path));
      const candidates = [...result.candidates, ...manual];
      setWorkspaceScan({ ...result, candidates });
      const now = Date.now();
      setSelectedPaths((current) => firstScan ? new Set(candidates.filter((item) => {
        const age = now - Date.parse(item.lastUsedAt);
        return item.isRepository && item.sessionCount >= 3 && age >= 0 && age <= 30 * 24 * 60 * 60 * 1000;
      }).map((item) => item.path)) : new Set(candidates.filter((item) => current.has(item.path)).map((item) => item.path)));
    }).catch((reason: unknown) => {
      if (current) failure(reason, "onboarding.discoveryFailed");
    }).finally(() => { if (current) setScanningWorkspaces(false); });
    return () => { current = false; };
  }, [step, preview, scanAttempt]);

  function toggleWorkspace(path: string): void {
    setSelectedPaths((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }

  async function chooseWorkspace(): Promise<void> {
    setChoosingWorkspace(true);
    setError(null);
    try {
      const path = preview ? "/Users/example/Projects/new-workspace" : await window.wuu.chooseWorkspaceDirectory?.();
      if (!path) return;
      const candidate: RecentWorkspace = {
        path, name: path.split(/[\\/]/).filter(Boolean).pop() ?? path,
        sources: [], sessionCount: 0, lastUsedAt: "", isRepository: false,
      };
      setWorkspaceScan((current) => ({
        candidates: current?.candidates.some((item) => item.path === path)
          ? current.candidates : [...(current?.candidates ?? []), candidate],
        incomplete: current?.incomplete ?? false,
      }));
      setSelectedPaths((current) => new Set([...current, path]));
    } catch (reason) { failure(reason, "onboarding.discoveryFailed"); }
    finally { setChoosingWorkspace(false); }
  }

  function failure(reason: unknown, fallback: TranslationKey): void {
    const detail = toastErrorMessage(reason) || undefined;
    setError({ title: t(fallback), detail });
  }

  function choosePreset(next: Exclude<PluginPreset, "custom">): void {
    if (next === "minimal") {
      setSelectedPluginIDs(new Set());
      return;
    }
    if (next === "all") {
      setSelectedPluginIDs(new Set(bundledPlugins.map((plugin) => plugin.id)));
      return;
    }
    setSelectedPluginIDs(recommendedPluginSubjectIDs(bundledPlugins));
  }

  function togglePlugin(id: string): void {
    setSelectedPluginIDs((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function applyPluginChoices(): Promise<void> {
    if (applyingPlugins || bundledPlugins.length === 0) return;
    if (preview) {
      setStep("runtime");
      return;
    }
    setApplyingPlugins(true);
    setError(null);
    try {
      for (const plugin of bundledPlugins) {
        const shouldEnable = selectedPluginIDs.has(plugin.id);
        const enabled = plugin.enabled !== false;
        if (shouldEnable !== enabled) {
          await onUpdateExtensionPackage({
            id: plugin.id,
            action: shouldEnable ? "enable" : "disable",
          });
        }
      }
      setStep("runtime");
    } catch (reason) {
      failure(reason, "onboarding.pluginsFailed");
    } finally {
      setApplyingPlugins(false);
    }
  }

  async function applyRuntimeChoices(): Promise<void> {
    if (savingRuntime) return;
    if (preview) {
      setStep("provider");
      return;
    }
    setSavingRuntime(true);
    setError(null);
    try {
      if (onUpdateEngines) {
        await onUpdateEngines({ default_engine: selectedEngine });
      }
      setStep("provider");
    } catch (reason) {
      failure(reason, "onboarding.runtimeFailed");
    } finally {
      setSavingRuntime(false);
    }
  }

  async function reuseCodexLogin(): Promise<void> {
    if (!discoveredCodex || savingProvider) return;
    setSavingProvider(true);
    setError(null);
    try {
      if (!preview) {
        await onSaveProvider(discoveredCodex.name, discoveredCodex.model, {
          reuse_codex_credentials: true,
        });
      }
      setConnected(true);
      setStep("ready");
    } catch (reason) {
      failure(reason, "provider.saveFailed");
    } finally {
      setSavingProvider(false);
    }
  }

  async function finish(paths = [...selectedPaths]): Promise<void> {
    if (finishing) return;
    if (preview) {
      onDismissPreview?.();
      return;
    }
    setFinishing(true);
    setError(null);
    try {
      await onComplete(paths);
    } catch (reason) {
      failure(reason, "onboarding.finishFailed");
      setFinishing(false);
    }
  }

  const primary: { label: string; disabled: boolean; run: () => void } | undefined =
    step === "welcome" ? { label: t("onboarding.begin"), disabled: false, run: () => setStep("plugins") }
      : step === "plugins" ? {
        label: applyingPlugins ? t("onboarding.applying") : t("onboarding.continue"),
        disabled: applyingPlugins || bundledPlugins.length === 0,
        run: () => void applyPluginChoices(),
      }
        : step === "runtime" ? {
          label: savingRuntime ? t("onboarding.applying") : t("onboarding.continue"),
          disabled: savingRuntime,
          run: () => void applyRuntimeChoices(),
        }
          // Without a connection the service tiles are the step's actions.
          : step === "provider" ? (providerReady ? { label: t("onboarding.continue"), disabled: false, run: () => setStep("ready") } : undefined)
            : { label: finishing ? t("onboarding.finishing") : selectedPaths.size > 0 ? t("onboarding.importAndStart", { count: selectedPaths.size }) : t("onboarding.enterWuu"), disabled: busy, run: () => void finish() };
  const back = step === "plugins" ? "welcome" : step === "runtime" ? "plugins" : step === "provider" ? "runtime" : step === "ready" ? "provider" : undefined;
  // Only a consequence the choices do not show earns a line under the title.
  const lead = step === "provider" && externalEngine ? t("onboarding.externalEngineConnection", { agent: externalEngine })
    : step === "ready" && selectedEngine === RECOMMENDED_ENGINE && !providerReady && !connected ? t("onboarding.readyNoModel")
      : undefined;

  const connector = (
    <ServiceConnector
      providers={providers ?? []}
      running={savingProvider}
      catalog={catalog}
      defaultOn
      onSave={async (name, model, _effort, connection) => {
        // The preview never persists; it only walks the flow.
        if (!preview) await onSaveProvider(name, model, connection ?? {});
      }}
      onConnected={() => {
        setConnected(true);
        setStep("ready");
      }}
    />
  );

  function takeDefaultAction(event: KeyboardEvent<HTMLElement>): void {
    if (event.key !== "Enter" || event.target !== titleRef.current || !primary || primary.disabled) return;
    event.preventDefault();
    primary.run();
  }

  return (
    <main className="first-run-onboarding" data-testid="first-run-onboarding">
      <header className="onboarding-chrome">
        <div
          className="onboarding-progress"
          role="progressbar"
          aria-label={t("onboarding.progress")}
          aria-valuemin={1}
          aria-valuemax={STEP_ORDER.length}
          aria-valuenow={currentStepIndex + 1}
          aria-valuetext={t("onboarding.stepProgress", { current: currentStepIndex + 1, total: STEP_ORDER.length })}
        >
          {STEP_ORDER.map((item, index) => (
            <span
              key={item}
              className="onboarding-progress-dot"
              data-state={index < currentStepIndex ? "done" : index === currentStepIndex ? "current" : undefined}
            />
          ))}
        </div>
        {preview && onDismissPreview ? (
          <button
            className="settings-button settings-button-ghost onboarding-preview-exit"
            type="button"
            data-testid="onboarding-preview-exit"
            onClick={onDismissPreview}
          >
            {t("onboarding.previewExit")}
          </button>
        ) : null}
      </header>

      <section
        className={`onboarding-stage onboarding-stage-${step}`}
        aria-labelledby={titleID}
        onKeyDown={takeDefaultAction}
      >
        <OnboardingMascotStage
          pluginIDs={step === "welcome" ? [] : wornPluginIDs}
          engineID={step === "welcome" || step === "plugins" ? undefined : selectedEngine}
        />
        <div className="onboarding-panel">
          <div className="onboarding-heading" key={step}>
            <h1 id={titleID} ref={titleRef} tabIndex={-1}>
              {t(step === "provider" && providerReady ? "onboarding.providerReadyTitle" : STEP_TITLES[step])}
            </h1>
            {lead ? <p className="onboarding-lead">{lead}</p> : null}
          </div>

          {step === "plugins" ? (
            <div className="onboarding-body" data-scroll-fade="">
              <div className="theme-segmented onboarding-presets" role="group" aria-label={t("onboarding.presets")}>
                {(["minimal", "recommended", "all"] as const).map((item) => (
                  <button
                    key={item}
                    type="button"
                    aria-pressed={preset === item}
                    disabled={applyingPlugins}
                    onClick={() => choosePreset(item)}
                  >
                    {t(`onboarding.preset.${item}`)}
                  </button>
                ))}
              </div>
              {inventory === undefined ? (
                <p className="onboarding-status" role="status">
                  <LoaderCircle className="icon settings-spin" aria-hidden="true" />
                  {t("onboarding.loadingPlugins")}
                </p>
              ) : bundledPlugins.length === 0 ? (
                <p className="onboarding-status" role="alert">{t("onboarding.pluginsUnavailable")}</p>
              ) : (
                <div className="onboarding-plugins">
                  {bundledPlugins.map((plugin) => (
                    <OnboardingPluginChoice
                      key={plugin.id}
                      plugin={plugin}
                      selected={selectedPluginIDs.has(plugin.id)}
                      disabled={applyingPlugins}
                      onToggle={() => togglePlugin(plugin.id)}
                    />
                  ))}
                </div>
              )}
            </div>
          ) : null}

          {step === "runtime" ? (
            <div className="onboarding-body" data-scroll-fade="">
              <div className="onboarding-engines" role="radiogroup" aria-labelledby={titleID}>
                {selectableEngines.map((engine) => {
                  const nameID = `${titleID}-engine-${engine.id}`;
                  return (
                    <label key={engine.id} className="onboarding-engine">
                      <input
                        className="settings-engine-radio"
                        type="radio"
                        name={`${titleID}-engine`}
                        checked={selectedEngine === engine.id}
                        disabled={savingRuntime}
                        aria-labelledby={nameID}
                        data-testid={`onboarding-engine-${engine.id}`}
                        onChange={() => setSelectedEngine(engine.id)}
                      />
                      <span className="catalog-row-mark" aria-hidden="true">
                        <EngineIcon engine={engine.id} />
                      </span>
                      <span className="catalog-row-title" id={nameID}>{engine.label}</span>
                    </label>
                  );
                })}
              </div>
            </div>
          ) : null}

          {step === "provider" ? (
            <div className="onboarding-body" data-scroll-fade="">
              {providerReady ? (
                <SettingsGroup>
                  {providers?.filter(isConfiguredOnboardingProvider).map((provider) => {
                    const identity = serviceIdentity(provider, t);
                    return (
                      <div key={provider.name} className="catalog-row onboarding-connection">
                        <span className="catalog-row-mark" aria-hidden="true"><ServiceMark identity={identity} /></span>
                        <span className="catalog-row-title">{identity.label}</span>
                        <span className="catalog-row-meta">{provider.model}</span>
                      </div>
                    );
                  })}
                </SettingsGroup>
              ) : discoveredCodex ? (
                <>
                  <SettingsGroup>
                    <button
                      className="catalog-row"
                      type="button"
                      data-testid="onboarding-reuse-codex"
                      disabled={savingProvider}
                      onClick={() => void reuseCodexLogin()}
                    >
                      <span className="catalog-row-mark" aria-hidden="true">
                        <ProviderMark id="openai" label="ChatGPT" />
                      </span>
                      <span className="catalog-row-title">{t("onboarding.reuseCodexTitle")}</span>
                      {savingProvider
                        ? <LoaderCircle className="icon settings-spin catalog-row-meta" aria-hidden="true" />
                        : <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />}
                    </button>
                  </SettingsGroup>
                  <SettingsSection title={t("onboarding.otherConnection")}>{connector}</SettingsSection>
                </>
              ) : connector}
            </div>
          ) : null}

          {step === "ready" ? (
            <div className="onboarding-body onboarding-workspaces" data-scroll-fade="">
              <p className="onboarding-lead">{t("onboarding.workspacesLead")}</p>
              <div className="onboarding-workspace-toolbar">
                <button className="settings-button" type="button" disabled={busy || scanningWorkspaces || (!preview && !window.wuu?.chooseWorkspaceDirectory)} onClick={() => void chooseWorkspace()}>
                  {t("onboarding.chooseWorkspace")}
                </button>
                {(workspaceScan?.candidates.length ?? 0) > 0 ? <button className="settings-button settings-button-ghost" type="button" disabled={busy} onClick={() => setSelectedPaths(selectedPaths.size === workspaceScan!.candidates.length ? new Set() : new Set(workspaceScan!.candidates.map((item) => item.path)))}>
                  {t(selectedPaths.size === workspaceScan!.candidates.length ? "onboarding.selectNone" : "onboarding.selectAll")}
                </button> : null}
                <button className="settings-button settings-button-ghost" type="button" disabled={busy || scanningWorkspaces} onClick={() => setScanAttempt((attempt) => attempt + 1)}>{t("onboarding.scanAgain")}</button>
              </div>
              {scanningWorkspaces ? <p className="onboarding-status" role="status"><LoaderCircle className="icon settings-spin" aria-hidden="true" />{t("onboarding.scanningWorkspaces")}</p> : null}
              {!scanningWorkspaces && workspaceScan?.candidates.length === 0 ? <p className="onboarding-status">{t("onboarding.noRecentWorkspaces")}</p> : null}
              {workspaceScan?.incomplete ? <p className="onboarding-status" role="status">{t("onboarding.workspaceScanIncomplete")}</p> : null}
              <div className="onboarding-workspace-list">
                {workspaceScan?.candidates.map((item) => (
                  <label key={item.path} className="onboarding-workspace-row">
                    <input type="checkbox" checked={selectedPaths.has(item.path)} disabled={busy} onChange={() => toggleWorkspace(item.path)} aria-label={item.name} />
                    <span className="onboarding-workspace-details">
                      <span className="onboarding-workspace-name">{item.name}</span>
                      <span className="onboarding-workspace-path" title={item.path}>{item.path}</span>
                      {item.sources.length > 0 ? <span className="onboarding-workspace-source">{item.sources.map((source) => source === "codex" ? "Codex" : "Claude Code").join(" · ")}</span> : null}
                    </span>
                    {item.lastUsedAt ? <time dateTime={item.lastUsedAt} title={new Date(item.lastUsedAt).toLocaleString(locale)}>{new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(new Date(item.lastUsedAt))}</time> : null}
                  </label>
                ))}
              </div>
            </div>
          ) : null}

          {error ? (
            <p className="onboarding-error" role="alert">
              {error.title}
              {error.detail ? <span>{error.detail}</span> : null}
            </p>
          ) : null}
        </div>

        <div className="onboarding-actions">
          {back ? (
            <button className="settings-button settings-button-ghost" type="button" disabled={busy} onClick={() => setStep(back)}>
              {t("onboarding.back")}
            </button>
          ) : null}
          {step === "provider" && !providerReady ? (
            <button className="settings-button" type="button" disabled={busy} onClick={() => setStep("ready")}>
              {t("onboarding.configureLater")}
            </button>
          ) : null}
          {step === "ready" && selectedPaths.size > 0 ? (
            <button className="settings-button settings-button-ghost" type="button" disabled={busy} onClick={() => void finish([])}>{t("onboarding.skipWorkspaces")}</button>
          ) : null}
          {primary ? (
            <button className="settings-button settings-button-primary" type="button" disabled={primary.disabled} onClick={primary.run}>
              {primary.label}
            </button>
          ) : null}
        </div>
      </section>
    </main>
  );
}

function OnboardingPluginChoice({
  plugin,
  selected,
  disabled,
  onToggle,
}: {
  plugin: ExtensionInventoryRecord;
  selected: boolean;
  disabled: boolean;
  onToggle: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const id = useId();
  const descriptionKey = PLUGIN_DESCRIPTION_KEYS[plugin.provenance.plugin_id ?? ""];
  // The whole row is the switch's label, so a click anywhere on it toggles.
  return (
    <label className="onboarding-plugin" data-plugin={plugin.provenance.plugin_id}>
      <span className="catalog-row-mark" aria-hidden="true">
        <PluginIcon icon={plugin.icon} pluginId={plugin.id} fingerprint={plugin.fingerprint ?? ""} />
      </span>
      <span className="catalog-row-title" id={`${id}-name`}>{plugin.name}</span>
      <span className="catalog-row-description" id={`${id}-description`}>
        {descriptionKey ? t(descriptionKey) : plugin.description}
      </span>
      <button
        className="settings-switch"
        type="button"
        role="switch"
        aria-checked={selected}
        aria-labelledby={`${id}-name`}
        aria-describedby={`${id}-description`}
        disabled={disabled}
        onClick={onToggle}
      >
        <span className="settings-switch-thumb" aria-hidden="true" />
      </button>
    </label>
  );
}

function selectedPreset(
  selected: ReadonlySet<string>,
  plugins: readonly ExtensionInventoryRecord[],
): PluginPreset {
  if (selected.size === 0) return "minimal";
  const available = new Set(plugins.map((plugin) => plugin.id));
  if (available.size > 0 && selected.size === available.size && [...available].every((id) => selected.has(id))) {
    return "all";
  }
  const recommended = [...recommendedPluginSubjectIDs(plugins)];
  if (selected.size === recommended.length && recommended.every((id) => selected.has(id))) {
    return "recommended";
  }
  return "custom";
}

function recommendedPluginSubjectIDs(
  plugins: readonly ExtensionInventoryRecord[],
): Set<string> {
  return new Set(
    plugins
      .filter((plugin) => RECOMMENDED_PLUGIN_IDS.has(plugin.provenance.plugin_id ?? ""))
      .map((plugin) => plugin.id),
  );
}
