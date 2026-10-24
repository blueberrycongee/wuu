import type { TranslationKey } from "./i18n/resources/zh-CN";

// First-run choices are deliberately a subset of installed extensions. Changing
// this catalog changes the offer, not installation or extension lifecycle.
export const ONBOARDING_PLUGIN_ORDER = [
  "side", "ask-user", "todo", "goal", "automation", "automation-client", "subagent", "peers", "memory", "dream",
] as const;

export const RECOMMENDED_PLUGIN_IDS = new Set<string>(["side", "automation", "automation-client"]);

// Package names distinguish providers from clients that share an interface name.
// Only presentation metadata is used; no extension runtime is loaded here.
export const ONBOARDING_PLUGIN_MANIFESTS = Object.fromEntries(Object.values(import.meta.glob<{
  id: string; name: string; description: string; icon?: string;
}>("../../../internal/plugin/bundled/*/plugin.json", { eager: true, import: "default" }))
  .map(manifest => [manifest.id, manifest]));

export const PLUGIN_DESCRIPTION_KEYS: Readonly<Record<string, TranslationKey>> = {
  side: "onboarding.plugin.side",
  "ask-user": "onboarding.plugin.askUser",
  todo: "onboarding.plugin.todo",
  goal: "onboarding.plugin.goal",
  subagent: "onboarding.plugin.subagent",
  memory: "onboarding.plugin.memory",
  dream: "onboarding.plugin.dream",
  peers: "onboarding.plugin.peers",
};
