import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { SystemPermissionGuideAPI, SystemPermissionGuideConfig } from "../shared/systemPermissionGuide";
import { ArrowUp, X } from "./WuuIcons";
import { translate } from "./i18n";
import { startAppearanceSync } from "./AppearancePreferences";
import "./styles.css";

declare global {
  interface Window { wuuPermissionGuide: SystemPermissionGuideAPI }
}

function PermissionGuide({ config }: { config: SystemPermissionGuideConfig }): JSX.Element {
  const [error, setError] = useState(false);
  const content = useRef<HTMLElement>(null);
  useEffect(() => {
    const observer = new ResizeObserver(([entry]) => window.wuuPermissionGuide.resize(entry.target.getBoundingClientRect().height));
    observer.observe(content.current!);
    return () => observer.disconnect();
  }, []);
  const t = (key: Parameters<typeof translate>[1], values?: Record<string, string>) => translate(config.locale, key, values);
  useEffect(() => window.wuuPermissionGuide.onError(() => setError(true)), []);
  return (
    <main ref={content} className="permission-guide">
      <header>
        <ArrowUp className="icon" aria-hidden="true" />
        <p>{t("permissions.dragHint", { permission: t(`permissions.${config.permission}`) })}</p>
        <button className="settings-button settings-button-ghost settings-icon-button" aria-label={t("permissions.closeGuide")}
          onClick={() => window.wuuPermissionGuide.close()}><X className="icon" aria-hidden="true" /></button>
      </header>
      <button className="permission-guide-app" draggable
        onDragStart={event => { event.preventDefault(); setError(false); window.wuuPermissionGuide.drag(); }}
        onClick={() => window.wuuPermissionGuide.reveal()} aria-describedby="permission-guide-help">
        <img src={config.appIcon} alt="" draggable={false} />
        <span>{config.appName}</span>
      </button>
      <p id="permission-guide-help" className="permission-guide-help">{t("permissions.dragFallback")}</p>
      {error ? <p className="settings-error" role="alert">{t("permissions.guideError")}</p> : null}
    </main>
  );
}

void window.wuuPermissionGuide.config().then(config => {
  document.documentElement.dataset.theme = config.theme;
  document.documentElement.lang = config.locale;
  document.documentElement.style.setProperty("--conversation-message-font-size", `${config.fontSize}px`);
  document.body.classList.add("permission-guide-window");
  startAppearanceSync();
  createRoot(document.getElementById("root")!).render(<PermissionGuide config={config} />);
});
