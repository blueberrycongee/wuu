import { useEffect, useId, useRef, useState, type CSSProperties, type JSX } from "react";
import { SHAPES } from "blobatar/blob";
import { AVATAR_HUES } from "./DefaultAvatar";
import { WuuMascot, WUU_MASCOT_ACCESSORIES, type WuuMascotAccessory } from "./WuuMascot";
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

function AgentAvatarAccent({ status }: { status: AgentAvatarStatus }): JSX.Element | null {
  if (status === "idle") return null;
  return <svg className="agent-avatar-accent" viewBox="0 0 100 100" aria-hidden="true">
    {status === "thinking" ? <g className="agent-avatar-thoughts"><circle cx="77" cy="17" r="2.5" /><circle cx="86" cy="10" r="3.2" /><circle cx="97" cy="6" r="3.8" /></g> : null}
    {status === "responding" ? <path d="M20 22C17 22 8 15 10 12C12 9 21 17 22 20Q23 23 20 22ZM28 13C25 13 23 1 26 0C30-1 32 12 28 13ZM11 34C8 35-2 31-1 28C0 25 12 29 13 31Q14 33 11 34Z" /> : null}
    {status === "sending" ? <path d="M82 18C94 19 101 27 104 37Q104 41 100 38C95 30 89 26 81 24Q77 21 82 18ZM88 7C99 10 106 17 110 27Q111 31 107 29C101 21 95 16 87 13Q84 10 88 7Z" /> : null}
    {status === "queued" ? <g><circle cx="8" cy="66" r="3.2" /><circle cx="-1" cy="70" r="2.6" /></g> : null}
    {status === "waiting" ? <path d="M18 22C13 22 6 16 8 12C11 9 20 17 21 20Q21 23 18 22ZM29 12C24 12 23 3 26-1Q29-3 30 1C32 6 32 12 29 12Z" /> : null}
    {status === "failed" ? <path d="M13 65C12 72 5 76 8 83Q10 87 5 86C-1 83 3 70 10 65Q13 62 13 65ZM87 65C88 72 95 76 92 83Q90 87 95 86C101 83 97 70 90 65Q87 62 87 65Z" /> : null}
    {status === "interrupted" ? <g>
      <path transform="translate(76 18) scale(.7)" d="M0 0H12V3L4 11H12V14H0V11L8 3H0Z" />
      <path transform="translate(86 5) scale(.9)" d="M0 0H12V3L4 11H12V14H0V11L8 3H0Z" />
      <path transform="translate(99 -10) scale(1.1)" d="M0 0H12V3L4 11H12V14H0V11L8 3H0Z" />
    </g> : null}
  </svg>;
}

const AGENT_TURN_MS = 1100;

/** Work begins with one turn. Work sub-states share it; pauses cancel it. */
function useAgentAvatarTurn(status: AgentAvatarStatus, enabled: boolean, signal: number): number {
  const active = status === "thinking" || status === "responding" || status === "sending";
  const previous = useRef({ active: false, signal });
  const sequence = useRef(0);
  const [turn, setTurn] = useState(0);
  useEffect(() => {
    const requested = (active && !previous.current.active) || signal !== previous.current.signal;
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

export function AgentAvatarMark({ seed, avatarKey, avatarImage, status = "idle", motion = "expressive", turnSignal = 0 }: {
  seed: string;
  avatarKey: string;
  avatarImage?: string;
  status?: AgentAvatarStatus;
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
        activity={status}
        idlePerspective={{ yaw: 0, pitch: 2, strength: 1 }}
        animate={active && motion === "expressive" ? "always" : "hover"}
        style={{
          "--agent-attention-delay": `${-phase}ms`,
          "--mo-look-x": 1,
          "--mo-look-y": 1,
        } as CSSProperties}
        showActivityProp={false}
      />}
      {avatarImage ? null : <AgentAvatarAccent status={status} />}
      {turn ? <AgentAvatarRibbon key={`front-${turn}`} front /> : null}
    </span>
  );
}
