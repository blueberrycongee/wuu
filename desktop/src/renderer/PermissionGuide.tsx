import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { SystemPermissionGuideAPI, SystemPermissionGuideConfig } from "../shared/systemPermissionGuide";
import { AlertCircle, ArrowUp, ChevronLeft } from "./WuuIcons";
import { SystemPermissionRow } from "./SystemPermissions";
import { translate } from "./i18n";
import { applyAppearance, readAppearance, startAppearanceSync } from "./AppearancePreferences";
import { startFocusModality } from "./FocusModality";
import { useReducedMotion } from "./motion";
import "./styles.css";

declare global {
  interface Window { wuuPermissionGuide: SystemPermissionGuideAPI }
}

function PermissionGuide(): JSX.Element | null {
  const [config, setConfig] = useState<SystemPermissionGuideConfig>();
  const [error, setError] = useState(false);
  const compact = useRef<HTMLDivElement>(null);
  const reducedMotion = useReducedMotion();
  const hasConfig = config !== undefined;

  useEffect(() => {
    let active = true;
    let receivedConfig = false;
    const stopConfig = window.wuuPermissionGuide.onConfig(next => {
      receivedConfig = true;
      setConfig(next);
    });
    void window.wuuPermissionGuide.config().then(initial => {
      if (active && !receivedConfig) setConfig(initial);
    });
    const stopError = window.wuuPermissionGuide.onError(() => setError(true));
    return () => { active = false; stopConfig(); stopError(); };
  }, []);

  useLayoutEffect(() => {
    if (!config) return;
    document.documentElement.dataset.theme = config.theme;
    document.documentElement.lang = config.locale;
    document.documentElement.style.setProperty("--conversation-message-font-size", `${config.fontSize}px`);
    applyAppearance(readAppearance());
  }, [config?.theme, config?.locale, config?.fontSize]);

  useEffect(() => {
    if (!hasConfig) return;
    let paintedFrame = 0;
    const frame = requestAnimationFrame(() => {
      paintedFrame = requestAnimationFrame(() => window.wuuPermissionGuide.ready());
    });
    return () => { cancelAnimationFrame(frame); cancelAnimationFrame(paintedFrame); };
  }, [hasConfig]);

  useEffect(() => {
    if (!config || config.phase === "returning") return;
    const observer = new ResizeObserver(([entry]) => {
      window.wuuPermissionGuide.resize(entry.target.getBoundingClientRect().height);
    });
    observer.observe(compact.current!);
    return () => observer.disconnect();
  }, [config?.phase]);

  if (!config) return null;
  const t = (key: Parameters<typeof translate>[1], values?: Record<string, string>) => translate(config.locale, key, values);
  const showingRow = config.phase === "source" || config.phase === "returning";
  return (
    <main className="permission-guide" data-phase={config.phase} data-reduced-motion={config.reducedMotion || reducedMotion || undefined}>
      <div className="permission-guide-source" aria-hidden={!showingRow}>
        <SystemPermissionRow permission={config.permission} locale={config.locale} granted={config.granted}
          pending={config.phase === "source" || config.phase === "lifting"} disabled={config.phase === "source" || config.phase === "lifting"} />
      </div>
      <div ref={compact} className="permission-guide-compact" aria-hidden={showingRow}>
        <p id="permission-guide-help" className="permission-guide-instruction">
          <ArrowUp className="icon" aria-hidden="true" />
          <span>{t("permissions.dragHint", { app: config.appName, permission: t(`permissions.${config.permission}`) })}</span>
        </p>
        <div className="permission-guide-controls">
          <button className="settings-button settings-button-ghost settings-icon-button permission-guide-back" type="button"
            aria-label={t("permissions.closeGuide")} title={t("permissions.closeGuide")} tabIndex={showingRow ? -1 : 0}
            onClick={() => window.wuuPermissionGuide.close()}><ChevronLeft className="icon" aria-hidden="true" /></button>
          <button className="permission-guide-app" type="button" draggable title={t("permissions.dragFallback")} tabIndex={showingRow ? -1 : 0}
            onDragStart={event => { event.preventDefault(); setError(false); window.wuuPermissionGuide.drag(); }}
            onClick={() => window.wuuPermissionGuide.reveal()} aria-describedby="permission-guide-help permission-guide-fallback">
            <img src={config.appIcon} alt="" draggable={false} />
            <span>{config.appName}</span>
          </button>
          {error ? <p className="system-permission-feedback permission-guide-feedback" role="alert">
            <AlertCircle className="icon" aria-hidden="true" /><span>{t("permissions.guideError")}</span>
          </p> : null}
        </div>
        <span id="permission-guide-fallback" className="sr-only">{t("permissions.dragFallback")}</span>
      </div>
    </main>
  );
}

document.body.classList.add("permission-guide-window");
startAppearanceSync();
startFocusModality();
createRoot(document.getElementById("root")!).render(<PermissionGuide />);
