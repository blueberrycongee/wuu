import "blobatar/motion.css";
import type { Expression } from "blobatar/expression";
import type { JSX } from "react";
import { AVATAR_HUES } from "./DefaultAvatar";
import { WuuMascot, wuuMascotExpression, type WuuMascotAccessory } from "./WuuMascot";
import { WUU_MASCOT_TRAITS } from "./wuu-mascot-spec";
import "./styles/agent-avatar-feedback.css";

export const AGENT_AVATAR_KEYS = [
  "abstract-1",
  "abstract-2",
  "abstract-3",
  "abstract-4",
  "abstract-5",
  "abstract-6",
  "abstract-7",
  "abstract-8",
  "abstract-9",
] as const;

export type AgentAvatarKey = (typeof AGENT_AVATAR_KEYS)[number];

export type AgentAvatarStatus = "idle" | "thinking" | "sending" | "responding" | "queued" | "waiting" | "failed" | "interrupted";

const EXPRESSIONS: Record<AgentAvatarStatus, Expression> = {
  idle: wuuMascotExpression(),
  thinking: wuuMascotExpression({ esy: 0.94, tilt: 2 }),
  responding: wuuMascotExpression({ esy: 1.04, tilt: -1 }),
  sending: wuuMascotExpression({ esy: 1.04, tilt: -1 }),
  queued: wuuMascotExpression({ esy: 0.96 }),
  waiting: wuuMascotExpression({ esy: 0.92, esy2: -0.03 }),
  failed: wuuMascotExpression({ esy: 0.9, tilt: 2 }),
  interrupted: wuuMascotExpression({ esy: 0.92 }),
};

function AgentAvatarFeedback({ status, active }: { status: AgentAvatarStatus; active: boolean }): JSX.Element | null {
  if (status === "idle") return null;
  if (active) {
    return (
      <svg className="agent-avatar-feedback-orbit" data-agent-avatar-feedback="active" viewBox="0 0 48 48" aria-hidden="true">
        <circle cx="24" cy="24" r="22" strokeDasharray="25 113.2" />
        <circle cx="24" cy="2" r="2" className="agent-avatar-feedback-dot" />
      </svg>
    );
  }
  return (
    <svg className="agent-avatar-feedback-marker" data-agent-avatar-feedback={status} viewBox="0 0 20 20" aria-hidden="true">
      <circle className="agent-avatar-feedback-marker-background" cx="10" cy="10" r="8.5" />
      {status === "failed" ? <><path d="M10 5.5v5" /><circle className="agent-avatar-feedback-dot" cx="10" cy="14" r="0.8" /></>
        : status === "queued" ? <path d="M10 5.5V10l3 2" />
          : <path d="M7.5 6.5v7m5-7v7" />}
    </svg>
  );
}

export const AGENT_AVATAR_SHAPES = [
  { id: "round", trait: 0.12 },
  { id: "organic", trait: 0.42 },
  { id: "boxy", trait: 0.65 },
  { id: "nub", trait: 0.78 },
  { id: "cloud", trait: 0.88 },
  { id: "sun", trait: 0.97 },
] as const;

export const AGENT_AVATAR_ACCESSORIES = [
  "none",
  "cap",
  "beanie",
  "top-hat",
  "sprout",
  "crown",
  "headphones",
  "scarf",
  "beret",
  "party-hat",
  "wizard-hat",
  "chef-hat",
  "flower",
  "halo",
  "bow-tie",
  "graduation-cap",
  "cowboy-hat",
  "propeller-cap",
  "mushroom-cap",
  "bunny-ears",
  "cat-ears",
  "ribbon",
  "necktie",
] as const satisfies readonly WuuMascotAccessory[];

export type AgentAvatarShape = (typeof AGENT_AVATAR_SHAPES)[number]["id"];

export type AgentAvatarConfig = {
  shape: AgentAvatarShape;
  accessory: WuuMascotAccessory;
  hue: number;
};

const AGENT_AVATAR_CONFIG_PREFIX = "mascot-v1";
const DEFAULT_AGENT_AVATAR_CONFIG: AgentAvatarConfig = { shape: "round", accessory: "none", hue: AVATAR_HUES[0] };

export function isAgentAvatarKey(value: string): value is AgentAvatarKey {
  return AGENT_AVATAR_KEYS.some((key) => key === value);
}

export function randomAgentAvatarKey(): string {
  const value = new Uint32Array(1);
  window.crypto.getRandomValues(value);
  const shape = AGENT_AVATAR_SHAPES[value[0] % AGENT_AVATAR_SHAPES.length].id;
  const hue = AVATAR_HUES[Math.floor(value[0] / AGENT_AVATAR_SHAPES.length) % AVATAR_HUES.length];
  return serializeAgentAvatarConfig({ shape, accessory: "none", hue });
}

export function parseAgentAvatarConfig(value: string): AgentAvatarConfig | null {
  const [prefix, shape, accessory, rawHue, ...rest] = value.split(":");
  if (prefix !== AGENT_AVATAR_CONFIG_PREFIX || rest.length > 0) return null;
  if (!AGENT_AVATAR_SHAPES.some((item) => item.id === shape)) return null;
  if (!AGENT_AVATAR_ACCESSORIES.some((item) => item === accessory)) return null;
  const hue = Number(rawHue);
  if (!Number.isInteger(hue) || hue < 0 || hue > 359) return null;
  return { shape: shape as AgentAvatarShape, accessory: accessory as WuuMascotAccessory, hue };
}

export function serializeAgentAvatarConfig(config: AgentAvatarConfig): string {
  return `${AGENT_AVATAR_CONFIG_PREFIX}:${config.shape}:${config.accessory}:${Math.round(config.hue)}`;
}

export function agentAvatarConfig(value: string): AgentAvatarConfig {
  const configured = parseAgentAvatarConfig(value);
  if (configured) return configured;
  if (isAgentAvatarKey(value)) {
    const index = AGENT_AVATAR_KEYS.indexOf(value);
    return {
      shape: AGENT_AVATAR_SHAPES[index % AGENT_AVATAR_SHAPES.length].id,
      accessory: "none",
      hue: AVATAR_HUES[index % AVATAR_HUES.length],
    };
  }
  return DEFAULT_AGENT_AVATAR_CONFIG;
}

export function AgentAvatarMark({ seed, avatarKey, avatarImage, status = "idle" }: {
  seed: string;
  avatarKey: string;
  avatarImage?: string;
  status?: AgentAvatarStatus;
}): JSX.Element {
  const active = status === "thinking" || status === "responding" || status === "sending";
  const config = agentAvatarConfig(avatarKey);
  const shape = AGENT_AVATAR_SHAPES.find((item) => item.id === config.shape) ?? AGENT_AVATAR_SHAPES[0];
  return (
    <span className="agent-avatar-mark" data-agent-avatar-id={seed} data-agent-avatar-state={status} aria-hidden="true">
      {avatarImage ? <img className="agent-avatar-image" src={avatarImage} alt="" draggable={false} /> : <WuuMascot
        identityName={`agent-avatar:${avatarKey}`}
        identityHue={config.hue}
        identityTraits={{ ...WUU_MASCOT_TRAITS, shape: shape.trait }}
        accessory={config.accessory}
        activity={status === "thinking" ? "thinking" : status === "sending" || status === "responding" ? "compose" : "idle"}
        animate={active ? "always" : "hover"}
        expression={EXPRESSIONS[status]}
        showActivityProp={false}
      />}
      <AgentAvatarFeedback status={status} active={active} />
    </span>
  );
}
