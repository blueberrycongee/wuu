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
  GitBranch,
  GitCommitHorizontal,
  GitCompare,
  GitPullRequest,
  Globe,
  Hammer,
  Lock,
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
  GitStatusResult,
  InitializeResult,
  PermissionSummary,
  ProviderModelSummary,
  ProviderSummary,
  RuntimeContext
} from "../shared/protocol";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../shared/protocol";
import { FloatingMenuPortal, isInsideFloatingMenu } from "./ComposerFloatingMenu";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import type {
  CodexModelLoadState,
  CodexRuntimeMenu,
  ComposerVariant,
  FloatingMenuPlacement,
  PermissionMode
} from "./ComposerTypes";
import { COMPOSER_PROJECT_MENU_WIDTH } from "./ComposerTypes";
import { lastEffortForEngineModel } from "./DraftEngineMemory";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import { lastEffortForRuntimeModel, lastModelForProvider } from "./DraftRuntimeMemory";
import {
  codexEffortOptions,
  displayCodexModelName,
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

// The panel is drawn at 224px for the default UI size and widens with larger
// UI text so model names keep the same room. The floating layer positions the
// panel from this number, so it is resolved here rather than in CSS.
const RUNTIME_PANEL_WIDTH = 224;

export function runtimePanelWidth(): number {
  const uiSize = Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--font-ui"));
  const defaultSize = MESSAGE_FLOW_FONT_SIZE_RANGE.default;
  return uiSize > defaultSize ? Math.round((RUNTIME_PANEL_WIDTH * uiSize) / defaultSize) : RUNTIME_PANEL_WIDTH;
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

// Panels opened from the composer trigger take focus so the keyboard can
// drive them: each page focuses its search field, its checked row, or its
// primary row. Touch shells skip this so a phone keyboard does not pop up.
function useRuntimePanelFocus(panelRef: RefObject<HTMLElement | null>, pageKey: string, enabled: boolean): void {
  useEffect(() => {
    if (!enabled || isTouchWebShell()) return undefined;
    // The floating layer reveals the panel after its first measurement.
    const frame = requestAnimationFrame(() => {
      const page = panelRef.current;
      const target = page?.querySelector<HTMLElement>("input[type='search']")
        ?? page?.querySelector<HTMLElement>("[aria-checked='true']:not(:disabled)")
        ?? page?.querySelector<HTMLElement>(".runtime-panel-model")
        ?? page?.querySelector<HTMLElement>("button:not(:disabled)");
      target?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [enabled, pageKey, panelRef]);
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
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  const target = event.target as HTMLElement;
  if (target instanceof HTMLInputElement && target.type === "range") return;
  const items = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>("input[type='search'], button:not(:disabled)")
  );
  if (items.length === 0) return;
  event.preventDefault();
  const index = items.indexOf(target);
  const next = index < 0
    ? event.key === "ArrowDown" ? 0 : items.length - 1
    : Math.min(items.length - 1, Math.max(0, index + (event.key === "ArrowDown" ? 1 : -1)));
  items[next]?.focus();
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

function RuntimePanelSummary({
  engine,
  engineId,
  provider,
  engineLocked,
  hideEngine = false,
  compactSummary = false,
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
  onHandoff,
  onSelectEffort
}: {
  engine: string;
  engineId: string;
  provider?: string;
  engineLocked: boolean;
  hideEngine?: boolean;
  compactSummary?: boolean;
  model: string;
  effortOptions: string[];
  selectedEffort: string;
  effortDisabled: boolean;
  speed?: string;
  defaultSpeed?: string;
  speedDisabled?: boolean;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  onOpenEngines: () => void;
  onOpenProviders?: () => void;
  onOpenModels: () => void;
  onHandoff?: () => void;
  onSelectEffort: (effort: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [pendingSpeed, setPendingSpeed] = useState<string | undefined>(undefined);
  const [speedSaving, setSpeedSaving] = useState(false);
  useEffect(() => setPendingSpeed(undefined), [speed, model]);
  const requestedSpeed = pendingSpeed ?? speed;
  const displayedSpeed = requestedSpeed || defaultSpeed;
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

  return (
    <div className="runtime-panel-summary">
      <div className="runtime-panel-context">
        {onSelectSpeed ? <button
          type="button"
          className="runtime-panel-fast"
          aria-label={t("runtime.fastMode")}
          aria-pressed={displayedSpeed ? displayedSpeed === "fast" : "mixed"}
          title={t(displayedSpeed === "fast" ? "runtime.fastModeOn" : displayedSpeed === "standard" ? "runtime.fastModeOff" : "runtime.fastModeDefault")}
          disabled={speedDisabled || speedSaving}
          onClick={() => { void changeSpeed(displayedSpeed === "fast" ? "standard" : "fast"); }}
        ><Zap aria-hidden="true" /></button> : null}
        {!hideEngine ? <button type="button" onClick={onOpenEngines}>
          <EngineIcon engine={engineId} />
          <span>{engineLabel(engine)}</span>
          {engineLocked ? <Lock aria-hidden="true" /> : null}
        </button> : null}
        {provider && onOpenProviders ? (
          <>
            {!hideEngine ? <span className="runtime-panel-context-separator" aria-hidden="true">/</span> : null}
            <button type="button" onClick={onOpenProviders}>
              <span>{provider}</span>
            </button>
          </>
        ) : null}
        {onSelectSpeed ? <button
          className="runtime-panel-speed-reset"
          type="button"
          aria-label={t("runtime.resetSpeed")}
          title={t("runtime.resetSpeed")}
          disabled={speedDisabled || speedSaving || !requestedSpeed}
          onClick={() => { void changeSpeed(""); }}
        ><RotateCcw aria-hidden="true" /></button> : null}
      </div>
      <button type="button" className="runtime-panel-model" onClick={onOpenModels}>
        <span className="runtime-panel-model-name">{model}</span>
        <span key={previewEffort} className="runtime-panel-effort-value">{variantLabel(previewEffort)}</span>
        <ChevronRight aria-hidden="true" />
      </button>
      {effortOptions.length > 1 ? (
        <div className={compactSummary ? "runtime-panel-effort-row" : undefined} style={compactSummary ? undefined : { display: "contents" }}>
        <EffortSelector
          options={effortOptions}
          selectedVariant={selectedEffort}
          disabled={effortDisabled}
          onPreviewEffort={setPreviewEffort}
          onSelectEffort={onSelectEffort}
        />
        </div>
      ) : null}
      {onHandoff ? (
        <button type="button" className="runtime-panel-handoff" onClick={onHandoff}>
          {t("runtime.handoffToNewSession")}
        </button>
      ) : null}
    </div>
  );
}

type PermissionModeState = PermissionMode;

type PermissionModeOption = {
  mode: PermissionMode;
  label: string;
  chipLabel: string;
  icon: IconComponent;
  tone: ChipTone;
};

function permissionModeLabels(engine?: string): Record<PermissionMode, { label: string; chipLabel: string }> {
  switch (engine?.trim().toLowerCase()) {
    case "codex":
      return {
        standard: { label: "Workspace Write", chipLabel: "Workspace Write" },
        read_only: { label: "Read Only", chipLabel: "Read Only" },
        unconfined: { label: "Danger Full Access", chipLabel: "Danger Full Access" }
      };
    case "claude":
      return {
        standard: { label: "Don't Ask", chipLabel: "Don't Ask" },
        read_only: { label: "Plan", chipLabel: "Plan" },
        unconfined: { label: "Bypass Permissions", chipLabel: "Bypass Permissions" }
      };
    case "cursor":
      return {
        standard: { label: "Agent", chipLabel: "Agent" },
        read_only: { label: "Plan", chipLabel: "Plan" },
        unconfined: {
          label: translate("runtime.permission.unconfined"),
          chipLabel: translate("runtime.permission.unconfined")
        }
      };
    case "devin":
      return {
        standard: { label: "Ask", chipLabel: "Ask" },
        read_only: { label: "Ask", chipLabel: "Ask" },
        unconfined: { label: "Bypass", chipLabel: "Bypass" }
      };
    default:
      return {
        standard: {
          label: translate("runtime.permission.standardLabel"),
          chipLabel: translate("runtime.permission.standard")
        },
        read_only: {
          label: translate("runtime.permission.readOnly"),
          chipLabel: translate("runtime.permission.readOnly")
        },
        unconfined: {
          label: translate("runtime.permission.unconfined"),
          chipLabel: translate("runtime.permission.unconfined")
        }
      };
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

function advertisedPermissionLabel(
  advertised: EnginePermissionModeInfo | undefined,
  fallback: { label: string; chipLabel: string }
): { label: string; chipLabel: string } {
  const label = advertised?.label?.trim();
  if (!label) {
    return fallback;
  }
  return { label, chipLabel: label };
}

function permissionModeOptions(
  engine?: string,
  advertised?: readonly EnginePermissionModeInfo[],
): PermissionModeOption[] {
  const labels = permissionModeLabels(engine);
  const catalog = advertised?.filter((mode) => mode.mode === "standard" || mode.mode === "read_only" || mode.mode === "unconfined");
  const modes: PermissionMode[] = catalog && catalog.length > 0
    ? catalog.map((mode) => mode.mode)
    : engineOffersReadOnly(engine)
      ? ["standard", "read_only", "unconfined"]
      : ["standard", "unconfined"];
  return modes.map((mode) => {
    const native = catalog?.find((item) => item.mode === mode);
    return {
      mode,
      ...advertisedPermissionLabel(native, labels[mode]),
      ...permissionModeIcons(mode)
    };
  });
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

export function permissionModeHasAdvancedOverrides(_permissions?: PermissionSummary): boolean {
  return false;
}

export function permissionModeOption(
  mode: PermissionModeState,
  engine?: string,
  advertised?: readonly EnginePermissionModeInfo[],
): Omit<PermissionModeOption, "mode"> & { mode: PermissionModeState } {
  const options = permissionModeOptions(engine, advertised);
  const match = options.find((option) => option.mode === mode);
  if (match) {
    return match;
  }
  const labels = permissionModeLabels(engine);
  return { mode, ...advertisedPermissionLabel(undefined, labels[mode] ?? labels.standard), ...permissionModeIcons(mode) };
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
  onHandoffModel,
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
  onHandoffModel?: (provider: string, model: string) => void;
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
      ? runtimeTriggerLabel(initialized, currentProviderModel, currentCodexModel, handoff.model)
      : handoff.provider
        ? `${handoff.provider} · ${t("runtime.selectModel")}`
        : t("runtime.selectModel")
    : externalEngine
      ? externalModelInfo?.display_name || engineModel || t("runtime.engineDefaultModel")
      : runtimeTriggerLabel(initialized, currentProviderModel, currentCodexModel, targetModel);
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
              onHandoffModel={handoff ? undefined : onHandoffModel}
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
              hideHandoff={Boolean(handoff)}
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
  useRuntimePanelFocus(panelRef, `${engine?.id ?? "engine"}:${view}`, true);
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
      style={runtimePanelStyle(view === "models" ? filteredModels.length : engineOptions.length, width)}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, view, showSummary)}
    >
      <div key={`${engine?.id ?? "engine"}:${view}`} className={`runtime-panel-page is-${direction}`}>
        {view === "summary" ? (
          <RuntimePanelSummary
            engine={engineLabel(selectedEngine, engine)}
            engineId={selectedEngine}
            engineLocked={engineLocked}
            model={effectiveModel?.display_name || effectiveModelID || t("runtime.engineDefaultModel")}
            effortOptions={effortOptions}
            selectedEffort={effectiveEffort}
            effortDisabled={disabled}
            speed={selectedSpeed}
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
  onHandoffModel,
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
  hideHandoff = false,
  embedded = false,
  hideEngine = false,
  compactSummary = false,
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
  onHandoffModel?: (provider: string, model: string) => void;
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
  hideHandoff?: boolean;
  embedded?: boolean;
  hideEngine?: boolean;
  compactSummary?: boolean;
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

  useRuntimePanelFocus(panelRef, view, autoFocus);

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
      className={`codex-runtime-menu codex-model-menu runtime-panel is-${view}${embedded ? " is-embedded" : ""}`}
      role="menu"
      style={runtimePanelStyle(pageRows, width)}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, view, showSummary)}
    >
      <div key={view} className={`runtime-panel-page is-${direction}`}>
        {view === "summary" ? (
          <RuntimePanelSummary
            engine={selectedEngine}
            engineId={selectedEngine}
            provider={effectiveProviderName}
            engineLocked={engineLocked}
            hideEngine={hideEngine}
            compactSummary={compactSummary}
            model={effectiveModel ? providerModelDisplayName(effectiveModel) : effectiveModelID || t("runtime.selectModel")}
            effortOptions={effortOptions}
            selectedEffort={effectiveVariant}
            effortDisabled={false}
            speed={initialized.speed}
            defaultSpeed={effectiveModel?.default_speed}
            speedDisabled={running}
            onSelectSpeed={effectiveModel?.fast_mode ? onSelectSpeed : undefined}
            onOpenEngines={() => openView("engines")}
            onOpenProviders={() => openView("providers")}
            onOpenModels={() => openView("models")}
            onHandoff={hideHandoff || !onHandoffModel ? undefined : () => onHandoffModel(effectiveProviderName, effectiveModelID)}
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

  // The capsule assigns an equal span to each discrete level. Handle pointer
  // coordinates directly so the hit regions match the visible segments;
  // native range events continue to provide keyboard interaction.
  const previewPointer = (clientX: number): void => {
    const rect = sliderRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0) return;
    const ratio = (clientX - rect.left) / rect.width;
    if (!Number.isFinite(ratio)) return;
    previewTo(Math.min(orderedOptions.length - 1, Math.max(0, Math.floor(ratio * orderedOptions.length))));
  };
  const progress = `${((displayIndex + 1) / orderedOptions.length) * 100}%`;

  return (
    <div
      ref={sliderRef}
      className={`codex-effort-slider${disabled ? " is-disabled" : ""}`}
      style={{ "--effort-progress": progress } as CSSProperties}
    >
      <span className="codex-effort-track" aria-hidden="true">
        <span className="codex-effort-fill" />
      </span>
      <span className="codex-effort-stops" aria-hidden="true">
        {orderedOptions.slice(0, -1).map((variant, index) => (
          <span
            key={variant || `default-${index}`}
            className={index < displayIndex ? "is-filled" : ""}
            style={{ left: `${((index + 1) / orderedOptions.length) * 100}%` }}
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

function runtimeTriggerLabel(
  initialized: InitializeResult,
  providerModel?: ProviderModelSummary,
  codexModel?: CodexModelSummary,
  fallbackModelId = initialized.model
): string {
  if (codexModel) {
    return shortCodexModelLabel(codexModel.slug);
  }
  return shortCodexModelLabel(providerModel?.display_name || fallbackModelId);
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
  const [open, setOpen] = useState(false);

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
        className="composer-tool-button composer-plus-button"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("composer.plusMenu")}
        title={t("composer.plusMenu")}
        disabled={disabled}
        onPointerDown={(event) => { if (isTouchWebShell()) event.preventDefault(); }}
        onClick={() => setOpen((current) => !current)}
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
          width={320}
          matchAnchorWidth
          mobileSheet={{ label: t("composer.plusMenu"), onClose: () => setOpen(false) }}
        >
          <div className="composer-context-menu composer-plus-menu" role="menu" aria-label={t("composer.plusMenu")}>
            <div className="composer-plus-menu-section" role="presentation">{t("composer.plusSectionAdd")}</div>
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
            <div className="composer-plus-menu-section" role="presentation">{t("composer.plusSectionCommands")}</div>
            {commands.map((command) => (
              <Tooltip content={command.disabledReason} key={command.id}>
                <button
                  role="menuitem"
                  type="button"
                  disabled={Boolean(command.disabledReason)}
                  onClick={() => {
                    setOpen(false);
                    onSelectCommand(command);
                  }}
                >
                  <SlashCommandIcon command={command} />
                  <span className="composer-plus-menu-item-title">
                    {command.kind === "skill" ? command.description : command.title}
                  </span>
                  <span className="composer-plus-menu-item-desc">
                    {command.kind === "skill" ? command.title : command.description}
                  </span>
                </button>
              </Tooltip>
            ))}
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
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
    case "open-project":
      return <FolderOpen className="icon" />;
    case "no-project":
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

type AccessOption = {
  key: string;
  mode: PermissionMode;
  approveForMe: boolean;
  label: string;
  tone: ChipTone;
};

function accessOptions(
  engine: string | undefined,
  includeApproveForMe: boolean,
  advertised?: readonly EnginePermissionModeInfo[],
): AccessOption[] {
  const options: AccessOption[] = [];
  for (const option of permissionModeOptions(engine, advertised)) {
    options.push({
      key: option.mode,
      mode: option.mode,
      approveForMe: false,
      label: option.label,
      tone: option.tone,
    });
    if (includeApproveForMe && option.mode === "standard") {
      options.push({
        key: "approve_for_me",
        mode: "standard",
        approveForMe: true,
        label: translate("runtime.permission.approveForMe"),
        tone: "neutral",
      });
    }
  }
  return options;
}

export function AccessMenu({
  permissions,
  engine,
  permissionModes,
  disabled,
  onSelect
}: {
  permissions?: PermissionSummary;
  engine?: string;
  permissionModes?: readonly EnginePermissionModeInfo[];
  disabled: boolean;
  onSelect: (mode: PermissionMode, approveForMe?: boolean) => void;
}): JSX.Element {
  useI18n();
  const mode = permissionModeFromSummary(permissions);
  const approveForMeOn = mode === "standard" && Boolean(permissions?.approve_for_me);
  const showApproveForMe = (engine || "wuu") === "wuu";
  return (
    <div className="composer-context-menu access-menu" role="menu">
      {accessOptions(engine, showApproveForMe, permissionModes).map((option) => {
        const selected = mode === option.mode && option.approveForMe === approveForMeOn;
        return (
          <button
            key={option.key}
            className={`permission-mode-option ${option.tone}`}
            role="menuitemradio"
            aria-checked={selected}
            aria-label={option.label}
            type="button"
            disabled={disabled}
            onClick={() => onSelect(option.mode, showApproveForMe ? option.approveForMe : undefined)}
          >
            <strong>{option.label}</strong>
            {selected ? <Check aria-hidden="true" /> : null}
          </button>
        );
      })}
    </div>
  );
}



export function BranchMenu({
  gitStatus,
  onSelectBranch
}: {
  gitStatus: GitStatusResult;
  onSelectBranch: (branch: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const branches = gitStatus.branches ?? [];
  return (
    <div className="composer-context-menu branch-menu" role="menu">
      {gitStatus.dirty_count > 0 ? (
        <div className="composer-menu-note warning">
          <strong>{t("runtime.uncommittedChanges")}</strong>
          <span>{t(gitStatus.dirty_count === 1 ? "runtime.dirtyFileWarningOne" : "runtime.dirtyFileWarning", { count: gitStatus.dirty_count })}</span>
        </div>
      ) : null}
      {branches.length === 0 ? <div className="composer-menu-empty">{t("runtime.noLocalBranches")}</div> : null}
      {branches.map((branch) => {
        const selected = branch === gitStatus.branch;
        return (
          <button
            key={branch}
            role="menuitem"
            type="button"
            disabled={selected || !hostSupports("checkoutGitBranch")}
            onClick={() => onSelectBranch(branch)}
          >
            <GitBranch className="icon-lg" />
            <span>{branch}</span>
            {selected ? <Check className="icon" /> : null}
          </button>
        );
      })}
    </div>
  );
}

export function ProjectPickerMenu({
  projects,
  activeContext,
  query,
  setQuery,
  onSelectProject,
  onSelectNoProject,
  onCreateProject,
  onOpenProject,
  folderActions = true,
}: {
  projects: DesktopProject[];
  activeContext?: RuntimeContext;
  query: string;
  setQuery: (value: string) => void;
  onSelectProject: (id: string) => void;
  onSelectNoProject: () => void;
  onCreateProject: () => void;
  onOpenProject: () => void;
  folderActions?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredProjects = normalizedQuery
    ? projects.filter((project) => project.name.toLocaleLowerCase().includes(normalizedQuery) || project.path.toLocaleLowerCase().includes(normalizedQuery))
    : projects;

  return (
    <div className="composer-project-menu" role="menu"
      style={{ "--composer-project-menu-width": `${COMPOSER_PROJECT_MENU_WIDTH}px` } as CSSProperties}>
      <label className="menu-search project-search">
        <Search className="icon-lg" />
        <input value={query} placeholder={t("runtime.searchProjects")} onChange={(event) => setQuery(event.target.value)} />
      </label>
      <div className="project-picker-list">
        {filteredProjects.length === 0 ? <div className="project-picker-empty">{t("runtime.noMatchingProjects")}</div> : null}
        {filteredProjects.map((project) => {
          const selected = activeContext?.kind === "project" && activeContext.project_id === project.id;
          return (
            <button key={project.id} type="button" role="menuitem" title={project.name} onClick={() => onSelectProject(project.id)}>
              <Folder className="icon-lg" />
              <span>{project.name}</span>
              {selected ? <Check className="icon-lg" /> : null}
            </button>
          );
        })}
      </div>
      {folderActions ? <>
      <div className="project-picker-divider" />
      <button type="button" role="menuitem" disabled={!hostSupports("chooseProjectFolder")} onClick={onOpenProject}>
        <FolderOpen className="icon-lg" />
        <span>{t("runtime.useExistingFolder")}</span>
      </button>
      <button type="button" role="menuitem" disabled={!hostSupports("createBlankProject")} onClick={onCreateProject}>
        <FolderPlus className="icon-lg" />
        <span>{t("runtime.createBlankProject")}</span>
      </button>
      <button type="button" role="menuitem" disabled={!hostSupports("createBlankProject")} onClick={onSelectNoProject}>
        <FolderX className="icon-lg" />
        <span>{t("runtime.noProject")}</span>
        {activeContext?.kind === "no_project" ? <Check className="icon-lg" /> : null}
      </button>
      </> : null}
    </div>
  );
}
