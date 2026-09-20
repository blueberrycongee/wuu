import { useEffect, useRef, useState } from "react";
import type { EngineAuthResult } from "../shared/protocol";
import { useI18n } from "./i18n";

/** Discovery never starts login. Only an explicit method selection does. */
export function EngineAuthentication({ engineID }: { engineID: string }): JSX.Element {
  const { t } = useI18n();
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
      if (current === generation.current) setResult(next);
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

  return (
    <div className="settings-engine-auth" aria-busy={busy}>
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
      <small className="settings-muted-line settings-engine-detail" role="status">
        {error || (busy ? t("settings.engineSigningIn") : result?.authenticated ? t("settings.engineSignedIn") : result?.methods.length === 0 ? t("settings.engineCLILogin") : t("settings.engineLoginHint"))}
      </small>
    </div>
  );
}
