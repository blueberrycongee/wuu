import { useEffect, useRef, useState } from "react";
import type { ProjectPresetConfig, ProjectPresetMode, ProjectPresetsConfig, ProviderSummary } from "../shared/protocol";
import { Check, ChevronDown, Settings } from "./WuuIcons";
import { FloatingMenuPortal, composerMenuWidth, handleFloatingMenuKeyDown, isInsideFloatingMenu, menuOpeningKey, useFloatingMenuFocus } from "./ComposerFloatingMenu";
import { PROJECT_PRESET_MODES } from "./ProjectPresetModes";
import { variantLabel } from "./RuntimeHelpers";
import { useI18n } from "./i18n";
import "./ProjectPresetPicker.css";

export function ProjectPresetPicker({ mode, presets, providers, snapshot, disabled, onChange, onConfigure }: {
  mode: ProjectPresetMode;
  presets?: ProjectPresetsConfig;
  providers?: ProviderSummary[];
  snapshot?: ProjectPresetConfig;
  disabled?: boolean;
  onChange?: (mode: ProjectPresetMode) => void;
  onConfigure: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const preset = snapshot ?? presets?.[mode];
  const roles = ["lead", "side", "worker"] as const;
  const incomplete = roles.some((role) => !preset?.[role]?.provider || !preset[role]?.model);
  useFloatingMenuFocus(menuRef, mode, open);
  useEffect(() => {
    if (!open) return;
    function dismiss(event: PointerEvent): void {
      if (event.target instanceof Node && !anchorRef.current?.contains(event.target) && !isInsideFloatingMenu(event.target, "project-preset")) setOpen(false);
    }
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);
  useEffect(() => { if (disabled) setOpen(false); }, [disabled]);

  return <div ref={anchorRef} className="project-preset-picker">
    <button ref={triggerRef} type="button" className="codex-runtime-trigger" data-testid="project-preset-picker"
      aria-label={t("projects.mode")} aria-haspopup="dialog" aria-expanded={open} disabled={disabled}
      onClick={() => setOpen(!open)} onKeyDown={(event) => {
        if (menuOpeningKey(event)) { event.preventDefault(); setOpen(true); }
        if (event.key === "Escape") setOpen(false);
      }}>
      <span>{mode}</span><ChevronDown className="icon" aria-hidden="true" />
    </button>
    {open && <FloatingMenuPortal anchorRef={anchorRef} owner="project-preset" placement="above" align="right"
      width={composerMenuWidth(360)} flip mobileSheet={{ label: t("projects.mode"), onClose: () => setOpen(false) }}>
      <div ref={menuRef} className="composer-context-menu project-preset-panel" role="dialog" aria-label={t("projects.mode")}
        data-testid="project-preset-panel" onKeyDown={(event) => handleFloatingMenuKeyDown(event, () => setOpen(false), triggerRef.current)}>
        <div className="project-preset-heading"><span>{t("projects.mode")}</span><strong>{mode}</strong></div>
        <dl className="project-preset-team">
          {roles.map((role) => {
            const selection = preset?.[role];
            const model = providers?.find((provider) => provider.name === selection?.provider)?.models?.find((model) => model.id === selection?.model);
            const label = t(role === "lead" ? "settings.projectLeadModel" : role === "side" ? "settings.projectSideModel" : "settings.projectWorkerModel");
            return <div key={role} data-testid={"project-preview-" + role}>
              <dt>{label}</dt>
              <dd>{selection?.provider && selection.model ? <>
                <span>{model?.display_name || selection.model}</span>
                <span className="project-preset-detail">{selection.provider} · {variantLabel(selection.variant || selection.effort || "")}</span>
              </> : <span className="project-preset-detail">{t("projects.notConfigured")}</span>}</dd>
            </div>;
          })}
        </dl>
        {snapshot ? <p className="project-preset-note">{t("projects.snapshotLocked")}</p> : <>
          {incomplete && <p className="project-preset-note" data-testid="project-preset-incomplete">{t("projects.configureTeam")}</p>}
          <div className="project-preset-options" role="group" aria-label={t("projects.mode")}>
            {PROJECT_PRESET_MODES.map((value) => <button key={value} type="button" aria-pressed={mode === value}
              data-testid={"project-mode-" + value} data-menu-autofocus={mode === value || undefined}
              onClick={() => onChange?.(value)}>
              <span className="project-preset-check">{mode === value && <Check aria-hidden="true" />}</span>
              <span className="project-preset-option-copy"><span>{value}</span><span className="project-preset-detail">{t(value === "low" ? "projects.modeLow" : value === "medium" ? "projects.modeMedium" : value === "high" ? "projects.modeHigh" : "projects.modeUltra")}</span></span>
            </button>)}
          </div>
        </>}
        <button type="button" className="project-preset-configure" onClick={() => { setOpen(false); onConfigure(); }}>
          <Settings aria-hidden="true" /><span>{t("projects.configureModes")}</span>
        </button>
      </div>
    </FloatingMenuPortal>}
  </div>;
}
