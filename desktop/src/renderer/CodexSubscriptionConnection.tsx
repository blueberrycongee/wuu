import { useState } from "react";
import type { ProviderSummary } from "../shared/protocol";
import { Check, Plug, LoaderCircle, RefreshCw } from "./WuuIcons";
import { EngineAuthentication } from "./EngineAuthentication";
import { SettingsRow } from "./SettingsRow";
import { toastErrorMessage } from "./Toast";
import { useI18n } from "./i18n";

/** Connects the Wuu loop to subscription credentials, without changing engines. */
export function CodexSubscriptionConnection({
  provider,
  disabled = false,
  preview = false,
  onConnected,
  onPendingChange,
}: {
  provider: ProviderSummary;
  disabled?: boolean;
  preview?: boolean;
  onConnected: () => Promise<void>;
  onPendingChange?: (pending: boolean) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const connected = Boolean(provider.api_key_configured);
  const local = provider.reuse_codex_credentials;

  async function connect(): Promise<void> {
    if (pending || disabled) return;
    setPending(true);
    onPendingChange?.(true);
    setError("");
    try {
      if (!preview) {
        await window.wuu.useCodexCredentials(provider.name);
        await window.wuu.loadCodexModels(provider.name);
      }
      await onConnected();
    } catch (reason) {
      setError(toastErrorMessage(reason));
    } finally {
      setPending(false);
      onPendingChange?.(false);
    }
  }

  return <>
    <SettingsRow title={t("provider.chatgptLogin")}>
      {connected && !error && !pending ? <span className="model-subscription-status" role="status">
        <Check className="icon-sm" aria-hidden="true" />
        {provider.codex_credential_source === "explicit" ? t("provider.apiKey") : local ? "Codex CLI" : "Wuu"}
      </span> : null}
      <button
        type="button"
        className={`settings-button${connected && local ? " settings-button-ghost settings-icon-button" : ""}`}
        data-testid="codex-subscription-connect"
        disabled={disabled || pending}
        aria-busy={pending}
        aria-label={t(connected && local ? "settings.codexCheckLogin" : "settings.codexUseLocal")}
        title={t(connected && local ? "settings.codexCheckLogin" : "settings.codexUseLocal")}
        onClick={() => void connect()}
      >
        {pending ? <LoaderCircle className="icon settings-spin" aria-hidden="true" />
          : connected && local ? <RefreshCw className="icon" aria-hidden="true" /> : <Plug className="icon" aria-hidden="true" />}
        {connected && local ? null : t("settings.codexUseLocal")}
      </button>
    </SettingsRow>
    {!connected && !provider.codex_credential_source ? <EngineAuthentication engineID="codex" protocol="codex" compact /> : null}
    {error ? <details className="model-subscription-error settings-error" role="alert">
      <summary>{t("provider.connectionFailed")}</summary>
      <p>{error}</p>
    </details> : null}
  </>;
}
