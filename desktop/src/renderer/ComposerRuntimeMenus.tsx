import { ComposerPluginChoices, type ComposerPluginMenu } from "./ComposerPluginMenu";
import type { ComposerConfiguration, ComposerWorkspace } from "./ComposerTypes";
import { useComposerServices, useComposerConnectionDisplay } from "./ComposerServicesContext";
import type { SettingsConnectionDisplayV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { chatGPTConnectionModels, engineRequiresHostModel, isChatGPTProvider } from "./RuntimeHelpers";
import { hostSupports } from "./HostCapabilities";
import {
  Bug,
  Check,
  ChevronDown,
  CircleHelp,
  Cpu,
  Eye,
  FileText,
  FlaskConical,
  FoldVertical,
  Folder,
  FolderOpen,
  FolderPlus,
  Gauge,
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
  ShieldCheck,
  Terminal,
  TriangleAlert,
  Zap,
  type IconComponent
} from "./WuuIcons";
import { type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject, useEffect, useRef, useState } from "react";
import type {
  CodexModelSummary,
  EngineInfo,
  EnginePermissionModeInfo,
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
import { SelectMenu } from "./SelectMenu";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import { lastEffortForRuntimeModel } from "./DraftRuntimeMemory";
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
import { showToast } from "./Toast";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import { ComposerMobileAttachmentChoices } from "./ComposerCamera";
import { isTouchWebShell } from "./ComposerFocus";
import { ProviderMark } from "./ProviderMarks";
import { primaryShortcutLabel } from "./platform";

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
  activeEngine: string,
  display: Pick<SettingsConnectionDisplayV1, "label">
): EngineOption[] {
  const options: EngineOption[] = [{ id: "wuu", label: engineLabel("wuu") }];
  for (const engine of engines ?? []) {
    if (engine.id === "wuu") continue;
    if ((!engine.enabled || !engine.binary_ok) && engine.id !== activeEngine) continue;
    options.push({ id: engine.id, label: display.label(`engine:${engine.id}`, engineLabel(engine.id, engine)) });
  }
  if (activeEngine && activeEngine !== "wuu" && !options.some((option) => option.id === activeEngine)) {
    options.push({ id: activeEngine, label: engineLabel(activeEngine) });
  }
  return options;
}

// A selectable model row shared by the engine and provider columns. The kbd
// hint marks the first nine visible rows, which ⌘/Ctrl+1–9 selects directly.
function RuntimeModelRow({
  name,
  checked,
  disabled,
  quickIndex,
  mark,
  onSelect
}: {
  name: string;
  checked: boolean;
  disabled?: boolean;
  quickIndex?: number;
  mark?: ReactNode;
  onSelect: () => void;
}): JSX.Element {
  return (
    <button
      className="codex-model-item"
      role="menuitemradio"
      type="button"
      aria-checked={checked}
      disabled={disabled}
      onClick={onSelect}
    >
      {mark ?? null}
      <span className="codex-model-item-name">{name}</span>
      {quickIndex !== undefined && quickIndex < 9 ? (
        <kbd className="runtime-model-kbd" aria-hidden="true">{primaryShortcutLabel(quickIndex + 1)}</kbd>
      ) : null}
      {checked ? <Check className="icon-lg" /> : null}
    </button>
  );
}

// The flat picker lays engines, models, and effort out as columns instead of
// drilling between pages. Its footprint is wider than the old summary card,
// so the base width follows the reference panel rather than a list menu.
const RUNTIME_PANEL_WIDTH = 640;
export const PERMISSION_MENU_WIDTH = 264;

export function runtimePanelWidth(): number {
  return composerMenuWidth(RUNTIME_PANEL_WIDTH);
}

// Arrow keys move between rows across the whole panel. The search field keeps
// Home and End for its caret; Escape is handled by the picker's own document
// listener so it always closes and refocuses the trigger.
function handleRuntimePanelKeyDown(
  event: ReactKeyboardEvent<HTMLElement>,
  quickSelect?: (index: number) => void
): void {
  if ((event.metaKey || event.ctrlKey) && quickSelect && /^[1-9]$/.test(event.key)) {
    event.preventDefault();
    quickSelect(Number(event.key) - 1);
    return;
  }
  moveFloatingMenuFocus(event);
}

// Engine switching lives in a vertical icon column, the same shape as the
// workspace tool rail the picker opens from when controls are docked there.
function RuntimeEngineRail({
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
      className={`runtime-engine-rail${locked ? " is-locked" : ""}`}
      role="group"
      aria-label={locked && lockedDescription ? lockedDescription : t("runtime.engine")}
    >
      {options.map((option) => {
        const isSelected = option.id === selected;
        const button = (
          <button
            className="runtime-engine-icon"
            key={option.id}
            role="menuitemradio"
            type="button"
            disabled={locked || running}
            aria-checked={isSelected}
            aria-label={option.label}
            onClick={() => onSelect(option.id)}
          >
            <EngineIcon engine={option.id} />
            {locked && isSelected ? <Lock aria-hidden="true" /> : null}
          </button>
        );
        return locked && isSelected ? (
          <Tooltip key={option.id} content={lockedDescription}>{button}</Tooltip>
        ) : button;
      })}
    </div>
  );
}

// The level list replaces the drag slider on the flat panel: radio rows share
// the menu's arrow-key navigation. The model's own default level trails the
// "Default" row as the value it resolves to, so "Default" appears once.
function RuntimeEffortColumn({
  model,
  options,
  selected,
  defaultId,
  disabled,
  busy,
  onSelect
}: {
  model: string;
  options: string[];
  selected: string;
  defaultId: string;
  disabled: boolean;
  busy: boolean;
  onSelect: (effort: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="runtime-effort-column" role="group" aria-label={t("runtime.reasoningEffort")}>
      <div className="runtime-flat-side-title">
        <span className="runtime-flat-side-heading">{t("runtime.reasoningEffort")}</span>
        <TruncatedText className="runtime-flat-side-model" text={model} />
      </div>
      {options.map((option) => {
        const isSelected = option === selected;
        return (
          <button
            className="runtime-effort-option"
            key={option || "default"}
            role="menuitemradio"
            type="button"
            disabled={disabled}
            aria-checked={isSelected}
            aria-busy={busy || undefined}
            onClick={() => onSelect(option)}
          >
            <span className="runtime-effort-option-name">{variantLabel(option)}</span>
            {option === "" && defaultId !== "" ? (
              <span className="runtime-effort-option-hint">{variantLabel(defaultId)}</span>
            ) : null}
            {isSelected ? <Check className="icon-lg" aria-hidden="true" /> : null}
          </button>
        );
      })}
    </div>
  );
}

// Speed is a segmented footer choice rather than a toolbar toggle: the flat
// panel shows both states at once. Save-in-flight keeps the control focusable
// so a keyboard user is not blurred mid-save.
function RuntimeSpeedControl({
  speed,
  defaultSpeed,
  disabled,
  onSelectSpeed
}: {
  speed?: string;
  defaultSpeed?: string;
  disabled?: boolean;
  onSelectSpeed: (speed: string) => void | Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [pendingSpeed, setPendingSpeed] = useState<string | undefined>(undefined);
  const [speedSaving, setSpeedSaving] = useState(false);
  const [speedCharging, setSpeedCharging] = useState(false);
  useEffect(() => setPendingSpeed(undefined), [speed]);
  const requestedSpeed = pendingSpeed ?? speed;
  const displayedSpeed = effectiveModelSpeed(requestedSpeed, defaultSpeed);
  const fastModeHint = `${t(displayedSpeed === "fast" ? "runtime.fastModeOn" : displayedSpeed === "standard" ? "runtime.fastModeOff" : "runtime.fastModeDefault")} · ${t("runtime.fastModeHint")}`;
  const changeSpeed = async (next: string): Promise<void> => {
    if (speedSaving || disabled || next === displayedSpeed) return;
    setSpeedCharging(next === "fast");
    setPendingSpeed(next);
    setSpeedSaving(true);
    try {
      if (await onSelectSpeed(next) === false) setPendingSpeed(undefined);
    } catch { setPendingSpeed(undefined); }
    finally { setSpeedSaving(false); }
  };
  const renderOption = (id: "standard" | "fast") => (
    <button
      key={id}
      type="button"
      className="runtime-speed-option"
      aria-pressed={displayedSpeed === id}
      disabled={Boolean(disabled) && !speedSaving}
      aria-disabled={disabled || speedSaving || undefined}
      aria-busy={speedSaving || undefined}
      onClick={() => { void changeSpeed(id); }}
    >
      {id === "fast" ? (
        <span
          className="runtime-speed-icon"
          aria-hidden="true"
          data-charging={speedCharging || undefined}
          onAnimationEnd={() => setSpeedCharging(false)}
        >
          <Zap className="runtime-speed-outline" />
          <Zap className="runtime-speed-fill" />
        </span>
      ) : null}
      {t(id === "fast" ? "runtime.speedFast" : "runtime.speedStandard")}
    </button>
  );
  return (
    <>
      <span className="runtime-flat-footer-label">{t("runtime.speed")}</span>
      <Tooltip content={fastModeHint}>
        <div className="theme-segmented runtime-speed-segmented" role="group" aria-label={t("runtime.speed")}>
          {renderOption("standard")}
          {renderOption("fast")}
        </div>
      </Tooltip>
    </>
  );
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
  onSelectProvider: (providerId: string) => void;
  onSelectModel: (provider: string, model: string, variant?: string) => void;
  onSelectEffort: (variant: string) => void;
};

export function RuntimePicker({
  inToolRail = false,
  compact = inToolRail,
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
  engineModelOptions,
  onSelectSpeed,
  onSelectEngine,
  onSelectEngineModel,
  onSelectEngineEffort,
  onSelectEngineModelOptions,
  onToggleMenu,
  onSelectModel,
  onSelectEffort,
  handoff
}: {
  initialized: ComposerConfiguration;
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
  engineModelOptions?: Record<string, string>;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  onSelectEngine?: (id: string) => void;
  onSelectEngineModel?: (model: string, effort: string) => void | Promise<boolean>;
  onSelectEngineEffort?: (effort: string) => void | Promise<boolean>;
  onSelectEngineModelOptions?: (options: Record<string, string>) => void | Promise<boolean>;
  onToggleMenu: (menu: Exclude<CodexRuntimeMenu, null>) => void;
  onSelectModel: (provider: string, model: string, variant?: string) => void | Promise<boolean>;
  onSelectEffort: (variant: string) => void | Promise<boolean>;
  handoff?: HandoffRuntimePicker;
  inToolRail?: boolean;
  compact?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const display = useComposerConnectionDisplay();
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
  const placement: FloatingMenuPlacement = inToolRail ? "middle" : "above";
  const selectedEngine = handoff ? "wuu" : activeEngine || "wuu";
  const selectedEngineInfo = engines?.find(engine => engine.id === selectedEngine);
  const externalEngine = handoff || engineRequiresHostModel(selectedEngine, selectedEngineInfo) ? "" : selectedEngine;
  const engineOptions = handoff
    ? [{ id: "wuu", label: engineLabel("wuu") }]
    : availableEngineOptions(engines, selectedEngine, display);
  const externalEngineInfo = engines?.find((engine) => engine.id === externalEngine);
  const externalModelInfo = externalEngineInfo?.models?.find((model) => engineModel ? model.id === engineModel : model.is_default);
  const triggerEngineName = display.label(`engine:${selectedEngine}`, engineLabel(selectedEngine, selectedEngineInfo));
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
      <Tooltip content={compact ? [triggerAccessibleName, running ? t("runtime.modelSwitchWhileRunning") : ""].filter(Boolean).join(" · ") : running ? t("runtime.modelSwitchWhileRunning") : undefined}>
        <button
          ref={triggerRef}
          className={`codex-runtime-trigger${compact ? " icon-button is-compact" : ""}`}
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
          {compact ? null : <><span
            key={labelKey}
            className={`codex-runtime-label${labelKey === initialLabelKey.current ? "" : " is-changed"}`}
          >
            <span className="codex-runtime-model">{triggerLabel}</span>
            {effortLabelText ? <span className="codex-runtime-effort">{effortLabelText}</span> : null}
          </span>
          <ChevronDown className="icon" /></>}
        </button>
      </Tooltip>
      {open ? (
        <FloatingMenuPortal
          anchorRef={anchorRef}
          owner="codex-runtime"
          onDismiss={() => onToggleMenu("model")}
          placement={placement}
          align="right"
          crossAxisOffset={inToolRail ? -(anchorRef.current?.offsetWidth ?? 0) - 8 : 0}
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
              selectedOptions={engineModelOptions}
              onSelectOptions={onSelectEngineModelOptions}
              disabled={running}
              onSelectModel={(model, effort) => onSelectEngineModel?.(model, effort)}
              onSelectEffort={(effort) => onSelectEngineEffort?.(effort)}
              engineOptions={engineOptions}
              selectedEngine={selectedEngine}
              engineLocked={Boolean(engineLocked)}
              running={running}
              lockedDescription={engineLocked ? t("runtime.engineLockedDescription") : undefined}
              onSelectEngine={(id) => onSelectEngine?.(id)}
              fromRail={inToolRail}
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
              onSelectProvider={handoff?.onSelectProvider}
              fromRail={inToolRail}
              width={panelWidth}
              autoFocus
            />
          )}
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

// External-engine picker: engines as an icon column, the engine's models as
// a searchable flat list, effort and per-model options in the right column,
// speed as a segmented footer. Everything stays visible at once, so a model
// switch keeps its engine and effort context on screen.
function EngineRuntimeMenu({
  engine,
  selectedModel,
  selectedEffort,
  selectedSpeed,
  selectedOptions = {},
  onSelectOptions,
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
  fromRail = false,
  width
}: {
  engine?: EngineInfo;
  selectedModel: string;
  selectedEffort: string;
  selectedSpeed: string;
  selectedOptions?: Record<string, string>;
  onSelectOptions?: (options: Record<string, string>) => void | Promise<boolean>;
  onSelectSpeed?: (speed: string) => void | Promise<boolean>;
  disabled: boolean;
  onSelectModel: (model: string, effort: string) => void | Promise<boolean>;
  onSelectEffort: (effort: string) => void | Promise<boolean>;
  engineOptions: EngineOption[];
  selectedEngine: string;
  engineLocked: boolean;
  running: boolean;
  lockedDescription?: string;
  onSelectEngine: (id: string) => void;
  // Docked next to the workspace tool rail the picker opens from; the engine
  // column mirrors to that edge so it reads as the rail's second column.
  fromRail?: boolean;
  width?: number;
}): JSX.Element {
  const { t } = useI18n();
  const panelRef = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  useFloatingMenuFocus(panelRef, `${engine?.id ?? "engine"}`);
  useEffect(() => {
    setQuery("");
  }, [engine?.id]);

  const display = useComposerConnectionDisplay();
  const catalogModels = engine?.models ?? [];
  const models = display.visibleModels(`engine:${engine?.id}`, catalogModels, [selectedModel || catalogModels.find(model => model.is_default)?.id || ""]);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredModels = normalizedQuery
    ? models.filter((model) =>
        (model.display_name || model.id).toLocaleLowerCase().includes(normalizedQuery)
        || model.id.toLocaleLowerCase().includes(normalizedQuery))
    : models;
  const effectiveModelID = selectedModel;
  const effectiveModel = models.find((model) => effectiveModelID ? model.id === effectiveModelID : model.is_default);
  const supportedEfforts = effectiveModel?.supported_efforts ?? [];
  const effortOptions = supportedEfforts.length ? orderedEffortOptions(["", ...supportedEfforts]) : [];
  // A degraded catalog still leaves its usable rows selectable, so discovery
  // failures surface as a notice rather than a banner inside the list. The
  // engine settings page keeps the full diagnostic; the notice quotes only
  // its first line.
  const modelsError = engine?.models_error?.trim();
  const modelCount = models.length;
  const modelsStatus = engine?.models_status;
  const engineID = engine?.id;
  useEffect(() => {
    if (!modelsError) return;
    const summary = t(
      modelsStatus === "partial" ? "runtime.modelsPartial"
        : modelCount > 0 ? "runtime.modelsRefreshFailed" : "runtime.modelsLoadFailed",
    );
    const detail = modelsError.split("\n").map(line => line.trim()).find(Boolean);
    showToast({
      message: detail ? `${summary}\n${detail}` : summary,
      tone: "error",
      dedupeKey: `engine-models:${engineID}:${modelsError}`,
    });
  }, [engineID, modelsStatus, modelsError, modelCount, t]);
  const changeSelection = async (save: () => void | Promise<boolean>): Promise<boolean> => {
    if (saving || disabled) return false;
    setSaving(true);
    try { return await save() !== false; }
    finally { setSaving(false); }
  };
  const selectModel = (model: string): void => {
    void changeSelection(() => onSelectModel(model, ""));
  };

  // The "engine default" row is index 0 of the quick-select list, ahead of the
  // filtered catalog rows.
  const quickSelect = (index: number): void => {
    if (disabled || saving) return;
    if (index === 0) {
      selectModel("");
      return;
    }
    const model = filteredModels[index - 1];
    if (model) selectModel(model.id);
  };
  const hasSide = effortOptions.length > 1 || Boolean(effectiveModel?.options?.length);

  return (
    <div
      ref={panelRef}
      className={`codex-runtime-menu codex-model-menu runtime-panel runtime-flat-panel${fromRail ? " from-rail" : ""}`}
      role="menu"
      style={{ "--runtime-panel-width": `${width ?? runtimePanelWidth()}px` } as CSSProperties}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, quickSelect)}
    >
      <div className="runtime-flat">
        {engineOptions.length > 1 ? (
          <RuntimeEngineRail
            options={engineOptions}
            selected={selectedEngine}
            locked={engineLocked}
            running={running}
            lockedDescription={lockedDescription}
            onSelect={(id) => { if (id !== selectedEngine) onSelectEngine(id); }}
          />
        ) : null}
        <div className="runtime-flat-main">
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
                    selectModel(first.id);
                  }
                }}
              />
            </label>
          ) : null}
          <div className="codex-model-groups runtime-flat-models">
            <RuntimeModelRow
              name={t("runtime.engineDefaultModel")}
              checked={!selectedModel}
              disabled={disabled || saving}
              quickIndex={0}
              onSelect={() => selectModel("")}
            />
            {models.length === 0 ? (
              <div className="composer-menu-empty">
                {t(engine?.models_status === "empty" ? "runtime.modelsEmpty" : "runtime.engineDefaultModelHint", { engine: engineLabel(selectedEngine, engine) })}
              </div>
            ) : null}
            {models.length > 0 && filteredModels.length === 0 ? (
              <div className="composer-menu-empty">{t("runtime.noMatchingModels")}</div>
            ) : null}
            {filteredModels.length > 0 ? (
              <div className="codex-model-group">
                {filteredModels.map((model, index) => (
                  <RuntimeModelRow
                    key={model.id}
                    name={model.display_name || model.id}
                    checked={model.id === effectiveModelID}
                    disabled={disabled || saving}
                    quickIndex={index + 1}
                    onSelect={() => selectModel(model.id)}
                  />
                ))}
              </div>
            ) : null}
          </div>
        </div>
        {hasSide ? (
          <div className="runtime-flat-side">
            {effortOptions.length > 1 ? (
              <RuntimeEffortColumn
                model={effectiveModelID ? effectiveModel?.display_name || effectiveModelID : t("runtime.engineDefaultModel")}
                options={effortOptions}
                selected={selectedEffort}
                defaultId={effectiveModel?.default_effort ?? ""}
                disabled={disabled}
                busy={saving}
                onSelect={(effort) => { void changeSelection(() => onSelectEffort(effort)); }}
              />
            ) : null}
            {effectiveModel?.options?.length ? (
              <div className="runtime-model-options">
                {effectiveModel.options.map(option => (
                  <div className="runtime-model-option" key={option.id}>
                    <span>{option.label}</span>
                    <SelectMenu
                      ariaLabel={option.label}
                      value={option.id in selectedOptions ? JSON.stringify(selectedOptions[option.id]) : ""}
                      options={[
                        { value: "", label: t("runtime.engineDefaultOption") },
                        ...option.choices.map(choice => ({ value: JSON.stringify(choice.value), label: choice.label })),
                      ]}
                      disabled={disabled || saving}
                      onChange={value => {
                        const options = { ...selectedOptions };
                        if (value === "") delete options[option.id];
                        else options[option.id] = JSON.parse(value) as string;
                        void changeSelection(() => onSelectOptions?.(options));
                      }}
                    />
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      {effectiveModel?.fast_mode && onSelectSpeed ? (
        <div className="runtime-flat-footer">
          <RuntimeSpeedControl
            speed={selectedSpeed}
            defaultSpeed={effectiveModel.default_speed}
            disabled={running}
            onSelectSpeed={onSelectSpeed}
          />
        </div>
      ) : null}
    </div>
  );
}

// Wuu-engine picker in the same flat shape as external engines: the engine
// column on the edge, every provider's models grouped in the searchable
// center column, effort on the right, speed in the footer. Providers are
// group headers rather than a separate page, so all of them stay selectable.
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
  onSelectProvider,
  fromRail = false,
  width,
  autoFocus = false,
}: {
  initialized: ComposerConfiguration;
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
  // Handoff provider headers apply the provider to the draft instead of
  // switching to it; other callers leave headers inert.
  onSelectProvider?: (providerId: string) => void;
  // Docked next to the workspace tool rail; the engine column mirrors to
  // that edge so it reads as the rail's second column.
  fromRail?: boolean;
  width?: number;
  // Only a panel the user opened from its trigger takes focus; the handoff
  // and onboarding panels sit beside an input that keeps it.
  autoFocus?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const panelRef = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  // The panel stays open while a selection commits through the app-server
  // stream, so the highlighted row and the effort list follow the click
  // immediately instead of waiting for the round-trip. Once the stream
  // confirms, the external props become the source of truth again and the
  // optimistic state is dropped.
  const [optimistic, setOptimistic] = useState<{ provider: string; model: string; variant: string } | null>(null);
  useEffect(() => {
    setOptimistic(null);
  }, [selectedProvider, selectedModel, selectedVariant]);

  useFloatingMenuFocus(panelRef, "", autoFocus);

  const display = useComposerConnectionDisplay();
  const providers = initialized.providers ?? [];
  const groups: RuntimeModelGroup[] = providers.map(provider => {
    const catalog = runtimeModelsForProvider(provider, state);
    const configured = configuredRuntimeModelForProvider(provider, state);
    const models = configured && !catalog.some(model => model.id === configured.id) ? [configured, ...catalog] : catalog;
    return { provider, models: display.visibleModels(`provider:${provider.name}`, models, [provider.name === (optimistic?.provider ?? selectedProvider) ? optimistic?.model ?? selectedModel : ""]) };
  });

  const effectiveProviderName = optimistic?.provider ?? selectedProvider;
  const effectiveModelID = optimistic?.model ?? selectedModel;
  const effectiveVariant = optimistic?.variant ?? selectedVariant;
  const effectiveProvider = providers.find((provider) => provider.name === effectiveProviderName);
  const scopedGroups = groups.filter((group) => group.provider.name === effectiveProviderName);

  // Every provider's models list together under group headers; a filter
  // matches provider names as well as model names so a prefix narrows to one
  // group without hiding the rest of the column behind navigation.
  const activeFilter = (query || filterQuery).trim().toLocaleLowerCase();
  const filteredGroups = activeFilter
    ? groups
        .map((group) => ({
          ...group,
          models: group.models.filter(
            (model) =>
              providerModelDisplayName(model).toLocaleLowerCase().includes(activeFilter) ||
              model.id.toLocaleLowerCase().includes(activeFilter) ||
              (group.provider.name.toLocaleLowerCase().includes(activeFilter) || display.label(`provider:${group.provider.name}`, group.provider.name).toLocaleLowerCase().includes(activeFilter))
          )
        }))
        .filter((group) => group.models.length > 0)
    : groups;

  const effectiveCodex = providerIsCodex(initialized, effectiveProviderName);
  const effortOptions = providerModelVariantOptions(effectiveProvider, effectiveModelID, effectiveVariant);
  const effectiveModel = scopedGroups
    .flatMap((group) => group.models)
    .find((model) => model.id === effectiveModelID);
  const visibleModels = filteredGroups.flatMap((group) => group.models.map((model) => ({ provider: group.provider, model })));
  const quickIndex = new Map(visibleModels.map((entry, index) => [`${entry.provider.name}/${entry.model.id}`, index]));

  // An unreadable Codex login still leaves the configured provider models
  // selectable, so like external catalog failures it reports through the
  // notice queue rather than a banner inside the list.
  const codexLoadError = effectiveCodex ? state.error.trim() : "";
  useEffect(() => {
    if (!codexLoadError) return;
    const summary = t("runtime.codexLoginUnavailable");
    const detail = codexLoadError.split("\n").map(line => line.trim()).find(Boolean);
    showToast({
      message: detail ? `${summary}\n${detail}` : summary,
      tone: "error",
      dedupeKey: `codex-models:${codexLoadError}`,
    });
  }, [codexLoadError, t]);

  const selectModel = (provider: string, model: string, variant?: string): void => {
    setOptimistic({ provider, model, variant: variant ?? "" });
    void Promise.resolve(onSelectModel(provider, model, variant)).then((committed) => {
      if (committed === false) setOptimistic(null);
    });
  };
  const selectEntry = (entry: { provider: ProviderSummary; model: ProviderModelSummary }): void => {
    const selected = entry.provider.name === effectiveProviderName && entry.model.id === effectiveModelID;
    selectModel(
      entry.provider.name,
      entry.model.id,
      selected ? effectiveVariant : rememberedVariantForRuntimeModel(entry.provider, entry.model),
    );
  };
  const quickSelect = (index: number): void => {
    const entry = visibleModels[index];
    if (entry) selectEntry(entry);
  };
  const effortLabel = effectiveModel ? providerModelDisplayName(effectiveModel) : effectiveModelID || t("runtime.selectModel");

  return (
    <div
      ref={panelRef}
      className={`codex-runtime-menu codex-model-menu runtime-panel runtime-flat-panel${fromRail ? " from-rail" : ""}`}
      role="menu"
      style={{ "--runtime-panel-width": `${width ?? runtimePanelWidth()}px` } as CSSProperties}
      onKeyDown={(event) => handleRuntimePanelKeyDown(event, quickSelect)}
    >
      <div className="runtime-flat">
        {engineOptions.length > 1 ? (
          <RuntimeEngineRail
            options={engineOptions}
            selected={selectedEngine}
            locked={engineLocked}
            running={running}
            lockedDescription={lockedDescription}
            onSelect={(id) => { if (id !== selectedEngine) onSelectEngine(id); }}
          />
        ) : null}
        <div className="runtime-flat-main">
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
                selectEntry(first);
              }}
            />
          </label>
          <div className="codex-model-groups runtime-flat-models">
            {effectiveCodex && state.loading ? <div className="composer-menu-empty">{t("runtime.loadingCodexModels")}</div> : null}
            {!state.loading && providers.length === 0 ? <div className="composer-menu-empty">{t("runtime.noModels")}</div> : null}
            {filteredGroups.length === 0 && providers.length > 0 ? <div className="composer-menu-empty">{t("runtime.noMatchingModels")}</div> : null}
            {filteredGroups.map((group) => {
              const providerName = display.label(`provider:${group.provider.name}`, group.provider.name);
              const header = (
                <>
                  <ProviderMark id={group.provider.catalog_id ?? group.provider.type} label={providerName} />
                  <span>{providerName}</span>
                </>
              );
              return (
                <div className="codex-model-group" key={group.provider.name}>
                  {onSelectProvider ? (
                    <button
                      type="button"
                      className="runtime-provider-head is-action"
                      onClick={() => onSelectProvider(group.provider.name)}
                    >
                      {header}
                    </button>
                  ) : (
                    <div className="runtime-provider-head">{header}</div>
                  )}
                  {group.models.map((model) => {
                    const selected = group.provider.name === effectiveProviderName && model.id === effectiveModelID;
                    return (
                      <RuntimeModelRow
                        key={`${group.provider.name}/${model.id}`}
                        name={providerModelDisplayName(model)}
                        checked={selected}
                        quickIndex={quickIndex.get(`${group.provider.name}/${model.id}`)}
                        onSelect={() => {
                          selectModel(
                            group.provider.name,
                            model.id,
                            selected ? effectiveVariant : rememberedVariantForRuntimeModel(group.provider, model),
                          );
                        }}
                      />
                    );
                  })}
                </div>
              );
            })}
          </div>
        </div>
        {effortOptions.length > 1 ? (
          <div className="runtime-flat-side">
            <RuntimeEffortColumn
              model={effortLabel}
              options={effortOptions}
              selected={effectiveVariant}
              defaultId={effectiveProvider && effectiveModel ? defaultVariantForRuntimeModel(effectiveProvider, effectiveModel) : ""}
              disabled={false}
              busy={false}
              onSelect={(variant) => {
                setOptimistic((current) =>
                  current ? { ...current, variant } : { provider: selectedProvider, model: selectedModel, variant }
                );
                void Promise.resolve(onSelectEffort(variant)).then((committed) => {
                  if (committed === false) setOptimistic(null);
                });
              }}
            />
          </div>
        ) : null}
      </div>
      {effectiveModel?.fast_mode && onSelectSpeed ? (
        <div className="runtime-flat-footer">
          <RuntimeSpeedControl
            speed={initialized.speed}
            defaultSpeed={effectiveModel.default_speed}
            disabled={running}
            onSelectSpeed={onSelectSpeed}
          />
        </div>
      ) : null}
    </div>
  );
}

type RuntimeModelGroup = {
  provider: ProviderSummary;
  models: ProviderModelSummary[];
};

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
  if (!provider.model || isChatGPTProvider(provider)) {
    return undefined;
  }
  return (
    runtimeModelsForProvider(provider, state).find((model) => model.id === provider.model) ??
    provider.models?.find((model) => model.id === provider.model) ?? { id: provider.model, source: "selected" }
  );
}

function runtimeModelsForProvider(provider: ProviderSummary, _state: CodexModelLoadState): ProviderModelSummary[] {
  if (isChatGPTProvider(provider)) return chatGPTConnectionModels(provider);
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
// (built-in actions, prompts, skills, and conversation plugins).
// The @ trigger shares this panel and inserts at the active reference range.
export function ComposerPlusButton({
  variant,
  disabled,
  commands,
  pluginMenu,
  menuAnchorRef,
  onAddAttachment,
  mobileAttachments,
  onSelectCommand
}: {
  variant: ComposerVariant;
  disabled: boolean;
  commands: ComposerSlashCommand[];
  pluginMenu?: ComposerPluginMenu;
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
  const [localOpen, setLocalOpen] = useState(false);
  const open = pluginMenu?.open ?? localOpen;
  const setOpen = (next: boolean) => { if (pluginMenu) pluginMenu.setOpen(next); else setLocalOpen(next); };
  const close = () => { if (pluginMenu) pluginMenu.close(); else setLocalOpen(false); };
  const [openedFromEnd, setOpenedFromEnd] = useState(false);
  useFloatingMenuFocus(menuRef, "", open && !pluginMenu?.fromMention, openedFromEnd);
  const preferredWidth = pluginMenu?.available ? 400 : COMPOSER_COMMAND_MENU_WIDTH;
  const menuWidth = open ? composerMenuWidth(preferredWidth) : preferredWidth;
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
      if (triggerRef.current?.contains(target) || pluginMenu?.inputElement()?.contains(target)) {
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
    document.addEventListener("pointerdown", handlePointerDown, true);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown, true);
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
        disabled={disabled}
        onPointerDown={(event) => { if (isTouchWebShell()) event.preventDefault(); }}
        onClick={() => {
          setOpenedFromEnd(false);
          setOpen(!open);
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
          offset={variant === "hero" ? 10 : 8}
          width={menuWidth}
          matchAnchorWidth
          mobileSheet={{ label: t("composer.plusMenu"), onClose: () => setOpen(false) }}
        >
          <div
            ref={menuRef}
            className="composer-context-menu composer-plus-menu"
            id={pluginMenu?.id}
            role="menu"
            aria-label={t("composer.plusMenu")}
            style={{ "--composer-menu-width": `${menuWidth}px` } as CSSProperties}
            onKeyDown={(event) => handleFloatingMenuKeyDown(event, close, pluginMenu?.fromMention ? pluginMenu.inputElement() : buttonRef.current)}
          >
            <div className="composer-menu-scroll scrollbar-hidden" data-scroll-fade="compact">
              {pluginMenu ? <ComposerPluginChoices menu={pluginMenu} /> : null}
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
                </button>
              )}
              <PlusMenuCommands label={t("composer.plusSectionCommands")} commands={builtInCommands.filter(command => !pluginMenu?.query || `${command.title} ${command.name}`.toLocaleLowerCase().includes(pluginMenu.query.toLocaleLowerCase()))} onSelect={selectCommand} />
              <PlusMenuCommands label={t("composer.plusSectionSkills")} commands={skillCommands.filter(command => !pluginMenu?.query || `${command.title} ${command.name}`.toLocaleLowerCase().includes(pluginMenu.query.toLocaleLowerCase()))} onSelect={selectCommand} />
            </div>
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

// One group of the plus menu. Rows come from the same ComposerCommandRow as
// the "/" panel so a command reads identically on both surfaces.
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
        <ComposerCommandRow key={command.id} command={command} role="menuitem" onSelect={onSelect} />
      ))}
    </>
  );
}

// One command row, shared by the "/" suggestion panel and the "+"/"@" menu so
// the same command reads identically on both surfaces: the icon, the title (or
// the skill's name, which is what one types), and secondary copy trailing at
// the row's end like a shortcut — the slash command, the skill's summary, or
// the reason the row is disabled.
export function ComposerCommandRow({
  command,
  id,
  role,
  className,
  selected,
  onHover,
  onSelect
}: {
  command: ComposerSlashCommand;
  id?: string;
  role: "option" | "menuitem";
  className?: string;
  selected?: boolean;
  onHover?(): void;
  onSelect(command: ComposerSlashCommand): void;
}): JSX.Element {
  return (
    <button
      className={className}
      data-command-name={command.name}
      id={id}
      role={role}
      type="button"
      aria-selected={role === "option" ? selected : undefined}
      disabled={Boolean(command.disabledReason)}
      onMouseEnter={command.disabledReason ? undefined : onHover}
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => onSelect(command)}
    >
      <SlashCommandIcon command={command} />
      <span className="composer-plus-menu-item-title slash-command-title">
        {command.kind === "skill" ? command.name : command.title}
      </span>
      <span
        className={`composer-plus-menu-item-desc ${
          command.disabledReason
            ? "slash-command-meta"
            : command.kind === "skill"
              ? "slash-command-summary"
              : "slash-command-command"
        }`}
      >
        {command.disabledReason ??
          (command.kind === "skill" ? command.description : `/${command.name}`)}
      </span>
    </button>
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
  onCreateWorkspace,
  onOpenWorkspace,
  onKeyDown,
}: {
  projects: ComposerWorkspace[];
  activeContext?: RuntimeContext;
  query: string;
  setQuery: (value: string) => void;
  onSelectWorkspace: (id: string) => void;
  onCreateWorkspace: () => void;
  onOpenWorkspace: () => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t } = useI18n();
  const menuRef = useRef<HTMLDivElement>(null);
  useFloatingMenuFocus(menuRef);
  const services = useComposerServices();
  const capabilities = services?.view.workspaceCapabilities;
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredWorkspaces = normalizedQuery
    ? projects.filter((project) => project.name.toLocaleLowerCase().includes(normalizedQuery) || project.path.toLocaleLowerCase().includes(normalizedQuery))
    : projects;
  return (
    <div ref={menuRef} className="composer-project-menu" role="menu" onKeyDown={onKeyDown}
      style={{ "--composer-project-menu-width": `${composerMenuWidth(COMPOSER_PROJECT_MENU_WIDTH)}px` } as CSSProperties}>
      <label className="menu-search project-search">
        <Search className="icon-sm" aria-hidden="true" />
        <input value={query} aria-label={t("runtime.searchWorkspaces")} placeholder={t("runtime.searchWorkspaces")}
          onChange={(event) => setQuery(event.target.value)} />
      </label>
      <div className="project-picker-list">
        {filteredWorkspaces.map((project) => {
          const selected = activeContext?.kind === "project" && activeContext.project_id === project.id;
          return (
            <button key={project.id} type="button" role="menuitemradio" aria-label={project.name} aria-checked={selected} onClick={() => onSelectWorkspace(project.id)}>
              <Folder />
              <TruncatedText text={project.name} />
              {selected ? <Check /> : null}
            </button>
          );
        })}
        {filteredWorkspaces.length === 0 ? <div className="project-picker-empty">{t("runtime.noMatchingWorkspaces")}</div> : null}
      </div>
      <div className="project-picker-divider" />
      <button type="button" role="menuitem" disabled={!(capabilities?.open ?? hostSupports("chooseProjectFolder"))} onClick={onOpenWorkspace}>
        <FolderOpen />
        <span>{t("runtime.openFolder")}</span>
      </button>
      <button type="button" role="menuitem" disabled={!(capabilities?.create ?? hostSupports("createBlankProject"))} onClick={onCreateWorkspace}>
        <FolderPlus />
        <span>{t("runtime.createBlankWorkspace")}</span>
      </button>
    </div>
  );
}
