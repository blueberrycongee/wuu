import { translateCurrent } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";

export const PARTICIPANT_ROLES = [
  "general-purpose",
  "planner",
  "researcher",
  "worker",
  "reviewer",
  "qa",
  "debugger",
  "integrator",
  "verification",
] as const;

const PARTICIPANT_ROLE_KEYS: Record<(typeof PARTICIPANT_ROLES)[number], TranslationKey> = {
  "general-purpose": "participant.role.generalPurpose",
  planner: "participant.role.planner",
  researcher: "participant.role.researcher",
  worker: "participant.role.worker",
  reviewer: "participant.role.reviewer",
  qa: "participant.role.qa",
  debugger: "participant.role.debugger",
  integrator: "participant.role.integrator",
  verification: "participant.role.verification",
};

export function participantRoleLabel(role: string): string {
  const key = PARTICIPANT_ROLE_KEYS[role as keyof typeof PARTICIPANT_ROLE_KEYS];
  return key ? translateCurrent(key) : role;
}
