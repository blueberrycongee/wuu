import { useEffect, useRef, type ReactNode } from "react";
import { _layout } from "blobatar";
import { prefersReducedMotion, subscribeReducedMotion } from "./motion";
import {
  WUU_MASCOT_BRAND_COLORS,
  WUU_MASCOT_ACTIVITY_PERSPECTIVES,
  WUU_MASCOT_NAME,
  WUU_MASCOT_TRAITS,
} from "./wuu-mascot-spec";
import "./styles/web-workspace-start-page.css";

const mascot = _layout(WUU_MASCOT_NAME, {
  traits: WUU_MASCOT_TRAITS,
  palette: WUU_MASCOT_BRAND_COLORS,
  perspective: WUU_MASCOT_ACTIVITY_PERSPECTIVES.idle,
});
// Supplying a perspective makes Blobatar return the projected contour of each eye.
const eyePaths = mascot.eyes.map((eye) => (eye as typeof eye & { path: string }).path);

export function WebWorkspaceStartPage({ active, searchField }: { active: boolean; searchField: ReactNode }): JSX.Element {
  const mascotRef = useRef<SVGSVGElement>(null);
  const eyesRef = useRef<SVGGElement>(null);
  const searchRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const svg = mascotRef.current;
    const eyes = eyesRef.current;
    const search = searchRef.current;
    if (!active || !svg || !eyes || !search) return;

    let pointer: { x: number; y: number } | null = null;
    let frame: number | undefined;
    let visible = true;
    let listening = false;
    let settleTimer: ReturnType<typeof setTimeout> | undefined;
    let typingTimer: ReturnType<typeof setTimeout> | undefined;
    const settle = () => {
      if (frame !== undefined) cancelAnimationFrame(frame);
      clearTimeout(settleTimer);
      frame = undefined;
      settleTimer = undefined;
      pointer = null;
      eyes.removeAttribute("transform");
    };
    const reset = () => {
      settle();
      clearTimeout(typingTimer);
      typingTimer = undefined;
    };
    const renderGaze = () => {
      frame = undefined;
      if (!pointer) return;
      const rect = svg.getBoundingClientRect();
      if (!rect.width || !rect.height) { reset(); return; }
      const dx = pointer.x - (rect.left + rect.width / 2);
      const dy = pointer.y - (rect.top + rect.height / 2);
      const reach = Math.max(80, Math.min(window.innerWidth, window.innerHeight) * 0.18);
      const distance = Math.max(reach, Math.hypot(dx, dy));
      const transform = `translate(${(dx / distance * 3).toFixed(2)} ${(dy / distance * 2).toFixed(2)})`;
      if (eyes.getAttribute("transform") !== transform) eyes.setAttribute("transform", transform);
    };
    const move = (event: PointerEvent) => {
      if (event.pointerType === "touch" || typingTimer !== undefined) return;
      pointer = { x: event.clientX, y: event.clientY };
      clearTimeout(settleTimer);
      settleTimer = setTimeout(settle, 1000);
      // Only pointer input schedules frames; the resting page has no animation loop.
      if (frame === undefined) frame = requestAnimationFrame(renderGaze);
    };
    const input = () => {
      if (!listening) return;
      // Glance once per typing burst, then face the user even if typing continues.
      // Focus alone (including new-tab autofocus) must not lower the gaze.
      if (typingTimer === undefined) {
        settle();
        eyes.setAttribute("transform", "translate(0 5)");
        settleTimer = setTimeout(settle, 700);
      }
      clearTimeout(typingTimer);
      typingTimer = setTimeout(() => { typingTimer = undefined; }, 1200);
    };
    const refresh = () => {
      const enabled = visible && !document.hidden && document.hasFocus() && !prefersReducedMotion();
      if (enabled === listening) return;
      listening = enabled;
      if (enabled) window.addEventListener("pointermove", move, { passive: true });
      else { window.removeEventListener("pointermove", move); reset(); }
    };
    const observer = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      refresh();
    });
    observer.observe(svg);
    window.addEventListener("focus", refresh);
    window.addEventListener("blur", refresh);
    document.addEventListener("visibilitychange", refresh);
    document.documentElement.addEventListener("pointerleave", reset);
    search.addEventListener("input", input);
    search.addEventListener("focusout", reset);
    const stopReducedMotion = subscribeReducedMotion(refresh);
    refresh();

    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("focus", refresh);
      window.removeEventListener("blur", refresh);
      document.removeEventListener("visibilitychange", refresh);
      document.documentElement.removeEventListener("pointerleave", reset);
      search.removeEventListener("input", input);
      search.removeEventListener("focusout", reset);
      stopReducedMotion();
      observer.disconnect();
      reset();
    };
  }, [active]);

  return (
    <div className="web-workspace-start-page">
      <div className="web-workspace-start-content">
        <svg
          ref={mascotRef}
          className="web-workspace-start-mascot"
          viewBox="0 0 100 100"
          aria-hidden="true"
          focusable="false"
        >
          <path d={mascot.body.path} fill={mascot.palette.head} />
          <g ref={eyesRef} className="web-workspace-start-mascot-eyes" fill={mascot.palette.eye}>
            {eyePaths.map((path, index) => <path key={index} d={path} />)}
          </g>
        </svg>
        <div ref={searchRef} className="web-workspace-start-search">{searchField}</div>
      </div>
    </div>
  );
}
