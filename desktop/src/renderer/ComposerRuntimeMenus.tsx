import { hostSupports } from "./HostCapabilities";
import {
  Bug,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Cpu,
  Eye,
  FileText,
  FlaskConical,
  FoldVertical,
  Folder,
  FolderOpen,
  FolderPlus,
  FolderX,
  Gauge,
  GitCommitHorizontal,
  GitCompare,
  GitPullRequest,
  Globe,
  Hammer,
  Lock,
  MessageSquare,
  MessageSquarePlus,
  Paperclip,
  PieChart,
  Plus,
  Puzzle,
  RotateCcw,
  ScrollText,
  Search,
  Settings,
  Shield,
  ShieldCheck,
  Terminal,
  TriangleAlert,
  Zap,
  type IconComponent
} from "./WuuIcons";
import { type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type RefObject, useEffect, useRef, useState } from "react";
import type {
  CodexModelSummary,
  DesktopProject,
  EngineInfo,
  EngineModelInfo,
  EnginePermissionModeInfo,
  InitializeResult,
  PermissionSummary,
  ProviderModelSummary,
  ProviderSummary,
  RuntimeContext
} from "../shared/protocol";
import {
  FloatingMenuPortal,
  composerMenuWidth,
  handleFloatingMenuKeyDown,
  isInsideFloatingMenu,
  menuOpeningKey,
  moveFloatingMenuFocus,
  useFloatingMenuFocus
} from "./ComposerFloatingMenu";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import type {
  CodexModelLoadState,
  CodexRuntimeMenu,
  ComposerVariant,
  FloatingMenuPlacement,
  PermissionMode
} from "./ComposerTypes";
import { COMPOSER_COMMAND_MENU_WIDTH, COMPOSER_PROJECT_MENU_WIDTH } from "./ComposerTypes";
import { lastEffortForEngineModel } from "./DraftEngineMemory";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import { lastEffortForRuntimeModel, lastModelForProvider } from "./DraftRuntimeMemory";
import {
  effectiveModelSpeed,
  orderedEffortOptions,
  providerIsCodex,
  providerModelDisplayName,
  providerModelVariantOptions,
  shortCodexModelLabel,
  variantLabel
} from "./RuntimeHelpers";
import { translateCurrent as translate, useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { ComposerMobileAttachmentChoices } from "./ComposerCamera";
import { isTouchWebShell } from "./ComposerFocus";

type ChipTone = "neutral" | "danger";

export type EngineOption = {
  id: string;
  label: string;
};

// Available engine choices shown in the runtime picker. The built-in wuu
// engine is always present; external engines appear only when auto-detected
// (enabled and binary found). An active external engine is kept in the list
// even when it just became unavailable, so the selection stays visible.
function availableEngineOptions(
  engines: EngineInfo[] | undefined,
  activeEngine: string
): EngineOption[] {
  const options: EngineOption[] = [{ id: "wuu", label: engineLabel("wuu") }];
  for (const engine of engines ?? []) {
    if (engine.id === "wuu") continue;
    if ((!engine.enabled || !engine.binary_ok) && engine.id !== activeEngine) continue;
    options.push({ id: engine.id, label: engineLabel(engine.id, engine) });
  }
  if (activeEngine && activeEngine !== "wuu" && !options.some((option) => option.id === activeEngine)) {
    options.push({ id: activeEngine, label: engineLabel(activeEngine) });
  }
  return options;
}

function EngineOptionsMenu({
  options,
  selected,
  locked,
  running,
  lockedDescription,
  onSelect
}: {
  options: EngineOption[];
  selected: string;
  locked: boolean;
  running: boolean;
  lockedDescription?: string;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div
      className={`runtime-engine-options${locked ? " is-locked" : ""}`}
      role="group"
      aria-label={locked && lockedDescription ? lockedDescription : t("runtime.engine")}
    >
      {options.map((option) => {
        const isSelected = option.id === selected;
        return (
          <button
            className="runtime-engine-option"
            key={option.id}
            role="menuitemradio"
            type="button"
            disabled={locked || running}
            aria-checked={isSelected}
            onClick={() => {
              if (!isSelected) onSelect(option.id);
            }}
          >
            <EngineIcon engine={option.id} />
            <span className="runtime-engine-option-name">{option.label}</span>
            {locked && isSelected ? <Lock aria-hidden="true" /> : isSelected ? <Check aria-hidden="true" /> : null}
          </button>
        );
      })}
    </div>
  );
}

export type RuntimePanelView = "summary" | "engines" | "providers" | "models";
type RuntimePanelDirection = "forward" | "back";

const RUNTIME_PANEL_WIDTH = 224;
export const PERMISSION_MENU_WIDTH = 264;

export function runtimePanelWidth(): number {
  return composerMenuWidth(RUNTIME_PANEL_WIDTH);
}

// The panel morphs between pages with a height transition, so its shell
// height cannot come from its content. Each page reports how many rows it
// shows; styles/workspace.css turns that count into a height from the same
// font-scaled row metrics the rows use.
function runtimePanelStyle(rows: number, width?: number): CSSProperties {
  return {
    "--runtime-rows": String(Math.max(rows, 1)),
    ...(width ? { "--runtime-panel-width": `${width}px` } : {}),
  } as CSSProperties;
}

// Arrow keys move between the current page's rows; the effort slider keeps
// them for its own steps. Escape on a drill-in page returns to the summary
// and marks the event handled so the picker does not also close.
function handleRuntimePanelKeyDown(
  event: ReactKeyboardEvent<HTMLElement>,
  view: RuntimePanelView,
  showSummary: () => void
): void {
  if (event.key === "Escape") {
    if (view === "summary") return;
    event.preventDefault();
    showSummary();
    return;
  }
  moveFloatingMenuFocus(event);
}

function RuntimePanelHeader({ title, onBack }: { title: string; onBack: () => void }): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="runtime-panel-header">
      <button type="button" className="runtime-panel-back" aria-label={t("common.back")} onClick={onBack}>
        <ChevronLeft aria-hidden="true" />
      </button>
      <span>{title}</span>
    </div>
  );
}

// The summary reads top down: where the model comes from (engine and model
// service, when there is a choice to show), the model, then how hard it thinks.
// Speed is an independent accessory in the context header, not another choice row.
function RuntimePanelSummary({
  engine,
  engineId,
  provider,
  showEngine,
  engineLocked,
  lockedDescription,
  model,
  effortOptions,
  selectedEffort,
  effortDisabled,
  speed,
  defaultSpeed,
  speedDisabled = false,
  onSelectSpeed,
  onOpenEngines,
  onOpenProviders,
  onOpenModels,
  onSelectEffort
}: {
  engine: string;
  engineId: string;
  provider?: string;
  showEngine: boolean;
  engineLocked: boolean;
  lockedDescription?: string;
  model: string;
  effortOptions: string[];
  selectedEffort: string;
  effortDisabled: boolean;
  speed?: string;
  defaultSpeed?: string;
  speedDisabled?: boolean;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  onOpenEngines: () => void;
  // Absent when the engine offers a single model service.
  onOpenProviders?: () => void;
  onOpenModels: () => void;
  onSelectEffort: (effort: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [pendingSpeed, setPendingSpeed] = useState<string | undefined>(undefined);
  const [speedSaving, setSpeedSaving] = useState(false);
  const fastButtonRef = useRef<HTMLButtonElement>(null);
  useEffect(() => setPendingSpeed(undefined), [speed, model]);
  const requestedSpeed = pendingSpeed ?? speed;
  const displayedSpeed = effectiveModelSpeed(requestedSpeed, defaultSpeed);
  const fastModeHint = `${t(displayedSpeed === "fast" ? "runtime.fastModeOn" : displayedSpeed === "standard" ? "runtime.fastModeOff" : "runtime.fastModeDefault")} · ${t("runtime.fastModeHint")}`;
  // A saving toggle stays focusable; native disabled would blur keyboard input.
  const changeSpeed = async (next: string): Promise<void> => {
    if (!onSelectSpeed || speedSaving) return;
    setPendingSpeed(next);
    setSpeedSaving(true);
    try {
      if (await onSelectSpeed(next) === false) setPendingSpeed(undefined);
    } catch { setPendingSpeed(undefined); }
    finally { setSpeedSaving(false); }
  };
  const [previewEffort, setPreviewEffort] = useState(selectedEffort);

  useEffect(() => {
    setPreviewEffort(selectedEffort);
  }, [selectedEffort]);

  const engineName = engineLabel(engine);
  // A bound conversation cannot switch engines, so the engine is a label that
  // says why rather than a way into a list of choices it cannot take.
  const engineItem = engineLocked ? (
    <Tooltip content={lockedDescription}>
      <span className="runtime-panel-context-item" aria-label={lockedDescription ? `${engineName} · ${lockedDescription}` : engineName}>
        <EngineIcon engine={engineId} />
        <span>{engineName}</span>
        <Lock aria-hidden="true" />
      </span>
    </Tooltip>
  ) : (
    <Tooltip content={t("runtime.engineNamed", { engine: engineName })}>
      <button type="button" aria-label={t("runtime.engineNamed", { engine: engineName })} onClick={onOpenEngines}>
        <EngineIcon engine={engineId} />
        <span>{engineName}</span>
      </button>
    </Tooltip>
  );
  const providerItem = provider ? (onOpenProviders ? (
    <Tooltip content={t("runtime.providerNamed", { provider })}>
      <button type="button" aria-label={t("runtime.providerNamed", { provider })} onClick={onOpenProviders}>
        <span>{provider}</span>
        <ChevronDown aria-hidden="true" />
      </button>
    </Tooltip>
  ) : (
    <span className="runtime-panel-context-item"><span>{provider}</span></span>
  )) : null;

  return (
    <div className="runtime-panel-summary">
      {showEngine || providerItem || onSelectSpeed ? (
        <div className="runtime-panel-context">
          <div className="runtime-panel-context-source">
            {showEngine ? engineItem : null}
            {showEngine && providerItem ? <span className="runtime-panel-context-separator" aria-hidden="true">/</span> : null}
            {providerItem}
          </div>
          {onSelectSpeed ? (
            <div className="runtime-panel-speed-controls">
              <Tooltip content={fastModeHint}>
                <button
                  type="button"
                  className="runtime-panel-fast"
                  ref={fastButtonRef}
                  aria-label={t("runtime.fastMode")}
                  aria-description={fastModeHint}
                  aria-pressed={displayedSpeed ? displayedSpeed === "fast" : "mixed"}
                  disabled={speedDisabled}
                  aria-disabled={speedSaving || undefined}
                  aria-busy={speedSaving || undefined}
                  onClick={() => { void changeSpeed(displayedSpeed === "fast" ? "standard" : "fast"); }}
                ><Zap aria-hidden="true" /></button>
              </Tooltip>
              <Tooltip content={t("runtime.resetSpeed")}>
                <button
                  className="runtime-panel-speed-reset"
                  type="button"
                  aria-label={t("runtime.resetSpeed")}
                  disabled={!requestedSpeed || speedDisabled || speedSaving}
                  onClick={() => {
                    // Reset becomes unavailable after this action; keep focus on
                    // the related toggle rather than dropping it onto the page.
                    fastButtonRef.current?.focus();
                    void changeSpeed("");
                  }}
                ><RotateCcw aria-hidden="true" /></button>
              </Tooltip>
            </div>
          ) : null}
        </div>
      ) : null}
      <button type="button" className="runtime-panel-model" data-menu-autofocus onClick={onOpenModels}>
        <span className="runtime-panel-model-name">{model}</span>
        {effortOptions.length > 1 ? (
          <span key={previewEffort} className="runtime-panel-effort-value">{variantLabel(previewEffort)}</span>
        ) : null}
        <ChevronRight aria-hidden="true" />
      </button>
      {effortOptions.length > 1 ? (
        <div className="runtime-panel-effort">
          <EffortSelector
            options={effortOptions}
            selectedVariant={selectedEffort}
            disabled={effortDisabled}
            onPreviewEffort={setPreviewEffort}
            onSelectEffort={onSelectEffort}
          />
        </div>
      ) : null}
    </div>
  );
}

// The shell morphs between pages, so the summary's height comes from the rows
// it shows rather than from its content.
function runtimeSummaryStyle({ context, effort, speed }: { context: boolean; effort: boolean; speed: boolean }): CSSProperties {
  return {
    "--runtime-summary-context": context ? "1" : "0",
    "--runtime-summary-effort": effort ? "1" : "0",
    ...(speed ? { "--runtime-context-row": "var(--control-size-inline)" } : {}),
  } as CSSProperties;
}

type PermissionModeState = PermissionMode;

type PermissionModeOption = {
  mode: PermissionMode;
  // The chip and the menu name a mode with the same words on every engine.
  label: string;
  // One line on what the mode allows; for an external engine, the native mode
  // Wuu selects, in the words of that engine's own documentation.
  hint: string;
  icon: IconComponent;
  tone: ChipTone;
};

function permissionModeLabel(mode: PermissionMode): string {
  switch (mode) {
    case "read_only":
      return translate("runtime.permission.readOnly");
    case "unconfined":
      return translate("runtime.permission.unconfined");
    default:
      return translate("runtime.permission.standard");
  }
}

function permissionModeHint(mode: PermissionMode): string {
  switch (mode) {
    case "read_only":
      return translate("runtime.permission.readOnlyHint");
    case "unconfined":
      return translate("runtime.permission.unconfinedHint");
    default:
      return translate("runtime.permission.standardHint");
  }
}

// Mode names the external programs use for the setting Wuu passes them.
function nativePermissionModeNames(engine?: string): Partial<Record<PermissionMode, string>> {
  switch (engine?.trim().toLowerCase()) {
    case "codex":
      return { standard: "Workspace Write", read_only: "Read Only", unconfined: "Danger Full Access" };
    case "claude":
      return { standard: "Don't Ask", read_only: "Plan", unconfined: "Bypass Permissions" };
    case "cursor":
      return { standard: "Agent", read_only: "Plan" };
    case "devin":
      return { standard: "Ask", unconfined: "Bypass" };
    default:
      return {};
  }
}

function permissionModeIcons(mode: PermissionMode): { icon: IconComponent; tone: ChipTone } {
  switch (mode) {
    case "read_only":
      return { icon: Eye, tone: "neutral" };
    case "unconfined":
      return { icon: TriangleAlert, tone: "danger" };
    default:
      return { icon: Shield, tone: "neutral" };
  }
}

function permissionModeOptionFor(
  mode: PermissionMode,
  engine?: string,
  advertised?: readonly EnginePermissionModeInfo[],
): PermissionModeOption {
  const hint = advertised?.find((item) => item.mode === mode)?.label?.trim()
    || nativePermissionModeNames(engine)[mode]
    || permissionModeHint(mode);
  return { mode, label: permissionModeLabel(mode), hint, ...permissionModeIcons(mode) };
}

function permissionModeOptions(
  engine?: string,
  advertised?: readonly EnginePermissionModeInfo[],
): PermissionModeOption[] {
  const catalog = advertised?.filter((mode) => mode.mode === "standard" || mode.mode === "read_only" || mode.mode === "unconfined");
  const modes: PermissionMode[] = catalog && catalog.length > 0
    ? catalog.map((mode) => mode.mode)
    : engineOffersReadOnly(engine)
      ? ["standard", "read_only", "unconfined"]
      : ["standard", "unconfined"];
  return modes.map((mode) => permissionModeOptionFor(mode, engine, catalog));
}

function engineOffersReadOnly(engine?: string): boolean {
  switch (engine?.trim().toLowerCase()) {
    case undefined:
    case "":
    case "wuu":
    case "codex":
    case "claude":
      return true;
    default:
      return false;
  }
}

export function permissionModeFromSummary(permissions?: PermissionSummary): PermissionModeState {
  const mode = permissions?.mode?.trim();
  switch (mode) {
    case "read_only":
      return "read_only";
    case "unconfined":
      return "unconfined";
    case "standard":
      return "standard";
    default:
      return "standard";
  }
}

export function permissionModeOption(
  mode: PermissionModeState,
  engine?: string,
  advertised?: readonly EnginePermissionModeInfo[],
): PermissionModeOption {
  return permissionModeOptions(engine, advertised).find((option) => option.mode === mode)
    ?? permissionModeOptionFor(mode, engine, advertised);
}

export type HandoffRuntimePicker = {
  provider: string;
  model: string;
  variant: string;
  filterQuery: string;
  forcedView: RuntimePanelView;
  onSelectProvider: (providerId: string) => void;
  onSelectModel: (provider: string, model: string, variant?: string) => void;
  onSelectEffort: (variant: string) => void;
};

export function RuntimePicker({
  initialized,
  state,
  openMenu,
  anchorRef,
  running,
  engines,
  activeEngine,
  engineLocked,
  engineModel,
  engineEffort,
  engineSpeed,
  onSelectSpeed,
  onSelectEngine,
  onSelectEngineModel,
  onSelectEngineEffort,
  onToggleMenu,
  onSelectModel,
  onSelectEffort,
  handoff
}: {
  initialized: InitializeResult;
  state: CodexModelLoadState;
  openMenu: CodexRuntimeMenu;
  anchorRef: RefObject<HTMLDivElement | null>;
  running: boolean;
  engines?: EngineInfo[];
  activeEngine?: string;
  engineLocked?: boolean;
  engineModel?: string;
  engineEffort?: string;
  engineSpeed?: string;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  onSelectEngine?: (id: string) => void;
  onSelectEngineModel?: (model: string, effort: string) => void;
  onSelectEngineEffort?: (effort: string) => void;
  onToggleMenu: (menu: Exclude<CodexRuntimeMenu, null>) => void;
  onSelectModel: (provider: string, model: string, variant?: string) => void | Promise<boolean>;
  onSelectEffort: (variant: string) => void | Promise<boolean>;
  handoff?: HandoffRuntimePicker;
}): JSX.Element {
  const { t } = useI18n();
  const targetProvider = handoff?.provider ?? initialized.provider;
  const targetModel = handoff?.model ?? initialized.model;
  const targetVariant = handoff?.variant ?? (initialized.variant || initialized.effort || "");
  const currentProvider = initialized.providers?.find((provider) => provider.name === targetProvider);
  const codexProvider = providerIsCodex(initialized, targetProvider);
  const currentCodexModel = codexProvider ? state.models.find((model) => model.slug === targetModel) : undefined;
  const currentProviderModel = currentProvider?.models?.find((model) => model.id === targetModel);
  // One level choice is stored in either column depending on how the session
  // was created: thread/start and the runtime update path mirror the level
  // into both, while some persisted sessions carry it in the effort column
  // with an empty variant. An empty variant means "not set", so display
  // whichever column holds the level instead of letting "" shadow a real
  // effort.
  const currentVariant = targetVariant;
  // Empty sessions still dock the composer at the bottom of the pane. Opening
  // the model card below that trigger leaves it in the greeting once a phone
  // keyboard has lifted the input, so keep it attached above the selector.
  const placement: FloatingMenuPlacement = "above";
  const externalEngine = handoff ? "" : activeEngine && activeEngine !== "wuu" ? activeEngine : "";
  const engineOptions = handoff
    ? [{ id: "wuu", label: engineLabel("wuu") }]
    : availableEngineOptions(engines, externalEngine);
  const selectedEngine = handoff ? "wuu" : externalEngine || "wuu";
  const externalEngineInfo = engines?.find((engine) => engine.id === externalEngine);
  const externalModelInfo = externalEngineInfo?.models?.find((model) => engineModel ? model.id === engineModel : model.is_default);
  const triggerEngineName = engineLabel(selectedEngine, externalEngineInfo);
  const triggerLabel = handoff
    ? handoff.model
      ? runtimeTriggerLabel(handoff.model, codexProvider, currentProviderModel, currentCodexModel)
      : handoff.provider
        ? `${handoff.provider} · ${t("runtime.selectModel")}`
        : t("runtime.selectModel")
    : externalEngine
      ? externalModelInfo?.display_name || engineModel || t("runtime.engineDefaultModel")
      : runtimeTriggerLabel(targetModel, codexProvider, currentProviderModel, currentCodexModel);
  // A level is only worth naming when the model offers a choice; a lone
  // "Default" next to a model without levels reads as a setting it lacks.
  const effortLevels = externalEngine
    ? orderedEffortOptions(externalModelInfo?.supported_efforts ?? [])
    : providerModelVariantOptions(currentProvider, targetModel, currentVariant);
  const effortLabelText = effortLevels.length > 1 && (!handoff || handoff.model)
    ? variantLabel(externalEngine ? engineEffort ?? "" : currentVariant)
    : "";
  const triggerAccessibleName = [triggerEngineName, triggerLabel, effortLabelText].filter(Boolean).join(" · ");
  const open = openMenu === "model";
  const triggerRef = useRef<HTMLButtonElement>(null);
  // The label cross-fades when a committed choice changes it, confirming the
  // selection behind the open panel. The first render stays still.
  const labelKey = `${selectedEngine}\n${triggerLabel}\n${effortLabelText}`;
  const initialLabelKey = useRef(labelKey);
  const panelWidth = open ? runtimePanelWidth() : RUNTIME_PANEL_WIDTH;

  useEffect(() => {
    if (!open) return undefined;
    function handleKeyDown(event: KeyboardEvent): void {
      // Tab leaves the panel from its trigger, not from the portal at the
      // end of the document.
      if (event.key === "Tab" && event.target instanceof Node && isInsideFloatingMenu(event.target, "codex-runtime")) {
        triggerRef.current?.focus();
        onToggleMenu("model");
        return;
      }
      // Pages handle Escape first when it only steps back to the summary.
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      onToggleMenu("model");
      triggerRef.current?.focus();
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, onToggleMenu]);

  return (
    <div className="codex-runtime-anchor" ref={anchorRef}>
      <Tooltip content={running ? t("runtime.modelSwitchWhileRunning") : undefined}>
        <button
          ref={triggerRef}
          className="codex-runtime-trigger"
          type="button"
          disabled={running}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={triggerAccessibleName}
          onPointerDown={(event) => { if (isTouchWebShell()) event.preventDefault(); }}
          onClick={() => onToggleMenu("model")}
          onKeyDown={(event) => {
            if (!open && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
              event.preventDefault();
              onToggleMenu("model");
            }
          }}
        >
          <EngineIcon engine={selectedEngine} />
          <span
            key={labelKey}
            className={`codex-runtime-label${labelKey === initialLabelKey.current ? "" : " is-changed"}`}
          >
            <span className="codex-runtime-model">{triggerLabel}</span>
            {effortLabelText ? <span className="codex-runtime-effort">{effortLabelText}</span> : null}
          </span>
          <ChevronDown className="icon" />
        </button>
      </Tooltip>
      {open ? (
        <FloatingMenuPortal
          anchorRef={anchorRef}
          owner="codex-runtime"
          placement={placement}
          align="right"
          width={panelWidth}
          flip
          mobileSheet={{ label: t("runtime.selectModel"), onClose: () => onToggleMenu("model") }}
        >
          {externalEngine ? (
            <EngineRuntimeMenu
              engine={externalEngineInfo}
              selectedModel={engineModel ?? ""}
              selectedEffort={engineEffort ?? ""}
              selectedSpeed={engineSpeed ?? ""}
              onSelectSpeed={onSelectSpeed}
              disabled={running || Boolean(engineLocked)}
              onSelectModel={(model, effort) => onSelectEngineModel?.(model, effort)}
              onSelectEffort={(effort) => onSelectEngineEffort?.(effort)}
              engineOptions={engineOptions}
              selectedEngine={selectedEngine}
              engineLocked={Boolean(engineLocked)}
              running={running}
              lockedDescription={engineLocked ? t("runtime.engineLockedDescription") : undefined}
              onSelectEngine={(id) => onSelectEngine?.(id)}
              width={panelWidth}
            />
          ) : (
            <RuntimeModelMenu
              initialized={initialized}
              state={state}
              selectedProvider={targetProvider}
              selectedModel={targetModel}
              selectedVariant={targetVariant}
              onSelectModel={handoff?.onSelectModel ?? onSelectModel}
              onSelectEffort={handoff?.onSelectEffort ?? onSelectEffort}
              onSelectSpeed={handoff ? undefined : onSelectSpeed}
              engineOptions={engineOptions}
              selectedEngine={selectedEngine}
              engineLocked={handoff ? true : Boolean(engineLocked)}
              running={running}
              lockedDescription={engineLocked ? t("runtime.engineLockedDescription") : undefined}
              onSelectEngine={(id) => {
                if (!handoff) onSelectEngine?.(id);
              }}
              filterQuery={handoff?.filterQuery ?? ""}
              forcedView={handoff?.forcedView}
              onSelectProvider={handoff?.onSelectProvider}
              width={panelWidth}
              autoFocus
            />
          )}
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function engineModelDefaultEffort(model: EngineModelInfo): string {
  const supported = model.supported_efforts ?? [];
  if (model.default_effort && supported.includes(model.default_effort)) {
    return model.default_effort;
  }
  if (supported.includes("medium")) return "medium";
  return supported[0] ?? "";
}

function EngineRuntimeMenu({
  engine,
  selectedModel,
  selectedEffort,
  selectedSpeed,
  onSelectSpeed,
  disabled,
  onSelectModel,
  onSelectEffort,
  engineOptions,
  selectedEngine,
  engineLocked,
  running,
  lockedDescription,
  onSelectEngine,
  width
}: {
  engine?: EngineInfo;
  selectedModel: string;
  selectedEffort: string;
  selectedSpeed: string;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  disabled: boolean;
  onSelectModel: (model: string, effort: string) => void;
  onSelectEffort: (effort: string) => void;
  engineOptions: EngineOption[];
  selectedEngine: string;
  engineLocked: boolean;
  running: boolean;
  lockedDescription?: string;
  onSelectEngine: (id: string) => void;
  width?: number;
}): JSX.Element {
  const { t } = useI18n();
  const panelRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<RuntimePanelView>("summary");
  const [direction, setDirection] = useState<RuntimePanelDirection>("forward");
  const [query, setQuery] = useState("");
  const [optimistic, setOptimistic] = useState<{ model: string; effort: string } | null>(null);
  useFloatingMenuFocus(panelRef, `${engine?.id ?? "engine"}:${view}`);
  useEffect(() => {
    setOptimistic(null);
  }, [selectedModel, selectedEffort]);
  useEffect(() => {
    setQuery("");
    setDirection("back");
    setView("summary");
  }, [engine?.id]);

  const openView = (nextView: RuntimePanelView): void => {
    setDirection("forward");
    setView(nextView);
  };
  const showSummary = (): void => {
    setDirection("back");
    setView("summary");
  };

  const models = engine?.models ?? [];
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredModels = normalizedQuery
    ? models.filter((model) =>
        (model.display_name || model.id).toLocaleLowerCase().includes(normalizedQuery)
        || model.id.toLocaleLowerCase().includes(normalizedQuery))
    : models;
  const effectiveModelID = optimistic?.model ?? selectedModel;
  const effectiveModel = models.find((model) => effectiveModelID ? model.id === effectiveModelID : model.is_default);
  const effortOptions = orderedEffortOptions(effectiveModel?.supported_efforts ?? []);
  const effectiveEffort = optimistic?.effort
    ?? (effortOptions.includes(selectedEffort)
      ? selectedEffort
      : effectiveModel
        ? engineModelDefaultEffort(effectiveModel)
        : selectedEffort);
  const selectModel = (model: EngineModelInfo): void => {
    const effort = lastEffortForEngineModel(engine?.id ?? "", model.id)
      || engineModelDefaultEffort(model);
    setOptimistic({ model: model.id, effort });
    onSelectModel(model.id, effort);
    showSummary();
  };

  return (
    <div
      ref={panelRef}
      className={`codex-runtime-menu codex-model-menu runtime-panel is-${view}`}
      role="menu"
      style={{
        ...runtimePanelStyle(view === "models" ? filteredModels.length : engineOptions.length, width),
        ...runtimeSummaryStyle({
          context: true,
          effort: effortOptions.length > 1,
          speed: Boolean(effectiveModel?.fast_mode && onSelectSpeed),
        }),
      }}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, view, showSummary)}
    >
      <div key={`${engine?.id ?? "engine"}:${view}`} className={`runtime-panel-page is-${direction}`}>
        {view === "summary" ? (
          <RuntimePanelSummary
            engine={engineLabel(selectedEngine, engine)}
            engineId={selectedEngine}
            showEngine
            engineLocked={engineLocked}
            lockedDescription={lockedDescription}
            model={effectiveModel?.display_name || effectiveModelID || t("runtime.engineDefaultModel")}
            effortOptions={effortOptions}
            selectedEffort={effectiveEffort}
            effortDisabled={disabled}
            speed={selectedSpeed}
            defaultSpeed={effectiveModel?.default_speed}
            speedDisabled={running}
            onSelectSpeed={effectiveModel?.fast_mode ? onSelectSpeed : undefined}
            onOpenEngines={() => openView("engines")}
            onOpenModels={() => openView("models")}
            onSelectEffort={(effort) => {
              setOptimistic((current) => ({ model: current?.model ?? selectedModel, effort }));
              onSelectEffort(effort);
            }}
          />
        ) : null}
        {view === "engines" ? (
          <>
            <RuntimePanelHeader title={t("runtime.engine")} onBack={showSummary} />
            <div className="runtime-panel-list">
              <EngineOptionsMenu
                options={engineOptions}
                selected={selectedEngine}
                locked={engineLocked}
                running={running}
                lockedDescription={lockedDescription}
                onSelect={(id) => {
                  onSelectEngine(id);
                  showSummary();
                }}
              />
            </div>
          </>
        ) : null}
        {view === "models" ? (
          <>
            {models.length > 0 ? (
              <label className="menu-search select-menu-search">
                <Search className="select-menu-search-icon icon-lg" />
                <input
                  type="search"
                  value={query}
                  placeholder={t("runtime.searchModels")}
                  aria-label={t("runtime.searchModels")}
                  onChange={(event) => setQuery(event.currentTarget.value)}
                  onKeyDown={(event) => {
                    const first = filteredModels[0];
                    if (event.key === "Enter" && first && !disabled) {
                      event.preventDefault();
                      selectModel(first);
                    }
                  }}
                />
              </label>
            ) : null}
            <div className="codex-model-groups">
              {engine?.models_error ? (
                <div className="composer-menu-note warning">
                  <strong>{t("runtime.modelsLoadFailed")}</strong>
                  <span>{engine.models_error}</span>
                </div>
              ) : null}
              {models.length === 0 ? (
                <div className="composer-menu-empty">
                  {engine?.models_error
                    ? t("runtime.noModels")
                    : t("runtime.engineDefaultModelHint", { engine: engineLabel(selectedEngine, engine) })}
                </div>
              ) : null}
              {models.length > 0 && filteredModels.length === 0 ? (
                <div className="composer-menu-empty">{t("runtime.noMatchingModels")}</div>
              ) : null}
              {filteredModels.length > 0 ? (
                <div className="codex-model-group">
                  {filteredModels.map((model) => {
                    const selected = model.id === effectiveModelID;
                    return (
                      <button
                        className="codex-model-item"
                        role="menuitemradio"
                        type="button"
                        key={model.id}
                        disabled={disabled}
                        aria-checked={selected}
                        onClick={() => selectModel(model)}
                      >
                        <span className="codex-model-item-name">{model.display_name || model.id}</span>
                        {selected ? <Check className="icon-lg" /> : null}
                      </button>
                    );
                  })}
                </div>
              ) : null}
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}

export function RuntimeModelMenu({
  initialized,
  state,
  selectedProvider,
  selectedModel,
  selectedVariant,
  onSelectModel,
  onSelectEffort,
  onSelectSpeed,
  engineOptions,
  selectedEngine,
  engineLocked,
  running,
  lockedDescription,
  onSelectEngine,
  filterQuery = "",
  forcedView,
  onSelectProvider,
  width,
  autoFocus = false,
}: {
  initialized: InitializeResult;
  state: CodexModelLoadState;
  selectedProvider: string;
  selectedModel: string;
  selectedVariant: string;
  onSelectModel: (provider: string, model: string, variant?: string) => void | Promise<boolean>;
  onSelectEffort: (variant: string) => void | Promise<boolean>;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  engineOptions: EngineOption[];
  selectedEngine: string;
  engineLocked: boolean;
  running: boolean;
  lockedDescription?: string;
  onSelectEngine: (id: string) => void;
  filterQuery?: string;
  forcedView?: RuntimePanelView;
  onSelectProvider?: (providerId: string) => void;
  width?: number;
  // Only a panel the user opened from its trigger takes focus; the handoff
  // and onboarding panels sit beside an input that keeps it.
  autoFocus?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const panelRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<RuntimePanelView>("summary");
  const [direction, setDirection] = useState<RuntimePanelDirection>("forward");
  const [query, setQuery] = useState("");
  // The panel stays open while a selection commits through the app-server
  // stream, so the highlighted row and the effort pills follow the click
  // immediately instead of waiting for the round-trip. Once the stream
  // confirms, the external props become the source of truth again and the
  // optimistic state is dropped.
  const [optimistic, setOptimistic] = useState<{ provider: string; model: string; variant: string } | null>(null);
  useEffect(() => {
    setOptimistic(null);
  }, [selectedProvider, selectedModel, selectedVariant]);
  useEffect(() => {
    if (!forcedView) {
      return;
    }
    setDirection(forcedView === "summary" ? "back" : "forward");
    setView(forcedView);
  }, [forcedView]);

  useFloatingMenuFocus(panelRef, view, autoFocus);

  const openView = (nextView: RuntimePanelView): void => {
    setDirection("forward");
    setView(nextView);
  };
  const showSummary = (): void => {
    setDirection("back");
    setView("summary");
  };

  const providers = initialized.providers ?? [];
  const configuredModels = providers
    .map((provider) => {
      const model = configuredRuntimeModelForProvider(provider, state);
      return model ? { provider, model } : undefined;
    })
    .filter((item): item is RuntimeModelOption => Boolean(item));
  const additionalModels = providers.flatMap((provider) =>
    runtimeModelsForProvider(provider, state)
      .filter((model) => model.id !== provider.model)
      .map((model) => ({ provider, model }))
  );

  // Group by provider with the configured model first, preserving provider
  // order. The group header is the provider name — the classification users
  // actually think in ("Claude is Anthropic's"), unlike a flat configured/
  // additional split.
  const groups: RuntimeModelGroup[] = [];
  const groupByProvider = new Map<string, RuntimeModelGroup>();
  for (const item of configuredModels) {
    const group = { provider: item.provider, models: [item.model] };
    groups.push(group);
    groupByProvider.set(item.provider.name, group);
  }
  for (const item of additionalModels) {
    const group = groupByProvider.get(item.provider.name);
    if (group) {
      group.models.push(item.model);
    } else {
      const created = { provider: item.provider, models: [item.model] };
      groups.push(created);
      groupByProvider.set(item.provider.name, created);
    }
  }

  const effectiveProviderName = optimistic?.provider ?? selectedProvider;
  const effectiveModelID = optimistic?.model ?? selectedModel;
  const effectiveVariant = optimistic?.variant ?? selectedVariant;
  const effectiveProvider = providers.find((provider) => provider.name === effectiveProviderName);
  const scopedGroups = groups.filter((group) => group.provider.name === effectiveProviderName);

  const activeFilter = (query || filterQuery).trim().toLocaleLowerCase();
  const visibleProviderGroups = activeFilter
    ? groups.filter((group) =>
        group.provider.name.toLocaleLowerCase().includes(activeFilter)
        || group.models.some((model) =>
          providerModelDisplayName(model).toLocaleLowerCase().includes(activeFilter)
          || model.id.toLocaleLowerCase().includes(activeFilter)))
    : groups;
  const filteredGroups = activeFilter
    ? scopedGroups
        .map((group) => ({
          ...group,
          models: group.models.filter(
            (model) =>
              providerModelDisplayName(model).toLocaleLowerCase().includes(activeFilter) ||
              model.id.toLocaleLowerCase().includes(activeFilter) ||
              group.provider.name.toLocaleLowerCase().includes(activeFilter)
          )
        }))
        .filter((group) => group.models.length > 0)
    : scopedGroups;

  // The engine is worth naming only when there is another one to pick.
  const showEngine = engineOptions.length > 1 || selectedEngine !== "wuu";
  const effectiveCodex = providerIsCodex(initialized, effectiveProviderName);
  const effortOptions = providerModelVariantOptions(effectiveProvider, effectiveModelID, effectiveVariant);
  const effectiveModel = scopedGroups
    .flatMap((group) => group.models)
    .find((model) => model.id === effectiveModelID);
  const visibleModels = filteredGroups.flatMap((group) => group.models.map((model) => ({ provider: group.provider, model })));
  const pageRows = view === "providers"
    ? visibleProviderGroups.length
    : view === "models"
      ? visibleModels.length
      : engineOptions.length;

  const selectModel = (provider: string, model: string, variant?: string): void => {
    setOptimistic({ provider, model, variant: variant ?? "" });
    void Promise.resolve(onSelectModel(provider, model, variant)).then((committed) => {
      if (committed === false) setOptimistic(null);
    });
  };

  return (
    <div
      ref={panelRef}
      className={`codex-runtime-menu codex-model-menu runtime-panel is-${view}`}
      role="menu"
      style={{
        ...runtimePanelStyle(pageRows, width),
        ...runtimeSummaryStyle({
          context: showEngine || Boolean(effectiveProviderName) || Boolean(effectiveModel?.fast_mode && onSelectSpeed),
          effort: effortOptions.length > 1,
          speed: Boolean(effectiveModel?.fast_mode && onSelectSpeed),
        }),
      }}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, view, showSummary)}
    >
      <div key={view} className={`runtime-panel-page is-${direction}`}>
        {view === "summary" ? (
          <RuntimePanelSummary
            engine={selectedEngine}
            engineId={selectedEngine}
            provider={effectiveProviderName}
            showEngine={showEngine}
            engineLocked={engineLocked}
            lockedDescription={lockedDescription}
            model={effectiveModel ? providerModelDisplayName(effectiveModel) : effectiveModelID || t("runtime.selectModel")}
            effortOptions={effortOptions}
            selectedEffort={effectiveVariant}
            effortDisabled={false}
            speed={initialized.speed}
            defaultSpeed={effectiveModel?.default_speed}
            speedDisabled={running}
            onSelectSpeed={effectiveModel?.fast_mode ? onSelectSpeed : undefined}
            onOpenEngines={() => openView("engines")}
            onOpenProviders={providers.length > 1 ? () => openView("providers") : undefined}
            onOpenModels={() => openView("models")}
            onSelectEffort={(variant) => {
              setOptimistic((current) =>
                current ? { ...current, variant } : { provider: selectedProvider, model: selectedModel, variant }
              );
              void Promise.resolve(onSelectEffort(variant)).then((committed) => {
                if (committed === false) setOptimistic(null);
              });
            }}
          />
        ) : null}
        {view === "engines" ? (
          <>
            <RuntimePanelHeader title={t("runtime.engine")} onBack={showSummary} />
            <div className="runtime-panel-list">
              <EngineOptionsMenu
                options={engineOptions}
                selected={selectedEngine}
                locked={engineLocked}
                running={running}
                lockedDescription={lockedDescription}
                onSelect={(id) => {
                  onSelectEngine(id);
                  showSummary();
                }}
              />
            </div>
          </>
        ) : null}
        {view === "providers" ? (
          <div className="runtime-panel-list runtime-provider-options" role="group" aria-label={t("runtime.provider")}>
            {visibleProviderGroups.map((group) => {
              const selected = group.provider.name === effectiveProviderName;
              return (
                <button
                  type="button"
                  className="runtime-provider-option"
                  role="menuitemradio"
                  aria-checked={selected}
                  key={group.provider.name}
                  onClick={() => {
                    if (onSelectProvider) {
                      onSelectProvider(group.provider.name);
                      openView("models");
                      return;
                    }
                    // Reuse the model this provider was last used with so a
                    // provider switch does not silently drop back to the catalog
                    // default; a model that disappeared falls back to it.
                    const remembered = lastModelForProvider(group.provider.name);
                    const model =
                      group.models.find((item) => item.id === remembered) ?? group.models[0];
                    if (model && !selected) {
                      selectModel(group.provider.name, model.id, rememberedVariantForRuntimeModel(group.provider, model));
                    }
                    showSummary();
                  }}
                >
                  <span>{group.provider.name}</span>
                  {selected ? <Check aria-hidden="true" /> : null}
                </button>
              );
            })}
          </div>
        ) : null}
        {view === "models" ? (
          <>
            <label className="menu-search select-menu-search">
              <Search className="select-menu-search-icon icon-lg" />
              <input
                type="search"
                value={query}
                placeholder={t("runtime.searchModels")}
                aria-label={t("runtime.searchModels")}
                onChange={(event) => setQuery(event.currentTarget.value)}
                onKeyDown={(event) => {
                  const first = visibleModels[0];
                  if (event.key !== "Enter" || !first) return;
                  event.preventDefault();
                  const selected = first.provider.name === effectiveProviderName && first.model.id === effectiveModelID;
                  selectModel(
                    first.provider.name,
                    first.model.id,
                    selected ? effectiveVariant : rememberedVariantForRuntimeModel(first.provider, first.model),
                  );
                  showSummary();
                }}
              />
            </label>
            <div className="codex-model-groups">
              {effectiveCodex && state.loading ? <div className="composer-menu-empty">{t("runtime.loadingCodexModels")}</div> : null}
              {effectiveCodex && state.error ? (
                <div className="composer-menu-note warning">
                  <strong>{t("runtime.codexLoginUnavailable")}</strong>
                  <span>{state.error}</span>
                </div>
              ) : null}
              {!state.loading && providers.length === 0 ? <div className="composer-menu-empty">{t("runtime.noModels")}</div> : null}
              {filteredGroups.length === 0 ? <div className="composer-menu-empty">{t("runtime.noMatchingModels")}</div> : null}
              {filteredGroups.map((group) => (
                <div className="codex-model-group" key={group.provider.name}>
                  {group.models.map((model) => (
                    <RuntimeModelMenuItem
                      key={`${group.provider.name}/${model.id}`}
                      provider={group.provider}
                      model={model}
                      selected={group.provider.name === effectiveProviderName && model.id === effectiveModelID}
                      selectedVariant={effectiveVariant}
                      onSelectModel={(provider, model, variant) => {
                        selectModel(provider, model, variant);
                        showSummary();
                      }}
                    />
                  ))}
                </div>
              ))}
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}

// A discrete slider over the levels the model offers, weakest on the left.
// Dragging previews the level in the heading above; the choice commits once on
// release (or on a keyboard step) instead of once per intermediate position,
// so tuning never spams the runtime stream.
function EffortSelector({
  options,
  selectedVariant,
  disabled = false,
  onPreviewEffort,
  onSelectEffort
}: {
  options: string[];
  selectedVariant: string;
  disabled?: boolean;
  onPreviewEffort?: (variant: string) => void;
  onSelectEffort: (variant: string) => void;
}): JSX.Element {
  const orderedOptions = orderedEffortOptions(options);
  const matchedIndex = orderedOptions.indexOf(selectedVariant);
  const selectedIndex = matchedIndex >= 0 ? matchedIndex : orderedOptions.length - 1;
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const pendingIndex = useRef<number | null>(null);
  const activePointer = useRef<number | null>(null);
  const sliderRef = useRef<HTMLDivElement>(null);
  const displayIndex = previewIndex ?? selectedIndex;
  const lastIndex = Math.max(orderedOptions.length - 1, 1);

  useEffect(() => {
    setPreviewIndex(null);
    pendingIndex.current = null;
  }, [selectedIndex]);

  const previewTo = (index: number): void => {
    pendingIndex.current = index;
    setPreviewIndex(index);
    onPreviewEffort?.(orderedOptions[index] ?? selectedVariant);
  };
  const cancel = (): void => {
    activePointer.current = null;
    pendingIndex.current = null;
    setPreviewIndex(null);
    onPreviewEffort?.(orderedOptions[selectedIndex] ?? selectedVariant);
  };
  const commit = (): void => {
    activePointer.current = null;
    const index = pendingIndex.current;
    pendingIndex.current = null;
    if (disabled || index === null) return;
    const next = orderedOptions[index];
    if (next !== undefined && index !== selectedIndex) onSelectEffort(next);
  };

  // Stops sit at the two ends of the capsule and evenly between them; a pointer
  // snaps to the nearest one. The knob's outside edge reaches each endpoint.
  const previewPointer = (clientX: number): void => {
    const slider = sliderRef.current;
    const rect = slider?.getBoundingClientRect();
    if (!slider || !rect || rect.width <= 0) return;
    const style = getComputedStyle(slider);
    const knob = Number.parseFloat(style.getPropertyValue("--effort-knob")) || 0;
    const edge = Number.parseFloat(style.getPropertyValue("--effort-edge")) || 0;
    const ratio = (clientX - rect.left - edge - knob / 2) / Math.max(rect.width - knob - edge * 2, 1);
    if (!Number.isFinite(ratio)) return;
    previewTo(Math.min(orderedOptions.length - 1, Math.max(0, Math.round(ratio * lastIndex))));
  };
  const stop = (index: number): string => String(index / lastIndex);

  return (
    <div
      ref={sliderRef}
      className={`codex-effort-slider${disabled ? " is-disabled" : ""}`}
      style={{ "--effort-ratio": stop(displayIndex) } as CSSProperties}
    >
      <span className="codex-effort-track" aria-hidden="true">
        <span className="codex-effort-fill" />
      </span>
      <span className="codex-effort-stops" aria-hidden="true">
        {orderedOptions.map((variant, index) => (
          <span
            key={variant || `default-${index}`}
            className={index <= displayIndex ? "is-filled" : ""}
            style={{ "--effort-stop": stop(index) } as CSSProperties}
          />
        ))}
      </span>
      <span className="codex-effort-knob" aria-hidden="true" />
      <input
        type="range"
        min={0}
        max={orderedOptions.length - 1}
        step={1}
        value={displayIndex}
        disabled={disabled}
        aria-label={translate("runtime.reasoningEffort")}
        aria-valuetext={variantLabel(orderedOptions[displayIndex] ?? selectedVariant)}
        onPointerDown={(event) => {
          if (disabled || event.button !== 0) return;
          event.preventDefault();
          event.currentTarget.focus();
          event.currentTarget.setPointerCapture(event.pointerId);
          activePointer.current = event.pointerId;
          previewPointer(event.clientX);
        }}
        onPointerMove={(event) => {
          if (activePointer.current === event.pointerId) previewPointer(event.clientX);
        }}
        onPointerUp={commit}
        onPointerCancel={cancel}
        onLostPointerCapture={() => {
          if (activePointer.current !== null) cancel();
        }}
        onChange={(event) => previewTo(Number(event.currentTarget.value))}
        onKeyUp={(event) => {
          if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown"].includes(event.key)) commit();
        }}
        onBlur={cancel}
      />
    </div>
  );
}

type RuntimeModelOption = {
  provider: ProviderSummary;
  model: ProviderModelSummary;
};

type RuntimeModelGroup = {
  provider: ProviderSummary;
  models: ProviderModelSummary[];
};

function RuntimeModelMenuItem({
  provider,
  model,
  selected,
  selectedVariant,
  onSelectModel
}: {
  provider: ProviderSummary;
  model: ProviderModelSummary;
  selected: boolean;
  selectedVariant: string;
  onSelectModel: (provider: string, model: string, variant?: string) => void;
}): JSX.Element {
  const nextVariant = selected ? selectedVariant : rememberedVariantForRuntimeModel(provider, model);
  return (
    <button
      className="codex-model-item"
      role="menuitemradio"
      type="button"
      aria-checked={selected}
      onClick={() => onSelectModel(provider.name, model.id, nextVariant)}
    >
      <span className="codex-model-item-name">{providerModelDisplayName(model)}</span>
      {selected ? <Check className="icon-lg" /> : null}
    </button>
  );
}

function rememberedVariantForRuntimeModel(provider: ProviderSummary, model: ProviderModelSummary): string {
  return lastEffortForRuntimeModel(provider.name, model.id) ?? defaultVariantForRuntimeModel(provider, model);
}

// Codex subscription slugs drop their shared "gpt-" prefix; every other model
// keeps its own name, so "GPT-5.2" is not cut to "5.2".
function runtimeTriggerLabel(
  modelId: string,
  codexProvider: boolean,
  providerModel?: ProviderModelSummary,
  codexModel?: CodexModelSummary,
): string {
  if (codexModel) {
    return shortCodexModelLabel(codexModel.slug);
  }
  return codexProvider ? shortCodexModelLabel(modelId) : providerModel?.display_name || modelId;
}

function configuredRuntimeModelForProvider(
  provider: ProviderSummary,
  state: CodexModelLoadState
): ProviderModelSummary | undefined {
  if (!provider.model) {
    return undefined;
  }
  return (
    runtimeModelsForProvider(provider, state).find((model) => model.id === provider.model) ??
    provider.models?.find((model) => model.id === provider.model) ?? { id: provider.model, source: "selected" }
  );
}

function runtimeModelsForProvider(provider: ProviderSummary, _state: CodexModelLoadState): ProviderModelSummary[] {
  if (provider.models?.length) {
    return provider.models;
  }
  return [{ id: provider.model, source: "selected" }];
}

function defaultVariantForRuntimeModel(
  provider: ProviderSummary,
  model: ProviderModelSummary
): string {
  const modelVariants = (model.variants ?? []).map((item) => item.id).filter(Boolean);
  const supported = modelVariants.length > 0 ? modelVariants : model.supported_efforts ?? [];
  if (supported.length === 0) {
    return "";
  }
  if (model.default_variant && supported.includes(model.default_variant)) {
    return model.default_variant;
  }
  if (model.default_effort && supported.includes(model.default_effort)) {
    return model.default_effort;
  }
  const providerModel = provider.models?.find((item) => item.id === model.id);
  if (providerModel?.default_variant && supported.includes(providerModel.default_variant)) {
    return providerModel.default_variant;
  }
  if (providerModel?.default_effort && supported.includes(providerModel.default_effort)) {
    return providerModel.default_effort;
  }
  return "";
}

// Composer-bar "+" menu: the single entry point for everything the composer
// can attach or invoke — attachments plus the full slash-command list
// (built-in actions, prompts, and skills; future plugin actions join here).
// Clicking a command behaves exactly like picking it in the "/" panel.
// Open state is local, so the host's floating-menu registry needs no wiring.
export function ComposerPlusButton({
  disabled,
  commands,
  menuAnchorRef,
  onAddAttachment,
  mobileAttachments,
  onSelectCommand
}: {
  variant: ComposerVariant;
  disabled: boolean;
  commands: ComposerSlashCommand[];
  menuAnchorRef: RefObject<HTMLElement | null>;
  onAddAttachment: () => void;
  // Mobile web presents three distinct attachment sources (camera, gallery,
  // files) instead of the single desktop file picker. Omit on desktop.
  mobileAttachments?: {
    onTakePhoto: () => void;
    onPickPhotos: () => void;
    onPickFiles: () => void;
  };
  onSelectCommand: (command: ComposerSlashCommand) => void;
}): JSX.Element {
  const { t } = useI18n();
  const triggerRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [openedFromEnd, setOpenedFromEnd] = useState(false);
  useFloatingMenuFocus(menuRef, "", open, openedFromEnd);
  const menuWidth = open ? composerMenuWidth(COMPOSER_COMMAND_MENU_WIDTH) : COMPOSER_COMMAND_MENU_WIDTH;
  const builtInCommands = commands.filter((command) => command.kind !== "skill");
  const skillCommands = commands.filter((command) => command.kind === "skill");
  const selectCommand = (command: ComposerSlashCommand): void => {
    setOpen(false);
    onSelectCommand(command);
  };

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    function handlePointerDown(event: PointerEvent): void {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
      }
      if (triggerRef.current?.contains(target)) {
        return;
      }
      if (isInsideFloatingMenu(target, "composer-plus")) {
        return;
      }
      setOpen(false);
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  return (
    <div className="composer-plus-menu-anchor" ref={triggerRef}>
      <button
        ref={buttonRef}
        className="composer-tool-button composer-plus-button"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("composer.plusMenu")}
        title={t("composer.plusMenu")}
        disabled={disabled}
        onPointerDown={(event) => { if (isTouchWebShell()) event.preventDefault(); }}
        onClick={() => {
          setOpenedFromEnd(false);
          setOpen((current) => !current);
        }}
        onKeyDown={(event) => {
          const side = open ? undefined : menuOpeningKey(event);
          if (!side) return;
          setOpenedFromEnd(side === "end");
          setOpen(true);
        }}
      >
        <Plus aria-hidden="true" />
      </button>
      {open ? (
        <FloatingMenuPortal
          anchorRef={isTouchWebShell() ? triggerRef : menuAnchorRef}
          owner="composer-plus"
          placement="above"
          align="left"
          offset={4}
          width={menuWidth}
          mobileSheet={{ label: t("composer.plusMenu"), onClose: () => setOpen(false) }}
        >
          <div
            ref={menuRef}
            className="composer-context-menu composer-plus-menu"
            role="menu"
            aria-label={t("composer.plusMenu")}
            style={{ "--composer-menu-width": `${menuWidth}px` } as CSSProperties}
            onKeyDown={(event) => handleFloatingMenuKeyDown(event, () => setOpen(false), buttonRef.current)}
          >
            {mobileAttachments ? (
              <ComposerMobileAttachmentChoices
                onTakePhoto={() => {
                  setOpen(false);
                  mobileAttachments.onTakePhoto();
                }}
                onPickPhotos={() => {
                  setOpen(false);
                  mobileAttachments.onPickPhotos();
                }}
                onPickFiles={() => {
                  setOpen(false);
                  mobileAttachments.onPickFiles();
                }}
              />
            ) : (
              <button
                role="menuitem"
                type="button"
                onClick={() => {
                  setOpen(false);
                  onAddAttachment();
                }}
              >
                <Paperclip className="icon-lg" />
                <span className="composer-plus-menu-item-title">{t("composer.addAttachment")}</span>
                <span className="composer-plus-menu-item-desc">{t("composer.addAttachmentHint")}</span>
              </button>
            )}
            <PlusMenuCommands label={t("composer.plusSectionCommands")} commands={builtInCommands} onSelect={selectCommand} />
            <PlusMenuCommands label={t("composer.plusSectionSkills")} commands={skillCommands} onSelect={selectCommand} />
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

// One group of the plus menu. A command's title leads and the slash command
// that runs it trails like a shortcut; a skill is its name, which is already
// what one types. The longer description waits in the tooltip.
function PlusMenuCommands({
  label,
  commands,
  onSelect
}: {
  label: string;
  commands: ComposerSlashCommand[];
  onSelect: (command: ComposerSlashCommand) => void;
}): JSX.Element | null {
  if (commands.length === 0) return null;
  return (
    <>
      <div className="composer-plus-menu-section" role="presentation">{label}</div>
      {commands.map((command) => (
        <Tooltip content={command.disabledReason ?? command.description} key={command.id}>
          <button
            role="menuitem"
            type="button"
            disabled={Boolean(command.disabledReason)}
            onClick={() => onSelect(command)}
          >
            <SlashCommandIcon command={command} />
            <span className="composer-plus-menu-item-title">
              {command.kind === "skill" ? command.name : command.title}
            </span>
            {command.kind === "skill" ? null : (
              <span className="composer-plus-menu-item-desc">/{command.name}</span>
            )}
          </button>
        </Tooltip>
      ))}
    </>
  );
}

// Icon resolver shared by the "/" command panel and the "+" menu so both
// surfaces show the same glyph for the same command.
export function SlashCommandIcon({ command }: { command: ComposerSlashCommand }): JSX.Element {
  switch (command.action ?? command.id) {
    case "review":
      return <Search className="icon" />;
    case "open-review":
      return <GitCompare className="icon" />;
    case "debug":
      return <Bug className="icon" />;
    case "fix":
      return <Hammer className="icon" />;
    case "test":
      return <FlaskConical className="icon" />;
    case "explain":
      return <CircleHelp className="icon" />;
    case "commit":
      return <GitCommitHorizontal className="icon" />;
    case "pr":
      return <GitPullRequest className="icon" />;
    case "open-skills":
      return <Puzzle className="icon" />;
    case "new-thread":
      return <MessageSquarePlus className="icon" />;
    case "open-terminal":
      return <Terminal className="icon" />;
    case "open-files":
      return <FileText className="icon" />;
    case "open-browser":
      return <Globe className="icon" />;
    case "open-workspace":
      return <FolderOpen className="icon" />;
    case "no-workspace":
      return <FolderX className="icon" />;
    case "reset-side-thread":
      return <RotateCcw className="icon" />;
    case "context":
      return <PieChart className="icon" />;
    case "compact":
      return <FoldVertical className="icon" />;
    case "instructions":
      return <ScrollText className="icon" />;
    case "fast":
      return <Zap className="icon" />;
    case "model":
      return <Cpu className="icon" />;
    case "effort":
      return <Gauge className="icon" />;
    case "settings":
      return <Settings className="icon" />;
    default:
      return <Puzzle className="icon" />;
  }
}

type AccessOption = PermissionModeOption & {
  key: string;
  approveForMe: boolean;
};

function accessOptions(
  engine: string | undefined,
  includeApproveForMe: boolean,
  advertised?: readonly EnginePermissionModeInfo[],
): AccessOption[] {
  const options: AccessOption[] = [];
  for (const option of permissionModeOptions(engine, advertised)) {
    options.push({ ...option, key: option.mode, approveForMe: false });
    if (includeApproveForMe && option.mode === "standard") {
      options.push({
        key: "approve_for_me",
        mode: "standard",
        approveForMe: true,
        label: translate("runtime.permission.approveForMe"),
        hint: translate("runtime.permission.approveForMeHint"),
        icon: ShieldCheck,
        tone: "neutral",
      });
    }
  }
  return options;
}

// The permission menu uses the shared select rows: the mode's symbol (the
// same one its chip shows), its name, and one line on what it allows.
export function AccessMenu({
  permissions,
  engine,
  permissionModes,
  disabled,
  onSelect,
  onKeyDown
}: {
  permissions?: PermissionSummary;
  engine?: string;
  permissionModes?: readonly EnginePermissionModeInfo[];
  disabled: boolean;
  onSelect: (mode: PermissionMode, approveForMe?: boolean) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
}): JSX.Element {
  useI18n();
  const menuRef = useRef<HTMLDivElement>(null);
  useFloatingMenuFocus(menuRef);
  const mode = permissionModeFromSummary(permissions);
  const approveForMeOn = mode === "standard" && Boolean(permissions?.approve_for_me);
  const showApproveForMe = (engine || "wuu") === "wuu";
  return (
    <div
      ref={menuRef}
      className="select-menu-panel access-menu"
      role="menu"
      style={{ "--composer-menu-width": `${composerMenuWidth(PERMISSION_MENU_WIDTH)}px` } as CSSProperties}
      onKeyDown={onKeyDown}
    >
      {accessOptions(engine, showApproveForMe, permissionModes).map((option) => {
        const selected = mode === option.mode && option.approveForMe === approveForMeOn;
        const Icon = option.icon;
        return (
          <button
            key={option.key}
            className={`select-menu-item permission-mode-option${option.tone === "danger" ? " is-danger" : ""}`}
            role="menuitemradio"
            aria-checked={selected}
            type="button"
            disabled={disabled}
            onClick={() => onSelect(option.mode, showApproveForMe ? option.approveForMe : undefined)}
          >
            <Icon className="permission-mode-icon" aria-hidden="true" />
            <span className="select-menu-item-text">
              <span className="select-menu-item-label">{option.label}</span>
              <span className="select-menu-item-hint">{option.hint}</span>
            </span>
            <Check className="select-menu-check" aria-hidden="true" />
          </button>
        );
      })}
    </div>
  );
}

export function WorkspacePickerMenu({
  projects,
  activeContext,
  query,
  setQuery,
  onSelectWorkspace,
  onSelectNoProject,
  onCreateWorkspace,
  onOpenWorkspace,
  onKeyDown,
}: {
  projects: DesktopProject[];
  activeContext?: RuntimeContext;
  query: string;
  setQuery: (value: string) => void;
  onSelectWorkspace: (id: string) => void;
  onSelectNoProject: () => void;
  onCreateWorkspace: () => void;
  onOpenWorkspace: () => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t } = useI18n();
  const menuRef = useRef<HTMLDivElement>(null);
  useFloatingMenuFocus(menuRef);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredWorkspaces = normalizedQuery
    ? projects.filter((project) => project.name.toLocaleLowerCase().includes(normalizedQuery) || project.path.toLocaleLowerCase().includes(normalizedQuery))
    : projects;
  // Conversations without a project lead the list, as they lead the sidebar.
  const conversationLabel = t("composer.conversation");
  const showConversation = !normalizedQuery || conversationLabel.toLocaleLowerCase().includes(normalizedQuery);
  const noProject = activeContext?.kind === "no_project";

  return (
    <div ref={menuRef} className="composer-project-menu" role="menu" onKeyDown={onKeyDown}
      style={{ "--composer-project-menu-width": `${composerMenuWidth(COMPOSER_PROJECT_MENU_WIDTH)}px` } as CSSProperties}>
      <label className="menu-search project-search">
        <Search className="icon-sm" aria-hidden="true" />
        <input value={query} aria-label={t("runtime.searchWorkspaces")} placeholder={t("runtime.searchWorkspaces")}
          onChange={(event) => setQuery(event.target.value)} />
      </label>
      <div className="project-picker-list">
        {showConversation ? (
          <button type="button" role="menuitemradio" aria-checked={noProject}
            disabled={!hostSupports("createBlankProject")} onClick={onSelectNoProject}>
            <MessageSquare />
            <span>{conversationLabel}</span>
            {noProject ? <Check /> : null}
          </button>
        ) : null}
        {filteredWorkspaces.map((project) => {
          const selected = activeContext?.kind === "project" && activeContext.project_id === project.id;
          return (
            <button key={project.id} type="button" role="menuitemradio" aria-checked={selected} title={project.name} onClick={() => onSelectWorkspace(project.id)}>
              <Folder />
              <span>{project.name}</span>
              {selected ? <Check /> : null}
            </button>
          );
        })}
        {!showConversation && filteredWorkspaces.length === 0 ? <div className="project-picker-empty">{t("runtime.noMatchingWorkspaces")}</div> : null}
      </div>
      <div className="project-picker-divider" />
      <button type="button" role="menuitem" disabled={!hostSupports("chooseProjectFolder")} onClick={onOpenWorkspace}>
        <FolderOpen />
        <span>{t("runtime.openFolder")}</span>
      </button>
      <button type="button" role="menuitem" disabled={!hostSupports("createBlankProject")} onClick={onCreateWorkspace}>
        <FolderPlus />
        <span>{t("runtime.createBlankWorkspace")}</span>
      </button>
    </div>
  );
}
