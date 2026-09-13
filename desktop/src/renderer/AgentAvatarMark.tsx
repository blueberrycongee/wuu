import { useEffect, useId, useRef, useState, type CSSProperties, type JSX } from "react";
import { SHAPES } from "blobatar/blob";
import { AVATAR_HUES } from "./DefaultAvatar";
import { WuuMascot, WUU_MASCOT_ACCESSORIES, type WuuMascotAccessory } from "./WuuMascot";
import { WUU_MASCOT_TRAITS, type WuuMascotActivity } from "./wuu-mascot-spec";
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

const AGENT_TURN_MS = 1100;

/** Explicit inspection gestures retain their bounded lifetime across work sub-states. */
function useAgentAvatarTurn(status: AgentAvatarStatus, enabled: boolean, signal: number): number {
  const active = status === "thinking" || status === "responding" || status === "sending";
  const previous = useRef({ active: false, signal });
  const sequence = useRef(0);
  const [turn, setTurn] = useState(0);
  useEffect(() => {
    const requested = signal !== previous.current.signal;
    previous.current = { active, signal };
    if (!enabled || (!active && !requested)) { setTurn(0); return; }
    if (requested && !document.hidden && !window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) setTurn(current => current || ++sequence.current);
  }, [active, enabled, signal, status]);
  useEffect(() => {
    if (!turn) return;
    const cancel = () => setTurn(0);
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const timer = window.setTimeout(cancel, AGENT_TURN_MS);
    const hide = () => { if (document.hidden) cancel(); };
    reduced?.addEventListener("change", cancel);
    document.addEventListener("visibilitychange", hide);
    return () => { window.clearTimeout(timer); reduced?.removeEventListener("change", cancel); document.removeEventListener("visibilitychange", hide); };
  }, [turn]);
  return turn;
}

/** Two clipped halves let the same ribbon pass behind and in front of the body. */
function AgentAvatarRibbon({ front }: { front: boolean }): JSX.Element {
  const id = useId().replace(/:/g, "");
  return <svg className={`agent-avatar-ribbon ${front ? "front" : "rear"}`} viewBox="0 0 100 100" aria-hidden="true">
    <defs><clipPath id={id}><rect x="-20" y={front ? 57 : -20} width="140" height={front ? 63 : 77} /></clipPath></defs>
    <g transform="rotate(-16 50 57)"><g clipPath={`url(#${id})`}>
      <ellipse className="agent-avatar-ribbon-lead" cx="50" cy="57" rx="47" ry="13" pathLength="360" />
      <ellipse className="agent-avatar-ribbon-tail" cx="50" cy="57" rx="50" ry="19" pathLength="360" />
      <ellipse className="agent-avatar-ribbon-third" cx="50" cy="57" rx="48" ry="25" pathLength="360" />
      <ellipse className="agent-avatar-ribbon-fourth" cx="50" cy="57" rx="45" ry="31" pathLength="360" />
    </g></g>
  </svg>;
}

export const AGENT_AVATAR_SHAPES = SHAPES;

export const AGENT_AVATAR_ACCESSORIES = WUU_MASCOT_ACCESSORIES;

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
  const legacyShapes: Record<string, AgentAvatarShape> = {
    organic: "round", nub: "round", boxy: "rounded-square", cloud: "capsule", sun: "diamond",
  };
  const normalizedShape = AGENT_AVATAR_SHAPES.find(item => item.id === shape)?.id
    ?? (Object.hasOwn(legacyShapes, shape) ? legacyShapes[shape] : undefined);
  if (!normalizedShape) return null;
  if (!accessory) return null;
  const hue = Number(rawHue);
  if (!Number.isInteger(hue) || hue < 0 || hue > 359) return null;
  // Removed or newer accessories do not erase the rest of a saved identity.
  const selected = AGENT_AVATAR_ACCESSORIES.find(item => item === accessory) ?? "none";
  return { shape: normalizedShape, accessory: selected, hue };
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

export function AgentAvatarMark({ seed, avatarKey, avatarImage, status = "idle", activity, motion = "expressive", turnSignal = 0 }: {
  seed: string;
  avatarKey: string;
  avatarImage?: string;
  status?: AgentAvatarStatus;
  activity?: WuuMascotActivity;
  /** Secondary placements retain state cues without the working gaze loop. */
  motion?: "expressive" | "subtle";
  /** Increment to replay a deliberate character gesture, independent of status. */
  turnSignal?: number;
}): JSX.Element {
  const active = status === "thinking" || status === "responding" || status === "sending";
  const turn = useAgentAvatarTurn(status, motion === "expressive" && !avatarImage, turnSignal);
  const config = agentAvatarConfig(avatarKey);
  const shape = AGENT_AVATAR_SHAPES.find((item) => item.id === config.shape) ?? AGENT_AVATAR_SHAPES[0];
  const phase = Array.from(seed).reduce((hash, character) => (hash * 31 + character.charCodeAt(0)) >>> 0, 0) % 2600;
  return (
    <span className="agent-avatar-mark" data-agent-avatar-id={seed} data-agent-avatar-state={status} data-agent-avatar-motion={motion}
      data-agent-avatar-turn={turn || undefined} style={{ "--agent-avatar-hue": config.hue, "--agent-turn-duration": `${AGENT_TURN_MS}ms` } as CSSProperties} aria-hidden="true">
      {turn ? <AgentAvatarRibbon key={`rear-${turn}`} front={false} /> : null}
      {avatarImage ? <img className="agent-avatar-image" src={avatarImage} alt="" draggable={false} /> : <WuuMascot
        identityName={`agent-avatar:${avatarKey}`}
        identityHue={config.hue}
        identityTraits={{ ...WUU_MASCOT_TRAITS, shape: shape.trait }}
        accessory={config.accessory}
        activity={activity ?? status}
        motionPaused={motion === "subtle"}
        ambient={motion === "expressive"}
        idlePerspective={{ yaw: 0, pitch: 2, strength: 1 }}
        animate={active && motion === "expressive" ? "always" : "hover"}
        style={{
          "--agent-attention-delay": `${-phase}ms`,
          "--mo-look-x": 1,
          "--mo-look-y": 1,
        } as CSSProperties}
        showActivityProp={false}
      />}
      {turn ? <AgentAvatarRibbon key={`front-${turn}`} front /> : null}
    </span>
  );
}
