import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { ChromeCapsuleModel, ChromeOverlayCommand } from "../shared/chromeOverlay";
import { ArrowUp, ArrowUpRight, MessageCircle } from "./WuuIcons";
import { WuuMascot } from "./WuuMascot";
import "./styles/context-chat-capsule.css";

/** Shared rendering only. The native website surface has no general Wuu API. */
export function ContextChatCapsuleSurface({ model, send }: { model: ChromeCapsuleModel; send: (command: ChromeOverlayCommand) => void }): JSX.Element {
  const [draft, setDraft] = useState(model.draft);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const ball = useRef<HTMLButtonElement>(null);
  const bodyID = useId();
  const input = useRef<HTMLInputElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const composing = useRef(false);
  const previousBusy = useRef(model.busy);
  useLayoutEffect(() => {
    if (!focused || previousBusy.current && !model.busy && !model.error) setDraft(model.draft);
    previousBusy.current = model.busy;
  }, [model.draft, model.busy, model.error]);
  useLayoutEffect(() => {
    if (!model.expanded) return;
    if (model.menuOpen) menu.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
    else input.current?.focus();
  }, [model.expanded, model.menuOpen]);
  const collapse = (restore = false) => {
    send({ type: "capsule-collapse", text: draft });
    if (restore && !("wuuChromeOverlay" in window)) {
      (returnFocus.current?.isConnected ? returnFocus.current : ball.current)?.focus({ preventScroll: true });
    }
  };
  // Crossing native WebContents does not produce a DOM focusout target.
  const latest = useRef({ expanded: model.expanded, collapse });
  latest.current = { expanded: model.expanded, collapse };
  useEffect(() => {
    const blur = () => {
      setHovered(false);
      setFocused(false);
      if (latest.current.expanded) latest.current.collapse();
    };
    const outside = (event: PointerEvent) => {
      if (latest.current.expanded && !root.current?.contains(event.target as Node)) latest.current.collapse();
    };
    window.addEventListener("blur", blur);
    document.addEventListener("pointerdown", outside, true);
    return () => { window.removeEventListener("blur", blur); document.removeEventListener("pointerdown", outside, true); };
  }, []);
  const rememberFocus = () => {
    if (!model.expanded && document.activeElement instanceof HTMLElement && !root.current?.contains(document.activeElement)) {
      returnFocus.current = document.activeElement;
    }
  };
  return <div ref={root} data-context-capsule className={`context-chat-capsule${model.expanded ? " expanded" : ""}`}
    data-menu-open={model.menuOpen || undefined} data-busy={model.busy || undefined}
    onPointerEnter={() => setHovered(true)} onPointerLeave={() => setHovered(false)}
    onFocus={event => {
      setFocused(true);
      if (!model.expanded && event.relatedTarget instanceof HTMLElement && !root.current?.contains(event.relatedTarget)) returnFocus.current = event.relatedTarget;
    }}
    onBlur={event => {
      if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
      setFocused(false);
      send({ type: "capsule-draft", text: draft });
      if (event.relatedTarget && model.expanded) collapse();
    }}
    onKeyDown={event => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); collapse(true); }
      if (event.target === ball.current && (event.key === "ContextMenu" || event.shiftKey && event.key === "F10")) {
        event.preventDefault(); send({ type: "capsule-menu" });
      }
      if (event.key === "Tab" && "wuuChromeOverlay" in window) {
        const controls = [...event.currentTarget.querySelectorAll<HTMLElement>("button:not(:disabled), input")];
        if (document.activeElement === controls[event.shiftKey ? 0 : controls.length - 1]) {
          event.preventDefault(); collapse(true);
        }
      }
    }}>
    <div id={bodyID} className="context-chat-capsule-body">
      {model.menuOpen && <div ref={menu} className="context-chat-capsule-menu" role="menu" aria-label={model.label}
        onKeyDown={event => {
          // Consume activation before replacing the menu with a focused form.
          // Otherwise the same Enter can submit the newly mounted editor.
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault(); event.stopPropagation();
            (document.activeElement as HTMLButtonElement).click();
            return;
          }
          if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
          event.preventDefault();
          const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
          const index = items.indexOf(document.activeElement as HTMLButtonElement);
          items[event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
            : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
        }}>
        <button type="button" role="menuitem" onClick={() => send({ type: "capsule-expand" })}><MessageCircle aria-hidden="true" />{model.label}</button>
        <button type="button" role="menuitem" disabled={!model.sessionId} onClick={() => send({ type: "capsule-open" })}><ArrowUpRight aria-hidden="true" />{model.openLabel}</button>
      </div>}
      {model.expanded && !model.menuOpen && (model.error || model.sessionId) && <div className="context-chat-capsule-status">
        <span role={model.error ? "alert" : "status"} title={model.error ?? model.sourceTitle}>{model.error ?? model.sourceTitle}</span>
        {model.sessionId && <button type="button" onClick={() => send({ type: "capsule-open" })}>{model.openLabel}<ArrowUpRight aria-hidden="true" /></button>}
      </div>}
      <form className="context-chat-capsule-row" onSubmit={event => { event.preventDefault(); if (!composing.current && draft.trim() && !model.busy) send({ type: "capsule-send", text: draft }); }}>
        {model.expanded && !model.menuOpen && <>
          <input ref={input} value={draft} maxLength={8000} aria-label={model.placeholder} placeholder={model.placeholder}
            onCompositionStart={() => { composing.current = true; }} onCompositionEnd={() => { composing.current = false; }}
            onChange={event => { setDraft(event.target.value); send({ type: "capsule-draft", text: event.target.value }); }}
            readOnly={model.busy} aria-busy={model.busy} />
          <button type="submit" className="context-chat-capsule-send" aria-label={model.sendLabel} disabled={model.busy || !draft.trim()}>
            {model.busy ? <span className="context-chat-capsule-busy" /> : <ArrowUp aria-hidden="true" />}
          </button>
        </>}
        <button ref={ball} type="button" className="context-chat-capsule-ball" aria-label={model.expanded ? model.collapseLabel : model.label}
          aria-expanded={model.expanded} aria-controls={bodyID} title={model.expanded ? model.collapseLabel : model.label}
          onPointerDown={rememberFocus}
          onContextMenu={event => { event.preventDefault(); rememberFocus(); send({ type: "capsule-menu" }); }}
          onClick={() => { if (model.expanded) collapse(true); else send({ type: "capsule-expand" }); }}>
          <WuuMascot brand size={48} disableMorph showActivityProp={false} followPointer={hovered && !model.expanded}
            activity={model.busy ? "sending" : model.error ? "failed" : model.expanded ? "compose" : "idle"}
            motionPaused={!hovered && !focused && !model.busy} animate="always" aria-hidden="true" />
          {!model.expanded && draft && <span className="context-chat-capsule-draft" />}
        </button>
      </form>
    </div>
  </div>;
}
