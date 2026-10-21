import { useEffect, useRef, useState } from "react";
import { desktopPlatform } from "./platform";
import "./styles/mac-window-controls.css";

/** Hover capsule behind the native AppKit window buttons. */
export function MacWindowControls(): JSX.Element | null {
  const [revealed, setRevealed] = useState(false);
  const [title, setTitle] = useState(document.title);
  const chrome = useRef<HTMLDivElement>(null);
  const capsule = useRef<HTMLDivElement>(null);
  const visible = useRef(false);
  const pointerInside = useRef(false);
  const enabled = desktopPlatform() === "darwin" &&
    (window.wuu?.hostKind ?? "desktop") === "desktop" &&
    typeof window.wuu?.windowSetControls === "function";
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const clearLeave = () => { clearTimeout(leaveTimer.current); leaveTimer.current = undefined; };
  const scheduleHide = () => {
    if (leaveTimer.current) return;
    leaveTimer.current = setTimeout(() => { leaveTimer.current = undefined; setRevealed(false); }, 140);
  };
  useEffect(() => {
    const observer = new MutationObserver(() => setTitle(document.title));
    observer.observe(document.querySelector("title")!, { childList: true, subtree: true, characterData: true });
    setTitle(document.title);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const hide = () => { clearLeave(); setRevealed(false); };
    window.addEventListener("blur", hide);
    return () => { clearLeave(); window.removeEventListener("blur", hide); };
  }, []);
  useEffect(() => {
    if (!enabled) return;
    visible.current = revealed;
    let frame = 0;
    let disposed = false;
    let lastPosition = "";
    // AppKit buttons live outside the DOM; follow the capsule's animated bounds.
    const sync = async () => {
      const element = capsule.current!;
      const rect = element.getBoundingClientRect();
      const style = getComputedStyle(element);
      const animating = element.getAnimations().some(animation => animation.playState === "running");
      const state = { visible: revealed && parseFloat(style.opacity) >= 0.6,
        x: rect.left + parseFloat(style.paddingLeft),
        y: rect.top + parseFloat(style.paddingTop) };
      const position = JSON.stringify(state);
      if (position !== lastPosition) {
        lastPosition = position;
        await window.wuu.windowSetControls!(state);
      }
      if (!disposed && animating) {
        frame = requestAnimationFrame(() => { void sync(); });
      }
    };
    void sync();
    return () => { disposed = true; cancelAnimationFrame(frame); };
  }, [enabled, revealed]);
  useEffect(() => {
    if (!enabled) return;
    return () => { void window.wuu.windowSetControls!({ visible: false, x: 0, y: 0 }); };
  }, [enabled]);
  useEffect(() => {
    const readPointer = window.wuu?.windowPointerPosition;
    if (!enabled || !readPointer) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    // Native drag regions can leave DOM hover state stale after closing. Use
    // native coordinates for both entry and exit, including while hidden.
    const observe = async () => {
      const position = await readPointer();
      if (disposed) return;
      const bounds = chrome.current!.getBoundingClientRect();
      const pill = capsule.current!.getBoundingClientRect();
      const wasInside = pointerInside.current;
      pointerInside.current = position !== null && (
        (position.x >= bounds.left && position.x < bounds.right &&
          position.y >= bounds.top && position.y < bounds.bottom) ||
        (visible.current && position.x >= pill.left && position.x < pill.right &&
          position.y >= bounds.top && position.y < pill.bottom));
      if (pointerInside.current) {
        clearLeave();
        setRevealed(true);
      } else {
        if (wasInside) scheduleHide();
      }
      timer = setTimeout(observe, 80);
    };
    void observe();
    return () => { disposed = true; clearTimeout(timer); };
  }, [enabled]);
  if (!enabled) return null;

  return (
    <div ref={chrome} className="mac-window-chrome" data-revealed={revealed || undefined}>
      <div ref={capsule} className="mac-window-controls">
        <span className="mac-window-title">{title}</span>
      </div>
    </div>
  );
}
