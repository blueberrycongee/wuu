import type { PluginUIAvatarProps } from "../shared/workbench";
import { AgentAvatarMark, serializeAgentAvatarConfig } from "./AgentAvatarMark";
import "./styles/plugin-avatar.css";

const PLUGIN_AVATAR_IDENTITY = {
  seed: "wuu-mascot",
  avatarKey: serializeAgentAvatarConfig({ shape: "round", accessory: "headset", hue: 222 }),
} as const;

/** The public mascot icon and plugin identity surfaces share one authored character. */
export function PluginAvatar({
  size,
  status = "idle",
  motion = "static",
  className,
}: PluginUIAvatarProps): JSX.Element {
  return (
    <span
      className={`plugin-ui-avatar${className ? ` ${className}` : ""}`}
      data-wuu-component="plugin-ui-avatar"
      data-plugin-avatar=""
      data-icon="mascot"
      aria-hidden="true"
      style={size === undefined ? undefined : { width: size, height: size }}
    >
      <AgentAvatarMark
        {...PLUGIN_AVATAR_IDENTITY}
        status={status}
        motion={motion === "ambient" ? "expressive" : "static"}
        disableMorph
      />
    </span>
  );
}
