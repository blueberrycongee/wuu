import type { ExtensionInventoryRecord } from "../shared/protocol";
import { ONBOARDING_PLUGIN_ORDER, RECOMMENDED_PLUGIN_IDS } from "./onboardingCatalog";

// Only presentation metadata is used; preview never loads an extension runtime.
const manifests = Object.values(import.meta.glob<{ id: string; name: string; icon?: string }>(
  "../../../internal/plugin/bundled/*/plugin.json",
  { eager: true, import: "default" },
));

export const PREVIEW_PLUGINS: ExtensionInventoryRecord[] = ONBOARDING_PLUGIN_ORDER.flatMap((id) => {
  const manifest = manifests.find((item) => item.id === id);
  if (!manifest) return [];
  return [{
    id: `preview:${id}`,
    name: manifest.name,
    icon: manifest.icon ? { name: manifest.icon } : undefined,
    kind: "plugin",
    state: "active",
    enabled: RECOMMENDED_PLUGIN_IDS.has(id),
    fingerprint: `preview:${id}`,
    package_source: "bundled",
    provenance: { kind: "plugin", source: "bundled", scope: "bundled", official: true, plugin_id: id },
  }];
});

export const PREVIEW_WORKSPACES: import("../shared/protocol").RecentWorkspace[] = [
  { name: "website", path: "/Users/example/Projects/website", sources: ["codex", "claude"], sessionCount: 8, isRepository: true, lastUsedAt: new Date(Date.now() - 3600000).toISOString() },
  { name: "api-server", path: "/Users/example/Projects/api-server", sources: ["claude"], sessionCount: 5, isRepository: true, lastUsedAt: new Date(Date.now() - 86400000).toISOString() },
  { name: "notes", path: "/Users/example/Documents/notes", sources: ["codex"], sessionCount: 1, isRepository: false, lastUsedAt: new Date(Date.now() - 3 * 86400000).toISOString() },
];
