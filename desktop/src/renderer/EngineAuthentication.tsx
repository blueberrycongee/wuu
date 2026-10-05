import { useEffect, useRef, useState } from "react";
import type { EngineAuthResult } from "../shared/protocol";
import { Check, Copy } from "./WuuIcons";
import { SettingsRow } from "./SettingsRow";
import { showErrorToast } from "./Toast";
import { useI18n } from "./i18n";

/** Discovery never starts login. Only an explicit method selection does. */
export function EngineAuthentication({ engineID, protocol = "acp", binaryPath, compact = false, onAuthenticated }: {
  engineID: string;
  protocol?: string;
  binaryPath?: string;
  compact?: boolean;
  onAuthenticated?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const [result, setResult] = useState<EngineAuthResult>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const generation = useRef(0);
  useEffect(() => () => {
    generation.current++;
    if (pending.current) void window.wuu.cancelEngineAuth(engineID).catch(() => {});
  }, [engineID]);

  async function run(method?: string): Promise<void> {
    if (pending.current) return;
    const current = generation.current;
    pending.current = true;
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const next = method
        ? await window.wuu.authenticateEngine(engineID, method)
        : await window.wuu.listEngineAuthMethods(engineID);
      if (current === generation.current) {
        setResult(next);
        if (next.authenticated) onAuthenticated?.();
      }
    } catch (e) {
      if (current === generation.current) setError(e instanceof Error ? e.message : String(e));
    } finally {
      pending.current = false;
      if (current === generation.current) setBusy(false);
    }
  }

  async function cancel(): Promise<void> {
    try {
      await window.wuu.cancelEngineAuth(engineID);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  if (protocol !== "acp") {
    const argumentsByEngine: Record<string, string> = { codex: "login", claude: "auth login", opencode: "auth login" };
    const args = argumentsByEngine[engineID];
    if (!args) return <p className="settings-muted-line">{t("settings.engineCLILogin")}</p>;
    const binary = binaryPath ? "'" + binaryPath.replaceAll("'", "'\"'\"'") + "'" : engineID;
    const command = `${binary} ${args}`;
    return <SettingsRow title={t("settings.engineTerminalLogin")} block>
      <code className="settings-engine-login-command">{command}</code>
      <button type="button" className="settings-button settings-button-ghost settings-icon-button"
        data-testid="engine-login-command-copy"
        aria-label={t("settings.engineCopyLoginCommand")} title={t("settings.engineCopyLoginCommand")}
        onClick={() => void navigator.clipboard.writeText(command).then(() => setCopied(true)).catch((reason) => showErrorToast(reason, t("common.copyFailed")))}>
        {copied ? <Check className="icon" aria-hidden="true" /> : <Copy className="icon" aria-hidden="true" />}
      </button>
    </SettingsRow>;
  }

  return (
    <div className={`settings-engine-auth${compact ? " settings-engine-auth-compact" : ""}`} aria-busy={busy}>
      <div className="settings-engine-auth-actions">
        <button className="settings-button" type="button" disabled={busy} data-testid="engine-auth-discover" onClick={() => void run()}>
          {t("settings.engineSignIn")}
        </button>
        {busy ? <button className="settings-button" type="button" data-testid="engine-auth-cancel" onClick={() => void cancel()}>{t("common.cancel")}</button> : null}
        {result?.methods.map((method) => (
          <button className="settings-button" type="button" key={method.id} title={method.description} disabled={busy} data-testid="engine-auth-method" onClick={() => void run(method.id)}>
            {method.name}
          </button>
        ))}
      </div>
      {!compact || busy || result || error ? (
        <small className="settings-muted-line settings-engine-detail" role="status">
          {error ? compact ? t("settings.engineLoginFailed") : error : busy ? t("settings.engineSigningIn") : result?.authenticated ? t("settings.engineSignedIn") : result?.methods.length === 0 ? t("settings.engineCLILogin") : t(compact ? "settings.engineLoginMethodHint" : "settings.engineLoginHint")}
        </small>
      ) : null}
    </div>
  );
}
