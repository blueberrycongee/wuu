import type { ReactNode } from "react";

export function MessageBubbleRow({
  outgoing,
  avatar,
  meta,
  children,
  footer,
  className = "",
  contentClassName = "",
  messageID,
}: {
  outgoing: boolean;
  avatar?: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
  contentClassName?: string;
  messageID?: string;
}): JSX.Element {
  return (
    <article
      data-message-id={messageID}
      className={`chat-row ${outgoing ? "chat-row--user" : "chat-row--participant"}${className ? ` ${className}` : ""}`}
    >
      {!outgoing && avatar ? <div className="chat-avatar-slot">{avatar}</div> : null}
      <div className={`chat-bubble-group${contentClassName ? ` ${contentClassName}` : ""}`}>
        {meta}
        {children}
        {footer}
      </div>
    </article>
  );
}

export function MessageBubble({
  outgoing,
  children,
  className = "",
}: {
  outgoing: boolean;
  children: ReactNode;
  className?: string;
}): JSX.Element {
  return (
    <div className={`chat-bubble${outgoing ? " chat-bubble--user" : ""}${className ? ` ${className}` : ""}`}>
      {children}
    </div>
  );
}
