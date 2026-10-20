import { useEffect, useRef, useState } from "react";
import { useI18n } from "./i18n";

/** The installed CLI owns browser authorization and its credential file. */
export function GrokLogin({ disabled = false, nested = false, onAuthenticated, onBusyChange, onReuseLocal }: {
  disabled?: boolean;
  nested?: boolean;
  onAuthenticated: () => Promise<void>;
  onBusyChange?: (busy: boolean) => void;
  onReuseLocal?: () => void | Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [state, setState] = useState<"idle" | "running" | "checking" | "done" | "failed" | "local-failed">("idle");
  const operation = useRef<{ cancelled: boolean; processDone: boolean } | undefined>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (operation.current) {
        const current = operation.current;
        operation.current = undefined;
        if (!current.processDone) void window.wuu.cancelEngineAuth("grok").catch(() => {});
        onBusyChange?.(false);
      }
    };
  }, []);

  async function start(local = false): Promise<void> {
    if (disabled || operation.current || window.wuu.hostKind === "web") return;
    const current = { cancelled: false, processDone: local };
    operation.current = current;
    setState(local ? "checking" : "running"); onBusyChange?.(true);
    try {
      if (local) {
        await onReuseLocal?.();
        if (mounted.current && operation.current === current) setState("idle");
        return;
      }
      const result = await window.wuu.authenticateEngine("grok", "local-cli");
      current.processDone = true;
      if (!mounted.current || operation.current !== current || current.cancelled) return;
      if (!result.authenticated) throw new Error("Login incomplete");
      await onAuthenticated();
      if (mounted.current && operation.current === current && !current.cancelled) setState("done");
    } catch {
      if (mounted.current && operation.current === current && !current.cancelled) setState(local ? "local-failed" : "failed");
    } finally {
      if (operation.current === current) {
        operation.current = undefined;
        if (mounted.current && current.cancelled) setState("idle");
        onBusyChange?.(false);
      }
    }
  }
  async function cancel(): Promise<void> {
    const current = operation.current;
    if (!current) return;
    current.cancelled = true;
    try {
      await window.wuu.cancelEngineAuth("grok");
      if (mounted.current && operation.current === current) {
        operation.current = undefined;
        setState("idle"); onBusyChange?.(false);
      }
    } catch {
      // Keep the operation locked until its original promise settles.
      if (mounted.current) setState("failed");
    }
  }
  if (window.wuu.hostKind === "web") return <span className="settings-muted-line">{t("provider.grokDesktopLogin")}</span>;
  const busy = Boolean(operation.current);
  return <span className="connection-login" aria-busy={busy}>
    <button type="button" className="settings-button settings-button-primary" data-testid="grok-login-start" disabled={disabled || busy} onClick={() => void start()}>{t("provider.authSignIn")}</button>
    {onReuseLocal ? <button type="button" className="settings-button" data-testid="grok-login-local" disabled={disabled || busy} onClick={() => void start(true)}>{t(nested ? "provider.authCheckLocal" : "provider.authUseLocal")}</button> : null}
    {busy && !operation.current?.processDone ? <button type="button" className="settings-button settings-button-ghost" data-testid="grok-login-cancel" onClick={() => void cancel()}>{t("common.cancel")}</button> : null}
    {state !== "idle" ? <span className="connection-login-feedback" role={state === "failed" || state === "local-failed" ? "alert" : "status"}>{t(state === "local-failed" ? "provider.authLocalUnavailable" : state === "checking" ? "settings.loading" : state === "running" ? "provider.grokLoginWaiting" : state === "done" ? "provider.grokLoginComplete" : "provider.grokLoginFailed")}</span> : null}
  </span>;
}
