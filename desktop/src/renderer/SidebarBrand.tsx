import { Bell } from "./WuuIcons";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";

const CLEAR_UNREAD_HOLD_MS = 600;

export function SidebarBrand({
  readOnly = false,
  unreadViewOpen = false,
  attentionCount = 0,
  hasUnread = false,
  onToggleUnreadView,
  onClearUnread,
}: {
  readOnly?: boolean;
  unreadViewOpen?: boolean;
  /** Running and unread sessions, both listed by the attention view. */
  attentionCount?: number;
  /** The dot means unread, as it does on session rows; running is not unread. */
  hasUnread?: boolean;
  onToggleUnreadView?: () => void;
  onClearUnread?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const holdTimerRef = useRef<number | undefined>(undefined);
  const longPressTriggeredRef = useRef(false);
  const keyboardPressRef = useRef<string | undefined>(undefined);
  const [holding, setHolding] = useState(false);

  function cancelHold(): void {
    if (holdTimerRef.current !== undefined) {
      window.clearTimeout(holdTimerRef.current);
      holdTimerRef.current = undefined;
    }
    setHolding(false);
  }

  function startHold(): void {
    cancelHold();
    longPressTriggeredRef.current = false;
    setHolding(true);
    holdTimerRef.current = window.setTimeout(() => {
      holdTimerRef.current = undefined;
      longPressTriggeredRef.current = true;
      setHolding(false);
      onClearUnread?.();
    }, CLEAR_UNREAD_HOLD_MS);
  }

  useEffect(() => () => {
    if (holdTimerRef.current !== undefined) {
      window.clearTimeout(holdTimerRef.current);
    }
  }, []);

  function handleNotificationPointerDown(event: ReactPointerEvent<HTMLButtonElement>): void {
    if (event.button !== 0) return;
    startHold();
  }

  function handleNotificationPointerLeave(): void {
    cancelHold();
    longPressTriggeredRef.current = false;
  }

  function handleNotificationKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>): void {
    if ((event.key !== "Enter" && event.key !== " ") || event.repeat) return;
    event.preventDefault();
    keyboardPressRef.current = event.key;
    startHold();
  }

  function handleNotificationKeyUp(event: ReactKeyboardEvent<HTMLButtonElement>): void {
    if (keyboardPressRef.current !== event.key) return;
    event.preventDefault();
    keyboardPressRef.current = undefined;
    const wasLongPress = longPressTriggeredRef.current;
    cancelHold();
    longPressTriggeredRef.current = false;
    if (!wasLongPress) onToggleUnreadView?.();
  }

  return (
    <div className="sidebar-brand">
      <span className="sidebar-brand-wordmark">wuu</span>
      {!readOnly ? (
        <Tooltip content={t("sidebar.attentionConversations")}>
          <button
            className="sidebar-notifications-button"
            type="button"
            aria-label={t("sidebar.notificationsHint", { count: attentionCount })}
            aria-pressed={unreadViewOpen}
            data-has-unread={hasUnread || undefined}
            data-holding={holding || undefined}
            onPointerDown={handleNotificationPointerDown}
            onPointerUp={cancelHold}
            onPointerCancel={() => {
              cancelHold();
              longPressTriggeredRef.current = false;
            }}
            onPointerLeave={handleNotificationPointerLeave}
            onKeyDown={handleNotificationKeyDown}
            onKeyUp={handleNotificationKeyUp}
            onBlur={() => {
              cancelHold();
              keyboardPressRef.current = undefined;
              longPressTriggeredRef.current = false;
            }}
            onClick={(event) => {
              if (longPressTriggeredRef.current) {
                event.preventDefault();
                longPressTriggeredRef.current = false;
                return;
              }
              onToggleUnreadView?.();
            }}
          >
            <Bell aria-hidden="true" />
            <span className="sidebar-notifications-dot" aria-hidden="true" />
          </button>
        </Tooltip>
      ) : null}
    </div>
  );
}
