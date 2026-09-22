import { useEffect, useState } from "react";
import type { IconComponent } from "./WuuIcons";
import { X } from "./WuuIcons";

const TOP_NOTICE_AUTO_DISMISS_MS = 6000;

export type TopNoticeAction = {
  label: string;
  onClick: () => void;
};

export type TopNoticeProps = {
  message: React.ReactNode;
  icon?: IconComponent;
  onDismiss: () => void;
  isError?: boolean;
  action?: TopNoticeAction;
  dismissAriaLabel?: string;
};

/**
 * Generic top-center pill toast. Reuses the archive-tip CSS so all ephemeral
 * notices share one visual treatment.
 */
export function TopNotice({
  message,
  icon: Icon,
  onDismiss,
  isError = false,
  action,
  dismissAriaLabel = "Close",
}: TopNoticeProps): JSX.Element {
  const [leaving, setLeaving] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setLeaving(true);
    }, TOP_NOTICE_AUTO_DISMISS_MS - 400);
    const finalize = window.setTimeout(onDismiss, TOP_NOTICE_AUTO_DISMISS_MS);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(finalize);
    };
  }, [onDismiss]);

  return (
    <div
      className={`archive-tip${isError ? " is-error" : ""}${leaving ? " leaving" : ""}`}
      data-wuu-component="notice"
      data-wuu-layer="notice"
      data-wuu-state={leaving ? "closing" : "open"}
      data-wuu-variant={isError ? "error" : "default"}
      role={isError ? "alert" : "status"}
      aria-live={isError ? "assertive" : "polite"}
    >
      {Icon ? <Icon className="archive-tip-icon" aria-hidden="true" /> : null}
      <span className="archive-tip-message">{message}</span>
      {action ? (
        <button
          type="button"
          className="archive-tip-action"
          onClick={action.onClick}
        >
          {action.label}
        </button>
      ) : null}
      <button
        type="button"
        className="archive-tip-dismiss"
        aria-label={dismissAriaLabel}
        onClick={() => {
          setLeaving(true);
          window.setTimeout(onDismiss, 200);
        }}
      >
        <X className="icon-sm" />
      </button>
    </div>
  );
}
