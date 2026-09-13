import type { CSSProperties } from "react";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";
import "./styles/room-coordinator-avatar.css";

export function RoomCoordinatorAvatar({ size = 32, activity = "idle" }: {
  size?: number;
  activity?: WuuMascotActivity;
}): JSX.Element {
  return <span className="channel-coordinator-mascot" aria-hidden="true" style={{ width: size, height: size, flexBasis: size }}>
    <WuuMascot size={size} brand accessory="none" showActivityProp={false} activity={activity}
      style={{ "--mo-head": "var(--channel-coordinator-body)", "--mo-eye": "var(--channel-coordinator-eyes)" } as CSSProperties} />
  </span>;
}
