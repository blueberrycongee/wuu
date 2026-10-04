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
      {/* Original wordmark from the brand proposal in PR #541. */}
      <svg className="sidebar-brand-wordmark" viewBox="0 0 3413 1020" role="img" aria-label="Wuu">
        <path fill="currentColor" d="M584 240L584 628.27C584 751.17 529.44 809 413.5 809C297.56 809 243 751.17 243 628.27L243 0L0 0L0 598.65C0 885.17 127.2 1020 397.5 1020C527.19 1020 623.94 988.96 689.5 925.02C755.06 988.96 851.81 1020 981.5 1020C1251.8 1020 1379 885.17 1379 598.65L1379 0L1136 0L1136 628.27C1136 751.17 1081.44 809 965.5 809C849.56 809 795 751.17 795 628.27L795 240L584 240ZM1752 0L1752 593.82C1752 740.14 1816.96 809 1955 809C2093.04 809 2158 740.14 2158 593.82L2158 0L2401 0L2401 1000L2158 1000L2158 989.46C2100.82 1009.93 2033.25 1020 1955 1020C1651.72 1020 1509 868.72 1509 547.24L1509 0L1752 0ZM2764 593.82L2764 0L2521 0L2521 547.24C2521 868.72 2663.72 1020 2967 1020C3045.25 1020 3112.82 1009.93 3170 989.46L3170 1000L3413 1000L3413 0L3170 0L3170 593.82C3170 740.14 3105.04 809 2967 809C2828.96 809 2764 740.14 2764 593.82Z" />
      </svg>
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
