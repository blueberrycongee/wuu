import { Children, isValidElement, useEffect, useLayoutEffect, useState, type ReactElement, type ReactNode } from "react";
import { motionDurationMs, prefersReducedMotion } from "./motion";

/** Retain departing activities only for their exit; their controls stop immediately. */
export function ChannelActivityPresence({ children }: { children: ReactNode }): JSX.Element {
  const current = Children.toArray(children).filter(isValidElement);
  const signature = current.map(node => node.key).join("\n");
  const [retained, setRetained] = useState<ReactElement[]>(current);
  useLayoutEffect(() => {
    setRetained(previous => {
      const next = [...current];
      previous.forEach((node, index) => {
        if (!next.some(item => item.key === node.key)) next.splice(Math.min(index, next.length), 0, node);
      });
      return next;
    });
    // Content updates use the current elements below; only membership changes
    // start an exit so streamed updates cannot extend a departing activity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature]);
  useEffect(() => {
    if (retained.every(node => current.some(item => item.key === node.key))) return;
    const finish = () => setRetained(previous => previous.filter(node => current.some(item => item.key === node.key)));
    const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const timer = window.setTimeout(finish, prefersReducedMotion() ? 0 : motionDurationMs("--motion-normal", 180));
    const reduce = () => { if (media?.matches) finish(); };
    media?.addEventListener("change", reduce);
    return () => { window.clearTimeout(timer); media?.removeEventListener("change", reduce); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature]);
  return <>{retained.map(node => {
    const live = current.find(item => item.key === node.key);
    return <div key={node.key} className="channel-activity-slot" data-leaving={!live || undefined}
      aria-hidden={!live || undefined} inert={!live || undefined}>
      <div className="channel-activity-slot-content">{live ?? node}</div>
    </div>;
  })}</>;
}
