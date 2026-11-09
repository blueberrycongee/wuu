import { useEffect, useRef, type PointerEvent } from "react";
import { RIBBON_COMPACT_WIDTH, RIBBON_MAX_WIDTH } from "../shared/navigationRibbon";

const expandedWidth = 248;
const collapseOvershoot = 24;

/** Dragging past the compact endpoint previews collapse until pointer release. */
export function RibbonResizeHandle({ width, label, onResize, onCancel }: {
  width: number;
  label: string;
  onResize: (width: number, finished: boolean, collapsed?: boolean) => void;
  onCancel: () => void;
}): JSX.Element {
  const handle = useRef<HTMLDivElement>(null);
  const originTop = useRef(0);
  const track = (event: PointerEvent<HTMLDivElement>) => {
    if (event.type === "pointerenter" || event.type === "pointerdown") {
      originTop.current = event.currentTarget.getBoundingClientRect().top;
    }
    event.currentTarget.style.setProperty("--sidebar-resizer-pointer-y", `${Math.round(event.clientY - originTop.current)}px`);
  };
  const drag = useRef<{ x: number; width: number; current: number; pointerId: number } | null>(null);
  const latest = useRef({ onResize, onCancel });
  latest.current = { onResize, onCancel };
  const finish = (cancelled = false) => {
    const session = drag.current;
    if (!session) return;
    drag.current = null;
    handle.current?.removeAttribute("data-resizing");
    if (handle.current?.hasPointerCapture(session.pointerId)) handle.current.releasePointerCapture(session.pointerId);
    if (cancelled) { latest.current.onCancel(); return; }
    const collapsed = session.current <= RIBBON_COMPACT_WIDTH - collapseOvershoot;
    latest.current.onResize(collapsed ? session.width : session.current < 100 ? RIBBON_COMPACT_WIDTH : session.current, true, collapsed);
  };
  useEffect(() => {
    const cancel = () => finish(true);
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || !drag.current) return;
      event.preventDefault(); event.stopPropagation();
      cancel();
    };
    window.addEventListener("blur", cancel);
    window.addEventListener("keydown", escape, true);
    return () => {
      window.removeEventListener("blur", cancel);
      window.removeEventListener("keydown", escape, true);
    };
  }, []);
  const move = (event: PointerEvent<HTMLDivElement>) => {
    track(event);
    if (!drag.current || drag.current.pointerId !== event.pointerId) return;
    const next = Math.min(RIBBON_MAX_WIDTH, drag.current.width + event.clientX - drag.current.x);
    drag.current.current = next;
    latest.current.onResize(Math.max(RIBBON_COMPACT_WIDTH, next), false, next <= RIBBON_COMPACT_WIDTH - collapseOvershoot);
  };
  return <div ref={handle} className="ribbon-resizer" role="separator" aria-label={label} aria-orientation="vertical"
    aria-valuemin={RIBBON_COMPACT_WIDTH} aria-valuemax={RIBBON_MAX_WIDTH} aria-valuenow={width} tabIndex={0}
    onPointerEnter={track}
    onPointerDown={event => {
      if (event.button !== 0 || drag.current) return;
      event.preventDefault(); event.stopPropagation();
      track(event);
      const bounds = event.currentTarget.getBoundingClientRect();
      event.currentTarget.style.setProperty("--sidebar-resizer-grab-x", `${Math.max(-3, Math.min(3, event.clientX - bounds.left - bounds.width / 2))}px`);
      event.currentTarget.setAttribute("data-resizing", "");
      event.currentTarget.setPointerCapture(event.pointerId);
      drag.current = { x: event.clientX, width, current: width, pointerId: event.pointerId };
      latest.current.onResize(width, false);
    }} onPointerMove={move} onPointerUp={event => {
      if (drag.current?.pointerId !== event.pointerId) return;
      move(event); finish();
    }} onPointerCancel={() => finish(true)} onLostPointerCapture={() => finish(true)}
    onDoubleClick={() => latest.current.onResize(width > RIBBON_COMPACT_WIDTH ? RIBBON_COMPACT_WIDTH : expandedWidth, true)}
    onKeyDown={event => {
      if (drag.current) return;
      const next = event.key === "ArrowRight" ? Math.min(RIBBON_MAX_WIDTH, width + 24)
        : event.key === "ArrowLeft" ? Math.max(RIBBON_COMPACT_WIDTH, width - 24)
        : event.key === "Home" ? RIBBON_COMPACT_WIDTH : event.key === "End" ? RIBBON_MAX_WIDTH : undefined;
      if (next !== undefined) { event.preventDefault(); event.stopPropagation(); latest.current.onResize(next, true); }
    }} />;
}
