import { useI18n } from "../i18n";
import "./SurfaceUnavailable.css";

/** A selected surface stays visibly unavailable; it never selects another provider. */
export function SurfaceUnavailable({ onRetry, fullscreen = false }: { onRetry?: () => void; fullscreen?: boolean }): JSX.Element {
  const { t } = useI18n();
  return <section className={`surface-unavailable${fullscreen ? " surface-unavailable-fullscreen" : ""}`} role="status">
    <p>{t("plugins.surfaceUnavailable")}</p>
    {onRetry && <button type="button" className="settings-button" onClick={onRetry}>{t("plugins.retrySurface")}</button>}
  </section>;
}
