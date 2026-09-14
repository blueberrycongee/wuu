import { createRoot } from "react-dom/client";
import { AgentAvatarMark, type AgentAvatarStatus } from "../../../../desktop/src/renderer/AgentAvatarMark";
import { ChannelGroupAvatar } from "../../../../desktop/src/renderer/ChannelGroupAvatar";
import type { ChannelRoom, NamedAgent } from "../../../../packages/protocol/src";
import "../../../../desktop/src/renderer/styles/default-avatar.css";
import "./native.css";

// This bundle contains presentation only: no account, file, network or RPC bridge.
// Native pages pass the same public agent/room records used by ChannelView.
type AvatarProps = {
  agent?: NamedAgent;
  room?: ChannelRoom;
  agents?: NamedAgent[];
  status?: AgentAvatarStatus;
  subtle?: boolean;
  turnSignal?: number;
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
function embeddedImage(value?: string): string | undefined {
  return value && value.length <= 3 * 1024 * 1024 && /^data:image\/(png|jpeg|gif|webp);base64,[a-z0-9+/=\s]+$/i.test(value) ? value : undefined;
}
function agentRecord(agent: NamedAgent): NamedAgent { return { ...agent, avatar_image: embeddedImage(agent.avatar_image) }; }
window.renderWuuAvatar = (props: AvatarProps) => {
  // Embedded surfaces can start with a zero layout viewport even after native layout.
  const size = Math.max(1, Math.min(512, props.size || 40));
  document.documentElement.style.height = `${size}px`;
  document.documentElement.style.width = `${size}px`;
  reducedMotion = Boolean(props.reducedMotion);
  document.documentElement.toggleAttribute("data-renderer-hidden", Boolean(props.paused));
  document.documentElement.toggleAttribute("data-reduced-motion", reducedMotion);
  document.documentElement.dataset.theme = props.dark ? "dark" : "light";
  const room = props.room;
  const agents = (props.agents ?? []).map(agentRecord);
  const agent = props.agent ? agentRecord(props.agent) : room?.kind === "dm"
    ? agents.find(agent => room.members.some(member => member.member_type === "agent" && member.member_id === agent.id)) : undefined;
  const status = props.status ?? (agent?.activity_status === "thinking" ? "thinking" : "idle");
  // A reduced-motion change remounts the effects so they observe the new setting.
  root.render(<div className="native-avatar" key={String(reducedMotion)}>
    {room && room.kind !== "dm" ? <ChannelGroupAvatar room={{ ...room, avatar_image: embeddedImage(room.avatar_image) }} agents={agents} /> :
      <AgentAvatarMark seed={agent?.id ?? "wuu"} avatarKey={agent?.avatar_key ?? "abstract-1"} avatarImage={agent?.avatar_image}
        status={status} motion={props.subtle ? "subtle" : "expressive"} turnSignal={props.turnSignal ?? 0} />}
  </div>);
};
declare global { interface Window { renderWuuAvatar: (props: AvatarProps) => void } }
