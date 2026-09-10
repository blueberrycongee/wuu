import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { isTouchWebShell } from "./ComposerFocus";
import { eventTargetsNestedAutoFollowScroll, selectionIntersectsNode } from "./AutoFollowScroll";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import "./styles/pull-to-new-session.css";

const THRESHOLD = 80;
const SETTLE_MS = 240;
type PullVisual = {
  distance: number;
  lift: number;
  left: number;
  top: number;
  width: number;
  height: number;
  phase: "pulling" | "cancelling" | "committing";
};

function atBottom(node: HTMLElement): boolean {
  return node.clientHeight > 0 &&
    Math.abs(Math.max(0, node.scrollHeight - node.clientHeight) - Math.max(0, node.scrollTop)) <= 2;
}

function ownsTouch(target: EventTarget | null, viewport: HTMLElement): boolean {
  if (!(target instanceof Element)) return false;
  if (eventTargetsNestedAutoFollowScroll(target, viewport) ||
    target.closest("button, a, input, textarea, select, [contenteditable]:not([contenteditable=false]), [role=slider]")) return false;
  for (let node: Element | null = target; node !== viewport; node = node.parentElement) {
    if (!node) return false;
    const style = getComputedStyle(node);
    if (/(auto|scroll)/.test(`${style.overflowX} ${style.overflowY}`)) return false;
  }
  return true;
}

/** A fresh upward touch at the latest message can open a new draft session. */
export function PullToNewSession({
  containerRef,
  contentRef,
  bottomAnchor,
  onNewSession,
}: {
  containerRef: RefObject<HTMLElement | null>;
  contentRef: RefObject<HTMLDivElement | null>;
  bottomAnchor: HTMLElement | null;
  onNewSession: () => void;
}) {
  const { t } = useI18n();
  const callback = useRef(onNewSession);
  useLayoutEffect(() => { callback.current = onNewSession; });
  const [visual, setVisual] = useState<PullVisual | null>(null);

  useLayoutEffect(() => {
    const node = containerRef.current;
    const content = contentRef.current;
    if (!node || !content || !isTouchWebShell()) return;
    const pane = node.closest<HTMLElement>(".conversation-pane");
    let gesture: { id: number; x: number; y: number; distance: number } | undefined;
    let current: PullVisual | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let settling = false;
    const clear = () => {
      clearTimeout(timer);
      gesture = undefined;
      current = null;
      settling = false;
      delete content.dataset.sessionPull;
      content.style.removeProperty("--session-pull-lift");
      if (pane) delete pane.dataset.sessionPull;
      setVisual(null);
    };
    const settle = (commit: boolean) => {
      gesture = undefined;
      if (settling) return;
      if (!current) { clear(); return; }
      settling = true;
      const phase = commit ? "committing" : "cancelling";
      content.dataset.sessionPull = phase;
      current = { ...current, phase };
      setVisual(current);
      // A deadline also completes the action when the browser suppresses
      // transitionend (background tabs or interrupted paint).
      timer = setTimeout(() => {
        clear();
        if (commit) callback.current();
      }, window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : SETTLE_MS);
    };
    const reset = () => { if (!settling) settle(false); };
    const eligible = () => isTouchWebShell() && atBottom(node) &&
      !selectionIntersectsNode(document.getSelection(), node);
    const start = (event: TouchEvent) => {
      if (settling) return;
      if (gesture) { reset(); return; }
      // A gesture that starts in history never becomes a new-session gesture,
      // even if native scrolling reaches the bottom before the finger lifts.
      if (event.touches.length !== 1 || !eligible() || !ownsTouch(event.target, node)) return;
      const touch = event.touches[0];
      gesture = { id: touch.identifier, x: touch.clientX, y: touch.clientY, distance: 0 };
    };
    const update = (touch: Touch): boolean => {
      if (!gesture || touch.identifier !== gesture.id || !eligible()) { reset(); return false; }
      const distance = gesture.y - touch.clientY;
      const horizontal = Math.abs(touch.clientX - gesture.x);
      if (distance < 0 || (horizontal > 10 && horizontal > distance)) { reset(); return false; }
      gesture.distance = distance;
      const rect = node.getBoundingClientRect();
      const bottom = Math.min(rect.bottom, bottomAnchor?.getBoundingClientRect().top ?? rect.bottom) - 12;
      // Resistance increases with travel. The bottom stays anchored while the
      // upper tip stretches into the space vacated by the message flow.
      const lift = Math.min(Math.max(0, bottom - rect.top), 140 * (1 - Math.exp(-distance / 150)));
      current = {
        distance,
        lift,
        left: rect.left,
        top: rect.top,
        width: rect.width,
        height: Math.max(0, bottom - rect.top),
        phase: "pulling",
      };
      content.dataset.sessionPull = "pulling";
      content.style.setProperty("--session-pull-lift", `${lift}px`);
      if (pane) pane.dataset.sessionPull = "active";
      setVisual(current);
      return distance > 0;
    };
    const move = (event: TouchEvent) => {
      if (!gesture) return;
      if (event.defaultPrevented || event.touches.length !== 1 || !event.cancelable) { reset(); return; }
      // Leave touch slop undecided so a thumb arc can become a drawer swipe.
      if (!current && Math.max(Math.abs(event.touches[0].clientX - gesture.x),
        Math.abs(event.touches[0].clientY - gesture.y)) < 8) return;
      // Cancel native bounce only after upward intent at the bottom. Ordinary
      // history scrolling and nested scroll areas retain their native behavior.
      if (update(event.touches[0])) event.preventDefault();
    };
    const end = (event: TouchEvent) => {
      if (!gesture) return;
      if (event.touches.length || event.changedTouches.length !== 1 || gesture.distance <= 0) { reset(); return; }
      update(event.changedTouches[0]);
      const commit = gesture && gesture.distance >= THRESHOLD;
      settle(Boolean(commit));
    };
    const scroll = () => { if (!atBottom(node)) reset(); };
    node.addEventListener("touchstart", start, { passive: true });
    node.addEventListener("touchmove", move, { passive: false });
    node.addEventListener("touchend", end);
    node.addEventListener("touchcancel", reset);
    node.addEventListener("scroll", scroll, { passive: true });
    window.addEventListener("resize", reset);
    window.addEventListener("blur", reset);
    window.visualViewport?.addEventListener("resize", reset);
    window.visualViewport?.addEventListener("scroll", reset);
    document.addEventListener("selectionchange", reset);
    return () => {
      clear();
      node.removeEventListener("touchstart", start);
      node.removeEventListener("touchmove", move);
      node.removeEventListener("touchend", end);
      node.removeEventListener("touchcancel", reset);
      node.removeEventListener("scroll", scroll);
      window.removeEventListener("resize", reset);
      window.removeEventListener("blur", reset);
      window.visualViewport?.removeEventListener("resize", reset);
      window.visualViewport?.removeEventListener("scroll", reset);
      document.removeEventListener("selectionchange", reset);
    };
  }, [containerRef, contentRef, bottomAnchor]);

  if (!visual) return null;
  const ready = visual.distance >= THRESHOLD;
  const committing = visual.phase === "committing";
  const cancelling = visual.phase === "cancelling";
  const height = Math.max(0, visual.lift - 12);
  return (
    <UILayerPortal layer="navigation">
      <div
        className="pull-to-new-session"
        data-wuu-component="pull-to-new-session"
        data-wuu-layer="navigation"
        data-phase={visual.phase}
        data-ready={ready}
        role="status"
        aria-label={t(ready ? "conversation.releaseNewSession" : "conversation.pullNewSession")}
        style={{ left: visual.left, top: visual.top, width: visual.width, height: visual.height }}
      >
        <div className="pull-to-new-session-drop" aria-hidden="true" style={{
          width: committing ? visual.width : cancelling ? 20 : Math.max(24, 34 - height * 0.06),
          height: committing ? visual.height : cancelling ? 0 : height,
          opacity: cancelling ? 0 : committing ? 1 : Math.min(1, height / 24),
        }}>
          <span className="pull-to-new-session-plus">+</span>
        </div>
      </div>
    </UILayerPortal>
  );
}
