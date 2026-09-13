import { Check, LoaderCircle } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  EngineInfo,
  EngineListResult,
  EngineUpdateParams,
  ExtensionInventoryRecord,
  ExtensionPackageUpdateParams,
  ProviderSummary,
  RuntimeConnectionUpdate,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { ONBOARDING_ENGINES, ONBOARDING_PLUGIN_ORDER, PLUGIN_DESCRIPTION_KEYS, RECOMMENDED_PLUGIN_IDS } from "./onboardingCatalog";
import { OnboardingMascotStage } from "./OnboardingMascotStage";
import { PREVIEW_PLUGINS } from "./onboardingPreview";
import { PluginIcon } from "./PublicIcon";
import { applyThemePreference } from "./Theme";

type OnboardingStep = "welcome" | "plugins" | "runtime" | "provider" | "ready";
type PluginPreset = "minimal" | "recommended" | "all" | "custom";

const STEP_ORDER: readonly OnboardingStep[] = ["welcome", "plugins", "runtime", "provider", "ready"];


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

export function recommendedOnboardingEngine(_engines?: readonly EngineInfo[]): string {
  return "wuu";
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

export function FirstRunOnboarding({
  inventory: liveInventory,
  providers: liveProviders,
  engines: liveEngines,
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
  onComplete: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  // Preview keeps connections isolated but uses real CLI discovery.
  const inventory = preview ? PREVIEW_PLUGINS : liveInventory;
  const providers = preview ? undefined : liveProviders;
  const engines = liveEngines;
  const bundledPlugins = useMemo(() => bundledOnboardingPlugins(inventory), [inventory]);
  const [step, setStep] = useState<OnboardingStep>("welcome");
  const [selectedPluginIDs, setSelectedPluginIDs] = useState<Set<string>>(
    () => recommendedPluginSubjectIDs(bundledPlugins),
  );
  const initializedPluginSelection = useRef(bundledPlugins.length > 0);
  const [applyingPlugins, setApplyingPlugins] = useState(false);
  const [providerName, setProviderName] = useState("");
  const [providerType, setProviderType] = useState("openai-compatible");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [xaiLogin, setXAILogin] = useState<{ userCode: string } | null>(null);
  const [savingProvider, setSavingProvider] = useState(false);
  const [savingRuntime, setSavingRuntime] = useState(false);
  const [finishing, setFinishing] = useState(false);
  const [error, setError] = useState("");
  const recommendedEngine = recommendedOnboardingEngine(engines?.engines);
  const [selectedEngine, setSelectedEngine] = useState(recommendedEngine);
  const discoveredCodex = discoveredCodexCredential(providers);
  const providerReady = hasOnboardingProvider(providers);
  const preset = selectedPreset(selectedPluginIDs, bundledPlugins);
  const currentStepIndex = STEP_ORDER.indexOf(step);
  const wornPluginIDs = useMemo(() => {
    const ids = bundledPlugins
      .filter((plugin) => selectedPluginIDs.has(plugin.id))
      .map((plugin) => plugin.provenance.plugin_id ?? "")
      .filter(Boolean);
    return ids;
  }, [bundledPlugins, selectedPluginIDs]);
  const selectableEngines = useMemo(
    () => ONBOARDING_ENGINES.flatMap((choice) => {
      if (choice.id === "wuu") return [{ ...choice, ready: true }];
      const engine = engines?.engines.find((item) => item.id === choice.id);
      return engine ? [{ ...choice, ready: engine.enabled && engine.binary_ok }] : [];
    }),
    [engines],
  );

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
      const stillReady = selectableEngines.some((engine) => engine.id === current && engine.ready);
      return stillReady ? current : recommendedEngine;
    });
  }, [recommendedEngine, selectableEngines]);

  useEffect(() => setError(""), [step]);

  function choosePreset(next: Exclude<PluginPreset, "custom">): void {
    if (next === "minimal") {
      setSelectedPluginIDs(new Set());
      return;
    }
    if (next === "all") {
      setSelectedPluginIDs(new Set(bundledPlugins.map((plugin) => plugin.id)));
      return;
    }
    setSelectedPluginIDs(
      recommendedPluginSubjectIDs(bundledPlugins),
    );
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
    setError("");
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
      setError(reason instanceof Error ? reason.message : t("onboarding.pluginsFailed"));
    } finally {
      setApplyingPlugins(false);
    }
  }

  const xaiSubscription = providerType === "xai-subscription";
  const grokBuild = providerType === "grok-build";

  async function applyRuntimeChoices(): Promise<void> {
    if (savingRuntime) return;
    if (preview) {
      setStep("provider");
      return;
    }
    setSavingRuntime(true);
    setError("");
    try {
      if (onUpdateEngines) {
        await onUpdateEngines({ default_engine: selectedEngine });
      }
      setStep("provider");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("onboarding.runtimeFailed"));
    } finally {
      setSavingRuntime(false);
    }
  }

  async function reuseCodexLogin(): Promise<void> {
    if (!discoveredCodex || savingProvider) return;
    setSavingProvider(true);
    setError("");
    try {
      if (!preview) {
        await onSaveProvider(discoveredCodex.name, discoveredCodex.model, {
          reuse_codex_credentials: true,
        });
      }
      setStep("ready");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("provider.saveFailed"));
    } finally {
      setSavingProvider(false);
    }
  }

  async function saveProvider(): Promise<void> {
    const name = providerName.trim();
    const providerModel = model.trim();
    const key = apiKey.trim();
    if (!name || !providerModel || savingProvider) return;
    if (!xaiSubscription && !grokBuild && !key) return;
    if (preview) {
      setStep("ready");
      return;
    }
    setSavingProvider(true);
    setError("");
    try {
      if (xaiSubscription) {
        const start = await window.wuu.startXAILogin();
        const url = start.verification_uri_complete || start.verification_uri;
        setXAILogin({ userCode: start.user_code });
        if (url) {
          await window.wuu.openExternal(url);
        }
        const deadline = Date.now() + Math.max(30, start.expires_in || 300) * 1000;
        let interval = Math.max(1000, start.interval_ms || 5000);
        let signedIn = false;
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
          signedIn = true;
          break;
        }
        if (!signedIn) {
          throw new Error(t("error.oauthFailed"));
        }
      }
      await onSaveProvider(name, providerModel, {
        type: providerType,
        create_provider: true,
        ...(xaiSubscription
          ? { base_url: "https://api.x.ai/v1" }
          : grokBuild
            ? { base_url: "https://cli-chat-proxy.grok.com/v1" }
            : { api_key: key }),
      });
      setStep("ready");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("provider.saveFailed"));
    } finally {
      setXAILogin(null);
      setSavingProvider(false);
    }
  }

  async function finish(): Promise<void> {
    if (finishing) return;
    if (preview) {
      onDismissPreview?.();
      return;
    }
    setFinishing(true);
    setError("");
    try {
      await onComplete();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("onboarding.finishFailed"));
      setFinishing(false);
    }
  }

  return (
    <main className="first-run-onboarding" data-testid="first-run-onboarding">
      <header className="onboarding-chrome">
        <div className="onboarding-progress" aria-label={t("onboarding.progress")}>
          {STEP_ORDER.map((item, index) => (
            <span
              key={item}
              className={`onboarding-progress-dot${index <= currentStepIndex ? " is-active" : ""}`}
            />
          ))}
        </div>
        {preview && onDismissPreview ? (
          <button
            className="onboarding-preview-exit"
            type="button"
            data-testid="onboarding-preview-exit"
            onClick={onDismissPreview}
          >
            {t("onboarding.previewExit")}
          </button>
        ) : null}
      </header>

      <section className={`onboarding-stage onboarding-stage-${step}`}>
        <div className="onboarding-masthead">
          <h1>{t(step === "welcome" ? "onboarding.welcomeTitle"
            : step === "plugins" ? "onboarding.pluginsTitle"
              : step === "runtime" ? "onboarding.runtimeTitle"
                : step === "provider" ? (providerReady ? "onboarding.providerReadyTitle" : "onboarding.providerTitle")
                  : "onboarding.readyTitle")}</h1>
          <OnboardingMascotStage
            pluginIDs={step === "welcome" ? [] : wornPluginIDs}
            engineID={step === "welcome" || step === "plugins" ? undefined : selectedEngine}
          />
        </div>
        {step === "welcome" ? (
          <div className="onboarding-welcome">
            <div className="onboarding-actions onboarding-actions-end">
            <button className="onboarding-primary" type="button" onClick={() => setStep("plugins")}>
              {t("onboarding.begin")}
            </button>
            </div>
          </div>
        ) : null}

        {step === "plugins" ? (
          <div className="onboarding-panel onboarding-plugins-panel">
            <div className="onboarding-body">
                <div className="onboarding-presets" aria-label={t("onboarding.presets")}>
                  {(["minimal", "recommended", "all"] as const).map((item) => (
                    <button
                      key={item}
                      type="button"
                      className={preset === item ? "is-selected" : ""}
                      aria-pressed={preset === item}
                      disabled={applyingPlugins}
                      onClick={() => choosePreset(item)}
                    >
                      {t(`onboarding.preset.${item}`)}
                    </button>
                  ))}
                </div>
            {inventory === undefined ? (
              <div className="onboarding-loading" role="status">
                <LoaderCircle className="is-spinning" />
                {t("onboarding.loadingPlugins")}
              </div>
            ) : bundledPlugins.length === 0 ? (
              <div className="onboarding-loading" role="alert">
                {t("onboarding.pluginsUnavailable")}
              </div>
            ) : (
              <div className="onboarding-plugin-grid">
                {bundledPlugins.map((plugin) => {
                  const selected = selectedPluginIDs.has(plugin.id);
                  const pluginID = plugin.provenance.plugin_id ?? "";
                  const descriptionKey = PLUGIN_DESCRIPTION_KEYS[pluginID];
                  return (
                    <button
                      key={plugin.id}
                      type="button"
                      className={`onboarding-plugin${selected ? " is-selected" : ""}`}
                      aria-pressed={selected}
                      disabled={applyingPlugins}
                      onClick={() => togglePlugin(plugin.id)}
                    >
                      <span className="onboarding-plugin-icon">
                        <PluginIcon
                          icon={plugin.icon}
                          pluginId={plugin.id}
                          fingerprint={plugin.fingerprint ?? ""}
                        />
                      </span>
                      <span className="onboarding-plugin-copy">
                        <strong>{plugin.name}</strong>
                        <span>{descriptionKey ? t(descriptionKey) : plugin.description}</span>
                      </span>
                      <span className="onboarding-plugin-check" aria-hidden="true">
                        {selected ? <Check /> : null}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}

            </div>
            <OnboardingError message={error} />
            <div className="onboarding-actions">
              <button className="onboarding-back" type="button" disabled={applyingPlugins} onClick={() => setStep("welcome")}>
                {t("onboarding.back")}
              </button>
              <button
                className="onboarding-primary"
                type="button"
                disabled={applyingPlugins || bundledPlugins.length === 0}
                onClick={() => void applyPluginChoices()}
              >
                {applyingPlugins ? t("onboarding.applying") : t("onboarding.continue")}
              </button>
            </div>
          </div>
        ) : null}

        {step === "runtime" ? (
          <div className="onboarding-panel">
            <div className="onboarding-body">
            <div className="onboarding-choice-grid" role="radiogroup" aria-label={t("onboarding.runtimeTitle")}>
              {selectableEngines.map((engine) => {
                const selected = selectedEngine === engine.id;
                const recommended = recommendedEngine === engine.id;
                return (
                  <button
                    key={engine.id}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    className={`onboarding-choice${selected ? " is-selected" : ""}`}
                    data-testid={`onboarding-engine-${engine.id}`}
                    disabled={savingRuntime || !engine.ready}
                    onClick={() => setSelectedEngine(engine.id)}
                  >
                    <span className="onboarding-choice-copy">
                      <strong>
                        {engine.label}
                        {recommended ? <em>{t("onboarding.recommended")}</em> : null}
                      </strong>
                      <span>
                        {t(engine.ready ? engine.readyDescription : engine.missingDescription)}
                      </span>
                    </span>
                    <span className="onboarding-plugin-check" aria-hidden="true">
                      {selected ? <Check /> : null}
                    </span>
                  </button>
                );
              })}
            </div>
            </div>
            <OnboardingError message={error} />
            <div className="onboarding-actions">
              <button className="onboarding-back" type="button" disabled={savingRuntime} onClick={() => setStep("plugins")}>
                {t("onboarding.back")}
              </button>
              <button
                className="onboarding-primary"
                type="button"
                disabled={savingRuntime}
                onClick={() => void applyRuntimeChoices()}
              >
                {savingRuntime ? t("onboarding.applying") : t("onboarding.continue")}
              </button>
            </div>
          </div>
        ) : null}

        {step === "provider" ? (
          <div className={`onboarding-panel${providerReady ? " is-ready" : ""}`}>
            <div className="onboarding-body">
            {providerReady ? (
              <dl className="onboarding-connection-summary">
                {providers?.filter(isConfiguredOnboardingProvider).map((provider) => (
                  <div key={provider.name}>
                    <dt>{provider.name}</dt>
                    <dd>{provider.model}</dd>
                  </div>
                ))}
              </dl>
            ) : null}
            {selectedEngine !== "wuu" ? (
              <p className="onboarding-provider-description">{t("onboarding.externalEngineConnection")}</p>
            ) : null}
            {!providerReady && discoveredCodex ? (<>
              <button className="onboarding-reuse" type="button" data-testid="onboarding-reuse-codex"
                disabled={savingProvider} onClick={() => void reuseCodexLogin()}>
                <strong>{t("onboarding.reuseCodexTitle")}</strong>
                <span>{t("onboarding.reuseCodexDescription")}</span>
              </button>
              <p className="onboarding-connection-alternative">{t("onboarding.otherConnection")}</p>
            </>) : null}
            {!providerReady ? (
              <div className="onboarding-provider-form">
                <label>
                  <span>{t("provider.identifier")}</span>
                  <input value={providerName} onChange={(event) => setProviderName(event.currentTarget.value)} placeholder="openai" autoFocus />
                </label>
                <label>
                  <span>{t("provider.type")}</span>
                  <select
                    value={providerType}
                    onChange={(event) => {
                      const next = event.currentTarget.value;
                      setProviderType(next);
                      if (next === "xai-subscription") {
                        if (!providerName.trim()) setProviderName("xai-subscription");
                        if (!model.trim()) setModel("grok-4.6");
                      } else if (next === "grok-build") {
                        setProviderName("grok-build");
                        setModel("grok-4.5");
                      }
                    }}
                  >
                    <option value="openai-compatible">{t("provider.openaiCompatible")}</option>
                    <option value="anthropic">{t("provider.anthropicCompatible")}</option>
                    <option value="xai-subscription">{t("provider.xaiSubscription")}</option>
                    <option value="grok-build">{t("provider.grokBuild")}</option>
                  </select>
                </label>
                <label>
                  <span>{t("provider.modelName")}</span>
                  <input value={model} onChange={(event) => setModel(event.currentTarget.value)} placeholder="gpt-4o" />
                </label>
                {providerType === "xai-subscription" ? (
                  <p className="onboarding-provider-description">
                    {xaiLogin ? t("provider.xaiLoginCode", { code: xaiLogin.userCode }) : t("provider.xaiLoginHint")}
                  </p>
                ) : providerType === "grok-build" ? (
                  <p className="onboarding-provider-description">{t("provider.grokBuildLoginHint")}</p>
                ) : (
                <label>
                  <span>{t("provider.apiKey")}</span>
                  <input type="password" value={apiKey} onChange={(event) => setApiKey(event.currentTarget.value)} />
                </label>
                )}
              </div>
            ) : null}

            </div>
            <OnboardingError message={error} />
            <div className="onboarding-actions">
              <button className="onboarding-back" type="button" disabled={savingProvider} onClick={() => setStep("runtime")}>
                {t("onboarding.back")}
              </button>
              <div className="onboarding-action-group">
                {!providerReady ? (
                  <button className="onboarding-secondary" type="button" disabled={savingProvider} onClick={() => setStep("ready")}>
                    {t("onboarding.configureLater")}
                  </button>
                ) : null}
                <button
                  className="onboarding-primary"
                  type="button"
                  disabled={savingProvider || (!providerReady && (!providerName.trim() || !model.trim() || (providerType !== "xai-subscription" && providerType !== "grok-build" && !apiKey.trim())))}
                  onClick={() => providerReady ? setStep("ready") : void saveProvider()}
                >
                  {savingProvider ? t("onboarding.savingProvider") : t("onboarding.continue")}
                </button>
              </div>
            </div>
          </div>
        ) : null}

        {step === "ready" ? (
          <div className="onboarding-welcome onboarding-ready">
            <OnboardingError message={error} />
            <div className="onboarding-actions onboarding-actions-end">
            <button className="onboarding-primary" type="button" disabled={finishing} onClick={() => void finish()}>
              {finishing ? t("onboarding.finishing") : t("onboarding.enterWuu")}
            </button>
            </div>
          </div>
        ) : null}
      </section>
    </main>
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

function OnboardingError({ message }: { message: string }): JSX.Element | null {
  if (!message) return null;
  return <p className="onboarding-error" role="alert">{message}</p>;
}
