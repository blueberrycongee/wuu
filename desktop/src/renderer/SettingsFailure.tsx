import { MCPDiagnostic } from "./MCPDiagnostic";
import { AlertCircle } from "./WuuIcons";
import { useI18n } from "./i18n";

/** Keep a blocking settings failure beside its controls, with diagnostics on demand. */
export function SettingsFailure({ message, diagnostic, onRetry, retrying = false }: {
  message: string;
  diagnostic?: string;
  onRetry?: () => void;
  retrying?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  return <div className="settings-failure">
    <div className="settings-failure-summary">
      <p role="alert"><AlertCircle className="icon-sm" aria-hidden="true" />{message}</p>
      {onRetry ? <button type="button" className="settings-button settings-button-ghost" data-testid="settings-error-retry"
        disabled={retrying} aria-busy={retrying} onClick={onRetry}>{t("settings.errorRetry")}</button> : null}
    </div>
    {diagnostic && diagnostic !== message ? <MCPDiagnostic key={diagnostic} message={diagnostic} /> : null}
  </div>;
}
