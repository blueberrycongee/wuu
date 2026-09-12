import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";

export function ChannelAgentHoverCard({ id, name, avatarKey, avatarImage, children, onInspect }: {
  id: string;
  name: string;
  avatarKey: string;
  avatarImage?: string;
  children?: ReactNode;
  onInspect: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLSpanElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const cardID = useId();
  const cancelClose = () => clearTimeout(closeTimer.current);
  const show = () => { cancelClose(); setOpen(true); };
  const scheduleClose = () => {
    cancelClose();
    closeTimer.current = setTimeout(() => {
      if (!anchor.current?.contains(document.activeElement) && !card.current?.contains(document.activeElement)) setOpen(false);
    }, 180);
  };
  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !anchor.current?.contains(event.target) && !card.current?.contains(event.target)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      if (card.current?.contains(document.activeElement)) anchor.current?.querySelector<HTMLElement>("button")?.focus();
      setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape, true);
    };
  }, [open]);
  const avatar = <AgentAvatarMark seed={id} avatarKey={avatarKey} avatarImage={avatarImage} status="idle" />;
  return <span ref={anchor} className="channel-agent-hover-anchor" onMouseEnter={show} onMouseLeave={scheduleClose} onFocus={show} onBlur={scheduleClose}>
    {children ?? <button type="button" className="channel-agent-hover-trigger" aria-label={name} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? cardID : undefined} onClick={event => { show(); if (event.detail === 0) requestAnimationFrame(() => card.current?.querySelector("button")?.focus()); }}>{avatar}</button>}
    {open ? <FloatingMenuPortal anchorRef={anchor} owner="channel-agent" placement="below" align="left" width={240} flip>
      <div ref={card} id={cardID} role="dialog" aria-label={name} className="channel-agent-hover-card" onMouseEnter={cancelClose} onMouseLeave={scheduleClose} onFocus={cancelClose} onBlur={scheduleClose}>
        <div className="channel-agent-hover-identity"><span>{avatar}</span><strong>{name}</strong></div>
        <button type="button" className="channel-agent-view-trace" onClick={() => { anchor.current?.querySelector<HTMLElement>("button")?.focus(); setOpen(false); onInspect(); }}>{t("channels.viewTrace")}</button>
      </div>
    </FloatingMenuPortal> : null}
  </span>;
}
