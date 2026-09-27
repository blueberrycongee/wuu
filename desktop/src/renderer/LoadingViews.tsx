import { createPortal } from "react-dom";
import { WuuIconMascot } from "./WuuIconMascot";
import { useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

export function RuntimeLoading({
  status
}: {
  status: string;
}): JSX.Element {
  const { t } = useI18n();
  const isStarting = status === "connecting" || status === "opening";
  return (
    <div className="project-empty-pane" data-wuu-component="launch-view">
      {isStarting ? (
        <div className="wuu-launch" role="status" aria-label={t("loading.starting")}>
          <div className="wuu-launch-glass" aria-hidden="true">
            <WuuIconMascot className="wuu-launch-mascot" />
          </div>
        </div>
      ) : (
        <div className="project-empty-content">
          <h2>{status}</h2>
        </div>
      )}
    </div>
  );
}

export function WorkspacePanelLoading(): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="workspace-panel-loading" role="status" aria-label={t("workspace.terminal.status.starting")}>
      <div className="workspace-panel-loading-rail" aria-hidden="true" />
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
      <div className="wuu-launch-mark view-switch-mark" aria-hidden="true">
        <span>w</span>
        <span>u</span>
        <span>u</span>
      </div>
      <div className="wuu-launch-rail view-switch-rail" aria-hidden="true" />
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
  title: string;
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
