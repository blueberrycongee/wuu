import type { HTMLAttributes } from "react";

/** The reading column is shared by Coding and independent conversation transports. */
export function ConversationFlow({ className, ...props }: HTMLAttributes<HTMLDivElement>): JSX.Element {
  return <div {...props} className={["conversation-width session-flow", className].filter(Boolean).join(" ")} />;
}
