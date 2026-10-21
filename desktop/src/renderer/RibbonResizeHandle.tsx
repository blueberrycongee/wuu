import { useEffect, useRef, type PointerEvent } from "react";
import { RIBBON_COMPACT_WIDTH as SIDEBAR_RIBBON_WIDTH, RIBBON_EXPANDED_WIDTH, RIBBON_MAX_WIDTH } from "../shared/navigationRibbon";

/** The compact endpoint keeps the icon axis; intermediate widths follow input. */
export function RibbonResizeHandle({ width, label, onResize }: {
  width: number;
  label: string;
  onResize: (width: number, finished: boolean) => void;
}): JSX.Element {
  const handle = useRef<HTMLDivElement>(null);
  const originTop = useRef(0);
  const track = (event: PointerEvent<HTMLDivElement>) => {
    if (event.type === "pointerenter" || event.type === "pointerdown") {
      originTop.current = event.currentTarget.getBoundingClientRect().top;
    }
    event.currentTarget.style.setProperty("--sidebar-resizer-pointer-y", `${Math.round(event.clientY - originTop.current)}px`);
  };
  const drag = useRef<{ x: number; width: number; current: number } | null>(null);
  const latest = useRef(onResize);
  latest.current = onResize;
  const finish = () => {
    const session = drag.current;
    if (!session) return;
    drag.current = null;
    handle.current?.removeAttribute("data-resizing");
    latest.current(session.current < 100 ? SIDEBAR_RIBBON_WIDTH : session.current, true);
  };
  useEffect(() => {
    window.addEventListener("blur", finish);
    return () => window.removeEventListener("blur", finish);
  }, []);
  const move = (event: PointerEvent<HTMLDivElement>) => {
    track(event);
    if (!drag.current) return;
    const next = Math.max(SIDEBAR_RIBBON_WIDTH, Math.min(RIBBON_MAX_WIDTH, drag.current.width + event.clientX - drag.current.x));
    drag.current.current = next;
    latest.current(next, false);
  };
  return <div ref={handle} className="ribbon-resizer" role="separator" aria-label={label} aria-orientation="vertical"
    aria-valuemin={SIDEBAR_RIBBON_WIDTH} aria-valuemax={RIBBON_MAX_WIDTH} aria-valuenow={width} tabIndex={0}
    onPointerEnter={track}
    onPointerDown={event => {
      if (event.button !== 0) return;
      event.preventDefault(); event.stopPropagation();
      track(event);
      const bounds = event.currentTarget.getBoundingClientRect();
      event.currentTarget.style.setProperty("--sidebar-resizer-grab-x", `${Math.max(-3, Math.min(3, event.clientX - bounds.left - bounds.width / 2))}px`);
      event.currentTarget.setAttribute("data-resizing", "");
      event.currentTarget.setPointerCapture(event.pointerId);
      drag.current = { x: event.clientX, width, current: width };
      latest.current(width, false);
    }} onPointerMove={move} onPointerUp={event => { move(event); finish(); }} onPointerCancel={finish} onLostPointerCapture={finish}
    onDoubleClick={() => latest.current(width > SIDEBAR_RIBBON_WIDTH ? SIDEBAR_RIBBON_WIDTH : RIBBON_EXPANDED_WIDTH, true)}
    onKeyDown={event => {
      const next = event.key === "ArrowRight" ? Math.min(RIBBON_MAX_WIDTH, width + 24)
        : event.key === "ArrowLeft" ? Math.max(SIDEBAR_RIBBON_WIDTH, width - 24)
        : event.key === "Home" ? SIDEBAR_RIBBON_WIDTH : event.key === "End" ? RIBBON_MAX_WIDTH : undefined;
      if (next !== undefined) { event.preventDefault(); latest.current(next, true); }
    }} />;
}
