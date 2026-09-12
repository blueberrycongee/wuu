import { ListTree, Pencil } from "lucide-react";
import { effortLabel } from "./RuntimeHelpers";
import { useEffect, useId, useRef, useState } from "react";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";

export function ChannelAgentHoverCard({ id, name, avatarKey, avatarImage, model, effort, onInspect, onEdit }: {
  id: string;
  name: string;
  avatarKey: string;
  avatarImage?: string;
  model?: string;
  effort?: string;
  onInspect: () => void;
  onEdit?: () => void;
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
  const activate = (action: () => void) => {
    anchor.current?.querySelector<HTMLElement>("button")?.focus();
    setOpen(false);
    action();
  };
  const avatar = <AgentAvatarMark seed={id} avatarKey={avatarKey} avatarImage={avatarImage} status="idle" />;
  return <span ref={anchor} className="channel-agent-hover-anchor" onMouseEnter={show} onMouseLeave={scheduleClose} onFocus={show} onBlur={scheduleClose}>
    <button type="button" className="channel-agent-hover-trigger" aria-label={name} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? cardID : undefined} onClick={event => { show(); if (event.detail === 0) requestAnimationFrame(() => card.current?.querySelector("button")?.focus()); }}>{avatar}</button>
    {open ? <FloatingMenuPortal anchorRef={anchor} owner="channel-agent" placement="below" align="left" width={264} flip>
      <div ref={card} id={cardID} role="dialog" aria-label={name} className="channel-agent-hover-card" onMouseEnter={cancelClose} onMouseLeave={scheduleClose} onFocus={cancelClose} onBlur={scheduleClose}>
        <div className="channel-agent-hover-identity">
          <span className="channel-agent-hover-avatar">{avatar}</span>
          <div className="channel-agent-hover-info">
            <strong>{name}</strong>
            <div className="channel-agent-hover-runtime">
              <span aria-label={t("channels.model")}>{model || t("channels.inheritModel")}</span>
              <span className="channel-agent-hover-effort" aria-label={t("channels.effort")}>{effortLabel(effort ?? "")}</span>
            </div>
          </div>
        </div>
        <div className="channel-agent-hover-actions">
          <button type="button" className="channel-agent-view-trace" onClick={() => activate(onInspect)}><ListTree aria-hidden="true" />{t("channels.viewTrace")}</button>
          {onEdit ? <button type="button" className="channel-agent-hover-edit" onClick={() => activate(onEdit)}><Pencil aria-hidden="true" />{t("common.edit")}</button> : null}
        </div>
      </div>
    </FloatingMenuPortal> : null}
  </span>;
}
