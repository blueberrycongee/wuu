import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { motionDurationMs } from "./motion";
import type { IconComponent } from "./WuuIcons";
import { X } from "./WuuIcons";

const TOP_NOTICE_AUTO_DISMISS_MS = 6000;

export type TopNoticeAction = {
  label: string;
  onClick: () => void;
  disabled?: boolean;
};

export type TopNoticeProps = {
  message: React.ReactNode;
  icon?: IconComponent;
  onDismiss: () => void;
  isError?: boolean;
  action?: TopNoticeAction;
  dismissAriaLabel?: string;
  persistent?: boolean;
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
  persistent = false,
}: TopNoticeProps): JSX.Element {
  const [leaving, setLeaving] = useState(false);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [dismissRequested, setDismissRequested] = useState(false);
  const onDismissRef = useRef(onDismiss);
  useLayoutEffect(() => { onDismissRef.current = onDismiss; }, [onDismiss]);

  // Reading or using an action pauses expiry. Unrelated parent renders do not
  // restart it, and every exit timer belongs to this notice's mounted lifetime.
  useEffect(() => {
    if (dismissRequested) {
      const timer = window.setTimeout(() => onDismissRef.current(), motionDurationMs("--motion-base", 180));
      return () => window.clearTimeout(timer);
    }
    setLeaving(false);
    if (persistent || hovered || focused) return;
    const timer = window.setTimeout(() => {
      setLeaving(true);
    }, TOP_NOTICE_AUTO_DISMISS_MS - 400);
    const finalize = window.setTimeout(() => onDismissRef.current(), TOP_NOTICE_AUTO_DISMISS_MS);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(finalize);
    };
  }, [persistent, hovered, focused, dismissRequested]);

  return (
    <div
      className={`archive-tip${isError ? " is-error" : ""}${leaving ? " leaving" : ""}`}
      data-wuu-component="notice"
      data-wuu-layer="notice"
      data-wuu-state={leaving ? "closing" : "open"}
      data-wuu-variant={isError ? "error" : "default"}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={event => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
      }}
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
          disabled={action.disabled}
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
          setDismissRequested(true);
        }}
      >
        <X className="icon-sm" />
      </button>
    </div>
  );
}
