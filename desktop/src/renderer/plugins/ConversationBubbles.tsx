import { createContext, useEffect, useRef, useState, type RefObject } from "react";

export const ConversationBubbleContext = createContext<{ enabled: boolean; revealed: boolean }>({ enabled: false, revealed: false });
const REVEAL_DISTANCE = 64;
const interactive = "a, button, input, textarea, select, summary, [role=button], [contenteditable=true]";

/** Horizontal intent must win before a gesture takes ownership from scrolling. */
export function useConversationBubbleGesture(enabled: boolean, conversationId: string, scrollRef: RefObject<HTMLDivElement | null>) {
  const [offset, setOffset] = useState(0);
  const [dragging, setDragging] = useState(false);
  const current = useRef(0);
  const update = (value: number) => { current.current = value; setOffset(value); };
  useEffect(() => { update(0); setDragging(false); }, [conversationId, enabled]);
  useEffect(() => {
    const node = scrollRef.current;
    if (!enabled || !node) return;
    let drag: { id: number; x: number; y: number; start: number; active: boolean } | undefined;
    let wheelDistance = 0;
    let suppressClick = false;
    let wheelTimer: ReturnType<typeof setTimeout> | undefined;
    const excluded = (target: EventTarget | null) => !(target instanceof Element) || !!target.closest(interactive);
    const nestedHorizontalScroll = (target: EventTarget | null) => {
      for (let el = target instanceof Element ? target : null; el && el !== node; el = el.parentElement) {
        if (el.scrollWidth > el.clientWidth + 1 && /auto|scroll/.test(getComputedStyle(el).overflowX)) return true;
      }
      return false;
    };
    const down = (event: PointerEvent) => {
      if (event.button !== 0 || !event.isPrimary || excluded(event.target) || nestedHorizontalScroll(event.target)) return;
      // Mouse drags within prose remain native text selection. Touch can begin
      // on prose; buttons, links, code scrollers and cards retain their actions.
      if (event.pointerType === "mouse" && (event.target as Element).closest(".agent-text, .user-message, .plugin-conversation-attachments")) return;
      suppressClick = false;
      drag = { id: event.pointerId, x: event.clientX, y: event.clientY, start: current.current, active: false };
    };
    const move = (event: PointerEvent) => {
      if (!drag || event.pointerId !== drag.id) return;
      const dx = drag.x - event.clientX, dy = event.clientY - drag.y;
      if (!drag.active) {
        if (Math.abs(dy) > 8 && Math.abs(dy) >= Math.abs(dx)) { drag = undefined; return; }
        if (Math.abs(dx) < 10 || Math.abs(dx) < Math.abs(dy) * 1.5) return;
        drag.active = true;
        setDragging(true);
        node.setPointerCapture(event.pointerId);
      }
      event.preventDefault();
      update(Math.max(0, Math.min(REVEAL_DISTANCE, drag.start + dx)));
    };
    const end = (event: PointerEvent) => {
      if (!drag || drag.id !== event.pointerId) return;
      const previous = drag;
      drag = undefined;
      setDragging(false);
      suppressClick = previous.active;
      update(event.type === "pointercancel" ? previous.start : current.current >= REVEAL_DISTANCE / 2 ? REVEAL_DISTANCE : 0);
      if (node.hasPointerCapture(event.pointerId)) node.releasePointerCapture(event.pointerId);
    };
    const wheel = (event: WheelEvent) => {
      const horizontalIntent = Math.abs(event.deltaX) >= Math.abs(event.deltaY) * 1.5;
      if (!horizontalIntent) { update(0); return; }
      if (event.ctrlKey || event.shiftKey || excluded(event.target) || nestedHorizontalScroll(event.target) || event.deltaX === 0) return;
      event.preventDefault();
      wheelDistance += event.deltaX;
      if (Math.abs(wheelDistance) >= 24) { update(wheelDistance > 0 ? REVEAL_DISTANCE : 0); wheelDistance = 0; }
      clearTimeout(wheelTimer);
      wheelTimer = setTimeout(() => { wheelDistance = 0; }, 180);
    };
    const click = (event: MouseEvent) => {
      if (suppressClick) { suppressClick = false; return; }
      if (!excluded(event.target) && !(event.target as Element).closest(".bubble-message-content")) update(0);
    };
    // The scroll controller stops bubbling native keys before React delegation.
    const key = (event: KeyboardEvent) => {
      if (!enabled || (event.target instanceof Element && event.target.closest("input, textarea, select, [contenteditable=true]"))) return;
      if (event.altKey && (event.key === "ArrowLeft" || event.key === "ArrowRight")) {
        event.preventDefault(); update(event.key === "ArrowLeft" ? REVEAL_DISTANCE : 0);
      } else if (event.key === "Escape" && current.current) { event.preventDefault(); update(0); }
    };
    node.addEventListener("keydown", key);
    node.addEventListener("click", click);
    node.addEventListener("pointerdown", down);
    node.addEventListener("pointermove", move, { passive: false });
    node.addEventListener("pointerup", end);
    node.addEventListener("pointercancel", end);
    node.addEventListener("wheel", wheel, { passive: false });
    return () => {
      clearTimeout(wheelTimer);
      node.removeEventListener("keydown", key);
      node.removeEventListener("click", click);
      node.removeEventListener("pointerdown", down); node.removeEventListener("pointermove", move);
      node.removeEventListener("pointerup", end); node.removeEventListener("pointercancel", end); node.removeEventListener("wheel", wheel);
    };
  }, [enabled, conversationId, scrollRef]);
  return { progress: offset / REVEAL_DISTANCE, dragging, revealed: offset >= REVEAL_DISTANCE / 2, toggle: () => update(current.current ? 0 : REVEAL_DISTANCE) };
}
