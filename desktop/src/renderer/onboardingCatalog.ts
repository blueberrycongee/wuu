import type { TranslationKey } from "./i18n/resources/zh-CN";

// First-run choices are deliberately a subset of installed extensions. Changing
// this catalog changes the offer, not installation or extension lifecycle.
export const ONBOARDING_PLUGIN_ORDER = [
  "ask-user", "todo", "goal", "automation", "subagent", "peers", "memory", "dream",
] as const;

export const RECOMMENDED_PLUGIN_IDS = new Set<string>(["todo", "automation", "subagent"]);

export const PLUGIN_DESCRIPTION_KEYS: Readonly<Record<string, TranslationKey>> = {
  "ask-user": "onboarding.plugin.askUser",
  todo: "onboarding.plugin.todo",
  goal: "onboarding.plugin.goal",
  automation: "onboarding.plugin.automation",
  subagent: "onboarding.plugin.subagent",
  memory: "onboarding.plugin.memory",
  dream: "onboarding.plugin.dream",
  peers: "onboarding.plugin.peers",
};

export const ONBOARDING_ENGINES: readonly {
  id: string;
  label: string;
  readyDescription: TranslationKey;
  missingDescription: TranslationKey;
}[] = [
  { id: "wuu", label: "Wuu", readyDescription: "onboarding.engine.wuu", missingDescription: "onboarding.engine.wuu" },
  { id: "codex", label: "Codex", readyDescription: "onboarding.engine.codexReady", missingDescription: "onboarding.engine.codexMissing" },
  { id: "claude", label: "Claude Code", readyDescription: "onboarding.engine.claudeReady", missingDescription: "onboarding.engine.claudeMissing" },
];
