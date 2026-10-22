import * as React from "react";
import { useLayoutEffect, useRef, useState } from "react";
import type { PluginUIConversationMessageProps } from "../../shared/workbench";
import { UILayerPortal } from "../ui/layers/UILayerHost";

const choices = ["❤️", "😂", "😮", "😢", "👍", "✅"];
/** Opt-in, transport-free presentation: choosing an emoji never submits text. */
export function ConversationReaction({ reaction }: { reaction: NonNullable<PluginUIConversationMessageProps["reaction"]> }): JSX.Element | null {
  const [open, setOpen] = useState(false), [position, setPosition] = useState({ top: 0, left: 0 });
  const trigger = useRef<HTMLButtonElement>(null), picker = useRef<HTMLDivElement>(null);
  const close = (restore = false) => { setOpen(false); if (restore) trigger.current?.focus({ preventScroll: true }); };
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      if (!trigger.current || !picker.current) return;
      const anchor = trigger.current.getBoundingClientRect(), box = picker.current.getBoundingClientRect();
      const view = window.visualViewport;
      const leftEdge = view?.offsetLeft ?? 0, topEdge = view?.offsetTop ?? 0;
      const right = leftEdge + (view?.width ?? innerWidth), bottom = topEdge + (view?.height ?? innerHeight);
      const top = anchor.top - box.height - 8 >= topEdge + 8 ? anchor.top - box.height - 8 : anchor.bottom + 8;
      setPosition({ left: Math.max(leftEdge + 8, Math.min(anchor.left - 12, right - box.width - 8)), top: Math.max(topEdge + 8, Math.min(top, bottom - box.height - 8)) });
    };
    place(); picker.current?.querySelector<HTMLButtonElement>('[aria-pressed="true"],button')?.focus({ preventScroll: true });
    const outside = (event: Event) => { if (!picker.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) close(); };
    const scroll = (event: Event) => { if (!picker.current?.contains(event.target as Node)) close(); };
    document.addEventListener("pointerdown", outside, true); document.addEventListener("scroll", scroll, true); window.addEventListener("resize", place); window.visualViewport?.addEventListener("resize", place);
    return () => { document.removeEventListener("pointerdown", outside, true); document.removeEventListener("scroll", scroll, true); window.removeEventListener("resize", place); window.visualViewport?.removeEventListener("resize", place); };
  }, [open]);
  if (!reaction.onChange && !reaction.emoji) return null;
  const badge = reaction.emoji ? "conversation-reaction-badge" : "conversation-reaction-trigger";
  return <>
    {reaction.onChange ? <button ref={trigger} type="button" className={badge} data-action="react-message" data-reaction-badge={reaction.emoji ? true : undefined} aria-label={`${reaction.label}${reaction.emoji ? `: ${reaction.emoji}` : ""}`} aria-haspopup="dialog" aria-expanded={open} aria-busy={reaction.pending} disabled={reaction.pending} onClick={() => setOpen(value => !value)}>{reaction.emoji || <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M8 14c1 4 7 4 8 0M8 9h1m6 0h1" /></svg>}</button>
      : <span className={badge} data-reaction-badge aria-label={`${reaction.label}: ${reaction.emoji}`} role="img">{reaction.emoji}</span>}
    {reaction.error && <span className="conversation-reaction-error" role="alert">{reaction.error}</span>}
    {open && <UILayerPortal layer="popover"><div ref={picker} className="conversation-reaction-picker" data-reaction-picker role="dialog" aria-label={reaction.label} style={position} onKeyDown={event => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(true); }
      if (["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) { event.preventDefault(); event.stopPropagation(); const buttons = [...picker.current!.querySelectorAll<HTMLButtonElement>("button")]; const i = buttons.indexOf(document.activeElement as HTMLButtonElement); buttons[event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (i + (event.key === "ArrowRight" ? 1 : -1) + buttons.length) % buttons.length]?.focus(); }
      if (event.key === "Tab") { event.preventDefault(); event.stopPropagation(); close(true); }
    }}>{choices.map(emoji => <button key={emoji} type="button" data-emoji={emoji} aria-label={emoji} aria-pressed={reaction.emoji === emoji} onClick={() => { reaction.onChange?.(reaction.emoji === emoji ? null : emoji); close(true); }}>{emoji}</button>)}{reaction.emoji && !choices.includes(reaction.emoji) && <button type="button" data-emoji={reaction.emoji} aria-label={reaction.emoji} aria-pressed onClick={() => { reaction.onChange?.(null); close(true); }}>{reaction.emoji}</button>}</div></UILayerPortal>}
  </>;
}
