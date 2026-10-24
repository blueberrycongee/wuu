import { useEffect, useId, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { WuuIconMascot } from "./WuuIconMascot";
import { useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

export function RuntimeLoading({
  status, fullscreen = false, onRetry,
}: {
  status: string;
  fullscreen?: boolean;
  onRetry?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const isStarting = status === "connecting" || status === "opening";
  const descriptionId = useId();
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    setSlow(false);
    if (!isStarting) return;
    const timeout = window.setTimeout(() => setSlow(true), 12000);
    return () => window.clearTimeout(timeout);
  }, [isStarting, status]);
  return (
    <div className={`project-empty-pane${fullscreen ? " application-recovery" : ""}`} data-wuu-component="launch-view">
      <div className="wuu-launch" role={isStarting ? "status" : "alert"}
        aria-label={t(isStarting ? "loading.starting" : "loading.failed")}
        aria-describedby={!isStarting || slow ? descriptionId : undefined}>
        {isStarting && <div className="wuu-launch-glass" aria-hidden="true">
            <WuuIconMascot className="wuu-launch-mascot" />
        </div>}
        <div className="wuu-launch-message">
          <p className="wuu-launch-title">{t(isStarting ? "loading.starting" : "loading.failed")}</p>
          {isStarting ? <>
            <div className="loading-rail" aria-hidden="true" />
            <p id={descriptionId} className="wuu-launch-description" aria-hidden={!slow}>{slow ? t("loading.slow") : "\u00a0"}</p>
          </> : <>
            <pre id={descriptionId} className="wuu-launch-error">{status}</pre>
            {onRetry && <button type="button" className="settings-button" onClick={onRetry}>{t("loading.reconnect")}</button>}
          </>}
        </div>
      </div>
    </div>
  );
}

export function WorkspacePanelLoading(): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="workspace-panel-loading" role="status" aria-label={t("workspace.terminal.status.starting")}>
      <div className="loading-rail" aria-hidden="true" />
    </div>
  );
}

const VIEW_SWITCH_LOADING_CLASS = {
  window: "view-switch-loading",
  conversation: "view-switch-loading-conversation",
  inline: "view-switch-loading-inline",
} as const;

export function ViewSwitchLoading({
  placement = "window",
}: {
  placement?: keyof typeof VIEW_SWITCH_LOADING_CLASS;
}): JSX.Element {
  const { t } = useI18n();
  const indicator = (
    <div className={VIEW_SWITCH_LOADING_CLASS[placement]} role="status" aria-label={t("loading.switching")}>
      <div className="loading-rail" aria-hidden="true" />
    </div>
  );
  // Connection cards embed the mark beside recovery controls, and conversation
  // switches cover only the conversation so navigation stays visible. Window
  // loading escapes pane clipping and stacking contexts to cover the whole app.
  return placement === "window" ? createPortal(indicator, document.body) : indicator;
}

export function EmptyConversationHome({
  title,
  belowTitle,
  activity = "idle",
  children
}: {
  title: ReactNode;
  // Optional element rendered in the title's own header row, so it sits
  // beside the greeting instead of one row-gap below it with the children.
  belowTitle?: JSX.Element;
  activity?: WuuMascotActivity;
  children?: JSX.Element | null;
}): JSX.Element {
  return (
    <section className="empty-home" data-wuu-component="empty-session">
      <div className="empty-home-inner session-flow">
        <div className="empty-home-header">
          <WuuMascot
            className="empty-home-mascot"
            activity={activity}
            ambient
            followPointer
            animate="hover"
            aria-hidden="true"
          />
          <h2>{title}</h2>
          {belowTitle ?? null}
        </div>
        {children}
      </div>
    </section>
  );
}
