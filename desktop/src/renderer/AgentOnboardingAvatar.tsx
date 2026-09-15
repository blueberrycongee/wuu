import { useEffect, useState } from "react";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { prefersReducedMotion } from "./motion";

export const AGENT_FORMATION_MS = 3000;
export const AGENT_BUBBLE_DELAY_MS = 3400;

export function AgentOnboardingAvatar({ avatarKey, seed = "draft-agent" }: { avatarKey: string; seed?: string }): JSX.Element {
  const [gathering, setGathering] = useState(() => !prefersReducedMotion());
  useEffect(() => {
    const timer = window.setTimeout(() => setGathering(false), AGENT_FORMATION_MS);
    return () => window.clearTimeout(timer);
  }, []);
  return <AgentAvatarMark seed={seed} avatarKey={avatarKey} morph={gathering ? "gather" : "idle"} motion="expressive" />;
}
