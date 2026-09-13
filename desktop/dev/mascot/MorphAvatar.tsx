import { agentAvatarConfig, AGENT_AVATAR_SHAPES } from "../../src/renderer/AgentAvatarMark";
import { WuuMascot } from "../../src/renderer/WuuMascot";
import { WUU_MASCOT_TRAITS } from "../../src/renderer/wuu-mascot-spec";
export const MORPH_STATES = [
  ["dots", "省略号"], ["orbit", "环绕"], ["radar", "雷达"], ["progress", "进度"],
  ["gather", "聚集"], ["wave", "波形"], ["send", "发送"], ["receive", "接收"],
  ["dock", "上传"], ["ball", "弹跳"], ["whirl", "旋涡"], ["pencil", "书写"],
  ["bang", "警报"], ["standby", "休眠"], ["scan", "阅读"], ["terminal", "命令"],
] as const;
export const CHARACTER_STATES = [
  ["idle", "就绪"], ["liquid", "液滴"], ["responding", "回复"], ["queued", "排队"],
  ["waiting", "等待"], ["failed", "失败"], ["interrupted", "中断"],
] as const;
export const ALL_MOTION_STATES = [...MORPH_STATES, ...CHARACTER_STATES];
export type PreviewMotion = (typeof ALL_MOTION_STATES)[number][0];


export function MorphAvatar({ avatarKey, mode, paused = false, replay = 0 }: { avatarKey: string; mode: PreviewMotion; paused?: boolean; replay?: number }) {
  const config = agentAvatarConfig(avatarKey);
  const shape = AGENT_AVATAR_SHAPES.find(item => item.id === config.shape)!;
  return <span className="morph-avatar" data-preview-motion={mode} aria-hidden="true">
    <WuuMascot identityName={`agent-avatar:${avatarKey}`} identityHue={config.hue} identityTraits={{ ...WUU_MASCOT_TRAITS, shape: shape.trait }} accessory={config.accessory} morph={mode} motionPaused={paused} motionReplay={replay} animate="hover" />
  </span>;
}
