import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { WuuMascot, type WuuMascotActivity } from "../../../../desktop/src/renderer/WuuMascot";
import { summarize } from "./process";
import type { ThreadItem } from "../../../../desktop/src/shared/protocol";
import "./native.css";

// This bundle contains presentation only: no account, file, network or RPC bridge.
type AvatarProps = {
  conversation?: boolean;
  tools?: ThreadItem[];
  active?: boolean;
  width?: number;
  fontSize?: number;
  activity?: WuuMascotActivity;
  provider?: string;
  model?: string;
  dark?: boolean;
  size: number;
  paused?: boolean;
  reducedMotion?: boolean;
};
const root = createRoot(document.getElementById("root")!);
const media = window.matchMedia.bind(window);
let reducedMotion = false;
// Native accessibility settings also apply on WebViews whose media query is stale.
window.matchMedia = (query: string) => query === "(prefers-reduced-motion: reduce)" && reducedMotion
  ? { ...media(query), matches: true, media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => true } as MediaQueryList
  : media(query);
window.renderWuuAvatar = (props: AvatarProps) => {
  // Embedded surfaces can start with a zero layout viewport even after native layout.
  const size = Math.max(1, Math.min(512, props.size || 40));
  document.documentElement.style.height = `${size}px`;
  document.documentElement.style.width = `${props.tools ? props.width : size}px`;
  reducedMotion = Boolean(props.reducedMotion);
  document.documentElement.toggleAttribute("data-renderer-hidden", Boolean(props.paused));
  document.documentElement.toggleAttribute("data-reduced-motion", reducedMotion);
  document.documentElement.dataset.theme = props.dark ? "dark" : "light";
  const summary = props.tools ? summarize(props.tools) : undefined;
  // A reduced-motion change remounts the effects so they observe the new setting.
  flushSync(() => root.render(<div className="native-avatar" key={String(reducedMotion)}>
    {props.tools ? <div className="native-process" style={{ fontSize: props.fontSize ?? 14 }}>
      {props.active && <WuuMascot visible size={28} activity={summary?.activity} provider={props.provider} model={props.model} />}
      <span style={{ color: summary?.failed ? "var(--status-error, #c33)" : undefined }}>{summary?.text}</span>
    </div> : props.conversation ? <WuuMascot visible size={size} activity={props.activity ?? "thinking"} provider={props.provider} model={props.model} /> : null}
  </div>));
  return document.querySelector(".native-process")?.getBoundingClientRect().height ?? size;
};
declare global { interface Window { renderWuuAvatar: (props: AvatarProps) => number } }
