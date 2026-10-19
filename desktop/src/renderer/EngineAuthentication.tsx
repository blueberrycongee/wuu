import { useEffect, useRef, useState } from "react";
import type { EngineAuthResult } from "../shared/protocol";
import { GrokLogin } from "./GrokLogin";
import { useI18n } from "./i18n";

/** Discovery never starts login. Credentials stay with the installed engine. */
export function EngineAuthentication({ engineID, protocol, compact = false, disabled = false, entry = false, onAuthenticated, onReuseLocal }: {
  engineID: string;
  protocol?: string;
  compact?: boolean;
  disabled?: boolean;
  entry?: boolean;
  onAuthenticated?: () => void | Promise<void>;
  onReuseLocal: () => void | Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const [methods, setMethods] = useState<EngineAuthResult["methods"]>();
  const [busy, setBusy] = useState<"auth" | "local" | null>(null);
  const [error, setError] = useState("");
  const operation = useRef<{ kind: "auth" | "local"; cancelled: boolean } | undefined>(undefined);
  useEffect(() => {
    setExpanded(false);
    setMethods(undefined);
    setBusy(null);
    setError("");
    return () => {
      const current = operation.current;
      operation.current = undefined;
      if (current?.kind === "auth") void window.wuu.cancelEngineAuth(engineID).catch(() => {});
    };
  }, [engineID]);

  async function run(method?: string): Promise<void> {
    if (disabled || operation.current) return;
    const current = { kind: "auth" as "auth" | "local", cancelled: false };
    operation.current = current;
    setBusy("auth");
    setError("");
    setMethods(undefined);
    try {
      const next = method
        ? await window.wuu.authenticateEngine(engineID, method)
        : await window.wuu.listEngineAuthMethods(engineID);
      if (operation.current !== current || current.cancelled) return;
      setMethods(next.methods.filter(choice => choice.id && (!choice.type || choice.type === "agent")));
      // An ACP acknowledgment need not prove a provider account is signed in.
      // Refresh observed account evidence rather than manufacturing that state.
      if (method && next.authenticated) {
        current.kind = "local";
        setBusy("local");
        setMethods(undefined);
        await onAuthenticated?.();
      }
    } catch (e) {
      if (operation.current === current && !current.cancelled) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (operation.current === current) {
        operation.current = undefined;
        setBusy(null);
      }
    }
  }

  async function reuseLocal(): Promise<void> {
    if (disabled || operation.current) return;
    const current = { kind: "local" as const, cancelled: false };
    operation.current = current;
    setBusy("local");
    setError("");
    setMethods(undefined);
    try {
      await onReuseLocal();
    } catch (e) {
      if (operation.current === current) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (operation.current === current) {
        operation.current = undefined;
        setBusy(null);
      }
    }
  }

  async function cancel(): Promise<void> {
    const current = operation.current;
    if (!current || current.kind !== "auth") return;
    current.cancelled = true;
    try {
      await window.wuu.cancelEngineAuth(engineID);
      if (operation.current === current) {
        operation.current = undefined;
        setBusy(null);
        setMethods(undefined);
      }
    } catch (e) {
      // A failed cancellation must not admit another request over the first.
      if (operation.current === current) setError(e instanceof Error ? e.message : String(e));
    }
  }

  const canSignIn = protocol === "acp" && !["dsh", "hermes", "pi"].includes(engineID);
  const cliCommand = engineID === "codex" ? "codex login" : engineID === "claude" ? "claude auth login" : engineID === "opencode" ? "opencode auth login" : undefined;
  const controls = (
    <div className={`settings-engine-auth${compact ? " settings-engine-auth-compact" : ""}`} aria-busy={Boolean(busy)}>
      {engineID === "grok" ? <GrokLogin nested={entry} disabled={disabled} onBusyChange={value => setBusy(value ? "auth" : null)} onAuthenticated={async () => { await onAuthenticated?.(); }} onReuseLocal={onReuseLocal} /> : <>
        <div className="settings-engine-auth-actions">
          {canSignIn ? <button className="settings-button" type="button" disabled={disabled || Boolean(busy)} data-testid="engine-auth-discover" onClick={() => void run()}>
            {t("provider.authSignIn")}
          </button> : null}
          <button className="settings-button" type="button" disabled={disabled || Boolean(busy)} data-testid="engine-auth-local" onClick={() => void reuseLocal()}>
            {t(entry ? "provider.authCheckLocal" : "provider.authUseLocal")}
          </button>
          {busy === "auth" ? <button className="settings-button" type="button" data-testid="engine-auth-cancel" onClick={() => void cancel()}>{t("common.cancel")}</button> : null}
          {methods?.map((method) => (
            <button className="settings-button" type="button" key={method.id} title={method.description} disabled={disabled || Boolean(busy)} data-testid="engine-auth-method" onClick={() => void run(method.id)}>
              {method.name}
            </button>
          ))}
        </div>
        <small className="settings-muted-line settings-engine-detail" role="status">
          {error ? compact ? t("settings.engineLoginFailed") : error
            : busy ? t(busy === "local" ? "settings.loading" : "settings.engineSigningIn")
              : cliCommand ? t("provider.authEngineCliCommand", { command: cliCommand })
                : !canSignIn || methods?.length === 0 ? t("settings.engineCLILogin")
                  : methods ? t("settings.engineLoginMethodHint") : t("provider.authEngineSource")}
        </small>
      </>}
    </div>
  );
  if (!entry) return controls;
  return <div className="connection-credential-entry">
    <button type="button" className="settings-button" data-testid="engine-auth-entry"
      aria-expanded={expanded} disabled={disabled || Boolean(busy)} onClick={() => setExpanded(value => !value)}>
      {t("connection.credentials")}
    </button>
    {expanded ? <div className="connection-credential-menu">{controls}</div> : null}
  </div>;
}
