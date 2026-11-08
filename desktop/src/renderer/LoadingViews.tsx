import { useEffect, useId, useState, type CSSProperties, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { WuuIconMascot } from "./WuuIconMascot";
import { AlertCircle } from "./WuuIcons";
import { useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

export function RuntimeLoading({
  status, fullscreen = false, recovering = false, onRetry, onRecovery, detail,
}: {
  /** Only discriminates the state: a starting verb or any failure value; never rendered. */
  status: string;
  fullscreen?: boolean;
  recovering?: boolean;
  onRetry?: () => void;
  onRecovery?: () => void;
  /** Optional failure detail under the title; absent leaves title and actions. */
  detail?: string;
}): JSX.Element {
  const { t } = useI18n();
  const isStarting = status === "connecting" || status === "opening";
  const descriptionId = useId();
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    setSlow(false);
    if (!isStarting || recovering) return;
    const timeout = window.setTimeout(() => setSlow(true), 12000);
    return () => window.clearTimeout(timeout);
  }, [isStarting, status, recovering]);
  return (
    <div className={`project-empty-pane${fullscreen ? " application-recovery" : ""}`} data-wuu-component="launch-view">
      <div className="wuu-launch" role={isStarting ? "status" : "alert"}
        aria-label={t(isStarting ? "loading.starting" : "loading.failed")}
        aria-describedby={(isStarting ? (recovering || slow) : Boolean(detail)) ? descriptionId : undefined}>
        {!(isStarting && recovering) && <div className="wuu-launch-mark" aria-hidden="true">
          <div className="wuu-launch-glass">
            <WuuIconMascot className="wuu-launch-mascot" />
          </div>
          {!isStarting && <span className="wuu-launch-badge"><AlertCircle size={20} /></span>}
        </div>}
        <div className="wuu-launch-message">
          <p className="wuu-launch-title">{t(isStarting ? "loading.starting" : "loading.failed")}</p>
          {isStarting ? <>
            {!recovering && <div className="loading-rail" aria-hidden="true" />}
            <p id={descriptionId} className="wuu-launch-description" aria-hidden={!recovering && !slow}>{recovering || slow ? t("loading.slow") : "\u00a0"}</p>
          </> : detail ? (
            <p id={descriptionId} className="wuu-launch-error">{detail}</p>
          ) : null}
          {(!isStarting || recovering) && (onRecovery || onRetry) && <div className="wuu-launch-actions">
            {onRecovery && <button type="button" className={`settings-button${onRetry ? "" : " settings-button-primary"}`} onClick={onRecovery}>{t("loading.openRecovery")}</button>}
            {onRetry && <button type="button" className="settings-button settings-button-primary" onClick={onRetry}>{t("loading.reconnect")}</button>}
          </div>}
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
  placement = "window", style,
}: {
  placement?: keyof typeof VIEW_SWITCH_LOADING_CLASS; style?: CSSProperties;
}): JSX.Element {
  const { t } = useI18n();
  const indicator = (
    <div className={VIEW_SWITCH_LOADING_CLASS[placement]} style={style} role="status" aria-label={t("loading.switching")}>
      <div className="loading-rail" aria-hidden="true" />
    </div>
  );
  // Connection cards embed the mark beside recovery controls, and conversation
  // switches cover only the conversation so navigation stays visible. Window
  // loading escapes pane clipping and stacking contexts to cover the whole app.
  return placement === "window" ? createPortal(indicator, document.body) : indicator;
}

export function EmptyHomeMascot({ activity }: { activity: WuuMascotActivity }): JSX.Element {
  return <WuuMascot className="empty-home-mascot" activity={activity} ambient followPointer animate="hover" aria-hidden="true" />;
}

export function EmptyConversationHome({
  title,
  belowTitle,
  activity = "idle",
  mascot,
  heading,
  children
}: {
  title: ReactNode;
  // Optional element rendered in the title's own header row, so it sits
  // beside the greeting instead of one row-gap below it with the children.
  belowTitle?: JSX.Element;
  activity?: WuuMascotActivity;
  mascot?: ReactNode;
  heading?: ReactNode;
  children?: ReactNode;
}): JSX.Element {
  return (
    <section className="empty-home" data-wuu-component="empty-session">
      <div className="empty-home-inner session-flow">
        <div className="empty-home-header">
          {mascot === undefined ? <EmptyHomeMascot activity={activity} /> : mascot}
          {heading === undefined ? <h2>{title}</h2> : heading}
          {belowTitle ?? null}
        </div>
        {children}
      </div>
    </section>
  );
}
