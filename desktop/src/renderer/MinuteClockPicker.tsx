import { ChevronDown, Clock3 } from "./WuuIcons";
import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { FloatingMenuPortal, isInsideFloatingMenu } from "./ComposerFloatingMenu";

const CLOCK_MINUTES = Array.from({ length: 12 }, (_, index) => index * 5);
const CLOCK_TICKS = Array.from({ length: 60 }, (_, index) => index);

export function minuteFromClockPoint(x: number, y: number): number {
  const degrees = (Math.atan2(y, x) * 180) / Math.PI + 90;
  return Math.round(((degrees + 360) % 360) / 6) % 60;
}

export function MinuteClockPicker({
  minute,
  onChange,
  ariaLabel,
}: {
  minute: number;
  onChange: (minute: number) => void;
  ariaLabel: string;
}): JSX.Element {
  const anchorRef = useRef<HTMLDivElement>(null);
  const faceRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [draftMinute, setDraftMinute] = useState(minute);

  useEffect(() => setDraftMinute(minute), [minute]);

  useEffect(() => {
    if (!open) return undefined;
    faceRef.current?.focus();
    function dismiss(event: PointerEvent): void {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (anchorRef.current?.contains(target) || isInsideFloatingMenu(target, "minute-clock")) return;
      setOpen(false);
    }
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);

  function commit(nextMinute: number): void {
    setDraftMinute(nextMinute);
    onChange(nextMinute);
    setOpen(false);
  }

  function minuteFromPointer(event: ReactPointerEvent<HTMLDivElement>): number | null {
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return null;
    const x = event.clientX - (rect.left + rect.width / 2);
    const y = event.clientY - (rect.top + rect.height / 2);
    return minuteFromClockPoint(x, y);
  }

  function updateFromPointer(event: ReactPointerEvent<HTMLDivElement>): void {
    const nextMinute = minuteFromPointer(event);
    if (nextMinute !== null) setDraftMinute(nextMinute);
  }

  function handleFaceKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      commit(draftMinute);
      return;
    }
    const direction = event.key === "ArrowUp" || event.key === "ArrowRight"
      ? 1
      : event.key === "ArrowDown" || event.key === "ArrowLeft"
        ? -1
        : 0;
    if (direction !== 0) {
      event.preventDefault();
      setDraftMinute((current) => (current + direction + 60) % 60);
    }
  }

  return (
    <div className="select-menu minute-clock-picker" ref={anchorRef}>
      <button
        type="button"
        className="select-menu-trigger settings-select-trigger"
        aria-label={ariaLabel}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <span className="minute-clock-trigger-value">
          <Clock3 className="icon" aria-hidden="true" />
          :{String(minute).padStart(2, "0")}
        </span>
        <ChevronDown className="select-menu-chevron icon" aria-hidden="true" />
      </button>
      {open ? (
        <FloatingMenuPortal
          anchorRef={anchorRef}
          owner="minute-clock"
          placement="below"
          align="left"
          width={240}
          flip
        >
          <div className="select-menu-panel minute-clock-panel" role="dialog" aria-label={ariaLabel}>
            <strong className="minute-clock-readout">:{String(draftMinute).padStart(2, "0")}</strong>
            <div
              ref={faceRef}
              className="minute-clock-face"
              role="slider"
              tabIndex={0}
              aria-label={ariaLabel}
              aria-valuemin={0}
              aria-valuemax={59}
              aria-valuenow={draftMinute}
              aria-valuetext={`:${String(draftMinute).padStart(2, "0")}`}
              onKeyDown={handleFaceKeyDown}
              onPointerDown={(event) => {
                event.currentTarget.setPointerCapture(event.pointerId);
                updateFromPointer(event);
              }}
              onPointerMove={(event) => {
                if (event.currentTarget.hasPointerCapture(event.pointerId)) updateFromPointer(event);
              }}
              onPointerUp={(event) => {
                const nextMinute = minuteFromPointer(event);
                event.currentTarget.releasePointerCapture(event.pointerId);
                if (nextMinute !== null) commit(nextMinute);
              }}
            >
              {CLOCK_TICKS.map((value) => (
                <span
                  key={`tick-${value}`}
                  className={[
                    "minute-clock-tick",
                    value % 5 === 0 ? "major" : "",
                    value === draftMinute ? "active" : "",
                  ].filter(Boolean).join(" ")}
                  data-tick={value}
                  style={{ transform: `translateX(-50%) rotate(${value * 6}deg)` }}
                />
              ))}
              <span className="minute-clock-hand" style={{ transform: `translateX(-50%) rotate(${draftMinute * 6}deg)` }}>
                <span className="minute-clock-hand-tip" />
              </span>
              <span className="minute-clock-pin" />
              {CLOCK_MINUTES.map((value) => {
                const angle = (value / 60) * Math.PI * 2 - Math.PI / 2;
                return (
                  <button
                    key={value}
                    type="button"
                    className={value === draftMinute ? "minute-clock-mark active" : "minute-clock-mark"}
                    style={{
                      left: `calc(50% + ${Math.cos(angle) * 88}px)`,
                      top: `calc(50% + ${Math.sin(angle) * 88}px)`,
                    }}
                    data-minute={value}
                    tabIndex={-1}
                    onPointerDown={(event) => event.stopPropagation()}
                    onClick={() => commit(value)}
                  >
                    {String(value).padStart(2, "0")}
                  </button>
                );
              })}
            </div>
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}
