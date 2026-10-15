import { useEffect, useState } from "react";
import type { DesktopUpdateState } from "../shared/protocol";
import { Download, RefreshCw, RotateCw } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";
import "./DesktopUpdateButton.css";

export function DesktopUpdateButton(): React.JSX.Element | null {
  const { t } = useI18n();
  const [state, setState] = useState<DesktopUpdateState>({ kind: "unsupported" });
  const [pending, setPending] = useState(false);
  const [requestFailed, setRequestFailed] = useState(false);
  useEffect(() => {
    const api = window.wuu;
    if (!api?.getDesktopUpdateState || !api.onDesktopUpdateChanged) return;
    let active = true;
    let receivedEvent = false;
    const unsubscribe = api.onDesktopUpdateChanged(next => {
      receivedEvent = true;
      if (active) { setState(next); setRequestFailed(false); }
    });
    void api.getDesktopUpdateState().then(next => {
      // An in-flight snapshot must not overwrite a newer progress event.
      if (active && !receivedEvent) setState(next);
    }).catch(error => console.error("Could not read desktop update state", error));
    return () => { active = false; unsubscribe(); };
  }, []);

  if (state.kind === "unsupported" || state.kind === "idle") return null;
  const version = "version" in state ? state.version : "";
  const downloading = state.kind === "downloading";
  const busy = pending || downloading || state.kind === "checking" || state.kind === "prompting" || state.kind === "installing";
  const label = requestFailed ? t("desktopUpdate.actionFailed")
    : state.kind === "available" ? t("desktopUpdate.available", { version })
    : state.kind === "ready" ? t("desktopUpdate.ready", { version })
    : state.kind === "error" ? t("desktopUpdate.failed")
    : downloading ? t("desktopUpdate.downloading", { percent: state.percent })
    : state.kind === "installing" ? t("desktopUpdate.installing")
    : state.kind === "prompting" ? t("desktopUpdate.prompting")
    : t("desktopUpdate.checking");
  const activate = async () => {
    if (busy) return;
    setPending(true);
    setRequestFailed(false);
    try {
      if (state.kind === "ready") await window.wuu.restartDesktopUpdate!();
      else await window.wuu.downloadDesktopUpdate!();
    } catch (error) {
      console.error("Desktop update action failed", error);
      setRequestFailed(true);
    } finally { setPending(false); }
  };

  return <div className="desktop-update" data-state={state.kind}>
    <Tooltip content={label}>
      <button type="button" className="desktop-update-button" aria-label={label}
        aria-disabled={busy} onClick={() => void activate()}>
        <span className="desktop-update-glyph" key={state.kind} aria-hidden="true">
          {downloading ? <>
            <svg className="desktop-update-ring" viewBox="0 0 28 28" fill="none">
              <circle className="desktop-update-track" cx="14" cy="14" r="11" />
              <circle className="desktop-update-progress" cx="14" cy="14" r="11" pathLength="100"
                strokeDasharray="100" strokeDashoffset={100 - state.percent} />
            </svg>
            <Download className="desktop-update-download" />
          </> : state.kind === "ready" || state.kind === "installing" ? <RotateCw />
            : state.kind === "checking" ? <RefreshCw /> : <Download />}
        </span>
      </button>
    </Tooltip>
    {downloading && <span className="sr-only" role="progressbar" aria-label={t("desktopUpdate.progress")}
      aria-valuemin={0} aria-valuemax={100} aria-valuenow={state.percent} />}
    <span className="sr-only" role="status">{downloading ? t("desktopUpdate.progress") : label}</span>
  </div>;
}
