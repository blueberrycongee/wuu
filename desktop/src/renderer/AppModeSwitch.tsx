import { Bell, X } from "lucide-react";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export type AppMode = "harness" | "collaboration";
const CLEAR_UNREAD_HOLD_MS = 600;
export const CLEAR_UNREAD_HINT_SEEN_KEY = "wuu.desktop.clearUnreadHintSeen";
const HINT_VIEWPORT_MARGIN = 8;
const HINT_TRIGGER_GAP = 6;

function loadClearUnreadHintSeen(): boolean {
  try {
    return window.localStorage.getItem(CLEAR_UNREAD_HINT_SEEN_KEY) === "true";
  } catch {
    return false;
  }
}

function persistClearUnreadHintSeen(): void {
  try {
    window.localStorage.setItem(CLEAR_UNREAD_HINT_SEEN_KEY, "true");
  } catch {
    // A blocked or full localStorage should not keep the tip looping forever.
  }
}

export function AppModeSwitch({
  mode,
  collaborationEnabled,
  onChange,
  readOnly = false,
  unreadViewOpen = false,
  unreadCount = 0,
  onToggleUnreadView,
  onClearUnread,
}: {
  mode: AppMode;
  collaborationEnabled: boolean;
  onChange?: (mode: AppMode) => void;
  readOnly?: boolean;
  unreadViewOpen?: boolean;
  unreadCount?: number;
  onToggleUnreadView?: () => void;
  onClearUnread?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const holdTimerRef = useRef<number | undefined>(undefined);
  const longPressTriggeredRef = useRef(false);
  const keyboardPressRef = useRef<string | undefined>(undefined);
  const bellButtonRef = useRef<HTMLButtonElement | null>(null);
  const hintLayerRef = useRef<HTMLDivElement | null>(null);
  const [holding, setHolding] = useState(false);
  const [clearUnreadHintSeen, setClearUnreadHintSeen] = useState(loadClearUnreadHintSeen);
  const [bellVisible, setBellVisible] = useState(false);
  const [hintPosition, setHintPosition] = useState<CSSProperties | null>(null);
  const hintEligible =
    !readOnly && mode === "harness" && unreadCount > 0 && !clearUnreadHintSeen;
  const showClearUnreadHint = hintEligible && bellVisible;

  function markClearUnreadHintSeen(): void {
    if (clearUnreadHintSeen) return;
    persistClearUnreadHintSeen();
    setClearUnreadHintSeen(true);
  }

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
      markClearUnreadHintSeen();
      onClearUnread?.();
    }, CLEAR_UNREAD_HOLD_MS);
  }

  useEffect(() => () => {
    if (holdTimerRef.current !== undefined) {
      window.clearTimeout(holdTimerRef.current);
    }
  }, []);

  useEffect(() => {
    if (!hintEligible) {
      setBellVisible(false);
      return;
    }

    const trigger = bellButtonRef.current;
    const Observer = window.IntersectionObserver;
    if (!trigger || typeof Observer !== "function") {
      // Without IntersectionObserver, keep the tip tied to an actually
      // laid-out bell so a collapsed/off-canvas rail cannot orphan it.
      const rect = trigger?.getBoundingClientRect();
      setBellVisible(Boolean(trigger && rect && rect.width > 0 && rect.height > 0));
      return;
    }

    const observer = new Observer(
      ([entry]) => {
        setBellVisible(Boolean(entry?.isIntersecting && entry.intersectionRatio > 0));
      },
      { threshold: [0, 0.01, 1] },
    );
    observer.observe(trigger);
    return () => observer.disconnect();
  }, [hintEligible]);

  useLayoutEffect(() => {
    if (!showClearUnreadHint) {
      setHintPosition(null);
      return;
    }

    const updatePosition = (): void => {
      const trigger = bellButtonRef.current;
      const layer = hintLayerRef.current;
      if (!trigger || !layer) return;

      const rect = trigger.getBoundingClientRect();
      // Wait for a real layout box before anchoring. Visibility itself is
      // owned by IntersectionObserver so a collapsed sidebar can hide the
      // tip without this geometry check fighting it.
      if (rect.width <= 0 || rect.height <= 0) {
        setHintPosition({ visibility: "hidden" });
        return;
      }

      const tip = layer.getBoundingClientRect();
      const maxLeft = Math.max(
        HINT_VIEWPORT_MARGIN,
        window.innerWidth - tip.width - HINT_VIEWPORT_MARGIN,
      );
      const preferredLeft = rect.right - tip.width;
      const left = Math.min(Math.max(preferredLeft, HINT_VIEWPORT_MARGIN), maxLeft);
      const belowTop = rect.bottom + HINT_TRIGGER_GAP;
      const aboveTop = rect.top - tip.height - HINT_TRIGGER_GAP;
      const fitsBelow = belowTop + tip.height <= window.innerHeight - HINT_VIEWPORT_MARGIN;
      const top = fitsBelow
        ? belowTop
        : Math.max(HINT_VIEWPORT_MARGIN, aboveTop);

      const anchorX = Math.min(Math.max(rect.left + rect.width / 2 - left, 12), Math.max(12, tip.width - 12));
      setHintPosition({
        left,
        top,
        visibility: "visible",
        "--hint-anchor-x": `${anchorX}px`,
        "--hint-arrow-top": fitsBelow ? "-4px" : "calc(100% - 4px)",
        "--hint-arrow-rotation": fitsBelow ? "45deg" : "225deg",
      } as CSSProperties);
    };

    updatePosition();
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(updatePosition) : null;
    if (bellButtonRef.current?.parentElement) observer?.observe(bellButtonRef.current.parentElement);
    if (hintLayerRef.current) observer?.observe(hintLayerRef.current);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [showClearUnreadHint, t]);

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
    <div className="sidebar-brand" aria-label={t("sidebar.productMode")}>
      <span className="sidebar-brand-wordmark">wuu</span>
      <div className="sidebar-mode-switch" role="group" aria-label={t("sidebar.productMode")}>
        {collaborationEnabled ? (
          readOnly ? (
            <span className="sidebar-mode-option sidebar-mode-option-static">
              {t("sidebar.collaboration")}
            </span>
          ) : (
            <button
              className="sidebar-mode-option"
              type="button"
              aria-pressed={mode === "collaboration"}
              onClick={() => onChange?.("collaboration")}
            >
              {t("sidebar.collaboration")}
            </button>
          )
        ) : null}
        {readOnly ? (
          <span className="sidebar-mode-option sidebar-mode-option-static sidebar-brand-descriptor">
            {t("sidebar.harness")}
          </span>
        ) : (
          <button
            className="sidebar-mode-option sidebar-brand-descriptor"
            type="button"
            aria-pressed={mode === "harness"}
            onClick={() => onChange?.("harness")}
          >
            {t("sidebar.harness")}
          </button>
        )}
      </div>
      {!readOnly && mode === "harness" ? (
        <>
          <button
            ref={bellButtonRef}
            className="sidebar-notifications-button"
            type="button"
            aria-label={t("sidebar.notificationsHint", { count: unreadCount })}
            aria-pressed={unreadViewOpen}
            title={t("sidebar.notificationsHint", { count: unreadCount })}
            data-has-unread={unreadCount > 0 || undefined}
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
          {showClearUnreadHint ? (
            <UILayerPortal layer="popover">
              <div
                ref={hintLayerRef}
                className="sidebar-clear-unread-hint"
                data-wuu-component="sidebar-clear-unread-hint"
                data-wuu-layer="popover"
                role="status"
                style={hintPosition ?? { visibility: "hidden" }}
              >
                <p className="sidebar-clear-unread-hint-copy">
                  {t("sidebar.clearUnreadHint")}
                </p>
                <button
                  className="sidebar-clear-unread-hint-dismiss"
                  type="button"
                  aria-label={t("common.close")}
                  onClick={markClearUnreadHintSeen}
                >
                  <X aria-hidden="true" />
                </button>
              </div>
            </UILayerPortal>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
